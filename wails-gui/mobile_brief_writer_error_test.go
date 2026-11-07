package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"yansu-agent/agent/native"
)

// The writer's log names the failure's class and nothing else (design R74, log review).
func TestMobileBriefWriterErrorClassNamesTheCauseOnly(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"timeout", context.DeadlineExceeded, "timeout"},
		{"canceled", context.Canceled, "canceled"},
		{"sources changed", errMobileBriefSourcesChanged, "sources_changed"},
		{"assistant signed out", &mobileBriefAssistantError{assistant: "claude-code", err: &native.HTTPError{Status: 401}}, "signed_out"},
		{"unattributed unauthorized", &native.HTTPError{Status: 401}, "auth_rejected"},
		{"forbidden", fmt.Errorf("call: %w", &native.HTTPError{Status: 403}), "auth_rejected"},
		{"rate limited", &native.HTTPError{Status: 429}, "rate_limited"},
		{"server", &native.HTTPError{Status: 503}, "http_503"},
		{"unknown", errors.New("something else"), "other"},
	}
	for _, tc := range cases {
		if got := mobileBriefWriterErrorClass(tc.err); got != tc.want {
			t.Fatalf("%s: class = %q, want %q", tc.name, got, tc.want)
		}
	}
	if got := mobileBriefWriterErrorClass(nil); got != "" {
		t.Fatalf("nil error class = %q", got)
	}
}

// The narrative writer logs the class of a failed model call: no prompt, reply,
// provider body or token reaches the log.
func TestNarrativeWriterFailureLogsOnlyTheClass(t *testing.T) {
	logged := captureMobileBriefLog(t)
	brief := mobileBriefVoiceFixture()
	brief.Slot = "evening"
	call := func(string) (string, error) {
		return "", fmt.Errorf("request failed PROMPT_SECRET_TEXT: %w", &native.HTTPError{
			Status: 401,
			Body:   `{"error":"invalid x-api-key sk-ant-api03-SECRETTOKEN"}`,
		})
	}
	_, _, _, _, check := writeMobileBriefNarrativeWithWeek(context.Background(), brief, nil, "prompt", "English", nil, call)
	if check != "model_call" {
		t.Fatalf("check = %q, want model_call", check)
	}
	line := logged.String()
	if !strings.Contains(line, "[mobile-brief] evening writer failed: auth_rejected") {
		t.Fatalf("log = %q, want the evening writer's class", line)
	}
	for _, secret := range []string{"PROMPT_SECRET_TEXT", "sk-ant-api03-SECRETTOKEN", "invalid x-api-key"} {
		if strings.Contains(line, secret) {
			t.Fatalf("log carries %q: %q", secret, line)
		}
	}
}
