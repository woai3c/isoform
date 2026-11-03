//go:build darwin && !cgo

package main

const sandboxCapability = "unsupported"

// Such binaries cannot register for a Seatbelt exception. Their normal CLI
// behavior remains available, with the shell's ordinary hard blocks in force.
func cliSandboxed() (bool, error) { return false, nil }
