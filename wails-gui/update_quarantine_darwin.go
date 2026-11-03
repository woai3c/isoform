package main

import (
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

const quarantineXattr = "com.apple.quarantine"

// stripQuarantineRecursive removes the com.apple.quarantine xattr from root
// and everything beneath it, without following symlinks.
//
// When the running app's executable carries a quarantine flag (e.g. the
// install was originally browser-downloaded, or a previous update left the
// xattr behind), the kernel propagates com.apple.quarantine onto every file
// this process creates — including a freshly extracted update bundle. macOS
// hard-rejects launching a bundle with such an unapproved quarantine
// ("Yansu.app is damaged and can't be opened"), valid signature and
// notarization notwithstanding. The strip must therefore complete, and be
// verified, before the new bundle is swapped into place.
//
// Returns the number of xattrs removed. A missing xattr or an entry that
// vanished mid-walk is not an error; anything else is, because installing a
// bundle that may still be quarantined would brick the app — the caller
// should abort and keep the current version running.
func stripQuarantineRecursive(root string) (int, error) {
	removed := 0
	err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		switch err := unix.Lremovexattr(path, quarantineXattr); err {
		case nil:
			removed++
			return nil
		case unix.ENOATTR, unix.ENOENT:
			return nil
		default:
			return fmt.Errorf("remove quarantine xattr from %s: %w", path, err)
		}
	})
	return removed, err
}

// startDetachedShell runs a shell script in its own session so it survives
// the caller's exit. The updater fires its relaunch script moments before
// os.Exit(0); without Setsid the script shares the dying app's process
// group and launchd's job teardown can kill it before `open -a` runs,
// silently losing the relaunch.
func startDetachedShell(script string) error {
	cmd := exec.Command("bash", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}
