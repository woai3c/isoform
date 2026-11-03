package main

import (
	"testing"

	"wails-gui/internal/pii"
)

func TestScrubSensitiveAppNames(t *testing.T) {
	pii.SetUserSensitiveApps(map[string]string{
		"com.apple.journal": "手记",
	})
	defer pii.SetUserSensitiveApps(map[string]string{})

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "removes protected name",
			in:   `["kitty","手记","Google Chrome"]`,
			want: `["kitty","Google Chrome"]`,
		},
		{
			name: "no protected names unchanged content",
			in:   `["kitty","Figma"]`,
			want: `["kitty","Figma"]`,
		},
		{
			name: "malformed JSON fails closed to empty list",
			in:   `not-json 手记`,
			want: `[]`,
		},
		{
			name: "empty input fails closed",
			in:   ``,
			want: `[]`,
		},
		{
			name: "all names protected leaves empty list",
			in:   `["手记"]`,
			want: `[]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scrubSensitiveAppNames(tt.in); got != tt.want {
				t.Errorf("scrubSensitiveAppNames(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
