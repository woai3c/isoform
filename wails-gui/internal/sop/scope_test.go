package sop

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestScopeTopicFrontmatterRoundTrip(t *testing.T) {
	for _, scope := range []string{"", "mine", "topic", "team", "company"} {
		t.Run(scope, func(t *testing.T) {
			s := newTestStore(t)
			in := Entry{ID: "entry", Scope: scope, TopicIDs: []string{"topic:launch", "opaque/registry/id"}, Body: "Keep the steps.\n"}
			saved, err := s.Save(in)
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.Get(in.ID)
			if err != nil || got.Scope != saved.Scope || got.Version != saved.Version || got.Body != in.Body || !reflect.DeepEqual(got.TopicIDs, in.TopicIDs) {
				t.Fatalf("round-trip: %+v, %v", got, err)
			}
			want := scope
			if want == "" {
				want = "mine"
			}
			data, err := os.ReadFile(s.SOPPath(in.ID))
			if err != nil || got.Scope != want || !strings.Contains(string(data), "scope: "+want+"\n") {
				t.Fatalf("scope not persisted: %s, %v", data, err)
			}
		})
	}
}

func TestOldBundleDefaultsToMineWithoutWriting(t *testing.T) {
	s := newTestStore(t)
	if err := os.MkdirAll(s.PathFor("old"), 0700); err != nil {
		t.Fatal(err)
	}
	old := "---\ntitle: Old bundle\n---\n\nOriginal steps.\n"
	if err := os.WriteFile(s.SOPPath("old"), []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("old")
	if err != nil || got.Scope != "mine" || len(got.TopicIDs) != 0 {
		t.Fatalf("legacy read: %+v, %v", got, err)
	}
	data, _ := os.ReadFile(s.SOPPath("old"))
	if string(data) != old {
		t.Fatal("legacy read rewrote frontmatter")
	}
}

func TestScopeAndTopicValidation(t *testing.T) {
	s := newTestStore(t)
	for _, in := range []Entry{
		{ID: "bad", Scope: "public"}, {ID: "bad", Scope: "Mine"}, {ID: "bad", Scope: "team\nstatus: confirmed"},
		{ID: "bad", TopicIDs: []string{""}}, {ID: "bad", TopicIDs: []string{" \t"}}, {ID: "bad", TopicIDs: []string{strings.Repeat("x", 2049)}},
	} {
		if _, err := s.Save(in); err == nil {
			t.Fatalf("accepted invalid entry: %+v", in)
		}
		if _, err := os.Stat(s.PathFor("bad")); !os.IsNotExist(err) {
			t.Fatal("invalid save mutated store")
		}
	}
	for _, header := range []string{"scope: invalid", "topicIds: broken", "topicIds: [1]", "topicIds: [\"\"]"} {
		if err := os.MkdirAll(s.PathFor("invalid"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(s.SOPPath("invalid"), []byte("---\n"+header+"\n---\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get("invalid"); err == nil {
			t.Fatalf("read accepted invalid frontmatter: %s", header)
		}
	}
}

func TestImportScopePolicy(t *testing.T) {
	for _, scope := range []string{"mine", "topic", "team", "company"} {
		for _, confirmed := range []bool{false, true} {
			t.Run(scope+"/"+map[bool]string{false: "default", true: "confirmed"}[confirmed], func(t *testing.T) {
				s := newTestStore(t)
				calls := 0
				if confirmed {
					s.ImportScopePolicy = func(requested string) bool { calls++; return requested == scope }
				}
				draft := t.TempDir()
				if err := os.WriteFile(filepath.Join(draft, sopFileName), []byte((Entry{Scope: scope, TopicIDs: []string{"topic-1"}, Body: "Steps"}).serialize()), 0600); err != nil {
					t.Fatal(err)
				}
				// Even prefilled metadata cannot bypass the default import policy.
				got, err := s.InstallBundle("import", draft, Entry{Source: SourceImported, Scope: "company"})
				want := "mine"
				if confirmed {
					want = scope
				}
				if err != nil || got.Scope != want || !reflect.DeepEqual(got.TopicIDs, []string{"topic-1"}) || (confirmed && scope != "mine" && calls != 1) {
					t.Fatalf("import: %+v, policy calls=%d, %v", got, calls, err)
				}
			})
		}
	}
	// An explicit denial behaves just like the default policy.
	s := newTestStore(t)
	s.ImportScopePolicy = func(string) bool { return false }
	draft := t.TempDir()
	if err := os.WriteFile(filepath.Join(draft, sopFileName), []byte("---\nscope: team\n---\nSteps"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := s.InstallBundle("denied", draft, Entry{Source: SourceImported})
	if err != nil || got.Scope != "mine" {
		t.Fatalf("denied import: %+v %v", got, err)
	}
}

func TestAuthorCannotPromoteScopeAndUpdatePreservesTopics(t *testing.T) {
	s := newTestStore(t)
	draft := t.TempDir()
	if err := os.WriteFile(filepath.Join(draft, sopFileName), []byte("---\nscope: company\ntopicIds: [\"invented\"]\n---\nSteps"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := s.InstallBundle("authored", draft, Entry{Source: SourceTriage})
	if err != nil || got.Scope != "mine" || len(got.TopicIDs) != 0 {
		t.Fatalf("author promoted metadata: %+v %v", got, err)
	}
	if _, err := s.Save(Entry{ID: "authored", Scope: "topic", TopicIDs: []string{"assigned"}}); err != nil {
		t.Fatal(err)
	}
	got, err = s.InstallBundle("authored", draft, Entry{Source: SourceTriage})
	if err != nil || got.Scope != "topic" || !reflect.DeepEqual(got.TopicIDs, []string{"assigned"}) {
		t.Fatalf("author update discarded metadata: %+v %v", got, err)
	}
}

func TestImportRejectsInvalidMetadataBeforeReplacingBundle(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Save(Entry{ID: "kept", Scope: "topic", Body: "Keep these steps.\n"}); err != nil {
		t.Fatal(err)
	}
	scriptDir := filepath.Join(s.PathFor("kept"), "scripts")
	if err := os.MkdirAll(scriptDir, 0700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(scriptDir, "kept.sh")
	if err := os.WriteFile(script, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, header := range []string{"scope: public", "topicIds: [\"\"]", "topicIds: broken"} {
		draft := t.TempDir()
		if err := os.WriteFile(filepath.Join(draft, sopFileName), []byte("---\n"+header+"\n---\nChanged"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.InstallBundle("kept", draft, Entry{Source: SourceImported}); err == nil {
			t.Fatalf("invalid import accepted: %s", header)
		}
		entry, err := s.Get("kept")
		data, readErr := os.ReadFile(script)
		if err != nil || readErr != nil || entry.Scope != "topic" || entry.Body != "Keep these steps.\n" || string(data) != "keep" {
			t.Fatalf("invalid import mutated existing bundle: %+v %v %v", entry, err, readErr)
		}
	}
	old := t.TempDir()
	if err := os.WriteFile(filepath.Join(old, sopFileName), []byte("---\ntitle: Old bundle\n---\nSteps"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := s.InstallBundle("old-import", old, Entry{Source: SourceImported})
	if err != nil || got.Scope != "mine" {
		t.Fatalf("legacy import: %+v %v", got, err)
	}
}
