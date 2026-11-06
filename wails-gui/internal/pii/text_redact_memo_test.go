package pii

import (
	"fmt"
	"sync"
	"testing"
)

// The same text is scanned once; later reads of it reuse the memoized
// redaction, and the result is identical to a fresh scan.
func TestRedactTextMemoReusesScanPerText(t *testing.T) {
	resetRedactMemo()
	text := "Card 4111 1111 1111 1111 and mail someone@example.com for the invoice"
	first, n1 := RedactText(text)
	before := RedactTextCalls()
	for i := 0; i < 50; i++ {
		got, n := RedactText(text)
		if got != first || n != n1 {
			t.Fatalf("memoized read changed the result: %q/%d vs %q/%d", got, n, first, n1)
		}
	}
	if scans := RedactTextCalls() - before; scans != 0 {
		t.Fatalf("repeated reads of one text scanned %d times, want 0", scans)
	}
	if n1 == 0 {
		t.Fatal("test text should contain redactable PII")
	}
}

// A rule version change invalidates every memoized redaction.
func TestRedactTextMemoIsKeyedOnRuleVersion(t *testing.T) {
	resetRedactMemo()
	text := "reach me at someone@example.com"
	RedactText(text)
	before := RedactTextCalls()
	saved := redactRuleVersion.Load()
	t.Cleanup(func() { redactRuleVersion.Store(saved) })
	redactRuleVersion.Store(saved + 1)
	RedactText(text)
	if scans := RedactTextCalls() - before; scans != 1 {
		t.Fatalf("a new rule version scanned %d times, want 1", scans)
	}
}

// Long texts bypass the memo, so the bounded memo never holds them.
func TestRedactTextMemoSkipsLongText(t *testing.T) {
	resetRedactMemo()
	long := fmt.Sprintf("%0*d someone@example.com", redactMemoMaxText, 1)
	RedactText(long)
	before := RedactTextCalls()
	RedactText(long)
	if scans := RedactTextCalls() - before; scans != 1 {
		t.Fatalf("a long text should scan on every read, scanned %d times", scans)
	}
}

// Concurrent readers and a rule-version change must not race on the memo.
func TestRedactTextMemoConcurrentReadersAreRaceFree(t *testing.T) {
	resetRedactMemo()
	saved := redactRuleVersion.Load()
	t.Cleanup(func() { redactRuleVersion.Store(saved) })
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if i%50 == 0 && w == 0 {
					redactRuleVersion.Store(saved + uint64(i))
				}
				text := fmt.Sprintf("note %d mail user%d@example.com", i%7, w%3)
				out, _ := RedactText(text)
				if out == text {
					t.Errorf("email was not redacted: %q", out)
					return
				}
			}
		}(w)
	}
	wg.Wait()
}
