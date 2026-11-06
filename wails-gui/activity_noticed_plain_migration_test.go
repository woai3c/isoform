package main

import (
	"encoding/json"
	"testing"
)

func TestNoticedPlainInvoiceMigration(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	old := "Send Michael at SalesRabbit your invoice payment check-in"
	item := NoticedSuggestion{ID: "kept-id", Kind: noticedKindFollowUp, MomentID: "moment", Text: old}
	raw, _ := json.Marshal([]NoticedSuggestion{item})
	db := a.activity.store.db
	if _, err := db.Exec(`INSERT INTO activity_noticed(date, language, generation, items, created_at) VALUES('2026-11-04','en',1,?,1)`, string(raw)); err != nil {
		t.Fatal(err)
	}
	oldKey := "text:follow_up:" + noticedNormalize(old)
	if _, err := db.Exec(`INSERT INTO activity_noticed_dismissals(key, created_at) VALUES(?,1)`, oldKey); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := migrateNoticedPlainInvoiceFollowup(tx); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	var saved string
	if err := db.QueryRow(`SELECT items FROM activity_noticed WHERE date='2026-11-04' AND language='en'`).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	var got []NoticedSuggestion
	if err := json.Unmarshal([]byte(saved), &got); err != nil {
		t.Fatal(err)
	}
	want := "Ask Michael at SalesRabbit when the invoice will be paid"
	if len(got) != 1 || got[0].ID != item.ID || got[0].MomentID != item.MomentID || got[0].Text != want {
		t.Fatalf("saved = %+v", got)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM activity_noticed_dismissals WHERE key=?`, "text:follow_up:"+noticedNormalize(want)).Scan(&count); err != nil || count != 1 {
		t.Fatalf("dismissal count = %d, %v", count, err)
	}
	if plainInvoiceFollowup("Send the invoice") != "Send the invoice" {
		t.Fatal("changed unrelated wording")
	}
}
