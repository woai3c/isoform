package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Scheduled runs must never invoke the standing context producers, even when
// a task happens to mention a real contact or a word present in memory.
func TestComposeCronContextTierKeepsOnlyTheTaskAndScheduleContract(t *testing.T) {
	rig := newHandoffTestRig(t)
	rig.app.memoryStore = setupFixtureStore(t)
	seedHandoffBoundaryContext(t, rig.app, "tier-chat")
	query := "Alice Example Halcyon"
	native := rig.app.composePrompt(query, "tier-chat", "", skillHubDomainChat, "English")
	task := query + " Read /task/context.md and use /task/skill/SKILL.md."
	cron := rig.app.composeCronPrompt(task, "/task/output", "slack", "en")
	for _, want := range []string{"<soul-context>", "<people-context>", "<memory-context>", "<memory-cli>", "<activity-cli>", "<yansu-context>", query} {
		if !strings.Contains(native, want) {
			t.Errorf("unchanged chat tier missing %q", want)
		}
	}
	omitted := append([]string{}, handoffSkippedBlocks...)
	omitted = append(omitted, "<knowledge-context>", "<memory-context-selection", "<user-installed-skills", "<yansu-context>", "<cua-tool>")
	for _, block := range omitted {
		if strings.Contains(cron, block) {
			t.Errorf("scheduled tier contains standing context %q", block)
		}
	}
	for _, want := range []string{task, "<automated-task-execution>", "/task/output", "slack", "<delivery_target>", responseLanguageInstruction("English")} {
		if !strings.Contains(cron, want) {
			t.Errorf("scheduled tier lost %q", want)
		}
	}
}

func TestComposeCronContextTierPreservesLanguageAndDeliveryChoices(t *testing.T) {
	app := &App{}
	// This tier needs no memory, activity or session store. Task paths take
	// precedence over the optional managed output directory, as before.
	prompt := app.composeCronPrompt("Write the report in French to /named/report.md.", "", "none", "zh-CN")
	for _, want := range []string{"Write the report in French", "/named/report.md", responseLanguageInstruction("Simplified Chinese"), "<deliverables>", "<automated-task-execution>"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("scheduled tier lost %q", want)
		}
	}
	for _, omitted := range []string{"<delivery-context>", "Default deliverable location:", "<soul-context>", "<memory-cli>", "<activity-cli>", "<yansu-context>"} {
		if strings.Contains(prompt, omitted) {
			t.Errorf("scheduled tier added %q", omitted)
		}
	}
}

func TestComposeCronContextIncludesOnlyNamedEnabledAutomationSkills(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	state := &skillHubState{Skills: map[string]SkillHubInstalledSkill{}}
	for _, spec := range []struct {
		slug, name          string
		enabled, automation bool
	}{
		{"release-deck", "Presentation", true, true},
		{"unrelated", "Unrelated", true, true},
		{"disabled", "Disabled", false, true},
		{"chat-only", "Chat Only", true, false},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("PRIVATE_SKILL_BODY"), 0600); err != nil {
			t.Fatal(err)
		}
		state.Skills[spec.slug] = SkillHubInstalledSkill{ID: spec.slug, Slug: spec.slug, Name: spec.name, Description: "Metadata for " + spec.name, Path: dir, Enabled: spec.enabled, Domains: map[string]bool{skillHubDomainAutomation: spec.automation}}
	}
	if err := saveSkillHubState(state); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	for _, task := range []string{"Prepare the weekly deck using my Presentation skill.", "Use RELEASE-DECK, Disabled and Chat Only."} {
		prompt := app.composeCronPrompt(task, "", "none", "en")
		for _, want := range []string{task, "- name: Presentation", filepath.Join(state.Skills["release-deck"].Path, "SKILL.md")} {
			if !strings.Contains(prompt, want) {
				t.Errorf("named skill discovery lost %q", want)
			}
		}
		for _, omitted := range []string{"- name: Unrelated", "- name: Disabled", "- name: Chat Only", "PRIVATE_SKILL_BODY"} {
			if strings.Contains(prompt, omitted) {
				t.Errorf("task received unrelated/disabled skill or body %q", omitted)
			}
		}
	}
	for _, task := range []string{"Write a report.", "Review presentational choices."} {
		if prompt := app.composeCronPrompt(task, "", "none", "en"); strings.Contains(prompt, "<user-installed-skills") {
			t.Errorf("unnamed catalog injected: %s", prompt)
		}
	}
}
