package main

import "testing"

func TestComputerUseAppAccessSnapshotFreezesNoOAuthPolicy(t *testing.T) {
	snapshot := (&App{}).GetComputerUseAppAccessSnapshot()
	if snapshot.PolicyVersion != ComputerUseAppAccessPolicyVersion {
		t.Fatalf("policy version = %q", snapshot.PolicyVersion)
	}
	if ComputerUseAppAccessMode != "computer_use_only" {
		t.Fatalf("policy constant = %q, want computer_use_only", ComputerUseAppAccessMode)
	}
	if snapshot.Mode != ComputerUseAppAccessMode {
		t.Fatalf("mode = %q, want computer_use_only", snapshot.Mode)
	}
	if snapshot.OAuthFallback {
		t.Fatal("first-party app access must not expose OAuth fallback")
	}
	if managedComposioAppAccessEnabled {
		t.Fatal("managed Composio app access must remain disabled")
	}

	seen := map[string]bool{}
	for _, app := range snapshot.Apps {
		if app.ID == "" || app.Label == "" {
			t.Fatalf("incomplete app definition: %+v", app)
		}
		if seen[app.ID] {
			t.Fatalf("duplicate app id %q", app.ID)
		}
		seen[app.ID] = true
	}
	for _, required := range []string{"gmail", "google-calendar", "slack", "github", "microsoft-teams"} {
		if !seen[required] {
			t.Fatalf("required Computer Use app %q missing", required)
		}
	}
}

func TestComputerUseAppAccessProbeFailsClosedWithoutDriver(t *testing.T) {
	result, err := (&App{}).ProbeComputerUseAppAccess()
	if err == nil {
		t.Fatal("probe without a Computer Use driver should fail closed")
	}
	if result.Snapshot.OAuthFallback || result.Snapshot.Mode != ComputerUseAppAccessMode {
		t.Fatalf("probe exposed an alternate transport: %+v", result.Snapshot)
	}
	if len(result.Browsers) != 0 || result.ReceiptHash != "" {
		t.Fatalf("failed probe should not fabricate browser evidence: %+v", result)
	}
}

func TestComputerUseBrowserCatalogAllowlist(t *testing.T) {
	for _, bundle := range []string{
		"com.apple.Safari",
		"com.google.Chrome",
		"company.thebrowser.Browser",
		"com.microsoft.edgemac",
		"com.brave.Browser",
	} {
		if !computerUseBrowserBundle(bundle) {
			t.Fatalf("supported browser %q rejected", bundle)
		}
	}
	if computerUseBrowserBundle("com.example.untrusted") {
		t.Fatal("unknown browser entered the routing allowlist")
	}
}
