package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Meeting notes used to be Knowledge entries (OKF source "meeting", id
// meeting-<hash of the meeting key>). They now live with their meetings on
// Activity (meeting_notes_store.go) and old entries move there once
// (meeting_notes_migration.go). What stays here is shared by both: the
// meeting key, the old entry id it hashes to, the publish markers startup
// recovery reads, and forgetting a call's recordings.

// meetingKnowledgeSource is the OKF source tag of an old meeting-notes entry.
const meetingKnowledgeSource = "meeting"

// meetingNotesMarkerDir, under the knowledge folder, holds one empty file per
// meeting whose notes were published to Knowledge or deleted on Activity.
// Startup recovery uses it to tell "the user deleted these notes" from "the
// notes were never written".
const meetingNotesMarkerDir = ".meeting-notes"

// meetingNotesKey is what a meeting's notes are keyed by: the meeting id
// when the activity monitor has one (a call the user rejoins keeps it), the
// activity session otherwise.
func meetingNotesKey(session ActivitySessionRow, meetingID string) string {
	if id := strings.TrimSpace(meetingID); id != "" {
		return id
	}
	return session.ID
}

// meetingKnowledgeEntryID is the old Knowledge entry id of a meeting key; the
// publish markers and the recordings' sidecars are matched by it.
func meetingKnowledgeEntryID(key string) string {
	hash := sha256.Sum256([]byte(key))
	return fmt.Sprintf("meeting-%x", hash[:8])
}

// meetingNotesMarkerPath is the file that records a publish or a deletion.
func meetingNotesMarkerPath(dir, entryID string) string {
	return filepath.Join(dir, meetingNotesMarkerDir, entryID)
}

// markMeetingNotesPublished records that the notes were published once.
func markMeetingNotesPublished(dir, entryID string) error {
	if err := os.MkdirAll(filepath.Join(dir, meetingNotesMarkerDir), 0o700); err != nil {
		return err
	}
	return os.WriteFile(meetingNotesMarkerPath(dir, entryID), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
}

// meetingNotesPublished reports whether the notes were published before, so
// notes missing now were deleted by the user.
func meetingNotesPublished(key string) bool {
	dir, err := knowledgeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(meetingNotesMarkerPath(dir, meetingKnowledgeEntryID(key)))
	return err == nil
}

func stringSliceContains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// forgetMeetingRecordings deletes the call behind an old meeting-notes
// entry the user deletes from Knowledge (one the move left there): the
// recording and the transcript of every activity session it was written
// from. The publish marker stays, so nothing writes the notes again.
func (a *App) forgetMeetingRecordings(entryContent string) {
	doc, ok := parseOKFDocument(entryContent)
	if !ok || okfString(doc.Frontmatter, "source") != meetingKnowledgeSource || a.activity == nil || a.activity.store == nil {
		return
	}
	sessions := okfStringSlice(doc.Frontmatter, "activity_sessions")
	entryID := strings.TrimPrefix(okfString(doc.Frontmatter, "resource"), "yansu://knowledge/")
	a.forgetMeetingCallFiles(sessions, entryID)
}

// forgetMeetingAudioFiles removes every recording of the meeting whose
// notes entry is entryID that no session row links: raw tracks a crash or a
// failed finalize left (they would come back as a call at the next start)
// and finished recordings not yet transcribed. The recording in progress is
// left alone.
func (a *App) forgetMeetingAudioFiles(audioDir, entryID string) {
	if audioDir == "" || entryID == "" {
		return
	}
	live := map[string]bool{}
	if am := a.activity; am != nil {
		am.mu.Lock()
		for _, w := range []*meetingAudioWriter{am.meetingAudioWriter, am.meetingCandidateAudioWriter} {
			if w != nil {
				live[w.metaPath()] = true
			}
		}
		am.mu.Unlock()
	}
	// A recording a session row still links stays: its row owns it (the
	// notes' own sessions were cleared before this runs). When the rows
	// cannot be read, nothing is removed.
	referenced := map[string]bool{}
	if am := a.activity; am != nil && am.store != nil {
		refs, err := am.store.referencedAudioPaths()
		if err != nil {
			log.Printf("[meeting-notes] recordings of %s kept: session rows unreadable: %v", entryID, err)
			return
		}
		referenced = refs
	}
	sidecars, _ := filepath.Glob(filepath.Join(audioDir, "*", "*.json"))
	for _, path := range sidecars {
		if live[path] {
			continue
		}
		var meta meetingAudioMeta
		raw, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(raw, &meta) != nil || meta.MeetingID == "" {
			continue
		}
		if meetingKnowledgeEntryID(meetingNotesKey(ActivitySessionRow{}, meta.MeetingID)) != entryID {
			continue
		}
		base := strings.TrimSuffix(path, ".json")
		rel, _ := filepath.Rel(audioDir, base)
		if referenced[rel+".wav"] || referenced[rel+meetingAudioOpusExt] {
			continue
		}
		for _, file := range []string{base + ".mic.pcm", base + ".sys.pcm", base + ".wav",
			base + meetingAudioOpusExt, base + meetingAudioOpusExt + ".tmp"} {
			if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
				log.Printf("[meeting-notes] remove %s: %v", filepath.Base(file), err)
			}
			meetingDecodeFailures.Delete(file)
		}
		if err := removeMeetingSidecar(path); err != nil {
			log.Printf("[meeting-notes] remove %s: %v", filepath.Base(path), err)
		}
	}
}
