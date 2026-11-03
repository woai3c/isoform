//go:build !darwin

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "yansu-pii-helper is only supported on macOS")
	os.Exit(1)
}
