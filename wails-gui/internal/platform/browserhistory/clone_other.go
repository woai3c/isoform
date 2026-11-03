//go:build !darwin

package browserhistory

import "errors"

// cloneFile is macOS only; elsewhere the file is copied.
func cloneFile(_, _ string) error {
	return errors.ErrUnsupported
}
