package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"wails-gui/storage"
)

// keptTextFixture is a brief as an earlier build stored it on 2026-10-20: the
// model's text ("ok"), its audio, offers and no cards, and the record of what
// its model read (the binding). The text read only the sources named by
// inputs; the brief also shows sources the text never read.
func keptTextFixture(t *testing.T, inputs ...string) (*App, *mobileBriefService, MobileBriefV1) {
	t.Helper()
	app, svc, brief := newStoredMobileBriefPrivacyFixture(t)
	binding := &mobileBriefModelBinding{Version: 2, Synthesis: "stored"}
	for _, ref := range brief.Evidence {
		for _, id := range inputs {
			if ref.SourceID == id {
				binding.Sources = append(binding.Sources, mobileBriefModelSource{Evidence: ref, Digest: "stored"})
			}
		}
	}
	if len(binding.Sources) != len(inputs) {
		t.Fatalf("inputs %v not in the fixture's evidence", inputs)
	}
	brief.modelBinding = binding
	brief.Offers = []MobileBriefOffer{{ID: "offer-1", Action: "prefill", Label: "Plan Safe recall", Prompt: "Plan the next steps for Safe recall.", Text: "Want me to plan the next steps for Safe recall?", Refs: brief.Narrative[0].Refs}}
	if err := svc.store.save(brief, svc.now()); err != nil {
		t.Fatal(err)
	}
	return app, svc, brief
}

func keptTextStored(t *testing.T, svc *mobileBriefService, briefID string) MobileBriefV1 {
	t.Helper()
	stored, ok := svc.store.load(briefID)
	if !ok {
		t.Fatal("the brief is no longer stored")
	}
	return stored
}

// On 2026-10-20 the 07:11 brief was stored with its text, and the first read
// after the 09:18 relaunch erased it: delivery withdrew the whole text when a
// source the text never read could not be read, and the Today read saved
// that. Now only the model's own inputs can withdraw its text, and a reason
// that passes never reaches the disk.
func TestStoredBriefKeepsItsTextAcrossARestart(t *testing.T) {
	t.Run("a source the text never read is gone", func(t *testing.T) {
		app, svc, brief := keptTextFixture(t, "safe-recall")
		if err := app.activity.store.DeleteSession("brief-delivery-source"); err != nil {
			t.Fatal(err)
		}
		latest, ok := svc.LatestMobileBrief()
		if !ok || len(latest.Narrative) != 1 || latest.NarrativeStatus != "ok" || latest.Audio == nil || len(latest.Offers) != 1 {
			t.Fatalf("the text left with a source it never read: %+v", latest)
		}
		data, _ := json.Marshal(latest)
		if strings.Contains(string(data), "brief-delivery-source") || strings.Contains(string(data), "Protected meeting title") {
			t.Fatal("the withdrawn source is still shown")
		}
		if stored := keptTextStored(t, svc, brief.BriefID); len(stored.Narrative) != 1 || stored.NarrativeStatus != "ok" {
			t.Fatal("the read erased the stored text")
		}
	})

	t.Run("an input of the text is withdrawn", func(t *testing.T) {
		app, svc, brief := keptTextFixture(t, "brief-delivery-source")
		logged := captureMobileBriefLog(t)
		if err := app.activity.store.DeleteSession("brief-delivery-source"); err != nil {
			t.Fatal(err)
		}
		latest, _ := svc.LatestMobileBrief()
		if len(latest.Narrative) != 0 || latest.NarrativeStatus == "ok" || latest.Audio != nil {
			t.Fatal("text written from a withdrawn source is still shown")
		}
		if stored := keptTextStored(t, svc, brief.BriefID); len(stored.Narrative) != 0 || stored.NarrativeStatus != "unavailable" {
			t.Fatal("text written from a withdrawn source stayed on disk")
		}
		if got := logged.String(); !strings.Contains(got, "withheld on read: reason=source_withdrawn kind=activity_session kept=false") || strings.Contains(got, "Protected") {
			t.Fatalf("log = %q", got)
		}
	})

	for _, condition := range []string{"activity store not open yet", "app database not open yet", "mail not readable"} {
		t.Run(condition, func(t *testing.T) {
			app, svc, brief := keptTextFixture(t, "brief-delivery-source", "safe-recall")
			logged := captureMobileBriefLog(t)
			restore := func() {}
			switch condition {
			case "activity store not open yet":
				activity := app.activity
				app.activity = nil
				restore = func() { app.activity = activity }
			case "app database not open yet":
				store := app.store
				app.store = nil
				restore = func() { app.store = store }
			case "mail not readable":
				original := mobileGoalMailRows
				mobileGoalMailRows = func(*App) ([]storage.MailMessageRow, error) { return nil, errors.New("mail is not open") }
				restore = func() { mobileGoalMailRows = original }
			}
			// A failed assertion below must not leave the replaced reader in place
			// for the rest of the package's tests.
			t.Cleanup(restore)
			// The approved rule (brief rebuild, PR 1): a sealed brief keeps showing
			// while a store is unavailable at read time. The words were checked
			// when they were sealed, and a read calls no store to hide them.
			latest, _ := svc.LatestMobileBrief()
			if len(latest.Narrative) != 1 || latest.NarrativeStatus != "ok" {
				t.Fatalf("a sealed brief hid its text while %s: %+v", condition, latest.Narrative)
			}
			restore()
			if stored := keptTextStored(t, svc, brief.BriefID); len(stored.Narrative) != 1 || stored.NarrativeStatus != "ok" || stored.Audio == nil {
				t.Fatalf("%s erased the stored text", condition)
			}
			if after, ok := svc.LatestMobileBrief(); !ok || len(after.Narrative) != 1 || after.NarrativeStatus != "ok" {
				t.Fatalf("the text did not come back after %s", condition)
			}
			if got := logged.String(); strings.Contains(got, "withheld on read") && !strings.Contains(got, "kept=true") {
				t.Fatalf("log = %q", got)
			}
		})
	}
}

// A rebuild never replaces a stored brief that has the model's text with one
// that does not.
func TestBriefRebuildKeepsTheWrittenText(t *testing.T) {
	_, svc, brief := cardRig(t)
	if brief.NarrativeStatus == "ok" {
		t.Fatal("the rig's model is expected to be unavailable")
	}
	stored := keptTextStored(t, svc, brief.BriefID)
	stored.NarrativeStatus = "ok"
	stored.Narrative = []MobileBriefPara{{Text: "The written morning text.", Refs: []string{stored.Evidence[0].Ref}}}
	if err := svc.store.save(stored, svc.now()); err != nil {
		t.Fatal(err)
	}
	logged := captureMobileBriefLog(t)
	rebuilt, err := svc.generate(context.Background(), mobileBriefSlotMorning, mobileBriefReasonPhone)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.BriefID != brief.BriefID || rebuilt.NarrativeStatus != "ok" || keptTextStored(t, svc, brief.BriefID).NarrativeStatus != "ok" {
		t.Fatalf("the rebuild replaced the written text: %+v", rebuilt.Narrative)
	}
	if !strings.Contains(logged.String(), "kept the stored morning brief") {
		t.Fatalf("log = %q", logged.String())
	}
}
