package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// Named activity migration, with no numbered shared-storage schema claim.
func migrateMeetingTurns(tx *sql.Tx) error {
	_, err := tx.Exec(`
	CREATE TABLE IF NOT EXISTS meeting_call_speakers (
	 meeting_key TEXT NOT NULL REFERENCES meeting_notes(meeting_key) ON DELETE CASCADE,
	 speaker_id TEXT NOT NULL, name TEXT NOT NULL DEFAULT '', name_source TEXT NOT NULL DEFAULT '',
	 PRIMARY KEY(meeting_key,speaker_id));
	CREATE TABLE IF NOT EXISTS meeting_call_turns (
	 meeting_key TEXT NOT NULL REFERENCES meeting_notes(meeting_key) ON DELETE CASCADE,
	 session_id TEXT NOT NULL, turn_index INTEGER NOT NULL,
	 speaker_id TEXT NOT NULL, source TEXT NOT NULL, start_time INTEGER NOT NULL, end_time INTEGER NOT NULL,
	 text TEXT NOT NULL, PRIMARY KEY(session_id,turn_index));
	CREATE INDEX IF NOT EXISTS idx_meeting_call_turns_call ON meeting_call_turns(meeting_key,start_time);
	CREATE TABLE IF NOT EXISTS meeting_voice_signatures (
	 meeting_key TEXT NOT NULL REFERENCES meeting_notes(meeting_key) ON DELETE CASCADE,
	 speaker_id TEXT NOT NULL, name TEXT NOT NULL, model TEXT NOT NULL, embedding BLOB NOT NULL,
	 PRIMARY KEY(meeting_key,speaker_id));
	CREATE TABLE IF NOT EXISTS meeting_voice_state (id INTEGER PRIMARY KEY CHECK(id=1), generation INTEGER NOT NULL);
	INSERT OR IGNORE INTO meeting_voice_state VALUES(1,0);
	CREATE TRIGGER IF NOT EXISTS meeting_call_delete AFTER DELETE ON meeting_notes BEGIN
	 DELETE FROM meeting_call_turns WHERE meeting_key=OLD.meeting_key;
	 DELETE FROM meeting_call_speakers WHERE meeting_key=OLD.meeting_key;
	 DELETE FROM meeting_voice_signatures WHERE meeting_key=OLD.meeting_key;
	END;
	CREATE TRIGGER IF NOT EXISTS meeting_call_slice_delete AFTER DELETE ON meeting_note_slices BEGIN
	 DELETE FROM meeting_call_turns WHERE session_id=OLD.session_id;
	 DELETE FROM meeting_voice_signatures WHERE meeting_key=OLD.meeting_key;
	END;`)
	return err
}

// Speaker identity is independent of whether text came from the legacy file.
// Existing rows have no proven legacy provenance and must not trigger an audio
// rebuild merely because their speaker is unknown.
func migrateMeetingTurnProvenance(tx *sql.Tx) error {
	_, err := tx.Exec(`ALTER TABLE meeting_call_turns ADD COLUMN legacy_text INTEGER NOT NULL DEFAULT 0`)
	if err != nil && strings.Contains(err.Error(), "duplicate column") {
		return nil
	}
	return err
}

// These are the only call-speaker objects exported to the window. No embedding
// appears in a DTO, transcript file, event payload or shared identity handle.
type MeetingCallTurn struct {
	SpeakerID  string `json:"speakerId"`
	Name       string `json:"name"`
	NameSource string `json:"-"`
	LegacyText bool   `json:"-"`
	Number     int    `json:"number"`
	Source     string `json:"source"`
	StartTime  int64  `json:"startTime"`
	EndTime    int64  `json:"endTime"`
	Text       string `json:"text"`
}

type MeetingCallTurnsView struct {
	Turns []MeetingCallTurn `json:"turns"`
	// "ready" or "unavailable" (speaker attribution is limited).
	Status string `json:"status"`
}

type callVoiceSignature struct {
	Key, Speaker, Name string
	Embedding          []float32
}

func (s *ActivityStore) callVoiceGeneration() (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT generation FROM meeting_voice_state WHERE id=1`).Scan(&n)
	return n, err
}

func (s *ActivityStore) callTurns(sessionID string) ([]MeetingCallTurn, error) {
	rows, err := s.db.Query(`SELECT t.speaker_id,COALESCE(s.name,''),COALESCE(s.name_source,''),t.legacy_text,t.source,t.start_time,t.end_time,t.text
	FROM meeting_call_turns t LEFT JOIN meeting_call_speakers s ON s.meeting_key=t.meeting_key AND s.speaker_id=t.speaker_id
	WHERE t.session_id=? ORDER BY t.start_time,t.turn_index`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MeetingCallTurn{}
	for rows.Next() {
		var t MeetingCallTurn
		if err := rows.Scan(&t.SpeakerID, &t.Name, &t.NameSource, &t.LegacyText, &t.Source, &t.StartTime, &t.EndTime, &t.Text); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *ActivityStore) saveCallTurns(key, sessionID string, turns []MeetingCallTurn) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM meeting_note_slices WHERE meeting_key=? AND session_id=?`, key, sessionID).Scan(&exists); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM meeting_call_turns WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	for i, t := range turns {
		if strings.HasPrefix(t.NameSource, "voice:") {
			var exists int
			if err := tx.QueryRow(`SELECT 1 FROM meeting_voice_signatures WHERE meeting_key=? AND name=? LIMIT 1`, strings.TrimPrefix(t.NameSource, "voice:"), t.Name).Scan(&exists); err != nil {
				t.Name = ""
				t.NameSource = ""
			}
		}
		if _, err := tx.Exec(`INSERT INTO meeting_call_speakers(meeting_key,speaker_id,name,name_source) VALUES(?,?,?,?) ON CONFLICT(meeting_key,speaker_id) DO NOTHING`, key, t.SpeakerID, t.Name, t.NameSource); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO meeting_call_turns(meeting_key,session_id,turn_index,speaker_id,source,start_time,end_time,text,legacy_text) VALUES(?,?,?,?,?,?,?,?,?)`, key, sessionID, i, t.SpeakerID, t.Source, t.StartTime, t.EndTime, t.Text, t.LegacyText); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *ActivityStore) callSignatures() ([]callVoiceSignature, error) {
	rows, err := s.db.Query(`SELECT meeting_key,speaker_id,name,embedding FROM meeting_voice_signatures WHERE model=?`, callVoiceModel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []callVoiceSignature
	for rows.Next() {
		var v callVoiceSignature
		var data []byte
		if err := rows.Scan(&v.Key, &v.Speaker, &v.Name, &data); err != nil {
			return nil, err
		}
		if json.Unmarshal(data, &v.Embedding) == nil {
			out = append(out, v)
		}
	}
	return out, rows.Err()
}

// Name and enrollment commit together. A Forget racing slow extraction changes
// generation, so that already-started rename cannot put a signature back.
func (s *ActivityStore) renameCallSpeaker(key, id, name string, embedding []float32, generation int64) error {
	name = strings.TrimSpace(name)
	if id == "you" || name == "" || len([]rune(name)) > 100 || strings.ContainsAny(name, "\n\r\x00") {
		return errors.New("invalid speaker name")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE meeting_call_speakers SET name=?,name_source='user' WHERE meeting_key=? AND speaker_id=?`, name, key, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	// Replace only this call's teaching sample, keeping independent samples
	// from other calls (and their provenance) separate.
	if len(embedding) > 0 {
		if _, err := tx.Exec(`DELETE FROM meeting_voice_signatures WHERE meeting_key=? AND speaker_id=?`, key, id); err != nil {
			return err
		}
		data, err := json.Marshal(embedding)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO meeting_voice_signatures SELECT ?,?,?,?,? WHERE (SELECT generation FROM meeting_voice_state WHERE id=1)=?`, key, id, name, callVoiceModel, data, generation); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(`UPDATE meeting_voice_signatures SET name=? WHERE meeting_key=? AND speaker_id=?`, name, key, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *ActivityStore) forgetCallVoices() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE meeting_voice_state SET generation=generation+1 WHERE id=1`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM meeting_voice_signatures`); err != nil {
		return err
	}
	// Older builds cached microphone embeddings in general event JSON.
	// New events omit them entirely; erase the old copies on Forget too.
	if _, err := tx.Exec(`UPDATE activity_events SET data=json_remove(data,'$.speechSpeakerEmbedding') WHERE json_valid(data) AND json_type(data,'$.speechSpeakerEmbedding') IS NOT NULL`); err != nil {
		return err
	}
	return tx.Commit()
}
