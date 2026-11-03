package native

import (
	"testing"
	"time"
)

func TestParseRetryAfterIsBounded(t *testing.T) {
	if got := parseRetryAfter("9999999999"); got != maxRetryAfter {
		t.Fatalf("a huge Retry-After must clamp to %s, got %s", maxRetryAfter, got)
	}
	if got := parseRetryAfter("30"); got != 30*time.Second {
		t.Fatalf("Retry-After 30 = %s", got)
	}
	if got := parseRetryAfter("-5"); got != 0 {
		t.Fatalf("a negative Retry-After must be ignored, got %s", got)
	}
}
