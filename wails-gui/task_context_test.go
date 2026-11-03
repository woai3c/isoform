package main

import (
	"strings"
	"testing"
)

func taskContextCandidate(ref, section, content string, observedAt int64) TaskContextCandidate {
	return TaskContextCandidate{
		Ref:             ref,
		Section:         section,
		SourceKind:      "test",
		SourceID:        ref,
		ObservedAt:      observedAt,
		Content:         content,
		EvidenceClass:   TaskContextEvidenceObservedUser,
		PrivacyDecision: TaskContextPrivacyInclude,
		Relevance:       0.5,
	}
}

func TestAssembleTaskContextRejectsPostCutoffEvidence(t *testing.T) {
	_, err := AssembleTaskContext(TaskContextAssemblyInput{
		EpisodeID: "episode-1",
		CutoffAt:  100,
		CreatedAt: 150,
		Candidates: []TaskContextCandidate{
			taskContextCandidate("later-result", TaskContextSectionActivity, "finished result", 101),
		},
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds cutoff") {
		t.Fatalf("error = %v, want cutoff rejection", err)
	}
}

func TestAssembleTaskContextExcludesHiddenAndUnsupportedAgentClaims(t *testing.T) {
	hidden := taskContextCandidate("shadow", TaskContextSectionSelf, "hidden hypothesis", 90)
	hidden.EvidenceClass = TaskContextEvidenceHiddenShadow
	agentClaim := taskContextCandidate("agent", TaskContextSectionKnowledge, "agent invented claim", 91)
	agentClaim.EvidenceClass = TaskContextEvidenceAgentGenerated
	observed := taskContextCandidate("observed", TaskContextSectionActivity, "user opened the brief", 92)

	result, err := AssembleTaskContext(TaskContextAssemblyInput{
		EpisodeID:  "episode-1",
		CutoffAt:   100,
		CreatedAt:  150,
		Candidates: []TaskContextCandidate{hidden, agentClaim, observed},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.CanonicalJSON, "hidden hypothesis") ||
		strings.Contains(result.CanonicalJSON, "agent invented claim") {
		t.Fatalf("excluded evidence leaked into context: %s", result.CanonicalJSON)
	}
	if !strings.Contains(result.CanonicalJSON, "user opened the brief") {
		t.Fatalf("observed evidence missing from context: %s", result.CanonicalJSON)
	}
}

func TestAssembleTaskContextUsesBudgetsRatherThanItemCount(t *testing.T) {
	candidates := make([]TaskContextCandidate, 0, 40)
	for i := 0; i < 40; i++ {
		candidate := taskContextCandidate(
			"memory-"+string(rune('A'+i)),
			TaskContextSectionKnowledge,
			"short relevant fact",
			int64(50+i),
		)
		candidate.Relevance = 1
		candidates = append(candidates, candidate)
	}
	result, err := AssembleTaskContext(TaskContextAssemblyInput{
		EpisodeID:   "episode-many",
		CutoffAt:    100,
		CreatedAt:   150,
		TotalBudget: 8_000,
		SectionBudget: map[string]int{
			TaskContextSectionKnowledge: 8_000,
		},
		Candidates: candidates,
	})
	if err != nil {
		t.Fatal(err)
	}
	var included int
	for _, section := range result.Payload.Sections {
		if section.Name == TaskContextSectionKnowledge {
			included = len(section.Evidence)
		}
	}
	if included != 40 {
		t.Fatalf("included %d candidates, want all 40 within budget", included)
	}
}

func TestAssembleTaskContextIsDeterministic(t *testing.T) {
	first := taskContextCandidate("first", TaskContextSectionSelf, "first", 80)
	first.Relevance = 0.8
	second := taskContextCandidate("second", TaskContextSectionSelf, "second", 90)
	second.Relevance = 0.9
	input := TaskContextAssemblyInput{
		EpisodeID:  "episode-deterministic",
		CutoffAt:   100,
		CreatedAt:  150,
		Candidates: []TaskContextCandidate{first, second},
	}
	left, err := AssembleTaskContext(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Candidates[0], input.Candidates[1] = input.Candidates[1], input.Candidates[0]
	right, err := AssembleTaskContext(input)
	if err != nil {
		t.Fatal(err)
	}
	if left.CanonicalJSON != right.CanonicalJSON || left.SHA256 != right.SHA256 {
		t.Fatal("context assembly changed when input order changed")
	}
}

func TestAssembleTaskContextFailsClosedWhenRequiredEvidenceDoesNotFit(t *testing.T) {
	required := taskContextCandidate(
		"hard-boundary",
		TaskContextSectionBoundaries,
		strings.Repeat("x", 200),
		90,
	)
	required.Required = true
	_, err := AssembleTaskContext(TaskContextAssemblyInput{
		EpisodeID:   "episode-required",
		CutoffAt:    100,
		CreatedAt:   150,
		TotalBudget: 100,
		SectionBudget: map[string]int{
			TaskContextSectionBoundaries: 100,
		},
		Candidates: []TaskContextCandidate{required},
	})
	if err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("error = %v, want required evidence budget failure", err)
	}
}

func TestAssembleTaskContextRejectsConflictingDuplicateRefs(t *testing.T) {
	first := taskContextCandidate("same", TaskContextSectionSelf, "one", 80)
	second := taskContextCandidate("same", TaskContextSectionSelf, "two", 80)
	_, err := AssembleTaskContext(TaskContextAssemblyInput{
		EpisodeID:  "episode-duplicate",
		CutoffAt:   100,
		CreatedAt:  150,
		Candidates: []TaskContextCandidate{first, second},
	})
	if err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("error = %v, want duplicate conflict", err)
	}
}
