package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

// When the current brief's words are withheld, the reason is logged (once per brief
// and reason), so Today's "Last brief" line can be explained (R64).
func TestCurrentBriefWithheldReasonIsLogged(t *testing.T) {
	app, svc, withdrawn := newStoredMobileBriefPrivacyFixture(t)
	if svc.app == nil {
		svc.app = app
	}
	now := svc.now()
	older := buildMobileBriefV1(mobileBriefInput{
		AsOf: now.Add(-12 * time.Hour), WindowStart: now.Add(-13 * time.Hour), Slot: mobileBriefSlotEvening, Location: time.UTC,
		Learned: []mobileBriefCandidate{{SourceKind: "memory", SourceID: "safe-recall", Title: "Safe recall title", At: now.Add(-12 * time.Hour)}},
	})
	older.NarrativeStatus = "ok"
	older.Narrative = []MobileBriefPara{{Text: "Earlier words.", Refs: []string{older.Evidence[0].Ref}}}
	if err := svc.store.save(older, now); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&out)
	t.Cleanup(func() { log.SetOutput(previous) })
	currentBriefWithheldLogged = ""
	// The real protection path: it advances the privacy epoch and re-seals the
	// stored briefs before it returns, as a person's change does.
	if err := app.AddSensitiveApp("com.example.test", "TestApp"); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.CurrentMobileBrief()
	if got.BriefID != older.BriefID {
		t.Fatalf("current = %s, want the readable earlier brief %s (withdrawn %s)", got.BriefID, older.BriefID, withdrawn.BriefID)
	}
	line := out.String()
	if !strings.Contains(line, "the current brief "+withdrawn.BriefID+" is withheld on read: reason=") {
		t.Fatalf("withheld reason not logged: %q", line)
	}
	out.Reset()
	svc.CurrentMobileBrief()
	if strings.Contains(out.String(), "is withheld on read") {
		t.Fatalf("the same reason was logged again on the next read: %q", out.String())
	}
}
