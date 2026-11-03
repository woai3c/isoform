package native

import (
	"testing"
	"time"
)

func TestIsTransientAPIError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"non-HTTP", errForTest("network failure"), false},
		{"400", &HTTPError{Status: 400}, false},
		{"401", &HTTPError{Status: 401}, false},
		{"403", &HTTPError{Status: 403}, false},
		{"429", &HTTPError{Status: 429}, true},
		{"500", &HTTPError{Status: 500}, true},
		{"502", &HTTPError{Status: 502}, true},
		{"503", &HTTPError{Status: 503}, true},
		{"504", &HTTPError{Status: 504}, true},
		{"529", &HTTPError{Status: 529}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientAPIError(tc.err); got != tc.want {
				t.Errorf("isTransientAPIError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsModelUnavailableError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"non-HTTP", errForTest("network failure"), false},
		{"429", &HTTPError{Status: 429, Body: "rate limited"}, false},
		{"503_generic", &HTTPError{Status: 503, Body: "service unavailable"}, false},
		{"503_no_available_accounts", &HTTPError{Status: 503, Body: `{"error":"no available accounts for model"}`}, true},
		{"503_no_available_account_singular", &HTTPError{Status: 503, Body: "No available account"}, true},
		{"503_mixed_case", &HTTPError{Status: 503, Body: "No Available Accounts"}, true},
		{"500_no_accounts", &HTTPError{Status: 500, Body: "no available accounts"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isModelUnavailableError(tc.err); got != tc.want {
				t.Errorf("isModelUnavailableError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestModelFallbackTargetFor(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		{"haiku_alias", "haiku", modelFallbackSonnet},
		{"haiku_pinned", "claude-haiku-4-5-20251001", modelFallbackSonnet},
		{"sonnet_alias", "sonnet", modelFallbackOpus},
		{"sonnet_pinned", "claude-sonnet-4-6", modelFallbackOpus},
		{"opus_no_fallback", "claude-opus-4-6", ""},
		{"custom_no_fallback", "gpt-4o", ""},
		{"empty_no_fallback", "", ""},
		{"mixed_case", "Claude-Haiku-4-5", modelFallbackSonnet},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := modelFallbackTargetFor(tc.model); got != tc.want {
				t.Errorf("modelFallbackTargetFor(%q) = %q, want %q", tc.model, got, tc.want)
			}
		})
	}
}

func TestAPICircuitBreakerBackoff(t *testing.T) {
	tests := []struct {
		streak int
		want   time.Duration
	}{
		{0, 10 * time.Second},
		{1, 10 * time.Second},
		{2, 20 * time.Second},
		{3, 40 * time.Second},
		{4, 60 * time.Second},
		{5, 60 * time.Second},
		{10, 60 * time.Second},
	}
	for _, tc := range tests {
		t.Run("", func(t *testing.T) {
			got := apiCircuitBreakerBackoff(tc.streak)
			if got != tc.want {
				t.Errorf("apiCircuitBreakerBackoff(%d) = %v, want %v", tc.streak, got, tc.want)
			}
		})
	}
}

type errForTest string

func (e errForTest) Error() string { return string(e) }
