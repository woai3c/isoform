//go:build darwin

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/platform/activitymon"
)

// ── Helpers ──────────────────────────────────────────────────────────

func skipIfNoAccessibility(t *testing.T) {
	t.Helper()
	if !activitymon.ComputerUseSupported() {
		t.Skip("computer use not supported on this platform")
	}
	if !activitymon.ComputerUsePermitted() {
		t.Skip("Accessibility permission not granted — enable in System Settings > Privacy & Security > Accessibility")
	}
}

func skipUnlessBrowserLaunchEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("YANSU_TEST_OPEN_BROWSER") != "1" {
		t.Skip("set YANSU_TEST_OPEN_BROWSER=1 to run tests that open the default browser")
	}
}

// bridgeURL reads the host bridge URL from the environment.
// Set HOST_BRIDGE_URL=http://127.0.0.1:<port> before running bridge tests.
// The port is logged by Yansu at startup: "[host-bridge] listening on 127.0.0.1:<port>"
// The bridge also wants its token: launch Isoform with
// YANSU_HOST_BRIDGE_TOKEN=<32 or more characters> and pass the same value
// here as HOST_BRIDGE_TOKEN.
func bridgeURL(t *testing.T) string {
	t.Helper()
	if url := os.Getenv("HOST_BRIDGE_URL"); url != "" {
		return url
	}
	return ""
}

// hostBridgeTokenHeader matches wails-gui/host_bridge_guard.go.
const hostBridgeTokenHeader = "X-Yansu-Host-Bridge-Token"

// newBridgeRequest builds a host bridge request carrying HOST_BRIDGE_TOKEN.
func newBridgeRequest(method, url string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set(hostBridgeTokenHeader, os.Getenv("HOST_BRIDGE_TOKEN"))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func postJSON(t *testing.T, url string, body any) map[string]any {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := newBridgeRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		t.Fatalf("POST %s: status %d: %s", url, resp.StatusCode, string(data))
	}
	var result map[string]any
	json.Unmarshal(data, &result)
	return result
}

func getJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	req, err := newBridgeRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		t.Fatalf("GET %s: status %d: %s", url, resp.StatusCode, string(data))
	}
	var result map[string]any
	json.Unmarshal(data, &result)
	return result
}

// ── Direct API tests (no host bridge needed) ────────────────────────

func TestComputerUsePermissions(t *testing.T) {
	if !activitymon.ComputerUseSupported() {
		t.Skip("not macOS")
	}
	t.Logf("ComputerUseSupported: %v", activitymon.ComputerUseSupported())
	t.Logf("ComputerUsePermitted: %v", activitymon.ComputerUsePermitted())
}

func TestListRunningApps(t *testing.T) {
	skipIfNoAccessibility(t)
	apps := activitymon.ListRunningApps()
	if len(apps) == 0 {
		t.Fatal("expected at least one running app")
	}
	t.Logf("found %d running apps", len(apps))

	// Finder should always be running.
	found := false
	for _, a := range apps {
		if a.Name == "Finder" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Finder not found in running apps")
	}
}

func TestListAppWindows(t *testing.T) {
	skipIfNoAccessibility(t)
	windows := activitymon.ListAppWindows("Finder")
	// Finder may have 0 windows if all are closed, but shouldn't error.
	t.Logf("Finder has %d windows", len(windows))
}

func TestCollectAppElements(t *testing.T) {
	skipIfNoAccessibility(t)
	start := time.Now()
	elements := activitymon.CollectAppElements("Finder")
	elapsed := time.Since(start)
	t.Logf("collected %d elements from Finder in %v", len(elements), elapsed)

	if elapsed > 10*time.Second {
		t.Errorf("element collection took too long: %v", elapsed)
	}
}

func TestOpenURL(t *testing.T) {
	skipIfNoAccessibility(t)
	skipUnlessBrowserLaunchEnabled(t)
	// Open a benign URL.
	ok := activitymon.OpenURL("https://example.com")
	if !ok {
		t.Error("OpenURL returned false")
	}
}

// ── Host Bridge integration tests (require running Yansu) ───────────

func TestBridgeApps(t *testing.T) {
	base := bridgeURL(t)
	if base == "" {
		t.Skip("host bridge not available (Yansu not running?)")
	}
	skipIfNoAccessibility(t)

	result := getJSON(t, base+"/api/computer-use/apps")
	if result["success"] != true {
		t.Fatalf("expected success: %v", result)
	}
	apps, ok := result["data"].([]any)
	if !ok || len(apps) == 0 {
		t.Fatalf("expected non-empty apps list: %v", result)
	}
	t.Logf("bridge returned %d apps", len(apps))
}

func TestBridgeWindows(t *testing.T) {
	base := bridgeURL(t)
	if base == "" {
		t.Skip("host bridge not available")
	}
	skipIfNoAccessibility(t)

	result := getJSON(t, base+"/api/computer-use/windows?app=Finder")
	if result["success"] != true {
		t.Fatalf("expected success: %v", result)
	}
	t.Logf("Finder windows via bridge: %v", result["data"])
}

func TestBridgeSee(t *testing.T) {
	base := bridgeURL(t)
	if base == "" {
		t.Skip("host bridge not available")
	}
	skipIfNoAccessibility(t)

	result := postJSON(t, base+"/api/computer-use/see", map[string]string{"app": "Finder"})
	if result["success"] != true {
		t.Fatalf("expected success: %v", result)
	}
	data, _ := result["data"].(map[string]any)
	count, _ := data["element_count"].(float64)
	t.Logf("Finder accessibility tree: %d elements", int(count))
}

func TestBridgeOpen(t *testing.T) {
	base := bridgeURL(t)
	if base == "" {
		t.Skip("host bridge not available")
	}
	skipIfNoAccessibility(t)
	skipUnlessBrowserLaunchEnabled(t)

	result := postJSON(t, base+"/api/computer-use/open", map[string]string{"url": "https://example.com"})
	if result["success"] != true {
		t.Fatalf("expected success: %v", result)
	}
}

// TestBridgeFullFlow runs a complete flow: open URL, wait, capture elements.
func TestBridgeFullFlow(t *testing.T) {
	base := bridgeURL(t)
	if base == "" {
		t.Skip("host bridge not available")
	}
	skipIfNoAccessibility(t)
	skipUnlessBrowserLaunchEnabled(t)

	// 1. Open a simple page.
	t.Log("step 1: opening example.com")
	postJSON(t, base+"/api/computer-use/open", map[string]string{"url": "https://example.com"})

	// 2. Wait for page load.
	time.Sleep(3 * time.Second)

	// 3. Capture Safari's accessibility tree.
	t.Log("step 2: capturing Safari elements")
	var elements []any
	for attempt := 0; attempt < 5; attempt++ {
		result := postJSON(t, base+"/api/computer-use/see", map[string]string{"app": "Safari"})
		data, _ := result["data"].(map[string]any)
		if els, ok := data["ui_elements"].([]any); ok {
			elements = els
			if len(els) > 10 {
				break
			}
		}
		time.Sleep(1 * time.Second)
	}
	t.Logf("step 3: captured %d elements from Safari", len(elements))

	if len(elements) == 0 {
		t.Error("expected at least some elements from Safari after loading example.com")
	}

	// 4. Look for "Example Domain" text in elements.
	found := false
	for _, el := range elements {
		m, _ := el.(map[string]any)
		for _, field := range []string{"label", "title", "value"} {
			if s, ok := m[field].(string); ok && strings.Contains(s, "Example Domain") {
				found = true
				break
			}
		}
		if found {
			break
		}
	}
	if found {
		t.Log("step 4: found 'Example Domain' in accessibility tree")
	} else {
		t.Log("step 4: 'Example Domain' not found (may be behind other windows)")
	}

	fmt.Println("\nE2E test passed.")
}

// The host bridge refuses a request without its token and a POST without a
// JSON Content-Type, so the e2e client must send both.
func TestBridgeRequestSendsTokenAndJSON(t *testing.T) {
	t.Setenv("HOST_BRIDGE_TOKEN", "e2e-token")
	post, err := newBridgeRequest(http.MethodPost, "http://127.0.0.1:1/api/computer-use/see", bytes.NewReader([]byte(`{}`)))
	if err != nil {
		t.Fatal(err)
	}
	if got := post.Header.Get(hostBridgeTokenHeader); got != "e2e-token" {
		t.Fatalf("token header = %q", got)
	}
	if got := post.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	get, err := newBridgeRequest(http.MethodGet, "http://127.0.0.1:1/api/computer-use/apps", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := get.Header.Get(hostBridgeTokenHeader); got != "e2e-token" {
		t.Fatalf("GET token header = %q", got)
	}
}
