package handoff

import (
	"testing"
)

func TestParseExtractsWorkdirAndKeyFiles(t *testing.T) {
	raw := "---\n" +
		"title: Do X\n" +
		"workdir: /Users/x/repo\n" +
		"keyFiles: [\"main.go\",\"docs/README.md\"]\n" +
		"---\n\n# Objective\n\n- **Key files**: main.go\n\nbody\n"
	e := Parse("hf_p", raw)
	if e.ID != "hf_p" {
		t.Errorf("id: %q", e.ID)
	}
	if e.Title != "Do X" {
		t.Errorf("title: %q", e.Title)
	}
	if e.Workdir != "/Users/x/repo" {
		t.Errorf("workdir: %q", e.Workdir)
	}
	if len(e.KeyFiles) != 2 || e.KeyFiles[0] != "main.go" || e.KeyFiles[1] != "docs/README.md" {
		t.Errorf("keyFiles: %#v", e.KeyFiles)
	}
	if e.Body != "# Objective\n\n- **Key files**: main.go\n\nbody\n" {
		t.Errorf("body: %q", e.Body)
	}
}
