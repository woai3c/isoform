package tools

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"
)

func TestInlineDestructiveHomeAndDataReferences(t *testing.T) {
	home, project := hardBlockHome(t)
	commands := []string{
		fmt.Sprintf(`python3 -c "import shutil; shutil.rmtree('%s')"`, home),
		fmt.Sprintf(`python3 -Sc"import shutil; shutil.rmtree('%s')"`, home),
		`python3 -c 'import os,shutil; shutil.rmtree(os.environ["HOME"])'`,
		`python3 -c 'import os,shutil; shutil.rmtree(os.getenv("HOME"))'`,
		`python3 -c 'from pathlib import Path; import shutil; shutil.rmtree(Path.home())'`,
		`perl -MFile::Path -e 'remove_tree($ENV{HOME})'`,
		`perl -MFile::Path -e 'remove_tree $ENV{HOME}'`,
		`perl -MFile::Path -i -e 'remove_tree($ENV{HOME})'`,
		`perl -MFile::Path -le 'remove_tree($ENV{HOME})'`,
		`perl -MFile::Path -0pe 'remove_tree($ENV{HOME})'`,
		`perl -MFile::Path -e 'remove_tree("$ENV{HOME}")'`,
		`ruby -e 'require "fileutils"; FileUtils.rm_rf ENV["HOME"]'`,
		`node -e 'require("fs").rmSync(process.env.HOME,{recursive:true})'`,
		`node --eval='require("fs").rmSync(require("os").homedir(),{recursive:true})'`,
		`node --require fs -e 'require("fs").rmSync(process.env.HOME,{recursive:true})'`,
		`node --input-type module -e 'import fs from "fs"; fs.rmSync(process.env.HOME,{recursive:true})'`,
		`node -e 'require("child_process").execFileSync("rm",["-rf",process.env.HOME])'`,
		`python3 -c 'import shutil,sys; shutil.rmtree(sys.argv[1])' "$HOME"`,
		`python3 -c 'import shutil,sys; shutil.rmtree(sys.argv[1])' "$UNKNOWN"`,
		`php -r 'rmdir(getenv("HOME"));'`,
		`osascript -e 'do shell script "rm -rf $HOME"'`,
		`python3 -c 'import os; os.system("rm -rf ~")'`,
		fmt.Sprintf(`python3 -c 'import subprocess; subprocess.run(["rm", "-rf", "%s"])'`, home),
		`python3 -c 'import shutil,os; shutil.move(os.environ["HOME"], "/tmp/moved")'`,
		`sh -c 'rm -rf "$HOME/."'`,
		fmt.Sprintf(`python3 -c "import shutil; shutil.rmtree('%s')"`, filepath.Join(home, ".isoform-agent")),
		fmt.Sprintf(`node -e "require('fs').rmSync('%s',{recursive:true})"`, filepath.Join(home, ".yansu-agent")),
	}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			// Classify only: never execute destructive interpreter probes.
			if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err == nil {
				t.Fatal("destructive inline program was allowed")
			}
		})
	}
}

func TestInlineDestructiveGuardPreservesOrdinaryPrograms(t *testing.T) {
	home, project := hardBlockHome(t)
	for _, command := range []string{
		fmt.Sprintf(`python3 -c "print('%s')"`, home),
		`python3 -c 'import shutil; shutil.rmtree("./build")'`,
		`node -e 'require("fs").rmSync("./build",{recursive:true})'`,
		`ruby -e 'require "fileutils"; FileUtils.rm_rf "./build"'`,
		`python3 -c 'print("cron-secret")'`,
		"node -e '// fs.rmSync(process.env.HOME)\nconsole.log(1)'",
		`node -e '/* fs.rmSync(process.env.HOME) */ console.log(1)'`,
		`osascript -e '-- do shell script "rm -rf $HOME"' -e 'return 1'`,
		`python3 -c 'import shutil; n=2//1; shutil.rmtree("./build")'`,
	} {
		if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err != nil {
			t.Errorf("ordinary inline program %s: %v", command, err)
		}
	}
}

func TestDynamicDestructiveSourcesFailClosed(t *testing.T) {
	_, project := hardBlockHome(t)
	for _, command := range []string{
		`rm -rf "$(echo $HOME)"`,
		`D=$HOME; rm -rf "$D"`,
		`rm -rf "${HOME:-/x}"`,
		`rm -rf "${HOME%/}"`,
		`mv "$(echo $HOME)" /tmp/x`,
		`rm -rf "$UNKNOWN"`,
		`rm -rf "./build/$UNKNOWN"`,
		`mv --exchange -T ordinary "$UNKNOWN"`,
	} {
		t.Run(command, func(t *testing.T) {
			if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err == nil {
				t.Fatal("unresolved destructive source was allowed")
			}
		})
	}
}

func TestBashInlineAndDynamicRefusalsIgnoreApproval(t *testing.T) {
	home, project := hardBlockHome(t)
	// Both programs are harmless even without the guard: the Python program
	// exits before its dead destructive call, and mv cannot move a parent into
	// its child. Require refusal before starting either program.
	for _, command := range []string{
		fmt.Sprintf(`python3 -c "raise SystemExit(0); import shutil; shutil.rmtree('%s')"`, home),
		`D=$HOME; mv "$D" ` + strconv.Quote(filepath.Join(home, "child")),
	} {
		for _, approved := range []bool{false, true} {
			_, err := NewBashTool().Execute(context.Background(), ToolCall{
				WorkDir: project, Input: map[string]any{"command": command},
				Authority: &AuthorityRuntime{AllowBashWithoutApproval: true, Approved: approved},
			})
			var refusal *DestructiveAncestorError
			if !errors.As(err, &refusal) {
				t.Errorf("approved=%v command=%s: want hard refusal, got %v", approved, command, err)
			}
		}
	}
}
