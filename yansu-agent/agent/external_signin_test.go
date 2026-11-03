package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// writeFakeCLI writes an executable POSIX shell script that runs script and
// returns its path. The sign-in probe runs the path as the CLI binary.
func writeFakeCLI(t *testing.T, name, script string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI scripts need a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExternalAgentSignedInClaude(t *testing.T) {
	cases := []struct {
		name    string
		script  string
		want    bool
		wantErr bool
	}{
		{"loggedIn true", `echo '{"loggedIn": true, "authMethod": "claude.ai"}'`, true, false},
		{"API key login is not subscription", `echo '{"loggedIn": true, "authMethod": "api_key"}'`, false, false},
		{"unknown login method", `echo '{"loggedIn": true}'`, false, false},
		{"loggedIn false exits non-zero", `echo '{"loggedIn": false}'; exit 1`, false, false},
		{"unparseable output with non-zero exit is an error", `echo 'usage: claude auth'; exit 2`, false, true},
		{"unparseable output with zero exit is an error", `echo 'not json'`, false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bin := writeFakeCLI(t, "claude", c.script)
			got, err := ExternalAgentSignedIn(ExternalAgentConfig{Format: "claude-code", Command: bin})
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if got != c.want {
				t.Fatalf("signed in = %v, want %v", got, c.want)
			}
		})
	}
}

func TestExternalAgentSignedInCodex(t *testing.T) {
	cases := []struct {
		name    string
		script  string
		want    bool
		wantErr bool
	}{
		{"ChatGPT sign-in", `echo "Logged in using ChatGPT"`, true, false},
		{"API key sign-in is not a ChatGPT sign-in", `echo "Logged in using an API key"`, false, false},
		{"signed out exits non-zero", `echo "Not logged in"; exit 1`, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bin := writeFakeCLI(t, "codex", c.script)
			got, err := ExternalAgentSignedIn(ExternalAgentConfig{Format: "codex", Command: bin})
			if (err != nil) != c.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, c.wantErr)
			}
			if got != c.want {
				t.Fatalf("signed in = %v, want %v", got, c.want)
			}
		})
	}
}

func TestExternalAgentSignedInMissingBinaryIsError(t *testing.T) {
	got, err := ExternalAgentSignedIn(ExternalAgentConfig{
		Format:  "claude-code",
		Command: filepath.Join(t.TempDir(), "no-such-claude"),
	})
	if err == nil {
		t.Fatal("expected an error for a missing binary")
	}
	if got {
		t.Fatal("missing binary reported as signed in")
	}
}

func TestExternalAgentSignedInTimesOut(t *testing.T) {
	prev := externalSignInTimeout
	externalSignInTimeout = 200 * time.Millisecond
	t.Cleanup(func() { externalSignInTimeout = prev })

	// exec replaces the shell, so the timeout kills the process that holds
	// the output pipe.
	bin := writeFakeCLI(t, "claude", `exec sleep 5`)
	start := time.Now()
	got, err := ExternalAgentSignedIn(ExternalAgentConfig{Format: "claude-code", Command: bin})
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if got {
		t.Fatal("timed-out probe reported as signed in")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("probe took %s, expected the 200ms timeout to end it", elapsed)
	}
}
