package storage

import (
	"context"
	"testing"
)

func TestRemindersUpsertListVanishAndPrune(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	open := ReminderRow{UID: "open", Title: "Send the deck", DueAt: 5_000, List: "Work", Priority: 1}
	undated := ReminderRow{UID: "undated", Title: "Read the paper", List: "Personal"}
	done := ReminderRow{UID: "done", Title: "Book flights", DueAt: 1_000, CompletedAt: 2_000}
	if err := s.UpsertReminders(ctx, []ReminderRow{undated, open, done, {UID: " "}}, 10); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	live, err := s.ListReminders(ctx, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(live) != 2 || live[0].UID != "open" || live[1].UID != "undated" {
		t.Fatalf("open reminders, dated first: %+v", live)
	}
	if live[0].FirstSeenAt != 10 || live[0].Priority != 1 || live[0].List != "Work" {
		t.Fatalf("row %+v", live[0])
	}

	// A second read keeps first_seen_at and follows everything else.
	open.Title = "Send the final deck"
	open.CompletedAt = 3_000
	if err := s.UpsertReminders(ctx, []ReminderRow{open}, 30); err != nil {
		t.Fatalf("upsert again: %v", err)
	}
	all, _ := s.ListReminders(ctx, true)
	var got ReminderRow
	for _, row := range all {
		if row.UID == "open" {
			got = row
		}
	}
	if got.Title != "Send the final deck" || got.FirstSeenAt != 10 || got.UpdatedAt != 30 || got.CompletedAt != 3_000 {
		t.Fatalf("updated row %+v", got)
	}

	// The next read saw only "open": "undated" vanished; "done" was completed
	// before the window, so it is not marked.
	changed, err := s.MarkRemindersVanished(ctx, map[string]bool{"open": true}, 2_500, 4_000)
	if err != nil || changed != 1 {
		t.Fatalf("vanish: %d %v", changed, err)
	}
	all, _ = s.ListReminders(ctx, true)
	for _, row := range all {
		switch row.UID {
		case "undated":
			if row.DeletedAt != 4_000 {
				t.Fatalf("undated should be deleted: %+v", row)
			}
		case "done":
			if row.DeletedAt != 0 {
				t.Fatalf("done fell out of the window and must stay: %+v", row)
			}
		}
	}
	// Seen again: undeleted.
	if err := s.UpsertReminders(ctx, []ReminderRow{undated}, 5_000); err != nil {
		t.Fatalf("upsert undeleted: %v", err)
	}
	live, _ = s.ListReminders(ctx, false)
	if len(live) != 1 || live[0].UID != "undated" || live[0].DeletedAt != 0 {
		t.Fatalf("live after undelete %+v", live)
	}

	pruned, err := s.PruneReminders(ctx, 2_500)
	if err != nil || pruned != 1 {
		t.Fatalf("prune: %d %v", pruned, err)
	}
	all, _ = s.ListReminders(ctx, true)
	if len(all) != 2 {
		t.Fatalf("after prune %+v", all)
	}
}
