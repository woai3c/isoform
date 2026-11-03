package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	agenttools "yansu-agent/agent/tools"
)

const agentWorkspaceMigrationMarker = ".isoform-legacy-migration-v1"

var agentWorkspaceMigrationMu sync.Mutex

// legacyAgentWorkspacePath uses the same data-root resolution as the retired
// workspace functions. It does not create, migrate or read the workspace.
func legacyAgentWorkspacePath(kind, id string) (string, error) {
	switch kind {
	case "chats", "automations":
	default:
		return "", fmt.Errorf("invalid workspace kind")
	}
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return "", fmt.Errorf("invalid workspace ID")
	}
	dataRoot, err := flavorDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataRoot, kind, id), nil
}

// migrateLegacyAgentWorkspace merges only this session/job's ordinary files.
// Destination files win conflicts; legacy conflicts and unsafe entries stay
// untouched. A completed move leaves compatibility links so historical paths
// in transcripts and deliverable records continue resolving to the same bytes.
func migrateLegacyAgentWorkspace(kind, id, destination string) error {
	agentWorkspaceMigrationMu.Lock()
	defer agentWorkspaceMigrationMu.Unlock()

	if err := validateCronProjectDir(destination); err != nil {
		return err
	}
	dst, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer dst.Close()
	if info, err := dst.Lstat(agentWorkspaceMigrationMarker); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("workspace migration marker is not a regular file")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	legacy, err := legacyAgentWorkspacePath(kind, id)
	if err != nil {
		return err
	}
	dataRoot := filepath.Dir(filepath.Dir(legacy))
	if !sameResolvedPath(resolvedFolder(legacy), filepath.Join(resolvedFolder(dataRoot), kind, id)) {
		return fmt.Errorf("legacy workspace is an alias: %s", legacy)
	}
	info, err := os.Lstat(legacy)
	switch true {
	case os.IsNotExist(err):
		// Mark a new workspace as well; future writes to the retired path
		// must not be imported silently on a later launch. A broken ancestor
		// link also reports not-exist, but cannot prove the source is absent.
		if err := confirmLegacyWorkspaceAbsent(legacy); err != nil {
			return err
		}
	case err != nil:
		return err
	case !info.IsDir():
		return fmt.Errorf("legacy workspace is not a directory: %s", legacy)
	default:
		src, err := os.OpenRoot(legacy)
		if err != nil {
			return err
		}
		defer src.Close()
		openedInfo, err := src.Stat(".")
		if err != nil || !os.SameFile(info, openedInfo) {
			return fmt.Errorf("legacy workspace changed during migration")
		}
		if err := mergeLegacyAgentDirectory(src, dst, ".", destination, dataRoot); err != nil {
			return err
		}
	}
	marker, err := dst.OpenFile(agentWorkspaceMigrationMarker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := marker.WriteString("1\n")
	closeErr := marker.Close()
	if writeErr != nil {
		_ = dst.Remove(agentWorkspaceMigrationMarker)
		return writeErr
	}
	return closeErr
}

// Confirm that not-exist names a missing suffix below an existing directory,
// rather than an unresolved or unreadable source. Only the ancestor chain is
// inspected; failed sources remain retryable without a completion marker.
func confirmLegacyWorkspaceAbsent(legacy string) error {
	for parent := filepath.Dir(legacy); ; parent = filepath.Dir(parent) {
		info, err := os.Lstat(parent)
		if os.IsNotExist(err) && filepath.Dir(parent) != parent {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			info, err = os.Stat(parent)
			if err != nil {
				return err
			}
		}
		if !info.IsDir() {
			return fmt.Errorf("legacy workspace parent is not a directory: %s", parent)
		}
		return nil
	}
}

func mergeLegacyAgentDirectory(src, dst *os.Root, relative, destination, dataRoot string) error {
	dir, err := src.Open(relative)
	if err != nil {
		return err
	}
	entries, err := dir.ReadDir(-1)
	_ = dir.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := filepath.Join(relative, entry.Name())
		if legacyWorkspaceEntryProtected(src.Name(), name, dataRoot) {
			log.Printf("[workspace-migration] left protected entry %s in %s", name, src.Name())
			continue
		}
		info, err := src.Lstat(name)
		if err != nil {
			return err
		}
		switch true {
		case info.IsDir():
			existing, statErr := dst.Lstat(name)
			if statErr == nil && !existing.IsDir() {
				log.Printf("[workspace-migration] left conflicting directory %s in %s", name, src.Name())
				continue
			}
			if statErr != nil && !os.IsNotExist(statErr) {
				return statErr
			}
			if os.IsNotExist(statErr) {
				if err := dst.Mkdir(name, info.Mode().Perm()); err != nil {
					return err
				}
			}
			if err := mergeLegacyAgentDirectory(src, dst, name, destination, dataRoot); err != nil {
				return err
			}
		case info.Mode().IsRegular() && fileLinkCount(info) == 1 && name != agentWorkspaceMigrationMarker:
			if err := moveLegacyAgentFile(src, dst, name, destination, info); err != nil {
				return err
			}
		default:
			log.Printf("[workspace-migration] left link or special entry %s in %s", name, src.Name())
		}
	}
	return nil
}

func legacyWorkspaceEntryProtected(sourceRoot, relative, dataRoot string) bool {
	// Moving private state out of a data root would remove its protection.
	// Apply the shared rules both to the real source and its workspace-relative
	// layout, so nested media/memory folders retain the data-root restrictions.
	for _, path := range []string{filepath.Join(sourceRoot, relative), filepath.Join(dataRoot, relative)} {
		if agenttools.CheckProtectedPaths("Write", map[string]any{"path": path}, sourceRoot) != nil {
			return true
		}
		// Bash also protects memory and knowledge that dedicated file tools
		// may read; migration must not make those accessible to a shell.
		if agenttools.CheckProtectedPaths("Bash", map[string]any{"command": "cat " + shellQuotePOSIX(path)}, sourceRoot) != nil {
			return true
		}
	}
	return false
}

func moveLegacyAgentFile(src, dst *os.Root, name, destination string, before os.FileInfo) error {
	in, err := src.Open(name)
	if err != nil {
		return err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil || !os.SameFile(before, opened) || !opened.Mode().IsRegular() || fileLinkCount(opened) != 1 {
		return fmt.Errorf("legacy file %s changed during migration", name)
	}
	out, err := dst.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, before.Mode().Perm())
	if os.IsExist(err) {
		log.Printf("[workspace-migration] kept existing destination and legacy conflict %s", name)
		return nil
	}
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	if copyErr == nil {
		copyErr = out.Sync()
	}
	closeErr := out.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	after, statErr := src.Lstat(name)
	if copyErr != nil || statErr != nil || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		_ = dst.Remove(name)
		if copyErr != nil {
			return fmt.Errorf("copy legacy file %s: %w", name, copyErr)
		}
		return fmt.Errorf("legacy file %s changed while copying", name)
	}
	// Install the compatibility link before atomically replacing the old
	// regular file. If links are unavailable, both complete copies survive.
	link := ".isoform-migration-link-" + uuid.NewString()
	if err := src.Symlink(filepath.Join(destination, name), link); err != nil {
		log.Printf("[workspace-migration] retained legacy copy of %s: %v", name, err)
		return nil
	}
	defer src.Remove(link)
	if err := src.Rename(link, name); err != nil {
		return err
	}
	return nil
}
