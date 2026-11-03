package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestPeopleRound2ContactSaveDoesNotAdoptMergedAliases(t *testing.T) {
	s := newTestStore(t)
	target := mkPrincipal(t, s, PrincipalKindContact, "Target")
	source := mkPrincipal(t, s, PrincipalKindContact, "Source")
	target.Aliases = []string{"target alias"}
	source.Aliases = []string{"source alias"}
	for _, p := range []PrincipalRow{target, source} {
		if _, err := s.UpdatePrincipal(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	id, _, err := s.MergePeople(ctx, target.ID, []string{source.ID})
	if err != nil {
		t.Fatal(err)
	}
	// The editor resends the complete alias list for a rename or notes save.
	edited, err := s.GetPrincipal(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	edited.DisplayName = "Renamed"
	edited.Notes = "later note"
	edited.Aliases = append(edited.Aliases, "actually new alias")
	if _, err := s.UpdatePrincipal(ctx, edited); err != nil {
		t.Fatal(err)
	}
	if err := s.UndoMerge(ctx, id); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetPrincipal(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got.Aliases)
	if got.DisplayName != "Renamed" || got.Notes != "later note" || !reflect.DeepEqual(got.Aliases, []string{"actually new alias", "target alias"}) {
		t.Fatalf("routine save adopted source aliases: %+v", got)
	}
	restored, err := s.GetPrincipal(ctx, source.ID)
	if err != nil || !reflect.DeepEqual(restored.Aliases, source.Aliases) {
		t.Fatalf("source aliases lost: %+v %v", restored, err)
	}
}

func TestPeopleRound2DeleteOneSourceKeepsSiblingUndo(t *testing.T) {
	s := newTestStore(t)
	target := mkPrincipal(t, s, PrincipalKindContact, "Target")
	a := mkPrincipal(t, s, PrincipalKindContact, "Private A")
	b := mkPrincipal(t, s, PrincipalKindContact, "B")
	a.Aliases = []string{"private-a-alias"}
	a.Notes = "private-a-notes"
	if _, err := s.UpdatePrincipal(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindAppHandle(ctx, a.ID, "wechat", "private-a-handle", "private-a-display"); err != nil {
		t.Fatal(err)
	}
	if err := s.BumpPrincipalSighting(ctx, a.ID, "wechat", "private-a-session", 100, "private-a-preview"); err != nil {
		t.Fatal(err)
	}
	if err := s.BumpPrincipalSighting(ctx, b.ID, "telegram", "b-session", 200, "B preview"); err != nil {
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
	if err := s.BumpPrincipalSighting(ctx, target.ID, "telegram", "later-session", 300, "later target preview"); err != nil {
		t.Fatal(err)
	}
	if err := s.UndoMerge(ctx, first); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := s.db.QueryRow(`SELECT receipt_json FROM principal_merges WHERE id=?`, second).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePrincipal(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	var after string
	if err := s.db.QueryRow(`SELECT receipt_json FROM principal_merges WHERE id=?`, second).Scan(&after); err != nil {
		t.Fatalf("unrelated receipt lost: %v", err)
	}
	if before != after {
		t.Fatal("deleting A changed B's independent receipt")
	}
	var receipt peopleMergeReceipt
	if err := json.Unmarshal([]byte(after), &receipt); err != nil {
		t.Fatal(err)
	}
	// Decode typed cells as well as raw JSON: text cells are base64 encoded.
	var decoded bytes.Buffer
	decoded.WriteString(after)
	for _, snapshots := range [][]mergeTableSnapshot{receipt.Before, receipt.After, receipt.Baseline} {
		for _, snapshot := range snapshots {
			for _, row := range snapshot.Rows {
				for _, cell := range row.Values {
					decoded.Write(cell.Text)
				}
			}
		}
	}
	for _, private := range []string{a.ID, "Private A", "private-a-alias", "private-a-notes", "private-a-handle", "private-a-display", "private-a-session", "private-a-preview"} {
		if strings.Contains(decoded.String(), private) {
			t.Errorf("A data retained in B receipt: %q", private)
		}
	}
	if err := s.UndoMerge(context.Background(), second); err != nil {
		t.Fatalf("B stranded after deleting A: %v", err)
	}
	restored, err := s.GetPrincipalStats(ctx, b.ID)
	if err != nil || restored.TotalMessages != 1 {
		t.Fatalf("B restoration=%+v %v", restored, err)
	}
	remaining, err := s.GetPrincipalStats(ctx, target.ID)
	if err != nil || remaining.TotalMessages != 1 || len(remaining.RecentMessages) != 1 || remaining.RecentMessages[0].Preview != "later target preview" {
		t.Fatalf("later target activity lost: %+v %v", remaining, err)
	}
}

func TestPeopleRound2LegacyReceiptsRefuseWithTypedError(t *testing.T) {
	s := newTestStore(t)
	target := mkPrincipal(t, s, PrincipalKindContact, "Target")
	source := mkPrincipal(t, s, PrincipalKindContact, "Source")
	id, _, err := s.MergePeople(ctx, target.ID, []string{source.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"before":[],"after":[]}`, `{"before":[{"rows":[{"values":[{"kind":"text","text":"not-base64"}]}]}]}`} {
		if _, err := s.db.Exec(`UPDATE principal_merges SET receipt_json=? WHERE id=?`, raw, id); err != nil {
			t.Fatal(err)
		}
		before := peopleSnapshot(t, s, target.ID, source.ID)
		err := s.UndoMerge(ctx, id)
		state, ok := err.(*MergeStateError)
		if !ok || state.Code != "invalid_receipt" {
			t.Fatalf("legacy receipt=%v", err)
		}
		if !bytes.Equal(before, peopleSnapshot(t, s, target.ID, source.ID)) {
			t.Fatal("invalid receipt changed identity rows")
		}
	}
}

func TestPeopleRound2SavesBetweenMergesKeepTheirTimestamp(t *testing.T) {
	for _, oldestFirst := range []bool{true, false} {
		t.Run(map[bool]string{true: "oldest-first", false: "newest-first"}[oldestFirst], func(t *testing.T) {
			s := newTestStore(t)
			target := mkPrincipal(t, s, PrincipalKindContact, "Target")
			a := mkPrincipal(t, s, PrincipalKindContact, "A")
			b := mkPrincipal(t, s, PrincipalKindContact, "B")
			if _, err := s.db.Exec(`UPDATE principals SET updated_at=10 WHERE id=?`, target.ID); err != nil {
				t.Fatal(err)
			}
			first, _, err := s.MergePeople(ctx, target.ID, []string{a.ID})
			if err != nil {
				t.Fatal(err)
			}
			target.DisplayName = "Renamed"
			edited, err := s.UpdatePrincipal(ctx, target)
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
			for _, id := range ids {
				if err := s.UndoMerge(ctx, id); err != nil {
					t.Fatal(err)
				}
			}
			restored, err := s.GetPrincipal(ctx, target.ID)
			if err != nil || restored.DisplayName != edited.DisplayName || restored.UpdatedAt != edited.UpdatedAt {
				t.Fatalf("intervening save lost: %+v want %+v (%v)", restored, edited, err)
			}
		})
	}
}

func TestPeopleRound2ReaddedAliasIsAnExplicitNewEdit(t *testing.T) {
	s := newTestStore(t)
	target := mkPrincipal(t, s, PrincipalKindContact, "Target")
	source := mkPrincipal(t, s, PrincipalKindContact, "Source")
	source.Aliases = []string{"source alias"}
	if _, err := s.UpdatePrincipal(ctx, source); err != nil {
		t.Fatal(err)
	}
	id, _, err := s.MergePeople(ctx, target.ID, []string{source.ID})
	if err != nil {
		t.Fatal(err)
	}
	// Removing and saving, then adding it again, is new relative to that save.
	if _, err := s.UpdatePrincipal(ctx, target); err != nil {
		t.Fatal(err)
	}
	target.Aliases = []string{"source alias"}
	if _, err := s.UpdatePrincipal(ctx, target); err != nil {
		t.Fatal(err)
	}
	if err := s.UndoMerge(ctx, id); err != nil {
		t.Fatal(err)
	}
	target, err = s.GetPrincipal(ctx, target.ID)
	if err != nil || !reflect.DeepEqual(target.Aliases, []string{"source alias"}) {
		t.Fatalf("explicit new edit lost: %+v %v", target, err)
	}
}

func TestPeopleRound2RealV62UpgradeIncludesChatTopicsAndPeople(t *testing.T) {
	schema, err := os.ReadFile("testdata/schema-v62.sql")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(schema)); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO principals(id,kind,display_name,notes,created_at,updated_at) VALUES('kept','contact','Manual person','manual note',1,1)`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"stream_item_topics", "principal_merges", "principal_merge_updates"} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			s.Close()
			t.Fatalf("missing migration table %s: %d %v", table, n, err)
		}
	}
	if stamp, err := checkSchemaVersion(s.db); err != nil || stamp != schemaVersion {
		s.Close()
		t.Fatalf("stamp=%d %v", stamp, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	kept, err := s.GetPrincipal(ctx, "kept")
	if err != nil || kept.Notes != "manual note" {
		t.Fatalf("manual identity lost: %+v %v", kept, err)
	}
	source := mkPrincipal(t, s, PrincipalKindContact, "New source")
	id, _, err := s.MergePeople(ctx, kept.ID, []string{source.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UndoMerge(ctx, id); err != nil {
		t.Fatal(err)
	}
}
