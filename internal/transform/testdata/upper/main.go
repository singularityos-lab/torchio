// Command upper is a trivial WASI transform module used to test Torchio's
// wazero runner: it reads the whole document from stdin and writes it back
// uppercased to stdout. Build it with:
//
//	GOOS=wasip1 GOARCH=wasm go build -o ../upper.wasm .
//
// The compiled upper.wasm is committed so tests need no toolchain at run time.
package main

import (
	"bytes"
	"io"
	"os"
)

func main() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(1)
	}
	if _, err := os.Stdout.Write(bytes.ToUpper(data)); err != nil {
		os.Exit(1)
	}
}
