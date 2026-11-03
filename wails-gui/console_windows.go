//go:build windows

package main

// initHiddenConsole is a no-op. All child processes are now spawned with
// CREATE_NO_WINDOW via cmdutil, so there is no need to pre-allocate a
// hidden console for inheritance. This completely eliminates the brief
// console window flash that occurred with AllocConsole or helper-process
// approaches on Windows.
func initHiddenConsole() {}
