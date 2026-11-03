package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestBashSeatbeltHomeWriteBoundary(t *testing.T) {
	home, project := sandboxFixture(t)
	outside := filepath.Join(home, "Documents", "keep")
	sandboxWrite(t, filepath.Join(outside, "report.txt"), "original")
	alias := filepath.Join(project, "home-link")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{outside, filepath.Join(alias, "Documents", "keep"), "/system/volumes/data" + outside} {
		for _, command := range []string{
			"/bin/rm -rf " + strconv.Quote(target),
			"printf changed > " + strconv.Quote(filepath.Join(target, "report.txt")),
			"/bin/mv " + strconv.Quote(target) + " " + strconv.Quote(filepath.Join(project, "escaped")),
		} {
			// Invoke the production sandbox directly to prove kernel enforcement,
			// independently of the friendly lexical destructive-source checks.
			inv, err := NewBashTool().bashInvocation(context.Background(), ToolCall{WorkDir: project}, command)
			if err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(inv.executable, inv.args...).CombinedOutput()
			if err == nil || !strings.Contains(string(out), "Operation not permitted") {
				t.Errorf("HOME write escaped: %s: %v %s", command, err, out)
			}
			if body, err := os.ReadFile(filepath.Join(outside, "report.txt")); err != nil || string(body) != "original" {
				t.Fatalf("HOME contents changed: %q %v", body, err)
			}
		}
	}
	for _, dir := range []string{project, filepath.Join(home, "Library", "Caches", "go-build"), filepath.Join(home, "go", "pkg", "sumdb"), t.TempDir()} {
		command := "mkdir -p " + strconv.Quote(dir) + " && printf allowed > " + strconv.Quote(filepath.Join(dir, "output")) + " && rm " + strconv.Quote(filepath.Join(dir, "output"))
		result, err := NewBashTool().Execute(context.Background(), ToolCall{WorkDir: project, Input: map[string]any{"command": command}})
		if err != nil || result.Metadata["exit_code"] != 0 {
			t.Errorf("workspace/cache/temp write failed: %v %+v", err, result)
		}
	}
}

func TestBashSeatbeltProbeTimeoutFailsClosed(t *testing.T) {
	_, project := sandboxFixture(t)
	probe := filepath.Join(project, "stalled-sandbox")
	sandboxWrite(t, probe, "#!/bin/sh\nexec /bin/sleep 30\n")
	if err := os.Chmod(probe, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(project, "must-not-run")
	tool := NewBashTool()
	tool.sandboxExecutable = probe
	_, err := tool.Execute(context.Background(), ToolCall{WorkDir: project, Input: map[string]any{"command": "touch " + strconv.Quote(marker)}, Authority: &AuthorityRuntime{AllowBashWithoutApproval: true}})
	if err == nil || !strings.Contains(err.Error(), "sandbox unavailable") {
		t.Fatalf("timed out sandbox allowed shell: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("shell ran after probe timeout: %v", err)
	}
}
