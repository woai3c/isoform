package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ownBundleOnce + ownBundle cache the lowercase exe basename of the
// currently running process (the FrontmostAppBundleID-equivalent for
// "us"). Used by isOwnProcessFocused so capture skips the GDI attempt
// on our own WebView2 window — the GDI BitBlt + GetDIBits + PrintWindow
// path can't read DComp-rendered surfaces and bursts of failures only
// pollute the activity capture pipeline.
//
// Lazily resolved because os.Executable() can take a few ms on first
// call (Windows resolves the image path via PEB) and we don't want
// to pay that on package init.
var (
	ownBundleOnce sync.Once
	ownBundle     string
)

func ownProcessBundleID() string {
	ownBundleOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil || exe == "" {
			return
		}
		ownBundle = strings.ToLower(filepath.Base(exe))
	})
	return ownBundle
}

// isOwnProcessFocused reports whether the given foreground bundleID
// belongs to the running wails-gui process. Matched on lowercase exe
// basename (the same identifier FrontmostAppBundleID returns on
// Windows), so e.g. "Yansu.exe" running as "yansu.exe" matches.
//
// Empty bundleID is treated as NOT-self so callers don't accidentally
// suppress real captures when the foreground-app probe failed.
func isOwnProcessFocused(bundleID string) bool {
	if bundleID == "" {
		return false
	}
	own := ownProcessBundleID()
	if own == "" {
		return false
	}
	return strings.EqualFold(bundleID, own)
}
