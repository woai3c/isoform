package storage

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

func migrationTestDatabase(t *testing.T, version int) string {
	t.Helper()
	// Exercise URI encoding as well as ordinary filenames during read-only checks.
	dbPath := filepath.Join(t.TempDir(), "isoform [dev] #1's.db")
	s, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
		t.Fatal(err)
	}
	if version < 57 {
		if _, err := s.db.Exec("DROP TABLE mail_messages"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

func migrationSnapshots(t *testing.T, dbPath string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), filepath.Base(dbPath)+".snapshot-") {
			paths = append(paths, filepath.Join(filepath.Dir(dbPath), entry.Name()))
		}
	}
	sort.Strings(paths)
	return paths
}

func TestNewRefusesNewerSchemaWithoutChangingDatabase(t *testing.T) {
	for _, journalMode := range []string{"DELETE", "WAL"} {
		t.Run(journalMode, func(t *testing.T) {
			testNewRefusesNewerSchemaWithoutChangingDatabase(t, journalMode)
		})
	}
}

func testNewRefusesNewerSchemaWithoutChangingDatabase(t *testing.T, journalMode string) {
	newerVersion := supportedSchemaVersion + 1
	dbPath := migrationTestDatabase(t, newerVersion)
	// Changing DELETE to WAL before the guard would mutate the DB. A SQLite
	// read-only open of a WAL database can also create sidecars unnecessarily.
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("PRAGMA journal_mode=" + journalMode); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	entriesBefore, err := os.ReadDir(filepath.Dir(dbPath))
	if err != nil {
		t.Fatal(err)
	}

	s, err := New(dbPath)
	if s != nil {
		s.Close()
		t.Fatal("newer schema returned a writable store")
	}
	var versionErr *SchemaVersionError
	if !errors.As(err, &versionErr) {
		t.Fatalf("New error = %v, want SchemaVersionError", err)
	}
	if versionErr.StoredVersion != newerVersion || versionErr.SupportedVersion != supportedSchemaVersion {
		t.Fatalf("schema error = %+v", versionErr)
	}
	if !strings.Contains(err.Error(), "newer Isoform build") {
		t.Fatalf("error lacks recovery guidance: %v", err)
	}
	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("rejecting the newer schema changed database bytes")
	}
	entriesAfter, err := os.ReadDir(filepath.Dir(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(entriesBefore, entriesAfter) {
		t.Fatalf("rejection changed directory entries: before %v, after %v", entriesBefore, entriesAfter)
	}
}

func TestMigrateRefusesNewerSchema(t *testing.T) {
	s := newTestStore(t)
	newerVersion := supportedSchemaVersion + 1
	if _, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version=%d", newerVersion)); err != nil {
		t.Fatal(err)
	}
	var versionErr *SchemaVersionError
	if err := s.migrate(); !errors.As(err, &versionErr) {
		t.Fatalf("migrate error = %v, want SchemaVersionError", err)
	}
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != newerVersion {
		t.Fatalf("version after rejected migration = %d, error %v", version, err)
	}
}

func TestNewRefusesNewerSchemaCommittedInWAL(t *testing.T) {
	newerVersion := supportedSchemaVersion + 1
	dbPath := migrationTestDatabase(t, schemaVersion)
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(fmt.Sprintf("PRAGMA wal_autocheckpoint=0; PRAGMA user_version=%d", newerVersion)); err != nil {
		t.Fatal(err)
	}
	walBefore, err := os.ReadFile(dbPath + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(dbPath)
	if s != nil {
		s.Close()
		t.Fatal("newer WAL schema returned a writable store")
	}
	var versionErr *SchemaVersionError
	if !errors.As(err, &versionErr) || versionErr.StoredVersion != newerVersion {
		t.Fatalf("New error = %v, want newer version from WAL", err)
	}
	walAfter, err := os.ReadFile(dbPath + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(walBefore, walAfter) {
		t.Fatal("rejecting the newer WAL schema changed its committed WAL")
	}
}

func TestMigrationSnapshotsV56BeforeChangesIncludingWAL(t *testing.T) {
	dbPath := migrationTestDatabase(t, 56)
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	// Keep committed data in the WAL to prove VACUUM INTO captures it and that
	// the space estimate accounts for more than just the main file's size.
	if _, err := raw.Exec(`PRAGMA wal_autocheckpoint=0;
		CREATE TABLE snapshot_marker (payload BLOB);
		INSERT INTO snapshot_marker VALUES (zeroblob(1048576));`); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var pageCount, pageSize uint64
	if err := raw.QueryRow("PRAGMA page_count").Scan(&pageCount); err != nil {
		t.Fatal(err)
	}
	if err := raw.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	if pageCount*pageSize <= uint64(info.Size()) {
		t.Fatal("fixture must contain committed WAL pages beyond the main file size")
	}
	// Exactly the required bytes should permit a snapshot.
	s, err := newStore(dbPath, func(dir string) (uint64, error) {
		if dir != filepath.Dir(dbPath) {
			t.Fatalf("free space checked in %s", dir)
		}
		return pageCount * pageSize, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshots := migrationSnapshots(t, dbPath)
	if len(snapshots) != 1 {
		t.Fatalf("snapshots = %v, want one", snapshots)
	}
	snapshotInfo, err := os.Stat(snapshots[0])
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && snapshotInfo.Mode().Perm() != 0600 {
		t.Fatalf("snapshot permissions = %04o, want 0600", snapshotInfo.Mode().Perm())
	}
	if err := verifySnapshot(snapshots[0]); err != nil {
		t.Fatal(err)
	}
	snapshot, err := openReadOnlyDatabase(snapshots[0])
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	var version, mailTables, payloadBytes int
	if err := snapshot.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 56 {
		t.Fatalf("snapshot version = %d, error %v; want 56", version, err)
	}
	if err := snapshot.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='mail_messages'").Scan(&mailTables); err != nil || mailTables != 0 {
		t.Fatalf("snapshot already has v57 mail table: count %d, error %v", mailTables, err)
	}
	if err := snapshot.QueryRow("SELECT length(payload) FROM snapshot_marker").Scan(&payloadBytes); err != nil || payloadBytes != 1048576 {
		t.Fatalf("snapshot WAL payload bytes = %d, error %v", payloadBytes, err)
	}
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("migrated version = %d, error %v", version, err)
	}
}

func TestMigrationSnapshotLowSpaceSkipsAndLogsReason(t *testing.T) {
	dbPath := migrationTestDatabase(t, 56)
	var output bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previousWriter) })
	s, err := newStore(dbPath, func(string) (uint64, error) { return 0, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if snapshots := migrationSnapshots(t, dbPath); len(snapshots) != 0 {
		t.Fatalf("low disk space created snapshots: %v", snapshots)
	}
	if !strings.Contains(output.String(), "snapshot skipped: insufficient free space (available 0 bytes, need ") {
		t.Fatalf("skip reason not logged: %q", output.String())
	}
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("low space prevented migration: version %d, error %v", version, err)
	}
}

func TestMigrationSnapshotsRetainNewestTwo(t *testing.T) {
	dbPath := migrationTestDatabase(t, 56)
	var allSnapshots []string
	for i := 0; i < 3; i++ {
		s, err := New(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		snapshots := migrationSnapshots(t, dbPath)
		allSnapshots = append(allSnapshots, snapshots[len(snapshots)-1])
		if i < 2 {
			if _, err := s.db.Exec("PRAGMA user_version=56"); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if got := migrationSnapshots(t, dbPath); !reflect.DeepEqual(got, allSnapshots[1:]) {
		t.Fatalf("retained snapshots = %v, want %v", got, allSnapshots[1:])
	}
	for _, snapshot := range allSnapshots[1:] {
		if err := verifySnapshot(snapshot); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNewAndCurrentDatabasesDoNotCreateMigrationSnapshots(t *testing.T) {
	dbPath := migrationTestDatabase(t, schemaVersion)
	s, err := newStore(dbPath, func(string) (uint64, error) {
		t.Fatal("current database should not check snapshot free space")
		return 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if snapshots := migrationSnapshots(t, dbPath); len(snapshots) != 0 {
		t.Fatalf("new or current database created snapshots: %v", snapshots)
	}
}

func TestMigrationSnapshotFailureDoesNotAdvanceSchema(t *testing.T) {
	dbPath := migrationTestDatabase(t, 56)
	s, err := newStore(dbPath, func(string) (uint64, error) {
		return 0, errors.New("volume unavailable")
	})
	if s != nil {
		s.Close()
		t.Fatal("snapshot failure returned a writable store")
	}
	if err == nil || !strings.Contains(err.Error(), "volume unavailable") {
		t.Fatalf("New error = %v, want snapshot failure", err)
	}
	raw, err := openReadOnlyDatabase(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var version int
	if err := raw.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 56 {
		t.Fatalf("failed snapshot advanced version: %d, error %v", version, err)
	}
}

func TestVerifySnapshotRejectsCorruptDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.db")
	if err := os.WriteFile(path, []byte("not a SQLite database"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifySnapshot(path); err == nil {
		t.Fatal("corrupt snapshot passed integrity check")
	}
}

func TestMigrationPreservesLegacy58RouteDataAndSnapshot(t *testing.T) {
	dbPath := migrationTestDatabase(t, 58)
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// The legacy assistant branch already added the route objects at v58.
	// Seed those existing objects before opening the newer migration target.
	legacy := &Store{db: raw}
	wantRoute := AgentRunRouteRow{
		RunID: "sibling-run", SessionID: "session", Backend: "codex",
		CandidateBackends: `["claude-code","codex","native"]`, FallbackFrom: `["claude-code"]`,
		BackendReason: "claude-code: unavailable", Model: "gpt-5.4", Status: "blocked", ActionsStarted: 3, UpdatedAt: 1,
	}
	wantCron := CronRunRow{
		ID: "run", JobID: "job", StartedAt: 10, Status: "running", ItemSortStart: -1,
		CandidateBackends: wantRoute.CandidateBackends, Backend: wantRoute.Backend,
		FallbackFrom: wantRoute.FallbackFrom, BackendReason: wantRoute.BackendReason, Model: wantRoute.Model, ActionsStarted: wantRoute.ActionsStarted,
	}
	if err := legacy.RecordAgentRunRoute(ctx, wantRoute); err != nil {
		t.Fatal(err)
	}
	if err := legacy.SaveCronJob(ctx, CronJobRow{ID: "job", Name: "Legacy route", ScheduleKind: "manual", TargetKind: "agent"}); err != nil {
		t.Fatal(err)
	}
	if err := legacy.InsertCronRun(ctx, wantCron); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("upgrade legacy route schema: %v", err)
	}
	defer s.Close()
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("upgraded schema version %d, error %v; want %d", version, err, schemaVersion)
	}
	assertRoutes := func(store *Store) {
		t.Helper()
		gotRoute, err := store.AgentRunRoute(ctx, wantRoute.RunID)
		if err != nil || !reflect.DeepEqual(gotRoute, wantRoute) {
			t.Fatalf("legacy agent route changed: got %+v, error %v; want %+v", gotRoute, err, wantRoute)
		}
		gotRuns, err := store.ListCronRuns(ctx, wantCron.JobID, 10)
		if err != nil || len(gotRuns) != 1 || !reflect.DeepEqual(gotRuns[0], wantCron) {
			t.Fatalf("legacy cron route changed: got %+v, error %v; want %+v", gotRuns, err, wantCron)
		}
	}
	assertRoutes(s)
	snapshots := migrationSnapshots(t, dbPath)
	if len(snapshots) != 1 {
		t.Fatalf("legacy upgrade snapshots = %v, want one", snapshots)
	}
	if err := verifySnapshot(snapshots[0]); err != nil {
		t.Fatal(err)
	}
	snapshot, err := openReadOnlyDatabase(snapshots[0])
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if err := snapshot.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 58 {
		t.Fatalf("legacy snapshot version %d, error %v; want 58", version, err)
	}
	assertRoutes(&Store{db: snapshot})
	if err := s.UpdateCronRun(ctx, wantCron.ID, map[string]any{"status": "completed"}); err != nil {
		t.Fatal(err)
	}
	wantCron.Status = "completed"
	assertRoutes(s)
}

func TestNewStampsCurrentImplementedGeneration(t *testing.T) {
	s := newTestStore(t)
	var version, routeTables int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("new database version %d, error %v; want %d", version, err, schemaVersion)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='agent_run_routes'").Scan(&routeTables); err != nil || routeTables != 1 {
		t.Fatalf("current route table count %d, error %v; want one", routeTables, err)
	}
}

func TestSnapshotPruneFailureLogsAndDoesNotRepeatMigration(t *testing.T) {
	dbPath := migrationTestDatabase(t, 56)
	contents, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	oldest := dbPath + ".snapshot-20000101T000000.000000000Z-v56-old.db"
	newer := dbPath + ".snapshot-20000102T000000.000000000Z-v56-newer.db"
	for _, path := range []string{oldest, newer} {
		if err := os.WriteFile(path, contents, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previousWriter) })
	removeCalls := 0
	s, err := newStoreWithSnapshotOptions(dbPath, snapshotOptions{
		availableBytes: freeDiskBytes,
		removeFile: func(path string) error {
			if path == oldest {
				removeCalls++
				return errors.New("snapshot is in use")
			}
			return os.Remove(path)
		},
	})
	if err != nil {
		t.Fatalf("prune failure blocked startup: %v", err)
	}
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("prune failure blocked migration: version %d, error %v", version, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if removeCalls != 1 || !strings.Contains(output.String(), "snapshot is in use") {
		t.Fatalf("prune failure was not attempted and logged: calls %d, log %q", removeCalls, output.String())
	}
	before := migrationSnapshots(t, dbPath)
	if len(before) != 3 {
		t.Fatalf("expected failed old snapshot plus newest two, got %v", before)
	}
	s, err = New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if after := migrationSnapshots(t, dbPath); !reflect.DeepEqual(after, before) {
		t.Fatalf("second startup repeated snapshot: before %v, after %v", before, after)
	}
}

func writePendingSnapshot(t *testing.T, dbPath, suffix string, age time.Duration) string {
	t.Helper()
	path := dbPath + ".snapshot-20000101T000000.000000000Z-v56-" + suffix + ".pending"
	if err := os.WriteFile(path, []byte("interrupted snapshot"), 0600); err != nil {
		t.Fatal(err)
	}
	modified := time.Now().Add(-age)
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestStalePendingSnapshotCleanupHonorsAgeAndActiveLock(t *testing.T) {
	dbPath := migrationTestDatabase(t, schemaVersion)
	stale := writePendingSnapshot(t, dbPath, "stale", 2*stalePendingSnapshotAge)
	recent := writePendingSnapshot(t, dbPath, "recent", time.Hour)
	otherDB := writePendingSnapshot(t, dbPath+"-other", "stale", 2*stalePendingSnapshotAge)
	lock, err := acquireSnapshotLock(dbPath)
	if err != nil || lock == nil {
		t.Fatalf("acquire active snapshot lock: %v", err)
	}
	cleanupStalePendingSnapshots(dbPath, os.Remove)
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("cleanup removed an active snapshot despite its lock: %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	// Ordinary startup also cleans remnants when no migration is necessary.
	s, err := New(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("unlocked stale pending snapshot remains: %v", err)
	}
	for _, path := range []string{recent, otherDB} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("cleanup removed recent or unrelated snapshot %s: %v", path, err)
		}
	}
}

func TestMigrationRemovesStalePendingBeforeSpaceCheck(t *testing.T) {
	dbPath := migrationTestDatabase(t, 56)
	stale := writePendingSnapshot(t, dbPath, "stale", 2*stalePendingSnapshotAge)
	s, err := newStore(dbPath, func(string) (uint64, error) {
		if _, err := os.Stat(stale); !os.IsNotExist(err) {
			t.Fatalf("space measured before stale pending cleanup: %v", err)
		}
		return 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
}

func TestMigrationSnapshotLockPreventsConcurrentStartup(t *testing.T) {
	dbPath := migrationTestDatabase(t, 56)
	lock, err := acquireSnapshotLock(dbPath)
	if err != nil || lock == nil {
		t.Fatalf("acquire active snapshot lock: %v", err)
	}
	s, err := New(dbPath)
	if s != nil {
		s.Close()
		t.Fatal("second startup migrated while a snapshot held the lock")
	}
	if err == nil || !strings.Contains(err.Error(), "another process is creating a migration snapshot") {
		t.Fatalf("concurrent snapshot error = %v", err)
	}
	if snapshots := migrationSnapshots(t, dbPath); len(snapshots) != 0 {
		t.Fatalf("second startup created a concurrent snapshot: %v", snapshots)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = newStore(dbPath, func(string) (uint64, error) {
		otherLock, err := acquireSnapshotLock(dbPath)
		if err != nil {
			t.Fatal(err)
		}
		if otherLock != nil {
			otherLock.Close()
			t.Fatal("snapshot did not hold the shared cleanup lock")
		}
		return 0, nil
	})
	if err != nil {
		t.Fatalf("startup did not recover after snapshot lock release: %v", err)
	}
	defer s.Close()
}

func TestNewPreservesCompatibleSiblingGeneration60(t *testing.T) {
	dbPath := migrationTestDatabase(t, 60)
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// Existing v60 route data must remain intact when upgrading to the current schema.
	if _, err := raw.Exec(`INSERT INTO agent_run_routes (run_id, backend, updated_at) VALUES ('sibling-run', 'codex', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := New(dbPath)
	if err != nil {
		t.Fatalf("open compatible sibling schema: %v", err)
	}
	defer s.Close()
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("migrated sibling schema = %d, error %v; want %d", version, err, schemaVersion)
	}
	var backend string
	if err := s.db.QueryRow("SELECT backend FROM agent_run_routes WHERE run_id='sibling-run'").Scan(&backend); err != nil || backend != "codex" {
		t.Fatalf("sibling route changed: backend %q, error %v", backend, err)
	}
	if err := s.SaveCronJob(ctx, CronJobRow{ID: "job", Name: "compatible"}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertCronRun(ctx, CronRunRow{ID: "run", JobID: "job", Status: "running"}); err != nil {
		t.Fatalf("current writer cannot insert into sibling schema: %v", err)
	}
	if _, err := s.db.Exec("UPDATE cron_runs SET backend='codex', actions_started=3 WHERE id='run'"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateCronRun(ctx, "run", map[string]any{"status": "completed"}); err != nil {
		t.Fatal(err)
	}
	var actions int
	if err := s.db.QueryRow("SELECT backend, actions_started FROM cron_runs WHERE id='run'").Scan(&backend, &actions); err != nil || backend != "codex" || actions != 3 {
		t.Fatalf("current writer changed sibling fields: backend %q, actions %d, error %v", backend, actions, err)
	}
	snapshots := migrationSnapshots(t, dbPath)
	if len(snapshots) != 1 {
		t.Fatalf("v60 upgrade snapshots = %v; want exactly one", snapshots)
	}
	snapshot, err := openReadOnlyDatabase(snapshots[0])
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if err := snapshot.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 60 {
		t.Fatalf("pre-upgrade snapshot schema = %d, error %v; want 60", version, err)
	}
	if err := snapshot.QueryRow("SELECT backend FROM agent_run_routes WHERE run_id='sibling-run'").Scan(&backend); err != nil || backend != "codex" {
		t.Fatalf("snapshot lost sibling route: backend %q, error %v", backend, err)
	}
}

func TestMigration57UpgradesAndReopensAtCurrentVersion(t *testing.T) {
	dbPath := migrationTestDatabase(t, 57)
	for attempt := 0; attempt < 2; attempt++ {
		s, err := New(dbPath)
		if err != nil {
			t.Fatalf("open %d after v57 upgrade: %v", attempt+1, err)
		}
		var version int
		if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != schemaVersion {
			t.Fatalf("open %d schema = %d, error %v; want %d", attempt+1, version, err, schemaVersion)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	snapshots := migrationSnapshots(t, dbPath)
	if len(snapshots) != 1 {
		t.Fatalf("v57 upgrade snapshots = %v; want exactly one", snapshots)
	}
	raw, err := openReadOnlyDatabase(snapshots[0])
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var version int
	if err := raw.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 57 {
		t.Fatalf("pre-upgrade snapshot schema = %d, error %v; want 57", version, err)
	}
}
