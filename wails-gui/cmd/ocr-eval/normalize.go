package main

import "wails-gui/internal/platform/ocr"

func fullwidthToHalfwidth(s string) string {
	return ocr.NormalizeFullwidth(s)
}
