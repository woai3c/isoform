package main

import (
	"os"
	"testing"
	"wails-gui/internal/pii"
)

const activityPrivacySecret = "sk-proj-abcdefghijklmnopqrstuvwxyz1234567890"

func isolateActivityPrivacyTest(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ap-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("HOME", dir)
	old := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(old) })
	pii.SetUserSensitiveApps(map[string]string{"com.example.private": "Private Notes"})
	oldVoice := GetUserVoiceInputApps()
	t.Cleanup(func() { SetUserVoiceInputApps(oldVoice) })
	SetUserVoiceInputApps(nil)
}
