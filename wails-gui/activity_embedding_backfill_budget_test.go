package main

import (
	"errors"
	"fmt"
	"testing"
)

func TestEmbeddingBackfillBoundsDeniedScanAndEventuallyRevisits(t *testing.T) {
	isolateActivityPrivacyTest(t)
	a := newPrivacyReadTestApp(t)
	s := a.activity.store
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 204; i++ {
		apps := `["Private Notes"]`
		if i == 203 {
			apps = `["Notes"]`
		}
		id := fmt.Sprintf("budget-%03d", i)
		if _, err := tx.Exec(`INSERT INTO activity_sessions(id,start_time,end_time,trigger_kind,app_names,event_count,segment_count,created_at) VALUES(?,?,?,'test',?,0,0,?)`, id, i+1, i+2, apps, i+1); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(`INSERT INTO activity_ocr_frames(session_id,segment_id,frame_time,text) VALUES(?,0,1,'source')`, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	checks := 0
	gate := a.activityEmbeddingGate(s)
	counted := func(id string) (bool, error) { checks++; return gate(id) }
	for pass := 0; pass < 3; pass++ {
		checks = 0
		ids, err := s.listEmbeddingBackfillSessionIDs(t.Context(), counted)
		if err != nil {
			t.Fatal(err)
		}
		if checks > 100 {
			t.Fatalf("pass %d checked %d sources; budget is 100", pass, checks)
		}
		if pass < 2 && len(ids) != 0 {
			t.Fatalf("pass %d bypassed scan budget or privacy: %v", pass, ids)
		}
		if pass == 2 && (len(ids) != 1 || ids[0] != "budget-203") {
			t.Fatalf("readable tail starved behind denied backlog: %v", ids)
		}
	}
	// A privacy change must take effect when the cursor wraps. Denied work is
	// delayed, never permanently marked processed or made eligible by a cache.
	if _, err := s.db.Exec(`UPDATE activity_sessions SET app_names='["Notes"]' WHERE id='budget-000'`); err != nil {
		t.Fatal(err)
	}
	checks = 0
	ids, err := s.listEmbeddingBackfillSessionIDs(t.Context(), counted)
	if err != nil || len(ids) != 1 || ids[0] != "budget-000" || checks > 100 {
		t.Fatalf("cursor did not revisit newly readable source within budget: %v checks=%d err=%v", ids, checks, err)
	}
}

func TestEmbeddingBackfillReadErrorDoesNotAdvancePastSource(t *testing.T) {
	s := newTestStore(t)
	insertTestSession(t, s, "retry-source", "pending", 1000)
	if _, err := s.db.Exec(`INSERT INTO activity_ocr_frames(session_id,segment_id,frame_time,text) VALUES('retry-source',0,1,'source')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.listEmbeddingBackfillSessionIDs(t.Context(), func(string) (bool, error) { return false, errors.New("busy") }); err == nil {
		t.Fatal("read failure hidden")
	}
	ids, err := s.listEmbeddingBackfillSessionIDs(t.Context(), func(string) (bool, error) { return true, nil })
	if err != nil || len(ids) != 1 || ids[0] != "retry-source" {
		t.Fatalf("failed source was skipped: %v err=%v", ids, err)
	}
}
