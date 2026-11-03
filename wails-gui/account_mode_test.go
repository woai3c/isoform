package main

import "testing"

// The daemon WebSocket (Yansu IM relay) must never resolve: the pure-function
// chokepoint fails closed in every build now that there is no Yansu account.
func TestNoAccount_DaemonWSURLBlocked(t *testing.T) {
	if _, ok := buildDaemonWSURL("https://dashboard.yansu.ai"); ok {
		t.Error("buildDaemonWSURL should return ok=false")
	}
}

// Without a Yansu account every backend helper fails closed in a normal build
// too, and the surfaces that needed the account answer locally.
func TestNoAccount_BackendOffInNormalBuild(t *testing.T) {
	if !noYansuAccount() {
		t.Fatal("noYansuAccount should be true in every build")
	}
	var target struct{}
	if err := apiGet("https://dashboard.yansu.ai", "token", "/api/auth/profile", &target); err != errNoYansuAccount {
		t.Errorf("apiGet should fail closed with errNoYansuAccount, got %v", err)
	}
	if err := apiPost("https://dashboard.yansu.ai", "token", "/api/x", nil, nil); err != errNoYansuAccount {
		t.Errorf("apiPost should fail closed with errNoYansuAccount, got %v", err)
	}
	if err := apiDelete("https://dashboard.yansu.ai", "token", "/api/x"); err != errNoYansuAccount {
		t.Errorf("apiDelete should fail closed with errNoYansuAccount, got %v", err)
	}
	a := &App{}
	status, err := a.GetSubscriptionStatus()
	if err != nil || status == nil || !status.IsActive || status.BillingEnabled {
		t.Errorf("GetSubscriptionStatus should answer an active, non-billable status locally, got %+v err=%v", status, err)
	}
}
