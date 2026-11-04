package main

import (
	"errors"
	"testing"
	"time"
)

func TestGetPersonBriefFillsBeforeYouTalkLinesByPerson(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	person := peopleTestContact(t, a, "Mei Chen")
	other := peopleTestContact(t, a, "Dana")
	peopleTestPurpose(t, a, "Send the Meridian memo outline", person.DisplayName, PurposeOwnerUser, time.Time{})
	peopleTestPurpose(t, a, "Book the offsite", other.DisplayName, PurposeOwnerUser, time.Time{})

	brief, err := a.GetPersonBrief(person.ID)
	if err != nil {
		t.Fatal(err)
	}
	if brief == nil || brief.PersonID != person.ID || brief.YouOwe != "Send the Meridian memo outline" {
		t.Fatalf("brief=%+v", brief)
	}
	if brief.TheyAsked != "" || brief.LastTime != "" {
		t.Fatalf("empty lines must stay empty: %+v", brief)
	}
	if _, err := a.GetPersonBrief(""); err == nil {
		t.Fatal("a brief without a person must fail")
	}
}

func TestOpenPersonReplyDraftOnlyForThisPersonsReplyOwed(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	person := peopleTestContact(t, a, "Mei Chen")
	other := peopleTestContact(t, a, "Dana")
	peopleTestMoment(t, a, other, "waiting_on_reply", time.Now().Add(-2*time.Hour))
	detail, err := a.GetPersonDetail(other.ID)
	if err != nil {
		t.Fatal(err)
	}
	moment := ""
	for _, loop := range detail.Loops {
		if loop.Kind == "reply_owed" {
			moment = loop.ID
		}
	}
	if moment == "" {
		t.Fatalf("no reply owed for the other person: %+v", detail.Loops)
	}
	if _, err := a.OpenPersonReplyDraft(person.ID, moment, "run-person-draft"); !errors.Is(err, errTodayReplyDraftUnavailable) {
		t.Fatalf("another person's reply must not open a draft: %v", err)
	}
	if _, err := a.OpenPersonReplyDraft(person.ID, "no-such-moment", "run-person-draft-2"); !errors.Is(err, errTodayReplyDraftUnavailable) {
		t.Fatalf("an unknown moment must not open a draft: %v", err)
	}
}
