//go:build !darwin && !linux

package models

import "os"

// Without a reliable kernel change time, always verify the pinned hash.
func fileChangeStamp(os.FileInfo) string { return "" }
