package main

import (
	"crypto/sha256"
	"encoding/binary"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// memoryExternalReadCache keeps the redacted, model-facing copy of memory
// between memory changes. Brief, chat and CLI readers ask for it many times a
// minute; redacting every entry on each read kept Isoform busy at rest.
//
// A snapshot is valid for one memory folder, one write revision (bumped by
// every memory write the app makes) and one stat fingerprint of the folder
// (catches edits made outside the app). Only the per-entry redaction and the
// stated exclusions are cached: the read policy (Closed Eyes, corrections,
// source privacy) is evaluated again on every read.
type memoryExternalReadCache struct {
	revision atomic.Uint64
	mu       sync.Mutex
	snapshot *memoryExternalSnapshot
}

type memoryExternalSnapshot struct {
	dir      string
	revision uint64
	signal   [sha256.Size]byte
	entries  []memoryExternalEntry

	exclusionsMu  sync.Mutex
	exclusionsFor string
	exclusions    []string
}

type memoryExternalEntry struct {
	raw      MemoryEntry
	redacted MemoryEntry
	inUse    bool
}

// memoryExternalReadLoad is a test hook: "entries" per memory folder read,
// "redact" per entry redacted, "exclusions" per exclusion scan.
var memoryExternalReadLoad func(kind string)

func recordMemoryExternalReadLoad(kind string) {
	if memoryExternalReadLoad != nil {
		memoryExternalReadLoad(kind)
	}
}

// invalidateMemoryExternalReads drops the cached copy at once, so a write,
// forget or confirm is what the next reader sees.
func (a *App) invalidateMemoryExternalReads() {
	if a == nil {
		return
	}
	c := &a.memoryExternalReads
	c.revision.Add(1)
	c.mu.Lock()
	c.snapshot = nil
	c.mu.Unlock()
}

// memoryFolderSettle is how old every file must be before a snapshot is kept:
// file times are coarse on some file systems, so a same-size rewrite inside
// one tick would otherwise keep its old fingerprint.
const memoryFolderSettle = 2 * time.Second

// memoryFolderSignal fingerprints the entries GetMemoryEntries reads without
// reading them: name, size and modification time of each live file. It also
// returns the newest modification time.
func memoryFolderSignal(dir string) ([sha256.Size]byte, time.Time, error) {
	var newest time.Time
	files, err := os.ReadDir(dir)
	if err != nil {
		return [sha256.Size]byte{}, newest, err
	}
	h := sha256.New()
	var buf [16]byte
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
		h.Write([]byte(file.Name()))
		h.Write([]byte{0})
		binary.LittleEndian.PutUint64(buf[:8], uint64(info.Size()))
		binary.LittleEndian.PutUint64(buf[8:], uint64(info.ModTime().UnixNano()))
		h.Write(buf[:])
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum, newest, nil
}

// memoryExternalSnapshot returns the current redacted copy of memory,
// reading and redacting the folder again only after it changed.
func (a *App) memoryExternalSnapshot() (*memoryExternalSnapshot, error) {
	dir, err := memoryDir()
	if err != nil {
		return nil, err
	}
	c := &a.memoryExternalReads
	// Taken before the read: a write that lands during it leaves a stale key,
	// so the next reader reads again instead of keeping the older copy.
	revision := c.revision.Load()
	checked := time.Now()
	signal, newest, err := memoryFolderSignal(dir)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.snapshot; s != nil && s.dir == dir && s.revision == revision && s.signal == signal {
		return s, nil
	}
	entries, err := a.GetMemoryEntries()
	if err != nil {
		return nil, err
	}
	recordMemoryExternalReadLoad("entries")
	s := &memoryExternalSnapshot{dir: dir, revision: revision, signal: signal, entries: make([]memoryExternalEntry, 0, len(entries))}
	for _, entry := range entries {
		item := memoryExternalEntry{raw: entry, inUse: memoryInUse(entry.Content)}
		if item.inUse {
			item.redacted = redactMemoryEntryForExternalRead(entry)
			recordMemoryExternalReadLoad("redact")
		}
		s.entries = append(s.entries, item)
	}
	if c.revision.Load() == revision && checked.Sub(newest) >= memoryFolderSettle {
		c.snapshot = s
	} else {
		c.snapshot = nil
	}
	return s, nil
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
	entry.TopicIDs = append([]string(nil), entry.TopicIDs...)
	entry.ActivitySessionIDs = append([]string(nil), entry.ActivitySessionIDs...)
	entry.Apps = append([]string(nil), entry.Apps...)
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
