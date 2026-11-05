package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var ctx = context.Background()

func TestSchemaCheckWaitsForSQLiteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE fixture (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("BEGIN EXCLUSIVE"); err != nil {
		t.Fatal(err)
	}
	defer db.Exec("ROLLBACK")
	checked := make(chan error, 1)
	go func() { checked <- checkExistingSchemaVersion(path) }()
	select {
	case err := <-checked:
		t.Fatalf("schema check returned while locked: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := db.Exec("COMMIT"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-checked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("schema check did not continue after unlock")
	}
}

func TestCloseWaitsForCheckedStreamItemConnections(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveProject(ctx, ProjectRow{ID: "p", Name: "Project", Path: "/tmp/checked-close"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSession(ctx, SessionRow{LocalID: "chat", ProjectID: "p", Label: "Chat"}); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := s.AppendStreamItemChecked(ctx, "chat", StreamItemRow{ID: "item", Kind: "user"}, func(*Store) error {
			close(entered)
			<-release
			return nil
		})
		result <- err
	}()
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	deadline := time.After(2 * time.Second)
	for {
		s.closeMu.Lock()
		closing := s.closing
		s.closeMu.Unlock()
		if closing {
			break
		}
		select {
		case <-deadline:
			close(release)
			t.Fatal("close did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	select {
	case err := <-closed:
		close(release)
		t.Fatalf("close returned before the private connections closed: %v", err)
	default:
	}
	if _, err := s.AppendStreamItemChecked(ctx, "chat", StreamItemRow{}, func(*Store) error { return nil }); !errors.Is(err, sql.ErrConnDone) {
		close(release)
		t.Fatalf("append during close: %v", err)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
}

func TestAppendStreamItemCheckedPublishesOnlyAfterValidation(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveProject(ctx, ProjectRow{ID: "p", Name: "Project", Path: "/tmp/checked-project"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSession(ctx, SessionRow{LocalID: "chat", ProjectID: "p", Label: "Chat"}); err != nil {
		t.Fatal(err)
	}
	// A delete-after-publication strategy would leave the rejected row behind
	// when cleanup fails. Transaction rollback never invokes this DELETE trigger.
	if _, err := s.db.Exec(`CREATE TRIGGER refuse_message_cleanup BEFORE DELETE ON stream_items BEGIN SELECT RAISE(ABORT, 'cleanup unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	inserted, finish := make(chan struct{}), make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(finish)
		}
	}()
	result := make(chan error, 1)
	withdrawn := errors.New("source withdrawn")
	go func() {
		_, err := s.AppendStreamItemChecked(ctx, "chat", StreamItemRow{ID: "rejected", Kind: "user", Content: "Source-derived text"}, func(*Store) error { close(inserted); <-finish; return withdrawn })
		result <- err
	}()
	select {
	case <-inserted:
	case err := <-result:
		t.Fatalf("insert did not reach validation: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("checked insert stalled")
	}
	rows, err := s.LoadStreamItems(ctx, "chat")
	close(finish)
	released = true
	if err != nil || len(rows) != 0 {
		t.Fatalf("unvalidated row escaped transaction: %+v %v", rows, err)
	}
	if err := <-result; !errors.Is(err, withdrawn) {
		t.Fatalf("rejection: %v", err)
	}
	rows, err = s.LoadStreamItems(ctx, "chat")
	if err != nil || len(rows) != 0 {
		t.Fatalf("failed cleanup left a rejected row: %+v %v", rows, err)
	}
	order, err := s.AppendStreamItemChecked(ctx, "chat", StreamItemRow{ID: "accepted", Kind: "user", Content: "Checked text"}, func(readOnly *Store) error {
		rows, err := readOnly.LoadStreamItems(ctx, "chat")
		if err != nil {
			return err
		}
		if len(rows) != 0 {
			return errors.New("row visible before admission")
		}
		return nil
	})
	if err != nil || order != 0 {
		t.Fatalf("accepted insert: %d %v", order, err)
	}
	rows, err = s.LoadStreamItems(ctx, "chat")
	if err != nil || len(rows) != 1 || rows[0].ID != "accepted" {
		t.Fatalf("accepted row missing: %+v %v", rows, err)
	}
}

// The normal Store intentionally keeps one connection. A concurrent write
// owns it while waiting for the checked insert's writer lock. Validation needs
// its own read connection so that neither operation waits for the other.
func TestAppendStreamItemCheckedAllowsConcurrentWriter(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveProject(ctx, ProjectRow{ID: "p", Name: "Project", Path: "/tmp/checked-writer"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSession(ctx, SessionRow{LocalID: "chat", ProjectID: "p", Label: "Chat"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("PRAGMA busy_timeout=1000"); err != nil {
		t.Fatal(err)
	}
	writeResult := make(chan error, 1)
	writerStarted := false
	_, admissionErr := s.AppendStreamItemChecked(ctx, "chat", StreamItemRow{ID: "checked", Kind: "user", Content: "Source-derived text"}, func(readOnly *Store) error {
		writerStarted = true
		go func() { writeResult <- s.SetUIState(ctx, "sensitive_apps", `{"private.app":"Private"}`) }()
		deadline := time.Now().Add(time.Second)
		for s.db.Stats().InUse != 1 {
			if time.Now().After(deadline) {
				return errors.New("concurrent writer did not acquire the normal connection")
			}
			time.Sleep(time.Millisecond)
		}
		rows, err := readOnly.LoadStreamItems(ctx, "chat")
		if err != nil {
			return err
		}
		if len(rows) != 0 {
			return errors.New("unvalidated row became visible")
		}
		_, err = readOnly.GetUIState(ctx, "sensitive_apps")
		return err
	})
	if writerStarted {
		select {
		case err := <-writeResult:
			if err != nil {
				t.Fatalf("admission blocked the concurrent privacy write: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("concurrent writer did not finish")
		}
	}
	if admissionErr != nil {
		t.Fatalf("admission failed: %v", admissionErr)
	}
	privacy, err := s.GetUIState(ctx, "sensitive_apps")
	if err != nil || privacy != `{"private.app":"Private"}` {
		t.Fatalf("privacy write was lost: %q %v", privacy, err)
	}
}

// newTestStore creates an in-memory Store for testing.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := New(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// ── Schema version ──

func TestMigrateStampsSchemaVersion(t *testing.T) {
	s := newTestStore(t)
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Errorf("user_version = %d, want %d", version, schemaVersion)
	}
}

func TestMigrateIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// First open: creates schema.
	s1, err := New(dbPath)
	if err != nil {
		t.Fatalf("first New: %v", err)
	}
	s1.Close()

	// Second open: same migrations run again without error.
	s2, err := New(dbPath)
	if err != nil {
		t.Fatalf("second New: %v", err)
	}
	s2.Close()
}

// TestMigrateV40RenamesLegacyStandaloneChat covers the scenario reported in
// #1741's follow-up: a random-UUID project left over from older builds of
// activeProjectAndWorkspace step 3 owns path="" with sessions/workspaces
// hanging off it. The migration must rewrite the row's id (plus all child
// FKs) to the canonical __standalone_chat__ sentinel so subsequent
// EnsureProjectStructure calls passing that id don't silently no-op on
// ON CONFLICT(path) and then FK-fail on the workspace insert.
func TestMigrateV40RenamesLegacyStandaloneChat(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy-standalone.db")

	// Boot once so the schema exists, then seed legacy state.
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("initial New: %v", err)
	}
	const legacyID = "11111111-2222-3333-4444-555555555555"
	if err := s.SaveProject(ctx, ProjectRow{ID: legacyID, Name: "General Chat", Path: ""}); err != nil {
		t.Fatalf("seed legacy project: %v", err)
	}
	if err := s.SaveWorkspace(ctx, WorkspaceRow{ID: "ws-legacy", ProjectID: legacyID, Label: "default", IsDefault: true}); err != nil {
		t.Fatalf("seed legacy workspace: %v", err)
	}
	if err := s.SaveSession(ctx, SessionRow{LocalID: "sess-legacy", ProjectID: legacyID, WorkspaceID: "ws-legacy", Label: "first turn", Model: "haiku"}); err != nil {
		t.Fatalf("seed legacy session: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	// Roll user_version back to 39 so v40 re-runs on the next open.
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec(`PRAGMA user_version = 39`); err != nil {
		raw.Close()
		t.Fatalf("rewind user_version: %v", err)
	}
	raw.Close()

	// Reopen — migration v40 fires.
	healed, err := New(dbPath)
	if err != nil {
		t.Fatalf("heal New: %v", err)
	}
	defer healed.Close()

	// Project id should now be the sentinel.
	p, err := healed.LoadProjectByPath(ctx, "")
	if err != nil {
		t.Fatalf("LoadProjectByPath after heal: %v", err)
	}
	if p.ID != "__standalone_chat__" {
		t.Fatalf("project id after migration = %q, want __standalone_chat__", p.ID)
	}

	// Child FKs should track the new id.
	ws, err := healed.LoadWorkspacesByProject(ctx, "__standalone_chat__")
	if err != nil {
		t.Fatalf("LoadWorkspacesByProject: %v", err)
	}
	if len(ws) != 1 || ws[0].ID != "ws-legacy" {
		t.Fatalf("workspaces after migration = %+v, want 1 (ws-legacy)", ws)
	}

	sessByWS, err := healed.LoadAllSessionsByWorkspace(ctx)
	if err != nil {
		t.Fatalf("LoadAllSessionsByWorkspace: %v", err)
	}
	if got := sessByWS["ws-legacy"]; len(got) != 1 || got[0].ProjectID != "__standalone_chat__" {
		t.Fatalf("sessions after migration = %+v, want 1 session under __standalone_chat__", got)
	}

	// Old id should no longer resolve to anything.
	if oldWS, _ := healed.LoadWorkspacesByProject(ctx, legacyID); len(oldWS) != 0 {
		t.Fatalf("legacy workspaces still referencing %s: %+v", legacyID, oldWS)
	}
}

// TestMigrateV40MergesIntoExistingSentinel covers the defensive branch:
// if a sentinel row coexists with a legacy path="" row under a different
// path, the migration reparents child rows to the sentinel, drops the
// legacy row, and reclaims path="" for the sentinel without losing any
// sessions or workspaces. This state can't be produced by current
// writers but the migration must stay correct against any future bug
// that creates it.
func TestMigrateV40MergesIntoExistingSentinel(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "merge-standalone.db")

	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("initial New: %v", err)
	}
	const legacyID = "aaaa1111-bbbb-2222-cccc-333333333333"
	// Sentinel parked at a non-empty path (forced state — current writers
	// never produce this, but the migration must handle it correctly).
	if err := s.SaveProject(ctx, ProjectRow{ID: "__standalone_chat__", Name: "General Chat", Path: "/non/empty/sentinel"}); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}
	if err := s.SaveProject(ctx, ProjectRow{ID: legacyID, Name: "Legacy Chat", Path: ""}); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	if err := s.SaveWorkspace(ctx, WorkspaceRow{ID: "ws-legacy", ProjectID: legacyID, Label: "default", IsDefault: true}); err != nil {
		t.Fatalf("seed legacy workspace: %v", err)
	}
	if err := s.SaveSession(ctx, SessionRow{LocalID: "sess-legacy", ProjectID: legacyID, WorkspaceID: "ws-legacy", Label: "turn", Model: "haiku"}); err != nil {
		t.Fatalf("seed legacy session: %v", err)
	}
	s.Close()

	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec(`PRAGMA user_version = 39`); err != nil {
		raw.Close()
		t.Fatalf("rewind user_version: %v", err)
	}
	raw.Close()

	healed, err := New(dbPath)
	if err != nil {
		t.Fatalf("heal New: %v", err)
	}
	defer healed.Close()

	// Sentinel now owns path="".
	p, err := healed.LoadProjectByPath(ctx, "")
	if err != nil {
		t.Fatalf("LoadProjectByPath after merge: %v", err)
	}
	if p.ID != "__standalone_chat__" {
		t.Fatalf("path=\"\" owner = %q, want __standalone_chat__", p.ID)
	}

	// Legacy row is gone.
	if _, err := healed.LoadProjectByPath(ctx, "/non/empty/sentinel"); err != sql.ErrNoRows {
		t.Fatalf("old sentinel path still exists; err=%v", err)
	}
	if oldWS, _ := healed.LoadWorkspacesByProject(ctx, legacyID); len(oldWS) != 0 {
		t.Fatalf("workspaces still under legacy id: %+v", oldWS)
	}

	// Child rows moved to the sentinel — history preserved.
	ws, err := healed.LoadWorkspacesByProject(ctx, "__standalone_chat__")
	if err != nil {
		t.Fatalf("LoadWorkspacesByProject sentinel: %v", err)
	}
	if len(ws) != 1 || ws[0].ID != "ws-legacy" {
		t.Fatalf("merged workspaces = %+v, want 1 (ws-legacy)", ws)
	}
	sessByWS, err := healed.LoadAllSessionsByWorkspace(ctx)
	if err != nil {
		t.Fatalf("LoadAllSessionsByWorkspace: %v", err)
	}
	if got := sessByWS["ws-legacy"]; len(got) != 1 || got[0].ProjectID != "__standalone_chat__" {
		t.Fatalf("merged sessions = %+v, want 1 session under sentinel", got)
	}
}

// TestMigrateV40NoOpOnHealthyDB confirms the migration leaves a healthy
// DB (sentinel already owns path="") untouched.
func TestMigrateV40NoOpOnHealthyDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "healthy-standalone.db")

	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("initial New: %v", err)
	}
	if err := s.SaveProject(ctx, ProjectRow{ID: "__standalone_chat__", Name: "General Chat", Path: ""}); err != nil {
		t.Fatalf("seed sentinel: %v", err)
	}
	s.Close()

	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	if _, err := raw.Exec(`PRAGMA user_version = 39`); err != nil {
		raw.Close()
		t.Fatalf("rewind user_version: %v", err)
	}
	raw.Close()

	healed, err := New(dbPath)
	if err != nil {
		t.Fatalf("heal New: %v", err)
	}
	defer healed.Close()

	p, err := healed.LoadProjectByPath(ctx, "")
	if err != nil {
		t.Fatalf("LoadProjectByPath: %v", err)
	}
	if p.ID != "__standalone_chat__" {
		t.Fatalf("sentinel id changed to %q after no-op migration", p.ID)
	}
}

func TestMigrateRepairsLegacyProjectsSortOrdColumn(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy-sort-ord.db")

	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("initial New: %v", err)
	}
	if err := s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P1", Path: "/tmp/p1", SortOrder: 1}); err != nil {
		t.Fatalf("save p1: %v", err)
	}
	if err := s.SaveProject(ctx, ProjectRow{ID: "p2", Name: "P2", Path: "/tmp/p2", SortOrder: 0}); err != nil {
		t.Fatalf("save p2: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		PRAGMA foreign_keys=OFF;
		ALTER TABLE projects RENAME TO projects_v7_good;
		CREATE TABLE projects (
			id                TEXT PRIMARY KEY,
			name              TEXT NOT NULL,
			path              TEXT NOT NULL UNIQUE,
			organization      TEXT NOT NULL DEFAULT '',
			product           TEXT NOT NULL DEFAULT '',
			is_yansu_project  INTEGER NOT NULL DEFAULT 0,
			active_session_id TEXT NOT NULL DEFAULT '',
			active_workspace_id TEXT NOT NULL DEFAULT '',
			sort_ord          INTEGER NOT NULL DEFAULT 0,
			created_at        INTEGER NOT NULL,
			updated_at        INTEGER NOT NULL
		);
		INSERT INTO projects (id, name, path, organization, product, is_yansu_project, active_session_id, active_workspace_id, sort_ord, created_at, updated_at)
		SELECT id, name, path, organization, product, is_yansu_project, active_session_id, active_workspace_id, sort_order, created_at, updated_at
		FROM projects_v7_good;
		DROP TABLE projects_v7_good;
		PRAGMA user_version = 7;
		PRAGMA foreign_keys=ON;
	`); err != nil {
		t.Fatalf("rewrite projects to legacy sort_ord schema: %v", err)
	}

	healed, err := New(dbPath)
	if err != nil {
		t.Fatalf("heal New: %v", err)
	}
	defer healed.Close()

	projects, err := healed.LoadProjects(ctx)
	if err != nil {
		t.Fatalf("LoadProjects after heal: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("got %d projects, want 2", len(projects))
	}
	if projects[0].ID != "p2" || projects[0].SortOrder != 0 {
		t.Fatalf("first project = %+v, want ID=p2 sortOrder=0", projects[0])
	}
	if projects[1].ID != "p1" || projects[1].SortOrder != 1 {
		t.Fatalf("second project = %+v, want ID=p1 sortOrder=1", projects[1])
	}
}

func TestMigrateRepairsProjectsWithoutSortColumns(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "legacy-no-sort-column.db")

	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("initial New: %v", err)
	}
	if err := s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P1", Path: "/tmp/p1"}); err != nil {
		t.Fatalf("save p1: %v", err)
	}
	if err := s.SaveProject(ctx, ProjectRow{ID: "p2", Name: "P2", Path: "/tmp/p2"}); err != nil {
		t.Fatalf("save p2: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		PRAGMA foreign_keys=OFF;
		ALTER TABLE projects RENAME TO projects_v7_good;
		CREATE TABLE projects (
			id                TEXT PRIMARY KEY,
			name              TEXT NOT NULL,
			path              TEXT NOT NULL UNIQUE,
			organization      TEXT NOT NULL DEFAULT '',
			product           TEXT NOT NULL DEFAULT '',
			is_yansu_project  INTEGER NOT NULL DEFAULT 0,
			active_session_id TEXT NOT NULL DEFAULT '',
			active_workspace_id TEXT NOT NULL DEFAULT '',
			created_at        INTEGER NOT NULL,
			updated_at        INTEGER NOT NULL
		);
		INSERT INTO projects (id, name, path, organization, product, is_yansu_project, active_session_id, active_workspace_id, created_at, updated_at)
		SELECT id, name, path, organization, product, is_yansu_project, active_session_id, active_workspace_id, created_at, updated_at
		FROM projects_v7_good;
		UPDATE projects
		SET created_at = CASE id
			WHEN 'p2' THEN 100
			WHEN 'p1' THEN 200
			ELSE created_at
		END,
		updated_at = CASE id
			WHEN 'p2' THEN 100
			WHEN 'p1' THEN 200
			ELSE updated_at
		END;
		DROP TABLE projects_v7_good;
		PRAGMA user_version = 7;
		PRAGMA foreign_keys=ON;
	`); err != nil {
		t.Fatalf("rewrite projects without sort columns: %v", err)
	}

	healed, err := New(dbPath)
	if err != nil {
		t.Fatalf("heal New: %v", err)
	}
	defer healed.Close()

	projects, err := healed.LoadProjects(ctx)
	if err != nil {
		t.Fatalf("LoadProjects after heal: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("got %d projects, want 2", len(projects))
	}
	if projects[0].ID != "p2" || projects[0].SortOrder != 0 {
		t.Fatalf("first project = %+v, want ID=p2 sortOrder=0", projects[0])
	}
	if projects[1].ID != "p1" || projects[1].SortOrder != 1 {
		t.Fatalf("second project = %+v, want ID=p1 sortOrder=1", projects[1])
	}
}

// ── Projects CRUD ──

func TestSaveAndLoadProject(t *testing.T) {
	s := newTestStore(t)

	p := ProjectRow{
		ID:   "p1",
		Name: "Project One",
		Path: "/tmp/proj1",
	}
	if err := s.SaveProject(ctx, p); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}

	projects, err := s.LoadProjects(ctx)
	if err != nil {
		t.Fatalf("LoadProjects: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(projects))
	}
	if projects[0].ID != "p1" || projects[0].Name != "Project One" {
		t.Errorf("project = %+v", projects[0])
	}
	if projects[0].CreatedAt == 0 {
		t.Error("CreatedAt should be set automatically")
	}
}

func TestSaveProjectUpsert(t *testing.T) {
	s := newTestStore(t)

	p := ProjectRow{ID: "p1", Name: "Old", Path: "/tmp/proj1"}
	if err := s.SaveProject(ctx, p); err != nil {
		t.Fatal(err)
	}

	p.Name = "New"
	if err := s.SaveProject(ctx, p); err != nil {
		t.Fatal(err)
	}

	projects, _ := s.LoadProjects(ctx)
	if len(projects) != 1 || projects[0].Name != "New" {
		t.Errorf("upsert failed: %+v", projects)
	}
}

func TestDeleteProject(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})

	if err := s.DeleteProject(ctx, "p1"); err != nil {
		t.Fatal(err)
	}

	projects, _ := s.LoadProjects(ctx)
	if len(projects) != 0 {
		t.Errorf("expected 0 projects after delete, got %d", len(projects))
	}
}

func TestDeleteProjectCascadesSessions(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s1", ProjectID: "p1", Label: "Session", Model: "m"})

	s.DeleteProject(ctx, "p1")

	sessions, _ := s.LoadSessions(ctx, "p1")
	if len(sessions) != 0 {
		t.Errorf("expected cascade delete of sessions, got %d", len(sessions))
	}
}

func TestUpdateProjectActiveSession(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})

	if err := s.UpdateProjectActiveSession(ctx, "p1", "s1"); err != nil {
		t.Fatal(err)
	}

	projects, _ := s.LoadProjects(ctx)
	if projects[0].ActiveSessionID != "s1" {
		t.Errorf("ActiveSessionID = %q, want %q", projects[0].ActiveSessionID, "s1")
	}
}

func TestLoadProjectsOrderedBySortOrder(t *testing.T) {
	s := newTestStore(t)

	if err := s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P1", Path: "/tmp/p1", SortOrder: 2}); err != nil {
		t.Fatalf("save p1: %v", err)
	}
	if err := s.SaveProject(ctx, ProjectRow{ID: "p2", Name: "P2", Path: "/tmp/p2", SortOrder: 0}); err != nil {
		t.Fatalf("save p2: %v", err)
	}
	if err := s.SaveProject(ctx, ProjectRow{ID: "p3", Name: "P3", Path: "/tmp/p3", SortOrder: 1}); err != nil {
		t.Fatalf("save p3: %v", err)
	}

	projects, err := s.LoadProjects(ctx)
	if err != nil {
		t.Fatalf("LoadProjects: %v", err)
	}
	if len(projects) != 3 {
		t.Fatalf("got %d projects, want 3", len(projects))
	}

	gotIDs := []string{projects[0].ID, projects[1].ID, projects[2].ID}
	wantIDs := []string{"p2", "p3", "p1"}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Fatalf("project[%d] = %q, want %q", i, gotIDs[i], wantIDs[i])
		}
	}
}

func TestUpdateProjectSortOrders(t *testing.T) {
	s := newTestStore(t)

	if err := s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P1", Path: "/tmp/p1", SortOrder: 0}); err != nil {
		t.Fatalf("save p1: %v", err)
	}
	if err := s.SaveProject(ctx, ProjectRow{ID: "p2", Name: "P2", Path: "/tmp/p2", SortOrder: 1}); err != nil {
		t.Fatalf("save p2: %v", err)
	}
	if err := s.SaveProject(ctx, ProjectRow{ID: "p3", Name: "P3", Path: "/tmp/p3", SortOrder: 2}); err != nil {
		t.Fatalf("save p3: %v", err)
	}

	if err := s.UpdateProjectSortOrders(ctx, []string{"p3", "p1", "p2"}); err != nil {
		t.Fatalf("UpdateProjectSortOrders: %v", err)
	}

	projects, err := s.LoadProjects(ctx)
	if err != nil {
		t.Fatalf("LoadProjects: %v", err)
	}
	if len(projects) != 3 {
		t.Fatalf("got %d projects, want 3", len(projects))
	}

	want := []string{"p3", "p1", "p2"}
	for i := range want {
		if projects[i].ID != want[i] {
			t.Fatalf("project[%d].ID = %q, want %q", i, projects[i].ID, want[i])
		}
		if projects[i].SortOrder != i {
			t.Fatalf("project[%d].SortOrder = %d, want %d", i, projects[i].SortOrder, i)
		}
	}
}

func TestNextProjectSortOrder(t *testing.T) {
	s := newTestStore(t)

	next, err := s.NextProjectSortOrder(ctx)
	if err != nil {
		t.Fatalf("NextProjectSortOrder empty store: %v", err)
	}
	if next != 0 {
		t.Fatalf("next sort order = %d, want 0", next)
	}

	if err := s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P1", Path: "/tmp/p1", SortOrder: 4}); err != nil {
		t.Fatalf("save p1: %v", err)
	}
	if err := s.SaveProject(ctx, ProjectRow{ID: "p2", Name: "P2", Path: "/tmp/p2", SortOrder: 7}); err != nil {
		t.Fatalf("save p2: %v", err)
	}

	next, err = s.NextProjectSortOrder(ctx)
	if err != nil {
		t.Fatalf("NextProjectSortOrder non-empty store: %v", err)
	}
	if next != 8 {
		t.Fatalf("next sort order = %d, want 8", next)
	}
}

// ── SaveProjectWithSessions ──

func TestSaveProjectWithSessions(t *testing.T) {
	s := newTestStore(t)

	p := ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"}
	sessions := []SessionRow{
		{LocalID: "s1", ProjectID: "p1", Label: "S1", Model: "m", Pinned: true, PinnedAt: 100},
		{LocalID: "s2", ProjectID: "p1", Label: "S2", Model: "m"},
	}

	if err := s.SaveProjectWithSessions(ctx, p, sessions); err != nil {
		t.Fatalf("SaveProjectWithSessions: %v", err)
	}

	loaded, _ := s.LoadSessions(ctx, "p1")
	if len(loaded) != 2 {
		t.Fatalf("got %d sessions, want 2", len(loaded))
	}

	// Pinned session should come first (ORDER BY pinned DESC).
	if !loaded[0].Pinned {
		t.Error("first session should be pinned")
	}
}

// ── Sessions CRUD ──

func TestSaveAndLoadSession(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})

	sess := SessionRow{
		LocalID:         "s1",
		ProjectID:       "p1",
		Label:           "My Session",
		Model:           "opus",
		ThinkingEnabled: true,
		Pinned:          true,
		PinnedAt:        12345,
	}
	if err := s.SaveSession(ctx, sess); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	sessions, err := s.LoadSessions(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}

	got := sessions[0]
	if got.LocalID != "s1" || got.Label != "My Session" || got.Model != "opus" {
		t.Errorf("basic fields: %+v", got)
	}
	if !got.ThinkingEnabled || !got.Pinned || got.PinnedAt != 12345 {
		t.Errorf("flag fields: ThinkingEnabled=%v Pinned=%v PinnedAt=%d", got.ThinkingEnabled, got.Pinned, got.PinnedAt)
	}
}

func TestDeleteSession(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s1", ProjectID: "p1", Label: "S", Model: "m"})

	if err := s.DeleteSession(ctx, "s1"); err != nil {
		t.Fatal(err)
	}

	sessions, _ := s.LoadSessions(ctx, "p1")
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions after delete, got %d", len(sessions))
	}
}

func TestUpdateSessionSessionID(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s1", ProjectID: "p1", Label: "S", Model: "m"})

	if err := s.UpdateSessionSessionID(ctx, "s1", "claude-123"); err != nil {
		t.Fatal(err)
	}

	sessions, _ := s.LoadSessions(ctx, "p1")
	if sessions[0].SessionID != "claude-123" {
		t.Errorf("SessionID = %q, want %q", sessions[0].SessionID, "claude-123")
	}
}

func TestForkSessionCopiesHistoryAndResetsRuntimeIdentity(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSession(ctx, SessionRow{
		LocalID:          "source-session",
		ProjectID:        "p1",
		WorkspaceID:      "workspace-1",
		SessionID:        "runtime-session",
		Label:            "Original",
		Model:            "sonnet",
		ThinkingEnabled:  true,
		Pinned:           true,
		PinnedAt:         123,
		AutomationResult: true,
		MeetingID:        "meeting-1",
	}); err != nil {
		t.Fatal(err)
	}

	items := []StreamItemRow{
		{ID: "user-old", Kind: "user", Content: "Question", Images: `[{"mediaType":"image/png"}]`, Files: `[{"fileName":"notes.txt"}]`},
		{ID: "tool-old", Kind: "tool_use", ToolName: "Read", ToolInput: `{"path":"README.md"}`, ToolUseID: "tool-use-1", RunID: "run-1", SessionID: "runtime-session"},
		{ID: "result-old", Kind: "tool_result", Content: "contents", ToolName: "Read", ToolUseID: "tool-use-1", RunID: "run-1", SessionID: "runtime-session"},
		{ID: "summary-old", Kind: "compact_summary", Content: "Earlier discussion summary"},
		{ID: "boundary-old", Kind: "compact_boundary", Content: `{"summary_id":"summary-old","version":1}`},
		{ID: "assistant-old", Kind: "assistant", Content: "Answer", AutomationContext: `{"jobId":"job-1"}`},
	}
	for _, item := range items {
		if _, err := s.AppendStreamItem(ctx, "source-session", item); err != nil {
			t.Fatal(err)
		}
	}

	forked, count, err := s.ForkSession(ctx, "source-session", "forked-session", "Original · Branch")
	if err != nil {
		t.Fatal(err)
	}
	if count != len(items) {
		t.Fatalf("forked item count = %d, want %d", count, len(items))
	}
	if forked.ProjectID != "p1" || forked.WorkspaceID != "workspace-1" {
		t.Fatalf("forked owner = %s/%s", forked.ProjectID, forked.WorkspaceID)
	}
	if forked.SessionID != "" || forked.Pinned || forked.PinnedAt != 0 {
		t.Fatalf("runtime/sidebar state was not reset: %+v", forked)
	}
	if forked.AutomationResult || forked.MeetingID != "" {
		t.Fatalf("grouping metadata was not reset: %+v", forked)
	}
	if !forked.ThinkingEnabled || forked.Model != "sonnet" || forked.Label != "Original · Branch" {
		t.Fatalf("conversation settings were not preserved: %+v", forked)
	}

	forkedItems, err := s.LoadStreamItems(ctx, "forked-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(forkedItems) != len(items) {
		t.Fatalf("loaded %d forked items, want %d", len(forkedItems), len(items))
	}
	oldIDs := make(map[string]bool, len(items))
	for _, item := range items {
		oldIDs[item.ID] = true
	}
	for i, item := range forkedItems {
		if oldIDs[item.ID] {
			t.Errorf("forked item %d reused source id %q", i, item.ID)
		}
		if item.SortOrder != i {
			t.Errorf("forked item %d sort order = %d", i, item.SortOrder)
		}
	}
	if forkedItems[0].Images != items[0].Images || forkedItems[0].Files != items[0].Files {
		t.Errorf("forked attachment metadata was not preserved: %+v", forkedItems[0])
	}
	if forkedItems[1].ToolUseID != "tool-use-1" || forkedItems[2].ToolUseID != "tool-use-1" {
		t.Errorf("forked tool pair was not preserved")
	}
	if forkedItems[5].AutomationContext != items[5].AutomationContext {
		t.Errorf("forked automation context was not preserved")
	}

	var boundaryMetadata map[string]any
	if err := json.Unmarshal([]byte(forkedItems[4].Content), &boundaryMetadata); err != nil {
		t.Fatal(err)
	}
	if got := boundaryMetadata["summary_id"]; got != forkedItems[3].ID {
		t.Errorf("forked compact boundary summary_id = %v, want %s", got, forkedItems[3].ID)
	}

	source, err := s.LoadSession(ctx, "source-session")
	if err != nil {
		t.Fatal(err)
	}
	if !source.Pinned || source.SessionID != "runtime-session" || source.MeetingID != "meeting-1" {
		t.Fatalf("source session was mutated: %+v", source)
	}
}

func TestForkSessionMissingSourceDoesNotCreateDestination(t *testing.T) {
	s := newTestStore(t)
	if _, _, err := s.ForkSession(ctx, "missing", "forked-session", "Branch"); err == nil {
		t.Fatal("expected missing source error")
	}
	if _, err := s.LoadSession(ctx, "forked-session"); err == nil {
		t.Fatal("destination session should not exist after failed fork")
	}
}

// ── Stream Items CRUD ──

func TestAppendAndLoadStreamItems(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s1", ProjectID: "p1", Label: "S", Model: "m"})

	items := []StreamItemRow{
		{ID: "i1", Kind: "user", Content: "Hello"},
		{ID: "i2", Kind: "assistant", Content: "Hi there"},
		{ID: "i3", Kind: "tool_use", ToolName: "bash", ToolInput: `{"cmd":"ls"}`},
	}
	for _, item := range items {
		if _, err := s.AppendStreamItem(ctx, "s1", item); err != nil {
			t.Fatalf("AppendStreamItem(%s): %v", item.ID, err)
		}
	}

	loaded, err := s.LoadStreamItems(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 3 {
		t.Fatalf("got %d items, want 3", len(loaded))
	}

	// Verify sort_order is sequential.
	for i, item := range loaded {
		if item.SortOrder != i {
			t.Errorf("item[%d].SortOrder = %d, want %d", i, item.SortOrder, i)
		}
	}

	if loaded[2].ToolName != "bash" || loaded[2].ToolInput != `{"cmd":"ls"}` {
		t.Errorf("tool item: %+v", loaded[2])
	}
}

// Regression: with the app window and a browser preview both open, each
// renderer wrote every reply row of a main chat turn, so the reply showed
// twice. Both now write the same id per agent event, and the second write of
// an id stores nothing.
func TestAppendStreamItemKeepsOneRowPerID(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s1", ProjectID: "p1", Label: "S", Model: "m"})
	s.SaveSession(ctx, SessionRow{LocalID: "s2", ProjectID: "p1", Label: "S2", Model: "m"})

	first, err := s.AppendStreamItem(ctx, "s1", StreamItemRow{ID: "evt-1", Kind: "assistant", Content: "Hello"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendStreamItem(ctx, "s1", StreamItemRow{ID: "evt-2", Kind: "tool", ToolName: "Bash"}); err != nil {
		t.Fatal(err)
	}
	again, err := s.AppendStreamItem(ctx, "s1", StreamItemRow{ID: "evt-1", Kind: "assistant", Content: "Hello"})
	if err != nil {
		t.Fatalf("second write of the same id: %v", err)
	}
	if again != first {
		t.Fatalf("second write returned sort_order %d, want the first row's %d", again, first)
	}

	loaded, err := s.LoadStreamItems(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("got %d rows, want 2: %+v", len(loaded), loaded)
	}

	if _, err := s.AppendStreamItem(ctx, "s2", StreamItemRow{ID: "evt-1", Kind: "assistant"}); !errors.Is(err, ErrStreamItemIDTaken) {
		t.Fatalf("id of another session's row: err = %v, want ErrStreamItemIDTaken", err)
	}
}

// A renderer that missed chunks of a reply must not cut down the text another
// stored: streamed assistant and thinking rows only grow, measured in bytes so
// a NUL or multi-byte text is measured whole. Other rows (an edited prompt)
// take what they are given, and a row of another chat is not written.
func TestGrowStreamItemContentNeverShortensAReply(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s1", ProjectID: "p1", Label: "S", Model: "m"})
	s.SaveSession(ctx, SessionRow{LocalID: "s2", ProjectID: "p1", Label: "S2", Model: "m"})
	for _, item := range []StreamItemRow{
		{ID: "a", Kind: "assistant", Content: "I'll"},
		{ID: "t", Kind: "thinking", Content: "Hmm"},
		{ID: "u", Kind: "user", Content: "Help me prepare"},
		{ID: "nul", Kind: "assistant", Content: "a\x00bcdef"},
		{ID: "cjk", Kind: "assistant", Content: "后台脚本"},
	} {
		if _, err := s.AppendStreamItem(ctx, "s1", item); err != nil {
			t.Fatal(err)
		}
	}

	for _, step := range []struct {
		session, id, content string
		applied              bool
	}{
		{"s1", "a", "I'll check.", true},
		{"s1", "a", "check.", false},
		{"s1", "t", "Hmm, the calendar", true},
		{"s1", "t", "", false},
		{"s1", "u", "Help", true},
		{"s2", "a", "I'll check. And more from another chat.", false},
		// "a\x00bcdef" is 8 bytes; a NUL must not make it look 1 long.
		{"s1", "nul", "abc", false},
		{"s1", "nul", "a\x00bcdefgh", true},
		// 4 characters, 12 bytes; 5 ASCII characters are shorter.
		{"s1", "cjk", "abcde", false},
		{"s1", "cjk", "后台脚本已完成", true},
	} {
		applied, err := s.GrowStreamItemContent(ctx, step.session, step.id, step.content)
		if err != nil {
			t.Fatal(err)
		}
		if applied != step.applied {
			t.Errorf("grow %s/%s to %q: applied = %v, want %v", step.session, step.id, step.content, applied, step.applied)
		}
	}

	loaded, err := s.LoadStreamItems(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, item := range loaded {
		got[item.ID] = item.Content
	}
	want := map[string]string{
		"a":   "I'll check.",
		"t":   "Hmm, the calendar",
		"u":   "Help",
		"nul": "a\x00bcdefgh",
		"cjk": "后台脚本已完成",
	}
	for id, content := range want {
		if got[id] != content {
			t.Errorf("row %s = %q, want %q", id, got[id], content)
		}
	}
}

func TestAppendAndLoadStreamItems_ImagesRoundTrip(t *testing.T) {
	// Regression: user-uploaded images used to vanish on session reload
	// because the stream_items table had no column for them. v36 added
	// an `images` TEXT column that holds a JSON-encoded attachment list.
	// This covers the save → reload round-trip and the "no images"
	// default case so a future refactor can't silently regress either.
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s1", ProjectID: "p1", Label: "S", Model: "m"})

	imagesJSON := `[{"mediaType":"image/png","dataBase64":"iVBORw0KGgo="}]`
	items := []StreamItemRow{
		{ID: "i-with-images", Kind: "user", Content: "look", Images: imagesJSON},
		{ID: "i-no-images", Kind: "user", Content: "plain"},
		{ID: "i-assistant", Kind: "assistant", Content: "hi"},
	}
	for _, item := range items {
		if _, err := s.AppendStreamItem(ctx, "s1", item); err != nil {
			t.Fatalf("AppendStreamItem(%s): %v", item.ID, err)
		}
	}

	loaded, err := s.LoadStreamItems(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 3 {
		t.Fatalf("got %d items, want 3", len(loaded))
	}
	if loaded[0].Images != imagesJSON {
		t.Errorf("item[0].Images = %q, want %q", loaded[0].Images, imagesJSON)
	}
	if loaded[1].Images != "" {
		t.Errorf("item[1].Images = %q, want empty", loaded[1].Images)
	}
	if loaded[2].Images != "" {
		t.Errorf("item[2].Images = %q, want empty", loaded[2].Images)
	}

	// Cursor-paginated load path used by the UI must also preserve the
	// JSON blob so the composer can render attachments on reload.
	page, err := s.LoadStreamItemsLatest(ctx, "s1", 10)
	if err != nil {
		t.Fatal(err)
	}
	var pageImages string
	for _, it := range page.Items {
		if it.ID == "i-with-images" {
			pageImages = it.Images
			break
		}
	}
	if pageImages != imagesJSON {
		t.Errorf("paged load dropped images: got %q, want %q", pageImages, imagesJSON)
	}
}

func TestSaveStreamItemsBatch(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s1", ProjectID: "p1", Label: "S", Model: "m"})

	items := []StreamItemRow{
		{ID: "i1", Kind: "user", Content: "A"},
		{ID: "i2", Kind: "assistant", Content: "B"},
	}
	if err := s.SaveStreamItems(ctx, "s1", items); err != nil {
		t.Fatal(err)
	}

	loaded, _ := s.LoadStreamItems(ctx, "s1")
	if len(loaded) != 2 {
		t.Fatalf("got %d, want 2", len(loaded))
	}
}

func TestUpdateStreamItemContent(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s1", ProjectID: "p1", Label: "S", Model: "m"})
	s.AppendStreamItem(ctx, "s1", StreamItemRow{ID: "i1", Kind: "assistant", Content: "partial"})

	if err := s.UpdateStreamItemContent(ctx, "i1", "full response"); err != nil {
		t.Fatal(err)
	}

	loaded, _ := s.LoadStreamItems(ctx, "s1")
	if loaded[0].Content != "full response" {
		t.Errorf("Content = %q, want %q", loaded[0].Content, "full response")
	}
}

func TestDeleteStreamItems(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s1", ProjectID: "p1", Label: "S", Model: "m"})
	s.AppendStreamItem(ctx, "s1", StreamItemRow{ID: "i1", Kind: "user", Content: "X"})

	if err := s.DeleteStreamItems(ctx, "s1"); err != nil {
		t.Fatal(err)
	}

	loaded, _ := s.LoadStreamItems(ctx, "s1")
	if len(loaded) != 0 {
		t.Errorf("expected 0 items, got %d", len(loaded))
	}
}

func TestDeleteSessionCascadesItems(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s1", ProjectID: "p1", Label: "S", Model: "m"})
	s.AppendStreamItem(ctx, "s1", StreamItemRow{ID: "i1", Kind: "user", Content: "X"})

	s.DeleteSession(ctx, "s1")

	loaded, _ := s.LoadStreamItems(ctx, "s1")
	if len(loaded) != 0 {
		t.Errorf("expected cascade delete of items, got %d", len(loaded))
	}
}

// ── Cursor Pagination ──

// seedItems creates a session with n stream items (sort_order 0..n-1).
func seedItems(t *testing.T, s *Store, n int) {
	t.Helper()
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s1", ProjectID: "p1", Label: "S", Model: "m"})
	for i := 0; i < n; i++ {
		if _, err := s.AppendStreamItem(ctx, "s1", StreamItemRow{
			ID:      fmt.Sprintf("i%d", i),
			Kind:    "user",
			Content: fmt.Sprintf("msg-%d", i),
		}); err != nil {
			t.Fatalf("seed item %d: %v", i, err)
		}
	}
}

func TestLoadStreamItemsLatest(t *testing.T) {
	s := newTestStore(t)
	seedItems(t, s, 10) // sort_order 0..9

	page, err := s.LoadStreamItemsLatest(ctx, "s1", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("got %d items, want 3", len(page.Items))
	}
	if !page.HasMore {
		t.Error("expected HasMore=true with 10 items and limit=3")
	}
	// Should be the last 3 items in ascending order.
	if page.Items[0].SortOrder != 7 || page.Items[1].SortOrder != 8 || page.Items[2].SortOrder != 9 {
		t.Errorf("sort_orders = [%d,%d,%d], want [7,8,9]",
			page.Items[0].SortOrder, page.Items[1].SortOrder, page.Items[2].SortOrder)
	}
}

func TestLoadStreamItemsLatestAll(t *testing.T) {
	s := newTestStore(t)
	seedItems(t, s, 3)

	page, err := s.LoadStreamItemsLatest(ctx, "s1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("got %d items, want 3", len(page.Items))
	}
	if page.HasMore {
		t.Error("expected HasMore=false when all items fit in limit")
	}
}

func TestLoadStreamItemsBefore(t *testing.T) {
	s := newTestStore(t)
	seedItems(t, s, 10) // sort_order 0..9

	// Load 3 items before cursor=7 → should get sort_order 4,5,6
	page, err := s.LoadStreamItemsBefore(ctx, "s1", 7, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("got %d items, want 3", len(page.Items))
	}
	if !page.HasMore {
		t.Error("expected HasMore=true")
	}
	if page.Items[0].SortOrder != 4 || page.Items[1].SortOrder != 5 || page.Items[2].SortOrder != 6 {
		t.Errorf("sort_orders = [%d,%d,%d], want [4,5,6]",
			page.Items[0].SortOrder, page.Items[1].SortOrder, page.Items[2].SortOrder)
	}
}

func TestLoadStreamItemsBeforeExhausted(t *testing.T) {
	s := newTestStore(t)
	seedItems(t, s, 5) // sort_order 0..4

	// Load 10 items before cursor=3 → should get sort_order 0,1,2
	page, err := s.LoadStreamItemsBefore(ctx, "s1", 3, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 {
		t.Fatalf("got %d items, want 3", len(page.Items))
	}
	if page.HasMore {
		t.Error("expected HasMore=false when no more items before cursor")
	}
}

func TestCursorPaginationWalkback(t *testing.T) {
	s := newTestStore(t)
	seedItems(t, s, 10) // sort_order 0..9

	// Simulate infinite scroll: latest → scroll up → scroll up again.
	p1, _ := s.LoadStreamItemsLatest(ctx, "s1", 4) // [6,7,8,9]
	if len(p1.Items) != 4 || p1.Items[0].SortOrder != 6 {
		t.Fatalf("p1: %+v", p1)
	}

	cursor := p1.Items[0].SortOrder                        // cursor = 6
	p2, _ := s.LoadStreamItemsBefore(ctx, "s1", cursor, 4) // [2,3,4,5]
	if len(p2.Items) != 4 || p2.Items[0].SortOrder != 2 {
		t.Fatalf("p2: %+v", p2)
	}

	cursor = p2.Items[0].SortOrder                         // cursor = 2
	p3, _ := s.LoadStreamItemsBefore(ctx, "s1", cursor, 4) // [0,1]
	if len(p3.Items) != 2 {
		t.Fatalf("p3: got %d items, want 2", len(p3.Items))
	}
	if p3.HasMore {
		t.Error("p3: expected HasMore=false at the beginning of the conversation")
	}
}

func TestLoadRecentStreamItems(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s1", ProjectID: "p1", Label: "S1", Model: "m"})
	s.SaveSession(ctx, SessionRow{LocalID: "s2", ProjectID: "p1", Label: "S2", Model: "m"})

	// Seed 5 items in s1 and 3 items in s2.
	for i := 0; i < 5; i++ {
		s.AppendStreamItem(ctx, "s1", StreamItemRow{ID: fmt.Sprintf("s1i%d", i), Kind: "user", Content: fmt.Sprintf("s1-%d", i)})
	}
	for i := 0; i < 3; i++ {
		s.AppendStreamItem(ctx, "s2", StreamItemRow{ID: fmt.Sprintf("s2i%d", i), Kind: "user", Content: fmt.Sprintf("s2-%d", i)})
	}

	// Limit 2 per session.
	result, err := s.LoadRecentStreamItems(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(result["s1"]) != 2 {
		t.Errorf("s1: got %d items, want 2", len(result["s1"]))
	}
	if len(result["s2"]) != 2 {
		t.Errorf("s2: got %d items, want 2", len(result["s2"]))
	}

	// Should be the latest 2 in ascending order.
	if result["s1"][0].SortOrder != 3 || result["s1"][1].SortOrder != 4 {
		t.Errorf("s1 sort_orders = [%d,%d], want [3,4]", result["s1"][0].SortOrder, result["s1"][1].SortOrder)
	}
}

func TestLoadRecentStreamItemsByTurns_PreservesPreUserContent(t *testing.T) {
	// Regression: legacy transcript chat sessions persist N assistant
	// messages (the transcript body) BEFORE any user message. The user
	// then asks a follow-up question, adding a user message at the end.
	// The turn cutoff used to anchor on that user message and exclude
	// everything before it, making the transcript disappear on reload.
	// When a session has fewer user messages than the requested turn
	// limit, cutoff should fall back to 0 so the preamble stays.
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s-transcript", ProjectID: "p1", Label: "Transcript", Model: "m"})

	// 8 pre-user assistant messages (transcript body).
	for i := 0; i < 8; i++ {
		s.AppendStreamItem(ctx, "s-transcript", StreamItemRow{
			ID:      fmt.Sprintf("t-a%d", i),
			Kind:    "assistant",
			Content: fmt.Sprintf("transcript-line-%d", i),
		})
	}
	// 1 user follow-up + 1 assistant reply.
	s.AppendStreamItem(ctx, "s-transcript", StreamItemRow{ID: "t-u0", Kind: "user", Content: "what was discussed?"})
	s.AppendStreamItem(ctx, "s-transcript", StreamItemRow{ID: "t-a-reply", Kind: "assistant", Content: "summary"})

	result, err := s.LoadRecentStreamItemsByTurns(ctx, 20)
	if err != nil {
		t.Fatal(err)
	}
	items := result["s-transcript"]
	if len(items) != 10 {
		t.Fatalf("want all 10 items to be loaded, got %d: %+v", len(items), items)
	}
	// First item should be the very start of the transcript, not the user message.
	if items[0].ID != "t-a0" {
		t.Errorf("first item = %q, want t-a0 (pre-user content preserved)", items[0].ID)
	}
}

func TestLoadRecentStreamItemsByTurns_LimitsLongSessions(t *testing.T) {
	// When a session has MORE user-message turns than the limit, the
	// cutoff should still chop off older turns — this is the original
	// behavior that keeps long chats bounded.
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s-long", ProjectID: "p1", Label: "Long", Model: "m"})

	// 25 user+assistant turn pairs (50 items total).
	for i := 0; i < 25; i++ {
		s.AppendStreamItem(ctx, "s-long", StreamItemRow{ID: fmt.Sprintf("u%d", i), Kind: "user", Content: fmt.Sprintf("q%d", i)})
		s.AppendStreamItem(ctx, "s-long", StreamItemRow{ID: fmt.Sprintf("a%d", i), Kind: "assistant", Content: fmt.Sprintf("r%d", i)})
	}

	result, err := s.LoadRecentStreamItemsByTurns(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	items := result["s-long"]
	// Last 5 user turns = items from u20 onward → 10 items.
	if len(items) != 10 {
		t.Fatalf("want 10 items (last 5 turns), got %d", len(items))
	}
	if items[0].ID != "u20" {
		t.Errorf("first item = %q, want u20 (turn cutoff applied)", items[0].ID)
	}
}

func TestLoadStreamItemsLatestEmpty(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s1", ProjectID: "p1", Label: "S", Model: "m"})

	page, err := s.LoadStreamItemsLatest(ctx, "s1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Errorf("got %d items, want 0", len(page.Items))
	}
	if page.HasMore {
		t.Error("expected HasMore=false for empty session")
	}
}

// ── UI State ──

func TestSetAndGetUIState(t *testing.T) {
	s := newTestStore(t)

	if err := s.SetUIState(ctx, "key1", "value1"); err != nil {
		t.Fatal(err)
	}

	val, err := s.GetUIState(ctx, "key1")
	if err != nil {
		t.Fatal(err)
	}
	if val != "value1" {
		t.Errorf("got %q, want %q", val, "value1")
	}
}

func TestGetUIStateMissingKey(t *testing.T) {
	s := newTestStore(t)

	val, err := s.GetUIState(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("expected nil error for missing key, got %v", err)
	}
	if val != "" {
		t.Errorf("expected empty string, got %q", val)
	}
}

func TestSetUIStateUpsert(t *testing.T) {
	s := newTestStore(t)
	s.SetUIState(ctx, "k", "v1")
	s.SetUIState(ctx, "k", "v2")

	val, _ := s.GetUIState(ctx, "k")
	if val != "v2" {
		t.Errorf("got %q, want %q", val, "v2")
	}
}

// ── Workspace CRUD ──

func TestWorkspaceRoundTrip(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "Test", Path: "/tmp/test"})

	ws := WorkspaceRow{
		ID:             "ws-1",
		ProjectID:      "p1",
		Label:          "Feature Branch",
		WorktreePath:   "/home/user/.yansu-agent/worktrees/p1/ws-1",
		WorktreeBranch: "workspace/ws-1",
		BaseBranch:     "main",
	}
	if err := s.SaveWorkspace(ctx, ws); err != nil {
		t.Fatalf("SaveWorkspace: %v", err)
	}

	got, err := s.LoadWorkspace(ctx, "ws-1")
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}
	if got.ID != "ws-1" {
		t.Errorf("ID = %q, want %q", got.ID, "ws-1")
	}
	if got.Label != "Feature Branch" {
		t.Errorf("Label = %q, want %q", got.Label, "Feature Branch")
	}
	if got.WorktreePath != ws.WorktreePath {
		t.Errorf("WorktreePath = %q, want %q", got.WorktreePath, ws.WorktreePath)
	}
	if got.WorktreeBranch != ws.WorktreeBranch {
		t.Errorf("WorktreeBranch = %q, want %q", got.WorktreeBranch, ws.WorktreeBranch)
	}
	if got.BaseBranch != "main" {
		t.Errorf("BaseBranch = %q, want %q", got.BaseBranch, "main")
	}

	// Non-existent workspace should return error.
	_, err = s.LoadWorkspace(ctx, "nonexistent")
	if err == nil {
		t.Error("LoadWorkspace for nonexistent ID should return error")
	}
}

func TestLoadWorkspacesByProject(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "Test", Path: "/tmp/test"})

	s.SaveWorkspace(ctx, WorkspaceRow{ID: "ws-default", ProjectID: "p1", Label: "Default", IsDefault: true})
	s.SaveWorkspace(ctx, WorkspaceRow{ID: "ws-feature", ProjectID: "p1", Label: "Feature", WorktreeBranch: "workspace/ws-feature"})
	s.SaveWorkspace(ctx, WorkspaceRow{ID: "ws-hidden", ProjectID: "p1", Label: "Hidden", Hidden: true})

	// LoadWorkspaces excludes hidden workspaces.
	visible, err := s.LoadWorkspaces(ctx, "p1")
	if err != nil {
		t.Fatalf("LoadWorkspaces: %v", err)
	}
	if len(visible) != 2 {
		t.Fatalf("LoadWorkspaces: expected 2 visible, got %d", len(visible))
	}

	// LoadWorkspacesByProject includes hidden workspaces.
	all, err := s.LoadWorkspacesByProject(ctx, "p1")
	if err != nil {
		t.Fatalf("LoadWorkspacesByProject: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("LoadWorkspacesByProject: expected 3 (incl. hidden), got %d", len(all))
	}

	// Verify the hidden workspace has Hidden == true.
	var foundHidden bool
	for _, ws := range all {
		if ws.ID == "ws-hidden" {
			foundHidden = true
			if !ws.Hidden {
				t.Fatalf("LoadWorkspacesByProject: expected ws-hidden to have Hidden == true, got false")
			}
			break
		}
	}
	if !foundHidden {
		t.Fatalf("LoadWorkspacesByProject: expected to find workspace with ID ws-hidden")
	}
}

func TestLoadProjectByPath(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p-visible", Name: "Visible", Path: "/tmp/visible"})
	s.SaveProject(ctx, ProjectRow{ID: "p-hidden", Name: "Hidden", Path: "__cron__/job123", Hidden: true})

	// LoadProjects excludes hidden.
	projects, _ := s.LoadProjects(ctx)
	if len(projects) != 1 {
		t.Fatalf("LoadProjects: expected 1 visible, got %d", len(projects))
	}

	// LoadProjectByPath finds hidden projects.
	p, err := s.LoadProjectByPath(ctx, "__cron__/job123")
	if err != nil {
		t.Fatalf("LoadProjectByPath hidden: %v", err)
	}
	if p.ID != "p-hidden" {
		t.Fatalf("LoadProjectByPath: expected p-hidden, got %s", p.ID)
	}

	// LoadProjectByPath finds visible projects too.
	p, err = s.LoadProjectByPath(ctx, "/tmp/visible")
	if err != nil {
		t.Fatalf("LoadProjectByPath visible: %v", err)
	}
	if p.ID != "p-visible" {
		t.Fatalf("LoadProjectByPath: expected p-visible, got %s", p.ID)
	}

	// Non-existent path returns error.
	_, err = s.LoadProjectByPath(ctx, "/no/such/path")
	if err == nil {
		t.Fatal("LoadProjectByPath: expected error for missing path")
	}
}

func TestLoadAllWorkspaces(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "Test1", Path: "/tmp/test1"})
	s.SaveProject(ctx, ProjectRow{ID: "p2", Name: "Test2", Path: "/tmp/test2"})

	s.SaveWorkspace(ctx, WorkspaceRow{ID: "ws-1", ProjectID: "p1", Label: "Default"})
	s.SaveWorkspace(ctx, WorkspaceRow{ID: "ws-2", ProjectID: "p2", Label: "Default"})

	all, err := s.LoadAllWorkspaces(ctx)
	if err != nil {
		t.Fatalf("LoadAllWorkspaces: %v", err)
	}
	if len(all["p1"]) != 1 {
		t.Errorf("p1 workspaces = %d, want 1", len(all["p1"]))
	}
	if len(all["p2"]) != 1 {
		t.Errorf("p2 workspaces = %d, want 1", len(all["p2"]))
	}
}

func TestDeleteWorkspaceCascade(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "Test", Path: "/tmp/test"})
	s.SaveWorkspace(ctx, WorkspaceRow{ID: "ws-1", ProjectID: "p1", Label: "Feature"})

	// Add a session in the workspace.
	s.SaveSession(ctx, SessionRow{
		LocalID:     "s-1",
		ProjectID:   "p1",
		WorkspaceID: "ws-1",
		Label:       "Session 1",
		Model:       "opus",
	})

	// Add a stream item to the session.
	s.AppendStreamItem(ctx, "s-1", StreamItemRow{ID: "item-1", Kind: "user"})

	// Delete workspace should cascade.
	if err := s.DeleteWorkspace(ctx, "ws-1"); err != nil {
		t.Fatalf("DeleteWorkspace: %v", err)
	}

	// Workspace should be gone.
	_, err := s.LoadWorkspace(ctx, "ws-1")
	if err == nil {
		t.Error("workspace should be deleted")
	}

	// Session should be gone.
	_, err = s.LoadSession(ctx, "s-1")
	if err == nil {
		t.Error("session should be cascade-deleted with workspace")
	}
}

func TestSessionWorkspaceID(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "Test", Path: "/tmp/test"})
	s.SaveWorkspace(ctx, WorkspaceRow{ID: "ws-1", ProjectID: "p1", Label: "Default"})

	sess := SessionRow{
		LocalID:     "s-1",
		ProjectID:   "p1",
		WorkspaceID: "ws-1",
		Label:       "Session 1",
		Model:       "opus",
	}
	if err := s.SaveSession(ctx, sess); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	got, err := s.LoadSession(ctx, "s-1")
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got.WorkspaceID != "ws-1" {
		t.Errorf("WorkspaceID = %q, want %q", got.WorkspaceID, "ws-1")
	}
}

func TestLoadAllSessionsByWorkspace(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "Test", Path: "/tmp/test"})
	s.SaveWorkspace(ctx, WorkspaceRow{ID: "ws-1", ProjectID: "p1", Label: "Default"})
	s.SaveWorkspace(ctx, WorkspaceRow{ID: "ws-2", ProjectID: "p1", Label: "Feature"})

	s.SaveSession(ctx, SessionRow{LocalID: "s-1", ProjectID: "p1", WorkspaceID: "ws-1", Label: "S1", Model: "opus"})
	s.SaveSession(ctx, SessionRow{LocalID: "s-2", ProjectID: "p1", WorkspaceID: "ws-1", Label: "S2", Model: "opus"})
	s.SaveSession(ctx, SessionRow{LocalID: "s-3", ProjectID: "p1", WorkspaceID: "ws-2", Label: "S3", Model: "opus"})

	byWs, err := s.LoadAllSessionsByWorkspace(ctx)
	if err != nil {
		t.Fatalf("LoadAllSessionsByWorkspace: %v", err)
	}
	if len(byWs["ws-1"]) != 2 {
		t.Errorf("ws-1 sessions = %d, want 2", len(byWs["ws-1"]))
	}
	if len(byWs["ws-2"]) != 1 {
		t.Errorf("ws-2 sessions = %d, want 1", len(byWs["ws-2"]))
	}
}

// ── New() edge cases ──

func TestNewCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "subdir", "nested", "test.db")

	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer s.Close()

	if _, err := os.Stat(filepath.Dir(dbPath)); err != nil {
		t.Errorf("directory not created: %v", err)
	}
}

// ── Checkpoint cleanup ──

func TestCleanupOrphansCheckpoints(t *testing.T) {
	s := newTestStore(t)

	// Create a project and session.
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s1", ProjectID: "p1", Label: "S", Model: "m"})

	// Insert checkpoint files for the existing session.
	s.SaveCheckpointFile(ctx, CheckpointFileRow{
		SessionLocalID: "s1", SortOrder: 0, FilePath: "/tmp/a.txt",
		BackupPath: "/tmp/bak/a", Existed: true, CreatedAt: 1,
	})

	// Insert an orphaned checkpoint file (session doesn't exist).
	s.SaveCheckpointFile(ctx, CheckpointFileRow{
		SessionLocalID: "nonexistent", SortOrder: 0, FilePath: "/tmp/b.txt",
		BackupPath: "/tmp/bak/b", Existed: true, CreatedAt: 1,
	})

	// Run cleanupOrphans.
	s.cleanupOrphans()

	// Valid checkpoint should remain.
	rows, err := s.LoadCheckpointFilesFrom(ctx, "s1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("expected 1 checkpoint for s1, got %d", len(rows))
	}

	// Orphaned checkpoint should be cleaned up.
	rows, err = s.LoadCheckpointFilesFrom(ctx, "nonexistent", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 orphan checkpoints, got %d", len(rows))
	}
}

func TestCleanupOrphansDanglingActiveSession(t *testing.T) {
	s := newTestStore(t)

	// Create project and workspace with active_session_id pointing to a non-existent session.
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p", ActiveSessionID: "deleted-session"})
	s.SaveWorkspace(ctx, WorkspaceRow{ID: "ws1", ProjectID: "p1", Label: "W", ActiveSessionID: "deleted-session"})

	// Run cleanupOrphans.
	s.cleanupOrphans()

	// Project's active_session_id should be cleared.
	projects, _ := s.LoadProjects(ctx)
	if projects[0].ActiveSessionID != "" {
		t.Errorf("project active_session_id = %q, want empty", projects[0].ActiveSessionID)
	}

	// Workspace's active_session_id should be cleared.
	ws, _ := s.LoadWorkspace(ctx, "ws1")
	if ws.ActiveSessionID != "" {
		t.Errorf("workspace active_session_id = %q, want empty", ws.ActiveSessionID)
	}
}

func TestSaveStreamItemsBatchAppend(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s1", ProjectID: "p1", Label: "S", Model: "m"})

	// Insert initial items via AppendStreamItem (sort_order 0, 1, 2).
	for i := 0; i < 3; i++ {
		if _, err := s.AppendStreamItem(ctx, "s1", StreamItemRow{
			ID: fmt.Sprintf("existing-%d", i), Kind: "user", Content: fmt.Sprintf("msg-%d", i),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Batch-insert more items via SaveStreamItems.
	batch := []StreamItemRow{
		{ID: "batch-0", Kind: "user", Content: "batch-0"},
		{ID: "batch-1", Kind: "assistant", Content: "batch-1"},
	}
	if err := s.SaveStreamItems(ctx, "s1", batch); err != nil {
		t.Fatal(err)
	}

	// Load all and verify sort_order continuity.
	all, err := s.LoadStreamItems(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 {
		t.Fatalf("got %d items, want 5", len(all))
	}

	// Batch items should start from sort_order 3 (max+1).
	if all[3].SortOrder != 3 || all[3].ID != "batch-0" {
		t.Errorf("item[3]: sort_order=%d id=%s, want sort_order=3 id=batch-0", all[3].SortOrder, all[3].ID)
	}
	if all[4].SortOrder != 4 || all[4].ID != "batch-1" {
		t.Errorf("item[4]: sort_order=%d id=%s, want sort_order=4 id=batch-1", all[4].SortOrder, all[4].ID)
	}
}

func TestDeleteCheckpointFilesForSession(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s1", ProjectID: "p1", Label: "S", Model: "m"})

	// Insert checkpoint records.
	s.SaveCheckpointFile(ctx, CheckpointFileRow{
		SessionLocalID: "s1", SortOrder: 0, FilePath: "/tmp/a.txt",
		BackupPath: "/tmp/bak/a", Existed: true, CreatedAt: 1,
	})
	s.SaveCheckpointFile(ctx, CheckpointFileRow{
		SessionLocalID: "s1", SortOrder: 1, FilePath: "/tmp/b.txt",
		BackupPath: "/tmp/bak/b", Existed: true, CreatedAt: 2,
	})

	// Delete checkpoint records for the session.
	deleted, err := s.DeleteCheckpointFilesForSession(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 2 {
		t.Errorf("expected 2 deleted rows, got %d", len(deleted))
	}

	// Verify no records remain.
	remaining, _ := s.LoadCheckpointFilesFrom(ctx, "s1", 0)
	if len(remaining) != 0 {
		t.Errorf("expected 0 remaining checkpoints, got %d", len(remaining))
	}
}

func TestVacuum(t *testing.T) {
	s := newTestStore(t)

	// Insert and delete some data to create free pages.
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s1", ProjectID: "p1", Label: "S", Model: "m"})
	for i := 0; i < 100; i++ {
		s.AppendStreamItem(ctx, "s1", StreamItemRow{
			ID: fmt.Sprintf("i%d", i), Kind: "user", Content: "data",
		})
	}
	s.DeleteProject(ctx, "p1")

	// Vacuum should complete without error.
	if err := s.Vacuum(); err != nil {
		t.Fatalf("Vacuum: %v", err)
	}
}

func TestClearActiveSessionReferences(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p", ActiveSessionID: "s1"})
	s.SaveWorkspace(ctx, WorkspaceRow{ID: "ws1", ProjectID: "p1", Label: "W", ActiveSessionID: "s1"})
	// Another project/workspace pointing to a different session should be unaffected.
	s.SaveProject(ctx, ProjectRow{ID: "p2", Name: "P2", Path: "/tmp/p2", ActiveSessionID: "s2"})
	s.SaveWorkspace(ctx, WorkspaceRow{ID: "ws2", ProjectID: "p2", Label: "W2", ActiveSessionID: "s2"})

	if err := s.ClearActiveSessionReferences(ctx, "s1"); err != nil {
		t.Fatal(err)
	}

	// p1 and ws1 should be cleared.
	projects, _ := s.LoadProjects(ctx)
	for _, p := range projects {
		switch p.ID {
		case "p1":
			if p.ActiveSessionID != "" {
				t.Errorf("p1 active_session_id = %q, want empty", p.ActiveSessionID)
			}
		case "p2":
			if p.ActiveSessionID != "s2" {
				t.Errorf("p2 active_session_id = %q, want s2", p.ActiveSessionID)
			}
		}
	}

	ws1, _ := s.LoadWorkspace(ctx, "ws1")
	if ws1.ActiveSessionID != "" {
		t.Errorf("ws1 active_session_id = %q, want empty", ws1.ActiveSessionID)
	}

	ws2, _ := s.LoadWorkspace(ctx, "ws2")
	if ws2.ActiveSessionID != "s2" {
		t.Errorf("ws2 active_session_id = %q, want s2", ws2.ActiveSessionID)
	}
}

func TestLoadCheckpointSessionIDs(t *testing.T) {
	s := newTestStore(t)
	s.SaveProject(ctx, ProjectRow{ID: "p1", Name: "P", Path: "/tmp/p"})
	s.SaveSession(ctx, SessionRow{LocalID: "s1", ProjectID: "p1", Label: "S1", Model: "m"})
	s.SaveSession(ctx, SessionRow{LocalID: "s2", ProjectID: "p1", Label: "S2", Model: "m"})

	// Insert checkpoints for two sessions.
	s.SaveCheckpointFile(ctx, CheckpointFileRow{
		SessionLocalID: "s1", SortOrder: 0, FilePath: "/tmp/a.txt",
		BackupPath: "/tmp/bak/a", Existed: true, CreatedAt: 1,
	})
	s.SaveCheckpointFile(ctx, CheckpointFileRow{
		SessionLocalID: "s2", SortOrder: 0, FilePath: "/tmp/b.txt",
		BackupPath: "/tmp/bak/b", Existed: true, CreatedAt: 1,
	})

	ids, err := s.LoadCheckpointSessionIDs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Errorf("expected 2 session IDs, got %d", len(ids))
	}
}

// The admission fence must observe writes through the normal connection and
// a separate writer, and keep both excluded until publication releases it.
func TestReadRevisionFencesLocalAndExternalWrites(t *testing.T) {
	s := newTestStore(t)
	var path string
	if err := s.db.QueryRow(`SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	external, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer external.Close()
	for _, db := range []*sql.DB{s.db, external} {
		before, err := s.ReadRevision(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT OR REPLACE INTO ui_state(key,value) VALUES('fence','changed')`); err != nil {
			t.Fatal(err)
		}
		if release, err := LockDatabaseRevision(t.Context(), s.db, before); !errors.Is(err, ErrReadRevisionChanged) {
			if release != nil {
				release()
			}
			t.Fatalf("changed database retained its old revision: %v", err)
		}
	}
	current, err := s.ReadRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	release, err := LockDatabaseRevision(t.Context(), s.db, current)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	readContext, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := s.SetUIState(readContext, "fence", "blocked"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("local writer escaped fence: %v", err)
	}
	if _, err := external.Exec(`UPDATE ui_state SET value='blocked' WHERE key='fence'`); err == nil {
		t.Fatal("external writer escaped fence")
	}
	release()
	release()
	if err := s.SetUIState(t.Context(), "fence", "released"); err != nil {
		t.Fatalf("fence did not release: %v", err)
	}
}
