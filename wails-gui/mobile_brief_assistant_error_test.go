package main

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"testing"

	"yansu-agent/agent"
	"yansu-agent/agent/native"
)

func TestMobileBriefAssistantFailureAttribution(t *testing.T) {
	for _, tc := range []struct {
		backend string
		status  int
		want    string
	}{
		{"claude-code", 401, "claude-code"},
		{"codex", 403, "codex"},
		{"native", 401, ""},
		{"", 401, ""},
		{"codex", 500, ""},
	} {
		t.Run(tc.backend+"/"+strconv.Itoa(tc.status), func(t *testing.T) {
			cause := &native.HTTPError{Status: tc.status}
			err := mobileBriefAttributeAssistantError(cause, tc.backend)
			var attributed *mobileBriefAssistantError
			if errors.As(err, &attributed) != (tc.want != "") {
				t.Fatalf("attribution = %v", err)
			}
			if attributed != nil && attributed.assistant != tc.want {
				t.Fatalf("assistant = %q, want %q", attributed.assistant, tc.want)
			}
			if !errors.Is(err, cause) {
				t.Fatal("lost the original error")
			}
		})
	}
}

func TestMobileBriefAssistantFailureSurvivesWriterAndRunStore(t *testing.T) {
	cause := mobileBriefAttributeAssistantError(&native.HTTPError{Status: 401}, "codex")
	writes := 0
	_, _, err := writeAndJudgeMorningBrief(context.Background(), morningBriefJudgeInput{}, nil,
		func(context.Context, string) (morningBriefDraft, error) {
			writes++
			return morningBriefDraft{}, cause
		}, nil)
	if !errors.Is(err, cause) || writes != 1 {
		t.Fatalf("writer calls=%d error=%v", writes, err)
	}
	clock := &fakeMobileBriefClock{now: mobileBriefTestDay(7, 0)}
	svc, _ := missedBriefRig(t, clock)
	occurrence := mobileBriefSlotOccurrence{BriefID: "2026-10-09-morning", Slot: mobileBriefSlotMorning, ScheduledAt: clock.Now()}
	svc.recordFailure(occurrence, withLogStage("write", err))
	runs := newMobileBriefRunStore(filepath.Dir(svc.runs.path))
	stored := runs.list()
	if len(stored) != 1 || stored[0].FailureCause != "signed_out" || stored[0].FailureAssistant != "codex" {
		t.Fatalf("stored runs = %+v", stored)
	}
	if _, err := runs.record(mobileBriefRun{BriefID: occurrence.BriefID, Status: mobileBriefRunMissed}); err != nil {
		t.Fatal(err)
	}
	if got := runs.list()[0].FailureAssistant; got != "codex" {
		t.Fatalf("missed run lost assistant: %q", got)
	}
}

type briefRouteErrorRunner struct {
	backends []string
}

func (*briefRouteErrorRunner) Init(context.Context) error { return nil }
func (*briefRouteErrorRunner) IsReady() bool              { return true }
func (r *briefRouteErrorRunner) ExecuteTask(_ context.Context, _ *agent.TaskInput, emit agent.StreamCallback) (string, error) {
	for _, backend := range r.backends {
		if err := emit(&agent.StreamEvent{Type: "backend_route", Backend: backend}); err != nil {
			return "", err
		}
	}
	return "", &native.HTTPError{Status: 401}
}
func (*briefRouteErrorRunner) CancelRun(string) error                   { return nil }
func (*briefRouteErrorRunner) RespondToPermission(string, string) error { return nil }
func (*briefRouteErrorRunner) CancelPermission(string)                  {}
func (*briefRouteErrorRunner) Shutdown() error                          { return nil }

func TestMobileBriefAssistantFailureUsesLastRoute(t *testing.T) {
	for _, tc := range []struct {
		name     string
		backends []string
		want     string
	}{
		{"assistant", []string{"codex"}, "codex"},
		{"fallback", []string{"codex", "native"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := &App{runner: &briefRouteErrorRunner{backends: tc.backends}}
			_, err := app.mobileBriefModelCall(context.Background(), "test")
			var attributed *mobileBriefAssistantError
			if errors.As(err, &attributed) != (tc.want != "") {
				t.Fatalf("error = %v", err)
			}
			if attributed != nil && attributed.assistant != tc.want {
				t.Fatalf("assistant = %q", attributed.assistant)
			}
		})
	}
}
