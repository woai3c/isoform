package sop

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"wails-gui/internal/topicid"
)

// sopFileName is the canonical document inside every bundle. A directory
// without it is not an SOP bundle and is skipped by List.
const sopFileName = "SOP.md"

// scriptsDirName holds optional agent-authored helper scripts inside a bundle.
const scriptsDirName = "scripts"

// Store is a file-first SOP repository: one directory bundle per SOP under dir
// (~/.yansu-agent/sops).
type Store struct {
	dir string
	mu  sync.Mutex

	// Now is overridable in tests; nil falls back to time.Now.
	Now func() time.Time
	// ImportScopePolicy is configured before use; nil means mine only.
	ImportScopePolicy ImportScopePolicy
}

// NewStore opens (creating if needed) the SOP directory.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("sop: empty store dir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("sop: mkdir %s: %w", dir, err)
	}
	return &Store{dir: dir}, nil
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Dir returns the backing directory.
func (s *Store) Dir() string { return s.dir }

// PathFor returns the bundle directory for an SOP id.
func (s *Store) PathFor(id string) string {
	return filepath.Join(s.dir, id)
}

// SOPPath returns the SOP.md path inside a bundle.
func (s *Store) SOPPath(id string) string {
	return filepath.Join(s.dir, id, sopFileName)
}

// saveLocked writes only SOP.md (atomically via temp+rename); scripts/ is
// owned by InstallBundle and never touched here.
func (s *Store) saveLocked(e Entry) (Entry, error) {
	if err := e.normalizeMetadata(); err != nil {
		return Entry{}, err
	}
	if strings.TrimSpace(e.ID) == "" {
		return Entry{}, errors.New("sop: save with empty id")
	}
	nowMs := s.now().UnixMilli()
	if e.CreatedAt == 0 {
		e.CreatedAt = nowMs
	}
	if e.UpdatedAt == 0 {
		e.UpdatedAt = nowMs
	}
	if e.Version <= 0 {
		e.Version = 1
	}
	if err := os.MkdirAll(s.PathFor(e.ID), 0o700); err != nil {
		return Entry{}, fmt.Errorf("sop: mkdir bundle %s: %w", e.ID, err)
	}
	tmp := s.SOPPath(e.ID) + ".tmp"
	if err := os.WriteFile(tmp, []byte(e.serialize()), 0o600); err != nil {
		return Entry{}, fmt.Errorf("sop: write %s: %w", e.ID, err)
	}
	if err := os.Rename(tmp, s.SOPPath(e.ID)); err != nil {
		_ = os.Remove(tmp)
		return Entry{}, fmt.Errorf("sop: commit %s: %w", e.ID, err)
	}
	e.Scripts = s.discoverScripts(e.ID)
	return e, nil
}

func (s *Store) getLocked(id string) (Entry, error) {
	data, err := os.ReadFile(s.SOPPath(id))
	if err != nil {
		return Entry{}, err
	}
	e := parseEntry(id, string(data))
	if err := e.normalizeMetadata(); err != nil {
		return Entry{}, err
	}
	e.Scripts = s.discoverScripts(id)
	return e, nil
}

// discoverScripts walks a bundle's scripts/ dir and returns relative paths
// (e.g. "scripts/build.sh"), sorted. A missing dir yields nil.
func (s *Store) discoverScripts(id string) []string {
	root := filepath.Join(s.PathFor(id), scriptsDirName)
	var out []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return nil //nolint:nilerr // missing scripts/ dir is normal
		}
		if rel, rerr := filepath.Rel(s.PathFor(id), path); rerr == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

func (s *Store) listLocked() ([]Entry, error) {
	dirEntries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Entry
	for _, de := range dirEntries {
		name := de.Name()
		if !de.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		e, err := s.getLocked(name)
		if err != nil {
			continue // a dir without SOP.md is not a bundle
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out, nil
}

// Save writes the entry's SOP.md (atomically), defaulting CreatedAt/UpdatedAt
// to now and Version to 1 when unset, and returns the normalized entry.
func (s *Store) Save(e Entry) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(e)
}

// Get loads a single SOP by id.
func (s *Store) Get(id string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(id)
}

// List returns every SOP, newest first by CreatedAt.
func (s *Store) List() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

// Mutate reloads an entry, applies mutate, bumps UpdatedAt, and saves it under
// the store lock.
func (s *Store) Mutate(id string, mutate func(*Entry)) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, err := s.getLocked(id)
	if err != nil {
		return Entry{}, err
	}
	mutate(&e)
	e.UpdatedAt = s.now().UnixMilli()
	return s.saveLocked(e)
}

// Delete removes an entire bundle. A missing bundle is not an error.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(id) == "" || strings.Contains(id, string(filepath.Separator)) || id == "." || id == ".." {
		return fmt.Errorf("sop: refusing to delete invalid id %q", id)
	}
	if err := os.RemoveAll(s.PathFor(id)); err != nil {
		return err
	}
	return nil
}

// ReadScript returns the content of one helper script inside a bundle. rel
// must be a clean path under scripts/ (as reported by Entry.Scripts) — path
// escapes are rejected so the frontend can never read outside the bundle.
func (s *Store) ReadScript(id, rel string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if strings.TrimSpace(id) == "" || strings.Contains(id, "/") || strings.Contains(id, "\\") || id == "." || id == ".." {
		return "", fmt.Errorf("sop: invalid id %q", id)
	}
	cleaned := filepath.ToSlash(filepath.Clean(rel))
	if cleaned != rel || !strings.HasPrefix(cleaned, scriptsDirName+"/") || strings.Contains(cleaned, "..") {
		return "", fmt.Errorf("sop: invalid script path %q", rel)
	}
	data, err := os.ReadFile(filepath.Join(s.PathFor(id), filepath.FromSlash(cleaned)))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// InstallBundle promotes an authored draft directory into the bundle for id:
// it replaces scripts/ with the draft's scripts/ (if any) and writes SOP.md
// last (atomically) so a bundle is never visible without its document. meta
// supplies the machine frontmatter fields; the draft SOP.md supplies
// title/description/trigger/body (meta fields win only when the draft omits
// them). On update the existing CreatedAt is preserved and Version bumped.
func (s *Store) InstallBundle(id, draftDir string, meta Entry) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(filepath.Join(draftDir, sopFileName))
	if err != nil {
		return Entry{}, fmt.Errorf("sop: draft %s missing %s: %w", draftDir, sopFileName, err)
	}
	drafted := parseEntry(id, string(data))

	e := meta
	e.ID = id
	if strings.TrimSpace(drafted.Title) != "" {
		e.Title = drafted.Title
	}
	if strings.TrimSpace(drafted.Description) != "" {
		e.Description = drafted.Description
	}
	if strings.TrimSpace(drafted.Trigger) != "" {
		e.Trigger = drafted.Trigger
	}
	// Sites name what a run may be allowed to open, so an author update
	// never changes them: only a new bundle takes the draft's.
	if len(e.Sites) == 0 && len(drafted.Sites) > 0 {
		e.Sites = drafted.Sites
	}
	e.Body = drafted.Body
	if meta.Source == SourceImported {
		if err := drafted.normalizeMetadata(); err != nil {
			return Entry{}, err
		}
		e.Scope = "mine"
		if drafted.Scope != "mine" && s.ImportScopePolicy != nil && s.ImportScopePolicy(drafted.Scope) {
			e.Scope = drafted.Scope
		}
		e.TopicIDs = drafted.TopicIDs
	}

	nowMs := s.now().UnixMilli()
	if prev, perr := s.getLocked(id); perr == nil {
		e.CreatedAt = prev.CreatedAt
		e.Version = prev.Version + 1
		if meta.Source != SourceImported {
			// Author updates cannot promote scope or discard topic assignments.
			e.Scope = prev.Scope
			e.TopicIDs = prev.TopicIDs
		}
	} else if !os.IsNotExist(perr) {
		return Entry{}, perr
	} else {
		e.CreatedAt = nowMs
		e.Version = 1
	}
	e.UpdatedAt = nowMs
	if err := e.normalizeMetadata(); err != nil {
		return Entry{}, err
	}

	if err := os.MkdirAll(s.PathFor(id), 0o700); err != nil {
		return Entry{}, fmt.Errorf("sop: mkdir bundle %s: %w", id, err)
	}

	// Replace scripts/ wholesale: an update rewrites the process, so stale
	// helper scripts from the previous version must not linger.
	dstScripts := filepath.Join(s.PathFor(id), scriptsDirName)
	if err := os.RemoveAll(dstScripts); err != nil {
		return Entry{}, fmt.Errorf("sop: clear scripts for %s: %w", id, err)
	}
	srcScripts := filepath.Join(draftDir, scriptsDirName)
	if info, serr := os.Stat(srcScripts); serr == nil && info.IsDir() {
		if err := copyDir(srcScripts, dstScripts); err != nil {
			return Entry{}, fmt.Errorf("sop: copy scripts for %s: %w", id, err)
		}
	}

	return s.saveLocked(e)
}

func (e *Entry) normalizeMetadata() error {
	if e.metadataErr != nil {
		return e.metadataErr
	}
	if e.Scope == "" {
		e.Scope = "mine"
	}
	switch e.Scope {
	case "mine", "topic", "team", "company":
	default:
		return fmt.Errorf("sop: invalid scope %q", e.Scope)
	}
	for _, id := range e.TopicIDs {
		if !topicid.Valid(id) {
			return errors.New("sop: invalid topic id")
		}
	}
	return nil
}

// copyDir recursively copies regular files from src into dst, preserving the
// executable bit. Symlinks and special files are skipped: the author agent is
// sandboxed to plain files, and following links out of the draft dir would be
// a path-escape hazard.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if info, ierr := d.Info(); ierr == nil && info.Mode()&0o100 != 0 {
			mode = 0o700
		}
		return os.WriteFile(target, data, mode)
	})
}

var (
	slugInvalidChars = regexp.MustCompile(`[^a-z0-9]+`)
	slugDashRuns     = regexp.MustCompile(`-{2,}`)
)

const maxSlugLen = 60

// Slugify derives a bundle directory name from a title: lowercase ASCII
// letters/digits with single dashes, capped in length. Titles that reduce to
// nothing (e.g. pure CJK) fall back to "sop".
func Slugify(title string) string {
	slug := strings.ToLower(strings.TrimSpace(title))
	slug = slugInvalidChars.ReplaceAllString(slug, "-")
	slug = slugDashRuns.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > maxSlugLen {
		slug = strings.Trim(slug[:maxSlugLen], "-")
	}
	if slug == "" {
		return "sop"
	}
	return slug
}

// UniqueSlug returns Slugify(title), appending -2, -3, ... while a bundle with
// that id already exists.
func (s *Store) UniqueSlug(title string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	base := Slugify(title)
	slug := base
	for n := 2; ; n++ {
		if _, err := os.Stat(s.PathFor(slug)); os.IsNotExist(err) {
			return slug
		}
		slug = fmt.Sprintf("%s-%d", base, n)
	}
}
