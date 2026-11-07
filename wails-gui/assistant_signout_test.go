package main

import (
	"testing"
	"time"

	"yansu-agent/agent"
)

func TestSignOutTrackerNeedsOneRecheckBeforeConfirming(t *testing.T) {
	var tr signOutTracker
	t0 := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

	step, _ := tr.observe("claude-code", signInOut, t0)
	if step != signOutRecheck {
		t.Fatalf("first sighting = %v, want recheck", step)
	}
	if tr.isConfirmed("claude-code") {
		t.Fatal("a single sighting must not confirm a sign-out")
	}
}

func TestSignOutTrackerBurstOfReadsDoesNotConfirm(t *testing.T) {
	var tr signOutTracker
	t0 := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

	tr.observe("claude-code", signInOut, t0)
	// Brief and chat reads in the same few seconds are one reading, not two.
	for _, offset := range []time.Duration{time.Second, 5 * time.Second, 19 * time.Second} {
		if step, _ := tr.observe("claude-code", signInOut, t0.Add(offset)); step != signOutNone {
			t.Fatalf("read at +%s = %v, want none", offset, step)
		}
	}
	if tr.isConfirmed("claude-code") {
		t.Fatal("reads inside the re-check window must not confirm")
	}
}

func TestSignOutTrackerConfirmsOnceAfterRecheck(t *testing.T) {
	var tr signOutTracker
	t0 := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

	tr.observe("claude-code", signInOut, t0)
	step, since := tr.observe("claude-code", signInOut, t0.Add(assistantSignOutRecheckAfter))
	if step != signOutConfirmed || !since.Equal(t0.Add(assistantSignOutRecheckAfter)) {
		t.Fatalf("recheck = %v at %v, want confirmed at the recheck time", step, since)
	}
	// Still signed out: no second notification.
	if step, _ := tr.observe("claude-code", signInOut, t0.Add(time.Minute)); step != signOutNone {
		t.Fatalf("repeat reading after confirmation = %v, want none", step)
	}
}

func TestSignOutTrackerClearsOnSignIn(t *testing.T) {
	var tr signOutTracker
	t0 := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

	tr.observe("claude-code", signInOut, t0)
	tr.observe("claude-code", signInOut, t0.Add(assistantSignOutRecheckAfter))
	step, since := tr.observe("claude-code", signInOK, t0.Add(10*time.Minute))
	if step != signOutCleared {
		t.Fatalf("sign-in after confirmation = %v, want cleared", step)
	}
	if !since.Equal(t0.Add(assistantSignOutRecheckAfter)) {
		t.Fatalf("cleared since = %v, want the confirmation time", since)
	}
	if tr.isConfirmed("claude-code") {
		t.Fatal("confirmed state must clear on sign-in")
	}
}

func TestSignOutTrackerBlipBeforeConfirmationClearsPending(t *testing.T) {
	var tr signOutTracker
	t0 := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

	tr.observe("claude-code", signInOut, t0)
	tr.observe("claude-code", signInOK, t0.Add(5*time.Second))
	// A new sighting starts over; it is not confirmed by the old one.
	if step, _ := tr.observe("claude-code", signInOut, t0.Add(assistantSignOutRecheckAfter+time.Second)); step != signOutRecheck {
		t.Fatalf("sighting after a blip = %v, want a fresh recheck", step)
	}
}

func TestSignOutTrackerUnknownVerdictChangesNothing(t *testing.T) {
	var tr signOutTracker
	t0 := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

	tr.observe("claude-code", signInOut, t0)
	if step, _ := tr.observe("claude-code", signInUnknown, t0.Add(time.Minute)); step != signOutNone {
		t.Fatalf("unknown reading = %v, want none", step)
	}
	tr.observe("claude-code", signInOut, t0.Add(assistantSignOutRecheckAfter))
	if !tr.isConfirmed("claude-code") {
		t.Fatal("expected confirmation")
	}
	if step, _ := tr.observe("claude-code", signInUnknown, t0.Add(2*time.Minute)); step != signOutNone {
		t.Fatalf("unknown reading while confirmed = %v, want none (a failed probe is not a sign-in)", step)
	}
	if !tr.isConfirmed("claude-code") {
		t.Fatal("an unknown reading must not clear a confirmed sign-out")
	}
}

func TestSignOutTrackerAssistantsAreIndependent(t *testing.T) {
	var tr signOutTracker
	t0 := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

	tr.observe("claude-code", signInOut, t0)
	tr.observe("claude-code", signInOut, t0.Add(assistantSignOutRecheckAfter))
	if tr.isConfirmed("codex") {
		t.Fatal("codex must not be confirmed by claude-code's sign-out")
	}
}

func TestFirstConfirmedFollowsUserOrder(t *testing.T) {
	var tr signOutTracker
	t0 := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

	for _, assistant := range []string{"claude-code", "codex"} {
		tr.observe(assistant, signInOut, t0)
		tr.observe(assistant, signInOut, t0.Add(assistantSignOutRecheckAfter))
	}
	got, ok := tr.firstConfirmed([]string{"codex", "claude-code"})
	if !ok || got != "codex" {
		t.Fatalf("firstConfirmed in codex-first order = %q, %v; want codex", got, ok)
	}
	if _, ok := tr.firstConfirmed([]string{"other"}); ok {
		t.Fatal("no confirmed assistant in the order must report none")
	}
}

func TestAssistantSignInVerdictOnlyCountsProbeSignedOut(t *testing.T) {
	cases := []struct {
		name  string
		state agent.AssistantState
		want  signInVerdict
	}{
		{"signed in", agent.AssistantState{Available: true, SignedIn: true}, signInOK},
		{"signed out", agent.AssistantState{Available: true, Reason: "signed out"}, signInOut},
		{"probe failed is not signed out", agent.AssistantState{Available: true, Reason: "sign-in probe failed"}, signInUnknown},
		{"binary missing", agent.AssistantState{Reason: "binary missing"}, signInUnknown},
	}
	for _, tc := range cases {
		if got := assistantSignInVerdict(tc.state); got != tc.want {
			t.Errorf("%s: verdict = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAssistantSignOutTelemetryAllowlist(t *testing.T) {
	if _, ok := productTelemetryProperties("assistant.signed_out", map[string]any{"assistant": "claude-code"}); !ok {
		t.Fatal("signed_out with a known assistant must be allowed")
	}
	props, ok := productTelemetryProperties("assistant.signed_out_fixed", map[string]any{"assistant": "codex", "seconds": 300, "email": "x@example.com"})
	if !ok {
		t.Fatal("signed_out_fixed with a known assistant must be allowed")
	}
	if _, leaked := props["email"]; leaked {
		t.Fatal("extra properties must not pass the allowlist")
	}
	if _, ok := productTelemetryProperties("assistant.signed_out", map[string]any{"assistant": "Claude Code"}); ok {
		t.Fatal("a display name is not an allowed assistant id")
	}
}

func TestMenuBarSignOutRowNamesConfirmedAssistantOnly(t *testing.T) {
	a := &App{}
	words := menuBarWords("en")
	if row := a.menuBarSignOutRow([]string{"claude-code"}, words); row != nil {
		t.Fatalf("no confirmed sign-out must give no row, got %+v", row)
	}
	t0 := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	a.signOut.observe("claude-code", signInOut, t0)
	a.signOut.observe("claude-code", signInOut, t0.Add(assistantSignOutRecheckAfter))

	row := a.menuBarSignOutRow([]string{"codex", "claude-code"}, words)
	if row == nil || row.Assistant != "claude-code" {
		t.Fatalf("row = %+v, want claude-code", row)
	}
	if row.Line != "Claude Code is signed out, so briefs are shorter and Chat can't answer." {
		t.Fatalf("line = %q", row.Line)
	}
	if row.SignIn != "Sign in" {
		t.Fatalf("sign-in label = %q", row.SignIn)
	}
	zh := a.menuBarSignOutRow([]string{"claude-code"}, menuBarWords("zh-CN"))
	if zh == nil || zh.Line != "Claude Code 已退出登录，所以简报会更简短，聊天也无法回答。" {
		t.Fatalf("zh-CN line = %+v", zh)
	}
}

func TestMobileTodaySignOutFieldOnlyWhenConfirmed(t *testing.T) {
	var nilApp *App
	if nilApp.mobileTodaySignOut([]string{"claude-code"}) != nil {
		t.Fatal("a nil app must report no sign-out")
	}
	a := &App{}
	if a.mobileTodaySignOut([]string{"claude-code"}) != nil {
		t.Fatal("no confirmed sign-out must omit the field")
	}
	t0 := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	a.signOut.observe("claude-code", signInOut, t0)
	a.signOut.observe("claude-code", signInOut, t0.Add(assistantSignOutRecheckAfter))
	got := a.mobileTodaySignOut([]string{"claude-code"})
	if got == nil || got.Assistant != "claude-code" {
		t.Fatalf("field = %+v, want claude-code", got)
	}
}
