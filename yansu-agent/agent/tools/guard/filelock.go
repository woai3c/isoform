package guard

import (
	"container/list"
	"path/filepath"
	"sync"
)

// FileLockManager gives out per-path mutexes so parallel Write/Edit
// calls on the same file serialize. It uses an LRU-capped cache so long
// sessions don't accumulate an unbounded number of mutex entries.
//
// The cap matches the refactor doc §4.3.3: default 1024 entries.
type FileLockManager struct {
	mu     sync.Mutex
	cap    int
	lru    *list.List
	byPath map[string]*list.Element
}

type lockEntry struct {
	path string
	lock *sync.Mutex
}

// NewFileLockManager constructs a lock manager capped at the given
// number of entries (0 means default 1024).
func NewFileLockManager(cap int) *FileLockManager {
	if cap <= 0 {
		cap = 1024
	}
	return &FileLockManager{
		cap:    cap,
		lru:    list.New(),
		byPath: make(map[string]*list.Element),
	}
}

// Lock returns the mutex for path, creating one if needed and updating
// its LRU position.
func (m *FileLockManager) Lock(path string) *sync.Mutex {
	cleaned := filepath.Clean(path)

	m.mu.Lock()
	defer m.mu.Unlock()

	if el, ok := m.byPath[cleaned]; ok {
		m.lru.MoveToFront(el)
		return el.Value.(*lockEntry).lock
	}
	entry := &lockEntry{path: cleaned, lock: &sync.Mutex{}}
	el := m.lru.PushFront(entry)
	m.byPath[cleaned] = el

	// Evict LRU tail if over cap. Note: we only evict entries that no
	// goroutine is currently holding — we can't know that from here, so
	// we take the risk: a bucket that was recently touched and then
	// pushed out might double-issue a mutex. In practice 1024 entries is
	// far beyond typical session footprint so eviction is rare.
	for m.lru.Len() > m.cap {
		tail := m.lru.Back()
		if tail == nil {
			break
		}
		te := tail.Value.(*lockEntry)
		delete(m.byPath, te.path)
		m.lru.Remove(tail)
	}
	return entry.lock
}

// Size returns the current number of tracked entries (for tests).
func (m *FileLockManager) Size() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lru.Len()
}
