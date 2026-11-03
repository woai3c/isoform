package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"wails-gui/internal/platform/activitymon"
)

// Pending sources contain privacy metadata only, never captured text. Keeping
// their original identities before releasing a rotation makes a failed commit
// recoverable without guessing that an unknown committed dependency was safe.
func callRecordingSource(id string, start, end int64, events []activitymon.ActivityEvent) activitySourceProvenance {
	source := activitySourceProvenance{SessionID: id, StartTime: start, EndTime: end}
	for _, event := range events {
		source.Apps = append(source.Apps, event.AppBundleID, event.AppName, event.SpeechDictationApp)
		source.Private = source.Private || event.PrivateWindow || activitymon.IsPrivateBrowsing(event.WindowTitle) || activitySourceAppIsSensitive(event.AppBundleID, event.AppName)
	}
	source.Apps = uniqueNonEmpty(source.Apps, strings.TrimSpace)
	return source
}

// meetingSidecarMu serializes every recording sidecar write and removal.
// updateMeetingWAVMeta reads and writes under it, so a field change never
// lands between another write's read and write. A whole record
// (writeMeetingAudioMeta) is built from its writer's own state outside the
// lock, so it keeps under the lock the two fields only field changes set.
// Nothing else is locked while it is held.
var meetingSidecarMu sync.Mutex

// writeMeetingAudioMeta replaces a sidecar atomically (a temp file renamed
// over it), so a reader never sees a half-written one. The transcribing
// engine and a decode-failure note already on the sidecar are kept when the
// record leaves them empty: the recording writer and crash recovery never
// set them.
func writeMeetingAudioMeta(path string, meta meetingAudioMeta) error {
	meetingSidecarMu.Lock()
	defer meetingSidecarMu.Unlock()
	if meta.TranscribedBy == "" || meta.DecodeFailed == nil {
		if current, present, err := readMeetingAudioMetaFile(path); err == nil && present {
			if meta.TranscribedBy == "" {
				meta.TranscribedBy = current.TranscribedBy
			}
			if meta.DecodeFailed == nil {
				meta.DecodeFailed = current.DecodeFailed
			}
		}
	}
	return writeMeetingAudioMetaFile(path, meta)
}

// readMeetingAudioMetaFile reads a sidecar: present is false when there is
// none, and err is set when one is there but cannot be read or parsed.
func readMeetingAudioMetaFile(path string) (meta meetingAudioMeta, present bool, err error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return meta, false, nil
	}
	if err != nil {
		return meta, true, err
	}
	err = json.Unmarshal(raw, &meta)
	return meta, true, err
}

// writeMeetingAudioMetaFile is writeMeetingAudioMeta for a caller that holds
// meetingSidecarMu.
func writeMeetingAudioMetaFile(path string, meta meetingAudioMeta) error {
	raw, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".meeting-meta-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(raw); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

// An initial reservation precedes snapshot collection. Only a completed proof
// may reconstruct a missing row; an interrupted reservation stays fail-closed.
// The embedded format reads older journals as incomplete, never as permission.
type pendingRecordingSource struct {
	activitySourceProvenance
	Complete bool `json:"complete,omitempty"`
}

func (w *meetingAudioWriter) reserveActivitySession(source activitySourceProvenance, owner bool) error {
	return w.journalActivitySession(source, owner, false)
}

func (w *meetingAudioWriter) completeActivitySession(source activitySourceProvenance, owner bool) error {
	return w.journalActivitySession(source, owner, true)
}

// persistMetaLocked chooses the journal location under the same lock as the
// finalization handoff. While WAV writing runs, updates stay in the raw journal;
// after it completes, every update goes to the final sidecar.
func (w *meetingAudioWriter) persistMetaLocked() error {
	path := w.metaPath()
	if w.finalized {
		path = meetingWAVMetaPath(w.audioDir, w.finalRel)
	}
	if err := writeMeetingAudioMeta(path, w.meta); err != nil {
		return err
	}
	if path != w.metaPath() {
		os.Remove(w.metaPath())
	}
	return nil
}

func (w *meetingAudioWriter) journalActivitySession(source activitySourceProvenance, owner, complete bool) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	pendingSource := false
	for _, pending := range w.meta.PendingSources {
		pendingSource = pendingSource || pending.SessionID == source.SessionID
	}
	if w.aborted || (w.closed && !pendingSource) {
		return fmt.Errorf("recording source is closed")
	}
	source.Apps = uniqueNonEmpty(append(source.Apps, w.meta.CaptureSource.Apps...), strings.TrimSpace)
	source.Apps = uniqueNonEmpty(append(source.Apps, w.meta.App, w.meta.BundleID), strings.TrimSpace)
	source.Private = source.Private || w.meta.Protected || w.meta.CaptureSource.Private
	if !stringSliceContains(w.meta.ActivitySessions, source.SessionID) {
		w.meta.ActivitySessions = append(w.meta.ActivitySessions, source.SessionID)
	}
	found := false
	for i, existing := range w.meta.PendingSources {
		if existing.SessionID == source.SessionID {
			if existing.Complete {
				// A stale reservation or narrower retry cannot remove the
				// completed source's privacy-history interval.
				if !complete || source.StartTime <= 0 || (existing.StartTime > 0 && existing.StartTime < source.StartTime) {
					source.StartTime = existing.StartTime
				}
				if !complete {
					source.EndTime = existing.EndTime
				} else {
					source.EndTime = max(source.EndTime, existing.EndTime)
				}
			}
			source.Apps = uniqueNonEmpty(append(source.Apps, existing.Apps...), strings.TrimSpace)
			source.Private = source.Private || existing.Private
			complete = complete || existing.Complete
			w.meta.PendingSources[i] = pendingRecordingSource{activitySourceProvenance: source, Complete: complete}
			found = true
			break
		}
	}
	if !found {
		w.meta.PendingSources = append(w.meta.PendingSources, pendingRecordingSource{activitySourceProvenance: source, Complete: complete})
	}
	if owner && !w.closed {
		w.meta.SessionID = source.SessionID
		w.meta.PendingAudio = true
	}
	// Privacy only grows, even after a completed source is committed and its
	// pending entry is removed. Keep snapshot-only apps and private titles.
	w.meta.CaptureSource.Apps = uniqueNonEmpty(append(w.meta.CaptureSource.Apps, source.Apps...), strings.TrimSpace)
	w.meta.CaptureSource.Private = w.meta.CaptureSource.Private || source.Private
	return w.persistMetaLocked()
}

func (w *meetingAudioWriter) commitActivitySession(id string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.aborted {
		return
	}
	pending := make([]pendingRecordingSource, 0, len(w.meta.PendingSources))
	for _, source := range w.meta.PendingSources {
		if source.SessionID != id {
			pending = append(pending, source)
		}
	}
	w.meta.PendingSources = pending
	if w.finalized && id == w.meta.SessionID {
		w.meta.PendingAudio = false
	}
	if err := w.persistMetaLocked(); err != nil {
		log.Printf("[meeting-audio] commit provenance: %v", err)
	}
}

func (s *ActivityStore) recoverPendingRecordingSources(meta *meetingAudioMeta) error {
	for _, source := range meta.PendingSources {
		if !source.Complete {
			return fmt.Errorf("pending source privacy is incomplete: %s", source.SessionID)
		}
		existing, err := s.GetSessionByID(source.SessionID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if existing != nil {
			continue
		} // never overwrite a later protected row
		if _, err := s.retainedActivitySourceProvenance(source.SessionID); err == nil {
			return fmt.Errorf("pending source was explicitly removed: %s", source.SessionID)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		apps, _ := json.Marshal(source.Apps)
		status := "dismissed"
		if source.Private {
			status = "protected"
		}
		row := ActivitySessionRow{ID: source.SessionID, StartTime: source.StartTime, EndTime: source.EndTime, AppNames: string(apps), TriageStatus: status, IsMeeting: true, MeetingApp: meta.App, TriggerKind: recoveredRecordingTrigger, CreatedAt: source.EndTime}
		if err := s.SaveSession(row, nil, nil); err != nil {
			return err
		}
	}
	meta.PendingSources = nil
	return nil
}
