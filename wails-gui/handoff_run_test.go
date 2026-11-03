package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"wails-gui/internal/handoff"
	"wails-gui/storage"
	"yansu-agent/agent"
)

func handoffResultForFile(t *testing.T, criterion, path string) string {
	t.Helper()
	result := handoff.Result{Status: handoff.StatusCompleted,
		Criteria: []handoff.ResultCriterion{{Text: criterion, Met: true, Evidence: []int{0}}},
		Evidence: []handoff.ResultEvidence{{Path: path}},
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return "The report is ready.\n\n" + handoffResultFence + string(raw) + "\n```"
}

func TestHandoffIdleTimeoutCancelsStalledRunButKeepsActiveRun(t *testing.T) {
	rig := newEngagementTestRig(t)
	store := newLifecycleHandoffStore(t, rig)
	app := &App{handoffStore: store}
	for _, id := range []string{"stalled", "active"} {
		entry, err := store.Save(handoff.Entry{ID: id, Status: handoff.StatusInProgress, AttemptID: id})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := app.registerHandoffRun(&entry, t.TempDir(), cancel); err != nil {
			t.Fatal(err)
		}
		now := time.Now().Add(2*time.Hour + time.Minute)
		if id == "active" {
			app.touchHandoffRun(&entry, now.Add(-time.Minute))
		}
		app.blockIdleHandoffs(now)
		got, _ := store.Get(id)
		if id == "stalled" && (got.Status != handoff.StatusBlocked || got.BlockedReason != handoff.BlockedTimeout || ctx.Err() == nil) {
			t.Fatalf("stalled = %+v, %v", got, ctx.Err())
		}
		if id == "active" && (got.Status != handoff.StatusInProgress || ctx.Err() != nil) {
			t.Fatalf("active = %+v, %v", got, ctx.Err())
		}
	}
}

func TestHandoffLateVerificationCannotCompleteRestartedAttempt(t *testing.T) {
	app := mainChatTestApp(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.handoffStore = store
	dir := t.TempDir()
	writeEvidenceFile(t, dir, "report.md")
	old, err := store.Save(handoff.Entry{ID: "late", Status: handoff.StatusInProgress, AttemptID: "first", Workdir: dir, Criteria: []string{"The report exists"}})
	if err != nil {
		t.Fatal(err)
	}
	started, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	previous := handoffVerifierModel
	handoffVerifierModel = func(string) (string, error) {
		close(started)
		<-resume
		return `{"status":"completed","criteria":[{"text":"The report exists","met":true}]}`, nil
	}
	t.Cleanup(func() { handoffVerifierModel = previous })
	go func() {
		defer close(done)
		app.markHandoffAttemptDone(old, true, handoffResultForFile(t, "The report exists", "report.md"))
	}()
	select {
	case <-started:
	case <-done:
		got, _ := store.Get(old.ID)
		t.Fatalf("verification ended before model: %+v", got.Verification)
	case <-time.After(5 * time.Second):
		t.Fatal("verification did not start")
	}
	app.markHandoffBlocked(old.ID, handoff.BlockedTimeout)
	if _, err := store.MutateIfStatus(old.ID, []handoff.Status{handoff.StatusBlocked}, func(e *handoff.Entry) { e.Status = handoff.StatusInProgress; e.AttemptID = "second" }); err != nil {
		t.Fatal(err)
	}
	close(resume)
	<-done
	got, _ := store.Get(old.ID)
	if got.Status != handoff.StatusInProgress || got.AttemptID != "second" || got.Verification != nil {
		t.Fatalf("late result overwrote new attempt: %+v", got)
	}
}

func TestHandoffAbortCancelsVerification(t *testing.T) {
	app := mainChatTestApp(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.handoffStore = store
	dir := t.TempDir()
	writeEvidenceFile(t, dir, "report.md")
	entry, err := store.Save(handoff.Entry{ID: "verify", Status: handoff.StatusInProgress, AttemptID: "first", Workdir: dir, Criteria: []string{"The report exists"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app.handoffRuns = map[string]*handoffRunState{entry.ID: {attemptID: entry.AttemptID, cancel: cancel, lastActivity: time.Now()}}
	if err := app.AbortHandoff(entry.ID); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil {
		t.Fatal("verification context remains live")
	}
	app.markHandoffAttemptDone(entry, true, handoffResultForFile(t, "The report exists", "report.md"))
	got, _ := store.Get(entry.ID)
	if got.Status != handoff.StatusAborted {
		t.Fatalf("late completion = %+v", got)
	}
}

// The actual native runner talks to a local HTTP model fixture. All storage,
// cron prompt composition, streaming and ephemeral verification paths are real.
func TestHandoffCronNativeRunnerVerifiesLocalEvidence(t *testing.T) {
	for _, exists := range []bool{true, false} {
		t.Run(fmt.Sprint(exists), func(t *testing.T) {
			home, err := os.MkdirTemp("/tmp", "ih-native-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(home) })
			t.Setenv("HOME", home)
			app := mainChatTestApp(t)
			store, err := handoff.NewStore(filepath.Join(home, "handoffs"))
			if err != nil {
				t.Fatal(err)
			}
			app.handoffStore = store
			dir := filepath.Join(home, "work")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if exists {
				writeEvidenceFile(t, dir, "report.md")
			}
			criterion := "The report exists at report.md"
			transcript := handoffResultForFile(t, criterion, "report.md")
			var mu sync.Mutex
			var models []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Model    string            `json:"model"`
					Messages json.RawMessage   `json:"messages"`
					Tools    []json.RawMessage `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				mu.Lock()
				models = append(models, body.Model)
				mu.Unlock()
				reply := transcript
				if body.Model == "verification-background" {
					if len(body.Tools) > 0 {
						t.Error("ephemeral verifier received tools")
					}
					if !strings.Contains(string(body.Messages), "report.md") {
						t.Error("verifier lacks evidence")
					}
					reply = `{"status":"completed","criteria":[{"text":"The report exists at report.md","met":true}]}`
				} else if !strings.Contains(string(body.Messages), "handoff-result") {
					t.Error("execution prompt lacks result contract")
				}
				writeHandoffModelSSE(w, reply)
			}))
			defer server.Close()
			runner := agent.NewNativeAgentRunner(agent.NativeRunnerConfig{BaseURL: server.URL, APIKey: "local-test", DisableEnvFallback: true, DefaultModel: "work", BackgroundModelAliases: map[string]string{app.lightTaskModel(): "verification-background"}})
			if err := runner.Init(context.Background()); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runner.Shutdown() })
			app.runner = runner
			entry, err := store.Save(handoff.Entry{ID: "cron-work", Status: handoff.StatusInProgress, AttemptID: "attempt", CronJobID: "job-work", Workdir: dir, Criteria: []string{criterion}, Body: "Prepare the report."})
			if err != nil {
				t.Fatal(err)
			}
			config, _ := json.Marshal(cronTargetConfig{Prompt: entry.Body, HandoffID: entry.ID, ProjectDir: dir, Model: "work"})
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_, err = app.executeAgentJob(ctx, &storage.CronJobRow{ID: "job-work", TargetConfig: string(config)})
			if err != nil {
				t.Fatal(err)
			}
			want := handoff.StatusNeedsReview
			if exists {
				want = handoff.StatusCompleted
			}
			for {
				app.handoffActionMu.Lock()
				got, _ := store.Get(entry.ID)
				app.handoffActionMu.Unlock()
				if got.Status == want {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatalf("settlement = %+v; %v", got, ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			mu.Lock()
			defer mu.Unlock()
			wantCalls := 1
			if exists {
				wantCalls = 2
			}
			if len(models) != wantCalls {
				t.Fatalf("model calls = %v", models)
			}
			if exists && models[1] != "verification-background" {
				t.Fatalf("verification was not background: %v", models)
			}
		})
	}
}

func writeHandoffModelSSE(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"fixture\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
	fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
	delta, _ := json.Marshal(map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": text}})
	fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", delta)
	fmt.Fprint(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
}

func TestHandoffDuplicateRunCannotChangeEvidenceFolder(t *testing.T) {
	rig := newEngagementTestRig(t)
	store := newLifecycleHandoffStore(t, rig)
	app := &App{handoffStore: store}
	entry, err := store.Save(handoff.Entry{ID: "duplicate", Status: handoff.StatusInProgress, AttemptID: "attempt"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := app.registerHandoffRun(&entry, dir, cancel); err != nil {
		t.Fatal(err)
	}
	otherCtx, otherCancel := context.WithCancel(context.Background())
	defer otherCancel()
	if err := app.registerHandoffRun(&entry, t.TempDir(), otherCancel); err == nil {
		t.Fatal("duplicate run admitted")
	}
	got, _ := store.Get(entry.ID)
	if got.Workdir != dir || ctx.Err() != nil || otherCtx.Err() == nil {
		t.Fatalf("duplicate changed live run: %+v", got)
	}
}

func TestHandoffOldCronJobCannotJoinRestartedWork(t *testing.T) {
	app := mainChatTestApp(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.handoffStore = store
	entry, err := store.Save(handoff.Entry{ID: "restarted-cron", Status: handoff.StatusInProgress, AttemptID: "new-chat-attempt", Workdir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(cronTargetConfig{HandoffID: entry.ID, ProjectDir: entry.Workdir, Prompt: "Old prompt"})
	if _, err := app.executeAgentJob(context.Background(), &storage.CronJobRow{ID: "old-job", TargetConfig: string(cfg)}); err == nil {
		t.Fatal("old cron job admitted into a new attempt")
	}
	got, _ := store.Get(entry.ID)
	if got.Status != entry.Status || got.AttemptID != entry.AttemptID || got.Verification != nil {
		t.Fatalf("old cron changed current work: %+v", got)
	}
}

func TestHandoffOwnedCronSetupFailureSettlesImmediately(t *testing.T) {
	app := mainChatTestApp(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.handoffStore = store
	entry, err := store.Save(handoff.Entry{ID: "bad-setup", Status: handoff.StatusInProgress, AttemptID: "first", CronJobID: "job"})
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(cronTargetConfig{HandoffID: entry.ID})
	if _, err := app.executeAgentJob(context.Background(), &storage.CronJobRow{ID: "job", TargetConfig: string(cfg)}); err == nil {
		t.Fatal("empty prompt accepted")
	}
	deadline := time.After(3 * time.Second)
	for {
		app.handoffActionMu.Lock()
		got, _ := store.Get(entry.ID)
		app.handoffActionMu.Unlock()
		if got.Status == handoff.StatusAborted {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("setup failure left work running: %+v", got)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestHandoffVerificationKeepsPendingBackgroundContinuation(t *testing.T) {
	app, _, _ := newBgShellTestApp(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.handoffStore = store
	dir := t.TempDir()
	writeEvidenceFile(t, dir, "report.md")
	entry, err := store.Save(handoff.Entry{ID: "background", Status: handoff.StatusInProgress, AttemptID: "first", ChatSessionID: "chat-1", Workdir: dir, Criteria: []string{"The report exists"}})
	if err != nil {
		t.Fatal(err)
	}
	started, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	previous := handoffVerifierModel
	handoffVerifierModel = func(string) (string, error) {
		close(started)
		<-resume
		return `{"status":"completed","criteria":[{"text":"The report exists","met":true}]}`, nil
	}
	t.Cleanup(func() { handoffVerifierModel = previous })
	resumed := make(chan struct{}, 1)
	bgShellResumeRun = func(*App, bgShellSessionInfo, string) error { resumed <- struct{}{}; return nil }
	transcript := handoffResultForFile(t, entry.Criteria[0], "report.md")
	go func() { defer close(done); app.markHandoffAttemptDone(entry, true, transcript) }()
	select {
	case <-started:
	case <-done:
		got, _ := store.Get(entry.ID)
		t.Fatalf("check ended before model: %+v", got.Verification)
	case <-time.After(3 * time.Second):
		t.Fatal("check did not start")
	}
	app.bgShells.AddPending(bgShellFinish{info: bgShellSessionInfo{Tag: "chat-1", WorkDir: dir, ShellID: "bg-ready"}})
	app.afterRunBgShells("chat-1")
	select {
	case <-resumed:
		t.Fatal("resumed before verifier released work")
	default:
	}
	close(resume)
	<-done
	select {
	case <-resumed:
	case <-time.After(3 * time.Second):
		t.Fatal("verification dropped pending continuation")
	}
	if users, _ := bgShellRows(t, app); len(users) != 1 {
		t.Fatalf("continuation lines = %v", users)
	}
}

func TestHandoffDuplicateCronSetupCannotAbortLiveAttempt(t *testing.T) {
	app := mainChatTestApp(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.handoffStore = store
	entry, err := store.Save(handoff.Entry{ID: "owned", Status: handoff.StatusInProgress, AttemptID: "first", CronJobID: "job", Workdir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := app.registerHandoffRun(&entry, entry.Workdir, cancel); err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(cronTargetConfig{HandoffID: entry.ID})
	if _, err := app.executeAgentJob(context.Background(), &storage.CronJobRow{ID: "job", TargetConfig: string(cfg)}); !errors.Is(err, errHandoffRunBusy) {
		t.Fatalf("duplicate error = %v", err)
	}
	got, _ := store.Get(entry.ID)
	if got.Status != handoff.StatusInProgress || ctx.Err() != nil {
		t.Fatalf("duplicate aborted owner: %+v, %v", got, ctx.Err())
	}
}

func TestHandoffStopDropsPendingAndFutureBackgroundResumes(t *testing.T) {
	app, _, calls := newBgShellTestApp(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.handoffStore = store
	entry, err := store.Save(handoff.Entry{ID: "stop-background", Status: handoff.StatusInProgress, AttemptID: "first", ChatSessionID: "chat-1"})
	if err != nil {
		t.Fatal(err)
	}
	app.bgShells.Register("active", bgShellSessionInfo{Tag: "chat-1"})
	app.bgShells.AddPending(bgShellFinish{info: bgShellSessionInfo{Tag: "chat-1", ShellID: "queued"}})
	if err := app.AbortHandoff(entry.ID); err != nil {
		t.Fatal(err)
	}
	app.onBgShellDone("active", 0, "completed", "test")
	// Cover a finish restored by a competing resume after Stop removed it.
	app.bgShells.AddPending(bgShellFinish{info: bgShellSessionInfo{Tag: "chat-1", ShellID: "restored"}})
	app.resumeBgShells("chat-1")
	if len(*calls) != 0 || len(app.bgShells.TakePending("chat-1")) != 0 || len(app.bgShells.ActiveForSession("chat-1")) != 0 {
		t.Fatal("stopped work retained background continuation")
	}
}
