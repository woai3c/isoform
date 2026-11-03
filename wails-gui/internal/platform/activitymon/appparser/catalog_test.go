package appparser

import (
	"sort"
	"testing"
)

func TestCatalogCoversKnownParsers(t *testing.T) {
	entries := Catalog()
	if len(entries) < 30 {
		t.Fatalf("catalog has %d entries, expected >= 30", len(entries))
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Name == "" {
			t.Errorf("entry with empty Name: %+v", e)
		}
		if e.Label == "" {
			t.Errorf("entry %q has empty Label", e.Name)
		}
		if len(e.Bundles) == 0 && len(e.URLHosts) == 0 {
			t.Errorf("entry %q exposes neither bundles nor url hosts", e.Name)
		}
		names = append(names, e.Name)
	}
	sort.Strings(names)
	want := []string{
		"appstore", "asana", "browser", "calendar", "chatgpt",
		"chatgpt_web", "claude_web", "code_editor", "discord",
		"feishu", "finder", "github", "gmail", "google_calendar",
		"linear", "mail", "messages", "notes", "notion", "obsidian",
		"office_document_pages", "office_document_word",
		"office_presentation_keynote", "office_presentation_powerpoint",
		"office_spreadsheet_excel", "office_spreadsheet_numbers",
		"reminders", "slack", "tencent_chat", "terminal", "whatsapp",
	}
	missing := []string{}
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	for _, n := range want {
		if !have[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		t.Errorf("catalog missing parsers: %v", missing)
	}
}
