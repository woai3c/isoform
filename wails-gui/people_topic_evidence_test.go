package main

import "testing"

func TestPeopleTopicActivityMatchesBothNamedMembers(t *testing.T) {
	names := personNames("Michael Archibald", nil)
	for _, tc := range []struct {
		title, entities string
		want            bool
	}{
		{"Review isoform hub", `[{"type":"person","name":"Michael Archibald"}]`, true},
		{"Review salesrabbit-isoform", `[{"type":"person","name":"Michael Archibald"}]`, false},
		{"Review isoform hub", `[{"type":"person","name":"Someone Else"}]`, false},
		{"Review isoform hub", `invalid`, false},
	} {
		if got := peopleTopicActivityMatches(ActivityRow{Title: tc.title, Entities: tc.entities}, "isoform hub", names); got != tc.want {
			t.Errorf("%s: got %v", tc.title, got)
		}
	}
}

func TestPeopleTopicSourceRequiresReadableNamedActivity(t *testing.T) {
	a := peopleTopicsTestApp(t)
	peopleTopicsSeed(t, a, "shared", "Code", "", "Atlas release", Entity{Type: entityProject, Name: "Atlas"}, Entity{Type: entityPerson, Name: "Mei Chen"})
	peopleTopicsPublish(t, a)
	topics, err := a.peopleTopicsRegistry().ReadableWorkTopics(t.Context())
	if err != nil || len(topics) == 0 {
		t.Fatalf("topics = %+v, %v", topics, err)
	}
	if !a.peopleTopicSourceShared(t.Context(), topics[0], personNames("Mei Chen", nil)) {
		t.Fatal("readable source should establish the named work")
	}
	if a.peopleTopicSourceShared(t.Context(), topics[0], personNames("Someone Else", nil)) {
		t.Fatal("unmentioned person matched")
	}
}
