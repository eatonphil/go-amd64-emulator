# go-amd64-emulator

Dependencies:
* Go
* Linux
* GCC

## Example

```bash
$ cat test/simple.c
int main() {
  return 254;
}
$ go build
$ ./go-amd64-emulator a.out && echo $?
254
```
