//go:build !darwin

package ocr

// PrewarmHelper is only needed for the macOS Vision helper.
func PrewarmHelper() {}

// CloseHelper is only needed for the macOS Vision helper.
func CloseHelper() {}
