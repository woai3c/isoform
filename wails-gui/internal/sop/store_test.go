package sop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func TestSaveGetRoundTrip(t *testing.T) {
	s := newTestStore(t)
	in := Entry{
		ID:          "release-flow",
		Title:       "Release hotfix flow",
		Description: "Ship a hotfix from branch to production",
		Trigger:     "A P0 bug lands on main",
		Status:      StatusDraft,
		Source:      SourceTriage,
		SourceIDs:   []string{"grp-1", "grp-2"},
		Body:        "# Goal\n\nShip it.\n\n---\n\nA horizontal rule must not break parsing.\n",
	}
	saved, err := s.Save(in)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.Version != 1 || saved.CreatedAt == 0 || saved.UpdatedAt == 0 {
		t.Fatalf("Save defaults not applied: %+v", saved)
	}

	got, err := s.Get("release-flow")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != in.Title || got.Description != in.Description || got.Trigger != in.Trigger {
		t.Fatalf("scalar fields lost: %+v", got)
	}
	if got.Status != StatusDraft || got.Source != SourceTriage {
		t.Fatalf("status/source lost: %+v", got)
	}
	if len(got.SourceIDs) != 2 || got.SourceIDs[0] != "grp-1" {
		t.Fatalf("sourceIds lost: %+v", got.SourceIDs)
	}
	if got.Body != in.Body {
		t.Fatalf("body mismatch:\n%q\nvs\n%q", got.Body, in.Body)
	}
}

func TestParseBareBody(t *testing.T) {
	e := Parse("x", "no frontmatter here\n")
	if e.ID != "x" || e.Body != "no frontmatter here\n" {
		t.Fatalf("bare body not preserved: %+v", e)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Release Hotfix Flow":  "release-hotfix-flow",
		"  Weekly -- Report ":  "weekly-report",
		"发布流程":                 "sop",
		"Deploy v2.0 (prod)!!": "deploy-v2-0-prod",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUniqueSlugCollision(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(Entry{ID: "weekly-report", Title: "Weekly report"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got := s.UniqueSlug("Weekly Report"); got != "weekly-report-2" {
		t.Fatalf("UniqueSlug = %q, want weekly-report-2", got)
	}
}

func writeDraft(t *testing.T, body string, scripts map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	content := "---\ntitle: Drafted title\ndescription: Drafted description\ntrigger: Drafted trigger\n---\n\n" + body
	if err := os.WriteFile(filepath.Join(dir, "SOP.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, data := range scripts {
		path := filepath.Join(dir, "scripts", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestInstallBundleCreateAndUpdate(t *testing.T) {
	s := newTestStore(t)
	s.Now = func() time.Time { return time.UnixMilli(1000) }

	draft := writeDraft(t, "# Steps\n", map[string]string{"fetch.sh": "#!/bin/sh\n"})
	created, err := s.InstallBundle("weekly-report", draft, Entry{
		Status: StatusDraft, Source: SourceTriage, SourceIDs: []string{"grp-1"},
	})
	if err != nil {
		t.Fatalf("InstallBundle create: %v", err)
	}
	if created.Version != 1 || created.CreatedAt != 1000 || created.Title != "Drafted title" {
		t.Fatalf("create meta wrong: %+v", created)
	}
	if len(created.Scripts) != 1 || created.Scripts[0] != "scripts/fetch.sh" {
		t.Fatalf("scripts not discovered: %+v", created.Scripts)
	}

	s.Now = func() time.Time { return time.UnixMilli(2000) }
	draft2 := writeDraft(t, "# Steps v2\n", map[string]string{"push.sh": "#!/bin/sh\n"})
	updated, err := s.InstallBundle("weekly-report", draft2, Entry{
		Status: StatusDraft, Source: SourceTriage, SourceIDs: []string{"grp-1", "grp-2"},
	})
	if err != nil {
		t.Fatalf("InstallBundle update: %v", err)
	}
	if updated.Version != 2 || updated.CreatedAt != 1000 || updated.UpdatedAt != 2000 {
		t.Fatalf("update meta wrong: %+v", updated)
	}
	// Old scripts must be replaced, not merged.
	if len(updated.Scripts) != 1 || updated.Scripts[0] != "scripts/push.sh" {
		t.Fatalf("scripts not replaced: %+v", updated.Scripts)
	}
	if updated.Body != "# Steps v2\n" {
		t.Fatalf("body not updated: %q", updated.Body)
	}
}

func TestListSkipsNonBundles(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(Entry{ID: "real", Title: "Real"}); err != nil {
		t.Fatal(err)
	}
	// A stray dir without SOP.md and a dotdir must both be skipped.
	if err := os.MkdirAll(filepath.Join(s.Dir(), "not-a-bundle"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Dir(), ".hidden"), 0o700); err != nil {
		t.Fatal(err)
	}
	all, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 1 || all[0].ID != "real" {
		t.Fatalf("List = %+v, want only 'real'", all)
	}
}

func TestMutateBumpsUpdatedAt(t *testing.T) {
	s := newTestStore(t)
	s.Now = func() time.Time { return time.UnixMilli(1000) }
	if _, err := s.Save(Entry{ID: "a", Title: "A", Status: StatusDraft}); err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return time.UnixMilli(5000) }
	got, err := s.Mutate("a", func(e *Entry) { e.Status = StatusConfirmed })
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
	if got.Status != StatusConfirmed || got.UpdatedAt != 5000 || got.CreatedAt != 1000 {
		t.Fatalf("Mutate result wrong: %+v", got)
	}
}

func TestReadScript(t *testing.T) {
	s := newTestStore(t)
	draft := writeDraft(t, "# Steps\n", map[string]string{"fetch.sh": "#!/bin/sh\necho hi\n"})
	if _, err := s.InstallBundle("with-script", draft, Entry{Status: StatusDraft, Source: SourceManual}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReadScript("with-script", "scripts/fetch.sh")
	if err != nil {
		t.Fatalf("ReadScript: %v", err)
	}
	if got != "#!/bin/sh\necho hi\n" {
		t.Fatalf("script content mismatch: %q", got)
	}
	for _, bad := range []string{"../SOP.md", "scripts/../SOP.md", "SOP.md", "/etc/passwd", "scripts/../../evil"} {
		if _, err := s.ReadScript("with-script", bad); err == nil {
			t.Errorf("ReadScript accepted escaping path %q", bad)
		}
	}
	if _, err := s.ReadScript("../elsewhere", "scripts/fetch.sh"); err == nil {
		t.Error("ReadScript accepted escaping id")
	}
}

func TestParseUpdateExistingTransportOnly(t *testing.T) {
	e := Parse("x", "---\ntitle: T\nupdateExisting: weekly-report\n---\n\nbody\n")
	if e.UpdateExisting != "weekly-report" {
		t.Fatalf("updateExisting not parsed: %+v", e)
	}
	// The field must never round-trip into the persisted form.
	if strings.Contains(e.serialize(), "updateExisting") {
		t.Fatal("updateExisting leaked into serialized frontmatter")
	}
}

func TestDeleteRemovesBundle(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(Entry{ID: "gone", Title: "Gone"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("gone"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get("gone"); !os.IsNotExist(err) {
		t.Fatalf("bundle still readable after delete: %v", err)
	}
	if err := s.Delete("../evil"); err == nil {
		t.Fatal("Delete accepted a path-escaping id")
	}
}
