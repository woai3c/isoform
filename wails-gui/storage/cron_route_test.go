package storage

import (
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestCronRunRoutePersistsAcrossHistoryQueries(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveCronJob(ctx, CronJobRow{ID: "job", Name: "Playbook", ScheduleKind: "manual", TargetKind: "agent"}); err != nil {
		t.Fatal(err)
	}
	want := CronRunRow{
		ID: "run", JobID: "job", StartedAt: 10, Status: "ok", ItemSortStart: -1,
		CandidateBackends: `["claude-code","codex","native"]`, Backend: "native",
		FallbackFrom: `["claude-code","codex"]`, BackendReason: "Claude Code: signed out; Codex: unavailable", Model: "claude-opus", ActionsStarted: 2,
	}
	if err := s.InsertCronRun(ctx, want); err != nil {
		t.Fatal(err)
	}
	for name, load := range map[string]func() ([]CronRunRow, error){
		"job history": func() ([]CronRunRow, error) { return s.ListCronRuns(ctx, "job", 10) },
		"timeline":    func() ([]CronRunRow, error) { return s.ListCronRunsByDateRange(ctx, 0, 20, 10) },
	} {
		t.Run(name, func(t *testing.T) {
			got, err := load()
			if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0], want) {
				t.Fatalf("history = %#v, %v; want %#v", got, err, want)
			}
		})
	}
	if err := s.UpdateCronRun(ctx, "run", map[string]any{"backend": "codex", "model": "gpt-5.4", "status": "blocked"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListCronRuns(ctx, "job", 10)
	if err != nil || len(got) != 1 || got[0].Backend != "codex" || got[0].Model != "gpt-5.4" || got[0].Status != "blocked" {
		t.Fatalf("updated history = %#v, %v", got, err)
	}
}

// These fixtures remove the route schema before opening. Merely changing the
// stamp on a current database would hide a migration still guarded by v58.
func TestCronRunRouteMigrationFrom57Through59(t *testing.T) {
	for _, version := range []int{57, 58, 59} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			dbPath := filepath.Join(t.TempDir(), ".isoform-agent", "sessions.db")
			s, err := New(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.SaveCronJob(ctx, CronJobRow{ID: "job", Name: "Existing", ScheduleKind: "manual", TargetKind: "agent"}); err != nil {
				t.Fatal(err)
			}
			oldRun := CronRunRow{ID: "old", JobID: "job", Status: "ok", ItemSortStart: -1}
			if err := s.InsertCronRun(ctx, oldRun); err != nil {
				t.Fatal(err)
			}
			if err := s.SetUIState(ctx, "existing-preference", "preserved"); err != nil {
				t.Fatal(err)
			}
			for _, column := range []string{"candidate_backends", "backend", "fallback_from", "backend_reason", "model", "actions_started"} {
				if _, err := s.db.Exec("ALTER TABLE cron_runs DROP COLUMN " + column); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.Exec("DROP TABLE agent_run_routes"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}

			s, err = New(dbPath)
			if err != nil {
				t.Fatalf("open database written at v%d: %v", version, err)
			}
			t.Cleanup(func() { s.Close() })
			var stamp, indexes int
			if err := s.db.QueryRow("PRAGMA user_version").Scan(&stamp); err != nil || stamp != schemaVersion {
				t.Errorf("migrated stamp = %d, %v; want %d", stamp, err, schemaVersion)
			}
			runs, err := s.ListCronRuns(ctx, "job", 10)
			if err != nil || len(runs) != 1 || !reflect.DeepEqual(runs[0], oldRun) {
				t.Fatalf("migrated history = %#v, %v; want %#v", runs, err, oldRun)
			}
			if got, err := s.GetUIState(ctx, "existing-preference"); err != nil || got != "preserved" {
				t.Fatalf("existing preference = %q, %v", got, err)
			}
			if err := s.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_agent_run_routes_updated'").Scan(&indexes); err != nil || indexes != 1 {
				t.Fatalf("route index count = %d, %v", indexes, err)
			}
			snapshots := migrationSnapshots(t, dbPath)
			if len(snapshots) != 1 {
				t.Fatalf("upgrade snapshots = %v; want one", snapshots)
			}
			snapshot, err := openReadOnlyDatabase(snapshots[0])
			if err != nil {
				t.Fatal(err)
			}
			defer snapshot.Close()
			if err := snapshot.QueryRow("PRAGMA user_version").Scan(&stamp); err != nil || stamp != version {
				t.Errorf("snapshot stamp = %d, %v; want %d", stamp, err, version)
			}
			var routeObjects int
			if err := snapshot.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='agent_run_routes'").Scan(&routeObjects); err != nil || routeObjects != 0 {
				t.Errorf("snapshot contains post-migration route table: count=%d, %v", routeObjects, err)
			}
			if has, err := (&Store{db: snapshot}).hasColumn("cron_runs", "backend"); err != nil || has {
				t.Errorf("snapshot contains post-migration route column: exists=%v, %v", has, err)
			}
			var status string
			if err := snapshot.QueryRow("SELECT status FROM cron_runs WHERE id='old'").Scan(&status); err != nil || status != "ok" {
				t.Errorf("snapshot lost existing history: %q, %v", status, err)
			}

			want := AgentRunRouteRow{RunID: "new", Backend: "codex", Model: "model", ActionsStarted: 2, Status: "blocked", UpdatedAt: 123}
			if err := s.RecordAgentRunRoute(ctx, want); err != nil {
				t.Fatal(err)
			}
			if err := s.UpdateCronRun(ctx, "old", map[string]any{"backend": "codex", "actions_started": 2}); err != nil {
				t.Fatal(err)
			}
			// Replay v60 over populated route objects, as after an interrupted
			// additive migration. Existing attribution must survive every replay.
			for i := 0; i < 2; i++ {
				if _, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
					t.Fatal(err)
				}
				if err := s.migrate(); err != nil {
					t.Fatalf("replay migration: %v", err)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = New(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := s.AgentRunRoute(ctx, want.RunID); err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("route after replay and reopen = %#v, %v; want %#v", got, err, want)
			}
			runs, err = s.ListCronRuns(ctx, "job", 10)
			if err != nil || len(runs) != 1 || runs[0].Backend != "codex" || runs[0].ActionsStarted != 2 {
				t.Errorf("attributed history after replay = %#v, %v", runs, err)
			}
			if after := migrationSnapshots(t, dbPath); !reflect.DeepEqual(after, snapshots) {
				t.Errorf("reopen repeated snapshot: before=%v after=%v", snapshots, after)
			}
		})
	}
}

func TestInterruptedRunsPreserveSchedules(t *testing.T) {
	due := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC).UnixMilli()
	tomorrow := due + int64(24*time.Hour/time.Millisecond)
	future := due + int64(72*time.Hour/time.Millisecond)
	for _, test := range []struct {
		name, surface, backend, kind, config, trigger string
		actions                                       int
		enabled                                       bool
		next, wantNext                                int64
		wantEnabled                                   bool
	}{
		{"Daily News after action", "system", "claude-code", "cron", `{"expr":"0 8 * * *","tz":"UTC"}`, "scheduled", 1, true, due, tomorrow, true},
		{"user daily after action", "", "codex", "every", `{"every_ms":86400000}`, "scheduled", 1, true, due, tomorrow, true},
		{"native after action", "", "native", "every", `{"every_ms":86400000}`, "scheduled", 1, true, due, tomorrow, true},
		{"Daily News before action", "system", "claude-code", "cron", `{"expr":"0 8 * * *","tz":"UTC"}`, "scheduled", 0, true, due, due, true},
		{"Codex before action", "", "codex", "every", `{"every_ms":86400000}`, "scheduled", 0, true, due, due, true},
		{"native before action", "", "native", "every", `{"every_ms":86400000}`, "scheduled", 0, true, due, due, true},
		{"legacy before action", "", "", "every", `{"every_ms":86400000}`, "scheduled", 0, true, due, due, true},
		{"manual action", "", "claude-code", "manual", `{}`, "manual", 1, true, 0, 0, true},
		{"one-time action", "", "claude-code", "at", `{"at_ms":1}`, "scheduled", 1, true, due, 0, false},
		{"user already paused", "", "claude-code", "cron", `{"expr":"0 8 * * *","tz":"UTC"}`, "scheduled", 1, false, due, tomorrow, false},
		{"future occurrence already scheduled", "system", "claude-code", "cron", `{"expr":"0 8 * * *","tz":"UTC"}`, "retry", 1, true, future, future, true},
		{"manual run does not replay overdue schedule", "system", "claude-code", "cron", `{"expr":"0 8 * * *","tz":"UTC"}`, "manual", 1, true, due, tomorrow, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newTestStore(t)
			job := CronJobRow{ID: "job", Name: "Schedule", Enabled: test.enabled, SurfaceKind: test.surface, ScheduleKind: test.kind, ScheduleConfig: test.config, TargetKind: "agent", NextRunAt: test.next, RetryAttempt: 1, RetryRunAt: due}
			if err := s.SaveCronJob(ctx, job); err != nil {
				t.Fatal(err)
			}
			if err := s.InsertCronRun(ctx, CronRunRow{ID: "run", JobID: "job", StartedAt: due + 60000, TriggerKind: test.trigger, Status: "running", Backend: test.backend, ActionsStarted: test.actions}); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if err := s.MarkStaleRunsAsError(ctx); err != nil {
					t.Fatal(err)
				}
			}
			runs, err := s.ListCronRuns(ctx, "job", 10)
			if err != nil || len(runs) != 1 {
				t.Fatalf("runs = %#v, %v", runs, err)
			}
			saved, err := s.LoadCronJob(ctx, "job")
			if err != nil {
				t.Fatal(err)
			}
			if saved.ConsecutiveErrors != 1 {
				t.Fatalf("failures = %d, want 1", saved.ConsecutiveErrors)
			}
			if saved.Enabled != test.wantEnabled || saved.NextRunAt != test.wantNext {
				t.Errorf("schedule after recovery: enabled=%v next=%d; want enabled=%v next=%d", saved.Enabled, saved.NextRunAt, test.wantEnabled, test.wantNext)
			}
			if test.actions > 0 {
				if runs[0].Status != "blocked" || saved.LastRunStatus != "blocked" || saved.RetryRunAt != 0 || saved.RetryAttempt != 0 {
					t.Fatalf("interrupted action may retry: run=%#v job=%#v", runs[0], saved)
				}
			} else if runs[0].Status != "error" || saved.LastRunStatus != "error" || saved.RetryRunAt != due || saved.RetryAttempt != 1 {
				t.Fatalf("zero-action attempt was blocked: run=%#v job=%#v", runs[0], saved)
			}
		})
	}
}

func TestFinalizeCronRunRollsBackIfRetryStateCannotPersist(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveCronJob(ctx, CronJobRow{ID: "job", Name: "Schedule", Enabled: true, ScheduleKind: "manual", TargetKind: "agent", RetryRunAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertCronRun(ctx, CronRunRow{ID: "run", JobID: "job", Status: "running", Backend: "claude-code"}); err != nil {
		t.Fatal(err)
	}
	// Force an actual SQLite update failure after the run update, rather than
	// replacing the store with a stub. Neither half may be committed.
	err := s.FinalizeCronRun(ctx, "run", "job", map[string]any{"status": "blocked"}, map[string]any{"column_that_does_not_exist": 0})
	if err == nil {
		t.Fatal("expected retry-state update failure")
	}
	runs, err := s.ListCronRuns(ctx, "job", 10)
	if err != nil || len(runs) != 1 || runs[0].Status != "running" {
		t.Fatalf("partial completion escaped transaction: %#v, %v", runs, err)
	}
	if err := s.FinalizeCronRun(ctx, "run", "job", map[string]any{"status": "blocked"}, map[string]any{"retry_run_at": 0}); err != nil {
		t.Fatal(err)
	}
	job, err := s.LoadCronJob(ctx, "job")
	if err != nil || job.RetryRunAt != 0 {
		t.Fatalf("retry state did not finalize: %#v, %v", job, err)
	}
}
