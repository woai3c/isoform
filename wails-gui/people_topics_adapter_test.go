package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/pii"
)

func peopleTopicsTestApp(t *testing.T) *App {
	t.Helper()
	a := newTodayContextTestApp(t, true)
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	configureTopicRegistryTest(a, time.Now())
	before := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(before) })
	pii.SetUserSensitiveApps(nil)
	return a
}

func peopleTopicsSeed(t *testing.T, a *App, id, app, bundle, title string, entities ...Entity) {
	t.Helper()
	end := time.Now().Add(-30 * time.Minute)
	seedThreadMoment(t, a, threadFixtureMoment{
		id: id, title: title, app: app, bundle: bundle,
		start: end.Add(-30 * time.Minute), end: end, entities: entities,
	})
}

// peopleTopicsPublish commits the registry snapshot the adapter reads.
func peopleTopicsPublish(t *testing.T, a *App) {
	t.Helper()
	if _, err := collectAndReadTopicViewsForTest(a); err != nil {
		t.Fatal(err)
	}
}

func personTopicNames(detail PersonDetail) []string {
	names := []string{}
	for _, topic := range detail.SharedTopics {
		names = append(names, topic.Name)
	}
	return names
}

func TestPeopleTopicsAdapterSharesReadableTopics(t *testing.T) {
	a := peopleTopicsTestApp(t)
	p := peopleTestContact(t, a, "Mei Chen")
	peopleTopicsSeed(t, a, "atlas", "Code", "", "Atlas launch plan",
		Entity{Type: entityProject, Name: "Atlas"}, Entity{Type: entityPerson, Name: "Mei Chen"})
	peopleTopicsSeed(t, a, "beacon", "Code", "", "Beacon budget review",
		Entity{Type: entityProject, Name: "Beacon"}, Entity{Type: entityPerson, Name: "Mei Chen"})
	peopleTopicsSeed(t, a, "other", "Code", "", "Other project",
		Entity{Type: entityProject, Name: "Other"}, Entity{Type: entityPerson, Name: "Someone Else"})
	peopleTopicsPublish(t, a)

	detail, err := a.GetPersonDetail(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	names := personTopicNames(detail)
	sort.Strings(names)
	if len(names) != 2 || names[0] != "Atlas" || names[1] != "Beacon" {
		t.Fatalf("shared topics = %v, want [Atlas Beacon]", names)
	}
	for _, topic := range detail.SharedTopics {
		if topic.ID == "" {
			t.Fatalf("topic without registry id: %+v", topic)
		}
	}
}

func TestPeopleTopicsAdapterListsPeopleBeyondTheDisplayCap(t *testing.T) {
	a := peopleTopicsTestApp(t)
	// topicRegistryPeopleMax truncates PeopleLine to three display names; the
	// fourth member alphabetically must still match the shared topic.
	members := []string{"Ava Hart", "Bo Lin", "Cy Rao", "Mei Chen", "Zoe Quinn"}
	entities := []Entity{{Type: entityProject, Name: "Atlas"}}
	for _, name := range members {
		entities = append(entities, Entity{Type: entityPerson, Name: name})
	}
	peopleTopicsSeed(t, a, "atlas", "Code", "", "Atlas launch plan", entities...)
	peopleTopicsPublish(t, a)
	views, err := a.todayTopicViews()
	if err != nil || len(views) != 1 {
		t.Fatalf("views = %+v, %v", views, err)
	}
	if views[0].PeopleLine == nil || views[0].PeopleLine.Count != 5 || len(views[0].PeopleLine.Names) != topicRegistryPeopleMax {
		t.Fatalf("display line is not the capped projection this test guards: %+v", views[0].PeopleLine)
	}
	for _, name := range members {
		p := peopleTestContact(t, a, name)
		detail, err := a.GetPersonDetail(p.ID)
		if err != nil {
			t.Fatal(err)
		}
		if names := personTopicNames(detail); len(names) != 1 || names[0] != "Atlas" {
			t.Fatalf("%s shared topics = %v, want [Atlas]", name, names)
		}
	}
}

func TestPeopleTopicsAdapterMatchesMergedAlias(t *testing.T) {
	a := peopleTopicsTestApp(t)
	target := peopleTestContact(t, a, "Mei Chen")
	source := peopleTestContact(t, a, "M Chen")
	if _, err := a.MergePeople(target.ID, []string{source.ID}); err != nil {
		t.Fatal(err)
	}
	peopleTopicsSeed(t, a, "atlas", "Code", "", "Atlas launch plan",
		Entity{Type: entityProject, Name: "Atlas"}, Entity{Type: entityPerson, Name: "M Chen"})
	peopleTopicsPublish(t, a)

	detail, err := a.GetPersonDetail(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if names := personTopicNames(detail); len(names) != 1 || names[0] != "Atlas" {
		t.Fatalf("merged alias topics = %v, want [Atlas]", names)
	}
}

func TestPeopleTopicsAdapterClosedEyesYieldsNone(t *testing.T) {
	a := peopleTopicsTestApp(t)
	p := peopleTestContact(t, a, "Mei Chen")
	peopleTopicsSeed(t, a, "atlas", "Code", "", "Atlas launch plan",
		Entity{Type: entityProject, Name: "Atlas"}, Entity{Type: entityPerson, Name: "Mei Chen"})
	peopleTopicsPublish(t, a)
	if topics, err := (registryPeopleWorkTopics{a}).ReadableWorkTopics(t.Context()); err != nil || len(topics) != 1 {
		t.Fatalf("readable control = %+v %v", topics, err)
	}
	a.closedEyes.enabled.Store(true)
	if topics, err := (registryPeopleWorkTopics{a}).ReadableWorkTopics(t.Context()); err != nil || len(topics) != 0 {
		t.Fatalf("closed eyes registry = %+v %v", topics, err)
	}
	detail, err := a.GetPersonDetail(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.SharedTopics) != 0 {
		t.Fatalf("closed eyes topics = %+v", detail.SharedTopics)
	}
}

func TestPeopleTopicsAdapterNeverReturnsPrivateOrSensitiveTopics(t *testing.T) {
	a := peopleTopicsTestApp(t)
	pii.SetUserSensitiveApps(map[string]string{"com.example.private": "Private Notes"})
	p := peopleTestContact(t, a, "Mei Chen")
	peopleTopicsSeed(t, a, "atlas", "Code", "", "Atlas launch plan",
		Entity{Type: entityProject, Name: "Atlas"}, Entity{Type: entityPerson, Name: "Mei Chen"})
	peopleTopicsSeed(t, a, "sensitive", "Private Notes", "com.example.private", "Secret project notes",
		Entity{Type: entityProject, Name: "Secret project"}, Entity{Type: entityPerson, Name: "Mei Chen"})
	peopleTopicsSeed(t, a, "private-window", "Chrome", "com.google.Chrome", "Banking secret - Incognito",
		Entity{Type: entityProject, Name: "Banking secret"}, Entity{Type: entityPerson, Name: "Mei Chen"})
	peopleTopicsPublish(t, a)

	detail, err := a.GetPersonDetail(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if names := personTopicNames(detail); len(names) != 1 || names[0] != "Atlas" {
		t.Fatalf("topics = %v, want [Atlas]", names)
	}
	raw, _ := json.Marshal(detail)
	for _, secret := range []string{"Secret project", "Banking secret", "Private Notes"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("private topic leaked: %s", raw)
		}
	}
}

type failingPeopleTopics struct{}

func (failingPeopleTopics) ReadableWorkTopics(context.Context) ([]peopleWorkTopic, error) {
	return nil, errTopicRegistryChanged
}

func TestPersonSharedTopicsReadFailureKeepsTheDetail(t *testing.T) {
	a := peopleTopicsTestApp(t)
	p := peopleTestContact(t, a, "Mei Chen")
	at := time.Now().Add(-time.Hour).UnixMilli()
	peopleTestActivity(t, a, p, "kept", "Code", "Readable work", "done", at)
	if err := a.store.BumpPrincipalSighting(t.Context(), p.ID, "wechat", "", at, "preview never in detail"); err != nil {
		t.Fatal(err)
	}
	a.peopleTopics = failingPeopleTopics{}

	detail, err := a.GetPersonDetail(p.ID)
	if err != nil {
		t.Fatalf("topics read failure took the detail down: %v", err)
	}
	if len(detail.SharedTopics) != 0 {
		t.Fatalf("failed topics read produced topics: %+v", detail.SharedTopics)
	}
	if len(detail.Together) != 2 || detail.MessageCount != 2 || len(detail.SeenIn) != 2 || detail.Name != "Mei Chen" {
		t.Fatalf("the rest of the detail changed: %+v", detail)
	}
	if _, err := a.peopleTopicsRegistry().ReadableWorkTopics(t.Context()); !errors.Is(err, errTopicRegistryChanged) {
		t.Fatalf("injected seam did not fail: %v", err)
	}
}
