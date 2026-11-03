package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const taskOnsetAnchorIdentityVersion = "task-onset-anchor-v1"

// taskEpisodeOnsetAnchor is an exact production source fact at the inferred
// task boundary. Model wording and detector-window identity are deliberately
// absent so overlapping observations of one start converge on one episode.
type taskEpisodeOnsetAnchor struct {
	Ref        string
	SourceKind string
	SourceID   string
	ObservedAt int64
}

type TaskEpisodeCaptureResult struct {
	Episode TaskEpisode `json:"episode"`
	Added   bool        `json:"added"`
}

func taskOnsetEvidenceByRef(input TaskOnsetInput) map[string]TaskOnsetEvidence {
	result := make(map[string]TaskOnsetEvidence, len(input.Evidence))
	for _, evidence := range input.Evidence {
		result[evidence.Ref] = evidence
	}
	return result
}

func normalizeTaskEpisodeOnsetAnchor(
	anchor taskEpisodeOnsetAnchor,
) (taskEpisodeOnsetAnchor, bool) {
	anchor.Ref = strings.TrimSpace(anchor.Ref)
	anchor.SourceKind = strings.TrimSpace(anchor.SourceKind)
	anchor.SourceID = strings.TrimSpace(anchor.SourceID)
	return anchor, anchor.Ref != "" && anchor.SourceKind != "" &&
		anchor.SourceID != "" && anchor.ObservedAt > 0
}

func taskEpisodeOnsetAnchorKey(anchor taskEpisodeOnsetAnchor) string {
	return strings.Join([]string{
		anchor.SourceKind,
		anchor.SourceID,
		fmt.Sprint(anchor.ObservedAt),
		anchor.Ref,
	}, "\x00")
}

func taskOnsetPredictionAnchor(
	prediction TaskOnsetPrediction,
	evidenceByRef map[string]TaskOnsetEvidence,
) (taskEpisodeOnsetAnchor, bool) {
	anchors := make([]taskEpisodeOnsetAnchor, 0, len(prediction.EvidenceRefs))
	seen := make(map[string]struct{}, len(prediction.EvidenceRefs))
	for _, ref := range prediction.EvidenceRefs {
		ref = strings.TrimSpace(ref)
		if _, duplicate := seen[ref]; duplicate {
			continue
		}
		seen[ref] = struct{}{}
		evidence, ok := evidenceByRef[ref]
		if !ok || evidence.OccurredAt != prediction.InferredStartedAt {
			continue
		}
		anchor, ok := normalizeTaskEpisodeOnsetAnchor(taskEpisodeOnsetAnchor{
			Ref:        evidence.Ref,
			SourceKind: evidence.SourceKind,
			SourceID:   evidence.SourceID,
			ObservedAt: evidence.OccurredAt,
		})
		if !ok {
			continue
		}
		anchors = append(anchors, anchor)
	}
	if len(anchors) == 0 {
		return taskEpisodeOnsetAnchor{}, false
	}
	sort.Slice(anchors, func(i, j int) bool {
		return taskEpisodeOnsetAnchorKey(anchors[i]) <
			taskEpisodeOnsetAnchorKey(anchors[j])
	})
	return anchors[0], true
}

func taskEpisodeOnsetAnchorFromPayload(
	raw string,
) (taskEpisodeOnsetAnchor, bool) {
	var evidence TaskOnsetEvidence
	if err := json.Unmarshal([]byte(raw), &evidence); err != nil {
		return taskEpisodeOnsetAnchor{}, false
	}
	return normalizeTaskEpisodeOnsetAnchor(taskEpisodeOnsetAnchor{
		Ref:        evidence.Ref,
		SourceKind: evidence.SourceKind,
		SourceID:   evidence.SourceID,
		ObservedAt: evidence.OccurredAt,
	})
}

// FindTaskEpisodeByOnsetAnchor returns the earliest immutable episode that was
// created from the exact source-bound start. It validates the stored evidence
// payload so a coincident source timestamp cannot alias a different ref.
func (s *ActivityStore) FindTaskEpisodeByOnsetAnchor(
	anchor taskEpisodeOnsetAnchor,
	inferredStartAt int64,
) (TaskEpisode, bool, error) {
	if s == nil || s.db == nil {
		return TaskEpisode{}, false, errors.New("activity store is unavailable")
	}
	anchor, ok := normalizeTaskEpisodeOnsetAnchor(anchor)
	if !ok || inferredStartAt <= 0 || anchor.ObservedAt != inferredStartAt {
		return TaskEpisode{}, false, errors.New("exact task onset anchor is invalid")
	}
	index, err := s.taskEpisodeOnsetIdentityIndex(inferredStartAt)
	if err != nil {
		return TaskEpisode{}, false, fmt.Errorf("find task episode by onset anchor: %w", err)
	}
	records := index.byKey[taskEpisodeOnsetIdentityKey(anchor, inferredStartAt)]
	if len(records) == 0 {
		return TaskEpisode{}, false, nil
	}
	episode, err := s.GetTaskEpisode(records[0].EpisodeID)
	if err != nil {
		return TaskEpisode{}, false, err
	}
	if episode.InferredStartAt != inferredStartAt ||
		episode.FrozenContext.CutoffAt != inferredStartAt {
		return TaskEpisode{}, false, errors.New(
			"source-bound task episode has an invalid frozen-context boundary",
		)
	}
	return episode, true, nil
}

func legacyTaskOnsetEpisodeID(
	input TaskOnsetInput,
	prediction TaskOnsetPrediction,
) string {
	return deterministicTaskEpisodeID("task-onset", strings.Join([]string{
		input.SourceKind,
		input.SourceID,
		taskOnsetPromptVersion,
		prediction.LocalKey,
		fmt.Sprint(prediction.InferredStartedAt),
	}, "\x00"))
}

func taskOnsetPredictionRefs(prediction TaskOnsetPrediction) []string {
	seen := make(map[string]struct{})
	for _, ref := range prediction.EvidenceRefs {
		seen[ref] = struct{}{}
	}
	for _, alternative := range prediction.Alternatives {
		for _, ref := range alternative.EvidenceRefs {
			seen[ref] = struct{}{}
		}
		for _, ref := range alternative.ContradictionRefs {
			seen[ref] = struct{}{}
		}
	}
	for _, hypothesis := range prediction.ReasoningHypotheses {
		for _, ref := range hypothesis.EvidenceRefs {
			seen[ref] = struct{}{}
		}
		for _, ref := range hypothesis.ContradictionRefs {
			seen[ref] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for ref := range seen {
		result = append(result, ref)
	}
	sort.Strings(result)
	return result
}

func buildCapturedTaskEvidence(
	episodeID string,
	prediction TaskOnsetPrediction,
	evidenceByRef map[string]TaskOnsetEvidence,
	detectedAt int64,
) ([]TaskEpisodeEvidence, map[string]string, error) {
	refs := taskOnsetPredictionRefs(prediction)
	result := make([]TaskEpisodeEvidence, 0, len(refs))
	idsByRef := make(map[string]string, len(refs))
	for _, ref := range refs {
		evidence, ok := evidenceByRef[ref]
		if !ok {
			return nil, nil, fmt.Errorf("task onset evidence %q is unavailable", ref)
		}
		payload, err := json.Marshal(evidence)
		if err != nil {
			return nil, nil, err
		}
		item, err := normalizeTaskEpisodeEvidence(TaskEpisodeEvidence{
			TaskEpisodeID: episodeID,
			Role:          TaskEpisodeEvidenceOnset,
			SourceKind:    evidence.SourceKind,
			SourceID:      evidence.SourceID,
			Summary:       evidence.Summary,
			PayloadJSON:   string(payload),
			ObservedAt:    evidence.OccurredAt,
			CreatedAt:     detectedAt,
		}, episodeID, detectedAt)
		if err != nil {
			return nil, nil, err
		}
		result = append(result, item)
		idsByRef[ref] = item.ID
	}
	return result, idsByRef, nil
}

func mapTaskOnsetRefsToEvidenceIDs(refs []string, idsByRef map[string]string) []string {
	result := make([]string, 0, len(refs))
	for _, ref := range refs {
		if id := idsByRef[ref]; id != "" {
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}

func buildTaskOnsetAlternatives(
	prediction TaskOnsetPrediction,
	idsByRef map[string]string,
) []TaskEpisodeHypothesisAlternative {
	result := make([]TaskEpisodeHypothesisAlternative, 0, len(prediction.Alternatives)*2)
	for _, alternative := range prediction.Alternatives {
		evidenceIDs := mapTaskOnsetRefsToEvidenceIDs(alternative.EvidenceRefs, idsByRef)
		if task := strings.TrimSpace(alternative.Task); task != "" {
			result = append(result, TaskEpisodeHypothesisAlternative{
				Kind:        "task",
				Statement:   task,
				Confidence:  alternative.Confidence,
				EvidenceIDs: evidenceIDs,
			})
		}
		if desiredResult := strings.TrimSpace(alternative.DesiredResult); desiredResult != "" {
			result = append(result, TaskEpisodeHypothesisAlternative{
				Kind:        "desired_result",
				Statement:   desiredResult,
				Confidence:  alternative.Confidence,
				EvidenceIDs: evidenceIDs,
			})
		}
	}
	return result
}

func buildTaskOnsetReasoningHypotheses(
	prediction TaskOnsetPrediction,
	idsByRef map[string]string,
) []TaskEpisodeReasoningHypothesis {
	result := make(
		[]TaskEpisodeReasoningHypothesis,
		0,
		len(prediction.ReasoningHypotheses),
	)
	for _, hypothesis := range prediction.ReasoningHypotheses {
		result = append(result, TaskEpisodeReasoningHypothesis{
			Statement:   hypothesis.Statement,
			Confidence:  hypothesis.Confidence,
			Basis:       hypothesis.Basis,
			SourceQuote: hypothesis.SourceQuote,
			EvidenceIDs: mapTaskOnsetRefsToEvidenceIDs(
				hypothesis.EvidenceRefs,
				idsByRef,
			),
			ProfileRefs: append(
				[]string(nil),
				hypothesis.ProfileRefs...,
			),
			ContradictionEvidenceIDs: mapTaskOnsetRefsToEvidenceIDs(
				hypothesis.ContradictionRefs,
				idsByRef,
			),
		})
	}
	return result
}

func taskOnsetContextCandidates(
	input TaskOnsetInput,
	prediction TaskOnsetPrediction,
	additional []TaskContextCandidate,
) []TaskContextCandidate {
	result := make([]TaskContextCandidate, 0, len(additional)+len(input.Evidence))
	for _, candidate := range additional {
		if candidate.ObservedAt <= prediction.InferredStartedAt {
			result = append(result, candidate)
		}
	}
	for _, evidence := range input.Evidence {
		if evidence.OccurredAt > prediction.InferredStartedAt {
			continue
		}
		result = append(result, TaskContextCandidate{
			Ref:             evidence.Ref,
			Section:         TaskContextSectionActivity,
			SourceKind:      evidence.SourceKind,
			SourceID:        evidence.SourceID,
			ObservedAt:      evidence.OccurredAt,
			Content:         evidence.Summary,
			EvidenceClass:   TaskContextEvidenceObservedUser,
			PrivacyDecision: TaskContextPrivacyInclude,
			Relevance:       1,
		})
	}
	return result
}

// PersistTaskOnsetEpisodes compiles normalized onset hypotheses into immutable
// Task Episodes. Detection evidence may occur after the inferred start, but
// the frozen context assembler receives only sources available at that start.
func PersistTaskOnsetEpisodes(
	store *ActivityStore,
	input TaskOnsetInput,
	response TaskOnsetResponse,
	detectedAt int64,
	contextCandidates []TaskContextCandidate,
) ([]TaskEpisodeCaptureResult, error) {
	return PersistTaskOnsetEpisodesWithSources(
		store,
		input,
		response,
		detectedAt,
		TaskContextSourceResult{Candidates: contextCandidates},
	)
}

func PersistTaskOnsetEpisodesWithSources(
	store *ActivityStore,
	input TaskOnsetInput,
	response TaskOnsetResponse,
	detectedAt int64,
	contextSources TaskContextSourceResult,
) ([]TaskEpisodeCaptureResult, error) {
	return PersistTaskOnsetEpisodesWithProfileSources(
		store,
		input,
		response,
		detectedAt,
		nil,
		WorkPatternProfileContext{},
		contextSources,
	)
}

func workPatternTaskContextCandidate(
	profile WorkPatternProfileContext,
) (TaskContextCandidate, bool, error) {
	if strings.TrimSpace(profile.SnapshotID) == "" {
		return TaskContextCandidate{}, false, nil
	}
	compact, err := compileWorkPatternProfilePrompt(
		profile,
		workPatternProfilePromptMaximumBytes,
	)
	if err != nil {
		return TaskContextCandidate{}, false, err
	}
	content, err := encodedWorkPatternProfilePromptBytes(compact)
	if err != nil {
		return TaskContextCandidate{}, false, err
	}
	return TaskContextCandidate{
		Ref:             "work-pattern-profile:" + profile.SnapshotID,
		Section:         TaskContextSectionWorkProfile,
		SourceKind:      "work_pattern_profile",
		SourceID:        profile.SnapshotID,
		ObservedAt:      profile.SourceCutoffAt,
		Content:         string(content),
		EvidenceClass:   TaskContextEvidenceProfileProjection,
		PrivacyDecision: TaskContextPrivacyInclude,
		Relevance:       1,
		Required:        true,
	}, true, nil
}

func PersistTaskOnsetEpisodesWithProfileSources(
	store *ActivityStore,
	input TaskOnsetInput,
	response TaskOnsetResponse,
	detectedAt int64,
	onsetProfile *WorkPatternProfileContext,
	workProfile WorkPatternProfileContext,
	contextSources TaskContextSourceResult,
) ([]TaskEpisodeCaptureResult, error) {
	if store == nil || store.db == nil {
		return nil, errors.New("activity store is unavailable")
	}
	if strings.TrimSpace(workProfile.SnapshotID) != "" {
		input.WorkProfile = nil
	}
	if onsetProfile != nil && strings.TrimSpace(onsetProfile.SnapshotID) != "" {
		profileCopy := *onsetProfile
		input.WorkProfile = &profileCopy
	}
	normalizedInput, err := prepareTaskOnsetPromptInput(input)
	if err != nil {
		return nil, err
	}
	if detectedAt < normalizedInput.CutoffAt {
		return nil, errors.New("task onset detection timestamp precedes its evidence cutoff")
	}
	response = normalizeTaskOnsetResponse(normalizedInput, response)
	evidenceByRef := taskOnsetEvidenceByRef(normalizedInput)
	profileCandidate, hasProfile, err := workPatternTaskContextCandidate(
		workProfile,
	)
	if err != nil {
		return nil, err
	}
	results := make([]TaskEpisodeCaptureResult, 0, len(response.Episodes))
	for _, prediction := range response.Episodes {
		anchor, hasAnchor := taskOnsetPredictionAnchor(prediction, evidenceByRef)
		episodeID := legacyTaskOnsetEpisodeID(normalizedInput, prediction)
		if hasAnchor {
			existing, found, err := store.FindTaskEpisodeByOnsetAnchor(
				anchor,
				prediction.InferredStartedAt,
			)
			if err != nil {
				return results, err
			}
			if found {
				results = append(results, TaskEpisodeCaptureResult{
					Episode: existing,
					Added:   false,
				})
				continue
			}
			episodeID = deterministicTaskEpisodeID(
				taskOnsetAnchorIdentityVersion,
				taskEpisodeOnsetAnchorKey(anchor),
			)
		}
		evidence, idsByRef, err := buildCapturedTaskEvidence(
			episodeID,
			prediction,
			evidenceByRef,
			detectedAt,
		)
		if err != nil {
			return results, err
		}
		if hasProfile {
			actualOnsetProfile := WorkPatternProfileContext{}
			actualOnsetProjection := WorkPatternProfileContext{}
			if onsetProfile != nil {
				actualOnsetProfile = *onsetProfile
			}
			if normalizedInput.WorkProfile != nil {
				actualOnsetProjection = *normalizedInput.WorkProfile
			}
			provenance, err := buildWorkPatternProfileSelectionEvidence(
				actualOnsetProfile,
				actualOnsetProjection,
				workProfile,
				profileCandidate.Content,
				episodeID,
				detectedAt,
			)
			if err != nil {
				return results, err
			}
			evidence = append(evidence, provenance)
		}
		candidates := taskOnsetContextCandidates(
			normalizedInput,
			prediction,
			contextSources.Candidates,
		)
		if hasProfile {
			if profileCandidate.ObservedAt >= prediction.InferredStartedAt {
				return results, errors.New(
					"work pattern profile does not precede the inferred task start",
				)
			}
			candidates = append(candidates, profileCandidate)
		}
		assembled, err := AssembleTaskContext(TaskContextAssemblyInput{
			EpisodeID: episodeID,
			CutoffAt:  prediction.InferredStartedAt,
			CreatedAt: detectedAt,
			PreExcluded: taskContextSourceExclusionManifest(
				contextSources.Exclusions,
			),
			Candidates: candidates,
		})
		if err != nil {
			return results, err
		}
		frozenContext := assembled.FrozenContext()
		if hasProfile {
			frozenContext.WorkProfileSnapshotID = workProfile.SnapshotID
			frozenContext.WorkProfileSHA256 = workProfile.SnapshotSHA256
		}
		episode, added, err := store.CreateTaskEpisode(CreateTaskEpisodeInput{
			ID:              episodeID,
			InferredStartAt: prediction.InferredStartedAt,
			DetectedAt:      detectedAt,
			InitialStatus:   TaskEpisodeStatusCandidate,
			FrozenContext:   frozenContext,
			InitialEvidence: evidence,
			InitialHypothesis: TaskEpisodeHypothesisRevision{
				TaskEpisodeID:           episodeID,
				Revision:                1,
				TaskHypothesis:          prediction.Task,
				DesiredResultHypothesis: prediction.DesiredResult,
				TaskConfidence:          prediction.TaskConfidence,
				BoundaryConfidence:      prediction.BoundaryConfidence,
				DesiredResultConfidence: prediction.ResultConfidence,
				ProfileInfluenceRefs:    prediction.ProfileInfluenceRefs,
				ProfileConflict:         prediction.ProfileConflict,
				Alternatives:            buildTaskOnsetAlternatives(prediction, idsByRef),
				ReasoningHypotheses: buildTaskOnsetReasoningHypotheses(
					prediction,
					idsByRef,
				),
				EvidenceIDs: mapTaskOnsetRefsToEvidenceIDs(
					prediction.EvidenceRefs,
					idsByRef,
				),
				CreatedAt: detectedAt,
			},
			CreatedAt: detectedAt,
		})
		if err != nil && hasAnchor {
			existing, found, lookupErr := store.FindTaskEpisodeByOnsetAnchor(
				anchor,
				prediction.InferredStartedAt,
			)
			if lookupErr != nil {
				return results, errors.Join(err, lookupErr)
			}
			if found {
				episode = existing
				added = false
				err = nil
			}
		}
		if err != nil {
			return results, err
		}
		results = append(results, TaskEpisodeCaptureResult{
			Episode: episode,
			Added:   added,
		})
	}
	return results, nil
}
