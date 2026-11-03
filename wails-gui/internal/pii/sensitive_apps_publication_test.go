package pii

import (
	"testing"
	"time"
)

func TestSensitiveAppPublicationKeepsPolicyStable(t *testing.T) {
	prior := GetUserSensitiveApps()
	t.Cleanup(func() { SetUserSensitiveApps(prior) })
	for _, mutate := range []struct {
		name string
		run  func()
	}{
		{"set", func() { SetUserSensitiveApps(map[string]string{"com.example.notes": "Notes"}) }},
		{"add", func() { AddUserSensitiveApp("com.example.notes", "Notes") }},
		{"remove", func() { RemoveUserSensitiveApp("com.example.notes") }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			if !TryLockSensitiveAppsForPublication() {
				t.Fatal("cannot acquire publication guard")
			}
			started, done := make(chan struct{}), make(chan struct{})
			go func() { close(started); mutate.run(); close(done) }()
			<-started
			// Existing getters remain safe while a policy writer waits.
			_ = GetUserSensitiveApps()
			_ = IsSensitiveAppName("Notes")
			select {
			case <-done:
				t.Error("policy changed before publication released")
			case <-time.After(10 * time.Millisecond):
			}
			UnlockSensitiveAppsForPublication()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("policy writer did not resume")
			}
		})
	}
}
