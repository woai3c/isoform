package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"yansu-agent/agent"
)

func assertThreadOutcomeSecretsAbsent(t *testing.T, value string, secrets ...string) {
	t.Helper()
	for _, secret := range append(secrets, activityPrivacySecret) {
		if strings.Contains(value, secret) {
			t.Fatalf("secret %q survived the boundary", secret)
		}
	}
}

func TestThreadOutcomeWorkerRedactsSecretsAtProviderAndDelivery(t *testing.T) {
	for _, code := range []string{"Your code is 482913", "Your code is 482 913", "PIN 7392"} {
		t.Run(code, func(t *testing.T) {
			isolateActivityPrivacyTest(t)
			app := newPrivacyReadTestApp(t)
			now := mobileBriefTestDay(18, 0)
			control := "Room 12 draft"
			f := threadOutcomeFixture{Kind: "document", State: "incomplete", Action: "review",
				Frames:   []string{control + ". " + code + ". API key " + activityPrivacySecret},
				Typed:    control + ". " + code,
				Summary:  control + " is open. " + code + ". API key " + activityPrivacySecret,
				NextStep: "Review Room 12. " + code,
				Question: "Did you save Room 12 with " + code + "?"}
			f.UserActed = code // An unrelated response field must not carry text.
			row := seedThreadFixture(t, app, "secrets", f, now)
			row.Title = control + ". " + code + ". API key " + activityPrivacySecret
			if err := app.activity.store.UpdateActivity(row); err != nil {
				t.Fatal(err)
			}
			evidence, ok := app.threadOutcomeEvidence(t.Context(), row)
			if !ok || len(evidence) != 2 {
				t.Fatalf("missing readable OCR/typed evidence: %+v", evidence)
			}
			secret := strings.TrimPrefix(strings.TrimPrefix(code, "Your code is "), "PIN ")
			runner := &threadOutcomeTestRunner{reply: threadFixtureReply(f, evidence), check: func(input *agent.TaskInput) {
				assertThreadOutcomeSecretsAbsent(t, input.Prompt, secret)
				if !strings.Contains(input.Prompt, control) || !strings.Contains(input.Prompt, "[REDACTED:code]") || !strings.Contains(input.Prompt, "Thread label") {
					t.Fatal("provider lost readable controls or label")
				}
			}}
			app.runner = runner
			app.fillThreadOutcomes(t.Context(), now, time.UTC)
			if runner.calls != 1 {
				t.Fatalf("worker calls=%d", runner.calls)
			}
			saved, err := app.activity.store.GetActivityByID(row.ID)
			if err != nil || saved == nil || saved.ThreadOutcome == nil {
				t.Fatalf("missing accepted worker outcome: %+v %v", saved, err)
			}
			assertThreadOutcomeSecretsAbsent(t, saved.threadOutcomeRecord, secret)
			if !strings.Contains(saved.ThreadOutcome.Summary, control) || saved.ThreadOutcome.Question == "" || saved.ThreadOutcome.NextStep == "" || saved.ThreadOutcome.UserActed != "" {
				t.Fatal("redaction removed useful outcome text")
			}
			in := mobileBriefInput{Slot: "evening", AsOf: now, WindowStart: now.Add(-12 * time.Hour), Location: time.UTC}
			collectMobileBriefRecap(app, &in)
			brief := buildMobileBriefV1(in)
			if len(brief.Sections.Moved) != 1 {
				t.Fatal("safe outcome lost from brief")
			}
			// A narrative provider must not reintroduce a code in its reply.
			raw, _ := json.Marshal(mobileBriefNarrativeReply{Paragraphs: []mobileBriefNarrativeReplyParagraph{{Text: f.Summary, Refs: brief.Sections.Moved[0].Refs}}})
			brief.Narrative, err = parseMobileBriefNarrative(string(raw), brief.Evidence)
			if err != nil {
				t.Fatal(err)
			}
			store := newMobileBriefStore(t.TempDir())
			if err := store.save(brief, now); err != nil {
				t.Fatal(err)
			}
			loaded, ok := store.load(brief.BriefID)
			if !ok {
				t.Fatal("stored brief missing")
			}
			delivered := app.mobileBriefForDelivery(loaded)
			wire, _ := json.Marshal(delivered)
			assertThreadOutcomeSecretsAbsent(t, string(wire), secret)
			assertThreadOutcomeSecretsAbsent(t, mobileBriefTestPrompt(delivered, "English"), secret)
			assertThreadOutcomeSecretsAbsent(t, mobileBriefSpokenText(delivered, "English"), secret)
			if len(delivered.Sections.Moved) != 1 || !strings.Contains(string(wire), control) {
				t.Fatal("stored delivery lost readable outcome")
			}
		})
	}
}

func TestThreadOutcomeRedactsBeforeShortening(t *testing.T) {
	for _, value := range []string{"Your code is 482913", "Your code is 482 913", "PIN 7392", "API key " + activityPrivacySecret} {
		// Cutting inside a secret first could leave a prefix that neither
		// shared redactor recognizes. Only a mask may be shortened.
		for limit := 1; limit < len(value); limit++ {
			assertThreadOutcomeSecretsAbsent(t, threadOutcomeText(value, limit), "482", "913", "739", activityPrivacySecret[:12])
		}
		observation := threadOutcomeObservationText(strings.Repeat("earlier ", 400) + value)
		assertThreadOutcomeSecretsAbsent(t, observation, "482913", "482 913", "7392")
		if !strings.HasPrefix(observation, "[...] ") || !strings.Contains(observation, "[REDACTED:") {
			t.Fatal("observation tail lost its redaction")
		}
	}
}

func TestThreadOutcomeStorageAndReplayRedactOutput(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newPrivacyReadTestApp(t)
	now := mobileBriefTestDay(18, 0)
	f := threadOutcomeFixtures(t)[0]
	row := seedThreadFixture(t, app, "stored", f, now)
	evidence, _ := app.threadOutcomeEvidence(t.Context(), row)
	outcome, err := parseThreadOutcome(threadFixtureReply(f, evidence), evidence)
	if err != nil {
		t.Fatal(err)
	}
	outcome.Summary += " Your code is 482913"
	outcome.NextStep += " PIN 7392"
	outcome.Question = "Did you use code 482 913?"
	record := activityThreadOutcomeRecord{InputHash: threadOutcomeInputHash(row, evidence), AttemptedAt: now.UnixMilli(), Outcome: outcome}
	if err := app.activity.store.saveThreadOutcome(t.Context(), row, record); err != nil {
		t.Fatal(err)
	}
	saved, _ := app.activity.store.GetActivityByID(row.ID)
	assertThreadOutcomeSecretsAbsent(t, saved.threadOutcomeRecord, "482913", "482 913", "7392")
	// Simulate old bytes bypassing the write boundary. Store reads, canonical
	// replay, and source identity must use the fully redacted copy.
	raw, _ := json.Marshal(record)
	if _, err := app.activity.store.db.Exec(`UPDATE activities SET thread_outcome=? WHERE id=?`, string(raw), row.ID); err != nil {
		t.Fatal(err)
	}
	saved, _ = app.activity.store.GetActivityByID(row.ID)
	readable := app.readableThreadOutcome(t.Context(), *saved)
	if readable == nil || saved.ThreadOutcome == nil {
		t.Fatal("readable control withheld")
	}
	for _, value := range []*ActivityThreadOutcome{saved.ThreadOutcome, readable} {
		raw, _ := json.Marshal(value)
		assertThreadOutcomeSecretsAbsent(t, string(raw), "482913", "482 913", "7392")
	}
	if app.threadOutcomeSourceReadable(t.Context(), threadOutcomeSourceID(row.ID, outcome)) {
		t.Fatal("unredacted historical identity remained readable")
	}
	if !app.threadOutcomeSourceReadable(t.Context(), threadOutcomeSourceID(row.ID, readable)) {
		t.Fatal("redacted canonical identity unavailable")
	}
	// Even a cached display field with a current source must be scrubbed;
	// its old synthesis and recording cannot survive the changed text.
	in := mobileBriefInput{Slot: "evening", AsOf: now, WindowStart: now.Add(-12 * time.Hour), Location: time.UTC}
	collectMobileBriefRecap(app, &in)
	brief := buildMobileBriefV1(in)
	brief.Sections.Moved[0].Title = outcome.Summary
	brief.Sections.Moved[0].NextStep = outcome.NextStep
	brief.Sections.Moved[0].Outcome = outcome
	brief.Sections.Moved[0].Context = &MobileBriefItemContext{SourceLabel: "PIN 7392", Preview: "Your code is 482 913"}
	brief.Evidence[0].Label = outcome.Summary
	brief.Narrative = []MobileBriefPara{{Text: outcome.Summary, Refs: brief.Sections.Moved[0].Refs}}
	brief.Audio = &MobileBriefAudio{}
	delivered := app.mobileBriefForDelivery(brief)
	raw, _ = json.Marshal(delivered)
	assertThreadOutcomeSecretsAbsent(t, string(raw), "482913", "482 913", "7392")
	if len(delivered.Sections.Moved) != 1 || len(delivered.Narrative) != 0 || delivered.Audio != nil {
		t.Fatal("replay did not retain safe item and withdraw old synthesis/audio")
	}
}

func TestThreadOutcomeWithdrawsPreRedactionProjections(t *testing.T) {
	isolateActivityPrivacyTest(t)
	app := newPrivacyReadTestApp(t)
	now := mobileBriefTestDay(18, 0)
	f := threadOutcomeFixtures(t)[0]
	row := saveFixtureOutcome(t, app, seedThreadFixture(t, app, "legacy", f, now), f)
	var record activityThreadOutcomeRecord
	if err := json.Unmarshal([]byte(row.threadOutcomeRecord), &record); err != nil {
		t.Fatal(err)
	}
	record.Outcome.Version = 1
	record.Outcome.Summary += " Your code is 482913"
	sourceID := threadOutcomeSourceID(row.ID, record.Outcome)
	raw, _ := json.Marshal(record)
	if _, err := app.activity.store.db.Exec(`UPDATE activities SET thread_outcome=? WHERE id=?`, string(raw), row.ID); err != nil {
		t.Fatal(err)
	}
	// Keep even a matching current input hash: the extraction version alone
	// must invalidate pre-redaction projections and their delivery identities.
	saved, err := app.activity.store.GetActivityByID(row.ID)
	if err != nil || saved == nil {
		t.Fatal("legacy source missing", err)
	}
	row = *saved
	if row.ThreadOutcome != nil || app.readableThreadOutcome(t.Context(), row) != nil {
		t.Fatal("legacy outcome replayed")
	}
	brief := buildMobileBriefV1(mobileBriefInput{Slot: "evening", AsOf: now, WindowStart: now.Add(-12 * time.Hour), Location: time.UTC})
	brief.Evidence = []MobileBriefRef{{Ref: "e1", SourceKind: mobileBriefRecapSource, SourceID: sourceID, Label: record.Outcome.Summary}}
	brief.Sections.Moved = []MobileBriefItem{{Title: record.Outcome.Summary, Kind: mobileBriefThreadOutcomeSource, Outcome: record.Outcome, Refs: []string{"e1"}}}
	brief.Narrative = []MobileBriefPara{{Text: record.Outcome.Summary, Refs: []string{"e1"}}}
	brief.Audio = &MobileBriefAudio{}
	store := newMobileBriefStore(t.TempDir())
	if err := store.save(brief, now); err != nil {
		t.Fatal(err)
	}
	loaded, ok := store.load(brief.BriefID)
	if !ok {
		t.Fatal("legacy brief missing")
	}
	delivered := app.mobileBriefForDelivery(loaded)
	if len(delivered.Sections.Moved) != 0 || len(delivered.Evidence) != 0 || len(delivered.Narrative) != 0 || delivered.Audio != nil {
		t.Fatal("legacy brief retained outcome, evidence, synthesis or audio")
	}
	item := mainChatProactive{ActivityOutcomeSource: sourceID, Headline: record.Outcome.Summary, Body: record.Outcome.Summary}
	app.projectThreadOutcomeItem(t.Context(), &item, "en")
	raw, _ = json.Marshal(item)
	assertThreadOutcomeSecretsAbsent(t, string(raw), "482913")
	if strings.Contains(item.Body, "sailing gloves") {
		t.Fatal("legacy proactive copy retained")
	}
	// A stale record with an otherwise matching hash must be re-extracted.
	evidence, _ := app.threadOutcomeEvidence(t.Context(), row)
	runner := &threadOutcomeTestRunner{reply: threadFixtureReply(f, evidence)}
	app.runner = runner
	app.fillThreadOutcomes(t.Context(), now, time.UTC)
	saved, _ = app.activity.store.GetActivityByID(row.ID)
	if runner.calls != 1 || app.readableThreadOutcome(t.Context(), *saved) == nil {
		t.Fatal("legacy outcome was not replaced by a current extraction")
	}
}
