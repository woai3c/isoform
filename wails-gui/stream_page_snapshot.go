package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// The first page of a chat stream (LoadStreamItemsPage at the latest cursor)
// paints from a saved copy: the last projected page, saved beside the other
// private data. A copy is served only while two things hold:
//   - the privacy stamp it was made under (a forget, a private app, Closed Eyes
//     deletes it at once), and
//   - the stream's item version it was read at (storage.StreamItemVersion, bumped
//     by triggers on every insert, edit, withdrawal and removal). A version that
//     differs is recomputed under a short deadline; on timeout the copy is served
//     and the event that changed the stream adds what is missing.
//
// Older pages always read live. The copy is written only after the page was
// computed, and only if the privacy stamp held across the computation.
const (
	streamPageDirName         = "stream-pages"
	streamPageVersionDeadline = 200 * time.Millisecond
	streamPageRecomputeWait   = 400 * time.Millisecond
	streamPageRefreshAge      = 15 * time.Minute
	streamPageLaunchDelay     = 30 * time.Second
	streamPageEventDelay      = time.Second
	streamPageDefaultLimit    = 50
	streamPageSnapshotVersion = 1
)

// streamPageWakeEvents are the events that say a stream changed. They wake a
// refresh early; correctness never depends on them, because the version does.
var streamPageWakeEvents = map[string]bool{
	"session:items-appended": true, "main-chat:waiting-changed": true,
}

type streamPageKey struct {
	session string
	limit   int
}

type streamPageSnapshot struct {
	Format  int                `json:"format"`
	Version int64              `json:"version"`
	Stamp   string             `json:"stamp"`
	SavedAt time.Time          `json:"savedAt"`
	Page    StreamItemPageData `json:"page"`
}

// streamPageState is the saved-page bookkeeping kept on the app.
type streamPageState struct {
	mu                         sync.Mutex
	launchAt                   time.Time
	keys                       map[streamPageKey]bool
	lastRun                    time.Time
	dueAt                      time.Time
	running                    bool
	versionWait, recomputeWait time.Duration
	launchDelay, eventDelay    time.Duration
	// computes and retries count background work, so tests can prove reads
	// within the window do no work.
	computes atomic.Int64
	retries  atomic.Int64
}

// settings fills in the defaults a zero-value state lacks. Callers hold mu.
func (st *streamPageState) settings() {
	if st.keys == nil {
		st.keys = map[streamPageKey]bool{}
		st.launchAt = time.Now()
		st.versionWait = streamPageVersionDeadline
		st.recomputeWait = streamPageRecomputeWait
		st.launchDelay = streamPageLaunchDelay
		st.eventDelay = streamPageEventDelay
	}
}

func streamPageFile(key streamPageKey) (string, error) {
	root, err := flavorDataDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(key.session + "\x00" + string(rune(key.limit))))
	return filepath.Join(root, streamPageDirName, hex.EncodeToString(sum[:16])+".json"), nil
}

// readStreamPageSnapshot returns the saved page when it was made under stamp.
// Any other copy, or an unreadable one, is deleted rather than served.
func readStreamPageSnapshot(key streamPageKey, stamp string) (streamPageSnapshot, bool) {
	path, err := streamPageFile(key)
	if err != nil {
		return streamPageSnapshot{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return streamPageSnapshot{}, false
	}
	var snap streamPageSnapshot
	if json.Unmarshal(raw, &snap) != nil || snap.Format != streamPageSnapshotVersion || stamp == "" || snap.Stamp != stamp {
		_ = os.Remove(path)
		return streamPageSnapshot{}, false
	}
	return snap, true
}

func writeStreamPageSnapshot(key streamPageKey, snap streamPageSnapshot) error {
	path, err := streamPageFile(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// streamPageStamp is the privacy stamp a saved copy is made under. It holds the
// Today privacy stamp and names the brief read epoch explicitly: a brief lead
// on a card is withheld by the same privacy changes that advance that epoch
// (withdrawals, forgets, private apps, source withdrawals). Any of them moves
// the stamp, so a copy that carries a withheld lead is dropped before the next
// read, not at the age limit. The epoch counts in memory; it resets on launch,
// which conservatively drops copies across a restart until its durable form
// (#588) is in.
func (a *App) streamPageStamp(ctx context.Context) string {
	stamp := a.todayChatSourcesStamp(ctx)
	if stamp == "" {
		return ""
	}
	return fmt.Sprintf("%s|brief:%d", stamp, mobileBriefReadEpoch.Load())
}

// loadLatestStreamPage is the latest-cursor read: the saved first page when it
// holds, otherwise a computed one. Background work is scheduled, not done here.
func (a *App) loadLatestStreamPage(session string, limit int) (StreamItemPageData, error) {
	if limit <= 0 {
		limit = streamPageDefaultLimit
	}
	key := streamPageKey{session: session, limit: limit}
	st := &a.streamPages
	st.mu.Lock()
	st.settings()
	st.keys[key] = true
	versionWait, recomputeWait := st.versionWait, st.recomputeWait
	st.mu.Unlock()
	now := time.Now()
	stamp := a.streamPageStamp(context.Background())
	if stamp == "" {
		page, _, err := a.computeLatestStreamPage(key)
		return page, err
	}
	verCtx, cancel := context.WithTimeout(context.Background(), versionWait)
	version, verErr := a.persistenceService().StreamItemVersion(verCtx, session)
	cancel()
	saved, ok := readStreamPageSnapshot(key, stamp)
	switch {
	case ok && verErr == nil && saved.Version == version:
		a.scheduleStreamPageRefresh(a.streamPageDueFor(now, saved.SavedAt))
		return saved.Page, nil
	case ok && verErr != nil:
		// The version could not be read in time: serve the copy and refresh.
		a.scheduleStreamPageRefresh(now)
		return saved.Page, nil
	case !ok:
		// No usable copy: compute before the first paint, then refresh later.
		page, _, err := a.computeLatestStreamPage(key)
		a.scheduleStreamPageRefresh(a.streamPageDueFor(now, time.Time{}))
		return page, err
	}
	// The copy is from an older version: recompute under a short deadline. On
	// timeout the copy is served, and the stream event adds what is missing.
	type result struct {
		page StreamItemPageData
		err  error
	}
	done := make(chan result, 1)
	go func() {
		page, _, err := a.computeLatestStreamPage(key)
		done <- result{page, err}
	}()
	select {
	case r := <-done:
		if r.err == nil {
			return r.page, nil
		}
		a.scheduleStreamPageRefresh(now)
		return saved.Page, nil
	case <-time.After(recomputeWait):
		a.scheduleStreamPageRefresh(now)
		return saved.Page, nil
	}
}

// streamPageDueFor is when the next background refresh may run for a copy
// saved at savedAt (zero: none). A copy older than the refresh age refreshes
// now; otherwise the first refresh after launch waits the launch delay.
func (a *App) streamPageDueFor(now, savedAt time.Time) time.Time {
	st := &a.streamPages
	st.mu.Lock()
	defer st.mu.Unlock()
	st.settings()
	if !savedAt.IsZero() && now.Sub(savedAt) > streamPageRefreshAge {
		return now
	}
	if st.lastRun.IsZero() {
		if ready := st.launchAt.Add(st.launchDelay); ready.After(now) {
			return ready
		}
	}
	if !st.lastRun.IsZero() && now.Sub(st.lastRun) > streamPageRefreshAge {
		return now
	}
	return time.Time{}
}

// computeLatestStreamPage reads the latest page, projects it, and saves it when
// the privacy stamp held across the computation. The item version is read before
// the items, so a write during the read leaves the saved version behind.
func (a *App) computeLatestStreamPage(key streamPageKey) (StreamItemPageData, bool, error) {
	st := &a.streamPages
	st.computes.Add(1)
	ctx := context.Background()
	stamp := a.streamPageStamp(ctx)
	version, verr := a.persistenceService().StreamItemVersion(ctx, key.session)
	page, err := a.persistenceService().LoadStreamItemsPage(ctx, key.session, -1, key.limit)
	if err != nil {
		return StreamItemPageData{}, false, err
	}
	a.projectStreamItems(page.Items)
	release, err := a.projectMainChatView(ctx, key.session, page.Items, time.Now())
	if err != nil {
		return StreamItemPageData{}, false, err
	}
	release()
	if verr != nil || stamp == "" || a.streamPageStamp(ctx) != stamp {
		return page, false, nil
	}
	_ = writeStreamPageSnapshot(key, streamPageSnapshot{Format: streamPageSnapshotVersion, Version: version, Stamp: stamp, SavedAt: time.Now(), Page: page})
	return page, true, nil
}

// scheduleStreamPageRefresh queues background work no earlier than at (zero:
// nothing to do). One goroutine runs the queue on a lowered-priority thread.
func (a *App) scheduleStreamPageRefresh(at time.Time) {
	if at.IsZero() {
		return
	}
	st := &a.streamPages
	st.mu.Lock()
	defer st.mu.Unlock()
	st.settings()
	if st.dueAt.IsZero() || at.Before(st.dueAt) {
		st.dueAt = at
	}
	if st.running {
		return
	}
	st.running = true
	go a.runStreamPageRefreshes()
}

func (a *App) runStreamPageRefreshes() {
	runtime.LockOSThread()
	lowerCurrentThreadPriority()
	st := &a.streamPages
	for {
		st.mu.Lock()
		due := st.dueAt
		if due.IsZero() {
			st.running = false
			st.mu.Unlock()
			return
		}
		st.mu.Unlock()
		if wait := time.Until(due); wait > 0 {
			time.Sleep(min(wait, time.Second))
			continue
		}
		st.mu.Lock()
		st.dueAt = time.Time{}
		st.mu.Unlock()
		a.refreshStreamPages()
	}
}

// refreshStreamPages runs the work the saved read leaves out: the permission
// card retries, then a recomputed and saved first page for each known session.
// Retries that fail stay dirty, so a delay loses no write.
func (a *App) refreshStreamPages() {
	st := &a.streamPages
	st.retries.Add(1)
	a.retryWorkPermissionWrites()
	st.mu.Lock()
	st.settings()
	keys := make([]streamPageKey, 0, len(st.keys))
	for key := range st.keys {
		keys = append(keys, key)
	}
	st.mu.Unlock()
	for _, key := range keys {
		_, _, _ = a.computeLatestStreamPage(key)
	}
	st.mu.Lock()
	st.lastRun = time.Now()
	st.mu.Unlock()
}

// noteStreamPageEvent wakes the refresh early after a stream change event.
func (a *App) noteStreamPageEvent(eventName string) {
	if streamPageWakeEvents[eventName] {
		st := &a.streamPages
		st.mu.Lock()
		st.settings()
		delay := st.eventDelay
		st.mu.Unlock()
		a.scheduleStreamPageRefresh(time.Now().Add(delay))
	}
}
