package main

import (
	"path/filepath"
	"time"

	"wails-gui/internal/platform/activitymon"
)

// storageFramesInUse reports, for the storage sweep, the screenshot files the
// capture still holds in memory (frames of the session being recorded and
// frames waiting for the PII mask) and whether a call or call candidate is
// open. A call keeps its frames until it ends, however long that is, and
// their rows are written only then, so the sweep must not take them.
func (am *activityMonitorState) storageFramesInUse() (map[string]bool, bool) {
	am.mu.Lock()
	defer am.mu.Unlock()
	held := make(map[string]bool, len(am.allSnapshots)+len(am.pendingPIISnapshots))
	for _, snap := range am.allSnapshots {
		held[filepath.Clean(snap.relPath)] = true
	}
	for _, pending := range am.pendingPIISnapshots {
		held[filepath.Clean(pending.snapshot.relPath)] = true
	}
	return held, am.inMeeting || am.meetingCandidateActive
}

// storageRecordingFinal reports whether a call recording's transcript is
// final, so storage may compress it and the storage limit may remove it:
//   - the recording itself was transcribed by the after-call engine
//     (Qwen3-ASR), or
//   - that engine cannot run here (turned off, or not shipped on this
//     platform), so the live engine's transcript is the final one, or
//   - it was saved meetingTrackMaxAge (7 days) ago: if the after-call pass
//     has not run by then, it will not, because the call pipeline only
//     retries calls that have no transcript at all.
//
// A call without a transcript is never final.
func (a *App) storageRecordingFinal(m audioMedia) bool {
	if !m.Transcribed {
		return false
	}
	if a.activity != nil && a.activity.store != nil &&
		readMeetingWAVMeta(a.activity.store.AudioDir(), m.RelPath).TranscribedBy == meetingTranscribedByAfterCall {
		return true
	}
	if !a.afterCallSpeechModelEnabled() || !a.afterCallModelView().Supported {
		return true
	}
	saved := m.SavedAt
	if saved <= 0 {
		saved = m.StartTime
	}
	return time.Since(time.UnixMilli(saved)) >= meetingTrackMaxAge
}

// storageMediaDeferReason says why the rotator's media work (shrinking
// screenshots, compressing recordings) should wait: it is CPU work that can
// always be done later, so it waits for mains power.
func storageMediaDeferReason() string {
	if lowPowerModeEnabled() {
		return "Low Power Mode is on"
	}
	if power := activitymon.PowerSource(); power.Known && power.OnBattery {
		return "on battery"
	}
	return ""
}
