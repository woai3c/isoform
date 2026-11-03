package handoff

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

// Store is a file-first handoff repository: one JSON file per handoff under
// dir (~/.yansu-agent/handoff).
type Store struct {
	dir         string
	mu          sync.Mutex
	publication sync.RWMutex

	// Now is overridable in tests; nil falls back to time.Now.
	Now func() time.Time
}

// NewStore opens (creating if needed) the handoff directory.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("handoff: empty store dir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("handoff: mkdir %s: %w", dir, err)
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

// TryLockForPublication keeps source facts stable through a derived history
// commit. Reads do not take this separate barrier, so publication can re-read
// the current source even while a mutation is waiting.
func (s *Store) TryLockForPublication() bool { return s.publication.TryRLock() }

func (s *Store) UnlockForPublication() { s.publication.RUnlock() }

func (s *Store) pathFor(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *Store) saveLocked(e Entry) (Entry, error) {
	return s.saveLockedWithLinkedDraft(e, false)
}

func (s *Store) saveLockedWithLinkedDraft(e Entry, linkedDraftCompletion bool) (Entry, error) {
	if err := validateID(e.ID); err != nil {
		return Entry{}, err
	}
	if e.SchemaVersion > SchemaVersion {
		return Entry{}, fmt.Errorf("handoff: schema version %d is newer than supported version %d", e.SchemaVersion, SchemaVersion)
	}
	// Read before replacing a file so Save cannot accidentally downgrade a
	// newer schema or overwrite an unreadable record with a partial snapshot.
	previous, err := s.getLocked(e.ID)
	if err != nil && !os.IsNotExist(err) {
		return Entry{}, err
	}
	if err == nil {
		if previous.SchemaVersion > SchemaVersion {
			return Entry{}, fmt.Errorf("handoff: schema version %d is newer than supported version %d", previous.SchemaVersion, SchemaVersion)
		}
		userClosed := e.Status == StatusCompleted && closedByYou(e) && canUserComplete(previous.Status)
		if !CanTransition(previous.Status, e.Status) && !userClosed && !linkedDraftCompletion {
			return Entry{}, fmt.Errorf("handoff: transition from %q to %q is not allowed", previous.Status, e.Status)
		}
	}
	if !validStatus(e.Status) {
		return Entry{}, fmt.Errorf("handoff: unknown status %q", e.Status)
	}
	e.SchemaVersion = SchemaVersion
	nowMs := s.now().UnixMilli()
	if e.CreatedAt == 0 {
		e.CreatedAt = nowMs
	}
	if e.UpdatedAt == 0 {
		e.UpdatedAt = nowMs
	}
	if e.LastActivityAt == 0 {
		e.LastActivityAt = e.UpdatedAt
	}
	if len(e.Criteria) == 0 {
		e.Criteria = CriteriaFromBody(e.Body)
	}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return Entry{}, fmt.Errorf("handoff: encode %s: %w", e.ID, err)
	}
	file, err := os.CreateTemp(s.dir, ".handoff-*.tmp")
	if err != nil {
		return Entry{}, fmt.Errorf("handoff: write %s: %w", e.ID, err)
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	_, writeErr := file.Write(append(data, '\n'))
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return Entry{}, fmt.Errorf("handoff: write %s: %w", e.ID, err)
	}
	if err := os.Rename(tmp, s.pathFor(e.ID)); err != nil {
		return Entry{}, fmt.Errorf("handoff: commit %s: %w", e.ID, err)
	}
	// Leave legacy files intact as recovery copies. JSON always takes precedence;
	// Delete removes both formats so an old record cannot reappear.
	return e, nil
}

// CompleteLinkedDraft records an outcome already confirmed by the existing
// source-checked draft action. It never performs a send itself.
func (s *Store) CompleteLinkedDraft(id, draftID string, outcome Outcome) (Entry, error) {
	s.publication.Lock()
	defer s.publication.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, err := s.getLocked(id)
	if err != nil {
		return Entry{}, err
	}
	visible, err := s.listEntryVisibleLocked(entry)
	if err != nil || !visible {
		return Entry{}, errors.New("handoff: list task is unavailable")
	}
	if entry.ListItem == nil || draftID == "" || entry.ListItem.DraftTaskID != draftID {
		return Entry{}, errors.New("handoff: draft changed")
	}
	if entry.Status == StatusCompleted {
		if entry.Thread != nil && entry.Thread.Outcome != nil && reflect.DeepEqual(*entry.Thread.Outcome, outcome) {
			return entry, nil
		}
		return Entry{}, errors.New("handoff: task was already completed")
	}
	switch entry.Status {
	case StatusActive, StatusInProgress, StatusBlocked, StatusNeedsReview:
	default:
		return Entry{}, errors.New("handoff: task is no longer open")
	}
	if outcome.At <= 0 || (outcome.Kind != "replied" || outcome.By != OutcomeByIsoform) && (outcome.Kind != OutcomeYouDid || outcome.By != OutcomeByYou) {
		return Entry{}, errors.New("handoff: invalid draft outcome")
	}
	if entry.Thread == nil {
		entry.Thread = &Thread{}
	}
	entry.Thread.Outcome = &outcome
	entry.Status = StatusCompleted
	entry.UpdatedAt = s.now().UnixMilli()
	return s.saveLockedWithLinkedDraft(entry, true)
}

func (s *Store) getLocked(id string) (Entry, error) {
	if err := validateID(id); err != nil {
		return Entry{}, err
	}
	data, err := os.ReadFile(s.pathFor(id))
	if err == nil {
		return decodeJSONEntry(id, data)
	}
	if !os.IsNotExist(err) {
		return Entry{}, err
	}
	data, err = os.ReadFile(filepath.Join(s.dir, id+".md"))
	if err != nil {
		return Entry{}, err
	}
	e := parseEntry(id, string(data))
	e.ID = id
	return normalizeReadEntry(e), nil
}

func (s *Store) listLocked() ([]Entry, error) {
	dirEntries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	ids := map[string]struct{}{}
	for _, de := range dirEntries {
		name := de.Name()
		if !de.Type().IsRegular() || strings.HasPrefix(name, ".") {
			continue
		}
		ext := filepath.Ext(name)
		if ext != ".json" && ext != ".md" {
			continue
		}
		ids[strings.TrimSuffix(name, ext)] = struct{}{}
	}
	var out []Entry
	for id := range ids {
		e, err := s.getLocked(id)
		if err != nil {
			continue
		}
		visible, err := s.listEntryVisibleLocked(e)
		if err != nil {
			return nil, err
		}
		if !visible {
			continue
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out, nil
}

// Save writes the entry (atomically via temp+rename), defaulting
// CreatedAt/UpdatedAt to now when unset, and returns the normalized entry
// actually persisted. Explicit timestamps are preserved (e.g. migration).
func (s *Store) Save(e Entry) (Entry, error) {
	s.publication.Lock()
	defer s.publication.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(e)
}

// StageNewListBatch writes recoverable list tasks without exposing them to
// readers. PublishListBatch exposes the complete selection with one marker.
// A retry never overwrites an existing task, including after it was renamed.
func (s *Store) StageNewListBatch(entries []Entry) ([]Entry, error) {
	s.publication.Lock()
	defer s.publication.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(entries) == 0 {
		return nil, errors.New("handoff: empty list batch")
	}
	batchID := ""
	if entries[0].ListItem != nil {
		batchID = entries[0].ListItem.BatchID
	}
	marker, err := s.readListBatchMarkerLocked(batchID)
	if err != nil {
		return nil, err
	}
	expected := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.ListItem == nil || entry.ListItem.BatchID != batchID || entry.ListItem.Fingerprint == "" || expected[entry.ID] != "" {
			return nil, errors.New("handoff: invalid list batch entry")
		}
		expected[entry.ID] = entry.ListItem.Fingerprint
	}
	staged, err := s.readListBatchFileLocked(s.listBatchStagePath(batchID))
	if err != nil {
		return nil, err
	}
	if staged != nil && !reflect.DeepEqual(staged, expected) {
		return nil, errors.New("handoff: staged list batch changed")
	}
	if marker != nil && !reflect.DeepEqual(marker, expected) {
		return nil, errors.New("handoff: published list batch changed")
	}
	seen := make(map[string]bool, len(entries))
	out := make([]Entry, len(entries))
	exists := make([]bool, len(entries))
	for i, entry := range entries {
		if err := validateID(entry.ID); err != nil {
			return nil, err
		}
		if seen[entry.ID] || entry.Status != StatusActive || entry.ListItem == nil || entry.ListItem.Fingerprint == "" || entry.ListItem.BatchID != batchID || entry.Thread == nil || entry.Thread.Origin == nil || entry.Thread.Origin.Kind != OriginList {
			return nil, errors.New("handoff: invalid list batch entry")
		}
		if marker != nil && marker[entry.ID] != entry.ListItem.Fingerprint {
			return nil, errors.New("handoff: published list batch changed")
		}
		seen[entry.ID] = true
		prior, err := s.getLocked(entry.ID)
		if err == nil {
			if prior.ListItem == nil || prior.ListItem.BatchID != batchID || prior.ListItem.Fingerprint != entry.ListItem.Fingerprint || prior.Source != entry.Source {
				return nil, errors.New("handoff: list task id already belongs to different work")
			}
			out[i], exists[i] = prior, true
		} else if !os.IsNotExist(err) {
			return nil, err
		} else if marker != nil {
			return nil, errors.New("handoff: published list task was removed")
		}
	}
	if marker != nil && len(marker) != len(entries) {
		return nil, errors.New("handoff: published list batch changed")
	}
	if staged == nil {
		if err := s.writeListBatchFileLocked(s.listBatchStagePath(batchID), expected); err != nil {
			return nil, err
		}
	}
	created := []string{}
	for i, entry := range entries {
		if exists[i] {
			continue
		}
		saved, err := s.saveLocked(entry)
		if err != nil {
			for _, id := range created {
				if removeErr := os.Remove(s.pathFor(id)); removeErr != nil && !os.IsNotExist(removeErr) {
					err = errors.Join(err, removeErr)
				}
			}
			return nil, err
		}
		out[i] = saved
		created = append(created, entry.ID)
	}
	return out, nil
}

// PublishListBatch atomically exposes all staged entries after the caller has
// prepared their reminders and execution. A matching retry is harmless.
func (s *Store) PublishListBatch(entries []Entry) error {
	s.publication.Lock()
	defer s.publication.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(entries) == 0 || entries[0].ListItem == nil {
		return errors.New("handoff: empty list batch")
	}
	batchID := entries[0].ListItem.BatchID
	previous, err := s.readListBatchMarkerLocked(batchID)
	if err != nil {
		return err
	}
	marker := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.ListItem == nil || entry.ListItem.BatchID != batchID || entry.ListItem.Fingerprint == "" || marker[entry.ID] != "" {
			return errors.New("handoff: invalid list batch entry")
		}
		stored, err := s.getLocked(entry.ID)
		if err != nil || stored.ListItem == nil || stored.ListItem.BatchID != batchID || stored.ListItem.Fingerprint != entry.ListItem.Fingerprint {
			return errors.New("handoff: staged list task is unavailable")
		}
		marker[entry.ID] = entry.ListItem.Fingerprint
	}
	if previous != nil {
		if !reflect.DeepEqual(previous, marker) {
			return errors.New("handoff: published list batch changed")
		}
		return nil
	}
	staged, err := s.readListBatchFileLocked(s.listBatchStagePath(batchID))
	if err != nil || !reflect.DeepEqual(staged, marker) {
		return errors.New("handoff: staged list batch changed")
	}
	return s.writeListBatchFileLocked(s.listBatchMarkerPath(batchID), marker)
}

func (s *Store) writeListBatchFileLocked(path string, marker map[string]string) error {
	data, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(s.dir, ".list-batch-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (s *Store) listBatchMarkerPath(batchID string) string {
	return filepath.Join(s.dir, ".list-batch-"+batchID+".json")
}

func (s *Store) listBatchStagePath(batchID string) string {
	return filepath.Join(s.dir, ".list-batch-"+batchID+".stage")
}

func (s *Store) readListBatchMarkerLocked(batchID string) (map[string]string, error) {
	if len(batchID) != 64 || strings.Trim(batchID, "0123456789abcdef") != "" {
		return nil, errors.New("handoff: invalid list batch id")
	}
	return s.readListBatchFileLocked(s.listBatchMarkerPath(batchID))
}

func (s *Store) readListBatchFileLocked(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var marker map[string]string
	if err := json.Unmarshal(data, &marker); err != nil || len(marker) == 0 {
		return nil, errors.New("handoff: invalid list batch marker")
	}
	return marker, nil
}

func (s *Store) listEntryVisibleLocked(entry Entry) (bool, error) {
	if entry.ListItem == nil || entry.ListItem.BatchID == "" {
		return true, nil
	}
	marker, err := s.readListBatchMarkerLocked(entry.ListItem.BatchID)
	if err != nil || marker == nil {
		return false, err
	}
	return marker[entry.ID] == entry.ListItem.Fingerprint && entry.ListItem.Fingerprint != "", nil
}

// ListBatchEntry reads an individual staged or published list task for the
// reminder producer. The stage manifest must bind its id and fingerprint.
// Product readers should use Get or List, which hide an unpublished batch.
func (s *Store) ListBatchEntry(id string) (Entry, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, err := s.getLocked(id)
	if err != nil {
		return Entry{}, false, err
	}
	if entry.ListItem == nil || entry.ListItem.BatchID == "" || entry.ListItem.Fingerprint == "" {
		return Entry{}, false, errors.New("handoff: not a list batch task")
	}
	marker, err := s.readListBatchMarkerLocked(entry.ListItem.BatchID)
	if err != nil {
		return Entry{}, false, err
	}
	if marker != nil {
		if marker[id] != entry.ListItem.Fingerprint {
			return Entry{}, false, errors.New("handoff: list batch does not contain task")
		}
		return entry, true, nil
	}
	staged, err := s.readListBatchFileLocked(s.listBatchStagePath(entry.ListItem.BatchID))
	if err != nil {
		return Entry{}, false, err
	}
	if staged == nil || staged[id] != entry.ListItem.Fingerprint {
		return Entry{}, false, errors.New("handoff: list batch does not contain task")
	}
	return entry, false, nil
}

// Get loads a single handoff by ID.
func (s *Store) Get(id string) (Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, err := s.getLocked(id)
	if err != nil {
		return Entry{}, err
	}
	visible, err := s.listEntryVisibleLocked(entry)
	if err != nil {
		return Entry{}, err
	}
	if !visible {
		return Entry{}, os.ErrNotExist
	}
	return entry, nil
}

// List returns every handoff, newest first by CreatedAt.
func (s *Store) List() ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

// UpdateStatus transitions a handoff to st and bumps UpdatedAt.
func (s *Store) UpdateStatus(id string, st Status) (Entry, error) {
	return s.MutateIfStatus(id, nil, func(e *Entry) {
		e.Status = st
	})
}

// MutateIfStatus reloads an entry, verifies its current status is allowed, then
// applies mutate and saves the result under the store lock. An empty allowed
// slice means any current status is accepted.
func (s *Store) MutateIfStatus(id string, allowed []Status, mutate func(*Entry)) (Entry, error) {
	return s.mutate(id, nil, allowed, mutate)
}

// MutateIfAttempt additionally fences a mutation to the exact running attempt.
// An empty attemptID matches legacy entries whose attempt ID is also empty.
func (s *Store) MutateIfAttempt(id, attemptID string, allowed []Status, mutate func(*Entry)) (Entry, error) {
	return s.mutate(id, &attemptID, allowed, mutate)
}

// MutateThread changes only a task's thread bookkeeping (opened, deferred)
// without moving its update time, so reading a thread is not news.
func (s *Store) MutateThread(id string, mutate func(*Thread)) (Entry, error) {
	return s.mutateWith(id, nil, nil, func(e *Entry) {
		if e.Thread == nil {
			e.Thread = &Thread{}
		}
		mutate(e.Thread)
	}, true)
}

func (s *Store) mutate(id string, attemptID *string, allowed []Status, mutate func(*Entry)) (Entry, error) {
	return s.mutateWith(id, attemptID, allowed, mutate, false)
}

func (s *Store) mutateWith(id string, attemptID *string, allowed []Status, mutate func(*Entry), keepTime bool) (Entry, error) {

	s.publication.Lock()
	defer s.publication.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()

	e, err := s.getLocked(id)
	if err != nil {
		return Entry{}, err
	}
	visible, err := s.listEntryVisibleLocked(e)
	if err != nil {
		return Entry{}, err
	}
	if !visible {
		return Entry{}, os.ErrNotExist
	}
	if attemptID != nil && e.AttemptID != *attemptID {
		return Entry{}, errors.New("handoff: execution attempt changed")
	}
	if len(allowed) > 0 {
		ok := false
		for _, st := range allowed {
			if e.Status == st {
				ok = true
				break
			}
		}
		if !ok {
			return Entry{}, fmt.Errorf("handoff: status %q is not allowed", e.Status)
		}
	}
	if mutate != nil {
		mutate(&e)
	}
	if e.ID != id {
		return Entry{}, errors.New("handoff: mutation cannot change entry id")
	}
	if !keepTime {
		e.UpdatedAt = s.now().UnixMilli()
	}
	return s.saveLocked(e)
}

// Delete removes a handoff file. A missing file is not an error.
func (s *Store) Delete(id string) error {
	s.publication.Lock()
	defer s.publication.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateID(id); err != nil {
		return err
	}
	// Remove the legacy copy first: a failed JSON removal must never expose an
	// older active state in place of a newer terminal state.
	for _, path := range []string{filepath.Join(s.dir, id+".md"), s.pathFor(id)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// DeleteIfUnchanged removes an entry created for a failed start only while its
// contents still match the created snapshot. Rollback may change UpdatedAt.
func (s *Store) DeleteIfUnchanged(expected Entry) (bool, error) {
	s.publication.Lock()
	defer s.publication.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.getLocked(expected.ID)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	current.UpdatedAt = expected.UpdatedAt
	if !reflect.DeepEqual(current, expected) {
		return false, nil
	}
	for _, path := range []string{filepath.Join(s.dir, expected.ID+".md"), s.pathFor(expected.ID)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return false, err
		}
	}
	return true, nil
}

// ActiveForChip returns the most recent chip-worthy handoff (see ActiveForChips),
// or nil when none. This is the single handoff surfaced as a composer chip.
func (s *Store) ActiveForChip() (*Entry, error) {
	active, err := s.ActiveForChips(1)
	if err != nil || len(active) == 0 {
		return nil, err
	}
	return &active[0], nil
}

// ActiveForChips returns the newest handoffs the user can act on now, to surface
// as composer chips and on Today: waiting to start (active), blocked and needs
// review. Working, completed, aborted and stale entries are not returned. The
// order is newest first; a non-positive limit means no explicit cap.
func (s *Store) ActiveForChips(limit int) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	all, err := s.listLocked()
	if err != nil {
		return nil, err
	}
	var out []Entry
	for i := range all {
		if IsChipStatus(all[i].Status) {
			out = append(out, all[i])
			if limit > 0 && len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

// MarkStale transitions `active` handoffs to `stale` when they have aged past
// ttl or been superseded by a newer handoff sharing the same SourceID. Returns
// the entries that were transitioned. Non-active handoffs are never touched.
func (s *Store) MarkStale(ttl time.Duration) ([]Entry, error) {
	s.publication.Lock()
	defer s.publication.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()

	all, err := s.listLocked()
	if err != nil {
		return nil, err
	}

	// Newest CreatedAt per SourceID across ALL statuses — an active handoff is
	// "superseded" if a strictly-newer handoff exists for the same source.
	newestBySource := map[string]int64{}
	for _, e := range all {
		if e.SourceID == "" {
			continue
		}
		if e.CreatedAt > newestBySource[e.SourceID] {
			newestBySource[e.SourceID] = e.CreatedAt
		}
	}

	now := s.now()
	var expired []Entry
	for _, e := range all {
		if e.Status != StatusActive || e.SchemaVersion > SchemaVersion {
			continue
		}
		// A person's own list reminder remains theirs until they complete it;
		// the handoff suggestion TTL does not retire it.
		if e.ListItem != nil && e.ListItem.Who == "reminder" {
			continue
		}
		stale := now.Sub(time.UnixMilli(e.CreatedAt)) >= ttl
		if !stale && e.SourceID != "" && newestBySource[e.SourceID] > e.CreatedAt {
			stale = true
		}
		if !stale {
			continue
		}
		e.Status = StatusStale
		e.UpdatedAt = now.UnixMilli()
		saved, err := s.saveLocked(e)
		if err != nil {
			return expired, err
		}
		expired = append(expired, saved)
	}
	return expired, nil
}
