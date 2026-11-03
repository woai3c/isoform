package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestTopicStepTimingEmitsFields(t *testing.T) {
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })

	done := timeTopicStep("today", "bootstrap_history")
	done("requested", 3, "loaded", 2)
	done("requested", 99)

	text := output.String()
	for _, field := range []string{"[topics] today level=info", "step=bootstrap_history", "elapsed_ms=", "requested=3", "loaded=2"} {
		if !strings.Contains(text, field) {
			t.Errorf("missing %q in timing log: %s", field, text)
		}
	}
	if strings.Count(text, "\n") != 1 {
		t.Fatalf("timing helper emitted more than one line: %s", text)
	}
}
