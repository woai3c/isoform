package main

import (
	"fmt"
	"strings"
	"testing"

	"yansu-agent/agent/authority"
)

func TestQualificationPromotesProvisionalPreferenceAndApprovalActivatesIt(t *testing.T) {
	store := newTestStore(t)
	action := authority.Action{
		Capability: "filesystem.write",
		ToolName:   "Write",
		Effect:     authority.EffectLocalWrite,
		Resource:   "/tmp/self-model/",
		Reversible: true,
	}
	for i := 0; i < 7; i++ {
		addQualificationEvidence(t, store, i, action)
	}
	_, qualification := rebuildAndQualify(t, store, 4_000_000)
	entries, err := store.ListCurrentSelfModelEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 ||
		entries[0].Kind != selfModelKindPreference ||
		entries[0].Status != selfModelStatusProvisional ||
		entries[0].ReasoningStatus != "behavior_derived" {
		t.Fatalf("provisional entries=%+v", entries)
	}
	if strings.TrimSpace(entries[0].Statement) == "" {
		t.Fatal("behavior-derived preference has no statement")
	}

	_, proposal := saveLegacyQualifiedProposalForTest(
		t,
		store,
		qualification,
	)
	app := newAuthorityTestApp(t, store)
	if _, err := app.ApproveInitiative(proposal.ID); err != nil {
		t.Fatal(err)
	}
	entries, err = store.ListCurrentSelfModelEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 ||
		entries[0].Status != selfModelStatusActive ||
		entries[0].ReasoningStatus != "behavior_and_explicit_approval" ||
		len(entries[0].Evidence) != 2 {
		t.Fatalf("approved entries=%+v", entries)
	}
	if got := app.selfModelContext(entries[0].Statement); !strings.Contains(got, "<self-model-context>") ||
		!strings.Contains(got, entries[0].Statement) ||
		!strings.Contains(got, "never grant execution authority") {
		t.Fatalf("self model context=%q", got)
	}
}

func TestSourceBackedReasoningRequiresRepeatedExplicitEvidence(t *testing.T) {
	store := newTestStore(t)
	action := authority.Action{
		Capability: "customer.context.read",
		ToolName:   "Read",
		Effect:     authority.EffectRead,
		Resource:   "customer/context",
		Reversible: true,
	}
	for i := 0; i < 5; i++ {
		run := addQualificationEvidence(t, store, i, action)
		if i >= 3 {
			continue
		}
		_, added, err := store.AddShadowRunObservation(ShadowRunObservation{
			ShadowRunID:    run.ID,
			SourceKind:     "activity",
			SourceID:       fmt.Sprintf("explicit-reason-%d", i),
			ObservedAction: "Explained the follow-up timing.",
			ReasoningEvidence: []ShadowReasoningEvidence{{
				Kind:       "stated_rationale",
				Statement:  "Wait for review before sending customer work.",
				Confidence: 0.9,
				SourceRefs: []string{fmt.Sprintf("activity:explicit-reason-%d", i)},
			}},
			ObservedAt: run.CreatedAt + 2_000,
			CreatedAt:  run.CreatedAt + 2_000,
		})
		if err != nil || !added {
			t.Fatalf("add explicit reasoning %d: added=%v err=%v", i, added, err)
		}
	}
	_, qualification := rebuildAndQualify(t, store, 4_000_000)
	if qualification.Status != "qualified" ||
		qualification.ProposalInitiativeID != "" {
		t.Fatalf("qualification=%+v", qualification)
	}
	entries, err := store.ListCurrentSelfModelEntries()
	if err != nil {
		t.Fatal(err)
	}
	var policy *SelfModelEntry
	for i := range entries {
		if entries[i].Kind == selfModelKindDecisionPolicy {
			policy = &entries[i]
			break
		}
	}
	if policy == nil ||
		policy.Status != selfModelStatusActive ||
		policy.ReasoningStatus != "source_backed" ||
		len(policy.Evidence) != 3 {
		t.Fatalf("decision policy=%+v entries=%+v", policy, entries)
	}
}

func TestBehaviorWithoutReasoningDoesNotCreatePolicyOrPrinciple(t *testing.T) {
	store := newTestStore(t)
	for i := 0; i < 7; i++ {
		addQualificationEvidence(t, store, i, authority.Action{
			Capability: "filesystem.write",
			ToolName:   "Write",
			Effect:     authority.EffectLocalWrite,
			Resource:   "/tmp/no-reason/",
			Reversible: true,
		})
	}
	_, _ = rebuildAndQualify(t, store, 4_000_000)
	entries, err := store.ListCurrentSelfModelEntries()
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Kind == selfModelKindDecisionPolicy || entry.Kind == selfModelKindPrinciple {
			t.Fatalf("unsupported reasoning produced %+v", entry)
		}
	}
}
