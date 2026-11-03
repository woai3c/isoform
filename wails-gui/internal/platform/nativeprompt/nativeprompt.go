package nativeprompt

import "testing"

// Allowed reports whether native APIs that may display a permission prompt may
// run. Test binaries always fail closed; production keeps its existing behavior.
func Allowed() bool {
	return !testing.Testing()
}
