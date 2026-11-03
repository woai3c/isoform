package main

import (
	"encoding/json"
	"os"
	"testing"
)

func TestQuickChatPresetsKeepStoredComboShape(t *testing.T) {
	tests := []QuickChatHotkeyConfig{
		{Key: quickChatHotkeyKeyOptOpt},
		{Ctrl: true, Key: " "},
		{Meta: true, Shift: true, Key: " "},
		{Key: quickChatHotkeyKeyOff},
	}
	for _, cfg := range tests {
		if err := validateQuickChatHotkey(cfg); err != nil {
			t.Fatalf("preset %+v: %v", cfg, err)
		}
	}
	if validateQuickChatHotkey(QuickChatHotkeyConfig{Key: "q"}) == nil {
		t.Fatal("bare character keys must still be refused")
	}
}

func TestQuickChatOffSurvivesReloadAndCanBeReenabled(t *testing.T) {
	t.Setenv("YANSU_AGENT_HOME", t.TempDir())
	previous := currentQuickChatHotkey()
	t.Cleanup(func() {
		quickChatHotkeyMuConfig.Lock()
		quickChatHotkeyCurrent = previous
		quickChatHotkeyMuConfig.Unlock()
		applyHotkeyToPlatform(previous)
	})
	app := &App{}
	for _, cfg := range []QuickChatHotkeyConfig{
		{Key: quickChatHotkeyKeyOff},
		{Ctrl: true, Key: " "},
	} {
		if err := app.SetQuickChatHotkey(cfg); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(quickChatHotkeyPath())
		if err != nil {
			t.Fatal(err)
		}
		var stored map[string]any
		if err := json.Unmarshal(data, &stored); err != nil {
			t.Fatal(err)
		}
		if len(stored) != 5 || stored["key"] != cfg.Key {
			t.Fatalf("stored binding shape changed: %s", data)
		}
		quickChatHotkeyMuConfig.Lock()
		quickChatHotkeyCurrent = QuickChatHotkeyConfig{}
		quickChatHotkeyMuConfig.Unlock()
		loadQuickChatHotkeyFromDisk()
		if got := app.GetQuickChatHotkey(); got != cfg {
			t.Fatalf("after reload: got %+v, want %+v", got, cfg)
		}
	}
}
