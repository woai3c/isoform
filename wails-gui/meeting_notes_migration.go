package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Meeting notes used to be Knowledge entries (source "meeting"). They move
// to their meetings on Activity once: each entry becomes the meeting's notes
// row, its activity sessions become slices, the decisions and follow-ups the
// after-call pass stored for those sessions become the outline, and anything
// the user wrote in the entry is kept as the meeting's own notes: every
// line Isoform cannot show it generated. The entry leaves Knowledge only
// after its move is committed. Knowledge learned elsewhere from the same
// sessions stays.

// meetingNotesMovedStateKey marks the move as done in ui_state.
const meetingNotesMovedStateKey = "meeting_notes_moved_to_activity_v1"

var (
	legacyMeetingNotesTitleRE  = regexp.MustCompile(`^Meeting notes: .+, [A-Z][a-z]{2} \d{1,2} \d{2}:\d{2}$`)
	legacyMeetingNotesHeaderRE = regexp.MustCompile(`^\*\*.+ call\*\* · .+ · (\d+) words$`)
	legacyMeetingNotesHeadRE   = regexp.MustCompile(`^## Meeting notes( \(continued[^)]*\))?$`)
	legacyMeetingNotesPartRE   = regexp.MustCompile(`^<!-- (/)?meeting-session( [^ ]+ [0-9]+)? -->$`)
	transcriptLineRE           = regexp.MustCompile(`^\[\d{2}:\d{2}:\d{2}\] `)
)

// moveMeetingNotesToActivity runs the move once. A failed entry stays in
// Knowledge and the move is tried again at the next start.
func (a *App) moveMeetingNotesToActivity(ctx context.Context) {
	if a == nil || a.store == nil || a.activity == nil || a.activity.store == nil {
		return
	}
	if done, err := a.store.GetUIState(ctx, meetingNotesMovedStateKey); err != nil || done != "" {
		return
	}
	dir, err := knowledgeDir()
	if err != nil {
		return
	}
	moved, kept, err := a.moveMeetingNotesEntries(ctx, dir)
	if err != nil {
		log.Printf("[meeting-notes] move to Activity: %v (%d moved, %d kept)", err, moved, kept)
		return
	}
	if moved > 0 || kept > 0 {
		log.Printf("[meeting-notes] moved %d meeting note(s) to Activity, %d left in Knowledge", moved, kept)
	}
	if err := a.store.SetUIState(ctx, meetingNotesMovedStateKey, time.Now().UTC().Format(time.RFC3339)); err != nil {
		log.Printf("[meeting-notes] move marker: %v", err)
	}
}

// moveMeetingNotesEntries moves every meeting-notes entry in dir. kept
// counts entries with no activity session to move to: they stay in
// Knowledge. A non-nil error means some entry failed and the move must run
// again.
func (a *App) moveMeetingNotesEntries(ctx context.Context, dir string) (moved, kept int, err error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0, err
	}
	var failures []string
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".md") {
			continue
		}
		entryID := strings.TrimSuffix(f.Name(), ".md")
		ok, notMeeting, moveErr := a.moveMeetingNotesEntry(ctx, dir, entryID)
		if moveErr != nil {
			failures = append(failures, entryID)
			log.Printf("[meeting-notes] move %s: %v", entryID, moveErr)
		} else if ok {
			moved++
		} else if !notMeeting {
			kept++
		}
	}
	if len(failures) > 0 {
		return moved, kept, fmt.Errorf("%d entr(ies) not moved", len(failures))
	}
	return moved, kept, nil
}

// moveMeetingNotesEntry moves one entry. It reports moved when the entry is
// now on Activity and gone from Knowledge, and notMeeting when the file is
// not a meeting-notes entry; neither means the entry stays in Knowledge.
func (a *App) moveMeetingNotesEntry(ctx context.Context, dir, entryID string) (moved, notMeeting bool, err error) {
	unlock := memoryFileLocks.Acquire("knowledge:" + entryID)
	defer unlock()
	path := filepath.Join(dir, entryID+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, true, nil
		}
		return false, false, err
	}
	doc, ok := parseOKFDocument(string(raw))
	if !ok || okfString(doc.Frontmatter, "source") != meetingKnowledgeSource {
		return false, true, nil
	}
	sessions := okfStringSlice(doc.Frontmatter, "activity_sessions")
	if len(sessions) == 0 {
		// Nothing on Activity to attach the notes to.
		return false, false, nil
	}
	store := a.activity.store
	legacy := a.legacyMeetingNotes(ctx, entryID, doc, sessions)
	if err := store.saveLegacyMeetingNotes(legacy); err != nil {
		return false, false, err
	}
	// The old entry was indexed into memory and triage already learned from
	// the call: moving it is not a reason to learn it again. A part added
	// later changes the notes and is learned. Which kinds were on at the
	// time is unknown, so the empty kinds count as both.
	if r, found, err := store.meetingNotesByKey(legacy.Header.Key); err == nil && found && r.LearnedHash == "" {
		if err := store.markMeetingNotesLearned(r.Key, meetingNotesLearningHash(meetingNotesLearningText(r)), ""); err != nil {
			return false, false, err
		}
	}
	// The move is committed: the entry leaves Knowledge. Its recording and
	// transcript stay; they belong to the meeting now. Its revision history
	// stays on disk too (memory does not index it) until the user deletes
	// the meeting's notes.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return false, false, fmt.Errorf("remove moved entry: %w", err)
	}
	a.markMemoryDirty("meeting-notes-moved")
	return true, false, nil
}

// legacyMeetingNotesRecord is a Knowledge entry read for the move.
type legacyMeetingNotesRecord struct {
	Header    meetingNotesHeader
	EntryID   string
	UserNotes string
	Slices    []meetingNoteSlice
}

// legacyMeetingNotes reads an entry, its sessions and their transcripts.
func (a *App) legacyMeetingNotes(ctx context.Context, entryID string, doc okfDocument, sessions []string) legacyMeetingNotesRecord {
	store := a.activity.store
	rec := legacyMeetingNotesRecord{EntryID: entryID}
	if apps := okfStringSlice(doc.Frontmatter, "apps"); len(apps) > 0 {
		rec.Header.MeetingApp = apps[0]
	}
	entryStart := parseOKFTimestampMs(okfString(doc.Frontmatter, "timestamp"))
	transcriptLines := map[string]bool{}
	transcriptsRead := true
	keyCandidates := []string{}
	for _, id := range sessions {
		sl := meetingNoteSlice{SessionID: id, StartTime: entryStart, EndTime: entryStart}
		row, err := store.GetSessionByID(id)
		if err == nil && row != nil {
			sl.StartTime, sl.EndTime = row.StartTime, row.EndTime
			if rec.Header.MeetingApp == "" {
				rec.Header.MeetingApp = row.MeetingApp
			}
			if row.AudioPath != "" {
				if meta := readMeetingWAVMeta(store.AudioDir(), row.AudioPath); meta.MeetingID != "" {
					keyCandidates = append(keyCandidates, meta.MeetingID)
				}
			}
		}
		transcript := ""
		if row != nil && row.TranscriptPath != "" {
			if b, err := os.ReadFile(row.TranscriptPath); err == nil {
				transcript = string(b)
			}
		}
		if transcript == "" {
			transcriptsRead = false
		} else {
			if id := parseTranscriptMeetingID(transcript); id != "" {
				keyCandidates = append(keyCandidates, id)
			}
			for _, line := range strings.Split(stripTranscriptHeader(transcript), "\n") {
				if line = strings.TrimSpace(line); line != "" {
					transcriptLines[line] = true
					sl.WordCount += len(strings.Fields(stripTranscriptLinePrefix(line)))
				}
			}
		}
		if a.store != nil {
			if fu, found, err := a.store.GetMeetingFollowup(ctx, id); err == nil && found {
				_ = json.Unmarshal([]byte(fu.DecisionsJSON), &sl.Decisions)
				_ = json.Unmarshal([]byte(fu.FollowupsJSON), &sl.Followups)
				if rec.Header.Title == "" {
					rec.Header.Title = strings.TrimSpace(fu.Title)
				}
			}
		}
		rec.Slices = append(rec.Slices, sl)
	}
	// The entry id is the hash of the meeting key: the meeting id the call
	// carried, or its first session. Use whichever produced this entry, so a
	// later slice of the same call finds these notes.
	rec.Header.Key = sessions[0]
	for _, candidate := range append(keyCandidates, sessions...) {
		if meetingKnowledgeEntryID(candidate) == entryID {
			rec.Header.Key = candidate
			break
		}
	}
	if rec.Header.Key != sessions[0] {
		rec.Header.MeetingID = rec.Header.Key
	}
	if len(rec.Slices) == 1 && rec.Slices[0].WordCount == 0 {
		rec.Slices[0].WordCount = legacyMeetingNotesWordCount(doc.Body)
	}
	for _, sl := range rec.Slices {
		if rec.Header.StartTime <= 0 || (sl.StartTime > 0 && sl.StartTime < rec.Header.StartTime) {
			rec.Header.StartTime = sl.StartTime
		}
		if sl.EndTime > rec.Header.EndTime {
			rec.Header.EndTime = sl.EndTime
		}
	}
	// A title the user typed is theirs; the generated one is not kept.
	if title := okfString(doc.Frontmatter, "title"); title != "" && !legacyMeetingNotesTitleRE.MatchString(title) {
		rec.Header.Title = title
	}
	var known map[string]bool
	if transcriptsRead {
		known = transcriptLines
	}
	rec.UserNotes = legacyMeetingNotesUserText(doc.Body, known)
	return rec
}

// legacyMeetingNotesWordCount reads the word count from the call header line.
func legacyMeetingNotesWordCount(body string) int {
	total := 0
	for _, line := range strings.Split(body, "\n") {
		if m := legacyMeetingNotesHeaderRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			n, _ := strconv.Atoi(m[1])
			total += n
		}
	}
	return total
}

// legacyMeetingNotesUserText returns what the user wrote in a notes entry:
// every block of the body that Isoform did not generate. Generated are the
// "## Meeting notes" headings, the call header line, the slice markers and
// the transcript lines. transcriptLines holds the lines of the meeting's
// transcripts; a line found there is generated, and a transcript line the
// user changed no longer matches and is kept as theirs. When it is nil (a
// transcript is gone), only a timestamped transcript line counts as
// generated: every other line is kept, inside a slice's markers too.
func legacyMeetingNotesUserText(body string, transcriptLines map[string]bool) string {
	var blocks []string
	var current []string
	flush := func() {
		if len(current) > 0 {
			blocks = append(blocks, strings.Join(current, "\n"))
			current = nil
		}
	}
	for _, rawLine := range strings.Split(body, "\n") {
		line := strings.TrimSpace(rawLine)
		generated := line == "" ||
			legacyMeetingNotesHeadRE.MatchString(line) ||
			legacyMeetingNotesHeaderRE.MatchString(line) ||
			legacyMeetingNotesPartRE.MatchString(line)
		if !generated {
			if transcriptLines != nil {
				generated = transcriptLines[line]
			} else {
				generated = transcriptLineRE.MatchString(line)
			}
		}
		if generated {
			flush()
			continue
		}
		current = append(current, strings.TrimRight(rawLine, " \t"))
	}
	flush()
	return strings.Join(blocks, "\n\n")
}

// saveLegacyMeetingNotes writes a moved entry in one transaction. When the
// meeting already has notes (a slice written after the entry), the slices
// and outline it has stay and the user's text is appended once.
func (s *ActivityStore) saveLegacyMeetingNotes(rec legacyMeetingNotesRecord) error {
	if s == nil || s.db == nil {
		return errors.New("activity store unavailable")
	}
	now := time.Now().UnixMilli()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertMeetingNotesHeader(tx, rec.Header, rec.Header.StartTime, rec.Header.EndTime, now); err != nil {
		return err
	}
	if err := appendMeetingUserNotes(tx, rec.Header.Key, rec.UserNotes, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE meeting_notes SET legacy_knowledge_id = ?, updated_at = ? WHERE meeting_key = ?`,
		rec.EntryID, now, rec.Header.Key); err != nil {
		return err
	}
	for _, sl := range rec.Slices {
		decisions, _ := json.Marshal(nonNilStrings(sl.Decisions))
		followups := sl.Followups
		if followups == nil {
			followups = []meetingFollowup{}
		}
		followupsJSON, _ := json.Marshal(followups)
		// A slice the notes store already has keeps what it has; a pending
		// one (an outline without a meeting) joins this meeting.
		if _, err := tx.Exec(`
			INSERT INTO meeting_note_slices (session_id, meeting_key, start_time, end_time, word_count, decisions_json, followups_json, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(session_id) DO UPDATE SET meeting_key = excluded.meeting_key,
				start_time = excluded.start_time, end_time = excluded.end_time, word_count = excluded.word_count
			WHERE meeting_note_slices.meeting_key = ''`,
			sl.SessionID, rec.Header.Key, sl.StartTime, sl.EndTime, sl.WordCount, string(decisions), string(followupsJSON), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}
