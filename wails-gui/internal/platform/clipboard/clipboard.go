// Package clipboard writes text to the system clipboard and, on macOS,
// watches what the user copies: the pasteboard's change counter is polled
// once a second, and each new copy is classified, filtered (concealed,
// transient and auto-generated pasteboard types, password managers and the
// apps the user protects) and reduced to a scrubbed preview before anything
// is kept. Platform-specific implementations live in build-tagged files.
package clipboard
