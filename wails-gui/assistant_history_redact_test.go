package main

import (
	"strings"
	"testing"
)

func TestAssistantHistoryRedactorDropsSecretsAndKeepsNormalLines(t *testing.T) {
	dropped := []string{
		"export OPENAI_API_KEY=sk-test-example-api-key",
		"token: ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		"password = hunter2222",
		"DB_PASSWORD=example-password",
		"-----BEGIN RSA PRIVATE KEY-----",
		"Authorization: Bearer abcdefghijklmnop",
		"aws key AKIAIOSFODNN7EXAMPLE in the config",
		"use this value aB3dE5gH7jK9mN1pQ3rS5tU7vW9xY1zA for the staging deploy",
		"密码: hunter2222",
	}
	for _, line := range dropped {
		if !assistantHistoryLooksLikeSecret(line) {
			t.Errorf("secret line kept: %q", line)
		}
	}
	kept := []string{
		"Run the tests before every push.",
		"git log --oneline",
		"commit 3f9a2c1d8e7b6a5f4e3d2c1b0a9f8e7d6c5b4a39 fixed the fixture",
		"request id 123e4567-e89b-12d3-a456-426614174000",
		"/Users/example/src/project/wails-gui/assistant_history.go",
		"The onboarding tests are failing in CI.",
	}
	for _, line := range kept {
		if assistantHistoryLooksLikeSecret(line) {
			t.Errorf("normal line dropped: %q", line)
		}
	}
}

func TestRedactAssistantHistoryTextKeepsTheRestOfTheMessage(t *testing.T) {
	text := "Here is the fix.\nexport STRIPE_SECRET_KEY=sk-test-example-api-key\nThen run the tests."
	got, dropped := redactAssistantHistoryText(text)
	if dropped != 1 {
		t.Fatalf("dropped = %d", dropped)
	}
	if strings.Contains(got, "sk-test-example-api-key") || !strings.Contains(got, "Here is the fix.") || !strings.Contains(got, "Then run the tests.") {
		t.Fatalf("redacted text: %q", got)
	}
}
