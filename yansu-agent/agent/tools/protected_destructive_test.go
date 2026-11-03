package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestDestructiveHomeSourcesAreHardBlocked(t *testing.T) {
	home, project := hardBlockHome(t)
	alias := filepath.Join(project, "home-alias")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	destination := strconv.Quote(filepath.Join(project, "moved-home"))
	commands := []string{
		"rm -rf " + strconv.Quote(home),
		"rm -rf ~/.",
		`rm -rf "$HOME/."`,
		"rm -rf " + strconv.Quote(home+"/project/.."),
		"rm -rf " + strconv.Quote(filepath.Dir(home)),
		"/bin/rm -rf -- " + strconv.Quote(home),
		"env rm -rf ~/.",
		"cd ~; rm -rf .",
		"cd ~/project; rm -rf ..",
		"rm -rf " + strconv.Quote(alias+"/."),
		`ln -s "$HOME" home-link; rm -rf home-link/.`,
		"mv ~ " + destination,
		"mv " + strconv.Quote(home) + " " + destination,
		"mv -T ~/. " + destination,
		"mv -t " + destination + " ~",
		"mv --target-directory=" + destination + " ~",
		"mv --backup ~ " + destination,
		"mv --backup=numbered ~ " + destination,
		"command mv -- ~/. " + destination,
		"sh -c 'mv \"$HOME\" \"$HOME/project/moved-home\"'",
		"rm -rf " + strconv.Quote(filepath.Dir(home)) + "/iso-guard-*",
		"rmdir -p ~/empty/child",
		"rmdir --parents ~/empty/child",
		"grmdir -p ../empty/child",
		"mv --exchange -T ordinary-dir ~",
		"mv --exchange --no-target-directory ordinary-dir " + strconv.Quote(home),
		"mv --exchange -t " + strconv.Quote(filepath.Dir(home)) + " " + strconv.Quote(filepath.Join(project, filepath.Base(home))),
	}
	if runtime.GOOS == "darwin" {
		commands = append(commands,
			"rm -rf "+strconv.Quote("/system/volumes/data"+home),
			"mv "+strconv.Quote(strings.ToUpper(home))+" "+destination,
		)
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			// Classify only: several probes name an ancestor of temporary HOME.
			// They must never be handed to a real shell by this regression.
			if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err == nil {
				t.Error("destructive HOME source was allowed")
			}
		})
	}
}

func TestBashDestructiveHomeRefusalIgnoresApproval(t *testing.T) {
	for _, tc := range []struct {
		name      string
		authority *AuthorityRuntime
	}{
		{name: "attended OFF", authority: &AuthorityRuntime{AllowBashWithoutApproval: true}},
		{name: "approved", authority: &AuthorityRuntime{Approved: true, AllowBashWithoutApproval: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, project := hardBlockHome(t)
			sentinel := filepath.Join(home, "keep.txt")
			writeFile(t, sentinel, "keep ordinary HOME contents")
			// This uses only temporary HOME and a destination within itself.
			// Even without the guard, mv cannot move a parent into its child.
			command := "mv " + strconv.Quote(home) + " " + strconv.Quote(filepath.Join(home, "moved-home"))
			_, err := NewBashTool().Execute(context.Background(), ToolCall{
				WorkDir: project, Input: map[string]any{"command": command}, Authority: tc.authority,
			})
			var refusal *DestructiveAncestorError
			if !errors.As(err, &refusal) {
				t.Errorf("expected destructive HOME refusal before execution, got %v", err)
			}
			if body, readErr := os.ReadFile(sentinel); readErr != nil || string(body) != "keep ordinary HOME contents" {
				t.Errorf("ordinary HOME sentinel changed: %q, %v", body, readErr)
			}
		})
	}
}

func TestDestructiveSourceGuardAllowsOrdinaryHomeDestinations(t *testing.T) {
	home, project := hardBlockHome(t)
	writeFile(t, filepath.Join(project, "report.pdf"), "report")
	writeFile(t, filepath.Join(home, "Downloads", "a.pdf"), "download")
	writeFile(t, filepath.Join(home, "old.log"), "log")
	for _, command := range []string{
		"cp report.pdf ~/",
		"mv ~/Downloads/a.pdf ~/",
		"mv -t ~/ ~/Downloads/a.pdf",
		"mv --target-directory=~/ ~/Downloads/a.pdf",
		"mv -T report.pdf ~/report.pdf",
		"rm ~/*.log",
		`rm "$HOME"/*.log`,
		"rm " + strconv.Quote(home) + "/*.log",
		"rm -rf ~/project/cache",
		"rmdir -p cache/child",
		"rmdir --parents project/cache",
		"rmdir -p " + strconv.Quote(filepath.Join(filepath.Dir(home), "unrelated-empty-parent", "child")),
		"mv --exchange -T report.pdf ~/report.pdf",
		"mv --exchange report.pdf ~/",
		"mv --exchange -t ~/ report.pdf",
		`printf '%s' "$HOME"`,
		`echo rm -rf "$HOME"`,
	} {
		t.Run(command, func(t *testing.T) {
			if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err != nil {
				t.Errorf("ordinary home operation was blocked: %v", err)
			}
		})
	}
	for _, flavor := range []string{".isoform-agent", ".yansu-agent"} {
		for _, command := range []string{
			"cp report.pdf ~/" + flavor + "/bin/tool",
			"mv ~/" + flavor + "/bin/tool moved",
			"rm -rf ~/" + flavor,
		} {
			if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err == nil {
				t.Errorf("bundled tool mutation was allowed: %s", command)
			}
		}
	}
}

func TestDestructiveConfiguredDataAncestorsAreHardBlocked(t *testing.T) {
	home, project := hardBlockHome(t)
	parent := filepath.Join(home, "storage")
	t.Setenv("YANSU_AGENT_HOME", filepath.Join(parent, "private-data"))
	for _, command := range []string{
		"rm -rf " + strconv.Quote(parent),
		"mv " + strconv.Quote(parent) + " moved-storage",
	} {
		if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err == nil {
			t.Errorf("configured data ancestor destruction was allowed: %s", command)
		}
	}
	for _, command := range []string{
		"cp report.pdf " + strconv.Quote(parent),
		"mv report.pdf " + strconv.Quote(parent),
	} {
		if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err != nil {
			t.Errorf("ordinary parent destination was blocked: %v", err)
		}
	}
}
