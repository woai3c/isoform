//go:build windows

package main

import "os"

func installCrashSignals(f *os.File) {
	// SetCrashOutput retains a duplicate; Unix fatal signals do not apply.
	_ = f.Close()
}
