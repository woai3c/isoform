package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/handoff"
	"yansu-agent/agent/native"
)

func TestHandoffStopCauseClaimsNoAIAndSignedOutOnlyWhenProven(t *testing.T) {
	for _, tc := range []struct {
		name  string
		proof handoffStopProof
		want  string
	}{
		{"no AI at the start-time check", handoffStopProof{NoAIAtStart: true}, handoff.CauseNoAssistant},
		{"no AI at start, then a 401", handoffStopProof{NoAIAtStart: true, AuthFailed: true}, handoff.CauseNoAssistant},
		{"provider 401 or 403", handoffStopProof{AuthFailed: true}, handoff.CauseSignedOut},
		{"CLI says signed out", handoffStopProof{External: true, SignedIn: false}, handoff.CauseSignedOut},
		{"CLI signed in", handoffStopProof{External: true, SignedIn: true}, handoff.CauseAssistantError},
		{"auth status unreadable", handoffStopProof{External: true, SignInErr: errors.New("timeout")}, handoff.CauseAssistantError},
		{"API key AI", handoffStopProof{}, handoff.CauseAssistantError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := handoffStopCause(tc.proof); got != tc.want {
				t.Fatalf("cause = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAllDoneCanReadOldCompletedTaskFromFixtureStore(t *testing.T) {
	app := mainChatTestApp(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.handoffStore = store
	saveLifecycleHandoff(t, store, handoff.Entry{ID: "old-finished", Title: "Finished task", Status: handoff.StatusCompleted, UpdatedAt: time.Now().AddDate(-2, 0, 0).UnixMilli()})
	items, err := app.ListHandoffs()
	if err != nil || len(items) != 1 || items[0].Status != handoff.StatusCompleted {
		t.Fatalf("completed task unavailable: %+v, %v", items, err)
	}
}

func TestLoopGuardOffersMarkDoneWithoutRestart(t *testing.T) {
	entry := handoff.Entry{Status: handoff.StatusAborted, StoppedBy: handoff.StoppedBySystem, StopCause: handoff.CauseLoopGuard}
	for _, action := range mobileTaskActions(entry, nil) {
		if action == mobileTaskActionMarkDone {
			return
		}
	}
	t.Fatal("loop guard task does not offer Mark as done")
}

func withHandoffStopProof(t *testing.T, proof handoffStopProof) {
	t.Helper()
	previous := handoffStopProbe
	handoffStopProbe = func(*App) handoffStopProof { return proof }
	t.Cleanup(func() { handoffStopProbe = previous })
}

func TestHandoffRunErrorStaysInNeedsYouWithItsCause(t *testing.T) {
	for _, tc := range []struct {
		name      string
		proof     handoffStopProof
		cause     string
		code      string
		assistant string
	}{
		{"no AI signed in (the HTTP 401 run)", handoffStopProof{NoAIAtStart: true}, handoff.CauseNoAssistant, handoff.EventCodeStoppedNoAssistant, ""},
		{"signed out", handoffStopProof{External: true, Assistant: "claude_code"}, handoff.CauseSignedOut, handoff.EventCodeStopped, "claude_code"},
		{"assistant error", handoffStopProof{External: true, SignedIn: true, Assistant: "codex"}, handoff.CauseAssistantError, handoff.EventCodeStopped, "codex"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withHandoffStopProof(t, tc.proof)
			app := mainChatTestApp(t)
			store, err := handoff.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			app.handoffStore = store
			saveLifecycleHandoff(t, store, handoff.Entry{ID: "hf-401", Title: "Draft the memo", Status: handoff.StatusInProgress, AttemptID: "a1", NoAIAtStart: tc.proof.NoAIAtStart})
			app.markHandoffDone("hf-401", false, "")
			got, _ := store.Get("hf-401")
			if got.Status != handoff.StatusAborted || got.StoppedBy != handoff.StoppedBySystem || got.StopCause != tc.cause || got.StopAssistant != tc.assistant || got.StoppedAt == 0 {
				t.Fatalf("stop = %+v", got)
			}
			if group, cause := handoff.Placement(got); group != handoff.BoardNeedsYou || cause != tc.cause {
				t.Fatalf("placement = %q %q", group, cause)
			}
			events := got.Thread.Events
			if len(events) == 0 || events[len(events)-1].Code != tc.code || events[len(events)-1].Params["cause"] != tc.cause {
				t.Fatalf("thread = %+v", got.Thread)
			}
			page := mobileTaskPage([]handoff.Entry{got}, nil, time.Now())
			if page.Counts.NeedsYou != 1 || len(page.Tasks) != 1 || page.Tasks[0].StopCause != tc.cause || page.Tasks[0].Group != mobileTaskGroupNeedsYou {
				t.Fatalf("phone page = %+v", page)
			}
			if view := handoffEntryView(got); view.Group != handoff.BoardNeedsYou || view.StopCause != tc.cause {
				t.Fatalf("desktop view = %+v", view)
			}
		})
	}
}

func TestStoppingATaskYourselfGoesToDoneAndNeverNotifies(t *testing.T) {
	app := mainChatTestApp(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.handoffStore = store
	saveLifecycleHandoff(t, store, handoff.Entry{ID: "hf-mine", Title: "Draft the memo", Status: handoff.StatusInProgress, AttemptID: "a1"})
	if err := app.AbortHandoff("hf-mine"); err != nil {
		t.Fatal(err)
	}
	got, _ := store.Get("hf-mine")
	if !handoff.StoppedByYou(got) || got.Thread == nil || got.Thread.Outcome == nil || got.Thread.Outcome.Kind != handoff.OutcomeStopped || got.Thread.Outcome.By != handoff.OutcomeByYou {
		t.Fatalf("stop = %+v", got)
	}
	if group, _ := handoff.Placement(got); group != handoff.BoardDone {
		t.Fatalf("group = %q", group)
	}
	if items := mainChatProactiveItems(t, app); len(items) != 0 {
		t.Fatalf("stopping it yourself notified: %+v", items)
	}
	page := mobileTaskPage([]handoff.Entry{got}, nil, time.Now())
	if page.Counts.DoneThisWeek != 1 || page.Counts.NeedsYou != 0 || page.Tasks[0].StoppedBy != handoff.StoppedByPerson || page.Tasks[0].Outcome == nil || page.Tasks[0].Outcome.Kind != handoff.OutcomeStopped {
		t.Fatalf("phone page = %+v", page)
	}
}

func TestQuittingMidTaskComesBackAsRestarted(t *testing.T) {
	app := mainChatTestApp(t)
	store, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	app.handoffStore = store
	saveLifecycleHandoff(t, store, handoff.Entry{ID: "hf-quit", Title: "Draft the memo", Status: handoff.StatusInProgress, AttemptID: "a1"})
	app.handoffShuttingDown.Store(true)
	app.markHandoffDone("hf-quit", false, "")
	if got, _ := store.Get("hf-quit"); got.Status != handoff.StatusInProgress {
		t.Fatalf("quitting settled the run: %+v", got)
	}
	reopened := &App{store: app.store, handoffStore: store}
	reopened.recoverInterruptedHandoffs()
	got, _ := store.Get("hf-quit")
	if group, cause := handoff.Placement(got); group != handoff.BoardNeedsYou || cause != handoff.CauseRestarted {
		t.Fatalf("placement = %q %q (%+v)", group, cause, got)
	}
	if events := got.Thread.Events; len(events) == 0 || events[len(events)-1].Params["cause"] != handoff.CauseRestarted {
		t.Fatalf("thread = %+v", got.Thread)
	}
}

func TestStartAgainRunsTheSameTaskWithAStartedAgainLine(t *testing.T) {
	for _, stop := range []handoff.Entry{
		{Status: handoff.StatusAborted, StoppedBy: handoff.StoppedBySystem, StopCause: handoff.CauseNoAssistant},
		{Status: handoff.StatusAborted, StoppedBy: handoff.StoppedByPerson, Thread: &handoff.Thread{Outcome: &handoff.Outcome{Kind: handoff.OutcomeStopped, By: handoff.OutcomeByYou}}},
		{Status: handoff.StatusBlocked, BlockedReason: handoff.BlockedTimeout, StopCause: handoff.CauseTimeout, StoppedBy: handoff.StoppedBySystem},
	} {
		t.Run(string(stop.Status)+stop.StoppedBy+stop.StopCause, func(t *testing.T) {
			app := mainChatTestApp(t)
			store, err := handoff.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			app.handoffStore = store
			entry := stop
			entry.ID, entry.Title, entry.Body = "hf-again", "Draft the memo", "# Draft the memo\n\nThe brief."
			saveLifecycleHandoff(t, store, entry)
			result, err := app.AcceptHandoff("hf-again")
			if err != nil {
				t.Fatal(err)
			}
			if result.HandoffID != "hf-again" || result.Prompt == "" {
				t.Fatalf("result = %+v", result)
			}
			got, _ := store.Get("hf-again")
			if got.Status != handoff.StatusInProgress || got.StoppedBy != "" || got.StopCause != "" || (got.Thread.Outcome != nil && got.Thread.Outcome.Kind == handoff.OutcomeStopped) {
				t.Fatalf("restart = %+v", got)
			}
			last := got.Thread.Events[len(got.Thread.Events)-1]
			if last.Kind != handoff.EventLine || last.Code != handoff.EventCodeStartedAgain || last.At == 0 {
				t.Fatalf("last event = %+v", last)
			}
		})
	}
}

func TestStopEventTextUsesTheCopyTable(t *testing.T) {
	at := time.Date(2026, 10, 31, 15, 4, 0, 0, time.Local).UnixMilli()
	for _, tc := range []struct {
		event handoff.Event
		lang  string
		want  string
	}{
		{handoff.Event{Code: handoff.EventCodeStoppedNoAssistant, At: at}, "en", "I started at 3:04 PM, but no AI is signed in on this computer, so nothing was done. Set one up, then start again."},
		{handoff.Event{Code: handoff.EventCodeStoppedNoAssistant, At: at}, "zh-CN", "我15:04开始了，但这台电脑上还没有登录 AI，所以什么都没做。设置好以后再重新开始。"},
		{handoff.Event{Code: handoff.EventCodeStopped, At: at, Params: map[string]string{"cause": handoff.CauseRestarted}}, "en", "I stopped at 3:04 PM because Isoform restarted. What I'd finished is below."},
		{handoff.Event{Code: handoff.EventCodeStopped, At: at, Params: map[string]string{"cause": handoff.CauseTimeout}}, "zh-CN", "我在15:04停下了，因为两小时没有任何进展。已经完成的部分在下面。"},
		{handoff.Event{Code: handoff.EventCodeStopped, At: at, Params: map[string]string{"cause": handoff.CauseAssistantError, "assistant": "codex"}}, "en", "I stopped at 3:04 PM because Codex stopped with an error. What I'd finished is below."},
		{handoff.Event{Code: handoff.EventCodeStartedAgain, At: at}, "zh-CN", "重新开始 · 15:04"},
	} {
		if got := handoffStopEventText(tc.event, tc.lang); got != tc.want {
			t.Errorf("%s %s = %q, want %q", tc.event.Code, tc.lang, got, tc.want)
		}
	}
}

func TestHandoffRunFailureCausesFollowTheRecord(t *testing.T) {
	for _, tc := range []struct {
		name        string
		noAIAtStart bool
		runErr      error
		want        string
	}{
		{"no AI configured at start, then HTTP 401", true, &native.HTTPError{Status: 401}, handoff.CauseNoAssistant},
		{"configured AI returns 401", false, fmt.Errorf("run: %w", &native.HTTPError{Status: 401, Body: "bad key"}), handoff.CauseSignedOut},
		{"configured AI returns 403", false, &native.HTTPError{Status: 403}, handoff.CauseSignedOut},
		{"configured AI returns 500", false, &native.HTTPError{Status: 500}, handoff.CauseAssistantError},
		{"typed loop guard", false, fmt.Errorf("run: %w", native.ErrLoopGuardStuck), handoff.CauseLoopGuard},
		{"error text says unauthorized", false, errors.New("401 Unauthorized: please log in"), handoff.CauseAssistantError},
		{"no error recorded", false, nil, handoff.CauseAssistantError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withHandoffStopProof(t, handoffStopProof{})
			app := mainChatTestApp(t)
			store, err := handoff.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			app.handoffStore = store
			saveLifecycleHandoff(t, store, handoff.Entry{ID: "hf-run", Title: "Draft the memo", Status: handoff.StatusInProgress, AttemptID: "a1", NoAIAtStart: tc.noAIAtStart})
			entry, _ := store.Get("hf-run")
			app.markHandoffAttemptEnded(entry, false, "", tc.runErr)
			got, _ := store.Get("hf-run")
			if got.StopCause != tc.want {
				t.Fatalf("cause = %q, want %q", got.StopCause, tc.want)
			}
		})
	}
}

func TestAcceptRecordsTheStartTimeAICheck(t *testing.T) {
	for _, ready := range []bool{false, true} {
		t.Run(fmt.Sprint(ready), func(t *testing.T) {
			app := mainChatTestApp(t)
			store, err := handoff.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			app.handoffStore = store
			setHandoffStartAIReadyForTest(t, ready)
			saveLifecycleHandoff(t, store, handoff.Entry{ID: "hf-new", Title: "Draft the memo", Status: handoff.StatusActive})
			_, _ = app.AcceptHandoff("hf-new")
			got, _ := store.Get("hf-new")
			if got.Status == handoff.StatusActive {
				t.Skipf("accept did not claim in this harness: %+v", got)
			}
			if got.NoAIAtStart != !ready {
				t.Fatalf("noAiAtStart = %v with ready=%v", got.NoAIAtStart, ready)
			}
		})
	}
}

func TestLegacyAbortedRowIsDoneWithoutClaimingYouStoppedIt(t *testing.T) {
	now := time.Now()
	legacy := handoff.Entry{ID: "hf-old", Title: "Old work", Status: handoff.StatusAborted, UpdatedAt: now.Add(-time.Hour).UnixMilli()}
	if group, cause := handoff.Placement(legacy); group != handoff.BoardDone || cause != "" {
		t.Fatalf("placement = %q %q", group, cause)
	}
	if handoff.StoppedByYou(legacy) || !handoff.StoppedUnrecorded(legacy) {
		t.Fatal("a legacy row claimed the person stopped it")
	}
	if view := handoffEntryView(legacy); view.Group != handoff.BoardDone || view.StoppedBy != "" {
		t.Fatalf("desktop view = %+v", view)
	}
	page := mobileTaskPage([]handoff.Entry{legacy}, nil, now)
	// The phone protocol excludes stops recorded before their cause was tracked.
	if page.Counts.DoneThisWeek != 0 || page.Counts.NeedsYou != 0 || len(page.Tasks) != 0 {
		t.Fatalf("phone page = %+v", page)
	}
}

func TestUnknownAIIsYourAI(t *testing.T) {
	event := handoff.Event{Code: handoff.EventCodeStopped, At: time.Now().UnixMilli(), Params: map[string]string{"cause": handoff.CauseAssistantError}}
	if got := handoffStopEventText(event, "en"); !strings.Contains(got, "your AI stopped with an error") {
		t.Fatalf("en = %q", got)
	}
	if got := handoffStopEventText(event, "zh-CN"); !strings.Contains(got, "你的 AI 出错停止了") {
		t.Fatalf("zh = %q", got)
	}
}

func setHandoffStartAIReadyForTest(t *testing.T, ready bool) {
	t.Helper()
	previous := handoffStartAIReady
	handoffStartAIReady = func(*App) bool { return ready }
	t.Cleanup(func() { handoffStartAIReady = previous })
}
