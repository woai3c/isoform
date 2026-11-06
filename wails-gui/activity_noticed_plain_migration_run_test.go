package main

import (
	"encoding/json"
	"testing"
)

// The invoice wording repair has no schema marker, so bootstrap leaves it
// unrecorded. Production then applies it through runPendingMigrations: on a
// fresh install (empty log) and on the next launch of an upgraded database
// whose log lacks it. Both paths must repair saved wording exactly once.
func TestNoticedPlainInvoiceMigrationRunsOnNextLaunch(t *testing.T) {
	const name = "noticed_plain_invoice_followup"
	a := newTodayContextTestApp(t, true)
	store := a.activity.store
	db := store.db

	var recorded int
	if err := db.QueryRow(`SELECT count(*) FROM migration_log WHERE name = ?`, name).Scan(&recorded); err != nil || recorded != 1 {
		t.Fatalf("fresh install did not record %s: count=%d err=%v", name, recorded, err)
	}

	old := "Send Dana at Northwind your invoice payment check-in"
	raw, _ := json.Marshal([]NoticedSuggestion{{ID: "run-id", Kind: noticedKindFollowUp, MomentID: "moment", Text: old}})
	if _, err := db.Exec(`INSERT INTO activity_noticed(date, language, generation, items, created_at) VALUES('2026-11-05','en',1,?,1)`, string(raw)); err != nil {
		t.Fatal(err)
	}
	// Simulate an upgraded database whose log predates this migration.
	if _, err := db.Exec(`DELETE FROM migration_log WHERE name = ?`, name); err != nil {
		t.Fatal(err)
	}
	store.runPendingMigrations()

	if err := db.QueryRow(`SELECT count(*) FROM migration_log WHERE name = ?`, name).Scan(&recorded); err != nil || recorded != 1 {
		t.Fatalf("next launch did not record %s: count=%d err=%v", name, recorded, err)
	}
	var saved string
	if err := db.QueryRow(`SELECT items FROM activity_noticed WHERE date='2026-11-05' AND language='en'`).Scan(&saved); err != nil {
		t.Fatal(err)
	}
	var got []NoticedSuggestion
	if err := json.Unmarshal([]byte(saved), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "run-id" || got[0].Text != "Ask Dana at Northwind when the invoice will be paid" {
		t.Fatalf("upgrade did not repair wording: %+v", got)
	}
}
