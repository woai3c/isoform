package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	isoformSkillFile       = "SKILL.md"
	isoformSkillStampStart = "\n<!-- managed-by-isoform:assistant-skill "
	isoformSkillStampEnd   = " -->\n"
	isoformSkillMaxBytes   = 1024 * 1024

	// isoformSkillConsentDir was the shared consent store for the installed
	// skill. The feature is gone, so the cleanup removes Isoform's own files
	// from it and the directory itself when nothing else is inside.
	isoformSkillConsentDir  = ".isoform-assistant-skills"
	isoformSkillConsentFile = "consent.json"
	isoformSkillConsentLock = "consent.lock"
)

var isoformSkillCleanupMu sync.Mutex

type isoformSkillStamp struct {
	Format  int    `json:"format"`
	Version string `json:"version"`
	SHA256  string `json:"sha256"`
}

// Isoform no longer installs its skill into the user's own Claude Code or
// Codex: every run that needs the instructions carries them for that run only
// (isoformskill.Prompt). removeInstalledIsoformSkills deletes, at startup and
// only ever once in practice, the copies older versions installed — identified
// by the managed-by-isoform stamp — plus Isoform's own consent store. Anything
// else stays: an unstamped or locally edited SKILL.md, extra files in the
// skill directory, and every other directory in the user's home.
func removeInstalledIsoformSkills(home string) (copies int, consentStore bool, err error) {
	isoformSkillCleanupMu.Lock()
	defer isoformSkillCleanupMu.Unlock()

	root, err := openIsoformSkillHome(home)
	if err != nil {
		return 0, false, err
	}
	defer root.Close()
	var failures []error
	for _, assistant := range []string{".claude", ".codex"} {
		gone, err := removeAssistantIsoformSkill(root, assistant)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s/skills/isoform: %w", assistant, err))
		}
		if gone {
			copies++
		}
	}
	storeGone, err := removeIsoformSkillConsentStore(root)
	if err != nil {
		failures = append(failures, fmt.Errorf("%s: %w", isoformSkillConsentDir, err))
	}
	return copies, storeGone, errors.Join(failures...)
}

// isoformSkillCleanupMarkerName records in Isoform's own data dir that the
// one-time removal already ran, so later starts do not walk the user's
// assistant folders at all.
const isoformSkillCleanupMarkerName = "assistant-skill-cleanup.done"

// removeInstalledIsoformSkillsLogged runs the cleanup once: the marker in
// Isoform's data dir ends later starts before any walk. It logs one line when
// it removed anything (or could not finish). A start that found nothing to
// remove logs nothing; a partial failure is not marked and retries on the
// next start.
func removeInstalledIsoformSkillsLogged() {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	dataDir, err := flavorDataDir()
	if err != nil {
		return
	}
	marker := filepath.Join(dataDir, isoformSkillCleanupMarkerName)
	if _, err := os.Stat(marker); err == nil {
		return
	}
	copies, consentStore, err := removeInstalledIsoformSkills(home)
	if err != nil {
		log.Printf("[isoform-skill] removed %d Isoform-installed assistant skill copies (consent store removed: %v); cleanup incomplete: %v", copies, consentStore, err)
		return
	}
	if copies > 0 || consentStore {
		log.Printf("[isoform-skill] removed %d Isoform-installed assistant skill copies from Claude Code/Codex (consent store removed: %v); runs carry the skill per run", copies, consentStore)
	}
	// Best effort: a marker that fails to write costs one more walk next
	// start, which finds nothing and logs nothing.
	if mkdirErr := os.MkdirAll(dataDir, 0o700); mkdirErr == nil {
		_ = os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
	}
}

// removeAssistantIsoformSkill deletes Isoform's stamped SKILL.md from one
// assistant's skills directory, and the isoform directory itself when nothing
// else is inside. It reports whether the copy was removed.
func removeAssistantIsoformSkill(home *os.Root, assistant string) (bool, error) {
	parent := home
	for _, name := range []string{assistant, "skills"} {
		next, err := openIsoformSkillDirectory(parent, name)
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		defer next.Close()
		parent = next
	}
	dir, err := openIsoformSkillDirectory(parent, "isoform")
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer dir.Close()

	existing, before, err := readIsoformSkillFile(dir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !validIsoformSkillStamp(existing) {
		// Not Isoform's copy (or the user edited it): never touch it.
		return false, nil
	}
	if err := verifyIsoformSkillUnchanged(dir, existing, before); err != nil {
		return false, err
	}
	if err := dir.Remove(isoformSkillFile); err != nil {
		return false, err
	}
	// Remove only an empty skill directory. Extra user files and parent
	// assistant directories always survive.
	entries, err := dir.Open(".")
	if err != nil {
		return true, err
	}
	names, readErr := entries.Readdirnames(1)
	_ = entries.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return true, readErr
	}
	if len(names) == 0 {
		// On platforms that cannot remove an open directory, close our
		// handle first; Remove never follows a substituted symlink.
		_ = dir.Close()
		return true, parent.Remove("isoform")
	}
	return true, nil
}

// removeIsoformSkillConsentStore deletes Isoform's own files from the old
// shared consent directory, and the directory itself when left empty. Files
// Isoform does not know keep the directory in place.
func removeIsoformSkillConsentStore(home *os.Root) (bool, error) {
	dir, err := openIsoformSkillDirectory(home, isoformSkillConsentDir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer dir.Close()
	removed := false
	for _, name := range []string{isoformSkillConsentFile, isoformSkillConsentLock} {
		if err := dir.Remove(name); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, err
		}
		removed = true
	}
	entries, err := dir.Open(".")
	if err != nil {
		return removed, err
	}
	names, readErr := entries.Readdirnames(1)
	_ = entries.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return removed, readErr
	}
	if len(names) == 0 {
		_ = dir.Close()
		if err := home.Remove(isoformSkillConsentDir); err != nil {
			return removed, err
		}
		return true, nil
	}
	return removed, nil
}

func openIsoformSkillHome(home string) (*os.Root, error) {
	if !filepath.IsAbs(home) {
		return nil, fmt.Errorf("Isoform skill home must be an absolute directory")
	}
	before, err := os.Lstat(home)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() {
		return nil, fmt.Errorf("Isoform skill home must not be a link or non-directory")
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		_ = root.Close()
		return nil, fmt.Errorf("Isoform skill home changed while opening")
	}
	return root, nil
}

// Open each directory separately and compare the opened inode. Root confines
// all later operations even if another process replaces an ancestor path.
// Nothing is ever created: the cleanup only descends into what exists.
func openIsoformSkillDirectory(parent *os.Root, name string) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s must not be a link or non-directory", name)
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = root.Close()
		return nil, fmt.Errorf("%s changed while opening", name)
	}
	return root, nil
}

func readIsoformSkillFile(dir *os.Root) ([]byte, os.FileInfo, error) {
	before, err := dir.Lstat(isoformSkillFile)
	if err != nil {
		return nil, nil, err
	}
	if !before.Mode().IsRegular() || fileLinkCount(before) != 1 || before.Size() > isoformSkillMaxBytes {
		return nil, nil, fmt.Errorf("preserved a linked, special, or oversized SKILL.md")
	}
	file, err := dir.Open(isoformSkillFile)
	if err != nil {
		return nil, nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	links, linkErr := isoformSkillFileLinkCount(file)
	if err != nil || linkErr != nil || links != 1 || !os.SameFile(before, opened) {
		return nil, nil, fmt.Errorf("SKILL.md changed while opening")
	}
	content, err := io.ReadAll(io.LimitReader(file, isoformSkillMaxBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(content) > isoformSkillMaxBytes {
		return nil, nil, fmt.Errorf("preserved an oversized SKILL.md")
	}
	return content, before, nil
}

func stampedIsoformSkill(content, version string) []byte {
	stamp, _ := json.Marshal(isoformSkillStamp{Format: 1, Version: version, SHA256: isoformSkillDigest(content, version)})
	return []byte(content + isoformSkillStampStart + string(stamp) + isoformSkillStampEnd)
}

func isoformSkillDigest(content, version string) string {
	digest := sha256.Sum256([]byte(version + "\x00" + content))
	return hex.EncodeToString(digest[:])
}

func validIsoformSkillStamp(content []byte) bool {
	body := string(content)
	start := strings.LastIndex(body, isoformSkillStampStart)
	if start < 0 || !strings.HasSuffix(body, isoformSkillStampEnd) {
		return false
	}
	var stamp isoformSkillStamp
	metadata := body[start+len(isoformSkillStampStart) : len(body)-len(isoformSkillStampEnd)]
	if json.Unmarshal([]byte(metadata), &stamp) != nil || stamp.Format != 1 || stamp.Version == "" {
		return false
	}
	// Require the canonical whole stamp as well as the content digest, so a
	// user's edits to the version, metadata, or footer layout also survive.
	return bytes.Equal(content, stampedIsoformSkill(body[:start], stamp.Version))
}

func verifyIsoformSkillUnchanged(dir *os.Root, expected []byte, before os.FileInfo) error {
	current, info, err := readIsoformSkillFile(dir)
	if err != nil {
		return err
	}
	if !os.SameFile(before, info) || !bytes.Equal(current, expected) {
		return fmt.Errorf("preserved SKILL.md changed during removal")
	}
	return nil
}

// The bundled content stays the per-run source of the skill; the stamp format
// remains so the cleanup can recognize copies older versions installed.
