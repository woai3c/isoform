//go:build !darwin

package main

import "errors"

// readAppBundleInfo is not supported on non-macOS platforms.
func readAppBundleInfo(_ string) (string, string, error) {
	return "", "", errors.New("app bundle detection is only supported on macOS")
}

// SystemApp represents an application found on the system.
type SystemApp struct {
	Name        string `json:"name"`
	EnglishName string `json:"englishName,omitempty"`
	BundleID    string `json:"bundleId"`
	Icon        string `json:"icon"`
}

// listSystemApps returns nil on non-macOS platforms.
func listSystemApps() []SystemApp { return nil }

// appIconsForBundleIDs returns no icons on non-macOS platforms; the frontend
// falls back to its brand-glyph/monogram chain.
func appIconsForBundleIDs(_ []string) map[string]string { return map[string]string{} }
