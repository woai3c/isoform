//go:build !darwin

package shellenv

import (
	"log"
	"strings"
)

// ResolveStartupPATH ensures CLI tools are discoverable on non-macOS platforms.
// On Linux and Windows, GUI apps generally inherit the full PATH.
func ResolveStartupPATH() {
	if added := ensureCommandDirsInPATH("claude", "codex", "node"); len(added) > 0 {
		log.Printf("[env] appended command dirs to PATH: %s", strings.Join(added, ", "))
	}
}
