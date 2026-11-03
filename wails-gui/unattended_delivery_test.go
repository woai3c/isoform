package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wails-gui/storage"
)

// A model-written <delivery_target> counts for an unattended run only when it
// is the destination the job was configured with: its notify platform, as a
// direct message.
func TestUnattendedDeliveryTargetMustMatchTheJobDestination(t *testing.T) {
	slackJob := &storage.CronJobRow{NotifyDM: true, NotifyPlatform: "slack"}
	for _, tc := range []struct {
		name   string
		job    *storage.CronJobRow
		target *DeliveryTarget
		keep   bool
	}{
		{"configured platform", slackJob, &DeliveryTarget{Platform: "slack"}, true},
		{"other platform", slackJob, &DeliveryTarget{Platform: "telegram"}, false},
		{"channel the job never named", slackJob, &DeliveryTarget{Platform: "slack", Channel: "general"}, false},
		{"server the job never named", slackJob, &DeliveryTarget{Platform: "slack", Server: "Acme"}, false},
		{"job notifications off", &storage.CronJobRow{NotifyDM: false, NotifyPlatform: "slack"}, &DeliveryTarget{Platform: "slack"}, false},
		{"job platform none", &storage.CronJobRow{NotifyDM: true, NotifyPlatform: "none"}, &DeliveryTarget{Platform: "slack"}, false},
		{"no target", slackJob, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := unattendedDeliveryTarget(tc.target, tc.job)
			if (got != nil) != tc.keep {
				t.Fatalf("kept = %v, want %v", got != nil, tc.keep)
			}
		})
	}

	// The same rule holds for a target the model wrote into its summary.
	_, parsed := parseDeliveryTarget("done\n<delivery_target>\nplatform: telegram\n</delivery_target>")
	if parsed == nil || parsed.Platform != "telegram" {
		t.Fatalf("parsed target = %+v, want telegram", parsed)
	}
	if got := unattendedDeliveryTarget(parsed, slackJob); got != nil {
		t.Fatalf("a telegram target written by the model was kept for a slack job: %+v", got)
	}
}

// The notify path checks the result's destination with the authority policy
// before anything is sent. Only the job's configured destination is allowed.
func TestScheduledDeliveryAuthorityOnlyAllowsTheJobDestination(t *testing.T) {
	store := newTestStore(t)
	app := &App{activity: &activityMonitorState{store: store}}

	for _, tc := range []struct {
		name     string
		platform string
		sendDM   bool
		target   *DeliveryTarget
		allow    bool
	}{
		{"no target uses the job's own destination", "slack", true, nil, true},
		{"the configured platform as a direct message", "slack", true, &DeliveryTarget{Platform: "slack"}, true},
		{"another platform", "slack", true, &DeliveryTarget{Platform: "telegram"}, false},
		{"a channel on the configured platform", "slack", true, &DeliveryTarget{Platform: "slack", Channel: "general"}, false},
		{"a server on the configured platform", "discord", true, &DeliveryTarget{Platform: "discord", Server: "Acme"}, false},
		{"notifications off for the job", "slack", false, &DeliveryTarget{Platform: "slack"}, false},
		{"job platform none", "none", true, &DeliveryTarget{Platform: "slack"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := app.authorizeScheduledDelivery(tc.platform, tc.sendDM, tc.target)
			if (err == nil) != tc.allow {
				t.Fatalf("authorizeScheduledDelivery = %v, want allow=%v", err, tc.allow)
			}
		})
	}

	// Each refusal is recorded in the authority ledger, not dropped silently.
	items, err := store.ListAuthorityDecisions("", 50)
	if err != nil {
		t.Fatal(err)
	}
	denied := 0
	for _, item := range items {
		if strings.HasSuffix(item.Evaluation.Action.Capability, ".message.deliver") && item.Evaluation.Decision == "deny" {
			denied++
		}
	}
	if denied != 5 {
		t.Fatalf("recorded deliver refusals = %d, want 5", denied)
	}
}

// Tool tracking counts a Read as well as a write, and nothing else.
func TestTouchedDeliverablePathCountsReadsAndWrites(t *testing.T) {
	workDir := t.TempDir()
	report := filepath.Join(workDir, "report.md")
	if err := os.WriteFile(report, []byte("# r"), 0o644); err != nil {
		t.Fatal(err)
	}
	resolved, err := resolvedExistingPath(report)
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"path": "report.md"}
	for _, tool := range []string{"Read", "Write", "Edit", "NotebookEdit"} {
		if got := touchedDeliverablePath(tool, input, workDir); got != resolved {
			t.Errorf("%s: touched path = %q, want %q", tool, got, resolved)
		}
	}
	for _, tool := range []string{"Bash", "Grep", "Glob", "WebFetch"} {
		if got := touchedDeliverablePath(tool, input, workDir); got != "" {
			t.Errorf("%s: touched path = %q, want none", tool, got)
		}
	}
}

// A file the run read is uploadable even inside the work folder; a file in the
// work folder that the run never touched is not.
func TestUploadNeedsTheRunToHaveReadOrWrittenTheFile(t *testing.T) {
	workDir := t.TempDir()
	read := filepath.Join(workDir, "input.csv")
	stale := filepath.Join(workDir, "old-export.csv")
	for _, p := range []string{read, stale} {
		if err := os.WriteFile(p, []byte("a,b"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	touched := touchedBy(t, read)
	files, skipped := readDeliverableFiles([]string{read, stale}, workDir, "", touched)
	if len(files) != 1 || files[0].Filename != "input.csv" {
		t.Fatalf("uploaded %+v, want only the file the run read", files)
	}
	if len(skipped) != 1 || !strings.Contains(skipped[0], "old-export.csv") {
		t.Fatalf("skipped = %v, want the untouched file", skipped)
	}
}
