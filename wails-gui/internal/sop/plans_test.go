package sop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSitesRoundTripAndSurviveAnUpdate(t *testing.T) {
	s := newTestStore(t)
	saved, err := s.Save(Entry{ID: "posthog-morning-check", Title: "PostHog morning check", Status: StatusDraft,
		Source: SourceObserved, Sites: []string{"https://us.posthog.com/"}, Body: "# Goal\n"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(saved.ID)
	if err != nil || len(got.Sites) != 1 || got.Sites[0] != "https://us.posthog.com/" || got.Source != SourceObserved {
		t.Fatalf("sites lost: %+v %v", got, err)
	}

	draft := t.TempDir()
	if err := os.WriteFile(filepath.Join(draft, "SOP.md"), []byte("---\ntitle: PostHog morning check\n---\n# Goal\nNew.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	updated, err := s.InstallBundle(saved.ID, draft, Entry{Status: StatusConfirmed, Source: SourceObserved, Sites: got.Sites})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Sites) != 1 || updated.Version != 2 {
		t.Fatalf("an update without sites keeps the old ones: %+v", updated)
	}
}

func TestPlanStore(t *testing.T) {
	dir := t.TempDir()
	plans, err := NewPlanStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := plans.Get("x"); ok || err != nil {
		t.Fatalf("empty store: %v %v", ok, err)
	}
	if _, err := plans.Update("posthog", func(p *Plan) {
		p.Proposal = &Proposal{Host: "us.posthog.com", Time: "09:10", Days: 5}
	}); err != nil {
		t.Fatal(err)
	}
	if proposed, err := plans.ProposedHost("US.posthog.com"); err != nil || !proposed {
		t.Fatalf("ProposedHost = %v %v", proposed, err)
	}
	plan, err := plans.Update("posthog", func(p *Plan) {
		p.Proposal.Answer = AnswerAccepted
		p.Channels = []string{"desktop", "text"}
	})
	if err != nil || plan.Proposal.Answer != AnswerAccepted || plan.UpdatedAt == 0 {
		t.Fatalf("Update = %+v %v", plan, err)
	}

	// A second store over the same folder reads what the first wrote, and the
	// plans file is never listed as a playbook.
	again, _ := NewPlanStore(dir)
	got, ok, err := again.Get("posthog")
	if err != nil || !ok || len(got.Channels) != 2 {
		t.Fatalf("reopened plan = %+v %v %v", got, ok, err)
	}
	bundles, _ := NewStore(dir)
	if list, err := bundles.List(); err != nil || len(list) != 0 {
		t.Fatalf("plans file listed as a bundle: %v %v", list, err)
	}
}
