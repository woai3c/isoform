package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClawHubSlugFromRef(t *testing.T) {
	tests := []struct {
		name string
		ref  string
		want string
		ok   bool
	}{
		{name: "plain slug", ref: "github", want: "github", ok: true},
		{name: "owner url", ref: "https://clawhub.ai/iisweetheartii/agentgram-openclaw", want: "agentgram-openclaw", ok: true},
		{name: "skills url", ref: "https://clawhub.ai/skills/clawhub-wrapper", want: "clawhub-wrapper", ok: true},
		{name: "install command", ref: "openclaw skills install calendar-helper", want: "calendar-helper", ok: true},
		{name: "foreign host", ref: "https://example.com/skills/calendar", ok: false},
		{name: "path traversal", ref: "../calendar", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := clawHubSlugFromRef(tt.ref)
			if ok != tt.ok {
				t.Fatalf("ok=%v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("slug=%q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeSkillHubDomainsDefaultsEnabled(t *testing.T) {
	domains := normalizeSkillHubDomains(map[string]bool{
		skillHubDomainChat: false,
		"unknown":          false,
	})

	if domains[skillHubDomainChat] {
		t.Fatalf("chat domain should keep explicit false")
	}
	if !domains[skillHubDomainProject] {
		t.Fatalf("project domain should default to true")
	}
	if !domains[skillHubDomainAutomation] {
		t.Fatalf("automation domain should default to true")
	}
	if _, ok := domains["unknown"]; ok {
		t.Fatalf("unknown domain should be ignored")
	}
}

func TestSkillHubContextHonorsEnabledAndDomains(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	enabledDir := filepath.Join(home, ".yansu-agent", "skill-hub", "skills", "enabled-skill")
	disabledDir := filepath.Join(home, ".yansu-agent", "skill-hub", "skills", "disabled-skill")
	if err := os.MkdirAll(enabledDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(disabledDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(enabledDir, "SKILL.md"), []byte("# Enabled Skill\nUse this skill."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(disabledDir, "SKILL.md"), []byte("# Disabled Skill\nDo not include."), 0o644); err != nil {
		t.Fatal(err)
	}

	state := &skillHubState{
		Skills: map[string]SkillHubInstalledSkill{
			"enabled-skill": {
				ID:      "enabled-skill",
				Slug:    "enabled-skill",
				Name:    "Enabled Skill",
				Path:    enabledDir,
				Enabled: true,
				Domains: map[string]bool{skillHubDomainChat: true, skillHubDomainProject: false},
			},
			"disabled-skill": {
				ID:      "disabled-skill",
				Slug:    "disabled-skill",
				Name:    "Disabled Skill",
				Path:    disabledDir,
				Enabled: false,
				Domains: defaultSkillHubDomains(),
			},
		},
	}
	if err := saveSkillHubState(state); err != nil {
		t.Fatal(err)
	}

	app := &App{}
	chatContext := app.skillHubContext(skillHubDomainChat)
	if !strings.Contains(chatContext, "Enabled Skill") {
		t.Fatalf("expected enabled chat skill in context: %s", chatContext)
	}
	if strings.Contains(chatContext, "Disabled Skill") {
		t.Fatalf("disabled skill should not appear in context: %s", chatContext)
	}

	projectContext := app.skillHubContext(skillHubDomainProject)
	if projectContext != "" {
		t.Fatalf("project domain was disabled, got context: %s", projectContext)
	}
}

func TestPatchSkillHubSkillMergesDomainsAtomically(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	state := &skillHubState{
		Skills: map[string]SkillHubInstalledSkill{
			"sources": {
				ID:      "sources",
				Slug:    "sources",
				Name:    "Reserved-looking Skill",
				Enabled: true,
				Domains: map[string]bool{
					skillHubDomainChat:       true,
					skillHubDomainProject:    true,
					skillHubDomainAutomation: false,
				},
			},
		},
	}
	if err := saveSkillHubState(state); err != nil {
		t.Fatal(err)
	}

	enabled := false
	app := &App{}
	updated, err := app.PatchSkillHubSkill("sources", &enabled, map[string]bool{
		skillHubDomainProject: true,
		"unknown":             true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Enabled {
		t.Fatalf("enabled should be patched to false")
	}
	if !updated.Domains[skillHubDomainChat] {
		t.Fatalf("chat domain should be preserved")
	}
	if !updated.Domains[skillHubDomainProject] {
		t.Fatalf("project domain should be patched to true")
	}
	if updated.Domains[skillHubDomainAutomation] {
		t.Fatalf("automation domain should be preserved as false")
	}
	if _, ok := updated.Domains["unknown"]; ok {
		t.Fatalf("unknown domain should be ignored")
	}
}
