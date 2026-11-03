//go:build darwin

package main

// This file carries ONLY the //export callback. cgo compiles a preamble
// that contains //export functions into multiple translation units, so the
// ObjC @implementation blocks (which emit external class symbols) must NOT
// live in a file that also has //export — otherwise the classes link twice
// ("duplicate symbol"). spotlight_panel_darwin.go keeps the ObjC code (no
// //export); this file has an empty preamble.
//
// NOTE: the blank line below is required — a comment immediately preceding
// `import "C"` is treated as the C preamble.

import "C"

// goSpotlightDisarmDismiss is invoked from the panel whenever it leaves the
// "shown and awaiting first input" state — the user engaged it (became key)
// or it hid (any path). It tells the main app to disarm dismiss-on-key.
//
//export goSpotlightDisarmDismiss
func goSpotlightDisarmDismiss() {
	reportSpotlightDisarmDismiss()
}
