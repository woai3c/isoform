//go:build !darwin

package tools

// RegisterTrustedYansuCLI is only needed for macOS Seatbelt enforcement.
func RegisterTrustedYansuCLI(installedPath, bundleSource string) error { return nil }

func trustedYansuCLIWriteHit(path string) bool { return false }
