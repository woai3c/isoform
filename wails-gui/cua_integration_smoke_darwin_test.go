//go:build darwin

package main

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// TestCuaIntegration_RealDaemonRoundTrip spins up the actual bundled
// cua-driver binary (built from cua-driver-src/) behind a real proxy
// and verifies the translator routes legacy-shape requests through
// alma's wire protocol and back without losing the envelope.
//
// Skipped automatically when the bundle isn't built (CI on non-darwin
// hosts, or a fresh checkout that hasn't run build-cua-driver-bundle.sh
// yet). We never gate on TCC grants here — `permissions` reports
// truthfully on either; we only assert the call round-trips.
//
// This test is the canary for translator/daemon protocol drift: any
// time we resync cua-driver-src/ from alma, this test catches a
// rename or shape change in `permissions`, `apps`, or the success
// envelope before it surfaces in a chat session.
func TestCuaIntegration_RealDaemonRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration smoke under -short")
	}
	if os.Getenv("YANSU_SKIP_CUA_INTEGRATION") != "" {
		t.Skip("YANSU_SKIP_CUA_INTEGRATION set")
	}
	paths, err := LocateBundledCuaDriver()
	if err != nil {
		t.Skipf("bundled cua-driver not located (run scripts/build-cua-driver-bundle.sh first): %v", err)
	}
	if _, err := os.Stat(paths.BinaryPath); err != nil {
		t.Skipf("bundled cua-driver binary missing: %v", err)
	}

	d, err := NewCuaDriverDaemon()
	if err != nil {
		t.Fatalf("NewCuaDriverDaemon: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	defer d.Stop(context.Background())

	if err := d.EnsureRunning(ctx); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}

	// 1) check_permissions — translates to alma's `permissions`.
	resp, err := d.Call(ctx, "check_permissions", nil)
	if err != nil {
		t.Fatalf("check_permissions: %v", err)
	}
	if !resp.OK {
		t.Fatalf("check_permissions returned ok=false: %s", resp.ErrorMessage())
	}
	var perm map[string]any
	if err := json.Unmarshal(resp.Result, &perm); err != nil {
		t.Fatalf("decode permissions result: %v (raw: %s)", err, resp.Result)
	}
	if _, ok := perm["accessibility"]; !ok {
		t.Errorf("permissions payload missing `accessibility` key: %v", perm)
	}

	// 2) list-tools — synthesized in-translator; the daemon never
	// sees this. We just need a non-empty tool list back.
	resp, err = d.Call(ctx, "list-tools", nil)
	if err != nil {
		// list-tools goes through Call with name="list-tools" but
		// method="call". The translator handles either; here we hit
		// the call path with name="list-tools", which the translator
		// also short-circuits via the tool-name guard.
		t.Logf("list-tools returned err=%v — translator only synthesizes via method=list-tools, not name=list-tools", err)
	} else if resp.OK {
		if !strings.Contains(string(resp.Result), "click") {
			t.Errorf("synthesized list-tools missing click entry: %s", resp.Result)
		}
	}

	// 3) list_apps — passthrough call that exercises real AX. Tolerate
	// a non-OK return if the host lacks the grant; just assert the
	// envelope shape.
	resp, err = d.Call(ctx, "list_apps", nil)
	if err != nil {
		t.Fatalf("list_apps: %v", err)
	}
	if resp.OK {
		// alma returns {apps: [...]} on success.
		var apps map[string]any
		if err := json.Unmarshal(resp.Result, &apps); err != nil {
			t.Fatalf("decode list_apps result: %v (raw: %.300s)", err, resp.Result)
		}
		if _, ok := apps["apps"]; !ok {
			t.Errorf("list_apps payload missing `apps` key: %v", apps)
		}
	}
}
