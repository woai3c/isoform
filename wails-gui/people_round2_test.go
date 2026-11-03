package main

import (
	"testing"
	"time"

	"wails-gui/internal/pii"
)

func TestPeopleRound2HiddenSourceLoopsKeepSourceGates(t *testing.T) {
	for _, chained := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "chained"}[chained], func(t *testing.T) {
			a := newTodayContextTestApp(t, true)
			original := pii.GetUserSensitiveApps()
			t.Cleanup(func() { pii.SetUserSensitiveApps(original) })
			pii.SetUserSensitiveApps(nil)
			if err := a.store.SetUIState(t.Context(), permissionUIStateKey(mailPermissionKey), "true"); err != nil {
				t.Fatal(err)
			}
			target := peopleTestContact(t, a, "Readable target")
			private := peopleTestContact(t, a, "Private source", "private source alias")
			privateRow, err := a.store.GetPrincipal(t.Context(), private.ID)
			if err != nil {
				t.Fatal(err)
			}
			privateRow.CreatedFrom = "com.example.private"
			if _, err := a.store.UpdatePrincipal(t.Context(), privateRow); err != nil {
				t.Fatal(err)
			}
			readable := peopleTestContact(t, a, "Readable source", "readable source alias")
			readableRow, err := a.store.GetPrincipal(t.Context(), readable.ID)
			if err != nil {
				t.Fatal(err)
			}
			readableRow.CreatedFrom = "auto_sender"
			if _, err := a.store.UpdatePrincipal(t.Context(), readableRow); err != nil {
				t.Fatal(err)
			}
			peopleTestActivity(t, a, readable, "readable-source-session", "Code", "Readable work", "done", time.Now().Add(-time.Hour).UnixMilli())
			for _, p := range []PrincipalDTO{private, readable} {
				peopleTestMoment(t, a, p, "waiting_on_reply", time.Now().Add(-48*time.Hour))
				peopleTestMoment(t, a, p, "person_reappeared", time.Now().Add(-time.Hour))
				peopleTestPurpose(t, a, p.ID+" own", p.DisplayName, PurposeOwnerUser, time.Time{})
				peopleTestPurpose(t, a, p.ID+" theirs", p.Aliases[0], PurposeOwnerOther, time.Time{})
			}
			peopleTestPurpose(t, a, "target control", target.DisplayName, PurposeOwnerUser, time.Time{})
			for _, source := range []PrincipalDTO{private, readable} {
				if _, err := a.MergePeople(target.ID, []string{source.ID}); err != nil {
					t.Fatal(err)
				}
			}
			targetID := target.ID
			if chained {
				final := peopleTestContact(t, a, "Final target")
				if _, err := a.MergePeople(final.ID, []string{target.ID}); err != nil {
					t.Fatal(err)
				}
				targetID = final.ID
			}

			detail, err := a.GetPersonDetail(targetID)
			if err != nil || len(detail.Loops) != 9 {
				t.Fatalf("readable control=%+v %v", detail.Loops, err)
			}
			// Protection changes after merging must govern the original source.
			pii.SetUserSensitiveApps(map[string]string{"com.example.private": "Private App"})
			if !personResearchAppProtected(privateRow.CreatedFrom) {
				t.Fatal("fixture origin is not protected")
			}
			detail, err = a.GetPersonDetail(targetID)
			if err != nil {
				t.Fatal(err)
			}
			if len(detail.Loops) != 5 {
				t.Fatalf("protected hidden loops carried to target: %+v", detail.Loops)
			}
			for _, loop := range detail.Loops {
				if loop.Title == private.ID+" own" || loop.Title == private.ID+" theirs" {
					t.Fatalf("private counterparty leaked: %+v", loop)
				}
			}
			overview, err := a.ListPeopleOverview()
			if err != nil || len(overview.Open) != 1 || len(overview.Open[0].Loops) != 5 {
				t.Fatalf("overview source gates=%+v %v", overview, err)
			}
			profile, err := a.store.GetPrincipal(t.Context(), targetID)
			if err != nil {
				t.Fatal(err)
			}
			topics, err := a.personSharedTopics(t.Context(), profile, fakePeopleTopics{
				{ID: "private", Name: "Private topic", Keys: []string{"person:" + threadNorm(private.Aliases[0])}},
				{ID: "public", Name: "Public topic", Keys: []string{"person:" + threadNorm(readable.Aliases[0])}},
			}, nil)
			if err != nil || len(topics) != 1 || topics[0].ID != "public" {
				t.Fatalf("source gates at registry seam=%+v %v", topics, err)
			}

		})
	}
}

func TestPeopleRound2MissingLoopOwnerKeepsReadablePeople(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	if err := a.store.SetUIState(t.Context(), permissionUIStateKey(mailPermissionKey), "true"); err != nil {
		t.Fatal(err)
	}
	p := peopleTestContact(t, a, "Readable")
	peopleTestMoment(t, a, p, "waiting_on_reply", time.Now().Add(-48*time.Hour))
	loops, err := a.peopleLoops(t.Context(), []PrincipalWithStatsDTO{{Principal: p}, {Principal: PrincipalDTO{ID: "missing"}}}, time.Now(), nil)
	if err != nil || len(loops[p.ID]) != 1 {
		t.Fatalf("unrelated missing row dropped readable loops: %+v %v", loops, err)
	}
}
