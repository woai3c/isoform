package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

func TestNoticedReadsAreCountedByTheirChainAndLoggedOncePerMinute(t *testing.T) {
	var logged bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logged)
	defer log.SetOutput(prev)
	noticedReadCounts.Lock()
	noticedReadCounts.byChain = map[string]int{}
	noticedReadCounts.since = time.Now().Add(-2 * noticedReadLogEvery)
	noticedReadCounts.Unlock()

	countNoticedRead()
	countNoticedRead()
	if !strings.Contains(logged.String(), "[noticed-pass] minute reads=") {
		t.Fatalf("the minute summary was not logged: %q", logged.String())
	}
	if !strings.Contains(logged.String(), "TestNoticedReadsAreCountedByTheirChainAndLoggedOncePerMinute") {
		t.Fatalf("the summary does not name the read's caller: %q", logged.String())
	}
}
