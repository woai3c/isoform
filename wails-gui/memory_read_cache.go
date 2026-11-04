package main

import (
	"crypto/sha256"
	"encoding/binary"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// memoryExternalReadCache keeps one parsed copy of the memory folder between
// memory changes. Brief, chat, Today, About You and CLI readers ask for memory
// many times a minute; reading, parsing and redacting every note on each call
// kept Isoform busy at rest.
//
// A snapshot is valid for one memory folder and one write revision, bumped at
// once by every memory write the app makes. Edits made outside the app are
// caught by a stat fingerprint of the folder (notes and .history), checked at
// most once every memoryFolderCheckEvery, so a read with nothing changed does
// no disk work. Raw entries are parsed once per snapshot and redacted copies
// are made on first external read. The read policy (Closed Eyes, corrections,
// source privacy) is evaluated again on every read.
type memoryExternalReadCache struct {
	revision atomic.Uint64
	mu       sync.Mutex
	snapshot *memoryExternalSnapshot
}

type memoryExternalSnapshot struct {
	dirKey   string
	dir      string
	revision uint64
	signal   [sha256.Size]byte
	// checked is when the folder last matched signal; guarded by the cache mu.
	checked time.Time
	entries []memoryExternalEntry

	externalOnce sync.Once

	exclusionsMu  sync.Mutex
	exclusionsFor string
	exclusions    []string
}

type memoryExternalEntry struct {
	raw      MemoryEntry
	redacted MemoryEntry
	inUse    bool
}

// memoryFolderCheckEvery bounds how long an edit made outside the app can go
// unseen. App writes do not wait for it: they bump the revision.
const memoryFolderCheckEvery = 5 * time.Second

// memoryReadNow and memoryFiles are the clock and file access of the memory
// read path, so tests can move time and count file opens and stats.
var (
	memoryReadNow                  = time.Now
	memoryFiles   memoryFileAccess = osMemoryFiles{}
)

type memoryFileAccess interface {
	ReadDir(name string) ([]os.DirEntry, error)
	ReadFile(name string) ([]byte, error)
}

type osMemoryFiles struct{}

func (osMemoryFiles) ReadDir(name string) ([]os.DirEntry, error) { return os.ReadDir(name) }
func (osMemoryFiles) ReadFile(name string) ([]byte, error)       { return os.ReadFile(name) }

// memoryExternalReadLoad is a test hook: "entries" per memory folder read,
// "redact" per entry redacted, "exclusions" per exclusion scan.
var memoryExternalReadLoad func(kind string)

func recordMemoryExternalReadLoad(kind string) {
	if memoryExternalReadLoad != nil {
		memoryExternalReadLoad(kind)
	}
}

// invalidateMemoryExternalReads makes the next reader read the folder again,
// so a write, forget or confirm is what it sees. It never waits for a read.
func (a *App) invalidateMemoryExternalReads() {
	if a == nil {
		return
	}
	a.memoryExternalReads.revision.Add(1)
}

// memoryFolderSettle is how old every file must be before a snapshot is kept:
// file times are coarse on some file systems, so a same-size rewrite inside
// one tick would otherwise keep its old fingerprint.
const memoryFolderSettle = 2 * time.Second

// memoryDirKey holds everything memoryDir resolves the folder from. Resolving
// it checks the folder on disk (symlinks, MkdirAll), so a warm read compares
// this key instead.
func memoryDirKey() string {
	return strings.Join([]string{configDirName, os.Getenv("YANSU_APP_FLAVOR"), os.Getenv("YANSU_AGENT_HOME"), os.Getenv("HOME"), os.Getenv("USERPROFILE")}, "\x00")
}

// memoryFolderSignal fingerprints what GetMemoryEntries reads without reading
// it: name, size and modification time of each live note and of each
// .history/<id> folder. It also returns the newest note modification time.
func memoryFolderSignal(dir string) ([sha256.Size]byte, time.Time, error) {
	var newest time.Time
	files, err := memoryFiles.ReadDir(dir)
	if err != nil {
		return [sha256.Size]byte{}, newest, err
	}
	h := sha256.New()
	var buf [16]byte
	write := func(name string, info os.FileInfo) {
		h.Write([]byte(name))
		h.Write([]byte{0})
		binary.LittleEndian.PutUint64(buf[:8], uint64(info.Size()))
		binary.LittleEndian.PutUint64(buf[8:], uint64(info.ModTime().UnixNano()))
		h.Write(buf[:])
	}
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".md") || isOKFReservedMarkdownFile(file.Name()) {
			continue
		}
		info, err := file.Info()
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return [sha256.Size]byte{}, newest, err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		write(file.Name(), info)
	}
	h.Write([]byte{1})
	history, err := memoryFiles.ReadDir(filepath.Join(dir, memoryHistoryDirName))
	if err != nil && !os.IsNotExist(err) {
		return [sha256.Size]byte{}, newest, err
	}
	for _, folder := range history {
		info, err := folder.Info()
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return [sha256.Size]byte{}, newest, err
		}
		write(folder.Name(), info)
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum, newest, nil
}

// memorySnapshot returns the current parsed copy of memory, reading the
// folder again only after it changed.
func (a *App) memorySnapshot() (*memoryExternalSnapshot, error) {
	key := memoryDirKey()
	c := &a.memoryExternalReads
	c.mu.Lock()
	defer c.mu.Unlock()
	// Taken before the read: a write that lands during it leaves a stale key,
	// so the next reader reads again instead of keeping the older copy.
	revision := c.revision.Load()
	now := memoryReadNow()
	if s := c.snapshot; s != nil && s.dirKey == key && s.revision == revision {
		if now.Sub(s.checked) < memoryFolderCheckEvery {
			return s, nil
		}
		if signal, _, err := memoryFolderSignal(s.dir); err == nil && signal == s.signal {
			s.checked = now
			return s, nil
		}
	}
	dir, err := memoryDir()
	if err != nil {
		return nil, err
	}
	signal, newest, err := memoryFolderSignal(dir)
	if err != nil {
		return nil, err
	}
	entries, err := readMemoryEntries(dir)
	if err != nil {
		return nil, err
	}
	recordMemoryExternalReadLoad("entries")
	s := &memoryExternalSnapshot{dirKey: key, dir: dir, revision: revision, signal: signal, checked: now, entries: make([]memoryExternalEntry, 0, len(entries))}
	for _, entry := range entries {
		s.entries = append(s.entries, memoryExternalEntry{raw: entry})
	}
	if c.revision.Load() == revision && now.Sub(newest) >= memoryFolderSettle {
		c.snapshot = s
	} else {
		c.snapshot = nil
	}
	return s, nil
}

// memoryExternalSnapshot returns the current snapshot with its redacted,
// model-facing copies made.
func (a *App) memoryExternalSnapshot() (*memoryExternalSnapshot, error) {
	s, err := a.memorySnapshot()
	if err != nil {
		return nil, err
	}
	s.externalOnce.Do(func() {
		for i := range s.entries {
			entry := &s.entries[i]
			entry.inUse = memoryInUse(entry.raw.Content)
			if entry.inUse {
				entry.redacted = redactMemoryEntryForExternalRead(entry.raw)
				recordMemoryExternalReadLoad("redact")
			}
		}
	})
	return s, nil
}

func (s *memoryExternalSnapshot) rawEntries() []MemoryEntry {
	out := make([]MemoryEntry, 0, len(s.entries))
	for _, entry := range s.entries {
		out = append(out, cloneMemoryEntry(entry.raw))
	}
	return out
}

// allowed applies the current read policy to the snapshot's entries.
func (s *memoryExternalSnapshot) allowed(allowRead func(string, string) bool) []int {
	var out []int
	for i, entry := range s.entries {
		if entry.inUse && allowRead(entry.raw.ID, entry.raw.Content) {
			out = append(out, i)
		}
	}
	return out
}

func (s *memoryExternalSnapshot) redactedEntries(allowed []int) []MemoryEntry {
	out := make([]MemoryEntry, 0, len(allowed))
	for _, i := range allowed {
		out = append(out, cloneMemoryEntry(s.entries[i].redacted))
	}
	return out
}

// statedExclusions scans the allowed entries once per allowed set.
func (s *memoryExternalSnapshot) statedExclusions(allowed []int) []string {
	var key strings.Builder
	for _, i := range allowed {
		key.WriteString(strconv.Itoa(i))
		key.WriteByte(',')
	}
	s.exclusionsMu.Lock()
	defer s.exclusionsMu.Unlock()
	if s.exclusions == nil || s.exclusionsFor != key.String() {
		entries := make([]MemoryEntry, 0, len(allowed))
		for _, i := range allowed {
			entries = append(entries, s.entries[i].redacted)
		}
		s.exclusions = append([]string{}, mobileBriefStatedExclusions(entries)...)
		s.exclusionsFor = key.String()
		recordMemoryExternalReadLoad("exclusions")
	}
	return append([]string(nil), s.exclusions...)
}

func cloneMemoryEntry(entry MemoryEntry) MemoryEntry {
	entry.TopicIDs = slices.Clone(entry.TopicIDs)
	entry.ActivitySessionIDs = slices.Clone(entry.ActivitySessionIDs)
	entry.Apps = slices.Clone(entry.Apps)
	return entry
}

// redactionMemo remembers the output of a pure redaction by a hash of its
// input, so no unredacted text is kept. It is cleared when full.
type redactionMemo struct {
	mu     sync.Mutex
	limit  int
	values map[[sha256.Size]byte]string
}

func newRedactionMemo(limit int) *redactionMemo {
	return &redactionMemo{limit: limit, values: map[[sha256.Size]byte]string{}}
}

func (m *redactionMemo) redact(value string, redact func(string) string) string {
	if value == "" {
		return redact(value)
	}
	key := sha256.Sum256([]byte(value))
	m.mu.Lock()
	out, ok := m.values[key]
	m.mu.Unlock()
	if ok {
		return out
	}
	out = redact(value)
	m.mu.Lock()
	if len(m.values) >= m.limit {
		clear(m.values)
	}
	m.values[key] = out
	m.mu.Unlock()
	return out
}
