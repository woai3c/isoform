package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"wails-gui/storage"
)

func TestPeopleRound1LegacyMergeReturnsReceiptsAndRepeats(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	target := peopleTestContact(t, a, "Target")
	var receipts []string
	for _, name := range []string{"First", "Second"} {
		source := peopleTestContact(t, a, name)
		result, err := a.MergePrincipals(source.ID, target.ID)
		if err != nil {
			t.Fatalf("legacy %s merge: %v", name, err)
		}
		raw, _ := json.Marshal(result)
		var payload map[string]any
		json.Unmarshal(raw, &payload)
		id, _ := payload["mergeId"].(string)
		if id == "" {
			t.Errorf("legacy result discarded Undo ID: %s", raw)
		} else {
			receipts = append(receipts, id)
		}
	}
	for i := len(receipts) - 1; i >= 0; i-- {
		if err := a.UndoMerge(receipts[i]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPeopleRound1AllLoopsRespectProfileGates(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	if err := a.store.SetUIState(t.Context(), permissionUIStateKey(mailPermissionKey), "true"); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Hour).UnixMilli()
	for _, status := range []string{"done", "protected"} {
		p := peopleTestContact(t, a, "Person "+status)
		peopleTestActivity(t, a, p, "profile-"+status, "Code", "Work", "done", at)
		if err := a.activity.store.UpdateTriageStatus("profile-"+status, status); err != nil {
			t.Fatal(err)
		}
		peopleTestMoment(t, a, p, "waiting_on_reply", time.Now().Add(-48*time.Hour))
		peopleTestPurpose(t, a, "own-"+status, p.DisplayName, PurposeOwnerUser, time.Time{})
		peopleTestPurpose(t, a, "theirs-"+status, p.DisplayName, PurposeOwnerOther, time.Time{})
		detail, err := a.GetPersonDetail(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if status == "done" && len(detail.Loops) != 3 {
			t.Fatalf("readable loops dropped: %+v", detail.Loops)
		}
		if status == "protected" && len(detail.Loops) != 0 {
			t.Fatalf("private profile exposed loops: %+v", detail.Loops)
		}
	}
}

func TestPeopleRound1DuplicatesRequireCurrentContactsConsent(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	for _, email := range []string{"Person@example.test", "person@example.test"} {
		p, err := a.store.CreatePrincipal(t.Context(), storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: email, CreatedFrom: contactsSourceSystem, Source: contactsSourceSystem})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.store.BindAppHandle(t.Context(), p.ID, "email", email, email); err != nil {
			t.Fatal(err)
		}
	}
	overview, err := a.ListPeopleOverview()
	if err != nil {
		t.Fatal(err)
	}
	if len(overview.Duplicates) != 0 {
		t.Errorf("unconsented contact sources formed groups: %+v", overview.Duplicates)
	}
	path, err := contactsConsentPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := recordContactsConsent(path, "people", time.Now()); err != nil {
		t.Fatal(err)
	}
	overview, err = a.ListPeopleOverview()
	if err != nil || len(overview.Duplicates) != 1 {
		t.Fatalf("consented control=%+v %v", overview, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	overview, err = a.ListPeopleOverview()
	if err != nil || len(overview.Duplicates) != 0 {
		t.Fatalf("withdrawn consent=%+v %v", overview, err)
	}
}

func TestPeopleRound1ConsentWithdrawalChangesPublicationStamp(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	path, err := contactsConsentPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := recordContactsConsent(path, "people", time.Now()); err != nil {
		t.Fatal(err)
	}
	stamp, err := a.peopleReadStamp(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := a.validatePeopleRead(t.Context(), stamp); err != storage.ErrReadRevisionChanged {
		t.Fatalf("consent withdrawal published old groups: %v", err)
	}
}
