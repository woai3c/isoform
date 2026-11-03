//go:build linux || darwin

package toolserver

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

type ledgerRetention struct {
	maxAge           time.Duration
	inspectionWindow time.Duration
	maxCompleted     int
}

// Recent ledgers always remain inspectable, even if a busy day exceeds the
// count target. Older completed history is limited to 1,000 runs and 30 days.
var defaultLedgerRetention = ledgerRetention{maxAge: 30 * 24 * time.Hour, inspectionWindow: 24 * time.Hour, maxCompleted: 1000}

// The OS releases this lease after a crash. Retention therefore excludes
// active runs across desktop processes without leaving stale PID markers.
type runLease struct{ file *os.File }

func lockRunDirectory(dir string) (*runLease, error) {
	return lockRunDirectoryBeforeLock(dir, nil)
}

// beforeLock is a deterministic scheduling seam for the lease/pruner race.
func lockRunDirectoryBeforeLock(dir string, beforeLock func()) (*runLease, error) {
	directory, err := openRunRoot(dir)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	// Before publication a pruner sees either an empty directory or this
	// unknown private filename. Both are excluded from retention candidates.
	privateName := ".run-lock-" + uuid.NewString()
	fd, err := unix.Openat(int(directory.Fd()), privateName, unix.O_CREAT|unix.O_EXCL|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	defer unix.Unlinkat(int(directory.Fd()), privateName, 0)
	f := os.NewFile(uintptr(fd), "run.lock")
	if beforeLock != nil {
		beforeLock()
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	// Link publishes the already-locked inode atomically, without replacing
	// an existing lease. Keep the lock held while dropping the private name.
	if err := unix.Linkat(int(directory.Fd()), privateName, int(directory.Fd()), "run.lock", 0); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := unix.Unlinkat(int(directory.Fd()), privateName, 0); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &runLease{file: f}, nil
}

func (l *runLease) close() error {
	if l == nil || l.file == nil {
		return nil
	}
	// A long-running task gets its inspection window from completion, even
	// when its last action was long ago. Update through the leased file fd.
	now := unix.NsecToTimeval(time.Now().UnixNano())
	touchErr := unix.Futimes(int(l.file.Fd()), []unix.Timeval{now, now})
	closeErr := l.file.Close()
	l.file = nil
	return errors.Join(touchErr, closeErr)
}

type retainedRun struct {
	name     string
	modified time.Time
}

type openedRun struct {
	dir      *os.File
	lease    *os.File
	files    []string
	modified time.Time
}

func (r *openedRun) close() {
	if r.lease != nil {
		_ = r.lease.Close()
	}
	_ = r.dir.Close()
}

func openRunRoot(root string) (*os.File, error) {
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), root), nil
}

// inspectRetainedRun holds an inactive run's lease while inspecting its
// contents. Unknown files, symlinks and pre-lease runs with credentials are
// preserved; only the files this server owns can ever become prune targets.
func inspectRetainedRun(root *os.File, name string) (*openedRun, error) {
	fd, err := unix.Openat(int(root.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil
	}
	r := &openedRun{dir: os.NewFile(uintptr(fd), name)}
	keep := false
	defer func() {
		if !keep {
			r.close()
		}
	}()
	lockFD, err := unix.Openat(fd, "run.lock", unix.O_RDWR|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err == nil {
		r.lease = os.NewFile(uintptr(lockFD), "run.lock")
		if err := unix.Flock(lockFD, unix.LOCK_EX|unix.LOCK_NB); err != nil {
			return nil, nil
		}
	} else if !errors.Is(err, unix.ENOENT) {
		return nil, nil
	}
	// Older releases left only ledger.jsonl after a successful Close.
	info, err := r.dir.Stat()
	if err != nil {
		return nil, err
	}
	r.modified = info.ModTime()
	entries, err := r.dir.ReadDir(8)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) == 0 || len(entries) > 7 {
		return nil, nil
	}
	ledgerFound := false
	for _, entry := range entries {
		var stat unix.Stat_t
		if err := unix.Fstatat(fd, entry.Name(), &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return nil, nil
		}
		if stat.Uid != uint32(os.Getuid()) {
			return nil, nil
		}
		kind := uint32(stat.Mode) & unix.S_IFMT
		switch entry.Name() {
		case "ledger.jsonl":
			ledgerFound = true
			if kind != unix.S_IFREG {
				return nil, nil
			}
		case "run.lock", "token", "ticket.json", "mcp.json", "claude-mcp.json":
			if r.lease == nil || kind != unix.S_IFREG {
				return nil, nil
			}
		case "tools.sock":
			if r.lease == nil || kind != unix.S_IFSOCK {
				return nil, nil
			}
		default:
			return nil, nil
		}
		modified := time.Unix(stat.Mtim.Sec, stat.Mtim.Nsec)
		if modified.After(r.modified) {
			r.modified = modified
		}
		r.files = append(r.files, entry.Name())
	}
	if !ledgerFound && r.lease == nil {
		return nil, nil
	}
	keep = true
	return r, nil
}

func pruneRunDirectories(root string, now time.Time, policy ledgerRetention) error {
	dir, err := openRunRoot(root)
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	var runs []retainedRun
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "assistant-") {
			continue
		}
		run, err := inspectRetainedRun(dir, entry.Name())
		if err != nil {
			return err
		}
		if run == nil {
			continue
		}
		runs = append(runs, retainedRun{name: entry.Name(), modified: run.modified})
		run.close()
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].modified.Equal(runs[j].modified) {
			return runs[i].name < runs[j].name
		}
		return runs[i].modified.After(runs[j].modified)
	})
	for i, candidate := range runs {
		if now.Sub(candidate.modified) < policy.inspectionWindow {
			continue
		}
		if now.Sub(candidate.modified) < policy.maxAge && i < policy.maxCompleted {
			continue
		}
		// Recheck the lease and timestamps immediately before deletion. A
		// concurrent Close may have refreshed the inspection window.
		run, err := inspectRetainedRun(dir, candidate.name)
		if err != nil {
			return err
		}
		if run == nil {
			continue
		}
		if run.modified.Equal(candidate.modified) {
			err = removeRunFiles(dir, candidate.name, run.dir, run.files)
		}
		run.close()
		if err != nil && !errors.Is(err, unix.ENOENT) {
			return err
		}
	}
	return nil
}

func removeRunFiles(root *os.File, name string, dir *os.File, files []string) error {
	// Directory-relative unlinks never follow a symlink, and AT_REMOVEDIR
	// refuses a replacement symlink or a directory containing unknown files.
	for _, file := range files {
		if file == "run.lock" {
			continue
		}
		if err := unix.Unlinkat(int(dir.Fd()), file, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			return err
		}
	}
	// Keep the lease discoverable until every other owned file is gone, so
	// another pruner cannot treat a partially cleaned run as a legacy run.
	if err := unix.Unlinkat(int(dir.Fd()), "run.lock", 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return err
	}
	return unix.Unlinkat(int(root.Fd()), name, unix.AT_REMOVEDIR)
}

// Failed setup owns only these fixed filenames. Never recursively delete a
// run directory, even on the failure path.
func discardRunDirectory(path string) {
	root, err := openRunRoot(filepath.Dir(path))
	if err != nil {
		return
	}
	defer root.Close()
	fd, err := unix.Openat(int(root.Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return
	}
	dir := os.NewFile(uintptr(fd), path)
	defer dir.Close()
	_ = removeRunFiles(root, filepath.Base(path), dir, []string{"tools.sock", "token", "ticket.json", "mcp.json", "claude-mcp.json", "ledger.jsonl", "run.lock"})
}
