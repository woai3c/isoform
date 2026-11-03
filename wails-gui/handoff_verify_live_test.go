package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/handoff"
)

// TestHandoffVerifierLive runs the one-time-work verifier against a real model
// through the magpie sidecar, the way the app does after a run ends. It is
// skipped unless YANSU_MAGPIE_BIN names the magpie binary, and it skips again
// when no subscription or key is signed in. One short call through the light
// group; each asks for at most 400 output tokens.
func TestHandoffVerifierLive(t *testing.T) {
	bin := strings.TrimSpace(os.Getenv(magpieOverrideEnv))
	if bin == "" {
		t.Skipf("set %s to the magpie binary to run the live verifier test", magpieOverrideEnv)
	}
	if !isExecutableFile(bin) {
		t.Fatalf("%s=%q is not an executable file", magpieOverrideEnv, bin)
	}
	s := newMagpieSidecar(filepath.Join(t.TempDir(), configDirName, magpieStateDirName))
	s.binaryPath = bin
	s.startDeadline = 30 * time.Second
	t.Cleanup(s.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := saveMagpieConsent(s.stateDir, MagpieConsent{Version: magpieConsentVersion, AcceptedAt: time.Now()}); err != nil {
		t.Fatalf("save consent: %v", err)
	}
	state, err := s.Bootstrap(ctx, magpieBootstrapOptions{ConsentAccepted: true, OllamaBaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if len(plansByID(state.Groups)["yansu-light"].Members) == 0 {
		t.Skip("yansu-light has no members; sign in to a subscription or add a key to run the live verifier test")
	}

	ask := func(prompt string) (string, error) {
		body, _ := json.Marshal(map[string]any{
			"model":      "group/yansu-light",
			"max_tokens": 400,
			"messages":   []map[string]string{{"role": "user", "content": prompt}},
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.BaseURL()+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("x-api-key", magpieLocalAPIKey)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("POST /v1/messages = %d: %s", resp.StatusCode, raw)
		}
		var reply struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(raw, &reply); err != nil {
			return "", err
		}
		var text strings.Builder
		for _, block := range reply.Content {
			if block.Type == "text" {
				text.WriteString(block.Text)
			}
		}
		t.Logf("verifier reply via %s/%s: %s", resp.Header.Get("X-Magpie-Provider"), resp.Header.Get("X-Magpie-Model"), text.String())
		return text.String(), nil
	}

	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, "report"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "report", "outline.md"), []byte("# Q3 outline\n\n1. Results\n2. Risks\n3. Asks\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := handoff.Entry{
		ID:      "live-1",
		Title:   "Draft the Q3 outline",
		Workdir: workdir,
		Criteria: []string{
			"The file report/outline.md exists",
		},
	}

	done := verifyHandoffOutcome(entry, handoffResultText(t, entry.Criteria, []handoff.ResultEvidence{{Path: "report/outline.md"}}), ask)
	if done.Status != handoff.StatusCompleted || done.Verification.Met != 1 || done.Verification.Total != 1 {
		t.Fatalf("a run whose evidence is on disk should complete: %+v", done)
	}

	missing := verifyHandoffOutcome(entry, handoffResultText(t, entry.Criteria, []handoff.ResultEvidence{{Path: "report/summary.md"}}), ask)
	if missing.Status != handoff.StatusNeedsReview || missing.Verification.Met >= 1 {
		t.Fatalf("a run naming a file that is not on disk must need review: %+v", missing)
	}
	t.Logf("needs-review note: %s", missing.Verification.Note)
}
