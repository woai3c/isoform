package main

import (
	"log"
	"os"
	"sync"
	"time"
)

// pendingSnapshotWriteWait bounds how long a session flush waits for
// screenshots whose PII-masked JPEG is still being written. A warm OCR pass
// takes one to two seconds, so this normally covers the session_end frame
// captured just before the flush.
const pendingSnapshotWriteWait = 5 * time.Second

// flushMustNotWait reports whether the flush for triggerKind must not wait on
// the capture slot, the PII slot or unfinished screenshot writes. Shutdown
// flushes must commit inside the 5s shutdown watchdog, and the pre-meeting
// flush runs on the event drain and delays meeting audio. A write that fails
// after the commit marks its row image-missing, and the startup reconcile
// does the same for rows whose write never finished.
func flushMustNotWait(triggerKind string) bool {
	switch triggerKind {
	case "shutdown_in_meeting", "shutdown_meeting_candidate", "pre_meeting_flush":
		return true
	default:
		return false
	}
}

// snapshotWriteWait returns how long the flush for triggerKind waits for
// unfinished screenshot writes.
func snapshotWriteWait(triggerKind string) time.Duration {
	if flushMustNotWait(triggerKind) {
		return 0
	}
	return pendingSnapshotWriteWait
}

// finalCaptureWait returns how long the flush for triggerKind lets its
// session_end frame wait for a busy capture or PII slot.
func finalCaptureWait(triggerKind string) time.Duration {
	if flushMustNotWait(triggerKind) {
		return 0
	}
	return sessionEndCaptureWait
}

// snapshotWrite tracks a screenshot whose file the async PII mask writes after
// its entry has already joined the session. The mask drops the frame when OCR
// or masking fails, and the entry must then never become a row: a row without
// a file is a broken image on the Activity screen.
//
// Either side may finish first. The session flush drops entries whose write
// already failed, counts the rest as pending for the session before its
// commit, and marks them persisted after it; a write that finishes after that
// marks its row image-missing, keeping the row's text (failure), or corrects
// its size (the masked JPEG is re-encoded, so its size differs from the
// captured one).
type snapshotWrite struct {
	relPath string
	done    chan struct{}

	mu        sync.Mutex
	finished  bool
	ok        bool
	written   int64
	persisted bool
	rowSize   int64
	sessionID string
	// pending is the session counter this write is counted in while it
	// runs, nil when it is not counted. Only changed under mu, so the
	// count goes up before finish can bring it down.
	pending *pendingSnapshotSessions
}

// snapshotWriteFollowUp is the row change a write outcome requires once both
// the write and the session commit have happened.
type snapshotWriteFollowUp struct {
	markMissing bool
	resizeTo    int64 // > 0 when the row's size_bytes must change
}

func newSnapshotWrite(relPath string) *snapshotWrite {
	return &snapshotWrite{relPath: relPath, done: make(chan struct{})}
}

// followUpLocked must be called with both finished and persisted set.
func (w *snapshotWrite) followUpLocked() snapshotWriteFollowUp {
	if !w.ok {
		return snapshotWriteFollowUp{markMissing: true}
	}
	if w.written > 0 && w.written != w.rowSize {
		return snapshotWriteFollowUp{resizeTo: w.written}
	}
	return snapshotWriteFollowUp{}
}

// finish records the outcome of the write. The caller applies followUp, then
// takes the write out of pending (when non-nil) for sessionID.
func (w *snapshotWrite) finish(written int64, ok bool) (followUp snapshotWriteFollowUp, sessionID string, pending *pendingSnapshotSessions) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finished {
		return snapshotWriteFollowUp{}, "", nil
	}
	w.finished = true
	w.ok = ok
	w.written = written
	close(w.done)
	pending, w.pending = w.pending, nil
	if w.persisted {
		followUp = w.followUpLocked()
	}
	return followUp, w.sessionID, pending
}

// track counts the write as pending for sessionID before the session commit,
// so the OCR worker never sees the committed session while one of its files
// is still being written. A write that already finished is not counted.
func (w *snapshotWrite) track(sessionID string, pending *pendingSnapshotSessions) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sessionID = sessionID
	if w.finished || w.pending != nil {
		return
	}
	pending.add(sessionID, 1)
	w.pending = pending
}

// untrack undoes track when the session commit failed.
func (w *snapshotWrite) untrack() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending != nil {
		w.pending.add(w.sessionID, -1)
		w.pending = nil
	}
}

// markPersisted records that the session commit inserted a row of rowSize
// bytes for this screenshot. A write that already finished returns the row
// change its outcome requires; one still running applies it in finish.
func (w *snapshotWrite) markPersisted(rowSize int64) snapshotWriteFollowUp {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.persisted = true
	w.rowSize = rowSize
	if !w.finished {
		return snapshotWriteFollowUp{}
	}
	return w.followUpLocked()
}

func (w *snapshotWrite) result() (finished, ok bool, written int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.finished, w.ok, w.written
}

// writtenOK reports whether the screenshot's file is on disk. A nil write is
// a synchronous write that already succeeded.
func (w *snapshotWrite) writtenOK() bool {
	if w == nil {
		return true
	}
	finished, ok, _ := w.result()
	return finished && ok
}

// pendingSnapshotSessions counts, per session, committed rows whose file the
// async PII mask is still writing. The OCR worker leaves those sessions for a
// later tick so a frame that lands late is still indexed.
type pendingSnapshotSessions struct {
	mu     sync.Mutex
	counts map[string]int
}

func (p *pendingSnapshotSessions) add(sessionID string, delta int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.counts == nil {
		p.counts = make(map[string]int)
	}
	p.counts[sessionID] += delta
	if p.counts[sessionID] <= 0 {
		delete(p.counts, sessionID)
	}
}

func (p *pendingSnapshotSessions) has(sessionID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts[sessionID] > 0
}

// finishSnapshotWrite resolves write and applies the row change its outcome
// requires. A nil write is a synchronous write and needs nothing.
func (am *activityMonitorState) finishSnapshotWrite(write *snapshotWrite, written int64, ok bool) {
	if write == nil {
		return
	}
	followUp, sessionID, pending := write.finish(written, ok)
	am.applySnapshotWriteFollowUp(write.relPath, followUp)
	if pending != nil {
		pending.add(sessionID, -1)
	}
}

// awaitSnapshotWrites waits up to budget for the screenshots in snapshots
// whose files are still being written, then returns the entries that can
// become rows: synchronous writes, finished writes (with their final size),
// and writes still in flight when the budget runs out. A budget of zero does
// not wait at all.
func awaitSnapshotWrites(snapshots []snapshotEntry, budget time.Duration) []snapshotEntry {
	var deadline <-chan time.Time
	expired := budget <= 0
	if !expired {
		timer := time.NewTimer(budget)
		defer timer.Stop()
		deadline = timer.C
	}

	kept := make([]snapshotEntry, 0, len(snapshots))
	dropped := 0
	for _, snap := range snapshots {
		if snap.write == nil {
			kept = append(kept, snap)
			continue
		}
		if !expired {
			select {
			case <-snap.write.done:
			case <-deadline:
				expired = true
			}
		}
		finished, ok, written := snap.write.result()
		if finished && !ok {
			dropped++
			continue
		}
		if finished {
			snap.sizeBytes = written
		}
		kept = append(kept, snap)
	}
	if dropped > 0 {
		log.Printf("[activity] left out %d screenshot(s) whose PII-masked file was never written", dropped)
	}
	return kept
}

// trackSnapshotWrites runs before the session commit: writes still running
// are counted as pending for sessionID. untrackSnapshotWrites undoes it when
// the commit fails.
func (am *activityMonitorState) trackSnapshotWrites(sessionID string, snapshots []snapshotEntry) {
	for _, snap := range snapshots {
		if snap.write != nil {
			snap.write.track(sessionID, &am.pendingSnapshotSessions)
		}
	}
}

func (am *activityMonitorState) untrackSnapshotWrites(snapshots []snapshotEntry) {
	for _, snap := range snapshots {
		if snap.write != nil {
			snap.write.untrack()
		}
	}
}

// markSnapshotWritesPersisted runs after the session commit. Writes that
// finished between awaitSnapshotWrites and the commit get their row change
// here; writes still running apply theirs when they finish.
func (am *activityMonitorState) markSnapshotWritesPersisted(snapshots []snapshotEntry) {
	for _, snap := range snapshots {
		if snap.write == nil {
			continue
		}
		am.applySnapshotWriteFollowUp(snap.write.relPath, snap.write.markPersisted(snap.sizeBytes))
	}
}

func (am *activityMonitorState) applySnapshotWriteFollowUp(relPath string, followUp snapshotWriteFollowUp) {
	if am == nil || am.store == nil {
		return
	}
	switch {
	case followUp.markMissing:
		marked, err := am.store.MarkSnapshotMissingByFilePath(relPath)
		if err != nil {
			log.Printf("[activity] failed to mark unwritten screenshot %s as missing: %v", relPath, err)
			return
		}
		if marked {
			log.Printf("[activity] marked unwritten screenshot %s as missing; its row keeps its text", relPath)
		}
	case followUp.resizeTo > 0:
		if err := am.store.UpdateSnapshotSizeByFilePath(relPath, followUp.resizeTo); err != nil {
			log.Printf("[activity] failed to update size for screenshot %s: %v", relPath, err)
		}
	}
}

// writeSnapshotFile writes a screenshot through a temporary file and a rename
// so a reader never sees a partial JPEG at path.
func writeSnapshotFile(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// reconcileSnapshotFiles runs once at startup. Rows from earlier runs whose
// screenshot never reached disk (the app quit before a masked JPEG landed, or
// an older build kept rows for frames the PII mask dropped) are marked
// image-missing and keep their text; marked rows whose file is back are
// restored; JPEGs no row refers to are removed. Rows and files from this run
// (beforeMs on) are left to their own write.
func reconcileSnapshotFiles(store *ActivityStore, beforeMs int64) {
	result, err := store.ReconcileSnapshotFiles(beforeMs)
	if err != nil {
		log.Printf("[activity] snapshot reconcile failed: %v", err)
		return
	}
	if result != (SnapshotFileReconcile{}) {
		log.Printf("[activity] snapshot reconcile: marked %d row(s) image-missing, restored %d, removed %d unreferenced file(s)",
			result.Marked, result.Restored, result.FilesRemoved)
	}
}
