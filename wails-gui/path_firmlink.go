package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// macOS mounts its system and user folders from the Data volume, so the same
// folder can be spelled /Users/me or /System/Volumes/Data/Users/me. The two
// spellings are linked by a firmlink, which filepath.EvalSymlinks does not
// follow. A string comparison of the two therefore misses a match, and a
// private folder named under the Data volume would slip past a check written
// against the plain spelling. stripDataVolumeFirmlink maps the Data-volume
// spelling back to the plain one, but only when the OS confirms both name the
// same folder.
const dataVolumeRoot = "/System/Volumes/Data"

// stripDataVolumeFirmlink returns p without the Data-volume prefix when the
// remainder names the same file or folder (os.SameFile on the nearest existing
// ancestors of both). Any other path comes back unchanged, so this is a no-op
// off macOS.
func stripDataVolumeFirmlink(p string) string {
	if runtime.GOOS != "darwin" || len(p) < len(dataVolumeRoot) ||
		!strings.EqualFold(p[:len(dataVolumeRoot)], dataVolumeRoot) ||
		(len(p) > len(dataVolumeRoot) && p[len(dataVolumeRoot)] != filepath.Separator) {
		return p
	}
	rest := p[len(dataVolumeRoot):]
	if rest == "" {
		rest = string(filepath.Separator)
	}
	a, aInfo := nearestExistingFile(p)
	b, bInfo := nearestExistingFile(rest)
	if a == "" || b == "" || aInfo == nil || bInfo == nil {
		return p
	}
	if !os.SameFile(aInfo, bInfo) {
		return p
	}
	return rest
}

// sameResolvedPath compares already resolved paths without mistaking aliases
// for different files. Case variants must identify the same file, since macOS
// can also use case-sensitive volumes. Keep the spelling for filesystem access.
func sameResolvedPath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	if (runtime.GOOS != "darwin" && runtime.GOOS != "windows") || !strings.EqualFold(a, b) {
		return false
	}
	aInfo, aErr := os.Stat(a)
	bInfo, bErr := os.Stat(b)
	return aErr == nil && bErr == nil && os.SameFile(aInfo, bInfo)
}

// nearestExistingFile returns the first existing ancestor of p (p itself when
// it exists) and its file info, or "" and nil when none is found.
func nearestExistingFile(p string) (string, os.FileInfo) {
	cur := filepath.Clean(p)
	for {
		if info, err := os.Stat(cur); err == nil {
			return cur, info
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", nil
		}
		cur = parent
	}
}

// resolvedExistingPath is filepath.EvalSymlinks followed by the firmlink
// strip. Use it for a path that must exist and that is compared with other
// canonical paths.
func resolvedExistingPath(p string) (string, error) {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	return stripDataVolumeFirmlink(resolved), nil
}
