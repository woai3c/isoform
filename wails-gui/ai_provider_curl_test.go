package main

import (
	"strings"
	"testing"
)

// The copied curl command reads the key from $API_KEY, so the text the user
// copies, pastes or screenshots never holds it.
func TestBuildProviderTestCurlNeverContainsTheAPIKey(t *testing.T) {
	const key = "example-api-key"
	app := &App{}
	for _, tc := range []struct {
		provider string
		header   string
	}{
		{"anthropic", `-H "x-api-key: $API_KEY"`},
		{"openai", `-H "authorization: Bearer $API_KEY"`},
	} {
		cmd := app.BuildProviderTestCurl(AIProviderConfig{
			Provider: tc.provider,
			APIKey:   key,
			BaseURL:  "https://gateway.example.com",
			Model:    "test-model",
		})
		if cmd == "" {
			t.Fatalf("%s: no curl command", tc.provider)
		}
		if strings.Contains(cmd, key) {
			t.Fatalf("%s: curl command contains the API key:\n%s", tc.provider, cmd)
		}
		if !strings.Contains(cmd, tc.header) {
			t.Fatalf("%s: curl command lacks %s:\n%s", tc.provider, tc.header, cmd)
		}
	}
}
