package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalSkillSlugIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	got1, err := localSkillSlug(dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	got2, err := localSkillSlug(dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got1 != got2 {
		t.Fatalf("slug differs across calls: %q vs %q", got1, got2)
	}
	if !strings.HasPrefix(got1, "local-") {
		t.Errorf("slug %q should start with local-", got1)
	}
	if !clawHubSlugPattern.MatchString(got1) {
		t.Fatalf("slug %q rejected by pattern", got1)
	}
}

func TestLocalSkillSlugDiffersByPath(t *testing.T) {
	first, err := localSkillSlug("/tmp/skill-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := localSkillSlug("/tmp/skill-b")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("expected different slugs, got %q", first)
	}
}

func TestExtractFrontmatterField(t *testing.T) {
	body := []byte("---\nname: Calendar Helper\ndescription: \"Schedule meetings\"\n---\n\n# Body")
	if got := extractFrontmatterField(body, "name"); got != "Calendar Helper" {
		t.Errorf("name=%q", got)
	}
	if got := extractFrontmatterField(body, "description"); got != "Schedule meetings" {
		t.Errorf("description=%q", got)
	}
	if got := extractFrontmatterField(body, "missing"); got != "" {
		t.Errorf("missing=%q", got)
	}
}

func TestExtractFirstHeading(t *testing.T) {
	if got := extractFirstHeading([]byte("Some text\n# Hello\nMore")); got != "Hello" {
		t.Errorf("got=%q", got)
	}
	if got := extractFirstHeading([]byte("no heading here")); got != "" {
		t.Errorf("got=%q", got)
	}
}

func TestSkillNameFromSkillBody(t *testing.T) {
	frontmatter := []byte("---\nname: Frontmatter Wins\n---\n# Heading Loses\n")
	if got := skillNameFromSkillBody(frontmatter, "fallback"); got != "Frontmatter Wins" {
		t.Errorf("got=%q", got)
	}
	heading := []byte("# Heading\nbody")
	if got := skillNameFromSkillBody(heading, "fallback"); got != "Heading" {
		t.Errorf("got=%q", got)
	}
	plain := []byte("just text")
	if got := skillNameFromSkillBody(plain, "my-folder"); got != "my-folder" {
		t.Errorf("got=%q", got)
	}
}

func TestInstallSkillFromLocalCopiesAndRegisters(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	src := filepath.Join(t.TempDir(), "Calendar Helper")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	skillBody := "---\nname: Calendar Helper\ndescription: Schedule meetings\n---\n\n# Body\nUse me to schedule."
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte(skillBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "extra.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{}
	skill, err := app.InstallSkillFromLocal(src)
	if err != nil {
		t.Fatalf("install err: %v", err)
	}
	if skill == nil {
		t.Fatal("expected skill, got nil")
	}
	if skill.Source != skillHubSourceLocal {
		t.Errorf("source=%q", skill.Source)
	}
	if skill.SecurityStatus != "" {
		t.Errorf("security_status=%q, want empty", skill.SecurityStatus)
	}
	if skill.Name != "Calendar Helper" {
		t.Errorf("name=%q", skill.Name)
	}
	if skill.Description != "Schedule meetings" {
		t.Errorf("description=%q", skill.Description)
	}

	// Files were copied into the target directory.
	if _, err := os.Stat(filepath.Join(skill.Path, "SKILL.md")); err != nil {
		t.Errorf("SKILL.md missing in target: %v", err)
	}
	if _, err := os.Stat(filepath.Join(skill.Path, "extra.txt")); err != nil {
		t.Errorf("extra.txt missing in target: %v", err)
	}

	// Origin marker was written.
	if _, err := os.Stat(filepath.Join(skill.Path, ".yansu-skillhub", "local.json")); err != nil {
		t.Errorf("local origin marker missing: %v", err)
	}

	// Reinstalling the same path should reuse the slug, not create a duplicate.
	again, err := app.InstallSkillFromLocal(src)
	if err != nil {
		t.Fatalf("reinstall err: %v", err)
	}
	if again.Slug != skill.Slug {
		t.Errorf("slug changed: %q -> %q", skill.Slug, again.Slug)
	}

	state, err := loadSkillHubState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Skills) != 1 {
		t.Errorf("expected 1 skill, got %d", len(state.Skills))
	}
}

func TestInstallSkillFromLocalRejectsMissingSkillFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	src := t.TempDir()
	app := &App{}
	if _, err := app.InstallSkillFromLocal(src); err == nil {
		t.Fatal("expected error for folder without SKILL.md")
	}
}

func TestInstallSkillFromLocalRejectsInsideSkillHub(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	insideHub := filepath.Join(home, ".yansu-agent", "skill-hub", "skills", "evil")
	if err := os.MkdirAll(insideHub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(insideHub, "SKILL.md"), []byte("# evil"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{}
	if _, err := app.InstallSkillFromLocal(insideHub); err == nil {
		t.Fatal("expected error when source is inside skill hub")
	}
}
