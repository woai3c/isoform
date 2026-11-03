package main

import (
	"encoding/json"
	"log"
	"slices"
	"time"

	"github.com/google/uuid"

	"wails-gui/internal/meetingdetect"
)

// A call can end with nothing in the activity buffer: a listen-only call
// whose live speech recognition returned nothing and that saw no keyboard or
// click. Its recording is then the only record of the call, so it is
// finalized with a session row of its own and transcribed like any other
// call, instead of staying open and being handed to the next call.

// meetingSliceTrigger is the 2-hour flush that cuts a long call into slices
// without ending it.
const meetingSliceTrigger = "meeting_flush"

// metaSnapshot returns the call the recording belongs to.
func (w *meetingAudioWriter) metaSnapshot() meetingAudioMeta {
	w.mu.Lock()
	defer w.mu.Unlock()
	meta := w.meta
	meta.ActivitySessions = slices.Clone(meta.ActivitySessions)
	meta.CaptureSource.Apps = slices.Clone(meta.CaptureSource.Apps)
	meta.PendingSources = slices.Clone(meta.PendingSources)
	for i := range meta.PendingSources {
		meta.PendingSources[i].Apps = slices.Clone(meta.PendingSources[i].Apps)
	}
	return meta
}

// detachCallRecordingLocked takes the main recording off the monitor when
// its call ends. Caller holds am.mu.
func detachCallRecordingLocked(am *activityMonitorState) *meetingAudioWriter {
	w := am.meetingAudioWriter
	am.meetingAudioWriter = nil
	return w
}

// persistEndedCallRecording finalizes a recording whose call ended with no
// events into a WAV with a meeting session row and writes its transcript
// from the recording. epoch is the recording generation the call ran under.
// It holds the recording fence from the check to the saved row, as the
// session path does, so turning recording off meanwhile leaves nothing.
func (a *App) persistEndedCallRecording(w *meetingAudioWriter, epoch uint64, triggerKind string) {
	if w == nil {
		return
	}
	a.meetingRecordingMu.Lock()
	defer a.meetingRecordingMu.Unlock()
	a.persistEndedCallRecordingLocked(w, epoch, triggerKind)
}

// persistEndedCallRecordingLocked is persistEndedCallRecording for a caller
// that holds meetingRecordingMu.
func (a *App) persistEndedCallRecordingLocked(w *meetingAudioWriter, epoch uint64, triggerKind string) {
	if w == nil {
		return
	}
	if a.IsClosedEyesModeEnabled() {
		// Observation is paused: keep the call as the pause path does
		// (keepCallRecordingAtPause). Nothing is transcribed while paused;
		// the transcript follows when observation resumes.
		if session, meta, ok := a.saveCallRecordingLocked(w, epoch, pausedCallTrigger); ok {
			a.callsKeptAtPauseMu.Lock()
			a.callsKeptAtPause = append(a.callsKeptAtPause, callKeptAtPause{session: session, meta: meta, epoch: epoch})
			a.callsKeptAtPauseMu.Unlock()
		}
		return
	}
	if session, meta, ok := a.saveCallRecordingLocked(w, epoch, triggerKind); ok {
		go a.saveAutomaticMeetingTranscript(session, nil, meta.App, epoch, meta.MeetingID)
	}
}

// saveCallRecordingLocked finalizes a call's recording into a WAV with a
// meeting session row of its own. Caller holds meetingRecordingMu.
func (a *App) saveCallRecordingLocked(w *meetingAudioWriter, epoch uint64, triggerKind string) (ActivitySessionRow, meetingAudioMeta, bool) {
	am := a.activity
	if am == nil || am.store == nil || !a.automaticMeetingRecordingEpochIsCurrent(epoch) {
		w.Abort()
		return ActivitySessionRow{}, meetingAudioMeta{}, false
	}
	sessionID := uuid.New().String()
	meta := w.metaSnapshot()
	if !meetingdetect.IsTranscriptionSupportedMeetingName(meta.App) {
		w.Abort()
		return ActivitySessionRow{}, meta, false
	}
	now := time.Now().UnixMilli()
	start := meta.StartMs
	if started, ok := meetingAudioStartFromPath(w.prefix + ".wav"); ok && start <= 0 {
		start = started.UnixMilli()
	}
	// The recording covers the whole call, while this row owns only the
	// activity after prior rotations. Keep activity totals disjoint.
	for _, id := range meta.ActivitySessions {
		if id == sessionID {
			continue
		}
		if row, err := am.store.GetSessionByID(id); err == nil && row != nil && row.EndTime > start {
			start = row.EndTime
		}
	}
	for _, pending := range meta.PendingSources {
		if pending.SessionID != sessionID && pending.EndTime > start {
			start = pending.EndTime
		}
	}
	if start <= 0 || start > now {
		start = now
	}
	appNames, _ := json.Marshal(compactStrings(append([]string{meta.App, meta.BundleID}, meta.CaptureSource.Apps...)))
	session := ActivitySessionRow{
		ID:           sessionID,
		StartTime:    start,
		EndTime:      now,
		TriggerKind:  triggerKind,
		AppNames:     string(appNames),
		TriageStatus: meetingRecordingTriageStatus(meta),
		IsMeeting:    true,
		MeetingApp:   meta.App,
		CreatedAt:    now,
	}
	proof := callRecordingSource(session.ID, session.StartTime, session.EndTime, nil)
	if err := w.completeActivitySession(proof, true); err != nil {
		w.Abort()
		log.Printf("[meeting-audio] cannot preserve call provenance: %v", err)
		return ActivitySessionRow{}, meta, false
	}
	meta = w.metaSnapshot()
	relPath, size, err := w.Finalize()
	if err != nil {
		w.RemoveFinalized(relPath)
		w.KeepForRecovery(session.ID)
		log.Printf("[meeting-audio] finalize failed; journal and tracks retained: %v", err)
		return ActivitySessionRow{}, meta, false
	}
	if relPath == "" {
		return ActivitySessionRow{}, meta, false
	}
	session.AudioPath, session.AudioSizeBytes = relPath, size
	if err := am.store.SaveSession(session, nil, nil); err != nil {
		// The WAV stays: a recording without a row beats no recording.
		log.Printf("[meeting-audio] could not record the session of %s: %v", relPath, err)
		return ActivitySessionRow{}, meta, false
	}
	w.commitActivitySession(session.ID)
	log.Printf("[meeting-audio] saved a call recording on its own (%s): %s (%d bytes)", triggerKind, relPath, size)
	return session, meta, true
}

// pausedCallTrigger marks the session of a call whose recording ended
// because observation was paused.
const pausedCallTrigger = "observation_paused"

// callKeptAtPause is a call recording saved when observation was paused;
// its transcript is written once observation resumes.
type callKeptAtPause struct {
	session ActivitySessionRow
	meta    meetingAudioMeta
	epoch   uint64
}

// endCallForObservationPauseLocked ends the call in progress when
// observation is paused: the recording so far is kept as the call (not
// thrown away), the meeting state is cleared so nothing is left half on,
// and the meeting id is remembered so a call still running after the pause
// continues under it. Caller holds am.mu; it returns the recording to keep.
func endCallForObservationPauseLocked(am *activityMonitorState) (*meetingAudioWriter, uint64) {
	if !am.inMeeting {
		return nil, 0
	}
	w := detachCallRecordingLocked(am)
	epoch := am.meetingRecordingEpoch
	if am.meetingID != "" {
		am.lastMeetingID = am.meetingID
		am.lastMeetingApp = am.meetingAppName
		am.lastMeetingEndMs = time.Now().UnixMilli()
		am.lastMeetingPaused = true
	}
	am.inMeeting = false
	am.meetingAppName = ""
	am.meetingBundleID = ""
	am.meetingStartMs = 0
	am.meetingRecordingEpoch = 0
	am.meetingID = ""
	am.lastMeetingEventMs = 0
	am.lastMeetingAudioMs = 0
	am.meetingBrowserMicReleased = false
	am.meetingBrowserGapSinceMs = 0
	am.meetingBrowserObservedMs = 0
	return w, epoch
}

// keepCallRecordingAtPause saves the recording of a call that observation
// was paused in. Nothing is transcribed while paused; the transcript
// follows when observation resumes (or at the next start).
func (a *App) keepCallRecordingAtPause(w *meetingAudioWriter, epoch uint64) {
	if w == nil {
		return
	}
	a.meetingRecordingMu.Lock()
	defer a.meetingRecordingMu.Unlock()
	session, meta, ok := a.saveCallRecordingLocked(w, epoch, pausedCallTrigger)
	if !ok {
		return
	}
	a.callsKeptAtPauseMu.Lock()
	a.callsKeptAtPause = append(a.callsKeptAtPause, callKeptAtPause{session: session, meta: meta, epoch: epoch})
	a.callsKeptAtPauseMu.Unlock()
}

// transcribeCallsKeptAtPause writes the transcripts of the calls whose
// recordings were saved when observation was paused.
func (a *App) transcribeCallsKeptAtPause() {
	a.callsKeptAtPauseMu.Lock()
	calls := a.callsKeptAtPause
	a.callsKeptAtPause = nil
	a.callsKeptAtPauseMu.Unlock()
	for _, call := range calls {
		// The row is the truth for the recording's path (storage may have
		// moved it on since the pause).
		if a.activity != nil && a.activity.store != nil {
			if fresh, err := a.activity.store.GetSessionByID(call.session.ID); err == nil && fresh != nil {
				call.session.AudioPath = fresh.AudioPath
			}
		}
		go a.saveAutomaticMeetingTranscript(call.session, nil, call.meta.App, call.epoch, call.meta.MeetingID)
	}
}
