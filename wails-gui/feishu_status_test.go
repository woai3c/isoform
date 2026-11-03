package main

import "testing"

func TestMaskFeishuAccountID(t *testing.T) {
	tests := []struct {
		name   string
		openID string
		want   string
	}{
		{name: "standard", openID: "ou_1234567890abcdef", want: "ou_…abcdef"},
		{name: "without prefix", openID: "123456789", want: "…456789"},
		{name: "short", openID: "ou_123", want: "ou_123"},
		{name: "trims whitespace", openID: "  ou_1234567  ", want: "ou_…234567"},
		{name: "empty", openID: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maskFeishuAccountID(tt.openID); got != tt.want {
				t.Errorf("maskFeishuAccountID(%q) = %q, want %q", tt.openID, got, tt.want)
			}
		})
	}
}
