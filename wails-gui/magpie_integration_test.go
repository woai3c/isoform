package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMagpieSidecarIntegration runs the real magpie binary in a temporary
// state directory. It is skipped unless YANSU_MAGPIE_BIN names the binary.
//
// The final request goes through the user's own subscriptions (consent is
// accepted for the test), so it costs a few tokens of allowance. It asks for
// at most 16 output tokens.
func TestMagpieSidecarIntegration(t *testing.T) {
	bin := strings.TrimSpace(os.Getenv(magpieOverrideEnv))
	if bin == "" {
		t.Skipf("set %s to the magpie binary to run the integration test", magpieOverrideEnv)
	}
	if !isExecutableFile(bin) {
		t.Fatalf("%s=%q is not an executable file", magpieOverrideEnv, bin)
	}

	// HOME stays real: magpie finds the user's Claude Code and Codex sign-ins
	// under it. Only the sidecar state (XDG config and cache) is isolated.
	// TestMain moved HOME aside and cleared the config pointers for this
	// run, so restore them from what TestMain captured first.
	t.Setenv("HOME", testRealHomeDir)
	if testRealClaudeConfigDir != "" {
		t.Setenv("CLAUDE_CONFIG_DIR", testRealClaudeConfigDir)
	}
	if testRealCodexHome != "" {
		t.Setenv("CODEX_HOME", testRealCodexHome)
	}
	s := newMagpieSidecar(filepath.Join(t.TempDir(), configDirName, magpieStateDirName))
	s.binaryPath = bin
	s.startDeadline = 30 * time.Second
	t.Cleanup(s.Stop)
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		if data, err := os.ReadFile(s.logPath); err == nil {
			t.Logf("magpie.log:\n%s", data)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	snap := s.Status()
	if !snap.Healthy || snap.Version == "" {
		t.Fatalf("sidecar status after Start = %+v", snap)
	}

	// Kill the real gateway and expect the supervisor to bring it back within
	// five seconds on the same port.
	first := s.childProcessID()
	port := s.Status().Port
	process, err := os.FindProcess(first)
	if err != nil {
		t.Fatalf("FindProcess(%d): %v", first, err)
	}
	if err := process.Kill(); err != nil {
		t.Fatalf("kill magpie: %v", err)
	}
	killedAt := time.Now()
	waitFor(t, 5*time.Second, "magpie back after kill", func() bool {
		pid := s.childProcessID()
		return pid != 0 && pid != first && s.Healthy()
	})
	t.Logf("magpie restored in %s", time.Since(killedAt).Round(time.Millisecond))
	if got := s.Status().Port; got != port {
		t.Fatalf("port changed across restart: %d -> %d", port, got)
	}

	accounts, err := s.runCLI(ctx, "accounts", "--json")
	if err != nil {
		t.Fatalf("accounts --json: %v", err)
	}
	if !json.Valid(accounts) {
		t.Fatalf("accounts --json did not print JSON: %q", accounts)
	}

	if err := saveMagpieConsent(s.stateDir, MagpieConsent{Version: magpieConsentVersion, AcceptedAt: time.Now()}); err != nil {
		t.Fatalf("save consent: %v", err)
	}
	state, err := s.Bootstrap(ctx, magpieBootstrapOptions{ConsentAccepted: true, OllamaBaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	groups := plansByID(state.Groups)
	for _, id := range []string{"yansu-chat", "yansu-background", "yansu-light"} {
		if _, ok := groups[id]; !ok {
			t.Fatalf("bootstrap did not plan %s", id)
		}
	}
	if len(groups["yansu-light"].Members) == 0 {
		catalog, _ := s.listModels(ctx)
		t.Fatalf("yansu-light has no members; sign in to a subscription or add a key before running this test (plans=%+v catalog=%d accounts=%s)", state.Groups, len(catalog), accounts)
	}

	body := []byte(`{"model":"group/yansu-light","max_tokens":16,"messages":[{"role":"user","content":"Reply with the single word: pong"}]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.BaseURL()+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("x-api-key", magpieLocalAPIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/messages: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/messages = %d: %s", resp.StatusCode, raw)
	}
	if resp.Header.Get("X-Magpie-Provider") == "" {
		t.Errorf("response has no X-Magpie-Provider header")
	}
	var reply struct {
		Type    string `json:"type"`
		Content []struct {
			Type string `json:"type"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		t.Fatalf("decode reply: %v: %s", err, raw)
	}
	if reply.Type != "message" || len(reply.Content) == 0 {
		t.Fatalf("unexpected reply: %s", raw)
	}
}
