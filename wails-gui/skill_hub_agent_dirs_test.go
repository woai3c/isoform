package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverAgentSkillsDoesNotInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	skillDir := filepath.Join(home, ".claude", "skills", "calendar-helper")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: Calendar Helper
description: Work with calendar events.
---

# Calendar Helper
`), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{}
	discovered, err := app.DiscoverAgentSkills()
	if err != nil {
		t.Fatal(err)
	}
	if len(discovered) != 1 {
		t.Fatalf("expected 1 discovered skill, got %d", len(discovered))
	}
	if discovered[0].AgentID != "claude" {
		t.Fatalf("agent id = %q, want claude", discovered[0].AgentID)
	}
	if discovered[0].Name != "Calendar Helper" {
		t.Fatalf("name = %q, want Calendar Helper", discovered[0].Name)
	}
	if discovered[0].Installed {
		t.Fatal("discovered skill should not be marked installed before import")
	}

	installed, err := app.ListSkillHubInstalledSkills()
	if err != nil {
		t.Fatal(err)
	}
	if len(installed) != 0 {
		t.Fatalf("discover should not install skills, got %d installed", len(installed))
	}
}

func TestDiscoverAgentSkillsWalksNestedAndDotDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	nestedDir := filepath.Join(home, ".codex", "skills", "codex-primary-runtime", "spreadsheets")
	systemDir := filepath.Join(home, ".codex", "skills", ".system", "openai-docs")
	for _, dir := range []string{nestedDir, systemDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(nestedDir, "SKILL.md"), []byte("# Excel\nWork with spreadsheets."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(systemDir, "SKILL.md"), []byte("# OpenAI Docs\nUse official docs."), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{}
	discovered, err := app.DiscoverAgentSkills()
	if err != nil {
		t.Fatal(err)
	}
	if len(discovered) != 2 {
		t.Fatalf("expected 2 discovered skills, got %d: %+v", len(discovered), discovered)
	}

	names := map[string]bool{}
	for _, skill := range discovered {
		names[skill.Name] = true
	}
	if !names["Excel"] {
		t.Fatal("nested Codex skill should be discovered")
	}
	if !names["OpenAI Docs"] {
		t.Fatal(".system skill should be discovered")
	}
}

func TestInstallSkillFromAgentDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	skillDir := filepath.Join(home, ".codex", "skills", "openai-docs")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# OpenAI Docs\nUse official docs."), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{}
	discovered, err := app.SearchAgentSkills("openai")
	if err != nil {
		t.Fatal(err)
	}
	if len(discovered) != 1 {
		t.Fatalf("expected 1 discovered skill, got %d", len(discovered))
	}

	installed, err := app.InstallSkillFromAgentDir(discovered[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if installed.Source != skillHubSourceAgentDirs {
		t.Fatalf("source = %q, want %q", installed.Source, skillHubSourceAgentDirs)
	}
	if installed.Path == skillDir {
		t.Fatal("agent-dir install should copy into Yansu Skill Hub, not reuse source path")
	}

	rediscovered, err := app.SearchAgentSkills("openai")
	if err != nil {
		t.Fatal(err)
	}
	if len(rediscovered) != 1 || !rediscovered[0].Installed || rediscovered[0].InstalledID != installed.ID {
		t.Fatalf("rediscovered skill should be marked installed, got %+v", rediscovered)
	}
}
