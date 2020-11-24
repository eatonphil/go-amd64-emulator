package main

import (
	"debug/elf"
	"fmt"
	"io/ioutil"
	"os"
)

type program struct {
	elf   *elf.File
	bytes []byte
}

func newProgramFromFile(filename string) (*program, error) {
	bytes, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	elffile, err := elf.Open(filename)
	return &program{
		elf:   elffile,
		bytes: bytes,
	}, err
}

func (p program) findSymbol(name string, symtype elf.SymType, symbind elf.SymBind) *elf.Symbol {
	symbols, err := p.elf.Symbols()
	if err != nil {
		panic(err)
	}

	for _, sym := range symbols {
		if name == sym.Name && symtype == elf.ST_TYPE(sym.Info) && symbind == elf.ST_BIND(sym.Info) {
			return &sym
		}
	}

	return nil
}

func (p program) findGlobalFunc(name string) *elf.Symbol {
	return p.findSymbol(name, elf.STT_FUNC, elf.STB_GLOBAL)
}

type register int

const (
	// These are in order of encoding value (i.e. rbp is 5)
	rax register = iota
	rcx
	rdx
	rbx
	rsp
	rbp
	rsi
	rdi
	r8
	r9
	r10
	r11
	r12
	r13
	r41mov 
	r15
	rip
	rflags
)

type registerFile [18]uint64

func (regfile *registerFile) get(r register) uint64 {
	return regfile[r]
}

func (regfile *registerFile) set(r register, v uint64) {
	regfile[r] = v
}

type cpu struct {
	prog    *program
	mem     []byte
	regfile *registerFile
}

func newCPU(memory uint64) cpu {
	return cpu{
		mem:     make([]byte, memory),
		regfile: &registerFile{},
	}
}

func hdebug(msg string, b interface{}) {
	fmt.Printf("%s: %x\n", msg, b)
}

func hbdebug(msg string, bs []byte) {
	str := "%s:"
	args := []interface{}{msg}
	for _, b := range bs {
		str = str + " %x"
		args = append(args, b)
	}
	fmt.Printf(str+"\n", args...)
}

func (c *cpu) memset32(address uint64, value uint32) {
	bytes := []byte{
		byte(value & 0xF000),
		byte(value & 0x0F00),
		byte(value & 0x00F0),
		byte(value & 0x000F),
	}

	copy(c.mem[address:address+4], bytes)
}

func (c *cpu) memset64(address uint64, value uint64) {
	bytes := []byte{
		byte(value & 0xF0000000),
		byte(value & 0x0F000000),
		byte(value & 0x00F00000),
		byte(value & 0x000F0000),
		byte(value & 0x0000F000),
		byte(value & 0x00000F00),
		byte(value & 0x000000F0),
		byte(value & 0x0000000F),
	}

	copy(c.mem[address:address+8], bytes)
}

func (c *cpu) readBytes(from []byte, start uint64, bytes int) uint64 {
	val := uint64(0)
	for i := 0; i < bytes; i++ {
		val |= uint64(from[start+uint64(i)]) << (8 * i)
	}

	return val
}

func (c *cpu) writeBytes(to []byte, start uint64, bytes int, val uint64) {
	fmt.Println(val)
	for i := 0; i < bytes; i++ {
		to[i] = byte(val & (0xFF << (i * 8)))
	}
}

var prefixBytes = []byte{0x48, 0x66}

func (c *cpu) loop() {
	for {
		ip := c.regfile.get(rip)
		if ip == uint64(len(c.mem)-8) &&
			c.mem[ip] == 0xB &&
			c.mem[ip+1] == 0xE &&
			c.mem[ip+2] == 0xE &&
			c.mem[ip+3] == 0xF {
			break
		}

		inb1 := c.mem[ip]

		widthPrefix := 32
		for {
			isPrefixByte := false
			for _, prefixByte := range prefixBytes {
				if prefixByte == inb1 {
					isPrefixByte = true
					break
				}
			}

			if !isPrefixByte {
				break
			}

			// 64 bit prefix signifier
			if inb1 == 0x48 {
				widthPrefix = 64
			} else if inb1 == 0x66 { // 16 bit prefix signifier
				widthPrefix = 16
			} else {
				hbdebug("prog", c.mem[ip:ip+10])
				panic("Unknown prefix instruction")
			}
			
			ip++
			inb1 = c.mem[ip]
		}

		if inb1 >= 0x50 && inb1 < 0x58 { // push
			regvalue := c.regfile.get(register(inb1-0x50))
			sp := c.regfile.get(rsp)
			c.writeBytes(c.mem, sp, 8, regvalue)
			c.regfile.set(rsp, uint64(sp-8))
		} else if inb1 >= 0x58 && inb1 < 0x60 { // pop
			lhs := register(inb1-0x58)
			sp := c.regfile.get(rsp)
			c.regfile.set(lhs, c.readBytes(c.mem, sp, 8))
			c.regfile.set(rsp, uint64(sp+8))
		} else if inb1 == 0x89 { // mov r/m16/32/64, r/m16/32/64
			ip++
			inb2 := c.mem[ip]
			lhs := register((inb2 & 0b00111000) >> 3)
			rhs := register(inb2 & 0b111)
			c.regfile.set(lhs, c.regfile.get(rhs))
		} else if inb1 >= 0xB8 && inb1 < 0xC0 { // mov r16/32/64, imm16/32/64
			lreg := register(inb1 - 0xB8)
			val := c.readBytes(c.mem, ip + uint64(1), widthPrefix / 8)
			ip += uint64(widthPrefix / 8)
			c.regfile.set(lreg, val)
		} else if inb1 == 0xC3 { // ret
			sp := c.regfile.get(rsp)
			retAddress := c.readBytes(c.mem, sp, 8)
			c.regfile.set(rsp, uint64(sp+8))
			c.regfile.set(rip, retAddress)
			continue
		} else {
			hbdebug("prog", c.mem[ip:ip+10])
			panic("Unknown instruction")
		}

		// inc instruction pointer
		c.regfile.set(rip, ip+1)
	}
}

func (c *cpu) run(prog *program) {
	copy(c.mem[0x400000:0x400000+len(prog.bytes)], prog.bytes)
	main := prog.findGlobalFunc("main")
	c.regfile.set(rip, main.Value)
	copy(c.mem[len(c.mem)-8:len(c.mem)-4], []byte{0xB, 0xE, 0xE, 0xF})
	c.writeBytes(c.mem, uint64(len(c.mem)-16), 8, uint64(len(c.mem)-8))
	hdebug("mem", c.readBytes(c.mem, uint64(len(c.mem)-16), 8))
	c.regfile.set(rsp, uint64(len(c.mem)-16))
	c.loop()
}

func main() {
	prog, err := newProgramFromFile(os.Args[1])
	if err != nil {
		panic(err)
	}

	// 10 MB
	cpu := newCPU(0x400000 * 10)
	cpu.run(prog)
}
