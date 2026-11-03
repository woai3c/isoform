package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBuildModelAccountsNeverInventsNumbers(t *testing.T) {
	rows := []magpieQuotaRow{
		{Provider: "codex", Name: "Codex", Kind: "subscription", Plan: "pro", Balance: "62500 credits"},
		{Provider: "kiro", Name: "Kiro", Kind: "subscription", Error: "sign-in has expired"},
		{
			Provider: "claude", Name: "Claude Code", Kind: "subscription", Plan: "max",
			Windows: []magpieQuotaWindowRow{{Name: "5 hours", Used: 26, Remaining: 74, ResetsAt: "2026-10-10T16:50:00-07:00"}},
		},
	}
	info := map[string]magpieAccountInfo{
		"codex":  {On: true, Plan: "pro", User: "a@example.com"},
		"claude": {On: false},
	}

	accounts := buildModelAccounts(rows, info)
	if len(accounts) != 3 {
		t.Fatalf("accounts = %d, want 3", len(accounts))
	}

	codex := accounts[0]
	if !codex.On || codex.User != "a@example.com" || codex.Balance != "62500 credits" {
		t.Errorf("codex = %+v", codex)
	}
	if codex.Windows == nil || len(codex.Windows) != 0 {
		t.Errorf("codex windows must be empty and non-nil, got %#v", codex.Windows)
	}

	kiro := accounts[1]
	if kiro.On || kiro.Error == "" || len(kiro.Windows) != 0 {
		t.Errorf("kiro = %+v", kiro)
	}

	claude := accounts[2]
	if claude.On {
		t.Error("the accounts snapshot must override On when it has the provider")
	}
	if len(claude.Windows) != 1 || claude.Windows[0].Used != 26 || claude.Windows[0].Remaining != 74 {
		t.Errorf("claude windows = %+v", claude.Windows)
	}

	encoded, err := json.Marshal(codex)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	_ = json.Unmarshal(encoded, &decoded)
	if w, ok := decoded["windows"].([]any); !ok || len(w) != 0 {
		t.Errorf("windows must encode as an empty JSON array, got %v", decoded["windows"])
	}
}

func TestMagpieModelStateDetectsAccountChanges(t *testing.T) {
	var m magpieModelState
	now := time.Now()
	rows := []magpieQuotaRow{{
		Provider: "claude", Windows: []magpieQuotaWindowRow{{Name: "5 hours", Used: 26, Remaining: 74}},
	}}

	if !m.applyQuotas(rows, nil, now) {
		t.Fatal("the first poll must report a change")
	}
	if m.applyQuotas(rows, nil, now) {
		t.Fatal("an identical poll must not report a change")
	}
	rows[0].Windows[0].Used = 27
	if !m.applyQuotas(rows, nil, now) {
		t.Fatal("a changed window must report a change")
	}

	if m.applyQuotas(nil, errors.New("gateway down"), now) {
		t.Fatal("a failed poll must not report a change")
	}
	snap := m.snapshot()
	if snap.PollErr != "gateway down" {
		t.Fatalf("PollErr = %q", snap.PollErr)
	}
	if len(snap.Accounts) != 1 || snap.Accounts[0].Windows[0].Used != 27 {
		t.Fatalf("a failed poll must keep the last good accounts: %+v", snap.Accounts)
	}
}

func TestMagpieModelStateAppliesBootstrapGroups(t *testing.T) {
	var m magpieModelState
	m.applyBootstrap(magpieBootstrapState{
		Groups: []magpieGroupPlan{
			{ID: "yansu-chat", Name: "yansu chat", Routing: "order", Members: []string{"claude/claude-sonnet-5-5"}},
		},
		Ollama:         true,
		ByokRegistered: true,
		Subscriptions:  true,
	}, nil)
	snap := m.snapshot()
	if len(snap.Groups) != 1 || snap.Groups[0].Members[0] != "claude/claude-sonnet-5-5" {
		t.Fatalf("groups = %+v", snap.Groups)
	}
	if !snap.Ollama || !snap.ByokRegistered || !snap.Subscriptions {
		t.Fatalf("detection flags = %+v", snap)
	}

	m.applyBootstrap(magpieBootstrapState{}, errors.New("not healthy"))
	if snap := m.snapshot(); snap.BootstrapErr != "not healthy" || len(snap.Groups) != 1 {
		t.Fatalf("a failed bootstrap keeps the last groups and records the error: %+v", snap)
	}
}

func TestFetchQuotasFromFakeMagpie(t *testing.T) {
	s := startFakeSidecar(t)
	quotas := `{"data":[{"provider":"claude","name":"Claude Code","kind":"subscription","plan":"max",
		"windows":[{"name":"5 hours","used":31,"remaining":69,"resetsAt":"2026-10-10T16:49:00-07:00"}]},
		{"provider":"codex","name":"Codex","kind":"subscription","balance":"62500 credits","windows":[]}]}`
	fakeState := filepath.Join(s.stateDir, "config", "magpie")
	if err := os.MkdirAll(fakeState, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(fakeState, "quotas.json"), []byte(quotas), 0o600); err != nil {
		t.Fatalf("write quotas: %v", err)
	}

	rows, err := s.fetchQuotas(context.Background())
	if err != nil {
		t.Fatalf("fetchQuotas: %v", err)
	}
	if len(rows) != 2 || rows[0].Provider != "claude" || rows[0].Windows[0].Used != 31 {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[1].Balance != "62500 credits" || len(rows[1].Windows) != 0 {
		t.Fatalf("codex row = %+v", rows[1])
	}
}

func TestFetchQuotasRequiresRunningSidecar(t *testing.T) {
	s := testSidecar(t, buildFakeMagpie(t))
	if _, err := s.fetchQuotas(context.Background()); err == nil {
		t.Fatal("fetchQuotas on a stopped sidecar must fail")
	}
}
