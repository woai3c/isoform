package main

import (
	"os"
	"testing"
)

func aboutYouSavedFixture() (AboutYouView, []MemoryEntry) {
	return AboutYouView{Beliefs: []AboutYouBelief{{ID: "belief-1", Title: "Saved belief"}}},
		[]MemoryEntry{{ID: "belief-1", Title: "Saved belief", Content: "A saved belief sentence."}}
}

func TestAboutYouSavedViewIsServedOnlyUnderItsStamp(t *testing.T) {
	app := newSnapshotTestApp(t)
	adoptTestBriefPrivacyEpoch(app)
	briefPrivacyChanged()
	about, entries := aboutYouSavedFixture()
	if err := app.SaveAboutYouSavedView(app.GetSavedCopyStamp(), about, entries); err != nil {
		t.Fatalf("save under the current stamp: %v", err)
	}
	view, err := app.GetSavedAboutYou()
	if err != nil || view == nil || len(view.Entries) != 1 {
		t.Fatalf("the copy under its own stamp was not served: %+v, %v", view, err)
	}
	briefPrivacyChanged()
	if view, _ := app.GetSavedAboutYou(); view != nil {
		t.Fatalf("a copy served after the privacy stamp moved: %+v", view)
	}
	if _, err := os.Stat(aboutYouSavedViewPath()); !os.IsNotExist(err) {
		t.Fatalf("the stale copy was not deleted: %v", err)
	}
}

func TestAboutYouSaveRefusesAStampThatMoved(t *testing.T) {
	app := newSnapshotTestApp(t)
	adoptTestBriefPrivacyEpoch(app)
	briefPrivacyChanged()
	stamp := app.GetSavedCopyStamp()
	briefPrivacyChanged() // a forget lands while the reads run
	about, entries := aboutYouSavedFixture()
	if err := app.SaveAboutYouSavedView(stamp, about, entries); err == nil {
		t.Fatal("a save made under a moved stamp was accepted")
	}
	if _, err := os.Stat(aboutYouSavedViewPath()); !os.IsNotExist(err) {
		t.Fatalf("a refused save wrote a file: %v", err)
	}
}
