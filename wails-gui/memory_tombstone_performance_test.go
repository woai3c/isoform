package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
	"yansu-agent/agent"
)

type optionalMemorySlowRunner struct {
	memoryTranslationFakeRunner
	started chan context.Context
	calls   atomic.Int32
}

func (r *optionalMemorySlowRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, callback agent.StreamCallback) (string, error) {
	r.calls.Add(1)
	select {
	case r.started <- ctx:
	default:
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(1500 * time.Millisecond):
		return "", errors.New("provider unavailable")
	}
}
func optionalMemoryPerformanceFixture(t *testing.T, ctx context.Context) (*App, string, *optionalMemorySlowRunner) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	runner := &optionalMemorySlowRunner{memoryTranslationFakeRunner: memoryTranslationFakeRunner{ready: true}, started: make(chan context.Context, 2)}
	app := &App{ctx: ctx, runner: runner}
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	prepared := memoryForgetSemantics{Version: memorySemanticVersion, Model: effectiveLightTaskModel(app), Hashes: []string{memoryForgetHash("prior prepared semantic fact")}}
	if err := recordMemoryTombstone(dir, "removed", []string{optionalMemoryObservation("Display", "Prefers dark mode.")}, time.Now(), prepared); err != nil {
		t.Fatal(err)
	}
	return app, dir, runner
}
func TestMemoryOptionalSemanticsHonorsCallerCancellation(t *testing.T) {
	// Virtual time keeps this a cancellation regression under scheduler load.
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		app, dir, runner := optionalMemoryPerformanceFixture(t, ctx)
		done := make(chan error, 1)
		content := optionalMemoryObservation("Calendar", "Checks the calendar each morning.")
		go func() { done <- app.prepareMemoryObservedWrite(dir, "calendar", content) }()
		synctest.Wait()
		var received context.Context
		select {
		case received = <-runner.started:
		default:
			t.Fatal("optional runner never started")
		}
		cancel()
		synctest.Wait()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("cancelled optional matching blocked observation: %v", err)
			}
		default:
			t.Fatal("optional matching ignored caller cancellation")
		}
		if received.Err() == nil {
			t.Fatal("runner did not receive caller cancellation")
		}
		if _, ok := cachedMemorySemanticHash(dir, effectiveLightTaskModel(app), content); ok {
			t.Fatal("cancelled result persisted a semantic receipt")
		}
	})
}
func TestMemoryOptionalSemanticsOneSecondBudgetAndFailureCooldown(t *testing.T) {
	// Assert the production budget with a virtual clock, not CPU scheduling time.
	synctest.Test(t, func(t *testing.T) {
		app, dir, runner := optionalMemoryPerformanceFixture(t, context.Background())
		start := time.Now()
		if err := app.prepareMemoryObservedWrite(dir, "first", optionalMemoryObservation("Calendar", "Checks the calendar.")); err != nil {
			t.Fatalf("slow optional matching blocked observation: %v", err)
		}
		if elapsed := time.Since(start); elapsed != time.Second {
			t.Fatalf("optional matching budget = %v, want one second", elapsed)
		}
		// Let the cancelled runner finish so this tests cooldown, not single-flight.
		synctest.Wait()
		start = time.Now()
		if err := app.prepareMemoryObservedWrite(dir, "second", optionalMemoryObservation("Workspace", "Uses a standing desk.")); err != nil {
			t.Fatal(err)
		}
		if runner.calls.Load() != 1 {
			t.Fatalf("failed provider retried inside cooldown: calls=%d", runner.calls.Load())
		}
		if time.Since(start) != 0 {
			t.Fatal("cooldown added another wait")
		}
	})
}
func TestMemoryOptionalSemanticsMalformedResponseUsesCooldown(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var calls atomic.Int32
	app := &App{memoryCanonicalizer: func(string, string) (string, error) { calls.Add(1); return `{"private-user-fact":"bad schema"}`, nil }}
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"first", "second"} {
		if err := app.prepareMemoryObservedWrite(dir, id, optionalMemoryObservation(id, "An unrelated observation "+id+".")); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("invalid response retried within failure cooldown: %d", calls.Load())
	}
}
func TestMemoryOptionalSemanticsReceiptPruningHasBoundedBatches(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), ".semantic")
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		t.Fatal(err)
	}
	receipt := memorySemanticReceipt{Version: memorySemanticVersion, Model: "model", Hash: "hash", ExpiresAt: time.Now().Add(-time.Hour).UnixMilli()}
	data, _ := json.Marshal(receipt)
	for i := 0; i < 100; i++ {
		name := memoryForgetHash(string(rune(i))) + ".json"
		if err := os.WriteFile(filepath.Join(cacheDir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	pruneExpiredMemorySemanticReceipts(cacheDir, now)
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 68 || len(entries) >= 100 {
		t.Fatalf("prune did not process one bounded batch: remaining=%d", len(entries))
	}
	firstCount := len(entries)
	pruneExpiredMemorySemanticReceipts(cacheDir, now.Add(time.Second))
	entries, _ = os.ReadDir(cacheDir)
	if len(entries) != firstCount {
		t.Fatal("each cache write rescans the receipt directory")
	}
	pruneExpiredMemorySemanticReceipts(cacheDir, now.Add(2*time.Minute))
	entries, _ = os.ReadDir(cacheDir)
	if len(entries) >= firstCount {
		t.Fatal("bounded cursor did not advance on a later prune")
	}
}

func TestMemoryOptionalSemanticsWritersUseCallerContextOnce(t *testing.T) {
	for _, kind := range []string{"generator create", "activity create", "generator update", "activity update"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			runner := &memoryTranslationFakeRunner{ready: true, response: `{"memoryPoints":["Tracks weekly planning.","Checks the calendar each morning."]}`}
			app := newTranslationTestApp(t, runner)
			var calls atomic.Int32
			app.memoryCanonicalizer = func(string, string) (string, error) {
				calls.Add(1)
				return `{"version":"isoform-memory-semantics-v1","status":"no_facts","facts":[]}`, nil
			}
			dir, err := memoryDir()
			if err != nil {
				t.Fatal(err)
			}
			existing := optionalMemoryObservation("Existing planning", "Tracks weekly planning.")
			if err := os.WriteFile(filepath.Join(dir, "existing.md"), []byte(existing), 0600); err != nil {
				t.Fatal(err)
			}
			input := &mcMemoryGenerationResponse{Title: "Planning preferences", MemoryPoints: []string{"Checks the calendar each morning."}, Dimension: "user_insight"}
			app.activity = &activityMonitorState{store: newTestStore(t)}
			insertTestSession(t, app.activity.store, "context-test-session", "dismissed", 100_000)
			session, err := app.activity.store.GetSessionByID("context-test-session")
			if err != nil || session == nil {
				t.Fatalf("readable activity source: %v", err)
			}
			group := sessionGroup{Sessions: []ActivitySessionRow{*session}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wanted := int32(1)
			// Creation must inherit the caller's cancellation instead of falling back
			// to the still-live application context. Updates get just one final probe.
			if strings.HasSuffix(kind, "create") {
				cancel()
				wanted = 0
			}
			switch kind {
			case "generator create":
				_, err = (&MemoryGenerator{app: app}).saveMemoryFileContext(ctx, memorySourceSlack, "new-source", input)
			case "activity create":
				_, err = (&ActivityTriager{app: app}).saveActivityMemoryContext(ctx, *session, group, input)
			case "generator update":
				_, _, err = (&MemoryGenerator{app: app}).updateMemoryFileDetailed(ctx, "existing", memorySourceSlack, "source", input, "")
			case "activity update":
				_, err = (&ActivityTriager{app: app}).updateActivityMemory(ctx, "existing", *session, group, input)
			}
			if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != wanted {
				t.Fatalf("optional probes=%d want=%d", calls.Load(), wanted)
			}
		})
	}
}
