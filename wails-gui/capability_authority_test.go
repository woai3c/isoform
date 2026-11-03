package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
	"yansu-agent/agent/authority"
)

func newAuthorityTestApp(t *testing.T, activityStore *ActivityStore) *App {
	t.Helper()
	setTestHome(t)
	stateStore, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stateStore.Close() })
	scheduler := NewCronScheduler(stateStore, func(string, any) {}, func(string, string) {}, nil)
	if err := scheduler.loadJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(scheduler.loadedCh)
	return &App{
		store:         stateStore,
		cronScheduler: scheduler,
		activity:      &activityMonitorState{store: activityStore},
	}
}

func TestQualifiedInitiativeApprovalIssuesScopedRevocableAuthority(t *testing.T) {
	store := newTestStore(t)
	action := authority.Action{
		Capability: "filesystem.write",
		ToolName:   "Write",
		Effect:     authority.EffectLocalWrite,
		Resource:   "/tmp/yansu-qualified/",
		Reversible: true,
	}
	for i := 0; i < 7; i++ {
		addQualificationEvidence(t, store, i, action)
	}
	_, qualification := rebuildAndQualify(t, store, 4_000_000)
	if qualification.Status != "qualified" ||
		qualification.ProposalInitiativeID != "" {
		t.Fatalf("qualification=%+v", qualification)
	}
	_, proposal := saveLegacyQualifiedProposalForTest(
		t,
		store,
		qualification,
	)
	manifest, err := store.GetCapabilityManifest(proposal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.Grantable || manifest.GrantMode != "scoped_expiring" ||
		len(manifest.Effects) != 1 {
		t.Fatalf("manifest=%+v", manifest)
	}
	if _, err := store.db.Exec(
		`UPDATE capability_manifests SET policy_version = 'tampered' WHERE id = ?`,
		manifest.ID,
	); err == nil || !strings.Contains(err.Error(), "immutable") {
		t.Fatalf("manifest update error=%v, want immutable rejection", err)
	}

	app := newAuthorityTestApp(t, store)
	approved, err := app.ApproveInitiative(proposal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != InitiativeStatusApproved ||
		approved.MemoryState != InitiativeMemoryEligible ||
		approved.ExecutionRef == "" {
		t.Fatalf("approved initiative=%+v", approved)
	}
	grants, err := store.ListActiveAuthorityGrants(approved.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(grants) != 1 ||
		grants[0].Capability != action.Capability ||
		grants[0].ResourcePrefix != action.Resource {
		t.Fatalf("active grants=%+v", grants)
	}
	policy := authority.NewPolicy(grants)
	allowed := policy.Evaluate(context.Background(), authority.Context{Unattended: true}, authority.Action{
		Capability: action.Capability,
		Effect:     action.Effect,
		Resource:   action.Resource + "draft.md",
	})
	if allowed.Decision != authority.DecisionAllow || allowed.GrantID != grants[0].ID {
		t.Fatalf("matching evaluation=%+v", allowed)
	}
	denied := policy.Evaluate(context.Background(), authority.Context{Unattended: true}, authority.Action{
		Capability: action.Capability,
		Effect:     action.Effect,
		Resource:   "/tmp/other/draft.md",
	})
	if denied.Decision != authority.DecisionDeny {
		t.Fatalf("out-of-scope evaluation=%+v", denied)
	}
}

func TestUnscopedIrreversibleExternalCapabilityCannotBecomeUnattended(t *testing.T) {
	store := newTestStore(t)
	action := authority.Action{
		Capability: "network.request.write",
		ToolName:   "Bash",
		Operation:  "curl",
		Effect:     authority.EffectExternalWrite,
		Reversible: false,
	}
	for i := 0; i < 15; i++ {
		addQualificationEvidence(t, store, i, action)
	}
	_, qualification := rebuildAndQualify(t, store, 8_000_000)
	if qualification.Status != "qualified" ||
		qualification.RiskTier != "high_risk" ||
		qualification.ProposalInitiativeID != "" {
		t.Fatalf("qualification=%+v", qualification)
	}
	_, proposal := saveLegacyQualifiedProposalForTest(
		t,
		store,
		qualification,
	)
	manifest, err := store.GetCapabilityManifest(proposal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Grantable || manifest.GrantMode != "per_run_approval" {
		t.Fatalf("manifest=%+v", manifest)
	}

	app := newAuthorityTestApp(t, store)
	if _, err := app.ApproveInitiative(proposal.ID); err == nil ||
		!strings.Contains(err.Error(), "cannot run unattended") {
		t.Fatalf("approval error=%v, want unattended refusal", err)
	}
	jobs, err := app.store.ListCronJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 0 {
		t.Fatalf("blocked manifest created jobs=%+v", jobs)
	}
}
