package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDynamicWrappersCannotHideDestructiveCommands(t *testing.T) {
	_, project := hardBlockHome(t)
	wrappers := []string{
		`env LANG="$LANG"`,
		`nice $OPTIONS`,
		`nohup $OPTIONS`,
		`timeout "$DURATION" $OPTIONS`,
		`command $OPTIONS`,
		`exec $OPTIONS`,
		`/usr/bin/time $OPTIONS`,
		`sudo $OPTIONS`,
	}
	for _, wrapper := range wrappers {
		for _, nested := range []string{
			`find "$HOME" -delete`,
			`fd . "$HOME" -delete`,
			`find "$HOME" -exec rm -rf {} +`,
			`rm -rf "$HOME"`,
			`mv "$HOME" /tmp/moved`,
		} {
			command := wrapper + " " + nested
			t.Run(command, func(t *testing.T) {
				// Classification only: never run a destructive wrapper probe.
				if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err == nil {
					t.Fatal("dynamic wrapper hid a destructive command")
				}
			})
		}
	}
	for _, command := range []string{
		`env LANG="$LANG" cat ./README.md`,
		`env LANG=C rm -f ./build.log`,
		`nice -n 5 find ./build -delete`,
		`nohup rm -f ./build.log`,
		`command rm -f ./build.log`,
	} {
		if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err != nil {
			t.Errorf("ordinary wrapped command was refused: %s: %v", command, err)
		}
	}
}

func TestDestructiveTargetsFollowLiteralCDChains(t *testing.T) {
	_, project := hardBlockHome(t)
	work := filepath.Join(project, "one", "two")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		`cd ..; cd ..; cd ..; rm -rf .`,
		`cd .. && cd .. && cd .. && mv . /tmp/moved`,
		`cd ..; cd ..; cd ..; find . -delete`,
	} {
		t.Run(command, func(t *testing.T) {
			if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, work); err == nil {
				t.Fatal("successive cd commands lost the HOME boundary")
			}
		})
	}
	if err := CheckProtectedPaths("Bash", map[string]any{"command": `cd ..; cd ..; rm -rf ./build`}, work); err != nil {
		t.Fatalf("ordinary literal cd chain was refused: %v", err)
	}
}

func TestRelativeDestructiveTargetsAfterDynamicCDFailClosed(t *testing.T) {
	_, project := hardBlockHome(t)
	for _, command := range []string{
		`cd "$TARGET"; rm -rf .`,
		`cd "$(pwd)"; rm -rf ..`,
		`cd "$TARGET"; mv folder /tmp/moved`,
		`cd "$TARGET"; rmdir ordinary`,
		`cd "$TARGET"; unlink ordinary`,
		`cd "$TARGET"; find . -delete`,
	} {
		t.Run(command, func(t *testing.T) {
			if err := CheckProtectedPaths("Bash", map[string]any{"command": command}, project); err == nil {
				t.Fatal("relative destructive target in an unknown cwd was allowed")
			}
		})
	}
}
