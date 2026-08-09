package main

import (
	"os"

	"github.com/ballastworks/xs/types/orduuid7"
)

func main() {
	var buf [33]byte

	orduuid7.New().AppendText(buf[:0])
	buf[32] = '\n'

	_, err := os.Stdout.Write(buf[:])
	if err != nil {
		panic(err)
	}
}
