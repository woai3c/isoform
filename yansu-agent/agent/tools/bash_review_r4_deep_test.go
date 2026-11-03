package tools

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReviewR4DeeperHoles covers the round-4 findings whose fix is to make
// the deeper, harder-to-detect command unknown (it asks when attended, denies
// when unattended, and tightens any unattended run that holds a grant).
func TestReviewR4DeeperHoles(t *testing.T) {
	home, project := reviewHome(t)
	// Files a python -m module name would shadow in the work folder.
	for _, f := range []string{"this.py", "unittest.py"} {
		if err := os.WriteFile(filepath.Join(project, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(project, "json"), 0o755); err != nil {
		t.Fatal(err)
	}
	assertAsks(t, project, []string{
		// 12/13/24/35: write .git config or hooks, then run git.
		"printf '#!/bin/sh\\ntouch M\\n' > .git/hooks/pre-commit && chmod +x .git/hooks/pre-commit && git commit -m x",
		"printf '[core]\\n\\tfsmonitor = ./evil2\\n' >> .git/config; git status",
		"printf '[diff]\\n\\texternal = touch ./pwned\\n' >> .git/config && git diff",
		// 26: go vet -vettool / -ldflags -extld / -overlay.
		"go vet -vettool=/tmp/evil ./...",
		"go build -ldflags='-linkmode=external -extld=/tmp/evil' ./...",
		"go build -ldflags '-extld=/tmp/evil' ./...",
		"go build -overlay=/tmp/o.json ./...",
		// 28/14: go install and go build -o outside the work folder.
		"go install ./...",
		"go install ./cmd/ls",
		"go build -o " + home + "/bin/x ./...",
		// 29: python -m shadowed by a work-folder module.
		"python3 -m this",
		"python3 -m unittest",
		"python3 -m json.tool in.json out.json",
		// 31: npm / pnpm script-shell and userconfig overrides.
		"npm test --script-shell=./evil.sh",
		"npm test --userconfig=./.npmrc",
		"pnpm test --script-shell=./evil.sh",
	})

	// Controls: the ordinary forms still run locally.
	assertLocal(t, project, []string{
		"go build ./...",
		"go vet ./...",
		"go build -ldflags='-s -w' ./...",
		"python3 -m pytest",
		"npm test",
		"pnpm build",
	})
}
