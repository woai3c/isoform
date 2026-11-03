package tools

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestInlineInterpreterInputProtectsHome(t *testing.T) {
	home, project := hardBlockHome(t)
	commands := []string{
		`python3 <<< 'import os,shutil; shutil.rmtree(os.environ["HOME"])'`,
		"python3 - <<'PY'\nimport os,shutil\nshutil.rmtree(os.environ[\"HOME\"])\nPY",
		"perl <<'PL'\nuse File::Path; remove_tree($ENV{HOME});\nPL",
		"ruby <<'RB'\nrequire 'fileutils'; FileUtils.rm_rf ENV['HOME']\nRB",
		"node <<'JS'\nrequire('fs').rmSync(process.env.HOME,{recursive:true})\nJS",
		"bun - <<'JS'\nrequire('fs').rmSync(process.env.HOME,{recursive:true})\nJS",
		`deno eval 'Deno.removeSync(Deno.env.get("HOME"),{recursive:true})'`,
		fmt.Sprintf(`deno eval 'Deno.removeSync("%s",{recursive:true})'`, home),
		`bun -e 'require("fs").rmSync(process.env.HOME,{recursive:true})'`,
		`node -e 'require("fs").rmSync(process.env.HOME,{recursive:true})'`,
		"sh <<'SH'\nrm -rf \"$HOME\"\nSH",
		`sh <<< 'rm -rf "$HOME"'`,
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			// Classify only; these probes must never reach an interpreter.
			if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err == nil {
				t.Fatal("inline interpreter removal of HOME was allowed")
			}
		})
	}
}

func TestInlineRemovalAliasesProtectHomeAndData(t *testing.T) {
	home, project := hardBlockHome(t)
	commands := []string{
		`python3 -c 'import os,shutil; wipe=shutil.rmtree; wipe(os.environ["HOME"])'`,
		`python3 -c 'import os,shutil; wipe=getattr(shutil,"rmtree"); wipe(os.environ["HOME"])'`,
		`python3 -c 'import os,shutil; getattr(shutil,"rmtree")(os.environ["HOME"])'`,
		fmt.Sprintf(`python3 -c 'import shutil; wipe=shutil.rmtree; wipe("%s")'`, home),
		fmt.Sprintf(`python3 -c 'import shutil; wipe=shutil.rmtree; wipe("%s")'`, filepath.Join(home, ".isoform-agent")),
		fmt.Sprintf(`python3 -c 'import shutil; wipe=getattr(shutil,"rmtree"); wipe("%s")'`, filepath.Join(home, ".yansu-agent")),
		`python3 -c 'import os,shutil; wipe=shutil.rmtree; wipe(os.path.expanduser("~"))'`,
		`node -e 'const wipe=require("fs").rmSync; wipe(process.env.HOME,{recursive:true})'`,
		`deno eval 'const wipe=Deno.removeSync; wipe(Deno.env.get("HOME"),{recursive:true})'`,
		// The whole inline program is deliberately conservative: a removal
		// API together with HOME needs refusal even when it is a destination
		// or a printed example, rather than trying to prove data flow.
		`python3 -c 'print("shutil.rmtree(\"~\")")'`,
		fmt.Sprintf(`python3 -c 'import shutil; shutil.move("report.pdf", "%s")'`, home),
		`python3 -c 'import shutil,os; shutil.move("report.pdf", os.environ["HOME"])'`,
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err == nil {
				t.Fatal("removal API and protected reference were allowed together")
			}
		})
	}
}

func TestInlineInterpreterInputPreservesWorkspacePrograms(t *testing.T) {
	_, project := hardBlockHome(t)
	for _, command := range []string{
		`python3 <<< 'import shutil; shutil.rmtree("./build")'`,
		"python3 - <<'PY'\nprint('HOME')\nPY",
		`deno eval 'Deno.removeSync("./build",{recursive:true})'`,
		`bun -e 'require("fs").rmSync("./build",{recursive:true})'`,
		`python3 -c 'import shutil; wipe=shutil.rmtree; wipe("./build")'`,
	} {
		if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err != nil {
			t.Errorf("workspace inline program %s: %v", command, err)
		}
	}
}
