//go:build darwin

package browserhistory

import "golang.org/x/sys/unix"

// cloneFile makes dst an APFS clone of src: the two share their blocks until
// one changes, so nothing is written, and the clone is a consistent snapshot
// of src even while the browser writes to it. It fails off APFS.
func cloneFile(src, dst string) error {
	return unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW)
}
