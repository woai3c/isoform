//go:build darwin

package shellenv

import (
	"log"
	"os"
	"strings"
)

// ResolveStartupPATH resolves PATH from the user's login shell on macOS.
// macOS app bundles launched from Finder inherit a minimal PATH.
func ResolveStartupPATH() {
	if resolved := resolveUserPATH(); resolved != "" {
		if err := os.Setenv("PATH", resolved); err != nil {
			log.Printf("[env] failed to set PATH from login shell: %v", err)
		}
	} else {
		log.Printf("[env] failed to resolve PATH from login shell; using inherited PATH")
	}

	if added := ensureCommandDirsInPATH("claude", "codex", "node"); len(added) > 0 {
		log.Printf("[env] appended command dirs to PATH: %s", strings.Join(added, ", "))
	}
}
