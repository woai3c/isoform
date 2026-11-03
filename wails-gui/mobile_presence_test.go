package main

import (
	"testing"

	"wails-gui/internal/proactivevoice"
)

func TestProactiveVoicePresenceNamesThePersonOnlyForDrafts(t *testing.T) {
	name := "Priya Raman"
	if _, ok := proactiveVoicePresence(proactivevoice.Job{DraftAllowed: false}); ok {
		t.Fatal("no draft, no line")
	}
	text, ok := proactiveVoicePresence(proactivevoice.Job{DraftAllowed: true, Items: []proactivevoice.Item{{FromName: &name}}})
	if !ok || text.EN != "Writing the reply to Priya…" || text.ZH != "正在写给 Priya 的回复…" {
		t.Fatalf("text = %+v", text)
	}
	if got := mobilePresenceReadingHistory("codex"); got.EN != "Reading your Codex history…" {
		t.Fatalf("codex = %+v", got)
	}
	if got := mobilePresenceReadingHistory(""); got.EN != "Reading your Claude Code history…" {
		t.Fatalf("default = %+v", got)
	}
	var app *App
	app.beginPresence(text)() // without a phone bridge it is a no-op
}
