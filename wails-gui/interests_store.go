package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"wails-gui/internal/interests"
)

// interests.json sits beside topics.json and keeps, on this Mac only, the
// interests the model named, with the evidence behind each as local
// references (an activity id, a Purpose Map id, a hash of a search; never
// its text), and when the passes ran.

const (
	interestsStateFileName = "interests.json"
	interestsStateVersion  = 1
)

type interestsStateFile struct {
	Version   int                  `json:"version"`
	Interests []interests.Interest `json:"interests,omitempty"`
	// RefreshedAt is the last pass of either kind; ConsolidatedAt the last
	// monthly one (RFC 3339).
	RefreshedAt    string `json:"refreshedAt,omitempty"`
	ConsolidatedAt string `json:"consolidatedAt,omitempty"`
	// RunsOn is the local date RunsToday counts passes for.
	RunsOn    string `json:"runsOn,omitempty"`
	RunsToday int    `json:"runsToday,omitempty"`
	// FailedAt is the last failed refresh; Failures counts failures in a
	// row. A failed monthly review is counted on its own, so refreshes go on
	// while it backs off.
	FailedAt            string `json:"failedAt,omitempty"`
	Failures            int    `json:"failures,omitempty"`
	ConsolidateFailedAt string `json:"consolidateFailedAt,omitempty"`
	ConsolidateFailures int    `json:"consolidateFailures,omitempty"`
}

// interestsStore reads and writes interests.json atomically.
type interestsStore struct {
	path        string
	mu          sync.Mutex
	publication sync.RWMutex
}

func newInterestsStore(dir string) *interestsStore {
	if dir == "" {
		return &interestsStore{}
	}
	return &interestsStore{path: filepath.Join(dir, interestsStateFileName)}
}

// load returns the stored state, or an empty one.
func (s *interestsStore) load() interestsStateFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *interestsStore) loadLocked() interestsStateFile {
	state := interestsStateFile{Version: interestsStateVersion}
	if s.path == "" {
		return state
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return state
	}
	if json.Unmarshal(raw, &state) != nil {
		return interestsStateFile{Version: interestsStateVersion}
	}
	return state
}

// update applies fn to the stored state and writes it back.
func (s *interestsStore) update(fn func(*interestsStateFile)) (interestsStateFile, error) {
	_, state, err := s.write(nil, fn)
	return state, err
}

// updateUnless is update, except that nothing is written when stop reports
// true under the store's lock: the feature went off while a pass ran, and
// forget, which takes the same lock, may already have removed the file. It
// reports whether it wrote.
func (s *interestsStore) updateUnless(stop func() bool, fn func(*interestsStateFile)) (bool, error) {
	wrote, _, err := s.write(stop, fn)
	return wrote, err
}

func (s *interestsStore) write(stop func() bool, fn func(*interestsStateFile)) (bool, interestsStateFile, error) {
	s.publication.Lock()
	defer s.publication.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if stop != nil && stop() {
		return false, interestsStateFile{}, nil
	}
	state := s.loadLocked()
	fn(&state)
	state.Version = interestsStateVersion
	if s.path == "" {
		return false, state, errors.New("the topics folder is unavailable")
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return false, state, err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return false, state, err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return false, state, err
	}
	return true, state, os.Rename(tmp, s.path)
}

// forget removes the file: the interests and their evidence go.
func (s *interestsStore) forget() error {
	s.publication.Lock()
	defer s.publication.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" {
		return nil
	}
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
