//go:build !bindings

package main

// isWailsBindingsBuild reports whether this binary was compiled with the
// `bindings` build tag, which Wails sets via `go build -tags bindings -o
// wailsbindings` during binding generation. See
// $GOMODCACHE/github.com/wailsapp/wails/v2@v2.11.0/pkg/commands/bindings/bindings.go:45.
//
// The flag exists so main() can skip runtime-side state (single-instance
// lock, activity monitor, background listening) when invoked purely for
// type-introspection — those would otherwise fail or deadlock when Yansu
// is already running on the same machine.
const isWailsBindingsBuild = false
