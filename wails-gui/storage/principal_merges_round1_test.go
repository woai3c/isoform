package storage

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPeopleRound1SequentialMergesAndUndoEitherOrder(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(map[bool]string{false: "oldest-first", true: "newest-first"}[reverse], func(t *testing.T) {
			s := newTestStore(t)
			target := mkPrincipal(t, s, PrincipalKindContact, "Target")
			a := mkPrincipal(t, s, PrincipalKindContact, "A")
			b := mkPrincipal(t, s, PrincipalKindContact, "B")
			for i, p := range []PrincipalRow{target, a, b} {
				p.Aliases = []string{"shared", p.ID}
				if _, err := s.UpdatePrincipal(ctx, p); err != nil {
					t.Fatal(err)
				}
				if err := s.BumpPrincipalSighting(ctx, p.ID, "wechat", "shared-session", int64(100+i*10), p.ID); err != nil {
					t.Fatal(err)
				}
			}
			before := peopleSnapshot(t, s, target.ID, a.ID, b.ID)
			first, _, err := s.MergePeople(ctx, target.ID, []string{a.ID})
			if err != nil {
				t.Fatal(err)
			}
			second, _, err := s.MergePeople(ctx, target.ID, []string{b.ID})
			if err != nil {
				t.Fatalf("second merge into same person: %v", err)
			}
			ids := []string{first, second}
			if reverse {
				ids = []string{second, first}
			}
			for _, id := range ids {
				if err := s.UndoMerge(ctx, id); err != nil {
					t.Fatal(err)
				}
			}
			if got := peopleSnapshot(t, s, target.ID, a.ID, b.ID); !bytes.Equal(got, before) {
				t.Fatalf("sequential Undo changed original rows\nbefore %s\nafter %s", before, got)
			}
		})
	}
}

func TestListPrincipalSessionSightingsForActivePrincipalsSortsTies(t *testing.T) {
	s := newTestStore(t)
	p := mkPrincipal(t, s, PrincipalKindContact, "Sightings")
	for _, sessionID := range []string{"tie-z", "tie-a"} {
		if err := s.BumpPrincipalSighting(ctx, p.ID, "wechat", sessionID, 100, ""); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := s.ListPrincipalSessionSightingsForActivePrincipals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := batch[p.ID]
	want, err := s.ListPrincipalSessionSightingsPage(ctx, p.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("batch sightings = %+v, per-principal = %+v", got, want)
	}
	if len(got) != 2 || got[0].LastSeenAt != got[1].LastSeenAt || got[0].SessionID != "tie-a" {
		t.Fatalf("tie ordering = %+v", got)
	}
}

func TestPeopleRound1MergedTargetCanBecomeSource(t *testing.T) {
	s := newTestStore(t)
	a := mkPrincipal(t, s, PrincipalKindContact, "A")
	b := mkPrincipal(t, s, PrincipalKindContact, "B")
	c := mkPrincipal(t, s, PrincipalKindContact, "C")
	s.BumpPrincipalSighting(ctx, a.ID, "wechat", "shared", 10, "A")
	s.BumpPrincipalSighting(ctx, b.ID, "wechat", "shared", 20, "B")
	before := peopleSnapshot(t, s, a.ID, b.ID, c.ID)
	first, _, err := s.MergePeople(ctx, b.ID, []string{a.ID})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.MergePeople(ctx, c.ID, []string{b.ID})
	if err != nil {
		t.Fatalf("merge from prior target: %v", err)
	}
	if err := s.UndoMerge(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := s.UndoMerge(ctx, first); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, peopleSnapshot(t, s, a.ID, b.ID, c.ID)) {
		t.Fatal("chain Undo failed to restore owners")
	}
}

func TestPeopleRound1UndoKeepsLaterMessagesAndRename(t *testing.T) {
	s := newTestStore(t)
	target := mkPrincipal(t, s, PrincipalKindContact, "Target")
	source := mkPrincipal(t, s, PrincipalKindContact, "Source")
	if err := s.BumpPrincipalSighting(ctx, source.ID, "wechat", "shared-session", 100, "source preview"); err != nil {
		t.Fatal(err)
	}
	merge, _, err := s.MergePeople(ctx, target.ID, []string{source.ID})
	if err != nil {
		t.Fatal(err)
	}
	// An empty preview still represents a real message; reconstruct its time
	// without relying on the rolling preview window.
	for _, at := range []int64{200, 300} {
		if err := s.BumpPrincipalSighting(ctx, target.ID, "wechat", "shared-session", at, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.BumpPrincipalSighting(ctx, target.ID, "telegram", "new-session", 400, "later preview"); err != nil {
		t.Fatal(err)
	}
	target.DisplayName = "Renamed"
	target.Notes = "new user note"
	edited, err := s.UpdatePrincipal(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UndoMerge(ctx, merge); err != nil {
		t.Fatalf("ordinary activity disabled Undo: %v", err)
	}
	got, err := s.GetPrincipal(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != edited.DisplayName || got.Notes != edited.Notes || got.UpdatedAt != edited.UpdatedAt {
		t.Fatalf("profile edit lost: %+v", got)
	}
	stats, err := s.GetPrincipalStats(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalMessages != 3 {
		t.Fatalf("target message count=%d", stats.TotalMessages)
	}
	for _, app := range stats.ByApp {
		if app.App == "wechat" && (app.FirstSeenAt != 200 || app.LastSeenAt != 300 || app.MessageCount != 2) {
			t.Fatalf("message delta lost: %+v", app)
		}
	}
	sessions, err := s.ListPrincipalSessionSightings(ctx, target.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if session.SessionID == "shared-session" && (session.FirstSeenAt != 200 || session.LastSeenAt != 300) {
			t.Fatalf("session delta lost: %+v", session)
		}
	}
	sourceStats, err := s.GetPrincipalStats(ctx, source.ID)
	if err != nil || sourceStats.TotalMessages != 1 {
		t.Fatalf("source stats=%+v %v", sourceStats, err)
	}
}

func TestPeopleRound1DeletePurgesHiddenSourcesAndReceipts(t *testing.T) {
	s := newTestStore(t)
	target := mkPrincipal(t, s, PrincipalKindContact, "Target")
	source := mkPrincipal(t, s, PrincipalKindContact, "Source")
	control := mkPrincipal(t, s, PrincipalKindContact, "Control")
	if _, err := s.CreateVoiceEmbedding(ctx, VoiceEmbeddingRow{PrincipalID: source.ID, Embedding: []float32{1, 2}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePrincipalResearch(ctx, PrincipalResearchRow{PrincipalID: source.ID, Summary: "private source dossier"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindAppHandle(ctx, source.ID, "wechat", "private-id", "Private"); err != nil {
		t.Fatal(err)
	}
	if err := s.BumpPrincipalSighting(ctx, source.ID, "wechat", "session", 10, "private preview"); err != nil {
		t.Fatal(err)
	}
	merge, _, err := s.MergePeople(ctx, target.ID, []string{source.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePrincipal(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`SELECT COUNT(*) FROM principals WHERE id IN (?,?)`,
		`SELECT COUNT(*) FROM principal_voice_embeddings WHERE principal_id IN (?,?)`,
		`SELECT COUNT(*) FROM principal_research WHERE principal_id IN (?,?)`,
		`SELECT COUNT(*) FROM app_handles WHERE principal_id IN (?,?)`,
		`SELECT COUNT(*) FROM principal_recent_messages WHERE principal_id IN (?,?)`,
	} {
		var count int
		if err := s.db.QueryRow(query, target.ID, source.ID).Scan(&count); err != nil || count != 0 {
			t.Errorf("retained private rows: %s => %d, %v", query, count, err)
		}
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM principal_merges WHERE id=?`, merge).Scan(&count); err != nil || count != 0 {
		t.Errorf("retained merge receipt: %d %v", count, err)
	}
	if _, err := s.GetPrincipal(ctx, control.ID); err != nil {
		t.Fatal("unrelated person removed", err)
	}
}

func TestPeopleRound1MigrationFromVersion64(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	p := mkPrincipal(t, s, PrincipalKindContact, "manual v64 person")
	if _, err := s.db.Exec(`DROP TABLE principal_merge_updates; DROP TABLE principal_merges`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE principals DROP COLUMN merged_into`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`PRAGMA user_version=64`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = New(path)
	if err != nil {
		t.Fatalf("v64 to v65: %v", err)
	}
	defer s.Close()
	if version, err := checkSchemaVersion(s.db); err != nil || version != schemaVersion {
		t.Fatalf("schema=%d %v", version, err)
	}
	if _, err := s.GetPrincipal(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	other := mkPrincipal(t, s, PrincipalKindContact, "other")
	merge, _, err := s.MergePeople(ctx, p.ID, []string{other.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UndoMerge(ctx, merge); err != nil {
		t.Fatal(err)
	}
}

func TestPeopleRound1UnknownUndoIsTyped(t *testing.T) {
	s := newTestStore(t)
	var state *MergeStateError
	if err := s.UndoMerge(ctx, "missing"); !errors.As(err, &state) || state.Code != "merge_not_found" {
		t.Fatalf("missing Undo=%v", err)
	}
}

func TestPeopleRound1SequentialUndoRetainsObservationsBetweenMerges(t *testing.T) {
	for _, oldestFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "oldest-first", false: "newest-first"}[oldestFirst], func(t *testing.T) {
			s := newTestStore(t)
			target := mkPrincipal(t, s, PrincipalKindContact, "Target")
			a := mkPrincipal(t, s, PrincipalKindContact, "A")
			b := mkPrincipal(t, s, PrincipalKindContact, "B")
			for i, p := range []PrincipalRow{a, b} {
				if err := s.BumpPrincipalSighting(ctx, p.ID, "wechat", "shared", int64(100+i*10), p.DisplayName); err != nil {
					t.Fatal(err)
				}
			}
			first, _, err := s.MergePeople(ctx, target.ID, []string{a.ID})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.BumpPrincipalSighting(ctx, target.ID, "wechat", "shared", 200, ""); err != nil {
				t.Fatal(err)
			}
			second, _, err := s.MergePeople(ctx, target.ID, []string{b.ID})
			if err != nil {
				t.Fatal(err)
			}
			ids := []string{first, second}
			if !oldestFirst {
				ids = []string{second, first}
			}
			if err := s.UndoMerge(ctx, ids[0]); err != nil {
				t.Fatal(err)
			}
			if err := s.BumpPrincipalSighting(ctx, target.ID, "wechat", "shared", 300, ""); err != nil {
				t.Fatal(err)
			}
			if err := s.UndoMerge(ctx, ids[1]); err != nil {
				t.Fatal(err)
			}
			stats, err := s.GetPrincipalStats(ctx, target.ID)
			if err != nil || stats.TotalMessages != 2 || len(stats.ByApp) != 1 || stats.ByApp[0].FirstSeenAt != 200 || stats.ByApp[0].LastSeenAt != 300 {
				t.Fatalf("later observations lost: %+v %v", stats, err)
			}
			for _, p := range []PrincipalRow{a, b} {
				stats, err := s.GetPrincipalStats(ctx, p.ID)
				if err != nil || stats.TotalMessages != 1 {
					t.Fatalf("source lost: %+v %v", stats, err)
				}
			}
		})
	}
}

func TestPeopleRound1DeletePurgesChainedFamily(t *testing.T) {
	s := newTestStore(t)
	a := mkPrincipal(t, s, PrincipalKindContact, "A")
	b := mkPrincipal(t, s, PrincipalKindContact, "B")
	c := mkPrincipal(t, s, PrincipalKindContact, "C")
	first, _, err := s.MergePeople(ctx, b.ID, []string{a.ID})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.MergePeople(ctx, c.ID, []string{b.ID})
	if err != nil {
		t.Fatal(err)
	}
	owners, err := s.PeopleMergeOwners(ctx)
	if err != nil || len(owners) != 2 {
		t.Fatalf("owners=%+v %v", owners, err)
	}
	for _, owner := range owners {
		if owner.TargetID != c.ID {
			t.Fatalf("chained signal lost: %+v", owner)
		}
	}
	if err := s.DeletePrincipal(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM principals`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("hidden family retained: %d %v", n, err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM principal_merges WHERE id IN (?,?)`, first, second).Scan(&n); err != nil || n != 0 {
		t.Fatalf("receipts retained: %d %v", n, err)
	}
}

func TestPeopleRound1HiddenByIDReadsAndUpdates(t *testing.T) {
	s := newTestStore(t)
	a := mkPrincipal(t, s, PrincipalKindContact, "Hidden")
	b := mkPrincipal(t, s, PrincipalKindContact, "Target")
	if _, err := s.CreateVoiceEmbedding(ctx, VoiceEmbeddingRow{PrincipalID: a.ID, Embedding: []float32{1}}); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.MergePeople(ctx, b.ID, []string{a.ID})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate stale external writers so the readers must enforce visibility.
	for _, query := range []string{
		`INSERT INTO app_handles(principal_id,app,handle_id,display_handle,created_at) VALUES(?,'wechat','hidden','Hidden',1)`,
		`INSERT INTO principal_session_sightings VALUES(?,'private-session',1,2)`,
		`INSERT INTO principal_recent_messages VALUES(?,'wechat',1,'private message')`,
	} {
		if _, err := s.db.Exec(query, a.ID); err != nil {
			t.Fatal(err)
		}
	}
	if rows, err := s.ListAppHandlesByPrincipal(ctx, a.ID); err != nil || len(rows) != 0 {
		t.Fatalf("hidden handles=%+v %v", rows, err)
	}
	if rows, err := s.ListPrincipalSessionSightingsPage(ctx, a.ID, 10, 0); err != nil || len(rows) != 0 {
		t.Fatalf("hidden sessions=%+v %v", rows, err)
	}
	if rows, err := s.GetPrincipalRecentMessages(ctx, a.ID, 10); err != nil || len(rows) != 0 {
		t.Fatalf("hidden messages=%+v %v", rows, err)
	}
	if n, err := s.CountVoiceEmbeddingsByPrincipal(ctx, a.ID); err != nil || n != 0 {
		t.Fatalf("hidden voice=%d %v", n, err)
	}
	before := peopleSnapshot(t, s, a.ID, b.ID)
	a.DisplayName = "stale rename"
	if _, err := s.UpdatePrincipal(ctx, a); err == nil {
		t.Fatal("hidden profile updated")
	}
	if err := s.BumpPrincipalSighting(ctx, a.ID, "wechat", "", 2, "stale"); err == nil {
		t.Fatal("hidden profile ingested")
	}
	if !bytes.Equal(before, peopleSnapshot(t, s, a.ID, b.ID)) {
		t.Fatal("rejected stale write changed rows")
	}
}

func TestPeopleRound1ReceiptPreservesInvalidUTF8Bytes(t *testing.T) {
	s := newTestStore(t)
	target := mkPrincipal(t, s, PrincipalKindContact, "Target")
	source := mkPrincipal(t, s, PrincipalKindContact, "Source")
	raw := string([]byte{'a', 0xff, 'b', 0xfe})
	source.Notes = raw
	source.Aliases = []string{raw}
	if _, err := s.UpdatePrincipal(ctx, source); err != nil {
		t.Fatal(err)
	}
	if err := s.BumpPrincipalSighting(ctx, source.ID, "wechat", "session", 100, raw); err != nil {
		t.Fatal(err)
	}
	before := peopleSnapshot(t, s, target.ID, source.ID)
	id, _, err := s.MergePeople(ctx, target.ID, []string{source.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UndoMerge(ctx, id); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, peopleSnapshot(t, s, target.ID, source.ID)) {
		t.Fatal("receipt rewrote raw SQLite text bytes")
	}
	restored, err := s.GetPrincipal(ctx, source.ID)
	if err != nil || restored.Notes != raw || len(restored.Aliases) != 1 || restored.Aliases[0] != raw {
		t.Fatalf("raw identity bytes changed: %q %+v %v", raw, restored, err)
	}
	messages, err := s.GetPrincipalRecentMessages(ctx, source.ID, 10)
	if err != nil || len(messages) != 1 || messages[0].Preview != raw {
		t.Fatalf("raw message bytes changed: %+v %v", messages, err)
	}
}

func TestPeopleRound1UndoConflictsAreAtomic(t *testing.T) {
	s := newTestStore(t)
	target := mkPrincipal(t, s, PrincipalKindContact, "Target")
	source := mkPrincipal(t, s, PrincipalKindContact, "Source")
	if err := s.BumpPrincipalSighting(ctx, source.ID, "wechat", "session", 100, "source"); err != nil {
		t.Fatal(err)
	}
	id, _, err := s.MergePeople(ctx, target.ID, []string{source.ID})
	if err != nil {
		t.Fatal(err)
	}
	// A destructive external rewrite is different from an ordinary message.
	if _, err := s.db.Exec(`UPDATE principal_sightings SET message_count=900 WHERE principal_id=?`, target.ID); err != nil {
		t.Fatal(err)
	}
	before := peopleSnapshot(t, s, target.ID, source.ID)
	var state *MergeStateError
	if err := s.UndoMerge(ctx, id); !errors.As(err, &state) || state.Code != "merge_conflict" {
		t.Fatalf("unsafe Undo=%v", err)
	}
	if !bytes.Equal(before, peopleSnapshot(t, s, target.ID, source.ID)) {
		t.Fatal("failed Undo partially restored rows")
	}
}

func TestPeopleRound1SequentialSharedAliasesAndContactProvenance(t *testing.T) {
	for _, oldestFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "oldest-first", false: "newest-first"}[oldestFirst], func(t *testing.T) {
			s := newTestStore(t)
			target := mkPrincipal(t, s, PrincipalKindContact, "Target")
			a := mkPrincipal(t, s, PrincipalKindContact, "A")
			b := mkPrincipal(t, s, PrincipalKindContact, "B")
			for _, p := range []PrincipalRow{a, b} {
				p.Aliases = []string{"new shared alias"}
				if _, err := s.UpdatePrincipal(ctx, p); err != nil {
					t.Fatal(err)
				}
				if err := s.BumpPrincipalSighting(ctx, p.ID, "wechat", "shared", 100, ""); err != nil {
					t.Fatal(err)
				}
			}
			// A real link receipt marks this alias as added by the contact.
			// Deliberate merges temporarily own it independently of the link.
			if _, err := s.db.Exec(`INSERT INTO imported_contact_cards VALUES('card','{}'); INSERT INTO principal_contact_links VALUES(?,'card',?)`, target.ID, `{"addedAliases":["new shared alias"]}`); err != nil {
				t.Fatal(err)
			}
			before := peopleSnapshot(t, s, target.ID, a.ID, b.ID)
			first, _, err := s.MergePeople(ctx, target.ID, []string{a.ID})
			if err != nil {
				t.Fatal(err)
			}
			second, _, err := s.MergePeople(ctx, target.ID, []string{b.ID})
			if err != nil {
				t.Fatal(err)
			}
			ids := []string{first, second}
			if !oldestFirst {
				ids = []string{second, first}
			}
			if err := s.UndoMerge(ctx, ids[0]); err != nil {
				t.Fatal(err)
			}
			p, err := s.GetPrincipal(ctx, target.ID)
			if err != nil || len(p.Aliases) != 1 || p.Aliases[0] != "new shared alias" {
				t.Fatalf("shared alias lost: %+v %v", p, err)
			}
			if err := s.UndoMerge(ctx, ids[1]); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, peopleSnapshot(t, s, target.ID, a.ID, b.ID)) {
				t.Fatalf("shared alias/contact receipt did not restore\nbefore %s\nafter %s", before, peopleSnapshot(t, s, target.ID, a.ID, b.ID))
			}
		})
	}
}

func TestPeopleRound1UndoKeepsExplicitlyEditedAliases(t *testing.T) {
	s := newTestStore(t)
	target := mkPrincipal(t, s, PrincipalKindContact, "Target")
	source := mkPrincipal(t, s, PrincipalKindContact, "Source")
	source.Aliases = []string{"copied alias"}
	if _, err := s.UpdatePrincipal(ctx, source); err != nil {
		t.Fatal(err)
	}
	id, _, err := s.MergePeople(ctx, target.ID, []string{source.ID})
	if err != nil {
		t.Fatal(err)
	}
	target, err = s.GetPrincipal(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	target.DisplayName = "Renamed"
	target.Aliases = append(target.Aliases, "new alias")
	if _, err := s.UpdatePrincipal(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := s.UndoMerge(ctx, id); err != nil {
		t.Fatal(err)
	}
	target, err = s.GetPrincipal(ctx, target.ID)
	if err != nil || target.DisplayName != "Renamed" || len(target.Aliases) != 1 || target.Aliases[0] != "new alias" {
		t.Fatalf("explicit alias edit lost: %+v %v", target, err)
	}
}

func TestPeopleRound1DeleteKeepsIndependentReceiptAfterEarlierUndo(t *testing.T) {
	s := newTestStore(t)
	target := mkPrincipal(t, s, PrincipalKindContact, "Target")
	a := mkPrincipal(t, s, PrincipalKindContact, "A")
	b := mkPrincipal(t, s, PrincipalKindContact, "B")
	if err := s.BumpPrincipalSighting(ctx, a.ID, "wechat", "", 100, "private source preview"); err != nil {
		t.Fatal(err)
	}
	if err := s.BumpPrincipalSighting(ctx, b.ID, "telegram", "", 200, "B preview"); err != nil {
		t.Fatal(err)
	}
	first, _, err := s.MergePeople(ctx, target.ID, []string{a.ID})
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.MergePeople(ctx, target.ID, []string{b.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UndoMerge(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePrincipal(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM principal_merges WHERE id IN (?,?)`, first, second).Scan(&n); err != nil || n != 1 {
		t.Fatalf("independent receipt lost: %d %v", n, err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM principal_merge_updates WHERE merge_id IN (?,?)`, first, second).Scan(&n); err != nil || n != 0 {
		t.Fatalf("private journal retained: %d %v", n, err)
	}
	stats, err := s.GetPrincipalStats(ctx, target.ID)
	if err != nil || stats.TotalMessages != 1 || len(stats.RecentMessages) != 1 || stats.RecentMessages[0].Preview != "B preview" {
		t.Fatalf("unrelated merged person lost data: %+v %v", stats, err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM principals WHERE id=? AND merged_into=?`, b.ID, target.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("unrelated hidden source lost: %d %v", n, err)
	}
	if err := s.UndoMerge(ctx, second); err != nil {
		t.Fatalf("independent Undo failed: %v", err)
	}
}
