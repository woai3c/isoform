package main

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
)

func TestActivityInfoLogsContainMetadataOnly(t *testing.T) {
	isolateActivityPrivacyTest(t)
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)
	marker := "private-title-and-user-content"
	group := sessionGroup{Sessions: []ActivitySessionRow{{TriggerKind: "speech_idle_transition"}}}
	logSpeechMemoryTriage(group, "session-id", marker)
	logSpeechMemorySaved(group, "session-id", "created", marker, "/private/"+marker, 2)
	seq := annotated(coObs(0, "cmux", "github.com/Isoform/something", ""), coObs(1, "Chrome", "unrelated documentary", ""))
	event := extractedEvent{Title: marker, SourceIndices: []int{0, 1}}
	for _, remaining := range []int{0, 1} {
		_, err := applyCoherence(t.Context(), []extractedEvent{event}, seq, &coherenceBudget{remaining: remaining}, func(context.Context, ObservationSequence) ([]extractedEvent, error) {
			return []extractedEvent{{Title: marker, SourceIndices: []int{0}}, {Title: marker, SourceIndices: []int{1}}}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(logs.String(), marker) || !strings.Contains(logs.String(), "title_bytes=") || !strings.Contains(logs.String(), "session-id") {
		t.Fatalf("logs retained content or lost metadata: %s", logs.String())
	}
}
