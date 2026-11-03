package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"yansu-agent/agent/authority"
)

const (
	ShadowLearningInfluenceProofVersion = "shadow-learning-influence-proof-v1"

	ShadowLearningInfluenceAwaitingSourceLearning = "awaiting_source_learning"
	ShadowLearningInfluenceAwaitingLaterTask      = "awaiting_later_task"
	ShadowLearningInfluenceLearningOmitted        = "learning_omitted"
	ShadowLearningInfluenceAwaitingShadowResult   = "awaiting_shadow_result"
	ShadowLearningInfluenceAwaitingAblation       = "awaiting_ablation"
	ShadowLearningInfluenceMeasuredUnknown        = "measured_unknown"
	ShadowLearningInfluenceMeasuredNoLift         = "measured_no_lift"
	ShadowLearningInfluenceMeasuredImprovement    = "measured_improvement"
	ShadowLearningInfluenceIntegrityError         = "integrity_error"
)

type ShadowLearningInfluenceProofStep struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Complete bool   `json:"complete"`
	Reason   string `json:"reason,omitempty"`
}

// ShadowLearningInfluenceProof is a read-only release argument. It proves that
// one current, evaluated Shadow learning entered a strictly later production
// detector and isolated worker before comparing neutral/profile predictions
// against the user's immutable result. It never changes authority.
type ShadowLearningInfluenceProof struct {
	Version              string   `json:"version"`
	State                string   `json:"state"`
	Complete             bool     `json:"complete"`
	Improved             bool     `json:"improved"`
	Summary              string   `json:"summary"`
	RequiredAction       string   `json:"requiredAction"`
	AuthorityEffect      string   `json:"authorityEffect"`
	GeneratedAt          int64    `json:"generatedAt"`
	SourceLearningCount  int      `json:"sourceLearningCount"`
	LaterTaskCount       int      `json:"laterTaskCount"`
	MeasuredChainCount   int      `json:"measuredChainCount"`
	LearningRevisionID   string   `json:"learningRevisionId,omitempty"`
	LearningReceiptID    string   `json:"learningReceiptId,omitempty"`
	LearningTargetKind   string   `json:"learningTargetKind,omitempty"`
	LearningKind         string   `json:"learningKind,omitempty"`
	LearningCapability   string   `json:"learningCapability,omitempty"`
	LearningAvailableAt  int64    `json:"learningAvailableAt,omitempty"`
	SourceTaskEpisodeIDs []string `json:"sourceTaskEpisodeIds"`
	SourceShadowRunIDs   []string `json:"sourceShadowRunIds"`
	SourceEvaluationIDs  []string `json:"sourceEvaluationIds"`

	LaterTaskEpisodeID string `json:"laterTaskEpisodeId,omitempty"`
	LaterTaskStatus    string `json:"laterTaskStatus,omitempty"`
	LaterTaskStartedAt int64  `json:"laterTaskStartedAt,omitempty"`
	ContextSnapshotID  string `json:"contextSnapshotId,omitempty"`
	ContextSHA256      string `json:"contextSha256,omitempty"`
	ProfileSnapshotID  string `json:"profileSnapshotId,omitempty"`
	ProfileSHA256      string `json:"profileSha256,omitempty"`

	OnsetProfilePresent   bool   `json:"onsetProfilePresent"`
	OnsetPromptIncluded   *bool  `json:"onsetPromptIncluded,omitempty"`
	OnsetPromptReason     string `json:"onsetPromptReason,omitempty"`
	OnsetPromptSHA256     string `json:"onsetPromptSha256,omitempty"`
	ShadowContextIncluded *bool  `json:"shadowContextIncluded,omitempty"`
	ShadowContextReason   string `json:"shadowContextReason,omitempty"`
	ShadowContextSHA256   string `json:"shadowContextSha256,omitempty"`

	ShadowRunID              string                             `json:"shadowRunId,omitempty"`
	ShadowExecutionAttemptID string                             `json:"shadowExecutionAttemptId,omitempty"`
	ShadowEvaluationID       string                             `json:"shadowEvaluationId,omitempty"`
	AblationRunID            string                             `json:"ablationRunId,omitempty"`
	AblationEvaluationID     string                             `json:"ablationEvaluationId,omitempty"`
	NeutralScore             *float64                           `json:"neutralScore,omitempty"`
	ProfileScore             *float64                           `json:"profileScore,omitempty"`
	Lift                     *float64                           `json:"lift,omitempty"`
	Winner                   string                             `json:"winner,omitempty"`
	Steps                    []ShadowLearningInfluenceProofStep `json:"steps"`
	Reasons                  []string                           `json:"reasons"`
}

type shadowLearningInfluenceCandidate struct {
	proof ShadowLearningInfluenceProof
	rank  int
}

func newShadowLearningInfluenceProof(now int64) ShadowLearningInfluenceProof {
	if now <= 0 {
		now = time.Now().UnixMilli()
	}
	return ShadowLearningInfluenceProof{
		Version:              ShadowLearningInfluenceProofVersion,
		State:                ShadowLearningInfluenceAwaitingSourceLearning,
		Summary:              "No evaluated Shadow comparison is available yet.",
		RequiredAction:       "Complete more contained Shadow comparisons.",
		AuthorityEffect:      ShadowTaskLearningAuthorityNone,
		GeneratedAt:          now,
		SourceTaskEpisodeIDs: []string{},
		SourceShadowRunIDs:   []string{},
		SourceEvaluationIDs:  []string{},
		Steps:                []ShadowLearningInfluenceProofStep{},
		Reasons:              []string{},
	}
}

func shadowLearningInfluenceStateRank(state string) int {
	switch state {
	case ShadowLearningInfluenceAwaitingLaterTask:
		return 1
	case ShadowLearningInfluenceLearningOmitted:
		return 2
	case ShadowLearningInfluenceAwaitingShadowResult:
		return 3
	case ShadowLearningInfluenceAwaitingAblation:
		return 4
	case ShadowLearningInfluenceMeasuredUnknown:
		return 5
	case ShadowLearningInfluenceMeasuredNoLift:
		return 6
	case ShadowLearningInfluenceMeasuredImprovement:
		return 7
	case ShadowLearningInfluenceIntegrityError:
		return 2
	default:
		return 0
	}
}

func listAllShadowLearningInfluenceEpisodeIDs(
	store *ActivityStore,
	cutoffAt int64,
) ([]string, error) {
	rows, err := store.db.Query(`
		SELECT id
		FROM task_episodes
		WHERE created_at <= ?
		ORDER BY inferred_start_at ASC, detected_at ASC, id ASC`, cutoffAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

func getShadowLearningInfluenceEpisodeDetailAt(
	store *ActivityStore,
	episodeID string,
	cutoffAt int64,
) (TaskEpisodeDetail, error) {
	episode, err := scanTaskEpisode(store.db.QueryRow(`
		SELECT `+taskEpisodeSelectColumns+`
		FROM task_episodes e
		JOIN task_episode_context_snapshots context
			ON context.task_episode_id = e.id
		JOIN task_episode_transitions current_transition
			ON current_transition.task_episode_id = e.id
			AND current_transition.sequence = (
				SELECT MAX(candidate_transition.sequence)
				FROM task_episode_transitions candidate_transition
				WHERE candidate_transition.task_episode_id = e.id
					AND candidate_transition.created_at <= ?
					AND candidate_transition.occurred_at <= ?
			)
		JOIN task_episode_hypotheses hypothesis
			ON hypothesis.task_episode_id = e.id
			AND hypothesis.revision = (
				SELECT MAX(candidate_hypothesis.revision)
				FROM task_episode_hypotheses candidate_hypothesis
				WHERE candidate_hypothesis.task_episode_id = e.id
					AND candidate_hypothesis.created_at <= ?
			)
		WHERE e.id = ? AND e.created_at <= ?
			AND context.created_at <= ? AND context.cutoff_at <= ?`,
		cutoffAt,
		cutoffAt,
		cutoffAt,
		episodeID,
		cutoffAt,
		cutoffAt,
		cutoffAt,
	))
	if err != nil {
		return TaskEpisodeDetail{}, err
	}
	detail := TaskEpisodeDetail{Episode: episode}

	hypotheses, err := store.db.Query(`
		SELECT id, task_episode_id, revision, task_hypothesis,
			desired_result_hypothesis, task_confidence,
			boundary_confidence, desired_result_confidence, alternatives_json,
			evidence_ids_json, profile_influence_json,
			reasoning_hypotheses_json, created_at
		FROM task_episode_hypotheses
		WHERE task_episode_id = ? AND created_at <= ?
		ORDER BY revision ASC, id ASC`, episodeID, cutoffAt)
	if err != nil {
		return TaskEpisodeDetail{}, err
	}
	for hypotheses.Next() {
		item, scanErr := scanTaskEpisodeHypothesis(hypotheses)
		if scanErr != nil {
			hypotheses.Close()
			return TaskEpisodeDetail{}, scanErr
		}
		detail.Hypotheses = append(detail.Hypotheses, item)
	}
	if err := hypotheses.Err(); err != nil {
		hypotheses.Close()
		return TaskEpisodeDetail{}, err
	}
	if err := hypotheses.Close(); err != nil {
		return TaskEpisodeDetail{}, err
	}

	evidence, err := store.db.Query(`
		SELECT id, task_episode_id, role, source_kind, source_id, summary,
			payload_json, content_hash, observed_at, created_at
		FROM task_episode_evidence
		WHERE task_episode_id = ? AND created_at <= ? AND observed_at <= ?
		ORDER BY observed_at ASC, created_at ASC, id ASC`,
		episodeID, cutoffAt, cutoffAt)
	if err != nil {
		return TaskEpisodeDetail{}, err
	}
	for evidence.Next() {
		item, scanErr := scanTaskEpisodeEvidence(evidence)
		if scanErr != nil {
			evidence.Close()
			return TaskEpisodeDetail{}, scanErr
		}
		detail.Evidence = append(detail.Evidence, item)
	}
	if err := evidence.Err(); err != nil {
		evidence.Close()
		return TaskEpisodeDetail{}, err
	}
	if err := evidence.Close(); err != nil {
		return TaskEpisodeDetail{}, err
	}

	transitions, err := store.db.Query(`
		SELECT id, task_episode_id, sequence, from_status, to_status,
			reason, occurred_at, created_at
		FROM task_episode_transitions
		WHERE task_episode_id = ? AND created_at <= ? AND occurred_at <= ?
		ORDER BY sequence ASC, id ASC`, episodeID, cutoffAt, cutoffAt)
	if err != nil {
		return TaskEpisodeDetail{}, err
	}
	for transitions.Next() {
		item, scanErr := scanTaskEpisodeTransition(transitions)
		if scanErr != nil {
			transitions.Close()
			return TaskEpisodeDetail{}, scanErr
		}
		detail.Transitions = append(detail.Transitions, item)
	}
	if err := transitions.Err(); err != nil {
		transitions.Close()
		return TaskEpisodeDetail{}, err
	}
	if err := transitions.Close(); err != nil {
		return TaskEpisodeDetail{}, err
	}

	links, err := store.db.Query(`
		SELECT id, source_episode_id, target_episode_id, relation, created_at
		FROM task_episode_links
		WHERE (source_episode_id = ? OR target_episode_id = ?)
			AND created_at <= ?
		ORDER BY created_at ASC, relation ASC, source_episode_id ASC,
			target_episode_id ASC, id ASC`, episodeID, episodeID, cutoffAt)
	if err != nil {
		return TaskEpisodeDetail{}, err
	}
	for links.Next() {
		item, scanErr := scanTaskEpisodeLink(links)
		if scanErr != nil {
			links.Close()
			return TaskEpisodeDetail{}, scanErr
		}
		detail.Links = append(detail.Links, item)
	}
	if err := links.Err(); err != nil {
		links.Close()
		return TaskEpisodeDetail{}, err
	}
	if err := links.Close(); err != nil {
		return TaskEpisodeDetail{}, err
	}
	if len(detail.Hypotheses) == 0 || len(detail.Transitions) == 0 {
		return TaskEpisodeDetail{}, errors.New(
			"task episode has no complete state at the cohort cutoff",
		)
	}
	for index, hypothesis := range detail.Hypotheses {
		if hypothesis.Revision != index+1 {
			return TaskEpisodeDetail{}, errors.New(
				"task episode hypothesis history is not contiguous",
			)
		}
	}
	for index, transition := range detail.Transitions {
		if transition.Sequence != index+1 {
			return TaskEpisodeDetail{}, errors.New(
				"task episode transition history is not contiguous",
			)
		}
	}
	return detail, nil
}

func loadShadowLearningInfluenceLedger(
	store *ActivityStore,
	cutoffAt int64,
) ([]VerifiedShadowLearningSource, []TaskEpisodeDetail, error) {
	return loadShadowLearningInfluenceLedgerForRevisions(
		store,
		cutoffAt,
		nil,
	)
}

func loadShadowLearningInfluenceLedgerForRevisions(
	store *ActivityStore,
	cutoffAt int64,
	revisionIDs map[string]struct{},
) ([]VerifiedShadowLearningSource, []TaskEpisodeDetail, error) {
	sources, err := VerifiedShadowLearningSourcesAt(store, cutoffAt, "")
	if err != nil {
		return nil, nil, err
	}
	if revisionIDs != nil {
		filtered := make([]VerifiedShadowLearningSource, 0, len(sources.Sources))
		for _, source := range sources.Sources {
			if _, ok := revisionIDs[source.Learning.RevisionID]; ok {
				filtered = append(filtered, source)
			}
		}
		sources.Sources = filtered
	}
	sort.SliceStable(sources.Sources, func(i, j int) bool {
		if sources.Sources[i].Receipt.CreatedAt != sources.Sources[j].Receipt.CreatedAt {
			return sources.Sources[i].Receipt.CreatedAt <
				sources.Sources[j].Receipt.CreatedAt
		}
		return sources.Sources[i].Learning.RevisionID <
			sources.Sources[j].Learning.RevisionID
	})
	if len(sources.Sources) == 0 {
		return []VerifiedShadowLearningSource{}, []TaskEpisodeDetail{}, nil
	}
	episodeIDs, err := listAllShadowLearningInfluenceEpisodeIDs(store, cutoffAt)
	if err != nil {
		return nil, nil, err
	}
	details := make([]TaskEpisodeDetail, 0, len(episodeIDs))
	for _, id := range episodeIDs {
		detail, err := getShadowLearningInfluenceEpisodeDetailAt(
			store,
			id,
			cutoffAt,
		)
		if err != nil {
			return nil, nil, err
		}
		details = append(details, detail)
	}
	return sources.Sources, details, nil
}

func listAllShadowLearningInfluenceRuns(
	store *ActivityStore,
	episodeID string,
	cutoffAt int64,
) ([]ShadowRun, error) {
	rows, err := store.db.Query(`
		SELECT `+shadowRunSelectColumns+`
		FROM shadow_runs
		WHERE mode = 'task_mirror' AND task_episode_id = ?
			AND created_at <= ?
		ORDER BY created_at ASC, id ASC`, episodeID, cutoffAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ShadowRun, 0)
	for rows.Next() {
		item, err := scanShadowRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func listAllShadowLearningInfluenceAblations(
	store *ActivityStore,
	episodeID string,
	cutoffAt int64,
) ([]WorkPatternProfileAblationEvaluation, error) {
	rows, err := store.db.Query(`
		SELECT
			id, ablation_run_id, task_episode_id, shadow_evaluation_id,
			policy_version, neutral_score, profile_score,
			neutral_boundary_error_ms, profile_boundary_error_ms,
			winner, evidence_json, created_at
		FROM work_pattern_profile_ablation_evaluations_v1
		WHERE task_episode_id = ? AND created_at <= ?
		ORDER BY created_at ASC, rowid ASC`, episodeID, cutoffAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]WorkPatternProfileAblationEvaluation, 0)
	for rows.Next() {
		item, err := scanWorkPatternProfileAblationEvaluation(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func getShadowLearningInfluenceEvaluation(
	store *ActivityStore,
	id string,
) (ShadowRunEvaluation, error) {
	return scanShadowRunEvaluation(store.db.QueryRow(`
		SELECT id, evaluation_json, created_at
		FROM shadow_run_evaluations
		WHERE id = ?`, strings.TrimSpace(id)))
}

func shadowLearningInfluenceEntry(
	manifest []WorkPatternProfileSelectionEntry,
	learning TaskContextLearningProvenance,
) (*WorkPatternProfileSelectionEntry, error) {
	var result *WorkPatternProfileSelectionEntry
	for index := range manifest {
		item := &manifest[index]
		if item.Learning == nil || *item.Learning != learning {
			continue
		}
		if result != nil {
			return nil, errors.New(
				"learning appears more than once in a frozen stage manifest",
			)
		}
		copy := *item
		result = &copy
	}
	return result, nil
}

func shadowLearningInfluenceHasSelectionEvidence(
	detail TaskEpisodeDetail,
) bool {
	for _, evidence := range detail.Evidence {
		if evidence.Role == TaskEpisodeEvidenceContext &&
			evidence.SourceKind == workPatternProfileSelectionEvidenceSource {
			return true
		}
	}
	return false
}

func shadowLearningInfluenceFrozenProfile(
	episode TaskEpisode,
	selection workPatternFrozenSelection,
) error {
	var payload TaskContextPayload
	if err := json.Unmarshal(
		[]byte(episode.FrozenContext.CanonicalJSON),
		&payload,
	); err != nil {
		return fmt.Errorf("decode frozen task context: %w", err)
	}
	matched := 0
	for _, section := range payload.Sections {
		if section.Name != TaskContextSectionWorkProfile {
			continue
		}
		for _, evidence := range section.Evidence {
			if evidence.SourceKind != "work_pattern_profile" {
				continue
			}
			matched++
			var profile WorkPatternProfileContext
			if err := json.Unmarshal([]byte(evidence.Content), &profile); err != nil {
				return fmt.Errorf("decode frozen Shadow profile: %w", err)
			}
			if err := validatePreparedWorkPatternProfilePrompt(
				profile,
				workPatternProfilePromptMaximumBytes,
			); err != nil {
				return err
			}
			manifestHash, err := workPatternProfileSelectionManifestSHA256(
				profile.SelectionManifest,
			)
			if err != nil {
				return err
			}
			if taskContextHash(evidence.Content) != selection.ShadowPromptSHA256 ||
				manifestHash != selection.ShadowContextSelectionSHA256 ||
				profile.SnapshotID != selection.SnapshotID ||
				profile.SnapshotSHA256 != selection.SnapshotSHA256 ||
				profile.SourceContextSHA256 !=
					selection.ShadowSourceContextSHA256 {
				return errors.New(
					"frozen Shadow profile differs from selection provenance",
				)
			}
		}
	}
	if matched != 1 {
		return fmt.Errorf(
			"frozen task context contains %d Shadow profile projections",
			matched,
		)
	}
	return nil
}

func shadowLearningInfluenceSourceIDs(
	source VerifiedShadowLearningSource,
) (episodes, runs, evaluations []string) {
	for _, evidence := range source.Candidate.Evidence {
		episodes = append(episodes, evidence.TaskEpisodeID)
		runs = append(runs, evidence.ShadowRunID)
		evaluations = append(evaluations, evidence.EvaluationID)
	}
	return uniqueSortedStrings(episodes), uniqueSortedStrings(runs),
		uniqueSortedStrings(evaluations)
}

func shadowLearningInfluenceAttachSource(
	proof *ShadowLearningInfluenceProof,
	source VerifiedShadowLearningSource,
) {
	if proof == nil {
		return
	}
	learning := source.Learning
	proof.LearningRevisionID = learning.RevisionID
	proof.LearningReceiptID = learning.MaterializationReceiptID
	proof.LearningTargetKind = learning.TargetKind
	proof.LearningKind = learning.LearningKind
	proof.LearningCapability = learning.CapabilityKey
	proof.LearningAvailableAt = learning.AvailableAt
	proof.SourceTaskEpisodeIDs, proof.SourceShadowRunIDs,
		proof.SourceEvaluationIDs = shadowLearningInfluenceSourceIDs(source)
}

func shadowLearningInfluenceAttachEpisode(
	proof *ShadowLearningInfluenceProof,
	detail TaskEpisodeDetail,
	selection workPatternFrozenSelection,
) {
	if proof == nil {
		return
	}
	episode := detail.Episode
	proof.LaterTaskEpisodeID = episode.ID
	proof.LaterTaskStatus = episode.CurrentStatus
	proof.LaterTaskStartedAt = episode.InferredStartAt
	proof.ContextSnapshotID = episode.FrozenContext.ID
	proof.ContextSHA256 = episode.FrozenContext.SHA256
	proof.ProfileSnapshotID = selection.SnapshotID
	proof.ProfileSHA256 = selection.SnapshotSHA256
	proof.OnsetProfilePresent = selection.OnsetProfilePresent
	proof.OnsetPromptSHA256 = selection.OnsetPromptSHA256
	proof.ShadowContextSHA256 = selection.ShadowPromptSHA256
}

func shadowLearningInfluencePredictionMatchesEpisode(
	response TaskOnsetResponse,
	input TaskOnsetInput,
	episode TaskEpisode,
) bool {
	evidenceByRef := taskOnsetEvidenceByRef(input)
	for _, prediction := range response.Episodes {
		id := legacyTaskOnsetEpisodeID(input, prediction)
		if anchor, ok := taskOnsetPredictionAnchor(
			prediction,
			evidenceByRef,
		); ok {
			id = deterministicTaskEpisodeID(
				taskOnsetAnchorIdentityVersion,
				taskEpisodeOnsetAnchorKey(anchor),
			)
		}
		if id == episode.ID &&
			prediction.Task == episode.LatestHypothesis.TaskHypothesis &&
			prediction.DesiredResult ==
				episode.LatestHypothesis.DesiredResultHypothesis &&
			prediction.InferredStartedAt == episode.InferredStartAt {
			return true
		}
	}
	return false
}

func shadowLearningInfluenceValidateOnsetAttempt(
	store *ActivityStore,
	run WorkPatternProfileAblationRun,
	episode TaskEpisode,
	selection workPatternFrozenSelection,
) error {
	current, err := currentWorkPatternProfileAblationRun(store, run)
	if err != nil {
		return err
	}
	if !current {
		return errors.New(
			"ablation run does not use the current independent evidence policy",
		)
	}
	if run.PrimaryMode != WorkPatternInfluenceProfile ||
		run.ProfileStatus != TaskAgentAttemptStatusSucceeded ||
		run.ProfileAttemptID == "" ||
		run.ProfileSnapshotID != selection.SnapshotID ||
		run.ProfileSHA256 != selection.SnapshotSHA256 ||
		run.PromptVersion != taskOnsetPromptVersion ||
		run.WindowStart > episode.InferredStartAt ||
		run.CutoffAt < episode.InferredStartAt {
		return errors.New("ablation run is not the exact profile production branch")
	}
	attempt, err := store.GetTaskAgentAttempt(run.ProfileAttemptID)
	if err != nil {
		return err
	}
	if attempt.Phase != TaskAgentAttemptPhaseOnset ||
		attempt.Status != TaskAgentAttemptStatusSucceeded ||
		attempt.PromptVersion != taskOnsetPromptVersion ||
		attempt.ModelName != run.Model ||
		attempt.PromptHash != taskAgentAttemptHash([]byte(attempt.PromptText)) ||
		attempt.InputHash !=
			taskAgentAttemptHash([]byte(attempt.NormalizedInputJSON)) {
		return errors.New("profile onset attempt provenance is invalid")
	}
	canonical, err := canonicalTaskAgentAttemptJSON(
		attempt.NormalizedInputJSON,
		"profile onset attempt input",
		true,
	)
	if err != nil || canonical != attempt.NormalizedInputJSON {
		return errors.New("profile onset attempt input is not canonical")
	}
	var input TaskOnsetInput
	if err := json.Unmarshal([]byte(attempt.NormalizedInputJSON), &input); err != nil {
		return fmt.Errorf("decode profile onset attempt input: %w", err)
	}
	if input.SourceKind != run.SourceKind || input.SourceID != run.SourceID ||
		input.WindowStart != run.WindowStart || input.CutoffAt != run.CutoffAt ||
		input.WorkProfile == nil {
		return errors.New("profile onset attempt does not match its ablation window")
	}
	if !shadowLearningInfluencePredictionMatchesEpisode(
		run.ProfileResponse,
		input,
		episode,
	) {
		return errors.New("ablation run is not the exact profile production branch")
	}
	profile := *input.WorkProfile
	if err := validatePreparedWorkPatternProfilePrompt(
		profile,
		workPatternProfilePromptMaximumBytes,
	); err != nil {
		return err
	}
	encoded, err := encodedWorkPatternProfilePromptBytes(profile)
	if err != nil {
		return err
	}
	selectionHash, err := workPatternProfileSelectionManifestSHA256(
		profile.SelectionManifest,
	)
	if err != nil {
		return err
	}
	if profile.SnapshotID != selection.SnapshotID ||
		profile.SnapshotSHA256 != selection.SnapshotSHA256 ||
		profile.SourceContextSHA256 != selection.OnsetSourceContextSHA256 ||
		taskContextHash(string(encoded)) != selection.OnsetPromptSHA256 ||
		selectionHash != selection.OnsetPromptSelectionSHA256 {
		return errors.New("profile onset attempt differs from frozen admission evidence")
	}
	return nil
}

func shadowLearningInfluenceContainedAttempt(
	store *ActivityStore,
	run ShadowRun,
	cutoffAt int64,
) (ShadowExecutionAttempt, error) {
	attempts, err := store.ListShadowExecutionAttempts(run.ID)
	if err != nil {
		return ShadowExecutionAttempt{}, err
	}
	eligible := make([]ShadowExecutionAttempt, 0, len(attempts))
	for _, attempt := range attempts {
		if cutoffAt <= 0 || attempt.CreatedAt <= cutoffAt {
			eligible = append(eligible, attempt)
		}
	}
	if len(eligible) == 0 {
		return ShadowExecutionAttempt{}, errors.New(
			"no Shadow execution attempt exists",
		)
	}
	attempt := eligible[len(eligible)-1]
	normalized, err := normalizeShadowExecutionAttempt(attempt)
	if err != nil || normalized.Status != "completed" ||
		normalized.PolicyVersion != authority.ShadowPolicyVersion ||
		normalized.RuntimeVersion != shadowExecutionRuntimeVersion ||
		normalized.ShadowRunID != run.ID {
		return ShadowExecutionAttempt{}, errors.New(
			"latest execution attempt does not satisfy the current Shadow policy",
		)
	}
	for _, decision := range normalized.Decisions {
		if decision.Context.Mode != authority.ExecutionModeShadow ||
			decision.Context.ShadowRun != run.ID ||
			(decision.Action.Effect != authority.EffectRead &&
				decision.Decision == authority.DecisionAllow) {
			return ShadowExecutionAttempt{}, errors.New(
				"latest execution attempt escaped Shadow containment",
			)
		}
	}
	return normalized, nil
}

func shadowLearningInfluenceValidateShadowEvaluation(
	store *ActivityStore,
	episode TaskEpisode,
	selection workPatternFrozenSelection,
	evaluation ShadowRunEvaluation,
	cutoffAt int64,
) (ShadowRun, ShadowExecutionAttempt, ShadowRunEvaluation, error) {
	if cutoffAt > 0 && evaluation.CreatedAt > cutoffAt {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{},
			errors.New("Shadow evaluation is newer than the cohort cutoff")
	}
	normalized, err := normalizeShadowRunEvaluation(evaluation)
	if err != nil || !shadowTaskEvaluationUsesCurrentContract(normalized) {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{},
			errors.New("Shadow evaluation does not use the current immutable contract")
	}
	run, err := store.GetShadowRun(normalized.ShadowRunID)
	if err != nil {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{}, err
	}
	if err := requireCurrentShadowTaskRun(run); err != nil {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{}, err
	}
	normalizedRun, _, _, err := normalizeShadowRun(run)
	if err != nil {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{}, err
	}
	run = normalizedRun
	if run.TaskEpisodeID != episode.ID ||
		run.TaskContextHash != episode.FrozenContext.SHA256 ||
		run.Context.PromptContext != episode.FrozenContext.CanonicalJSON ||
		run.WorkProfileSnapshotID != selection.SnapshotID ||
		run.WorkProfileSHA256 != selection.SnapshotSHA256 {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{},
			errors.New("Shadow run does not match the later task and frozen profile")
	}
	input := normalized.TaskEvaluationInput
	if input == nil || input.TaskEpisodeID != episode.ID ||
		input.ShadowRunID != run.ID ||
		input.FrozenContextHash != episode.FrozenContext.SHA256 {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{},
			errors.New("Shadow evaluation input does not match the later task")
	}
	evidenceCutoffAt := cutoffAt
	if evidenceCutoffAt <= 0 {
		evidenceCutoffAt = normalized.CreatedAt
	}
	detail, err := getShadowLearningInfluenceEpisodeDetailAt(
		store,
		episode.ID,
		evidenceCutoffAt,
	)
	if err != nil {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{}, err
	}
	if detail.Episode.FrozenContext.SHA256 != episode.FrozenContext.SHA256 ||
		detail.Episode.CurrentStatus != episode.CurrentStatus {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{},
			errors.New("Shadow evaluation task state differs at its immutable cutoff")
	}
	attempt, err := shadowLearningInfluenceContainedAttempt(
		store,
		run,
		normalized.CreatedAt,
	)
	if err != nil {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{}, err
	}
	rebuiltInput, err := buildShadowTaskEvaluationInput(run, detail, attempt)
	if err != nil {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{}, err
	}
	rebuiltJSON, err := json.Marshal(rebuiltInput)
	if err != nil {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{}, err
	}
	storedJSON, err := json.Marshal(input)
	if err != nil {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{}, err
	}
	if string(rebuiltJSON) != string(storedJSON) {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{},
			errors.New(
				"Shadow evaluation input does not reconstruct from immutable evidence",
			)
	}
	return run, attempt, normalized, nil
}

func shadowLearningInfluenceFloatEqual(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return math.Abs(*left-*right) <= 1e-12
}

func shadowLearningInfluenceIntEqual(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func shadowLearningInfluenceValidateAblation(
	store *ActivityStore,
	episode TaskEpisode,
	selection workPatternFrozenSelection,
	evaluation WorkPatternProfileAblationEvaluation,
	cutoffAt int64,
) (ShadowRun, ShadowExecutionAttempt, ShadowRunEvaluation, error) {
	if cutoffAt > 0 && evaluation.CreatedAt > cutoffAt {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{},
			errors.New("ablation evaluation is newer than the cohort cutoff")
	}
	if evaluation.PolicyVersion != workPatternProfileAblationPolicyVersion ||
		evaluation.TaskEpisodeID != episode.ID ||
		strings.TrimSpace(evaluation.ShadowEvaluationID) == "" {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{},
			errors.New("ablation evaluation provenance is incomplete")
	}
	run, err := store.GetWorkPatternProfileAblationRun(evaluation.AblationRunID)
	if err != nil {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{}, err
	}
	if err := shadowLearningInfluenceValidateOnsetAttempt(
		store,
		run,
		episode,
		selection,
	); err != nil {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{}, err
	}
	shadowEvaluation, err := getShadowLearningInfluenceEvaluation(
		store,
		evaluation.ShadowEvaluationID,
	)
	if err != nil {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{}, err
	}
	shadowRun, attempt, shadowEvaluation, err :=
		shadowLearningInfluenceValidateShadowEvaluation(
			store,
			episode,
			selection,
			shadowEvaluation,
			cutoffAt,
		)
	if err != nil {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{}, err
	}
	observed, err := buildWorkPatternProfileAblationObservedReference(
		store,
		episode,
		shadowEvaluation,
		run,
	)
	if err != nil {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{}, err
	}
	metrics, neutralScore, profileScore, neutralBoundary,
		profileBoundary, winner :=
		workPatternProfileAblationMetricsForEvidence(run, observed)
	metricsJSON, err := json.Marshal(metrics)
	if err != nil {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{}, err
	}
	if !shadowLearningInfluenceFloatEqual(
		evaluation.NeutralScore,
		neutralScore,
	) || !shadowLearningInfluenceFloatEqual(
		evaluation.ProfileScore,
		profileScore,
	) || !shadowLearningInfluenceIntEqual(
		evaluation.NeutralBoundaryErrorMS,
		neutralBoundary,
	) || !shadowLearningInfluenceIntEqual(
		evaluation.ProfileBoundaryErrorMS,
		profileBoundary,
	) || evaluation.Winner != winner ||
		evaluation.EvidenceJSON != string(metricsJSON) {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{},
			errors.New("ablation evaluation does not recompute from immutable user evidence")
	}
	return shadowRun, attempt, shadowEvaluation, nil
}

func shadowLearningInfluenceCurrentResult(
	store *ActivityStore,
	episode TaskEpisode,
	selection workPatternFrozenSelection,
	cutoffAt int64,
) (ShadowRun, ShadowExecutionAttempt, ShadowRunEvaluation, bool, error) {
	runs, err := listAllShadowLearningInfluenceRuns(
		store,
		episode.ID,
		cutoffAt,
	)
	if err != nil {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{}, false, err
	}
	type validatedResult struct {
		run        ShadowRun
		attempt    ShadowExecutionAttempt
		evaluation ShadowRunEvaluation
	}
	validated := make([]validatedResult, 0)
	for _, run := range runs {
		if err := requireCurrentShadowTaskRun(run); err != nil ||
			run.TaskContextHash != episode.FrozenContext.SHA256 ||
			run.WorkProfileSnapshotID != selection.SnapshotID ||
			run.WorkProfileSHA256 != selection.SnapshotSHA256 {
			continue
		}
		evaluations, err := store.ListShadowRunEvaluations(run.ID)
		if err != nil {
			return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{}, false, err
		}
		for _, evaluation := range evaluations {
			if evaluation.CreatedAt > cutoffAt {
				continue
			}
			validatedRun, validatedAttempt, validatedEvaluation, validateErr :=
				shadowLearningInfluenceValidateShadowEvaluation(
					store,
					episode,
					selection,
					evaluation,
					cutoffAt,
				)
			if validateErr == nil {
				validated = append(validated, validatedResult{
					run:        validatedRun,
					attempt:    validatedAttempt,
					evaluation: validatedEvaluation,
				})
			}
		}
	}
	if len(validated) > 1 {
		return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{},
			false, errors.New(
				"later task has multiple current valid Shadow comparisons",
			)
	}
	if len(validated) == 1 {
		return validated[0].run, validated[0].attempt,
			validated[0].evaluation, true, nil
	}
	return ShadowRun{}, ShadowExecutionAttempt{}, ShadowRunEvaluation{}, false, nil
}

func shadowLearningInfluenceCandidateForEpisode(
	store *ActivityStore,
	base ShadowLearningInfluenceProof,
	source VerifiedShadowLearningSource,
	detail TaskEpisodeDetail,
) shadowLearningInfluenceCandidate {
	proof := base
	shadowLearningInfluenceAttachSource(&proof, source)
	selection := workPatternProfileSelectionFromEpisodeDetail(detail)
	shadowLearningInfluenceAttachEpisode(&proof, detail, selection)
	proof.State = ShadowLearningInfluenceLearningOmitted
	proof.Summary = "The later task did not receive this learning in both model stages."
	proof.RequiredAction = "Wait for another later task with exact profile admission."
	proof.Reasons = []string{}
	for _, sourceEpisodeID := range proof.SourceTaskEpisodeIDs {
		if sourceEpisodeID == detail.Episode.ID {
			proof.State = ShadowLearningInfluenceIntegrityError
			proof.Summary = "The candidate later task is part of the learning's source evidence."
			proof.RequiredAction = "Wait for a genuinely independent later task."
			proof.Reasons = append(proof.Reasons, "later_task_is_source_evidence")
			return shadowLearningInfluenceCandidate{proof: proof, rank: 2}
		}
	}
	for _, link := range detail.Links {
		if link.Relation == TaskEpisodeLinkMergedInto ||
			link.Relation == TaskEpisodeLinkSplitInto {
			proof.State = ShadowLearningInfluenceIntegrityError
			proof.Summary = "The candidate later task is part of an unresolved merge or split graph."
			proof.RequiredAction = "Resolve one canonical Task Episode before cohort measurement."
			proof.Reasons = append(
				proof.Reasons,
				"later_task_component_is_ambiguous",
			)
			return shadowLearningInfluenceCandidate{proof: proof, rank: 2}
		}
	}

	if selection.Version == "" &&
		shadowLearningInfluenceHasSelectionEvidence(detail) {
		proof.State = ShadowLearningInfluenceIntegrityError
		proof.Summary = "The later task has invalid profile-selection provenance."
		proof.RequiredAction = "Inspect the immutable selection evidence."
		proof.Reasons = append(proof.Reasons, "invalid_selection_provenance")
		return shadowLearningInfluenceCandidate{
			proof: proof,
			rank:  shadowLearningInfluenceStateRank(proof.State),
		}
	}
	if selection.Version != workPatternProfileSelectionEvidenceVersion {
		proof.Reasons = append(proof.Reasons, "exact_prompt_admission_not_recorded")
		return shadowLearningInfluenceCandidate{proof: proof, rank: 2}
	}
	if selection.SnapshotID == "" ||
		selection.SnapshotID != detail.Episode.FrozenContext.WorkProfileSnapshotID ||
		selection.SnapshotSHA256 != detail.Episode.FrozenContext.WorkProfileSHA256 ||
		selection.PolicyVersion != WorkPatternProfilePolicyVersion ||
		selection.SourceCutoffAt < source.Receipt.CreatedAt ||
		selection.SourceCutoffAt >= detail.Episode.InferredStartAt {
		proof.State = ShadowLearningInfluenceIntegrityError
		proof.Summary = "The profile snapshot does not match the later task cutoff."
		proof.RequiredAction = "Inspect the frozen task/profile linkage."
		proof.Reasons = append(proof.Reasons, "profile_snapshot_linkage_invalid")
		return shadowLearningInfluenceCandidate{proof: proof, rank: 2}
	}
	if err := shadowLearningInfluenceFrozenProfile(
		detail.Episode,
		selection,
	); err != nil {
		proof.State = ShadowLearningInfluenceIntegrityError
		proof.Summary = "The frozen Shadow profile does not match its admission record."
		proof.RequiredAction = "Inspect the frozen context and selection hashes."
		proof.Reasons = append(proof.Reasons, err.Error())
		return shadowLearningInfluenceCandidate{proof: proof, rank: 2}
	}
	onset, onsetErr := shadowLearningInfluenceEntry(
		selection.OnsetManifest,
		source.Learning,
	)
	shadow, shadowErr := shadowLearningInfluenceEntry(
		selection.ShadowManifest,
		source.Learning,
	)
	if onsetErr != nil || shadowErr != nil {
		proof.State = ShadowLearningInfluenceIntegrityError
		proof.Summary = "The learning has ambiguous frozen profile admission."
		proof.RequiredAction = "Inspect duplicate learning lineage in the manifest."
		proof.Reasons = append(proof.Reasons, "duplicate_learning_admission")
		return shadowLearningInfluenceCandidate{proof: proof, rank: 2}
	}
	if onset != nil {
		proof.OnsetPromptIncluded = onset.OnsetPromptIncluded
		proof.OnsetPromptReason = onset.OnsetPromptReason
	}
	if shadow != nil {
		proof.ShadowContextIncluded = shadow.ShadowContextIncluded
		proof.ShadowContextReason = shadow.ShadowContextReason
	}
	if !selection.OnsetProfilePresent || onset == nil ||
		onset.OnsetPromptIncluded == nil || !*onset.OnsetPromptIncluded {
		proof.Reasons = append(proof.Reasons, "learning_not_used_by_production_onset")
		return shadowLearningInfluenceCandidate{proof: proof, rank: 2}
	}
	if shadow == nil || shadow.ShadowContextIncluded == nil ||
		!*shadow.ShadowContextIncluded {
		proof.Reasons = append(proof.Reasons, "learning_not_used_by_shadow_context")
		return shadowLearningInfluenceCandidate{proof: proof, rank: 2}
	}

	proof.State = ShadowLearningInfluenceAwaitingShadowResult
	proof.Summary = "The learning entered both stages; the contained result is not evaluated yet."
	proof.RequiredAction = "Let the user finish the task and complete the Shadow comparison."
	proof.Reasons = []string{}
	if detail.Episode.CurrentStatus != TaskEpisodeStatusCompleted {
		proof.Reasons = append(proof.Reasons, "later_task_not_completed")
		return shadowLearningInfluenceCandidate{proof: proof, rank: 3}
	}
	shadowRun, attempt, shadowEvaluation, ready, err :=
		shadowLearningInfluenceCurrentResult(
			store,
			detail.Episode,
			selection,
			proof.GeneratedAt,
		)
	if err != nil {
		proof.State = ShadowLearningInfluenceIntegrityError
		proof.Summary = "The later Shadow result could not be validated."
		proof.RequiredAction = "Inspect the contained run and evaluation linkage."
		proof.Reasons = append(proof.Reasons, err.Error())
		return shadowLearningInfluenceCandidate{proof: proof, rank: 2}
	}
	if !ready {
		return shadowLearningInfluenceCandidate{proof: proof, rank: 3}
	}
	proof.ShadowRunID = shadowRun.ID
	proof.ShadowExecutionAttemptID = attempt.ID
	proof.ShadowEvaluationID = shadowEvaluation.ID
	proof.State = ShadowLearningInfluenceAwaitingAblation
	proof.Summary = "The later contained result is evaluated; the paired detector comparison is pending."
	proof.RequiredAction = "Complete the neutral/profile ablation evaluation."

	ablations, err := listAllShadowLearningInfluenceAblations(
		store,
		detail.Episode.ID,
		proof.GeneratedAt,
	)
	if err != nil {
		proof.State = ShadowLearningInfluenceIntegrityError
		proof.Summary = "The ablation ledger could not be read."
		proof.RequiredAction = "Inspect the ablation ledger."
		proof.Reasons = append(proof.Reasons, err.Error())
		return shadowLearningInfluenceCandidate{proof: proof, rank: 2}
	}
	var lastIntegrity error
	historicalAblations := 0
	type validatedAblation struct {
		evaluation       WorkPatternProfileAblationEvaluation
		run              ShadowRun
		attempt          ShadowExecutionAttempt
		shadowEvaluation ShadowRunEvaluation
	}
	validatedAblations := make([]validatedAblation, 0)
	for _, ablation := range ablations {
		if ablation.PolicyVersion != workPatternProfileAblationPolicyVersion {
			historicalAblations++
			continue
		}
		validatedRun, validatedAttempt, validatedEvaluation, validateErr :=
			shadowLearningInfluenceValidateAblation(
				store,
				detail.Episode,
				selection,
				ablation,
				proof.GeneratedAt,
			)
		if validateErr != nil {
			lastIntegrity = validateErr
			continue
		}
		validatedAblations = append(validatedAblations, validatedAblation{
			evaluation:       ablation,
			run:              validatedRun,
			attempt:          validatedAttempt,
			shadowEvaluation: validatedEvaluation,
		})
	}
	if lastIntegrity != nil {
		proof.State = ShadowLearningInfluenceIntegrityError
		proof.Summary = "A candidate ablation chain failed immutable provenance checks."
		proof.RequiredAction = "Inspect the exact onset, runtime, evaluation, and ablation joins."
		proof.Reasons = append(proof.Reasons, lastIntegrity.Error())
		return shadowLearningInfluenceCandidate{proof: proof, rank: 2}
	}
	if len(validatedAblations) > 1 {
		proof.State = ShadowLearningInfluenceIntegrityError
		proof.Summary = "The later task has more than one current valid ablation comparison."
		proof.RequiredAction = "Resolve one canonical paired comparison before cohort measurement."
		proof.Reasons = append(
			proof.Reasons,
			"multiple_current_ablation_comparisons",
		)
		return shadowLearningInfluenceCandidate{proof: proof, rank: 2}
	}
	if len(validatedAblations) == 1 {
		validated := validatedAblations[0]
		ablation := validated.evaluation
		proof.ShadowRunID = validated.run.ID
		proof.ShadowExecutionAttemptID = validated.attempt.ID
		proof.ShadowEvaluationID = validated.shadowEvaluation.ID
		proof.AblationRunID = ablation.AblationRunID
		proof.AblationEvaluationID = ablation.ID
		proof.NeutralScore = ablation.NeutralScore
		proof.ProfileScore = ablation.ProfileScore
		proof.Winner = ablation.Winner
		if ablation.NeutralScore == nil || ablation.ProfileScore == nil {
			proof.State = ShadowLearningInfluenceMeasuredUnknown
			proof.Summary = "The paired detector comparison is valid but independent evidence is not sufficient to score."
			proof.RequiredAction = "Observe another task with retained action and materialized-result evidence."
			proof.Reasons = append(
				proof.Reasons,
				"independent_observed_evidence_unscorable",
			)
			return shadowLearningInfluenceCandidate{
				proof: proof,
				rank:  shadowLearningInfluenceStateRank(proof.State),
			}
		}
		lift := *ablation.ProfileScore - *ablation.NeutralScore
		proof.Lift = &lift
		proof.Complete = true
		proof.Improved = ablation.Winner == WorkPatternInfluenceProfile && lift > 0
		if proof.Improved {
			proof.State = ShadowLearningInfluenceMeasuredImprovement
			proof.Summary = "Current Shadow mode evidence improved prediction on a later contained task."
			proof.RequiredAction = "Keep measuring later tasks that were not part of the source evidence before changing autonomy."
		} else {
			proof.State = ShadowLearningInfluenceMeasuredNoLift
			proof.Summary = "The full learning-to-later-task chain is measured, without lift yet."
			proof.RequiredAction = "Collect another independently observed later task before revising the learning."
		}
		return shadowLearningInfluenceCandidate{
			proof: proof,
			rank:  shadowLearningInfluenceStateRank(proof.State),
		}
	}
	if historicalAblations > 0 {
		proof.Reasons = append(
			proof.Reasons,
			"independent_ablation_not_recorded",
		)
		proof.RequiredAction = "Collect a current independent-evidence ablation; historical scores cannot qualify."
	}
	return shadowLearningInfluenceCandidate{proof: proof, rank: 4}
}

func shadowLearningInfluenceProofSteps(
	proof ShadowLearningInfluenceProof,
) []ShadowLearningInfluenceProofStep {
	hasSource := proof.LearningRevisionID != ""
	hasLater := proof.LaterTaskEpisodeID != ""
	onset := proof.OnsetPromptIncluded != nil && *proof.OnsetPromptIncluded &&
		proof.OnsetProfilePresent
	shadow := proof.ShadowContextIncluded != nil && *proof.ShadowContextIncluded
	hasResult := proof.ShadowRunID != "" &&
		proof.ShadowExecutionAttemptID != "" && proof.ShadowEvaluationID != ""
	hasAblation := proof.AblationRunID != "" &&
		proof.AblationEvaluationID != ""
	hasMeasurement := proof.NeutralScore != nil && proof.ProfileScore != nil
	return []ShadowLearningInfluenceProofStep{
		{Key: "source_learning", Label: "Current verified learning", Complete: hasSource},
		{Key: "later_task", Label: "Strictly later user task", Complete: hasLater},
		{Key: "onset_admission", Label: "Learning entered production onset", Complete: onset, Reason: proof.OnsetPromptReason},
		{Key: "shadow_admission", Label: "Learning entered isolated Shadow context", Complete: shadow, Reason: proof.ShadowContextReason},
		{Key: "contained_result", Label: "Contained result compared with user result", Complete: hasResult},
		{Key: "paired_ablation", Label: "Neutral and profile branches joined", Complete: hasAblation},
		{Key: "measured_lift", Label: "Prediction lift measured", Complete: hasMeasurement},
	}
}

func (s *ActivityStore) BuildShadowLearningInfluenceProof(
	now int64,
) (ShadowLearningInfluenceProof, error) {
	proof := newShadowLearningInfluenceProof(now)
	if s == nil || s.db == nil {
		return proof, errors.New("activity store is unavailable")
	}
	sources, details, err := loadShadowLearningInfluenceLedger(
		s,
		proof.GeneratedAt,
	)
	if err != nil {
		return proof, err
	}
	proof.SourceLearningCount = len(sources)
	if len(sources) == 0 {
		proof.Steps = shadowLearningInfluenceProofSteps(proof)
		return proof, nil
	}
	laterIDs := make(map[string]struct{})
	measuredChainCount := 0
	best := shadowLearningInfluenceCandidate{proof: proof}
	for _, source := range sources {
		hadLater := false
		for _, detail := range details {
			if detail.Episode.InferredStartAt <= source.Receipt.CreatedAt {
				continue
			}
			hadLater = true
			laterIDs[detail.Episode.ID] = struct{}{}
			candidate := shadowLearningInfluenceCandidateForEpisode(
				s,
				proof,
				source,
				detail,
			)
			if candidate.proof.Complete {
				measuredChainCount++
			}
			if candidate.rank > best.rank ||
				(candidate.rank == best.rank &&
					candidate.proof.LaterTaskStartedAt >
						best.proof.LaterTaskStartedAt) {
				best = candidate
			}
		}
		if !hadLater && best.rank == 0 {
			candidateProof := proof
			shadowLearningInfluenceAttachSource(&candidateProof, source)
			candidateProof.State = ShadowLearningInfluenceAwaitingLaterTask
			candidateProof.Summary = "An evaluated Shadow comparison exists; no later task has begun yet."
			candidateProof.RequiredAction = "Observe the next separate task after this learning."
			best = shadowLearningInfluenceCandidate{
				proof: candidateProof,
				rank:  1,
			}
		}
	}
	proof = best.proof
	proof.SourceLearningCount = len(sources)
	proof.LaterTaskCount = len(laterIDs)
	proof.MeasuredChainCount = measuredChainCount
	proof.Steps = shadowLearningInfluenceProofSteps(proof)
	proof.Reasons = uniqueSortedStrings(proof.Reasons)
	return proof, nil
}
