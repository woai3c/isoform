package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInferSkillInstallSource(t *testing.T) {
	tmp := t.TempDir()
	localSkillDir := filepath.Join(tmp, "my-skill")
	if err := os.Mkdir(localSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		ref  string
		want string
	}{
		{name: "clawhub url", ref: "https://clawhub.ai/skills/calendar", want: "clawhub"},
		{name: "github url", ref: "https://github.com/acme/skills/tree/main/calendar", want: "github"},
		{name: "github shorthand", ref: "acme/skills", want: "github"},
		{name: "agent dirs id", ref: "claude:calendar:abc12345", want: "agent-dirs"},
		{name: "local directory", ref: localSkillDir, want: "local"},
		{name: "ambiguous slug", ref: "calendar", want: ""},
		{name: "absolute path that does not exist", ref: "/tmp/not-a-real-skill-path-xyz123/sub", want: ""},
		{name: "relative dot path", ref: "./foo/bar", want: ""},
		{name: "tilde home path", ref: "~/foo", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inferSkillInstallSource(tt.ref); got != tt.want {
				t.Fatalf("inferSkillInstallSource(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestAddSkillDomainFlag(t *testing.T) {
	domains := map[string]bool{}
	if err := addSkillDomainFlag(domains, "chat", "true"); err != nil {
		t.Fatal(err)
	}
	if err := addSkillDomainFlag(domains, "project", "off"); err != nil {
		t.Fatal(err)
	}
	if err := addSkillDomainFlag(domains, "automation", ""); err != nil {
		t.Fatal(err)
	}

	if !domains["chat"] {
		t.Fatal("chat domain should be true")
	}
	if domains["project"] {
		t.Fatal("project domain should be false")
	}
	if _, ok := domains["automation"]; ok {
		t.Fatal("empty automation flag should be ignored")
	}
	if err := addSkillDomainFlag(domains, "automation", "maybe"); err == nil {
		t.Fatal("invalid domain flag value should return an error")
	}
}
