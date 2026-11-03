//go:build !darwin && !windows && !linux

package browser

// FindChromium returns "" on unsupported platforms.
func FindChromium() string {
	return ""
}
