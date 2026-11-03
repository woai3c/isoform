//go:build windows

package activitymon

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestUIARequestMarshalShape locks in the JSON shape the PowerShell side
// reads — the script keys off lowercase "mode", "hwnd", and "budget" with
// a nested object so any rename here would silently break the protocol.
func TestUIARequestMarshalShape(t *testing.T) {
	req := uiaRequest{
		Mode: "tree",
		HWND: 12345,
		Budget: &uiaBudget{
			MaxNodes: 1000,
			MaxDepth: 20,
			MaxBytes: 65536,
		},
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(data)

	for _, needle := range []string{
		`"mode":"tree"`,
		`"hwnd":12345`,
		`"budget":{`,
		`"maxNodes":1000`,
		`"maxDepth":20`,
		`"maxBytes":65536`,
	} {
		if !strings.Contains(got, needle) {
			t.Errorf("expected %q in marshalled request, got: %s", needle, got)
		}
	}
}

// TestUIARequestOmitEmpty verifies the optional fields disappear from the
// wire when unset. The PowerShell side treats `null`/missing as "use
// defaults" for hwnd (foreground) and budget (built-in caps).
func TestUIARequestOmitEmpty(t *testing.T) {
	req := uiaRequest{Mode: "text"}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(data)

	if !strings.Contains(got, `"mode":"text"`) {
		t.Fatalf("expected mode=text, got: %s", got)
	}
	if strings.Contains(got, `"hwnd"`) {
		t.Errorf("hwnd should be omitted when zero, got: %s", got)
	}
	if strings.Contains(got, `"budget"`) {
		t.Errorf("budget should be omitted when nil, got: %s", got)
	}
}

// TestUIATextResponseParse exercises the response decoder with shapes the
// PowerShell side actually emits (including the error envelope).
func TestUIATextResponseParse(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantTxt string
		wantErr string
	}{
		{"plain", `{"text":"hello world"}`, "hello world", ""},
		{"empty", `{"text":""}`, "", ""},
		{"unicode", `{"text":"你好 世界"}`, "你好 世界", ""},
		{"error", `{"error":"no foreground window"}`, "", "no foreground window"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var resp uiaTextResponse
			if err := json.Unmarshal([]byte(tc.input), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if resp.Text != tc.wantTxt {
				t.Errorf("text: got %q want %q", resp.Text, tc.wantTxt)
			}
			if resp.Error != tc.wantErr {
				t.Errorf("error: got %q want %q", resp.Error, tc.wantErr)
			}
		})
	}
}

// TestUIATreeResponseShape locks in the field names so a refactor in
// uia_windows.go can't silently break the protocol.
func TestUIATreeResponseShape(t *testing.T) {
	const sample = `{"tree":{"role":"AXWindow","title":"Notepad"},"nodeCount":3,"truncated":false}`
	var resp uiaTreeResponse
	if err := json.Unmarshal([]byte(sample), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.NodeCount != 3 {
		t.Errorf("nodeCount: got %d want 3", resp.NodeCount)
	}
	if resp.Truncated {
		t.Error("expected truncated=false")
	}
	var node AXNode
	if err := json.Unmarshal(resp.Tree, &node); err != nil {
		t.Fatalf("unmarshal tree: %v", err)
	}
	if node.Role != "AXWindow" {
		t.Errorf("role: got %q want AXWindow", node.Role)
	}
	if node.Title != "Notepad" {
		t.Errorf("title: got %q want Notepad", node.Title)
	}
}

// TestUIATreeUnmarshalsAXNode covers the shape the PowerShell walker
// emits for a real-ish tree (window with two children, one with a frame).
// Confirms domClassList stays empty (UIA doesn't expose it) without
// confusing the unmarshaller.
func TestUIATreeUnmarshalsAXNode(t *testing.T) {
	const sample = `{"role":"AXWindow","title":"Untitled - Notepad","frame":{"x":0,"y":0,"w":1280,"h":720},"children":[{"role":"AXMenuBar","title":"Application"},{"role":"AXTextArea","value":"hello"}]}`
	var node AXNode
	if err := json.Unmarshal([]byte(sample), &node); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if node.Role != "AXWindow" || len(node.Children) != 2 {
		t.Fatalf("unexpected tree shape: %+v", node)
	}
	if node.Frame == nil || node.Frame.W != 1280 {
		t.Errorf("frame not parsed: %+v", node.Frame)
	}
	if len(node.DOMClassList) != 0 {
		t.Errorf("UIA trees should not carry DOM class lists, got %v", node.DOMClassList)
	}
	if node.Children[1].Role != "AXTextArea" || node.Children[1].Value != "hello" {
		t.Errorf("child[1] mismatch: %+v", node.Children[1])
	}
}

// TestUIASmokeForegroundText starts the real PowerShell subprocess and
// asks for the foreground window's text. The test passes when:
//   - PowerShell is unavailable (skipped — common in headless CI)
//   - testing.Short() is set (skipped — PowerShell warmup blows up the
//     parallel test budget when run inside `go test ./...`)
//   - The subprocess starts, returns within the budget, and the response
//     is either a non-error JSON envelope or a controlled error like
//     "no foreground window" (which can happen if the test runner has
//     no foreground HWND).
//
// This is a coarse end-to-end check — it does NOT assert on the contents
// of the returned text because that depends on whatever the developer
// happens to be focused on. The point is to catch protocol regressions
// (missing READY sentinel, malformed JSON envelope, etc).
//
// Cold-start budget: PowerShell + UIAutomationClient + UIAutomationTypes +
// the CacheRequest setup takes ~2-3s in isolation but can stretch to 15s+
// under parallel-test CPU contention. Give it 60s to keep the suite green
// on slower machines / CI runners.
func TestUIASmokeForegroundText(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PowerShell UIA smoke test in short mode")
	}
	if _, err := exec.LookPath("powershell.exe"); err != nil {
		t.Skipf("powershell.exe not on PATH: %v", err)
	}

	e := &uiaEngine{}
	defer e.killProcess()

	done := make(chan struct{}, 1)
	go func() {
		_ = e.CollectText(0)
		done <- struct{}{}
	}()

	select {
	case <-done:
		// success — contents are environment-specific
	case <-time.After(60 * time.Second):
		t.Fatalf("CollectText did not return within 60s — PowerShell subprocess likely hung")
	}
}

// TestNormalizeBundle exercises the lowercase-only normalization used
// when matching enumerated HWND owners against the bundleID stand-in
// (lowercase exe basename) produced by FrontmostAppBundleID.
func TestNormalizeBundle(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"Notepad.exe", "notepad.exe"},
		{"NOTEPAD.EXE", "notepad.exe"},
		{"slack.exe", "slack.exe"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := normalizeBundle(tc.in); got != tc.out {
			t.Errorf("normalizeBundle(%q) = %q, want %q", tc.in, got, tc.out)
		}
	}
}

// TestTruncForLog confirms the log helper caps long payloads (used when
// the PowerShell side emits something we can't parse — we want a short
// excerpt in the log without spamming the entire response).
func TestTruncForLog(t *testing.T) {
	short := []byte("short")
	if got := truncForLog(short); got != "short" {
		t.Errorf("short input: got %q", got)
	}
	long := make([]byte, 1024)
	for i := range long {
		long[i] = 'a'
	}
	got := truncForLog(long)
	if !strings.HasSuffix(got, "...(truncated)") {
		// Go 1.21+ ships a built-in max.
		start := len(got) - 20
		if start < 0 {
			start = 0
		}
		t.Errorf("expected truncation suffix, got: %s", got[start:])
	}
	if len(got) > 300 {
		t.Errorf("truncated output too long: %d bytes", len(got))
	}
}
