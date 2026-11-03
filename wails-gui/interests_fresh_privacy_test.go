package main

import (
	"testing"
	"time"
	"wails-gui/internal/pii"
)

func TestFreshInterestCollectionChecksOriginalEvidence(t *testing.T) {
	for _, kind := range []string{"activity", "semantic", "purpose"} {
		t.Run(kind, func(t *testing.T) {
			app := newMobileBriefPurposeTestApp(t)
			isolateActivityPrivacyTest(t)
			now := time.Now()
			at := now.Add(-time.Hour)
			store := app.activity.store
			source := seedMobileBriefPurposeRun(t, app, "interest-source", 1, at, 60000, []string{"Diary"})
			switch kind {
			case "activity":
				if err := store.InsertActivity(ActivityRow{ID: "fresh-derived", Title: "Optical camera project", ActiveMsByApp: `{"Notes":60000}`, RunIDs: `["interest-source"]`, StartMs: at.UnixMilli(), EndMs: at.Add(time.Minute).UnixMilli()}); err != nil {
					t.Fatal(err)
				}
			case "semantic":
				if err := store.InsertSemanticEvent(SemanticEventRow{ID: "fresh-derived", Title: "Optical camera project", Apps: `["Notes"]`, SourceRefs: `{"session_ids":["` + source + `"]}`, StartMs: at.UnixMilli(), EndMs: at.Add(time.Minute).UnixMilli()}); err != nil {
					t.Fatal(err)
				}
			case "purpose":
				mobileTestPurpose(t, NewPurposeMapStore(store.db), "fresh-derived", PurposeKindGoal, "Optical camera project", false, func(input *CreatePurposeOpenLoopInput) {
					e := input.Evidence[0]
					e.SourceKind = "activity_session"
					e.SourceID = source
					e.SourceObservedAt = at.UnixMilli()
					e.EvidenceClass = PurposeEvidenceObservedUser
					e.IndependenceKey = source
					input.Evidence = append(input.Evidence, e)
				})
			}
			present := func(in interestInput) bool {
				switch kind {
				case "activity":
					return len(in.Activities) > 0
				case "semantic":
					return len(in.Semantic) > 0
				default:
					return len(in.Goals) > 0
				}
			}
			if !present(collectInterestInput(app, now.Add(-24*time.Hour), now)) {
				t.Fatal("readable fresh control missing")
			}
			pii.AddUserSensitiveApp("com.example.diary", "Diary")
			in := collectInterestInput(app, now.Add(-24*time.Hour), now)
			if present(in) {
				t.Fatalf("restricted original reintroduced via %s metadata", kind)
			}
			if len(in.Runs) != 0 {
				t.Fatal("restricted original reintroduced through named run metadata")
			}
		})
	}
}
