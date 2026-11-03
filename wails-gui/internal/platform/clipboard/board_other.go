//go:build !darwin

package clipboard

// SystemBoard has nothing to watch outside macOS.
type SystemBoard struct{}

// Supported reports whether this platform has a pasteboard to watch.
func Supported() bool { return false }

func (SystemBoard) ChangeCount() int                                  { return 0 }
func (SystemBoard) Types() []string                                   { return nil }
func (SystemBoard) ContentReadable() bool                             { return false }
func (SystemBoard) ReadText(maxUnits int) (string, int, string, bool) { return "", 0, "", false }

// FrontApp has no frontmost app to report outside macOS.
func FrontApp() App { return App{} }

// SelfBundleID has no bundle outside macOS.
func SelfBundleID() string { return "" }

// NoteOwnWrite has nothing to note outside macOS.
func NoteOwnWrite() {}
