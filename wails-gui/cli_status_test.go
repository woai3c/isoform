package main

import (
	"errors"
	"testing"
)

func TestCLILoggedInReadsTheStatusCommands(t *testing.T) {
	cases := []struct {
		name   string
		output string
		err    error
		want   bool
	}{
		{"claude signed in", "{\n  \"loggedIn\": true,\n  \"authMethod\": \"claude.ai\"\n}", nil, true},
		{"claude signed out", "{\"loggedIn\": false}", nil, false},
		{"codex signed in", "Logged in using ChatGPT", nil, true},
		{"codex signed out", "Not logged in", nil, false},
		{"command failed", "Logged in using ChatGPT", errors.New("exit status 1"), false},
		{"unknown output", "There's an issue with the selected model", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cliLoggedIn(tc.output, tc.err); got != tc.want {
				t.Fatalf("cliLoggedIn(%q, %v) = %v, want %v", tc.output, tc.err, got, tc.want)
			}
		})
	}
}
