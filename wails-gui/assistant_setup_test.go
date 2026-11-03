package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/platform/shellenv"
)

// These tests never run a real Claude Code or Codex: HOME is a temporary
// folder, PATH holds only the system folders and fake CLIs written here, and
// the login shell is a stand-in that runs the command with that PATH.

// fakeCLI writes an executable `claude` or `codex` into dir. It answers
// --version, and reports signed in only while home/<name>-signed-in exists.
func fakeCLI(t *testing.T, dir, home, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	flag := filepath.Join(home, name+"-signed-in")
	script := `#!/bin/sh
case "$1" in
--version) echo "` + name + ` 9.9.9 (fake)";;
auth|login)
  if [ -f '` + flag + `' ]; then echo '{"loggedIn": true, "subscriptionType": "max"}'; echo "Logged in using ChatGPT"; exit 0; fi
  echo '{"loggedIn": false}'; echo "Not logged in"; exit 1;;
esac
`
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func signInFake(t *testing.T, home, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, name+"-signed-in"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// assistantSetupHarness isolates HOME, PATH and the login shell, and gives
// the app a probe cache in a temporary folder.
func assistantSetupHarness(t *testing.T) (*App, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	shell := filepath.Join(t.TempDir(), "fake-login-shell")
	// The stand-in login shell: `-lc CMD` runs CMD with the test PATH.
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nshift\nexec /bin/sh -c \"$1\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	app := &App{}
	app.setupAgents.init(filepath.Join(t.TempDir(), "probe.json"), probeSetupAgent, nil, nil)
	restore := []func(){}
	save := func(f func()) { restore = append(restore, f) }
	oldRun, oldNPM, oldCurl, oldRefresh, oldProbe, oldTimeout, oldGateway, oldLocate := assistantInstallRun, assistantNPMAvailable, assistantCurlAvailable, assistantRefreshPATH, assistantProbe, assistantInstallTimeout, assistantGateway, assistantLoginShellLocate
	save(func() {
		assistantInstallRun, assistantNPMAvailable, assistantCurlAvailable, assistantRefreshPATH, assistantProbe, assistantInstallTimeout, assistantGateway, assistantLoginShellLocate = oldRun, oldNPM, oldCurl, oldRefresh, oldProbe, oldTimeout, oldGateway, oldLocate
	})
	oldGatewayWait, oldGatewayRetry := assistantGatewayWait, assistantGatewayRetry
	save(func() { assistantGatewayWait, assistantGatewayRetry = oldGatewayWait, oldGatewayRetry })
	t.Cleanup(func() {
		for _, f := range restore {
			f()
		}
	})
	// The real refresh may also look in /opt/homebrew/bin and /usr/local/bin,
	// where this Mac's own CLIs live; the tests add only the fake folders.
	assistantRefreshPATH = func(string) {
		for _, dir := range []string{filepath.Join(home, ".local", "bin"), filepath.Join(home, "npm-global", "bin")} {
			shellenv.AppendPATH(dir)
		}
	}
	assistantNPMAvailable = func() bool { return true }
	assistantCurlAvailable = func() bool { return true }
	// No test may reach a real installer.
	assistantInstallRun = func(_ context.Context, command string) (string, error) {
		t.Errorf("unexpected install: %s", command)
		return "", errors.New("no installs in tests")
	}
	assistantGateway = func(*App) (string, func(context.Context) ([]string, error), func(context.Context)) {
		return "", nil, nil
	}
	return app, home
}

func TestSetupInstallFindsTheCLIInAFolderTheStartupPATHLacked(t *testing.T) {
	app, home := assistantSetupHarness(t)
	// Before the install the launch probe sees nothing.
	if row := probeSetupAgent(context.Background(), "claude"); row.Installed {
		t.Fatalf("fake PATH must start without claude: %+v", row)
	}
	assistantInstallRun = func(ctx context.Context, command string) (string, error) {
		if command != "curl -fsSL https://claude.ai/install.sh | bash" {
			t.Fatalf("install command = %q, want the native installer first", command)
		}
		fakeCLI(t, filepath.Join(home, ".local", "bin"), home, "claude")
		return "added 1 package", nil
	}
	got := app.SetupInstallAssistant("claude")
	if !got.Installed || got.SignedIn || got.Code != assistantSetupSignedOut {
		t.Fatalf("after install = %+v, want installed and signedOut", got)
	}
	if got.Command != "claude auth login" {
		t.Fatalf("next step command = %q", got.Command)
	}
	// The cached probe the screen reads moved too, so it can't show Install.
	for _, row := range app.setupAgents.snapshot().Agents {
		if row.ID == "claude" && !row.Installed {
			t.Fatalf("cache still says not installed: %+v", row)
		}
	}
}

func TestSetupInstallThatLandsNowhereSaysNotFoundWithWhereItLooked(t *testing.T) {
	app, _ := assistantSetupHarness(t)
	assistantInstallRun = func(context.Context, string) (string, error) { return "added 1 package", nil }
	assistantLoginShellLocate = func(context.Context, string) string { return "/somewhere/else" }
	got := app.SetupInstallAssistant("codex")
	if got.Installed || got.Code != assistantSetupNotFound || got.Command != codexNative || got.Detail != "/somewhere/else" {
		t.Fatalf("got %+v, want notFound with the Terminal command and where the shell looked", got)
	}
}

const (
	claudeNative = "curl -fsSL https://claude.ai/install.sh | bash"
	codexNative  = "curl -fsSL https://chatgpt.com/codex/install.sh | CODEX_NON_INTERACTIVE=1 sh"
)

func TestSetupInstallFailuresEachNameTheirNextStep(t *testing.T) {
	// npm is missing here, so the native installer is the only route and its
	// failure is what the card shows, with the native Terminal command.
	cases := []struct {
		name string
		run  func(ctx context.Context) (string, error)
		want string
	}{
		{"permission", func(context.Context) (string, error) {
			return "mkdir: /Users/x/.local/bin: Permission denied", errors.New("exit status 1")
		}, assistantSetupInstallDenied},
		{"timeout", func(ctx context.Context) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		}, assistantSetupInstallTimeout},
		{"other", func(context.Context) (string, error) {
			return "curl: (6) Could not resolve host: claude.ai", errors.New("exit status 6")
		}, assistantSetupInstallFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, _ := assistantSetupHarness(t)
			assistantNPMAvailable = func() bool { return false }
			assistantInstallTimeout = 20 * time.Millisecond
			var ran []string
			assistantInstallRun = func(ctx context.Context, command string) (string, error) {
				ran = append(ran, command)
				return tc.run(ctx)
			}
			got := app.SetupInstallAssistant("claude")
			if got.Code != tc.want || got.Command != claudeNative || got.Detail == "" {
				t.Fatalf("got %+v, want %q with the native command and the real error", got, tc.want)
			}
			if len(ran) != 1 || ran[0] != claudeNative {
				t.Fatalf("ran %v, want only the native installer", ran)
			}
		})
	}
}

func TestSetupInstallFallsBackToNPMWhenTheNativeInstallerFails(t *testing.T) {
	app, home := assistantSetupHarness(t)
	var ran []string
	assistantInstallRun = func(_ context.Context, command string) (string, error) {
		ran = append(ran, command)
		if command == codexNative {
			return "curl: (22) The requested URL returned error: 403", errors.New("exit status 22")
		}
		fakeCLI(t, filepath.Join(home, "npm-global", "bin"), home, "codex")
		return "added 1 package", nil
	}
	got := app.SetupInstallAssistant("codex")
	if !got.Installed || got.Code != assistantSetupSignedOut {
		t.Fatalf("got %+v, want installed through npm", got)
	}
	if len(ran) != 2 || ran[0] != codexNative || ran[1] != "npm install -g @openai/codex" {
		t.Fatalf("ran %v, want native then npm", ran)
	}
}

func TestSetupInstallNeedsNodeOnlyWhenNoRouteIsLeft(t *testing.T) {
	app, _ := assistantSetupHarness(t)
	assistantCurlAvailable = func() bool { return false }
	assistantNPMAvailable = func() bool { return false }
	if got := app.SetupInstallAssistant("claude"); got.Code != assistantSetupNeedsNode {
		t.Fatalf("got %+v, want needsNode", got)
	}
	// With npm but no curl, npm runs instead of asking for Node.
	assistantNPMAvailable = func() bool { return true }
	var ran []string
	assistantInstallRun = func(_ context.Context, command string) (string, error) {
		ran = append(ran, command)
		return "npm error network", errors.New("exit status 1")
	}
	got := app.SetupInstallAssistant("claude")
	if got.Code != assistantSetupInstallFailed || len(ran) != 1 || ran[0] != "npm install -g @anthropic-ai/claude-code" {
		t.Fatalf("got %+v after %v", got, ran)
	}
}

func TestSetupCheckPicksUpASignInFinishedInTerminal(t *testing.T) {
	app, home := assistantSetupHarness(t)
	fakeCLI(t, filepath.Join(home, ".local", "bin"), home, "codex")
	if got := app.SetupCheckAssistant("codex", false); got.Code != assistantSetupSignedOut || !got.Installed {
		t.Fatalf("before sign-in = %+v", got)
	}
	signInFake(t, home, "codex")
	got := app.SetupCheckAssistant("codex", false)
	if got.Code != "" || !got.SignedIn || got.Tested {
		t.Fatalf("after sign-in = %+v", got)
	}
}

// fakeGateway serves /v1/models and /v1/messages like the magpie sidecar.
func fakeGateway(t *testing.T, models []string, status int) (*httptest.Server, *[]string) {
	t.Helper()
	asked := &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Model     string `json:"model"`
			MaxTokens int    `json:"max_tokens"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.MaxTokens != 1 {
			t.Errorf("test call max_tokens = %d, want 1", body.MaxTokens)
		}
		*asked = append(*asked, body.Model)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"type":"error"}`))
	}))
	t.Cleanup(srv.Close)
	assistantGateway = func(*App) (string, func(context.Context) ([]string, error), func(context.Context)) {
		return srv.URL, func(context.Context) ([]string, error) { return models, nil }, nil
	}
	return srv, asked
}

func TestSetupCheckTestCallThroughTheGateway(t *testing.T) {
	cases := []struct {
		status  int
		code    string
		working bool
	}{
		{http.StatusOK, "", true},
		{http.StatusUnauthorized, assistantSetupTestSignedOut, false},
		{http.StatusTooManyRequests, assistantSetupTestLimit, true},
		{http.StatusBadGateway, assistantSetupTestFailed, false},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			app, home := assistantSetupHarness(t)
			fakeCLI(t, filepath.Join(home, ".local", "bin"), home, "claude")
			signInFake(t, home, "claude")
			_, asked := fakeGateway(t, []string{"codex/gpt-5.6-sol", "claude/claude-sonnet-5-5", "claude/claude-haiku-5-5"}, tc.status)
			got := app.SetupCheckAssistant("claude", true)
			if got.Code != tc.code || got.Working != tc.working || !got.Tested {
				t.Fatalf("got %+v, want code %q working %t", got, tc.code, tc.working)
			}
			if len(*asked) != 1 || (*asked)[0] != "claude/claude-haiku-5-5" {
				t.Fatalf("test call went to %v, want the light claude model once", *asked)
			}
			if tc.code != "" && !strings.Contains(got.Detail, "HTTP") {
				t.Fatalf("detail = %q, want the real status", got.Detail)
			}
		})
	}
}

func TestSetupCheckReloadsTheGatewayForANewSignIn(t *testing.T) {
	app, home := assistantSetupHarness(t)
	fakeCLI(t, filepath.Join(home, ".local", "bin"), home, "codex")
	signInFake(t, home, "codex")
	srv, asked := fakeGateway(t, nil, http.StatusOK)
	catalog := []string{"claude/claude-haiku-5-5"}
	reloaded := false
	assistantGateway = func(*App) (string, func(context.Context) ([]string, error), func(context.Context)) {
		return srv.URL, func(context.Context) ([]string, error) { return catalog, nil }, func(context.Context) {
			reloaded = true
			catalog = append(catalog, "codex/gpt-5.6-sol", "codex/gpt-5.6-luna")
		}
	}
	got := app.SetupCheckAssistant("codex", true)
	if !reloaded || got.Code != "" || !got.Working {
		t.Fatalf("got %+v reloaded=%t, want a reload and a working codex", got, reloaded)
	}
	if len(*asked) != 1 || (*asked)[0] != "codex/gpt-5.6-luna" {
		t.Fatalf("test call went to %v", *asked)
	}
}

func TestSetupCheckWithoutTheGatewaySaysSo(t *testing.T) {
	app, home := assistantSetupHarness(t)
	assistantGatewayWait = 20 * time.Millisecond
	assistantGatewayRetry = time.Millisecond
	fakeCLI(t, filepath.Join(home, ".local", "bin"), home, "claude")
	signInFake(t, home, "claude")
	if got := app.SetupCheckAssistant("claude", true); got.Code != assistantSetupGatewayDown || got.Working {
		t.Fatalf("got %+v, want gatewayDown", got)
	}
}

func TestSetupCheckWaitsForGatewayDuringLaunch(t *testing.T) {
	app, home := assistantSetupHarness(t)
	fakeCLI(t, filepath.Join(home, ".local", "bin"), home, "claude")
	signInFake(t, home, "claude")
	srv, asked := fakeGateway(t, []string{"claude/claude-haiku-5-5"}, http.StatusOK)
	ready := make(chan struct{})
	calls := 0
	assistantGateway = func(*App) (string, func(context.Context) ([]string, error), func(context.Context)) {
		calls++
		if calls == 1 {
			timer := time.AfterFunc(40*time.Millisecond, func() { close(ready) })
			t.Cleanup(func() { timer.Stop() })
		}
		select {
		case <-ready:
			return srv.URL, func(context.Context) ([]string, error) {
				return []string{"claude/claude-haiku-5-5"}, nil
			}, nil
		default:
			return "", nil, nil
		}
	}
	got := app.SetupCheckAssistant("claude", true)
	if got.Code != "" || !got.Working || calls < 2 {
		t.Fatalf("got %+v after %d gateway reads, want the delayed gateway to answer", got, calls)
	}
	if len(*asked) != 1 || (*asked)[0] != "claude/claude-haiku-5-5" {
		t.Fatalf("test call went to %v, want one call after the gateway became healthy", *asked)
	}
}

func TestSetupProbeBudgetFitsASlowFirstRun(t *testing.T) {
	// The background probe once gave each CLI two seconds for --version and
	// its sign-in status together; a node CLI's first run after an install
	// can take longer, and the card stayed on Install.
	if setupAgentProbeTimeout < 5*time.Second || assistantCheckTimeout < setupAgentProbeTimeout {
		t.Fatalf("probe budgets too small: background %v, setup check %v", setupAgentProbeTimeout, assistantCheckTimeout)
	}
}

func TestRefreshAssistantPATHAddsTheNativeInstallFolders(t *testing.T) {
	_, home := assistantSetupHarness(t)
	assistantRefreshPATH = refreshAssistantPATH
	local := filepath.Join(home, ".local", "bin")
	fakeCLI(t, local, home, "claude")
	refreshAssistantPATH("claude")
	entries := filepath.SplitList(os.Getenv("PATH"))
	at := -1
	for i, entry := range entries {
		if entry == local {
			at = i
			break
		}
	}
	if at < 0 {
		t.Fatalf("PATH %q lacks %s", os.Getenv("PATH"), local)
	}
	for _, entry := range entries[:at] {
		if entry == "/opt/homebrew/bin" || entry == "/usr/local/bin" {
			t.Fatalf("PATH %q puts %s before the fresh install", os.Getenv("PATH"), entry)
		}
	}
	if entries[0] != "/usr/bin" {
		t.Fatalf("refresh must keep the existing entries first: %q", os.Getenv("PATH"))
	}
}

func TestOpenAssistantInstallTerminalRunsTheExactCommand(t *testing.T) {
	assistantSetupHarness(t)
	old := openSignInTerminal
	t.Cleanup(func() { openSignInTerminal = old })
	var script string
	openSignInTerminal = func(path string) error { script = path; return nil }
	app := &App{}
	if err := app.OpenAssistantInstallTerminal("codex"); err != nil {
		if strings.Contains(err.Error(), "open a terminal") {
			t.Skip("Terminal scripts are macOS only")
		}
		t.Fatal(err)
	}
	raw, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `'/bin/sh' '-c' '`+codexNative+`'`) {
		t.Fatalf("script = %q", raw)
	}
}
