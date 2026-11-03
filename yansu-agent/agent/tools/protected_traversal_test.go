package tools

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestDestructiveFindAndFDProtectHome(t *testing.T) {
	home, project := hardBlockHome(t)
	for _, command := range []string{
		"find ~ -delete",
		`find "$HOME" -mindepth 1 -delete`,
		"find " + strconv.Quote(home) + " -delete",
		"find .. -delete",
		"find -L ~ -delete",
		"gfind ~ -delete",
		`find ~ -exec rm -rf {} \;`,
		"find ~ -exec rm -rf {} +",
		`find ~ -execdir rm -rf {} \;`,
		`find ~ -execdir rm -rf ordinary \;`,
		`find ~ -okdir rm -rf ordinary \;`,
		`find ~ -ok rm -rf {} \;`,
		`find ~ -okdir rm -rf {} \;`,
		`find ~ -exec sh -c 'rm -rf "$1"' sh {} \;`,
		"fd --hidden . ~ -x rm -rf {}",
		"fd --hidden . ~ -X rm -rf",
		"fdfind -H . ~ --exec rm -rf {}",
		"fd . ~ -delete",
		`find "$TARGET" -delete`,
	} {
		t.Run(command, func(t *testing.T) {
			// Classification only: no destructive traversal reaches a shell.
			if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err == nil {
				t.Error("destructive HOME traversal was allowed")
			}
		})
	}
	for _, command := range []string{
		"find . -name '*.log' -delete",
		"find ./cache -delete",
		"find . -name '*.log' -exec rm -f {} +",
		`find . -name '*.log' -execdir rm -f {} \;`,
		"fd . . -x rm -f {}",
		"fd . . -X rm -f",
		"find . -name '*.log' -print0 | xargs -0 rm -f",
		`find . -name '*.log' -exec rm -f ./ordinary.log \;`,
		"find ~ -maxdepth 1 -print",
		"fd --hidden . ~",
	} {
		t.Run(command, func(t *testing.T) {
			if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err != nil {
				t.Errorf("ordinary traversal was blocked: %v", err)
			}
		})
	}
}

func TestTraversalProvenanceCannotBeForged(t *testing.T) {
	_, project := hardBlockHome(t)
	for _, command := range []string{
		"find . -print | xargs rm -rf",
		"find . -printf '../..\\0' | xargs -0 rm -rf",
		"find . -print -print0 | xargs -0 rm -rf",
		`find . -exec rm -rf {}/../.. \;`,
		"find . -print0 | xargs -0 -I{} rm -rf {}/../..",
		`find -L . -exec rm -rf {} \;`,
		"fd -L . . -x rm -rf {}",
	} {
		scan := analyzeBash(command, project)
		for _, cmd := range scan.cmds {
			if !destructiveTraversalCommand(cmd) {
				continue
			}
			for _, arg := range cmd.argv[1:] {
				if arg.dynamic && arg.pathRoot != "" {
					t.Errorf("unsafe replacement acquired path provenance: %s: %+v", command, arg)
				}
			}
		}
		if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err == nil {
			t.Errorf("unbounded generated destructive source was allowed: %s", command)
		}
	}
}

func TestFindDotDotReadersKeepProtectedPrefix(t *testing.T) {
	_, project := hardBlockHome(t)
	for _, command := range []string{
		"find " + strconv.Quote(project+"/..") + " -name MEMORY.md -exec cat {} +",
		"find " + strconv.Quote(project+"/..") + " -name MEMORY.md -print0 | xargs -0 cat",
		"find .. -name MEMORY.md -exec cat {} +",
		"find ./.. -name MEMORY.md -print0 | xargs -0 cat",
		"find -L .. -name MEMORY.md -exec cat {} +",
	} {
		t.Run(command, func(t *testing.T) {
			if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err == nil {
				t.Error("reader lost the protected root through dot-dot")
			}
		})
	}
	for _, command := range []string{
		"find ./cache/.. -name README.md -exec cat {} +",
		"find ./cache/.. -name README.md -print0 | xargs -0 cat",
	} {
		if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err != nil {
			t.Errorf("workspace reader was blocked: %s: %v", command, err)
		}
	}
}

func TestFindExplicitAndRuntimeRootOptions(t *testing.T) {
	_, project := hardBlockHome(t)
	for _, command := range []string{
		"find -f ~ -delete",
		"find -f . -f ~ -delete",
		"find -- ~ -delete",
		"find -L -f ~ -delete",
		"find -f ~ -name MEMORY.md -exec cat {} +",
		"find -- ~ -name MEMORY.md -print0 | xargs -0 cat",
		"find -files0-from roots.txt -delete",
		"find -depth -files0-from roots.txt -delete",
		"find -files0-from roots.txt -name MEMORY.md -exec cat {} +",
		"find -files0-from roots.txt -name MEMORY.md -print0 | xargs -0 cat",
		"gfind -files0-from - -delete",
	} {
		t.Run(command, func(t *testing.T) {
			if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err == nil {
				t.Error("explicit or runtime protected traversal root was allowed")
			}
		})
	}
	for _, command := range []string{
		"find -f . -name '*.log' -delete",
		"find -- . -name '*.log' -delete",
		"find -f . -name README.md -exec cat {} +",
		"find -- . -name README.md -print0 | xargs -0 cat",
		"find -files0-from roots.txt -print0",
	} {
		t.Run(command, func(t *testing.T) {
			if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err != nil {
				t.Errorf("ordinary explicit root or names-only listing was blocked: %v", err)
			}
		})
	}
}

func TestCanonicalPathPreservesSymlinkBeforeDotDot(t *testing.T) {
	home, project := hardBlockHome(t)
	data := filepath.Join(home, ".isoform-agent")
	scratch := filepath.Join(data, "scratch")
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(project, "data-scratch")
	if err := os.Symlink(scratch, alias); err != nil {
		t.Fatal(err)
	}
	for _, leaf := range []string{"cron-secret", "credentials.json"} {
		raw := alias + "/../" + leaf
		want := filepath.Join(data, leaf)
		if got, err := canonicalPathLoose(raw); err != nil || got != want {
			t.Errorf("resolve %q: got %q, %v; want %q", raw, got, err, want)
		}
		for _, tool := range []string{"Read", "Write", "Edit", "NotebookEdit"} {
			if err := CheckProtectedPaths(tool, map[string]any{"path": raw}, project); err == nil {
				t.Errorf("%s allowed symlink-before-dot-dot credential: %s", tool, raw)
			}
		}
		for _, spelling := range []string{raw, "./data-scratch/../" + leaf} {
			command := "cat " + strconv.Quote(spelling)
			if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err == nil {
				t.Errorf("Bash allowed symlink-before-dot-dot credential: %s", spelling)
			}
		}
	}
	safe := alias + "/../scratch/new.txt"
	if got, err := canonicalPathLoose(safe); err != nil || got != filepath.Join(scratch, "new.txt") {
		t.Errorf("resolve safe missing leaf: %q, %v", got, err)
	}
	if err := CheckProtectedPaths("Write", map[string]any{"path": safe}, project); err != nil {
		t.Errorf("safe scratch file was blocked: %v", err)
	}
}

func TestRelocatedBundledParentDestructionIsBlocked(t *testing.T) {
	home, project := hardBlockHome(t)
	parent := filepath.Join(home, "tools")
	bundle := filepath.Join(parent, "bin")
	writeFile(t, filepath.Join(bundle, "tool"), "bundled tool")
	if err := os.Symlink(bundle, filepath.Join(home, ".isoform-agent", "bin")); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		"rm -rf ~/tools",
		"mv ~/tools ~/moved-tools",
		"rm -rf " + strconv.Quote(bundle+"/.."),
		"find ~/tools -delete",
	} {
		if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err == nil {
			t.Errorf("relocated bundled parent destruction was allowed: %s", command)
		}
	}
	for _, command := range []string{
		"cp report.pdf ~/tools/",
		"mv report.pdf ~/tools/",
		"rm -f ~/tools/ordinary.log",
	} {
		if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err != nil {
			t.Errorf("ordinary relocated-parent operation was blocked: %s: %v", command, err)
		}
	}
}
