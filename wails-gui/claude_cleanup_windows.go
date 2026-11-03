//go:build windows

package main

// Workers are tracked on every platform (isoformClaudeWorkers); reaping stays
// unix-only, as before.
func cleanupOrphanedClaudeSessionWorkers() {}
