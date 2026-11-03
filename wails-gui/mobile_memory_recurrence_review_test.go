package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMobileMemoryReviewPhoneCorrectionStopsRecurrence(t *testing.T) {
	for _, writer := range []string{"activity", "generator"} {
		t.Run(writer, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			bridge, brief := mobileTestFeedbackBridge(t)
			dir, err := memoryDir()
			if err != nil {
				t.Fatal(err)
			}
			const point = "Prefers quiet workspaces for focused writing."
			const title = "Quiet places help with writing"
			content := "# " + title + "\n\n- **Source**: activity\n- **Dimension**: user_insight\n- **Activity Session**: old-session\n\n## Memory Points\n\n- " + point + "\n"
			if err := os.WriteFile(filepath.Join(dir, "mem-1.md"), []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			bridge.app.memoryCanonicalizer = func(prompt, model string) (string, error) {
				calls++
				return "", errors.New("offline")
			}
			path := mobileTestFeedbackPath(brief, mobileTestItemKey(t, brief, "memory"))
			device := mobileTestDevice(mobileScopeBriefFeedback, mobileScopeMemoryFix)
			request := `{"idempotencyKey":"recurrence-review","action":"not_right","reason":"  Not my preference  "}`
			for attempt := 0; attempt < 2; attempt++ {
				response := mobileTestChatCall(bridge, device, http.MethodPost, path, request)
				if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"effect":"memory_removed"`) {
					t.Fatalf("phone correction failed: %d %s", response.Code, response.Body.String())
				}
			}
			if calls != 0 {
				t.Errorf("offline phone correction called model %d times", calls)
			}
			corrections, err := LoadRecentCorrections(dir, 50)
			if err != nil || len(corrections) != 1 {
				t.Errorf("phone rejection missing or duplicated in correction gate: %+v, %v", corrections, err)
			} else {
				record := corrections[0]
				if record.Kind != CorrectionKindReject || record.MemoryID != "mem-1" || record.Title != title || record.Source != "activity" || record.Dimension != "user_insight" || record.Reason != "Not my preference" || record.OccurredAt <= 0 || len(record.SessionIDs) != 1 || record.SessionIDs[0] != "old-session" {
					t.Errorf("phone correction lost legacy rejection metadata: %+v", record)
				}
				if FindMatchingCorrection(corrections, title, []string{point}) == nil {
					t.Error("phone rejection does not reach the existing correction gate")
				}
			}
			memory := &mcMemoryGenerationResponse{Action: "create", Title: "Newly observed working habits", Dimension: "user_insight", MemoryPoints: []string{point, "Uses a standing desk while reading."}}
			var saved string
			switch writer {
			case "activity":
				session := ActivitySessionRow{ID: "new-activity-session"}
				triager := &ActivityTriager{app: bridge.app}
				saved, err = triager.saveActivityMemoryContext(context.Background(), session, sessionGroup{Sessions: []ActivitySessionRow{session}}, memory)
			case "generator":
				generator := &MemoryGenerator{app: bridge.app}
				saved, err = generator.saveMemoryFileContext(context.Background(), memorySourceSlack, "new-conversation", memory)
			}
			if !errors.Is(err, errMemoryForgotten) || saved != "" {
				t.Errorf("phone-rejected point returned through %s under a new title/source: saved=%q err=%v", writer, saved, err)
			}
			if calls != 0 {
				t.Errorf("local recurrence suppression called semantic model %d times", calls)
			}
			if forgotten, err := isMemoryForgotten(dir, "new-source", content, time.Now().Add(31*24*time.Hour)); err != nil || forgotten {
				t.Errorf("phone recurrence marker did not expire: forgotten=%v err=%v", forgotten, err)
			}
		})
	}
}

func TestMobileMemoryReviewPhoneCorrectionLogFailureStillRemoves(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bridge, brief := mobileTestFeedbackBridge(t)
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mem-1.md"), []byte("# Work preference\n\n## Memory Points\n\n- Prefers quiet workspaces for focused writing."), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, memoryRejectionsLogName), 0700); err != nil {
		t.Fatal(err)
	}
	response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeBriefFeedback, mobileScopeMemoryFix), http.MethodPost,
		mobileTestFeedbackPath(brief, mobileTestItemKey(t, brief, "memory")),
		`{"idempotencyKey":"log-failure-review","action":"not_right","reason":"Not me"}`)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"effect":"memory_removed"`) {
		t.Errorf("completed deletion reported as failed: %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "mem-1.md")); !os.IsNotExist(err) {
		t.Errorf("selected memory remains: %v", err)
	}
	markers, err := loadMemoryTombstones(dir)
	if err != nil || len(markers) != 1 {
		t.Errorf("failed audit append lost recurrence protection: %+v, %v", markers, err)
	}
}
