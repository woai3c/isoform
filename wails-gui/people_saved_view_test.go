package main

import (
	"os"
	"strings"
	"testing"
)

func peopleSavedFixture() (PeopleOverview, []PrincipalWithStatsDTO) {
	overview := PeopleOverview{Everyone: []PersonCard{{Name: "Saved Person"}}}
	entries := []PrincipalWithStatsDTO{{
		Principal: PrincipalDTO{DisplayName: "Saved Person"},
		Stats:     PrincipalStatsDTO{RecentMessages: []PrincipalRecentMessageDTO{{App: "Mail", Preview: "private words"}}},
	}}
	return overview, entries
}

func TestPeopleSavedViewIsServedOnlyUnderItsStamp(t *testing.T) {
	app := newSnapshotTestApp(t)
	adoptTestBriefPrivacyEpoch(app)
	briefPrivacyChanged()
	stamp := app.GetSavedCopyStamp()
	overview, entries := peopleSavedFixture()
	if err := app.SavePeopleSavedView(stamp, overview, entries); err != nil {
		t.Fatalf("save under the current stamp: %v", err)
	}
	view, err := app.GetSavedPeopleView()
	if err != nil || view == nil || len(view.Overview.Everyone) != 1 {
		t.Fatalf("the copy under its own stamp was not served: %+v, %v", view, err)
	}
	briefPrivacyChanged()
	if view, _ := app.GetSavedPeopleView(); view != nil {
		t.Fatalf("a copy served after the privacy stamp moved: %+v", view)
	}
	if _, err := os.Stat(peopleSavedViewPath()); !os.IsNotExist(err) {
		t.Fatalf("the stale copy was not deleted: %v", err)
	}
}

func TestPeopleSavedViewSaveRefusesAStampThatMoved(t *testing.T) {
	app := newSnapshotTestApp(t)
	adoptTestBriefPrivacyEpoch(app)
	briefPrivacyChanged()
	stamp := app.GetSavedCopyStamp()
	briefPrivacyChanged() // a withdrawal lands while the reads run
	overview, entries := peopleSavedFixture()
	if err := app.SavePeopleSavedView(stamp, overview, entries); err == nil {
		t.Fatal("a save made under a moved stamp was accepted")
	}
	if _, err := os.Stat(peopleSavedViewPath()); !os.IsNotExist(err) {
		t.Fatalf("a refused save wrote a file: %v", err)
	}
}

func TestPeopleSavedViewNeverStoresMessageText(t *testing.T) {
	app := newSnapshotTestApp(t)
	adoptTestBriefPrivacyEpoch(app)
	briefPrivacyChanged()
	overview, entries := peopleSavedFixture()
	if err := app.SavePeopleSavedView(app.GetSavedCopyStamp(), overview, entries); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(peopleSavedViewPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private words") {
		t.Fatal("message text was written to the saved people view")
	}
}
