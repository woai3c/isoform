//go:build darwin && !cgo

package cronsecret

import "fmt"

// A shared helper cannot provide an executable-specific Keychain identity.
// Darwin CLI builds must use cgo so Security.framework checks the CLI itself.
func keychainRead(_, _ string) (string, error) {
	return "", fmt.Errorf("%w: this macOS CLI requires a build with native Keychain support", ErrUnavailable)
}

func keychainWrite(_, _, _, _ string) error {
	return fmt.Errorf("%w: start the desktop app to save or migrate the Keychain credential", ErrUnavailable)
}
