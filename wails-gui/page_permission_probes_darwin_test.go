//go:build darwin

package main

import "testing"

// Native status reads only. Never request access or start the app/capture.
func BenchmarkPagePermissionProbes(b *testing.B) {
	checks := []struct {
		name string
		read func() bool
	}{
		{"screen", preflightScreenCaptureAccess}, {"accessibility", accessibilityAccess},
		{"microphone", microphoneAccess}, {"notifications", notificationAccess},
		{"contacts", contactsAccessGranted}, {"calendar", calendarAccessGranted},
		{"reminders", remindersAccessGranted}, {"mail", mailAutomationGranted},
		{"messages", func() bool { return sharedTextChannel().automationAllowed(false) }},
	}
	for _, check := range checks {
		b.Run(check.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				check.read()
			}
		})
	}
}
