//go:build !darwin

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "yansu-ocr-helper is only supported on macOS")
	os.Exit(1)
}
