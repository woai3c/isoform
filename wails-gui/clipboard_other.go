//go:build !darwin

package main

// NativeClipboardSetText is a darwin-only shortcut. On other platforms the
// frontend falls back to the wails runtime ClipboardSetText, so this stub
// just errors out so the caller knows to use the other path.
func (a *App) NativeClipboardSetText(_ string) error {
	return errNativeClipboardUnavailable
}

var errNativeClipboardUnavailable = &clipboardUnavailableError{}

type clipboardUnavailableError struct{}

func (*clipboardUnavailableError) Error() string {
	return "native clipboard bridge not available on this platform"
}
