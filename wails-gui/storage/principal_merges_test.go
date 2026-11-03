package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func peopleSnapshot(t *testing.T, s *Store, ids ...string) []byte {
	t.Helper()
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	snapshot, err := snapshotPeopleMerge(context.Background(), tx, ids)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPeopleMergeUndoRestoresEveryTransferredRow(t *testing.T) {
	s := newTestStore(t)
	target := mkPrincipal(t, s, PrincipalKindContact, "Target")
	source := mkPrincipal(t, s, PrincipalKindAnonymous, "Source")
	second := mkPrincipal(t, s, PrincipalKindContact, "Second")
	for i, id := range []string{target.ID, source.ID, second.ID} {
		if _, err := s.UpdatePrincipal(ctx, PrincipalRow{ID: id, DisplayName: "Name", Notes: "manual notes", Aliases: []string{"shared", id}}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.BindAppHandle(ctx, id, "wechat", "one-account", id); err != nil {
			t.Fatal(err)
		}
		for _, app := range []string{"wechat", id} {
			if err := s.BumpPrincipalSighting(ctx, id, app, "shared-session", int64(100+i), "preview "+id); err != nil {
				t.Fatal(err)
			}
			if err := s.BumpPrincipalSighting(ctx, id, app, id, int64(200+i), "later "+id); err != nil {
				t.Fatal(err)
			}
		}
	}
	before := peopleSnapshot(t, s, target.ID, source.ID, second.ID)
	mergeID, got, err := s.MergePeople(ctx, target.ID, []string{source.ID, second.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != target.ID {
		t.Fatal(got)
	}
	if _, err := s.GetPrincipal(ctx, source.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	if err := s.UndoMerge(ctx, mergeID); err != nil {
		t.Fatal(err)
	}
	after := peopleSnapshot(t, s, target.ID, source.ID, second.ID)
	if !bytes.Equal(before, after) {
		t.Fatalf("row snapshots differ\nbefore=%s\nafter=%s", before, after)
	}
	var state *MergeStateError
	if err := s.UndoMerge(ctx, mergeID); !errors.As(err, &state) || state.Code != "already_undone" {
		t.Fatalf("second undo=%v", err)
	}
}

func TestPeopleMergeUndoRetainsLaterMessagesInNewAndExistingApps(t *testing.T) {
	for _, change := range []bool{false, true} {
		t.Run(map[bool]string{false: "new-row", true: "changed-row"}[change], func(t *testing.T) {
			s := newTestStore(t)
			target := mkPrincipal(t, s, PrincipalKindContact, "T")
			source := mkPrincipal(t, s, PrincipalKindContact, "S")
			if err := s.BumpPrincipalSighting(ctx, source.ID, "wechat", "old", 100, "old private preview"); err != nil {
				t.Fatal(err)
			}
			id, _, err := s.MergePeople(ctx, target.ID, []string{source.ID})
			if err != nil {
				t.Fatal(err)
			}
			app := "telegram"
			if change {
				app = "wechat"
			}
			if err := s.BumpPrincipalSighting(ctx, target.ID, app, "new", 200, "new private preview"); err != nil {
				t.Fatal(err)
			}
			if err := s.UndoMerge(ctx, id); err != nil {
				t.Fatal(err)
			}
			stats, err := s.GetPrincipalStats(ctx, target.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stats.TotalMessages != 1 || len(stats.RecentMessages) != 1 || stats.RecentMessages[0].Preview != "new private preview" {
				t.Fatalf("new observations lost: %+v", stats)
			}
			stats, err = s.GetPrincipalStats(ctx, source.ID)
			if err != nil || stats.TotalMessages != 1 {
				t.Fatalf("source=%+v %v", stats, err)
			}
		})
	}
}

func TestPeopleMergedProfilesHiddenFromEveryResolver(t *testing.T) {
	s := newTestStore(t)
	target := mkPrincipal(t, s, PrincipalKindContact, "Target")
	source := mkPrincipal(t, s, PrincipalKindAnonymous, "Hidden Source")
	if _, err := s.BindAppHandle(ctx, source.ID, "wechat", "account", "Source Handle"); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePrincipalResearch(ctx, PrincipalResearchRow{PrincipalID: source.ID, Summary: "source dossier"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateVoiceEmbedding(ctx, VoiceEmbeddingRow{PrincipalID: source.ID, Embedding: []float32{1, 2}}); err != nil {
		t.Fatal(err)
	}
	id, _, err := s.MergePeople(ctx, target.ID, []string{source.ID})
	if err != nil {
		t.Fatal(err)
	}
	// An old/manual writer leaving a handle on a hidden profile must not expose it.
	if _, err := s.db.Exec(`INSERT INTO app_handles(principal_id,app,handle_id,display_handle,created_at) VALUES(?,'telegram','stale','Stale',1)`, source.ID); err != nil {
		t.Fatal(err)
	}
	for _, read := range []func() (*PrincipalRow, error){
		func() (*PrincipalRow, error) { return s.ResolveByAppHandle(ctx, "telegram", "stale") },
		func() (*PrincipalRow, error) { return s.ResolveByDisplayHandle(ctx, "telegram", "Stale") },
		func() (*PrincipalRow, error) { return s.ResolveByAlias(ctx, "Hidden Source") },
	} {
		p, err := read()
		if err != nil || p != nil {
			t.Fatalf("hidden lookup=%+v %v", p, err)
		}
	}
	rows, err := s.ListPrincipals(ctx)
	if err != nil || len(rows) != 1 || rows[0].ID != target.ID {
		t.Fatalf("list=%+v %v", rows, err)
	}
	found, _, err := s.SearchMCPPrincipals(ctx, "Hidden", 20)
	if err != nil || len(found) != 0 {
		t.Fatalf("MCP=%+v %v", found, err)
	}
	resolved, err := s.ResolveByAppHandle(ctx, "wechat", "account")
	if err != nil || resolved == nil || resolved.ID != target.ID {
		t.Fatalf("transferred handle=%+v %v", resolved, err)
	}
	if dossier, err := s.GetPrincipalResearch(ctx, source.ID); err != nil || dossier.Summary != "" {
		t.Fatalf("hidden dossier=%+v %v", dossier, err)
	}
	if samples, err := s.ListVoiceEmbeddingsByPrincipal(ctx, source.ID); err != nil || len(samples) != 0 {
		t.Fatalf("hidden voice=%+v %v", samples, err)
	}
	if samples, err := s.ListVoiceEmbeddingsForKind(ctx, PrincipalKindAnonymous); err != nil || len(samples) != 0 {
		t.Fatalf("hidden voice kind=%+v %v", samples, err)
	}
	if err := s.UndoMerge(ctx, id); err != nil {
		t.Fatal(err)
	}
	if dossier, err := s.GetPrincipalResearch(ctx, source.ID); err != nil || dossier.Summary != "source dossier" {
		t.Fatalf("restored dossier=%+v %v", dossier, err)
	}
	if samples, err := s.ListVoiceEmbeddingsByPrincipal(ctx, source.ID); err != nil || len(samples) != 1 {
		t.Fatalf("restored voice=%+v %v", samples, err)
	}

}

func TestPeopleMergeRefusalsRollbackAllSources(t *testing.T) {
	s := newTestStore(t)
	target := mkPrincipal(t, s, PrincipalKindContact, "Target")
	source := mkPrincipal(t, s, PrincipalKindContact, "Source")
	linked := mkPrincipal(t, s, PrincipalKindContact, "Linked")
	if _, err := s.db.Exec(`INSERT INTO imported_contact_cards(external_ref,payload) VALUES('card','{}'); INSERT INTO principal_contact_links(principal_id,external_ref,receipt) VALUES(?,'card','{}')`, linked.ID); err != nil {
		t.Fatal(err)
	}
	before := peopleSnapshot(t, s, target.ID, source.ID, linked.ID)
	if _, _, err := s.MergePeople(ctx, target.ID, []string{source.ID, linked.ID}); err == nil {
		t.Fatal("contact-linked source accepted")
	}
	if !bytes.Equal(before, peopleSnapshot(t, s, target.ID, source.ID, linked.ID)) {
		t.Fatal("partial merge")
	}
	for _, sources := range [][]string{{target.ID}, {source.ID, source.ID}, {"missing"}, {}} {
		if _, _, err := s.MergePeople(ctx, target.ID, sources); err == nil {
			t.Fatalf("accepted %v", sources)
		}
	}
	id, _, err := s.MergePeople(ctx, target.ID, []string{source.ID})
	if err != nil {
		t.Fatal(err)
	}
	later, _, err := s.MergePeople(ctx, linked.ID, []string{target.ID})
	if err != nil {
		t.Fatalf("prior target cannot merge into a contact-linked destination: %v", err)
	}
	if err := s.UndoMerge(ctx, later); err != nil {
		t.Fatal(err)
	}
	if err := s.UndoMerge(ctx, id); err != nil {
		t.Fatal(err)
	}
}

func TestPeopleMigrationPreviousSchemaAndPartialReopen(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "previous-v62", true: "partial-column"}[partial], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			s, err := New(path)
			if err != nil {
				t.Fatal(err)
			}
			p := mkPrincipal(t, s, PrincipalKindContact, "manual preserved")
			if _, err := s.BindAppHandle(ctx, p.ID, "email", "manual@example.test", "manual@example.test"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`DROP TABLE principal_merge_updates; DROP TABLE principal_merges`); err != nil {
				t.Fatal(err)
			}
			if !partial {
				if _, err := s.db.Exec(`ALTER TABLE principals DROP COLUMN merged_into`); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.Exec(`PRAGMA user_version=62`); err != nil {
				t.Fatal(err)
			}
			s.Close()
			s, err = New(path)
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.GetPrincipal(ctx, p.ID)
			if err != nil || got.DisplayName != p.DisplayName {
				t.Fatalf("principal=%+v %v", got, err)
			}
			handles, err := s.ListAppHandlesByPrincipal(ctx, p.ID)
			if err != nil || len(handles) != 1 || handles[0].HandleID != "manual@example.test" {
				t.Fatalf("handles=%+v %v", handles, err)
			}
			version, err := checkSchemaVersion(s.db)
			if err != nil || version != schemaVersion {
				t.Fatalf("version=%d %v", version, err)
			}
			if len(migrationSnapshots(t, path)) != 1 {
				t.Fatal("missing upgrade snapshot")
			}
			s.Close()
			s, err = New(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			other := mkPrincipal(t, s, PrincipalKindContact, "other")
			id, _, err := s.MergePeople(ctx, p.ID, []string{other.ID})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.UndoMerge(ctx, id); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPeopleMergeUndoPreservesContactAccountingAndHandleProvenance(t *testing.T) {
	s := newTestStore(t)
	target := mkPrincipal(t, s, PrincipalKindContact, "Target")
	source := mkPrincipal(t, s, PrincipalKindContact, "Source")
	handle, err := s.BindAppHandle(ctx, source.ID, "email", "source@example.test", "Source")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO imported_contact_cards(external_ref,payload) VALUES('target-card','{}'),('source-card','{}')`, nil},
		{`INSERT INTO principal_contact_links(principal_id,external_ref,receipt) VALUES(?,'target-card','{}')`, []any{target.ID}},
		{`INSERT INTO contact_handle_provenance(external_ref,handle_id) VALUES('source-card',?)`, []any{handle.ID}},
		{`INSERT INTO principal_contact_link_updates(principal_id,app,session_id,message_count,first_seen_at,last_seen_at) VALUES(?,'email','',1,1,2)`, []any{target.ID}},
	} {
		if _, err := s.db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.BumpPrincipalSighting(ctx, source.ID, "email", "source-session", 100, "source"); err != nil {
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
		t.Fatal("contact accounting did not restore byte for byte")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM contact_handle_provenance WHERE external_ref='source-card' AND handle_id=?`, handle.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("handle provenance removed by Undo: %d %v", count, err)
	}
}
