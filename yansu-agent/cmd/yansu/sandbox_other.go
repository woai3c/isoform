//go:build !darwin

package main

const sandboxCapability = "unsupported"

func cliSandboxed() (bool, error) { return false, nil }
