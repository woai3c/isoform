package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const transcriptActivitySourcesMarker = "- **Activity sources**: "

var errMeetingTranscriptNotReadable = errors.New("transcript source is not readable")

func withTranscriptActivitySources(markdown string, ids []string) string {
	raw, _ := json.Marshal(compactStrings(ids))
	return strings.Replace(markdown, "- **Session**: ", transcriptActivitySourcesMarker+string(raw)+"\n- **Session**: ", 1)
}

// The transcript carries its dependencies even after audio retention clears
// audio_path. Older recordings also keep them in the audio sidecar.
func (s *ActivityStore) transcriptSourceIDs(session ActivitySessionRow, text string) ([]string, error) {
	ids := []string{session.ID}
	declared := false
	audioPath := session.AudioPath
	if text == "" && session.TranscriptPath != "" {
		raw, err := os.ReadFile(session.TranscriptPath)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		text = string(raw)
	}
	header, _, _ := strings.Cut(text, "## Transcript")
	for _, line := range strings.Split(header, "\n") {
		if ref, ok := strings.CutPrefix(line, "- **Audio**: activity/audio/"); ok && audioPath == "" {
			if !filepath.IsLocal(ref) {
				return nil, errMeetingTranscriptNotReadable
			}
			audioPath = ref
		}
		if raw, ok := strings.CutPrefix(line, transcriptActivitySourcesMarker); ok {
			var sources []string
			if json.Unmarshal([]byte(raw), &sources) != nil || len(sources) == 0 || !stringSliceContains(sources, session.ID) {
				return nil, errMeetingTranscriptNotReadable
			}
			ids, declared = append(ids, sources...), true
		}
	}
	if audioPath != "" {
		raw, err := os.ReadFile(meetingWAVMetaPath(s.AudioDir(), audioPath))
		var meta meetingAudioMeta
		if err == nil {
			if json.Unmarshal(raw, &meta) != nil {
				return nil, fmt.Errorf("invalid recording dependencies")
			}
			// Older builds could create a sidecar holding only the engine or
			// a decode note for a recording that had none. It establishes
			// nothing about the recording, so it counts as missing.
			if !meetingAudioMetaEstablished(meta) {
				err = fs.ErrNotExist
			}
		}
		if err != nil && (!os.IsNotExist(err) || !declared) {
			return nil, fmt.Errorf("recording dependencies unavailable: %w", err)
		}
		if err == nil {
			for _, source := range meta.PendingSources {
				if !source.Complete {
					return nil, errMeetingTranscriptNotReadable
				}
			}
			if meta.Protected || meta.CaptureSource.Private {
				return nil, errMeetingTranscriptNotReadable
			}
			for _, app := range meta.CaptureSource.Apps {
				if activitySourceAppIsSensitive(app, app) {
					return nil, errMeetingTranscriptNotReadable
				}
			}
			ids = append(ids, meta.ActivitySessions...)
		}
	}
	return compactStrings(ids), nil
}

func (a *App) transcriptSourcesWithPolicy(store *ActivityStore, session ActivitySessionRow, text string, policy activityReadPrivacyPolicy) ([]activitySourceProvenance, error) {
	ids, err := store.transcriptSourceIDs(session, text)
	if err != nil {
		return nil, err
	}
	var sources []activitySourceProvenance
	for _, id := range ids {
		source, err := store.activityDependencyProvenance(id, nil)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, errMeetingTranscriptNotReadable
			}
			return nil, err
		}
		if !policy.allows(source) {
			return nil, errMeetingTranscriptNotReadable
		}
		sources = append(sources, source)
	}
	return sources, nil
}

// Read content and authorize the complete dependency set together. Every direct
// transcript reader uses this path; a readable final slice cannot authorize an
// earlier protected slice, including one outside the requested digest day.
func (a *App) readMeetingTranscript(session ActivitySessionRow) (string, []activitySourceProvenance, error) {
	if a == nil || a.activity == nil || a.activity.store == nil {
		return "", nil, fmt.Errorf("activity store unavailable")
	}
	store := a.activity.store
	current, err := store.GetSessionByID(session.ID)
	if err != nil || current == nil {
		return "", nil, fmt.Errorf("transcript source unavailable")
	}
	session = *current
	policy, err := a.activityReadPrivacyPolicy()
	if err != nil {
		return "", nil, err
	}
	allowed, err := a.activitySessionAllowsWithPolicy(store, session, policy)
	if err != nil || !allowed {
		return "", nil, errMeetingTranscriptNotReadable
	}
	raw, err := os.ReadFile(session.TranscriptPath)
	if err != nil {
		return "", nil, err
	}
	sources, err := a.transcriptSourcesWithPolicy(store, session, string(raw), policy)
	if err != nil {
		return "", nil, err
	}
	return string(raw), sources, nil
}

// meetingAudioMetaEstablished reports whether a recording's sidecar says
// anything about the recording itself: its app, meeting, owner, sources or
// protection. TranscribedBy and DecodeFailed alone do not. A field added to
// meetingAudioMeta or activitySourceProvenance must be listed here
// (TestMeetingAudioMetaEstablishedNamesEveryField fails until it is), and a
// privacy field must also be checked by transcriptSourceIDs.
func meetingAudioMetaEstablished(meta meetingAudioMeta) bool {
	source := meta.CaptureSource
	return meta.App != "" || meta.BundleID != "" || meta.MeetingID != "" || meta.SessionID != "" ||
		meta.StartMs != 0 || meta.MonoSource != "" || meta.Protected || meta.PendingAudio ||
		len(meta.PendingSources) > 0 || len(meta.ActivitySessions) > 0 ||
		source.SessionID != "" || source.StartTime != 0 || source.EndTime != 0 || len(source.Apps) > 0 ||
		source.Private || source.Retained || source.MCPVerified || source.MCPSnapshots != nil
}
