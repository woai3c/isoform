//go:build darwin

package activitymon

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"
)

func skipUnlessBrowserLaunchEnabled(t *testing.T) {
	t.Helper()
	if os.Getenv("YANSU_TEST_OPEN_BROWSER") != "1" {
		t.Skip("set YANSU_TEST_OPEN_BROWSER=1 to run tests that open the default browser")
	}
}

func TestListRunningApps(t *testing.T) {
	apps := ListRunningApps()
	if len(apps) == 0 {
		t.Fatal("expected at least one running app")
	}
	t.Logf("Found %d running apps:", len(apps))
	for _, app := range apps {
		active := ""
		if app.IsActive {
			active = " [ACTIVE]"
		}
		t.Logf("  %s (%s) pid=%d%s", app.Name, app.BundleID, app.PID, active)
	}
}

func TestListRunningAppsBackground(t *testing.T) {
	// Keep this caller on its thread so enumeration runs on a separate thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	result := make(chan []RunningApp, 1)
	go func() {
		result <- ListRunningApps()
	}()
	select {
	case apps := <-result:
		if len(apps) == 0 {
			t.Fatal("expected at least one running app from a background goroutine")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("running-app listing blocked from a background goroutine")
	}
}

func TestCollectAppElements_Safari(t *testing.T) {
	// This test requires Safari to be running with at least one window.
	elements := CollectAppElements("Safari")
	if elements == nil {
		t.Skip("Safari not running or no windows open")
	}
	t.Logf("Collected %d elements from Safari", len(elements))

	// Print first 20 interesting elements.
	count := 0
	for _, el := range elements {
		if el.Label == "" && el.Title == "" && el.Value == "" {
			continue
		}
		text := el.Label
		if text == "" {
			text = el.Title
		}
		if text == "" {
			text = el.Value
		}
		if len(text) > 100 {
			text = text[:100] + "..."
		}
		t.Logf("  [%s] %s: %s (actionable=%v)", el.ID, el.Role, text, el.IsActionable)
		count++
		if count >= 20 {
			break
		}
	}
}

func TestCollectAppElements_JSON(t *testing.T) {
	elements := CollectAppElements("Safari")
	if elements == nil {
		t.Skip("Safari not running")
	}
	// Verify JSON serialization works.
	data, err := json.Marshal(map[string]any{
		"success": true,
		"data": map[string]any{
			"element_count": len(elements),
			"ui_elements":   elements,
		},
	})
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}
	t.Logf("JSON output: %d bytes", len(data))
	// Print a snippet.
	if len(data) > 500 {
		t.Logf("First 500 chars: %s", string(data[:500]))
	} else {
		t.Logf("Full JSON: %s", string(data))
	}
}

func TestListAppWindows(t *testing.T) {
	windows := ListAppWindows("Safari")
	if windows == nil {
		t.Skip("Safari not running")
	}
	t.Logf("Found %d Safari windows:", len(windows))
	for _, w := range windows {
		t.Logf("  %q at (%.0f,%.0f) %.0fx%.0f main=%v", w.Title, w.X, w.Y, w.Width, w.Height, w.IsMain)
	}
}

func TestOpenURL(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in short mode (opens browser)")
	}
	skipUnlessBrowserLaunchEnabled(t)
	// This will actually open a browser tab.
	ok := OpenURL("https://example.com")
	if !ok {
		t.Fatal("OpenURL returned false")
	}
	fmt.Println("Opened https://example.com in default browser")
}
