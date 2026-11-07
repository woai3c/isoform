package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// The walk reads this line on the real app: counts only, never note text.
func TestTopicHistoryProofCheckLogsCountsOnly(t *testing.T) {
	var logged bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logged)
	defer log.SetOutput(prev)
	logTopicHistoryProofs(map[string]bool{`{"fingerprint":"a"}`: true, `{"fingerprint":"b"}`: false})
	line := logged.String()
	if !strings.Contains(line, "[topic-history] checked proofs=2 kept=1 withheld=1") {
		t.Fatalf("unexpected line: %q", line)
	}
	if strings.Contains(line, "fingerprint") {
		t.Fatalf("the line carries proof text: %q", line)
	}
}
