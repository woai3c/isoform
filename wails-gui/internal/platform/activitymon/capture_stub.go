//go:build !darwin && !windows

package activitymon

// stubScreenCapture is a no-op ScreenCapture for non-darwin platforms.
type stubScreenCapture struct{}

// NewScreenCapture creates a no-op ScreenCapture on non-darwin platforms.
func NewScreenCapture() ScreenCapture {
	return &stubScreenCapture{}
}

func (c *stubScreenCapture) CaptureRaw() ([]byte, int, int, error) {
	return nil, 0, 0, nil
}

func (c *stubScreenCapture) EncodeJPEG(_ []byte, _, _, _ int) ([]byte, error) {
	return nil, nil
}

// FrontmostWindowInfo returns no window on unsupported platforms.
func FrontmostWindowInfo() FrontmostWindow {
	return FrontmostWindow{}
}

// CaptureWindowJPEG is a no-op on unsupported platforms.
func CaptureWindowJPEG(_ uint32, _ int) ([]byte, int, int) {
	return nil, 0, 0
}

// IsScreenLocked always returns false on non-darwin platforms.
func IsScreenLocked() bool {
	return false
}

// IsDisplayAsleep always returns false on non-darwin platforms.
func IsDisplayAsleep() bool {
	return false
}

// ScreenDimensions is a no-op on unsupported platforms.
func ScreenDimensions() (int, int) {
	return 0, 0
}

// SupportsWindowCapture reports whether per-window capture is available. It is
// false on non-darwin platforms, so callers treat a 0 window ID as "no per-window
// support" and fall back to full-screen capture instead of skipping the frame.
func SupportsWindowCapture() bool {
	return false
}
