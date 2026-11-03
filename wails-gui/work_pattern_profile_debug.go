package main

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
)

type WorkPatternProfileDebugSummary struct {
	ID                    string  `json:"id"`
	SHA256                string  `json:"sha256"`
	PolicyVersion         string  `json:"policyVersion"`
	Status                string  `json:"status"`
	SourceWindowStart     int64   `json:"sourceWindowStart"`
	SourceCutoffAt        int64   `json:"sourceCutoffAt"`
	SampleCount           int     `json:"sampleCount"`
	SessionCount          int     `json:"sessionCount"`
	Confidence            float64 `json:"confidence"`
	ProjectionCount       int     `json:"projectionCount"`
	EvidenceCount         int     `json:"evidenceCount"`
	ExclusionCount        int     `json:"exclusionCount"`
	CorrectionCount       int     `json:"correctionCount"`
	ConsumingEpisodeCount int     `json:"consumingEpisodeCount"`
	ConsumingRunCount     int     `json:"consumingRunCount"`
	AblationRunCount      int     `json:"ablationRunCount"`
	CreatedAt             int64   `json:"createdAt"`
}

type WorkPatternProfileDebugSnapshot struct {
	GeneratedAt       int64                                 `json:"generatedAt"`
	Limit             int                                   `json:"limit"`
	Profiles          []WorkPatternProfileDebugSummary      `json:"profiles"`
	Rollout           WorkPatternProfileRolloutStatus       `json:"rollout"`
	LearningInfluence ShadowLearningInfluenceProof          `json:"learningInfluence"`
	LearningCohorts   ShadowLearningInfluenceCohortSnapshot `json:"learningCohorts"`
}

type WorkPatternProfileDebugEpisode struct {
	ID                              string                             `json:"id"`
	Status                          string                             `json:"status"`
	Task                            string                             `json:"task"`
	DesiredResult                   string                             `json:"desiredResult"`
	InferredStartAt                 int64                              `json:"inferredStartAt"`
	ContextSnapshotID               string                             `json:"contextSnapshotId"`
	ContextSHA256                   string                             `json:"contextSha256"`
	WorkProfileSHA256               string                             `json:"workProfileSha256"`
	ProfileSelectionVersion         string                             `json:"profileSelectionVersion"`
	ProfileOnsetPresent             bool                               `json:"profileOnsetPresent"`
	ProfileOnsetSourceContextSHA256 string                             `json:"profileOnsetSourceContextSha256"`
	ProfileOnsetPromptSHA256        string                             `json:"profileOnsetPromptSha256"`
	ProfileOnsetSelectionSHA256     string                             `json:"profileOnsetSelectionSha256"`
	OnsetProfileSelectionManifest   []WorkPatternProfileSelectionEntry `json:"onsetProfileSelectionManifest"`
	OnsetProfileSelectionReason     string                             `json:"onsetProfileSelectionReason"`
	ProfileSourceContextSHA256      string                             `json:"profileSourceContextSha256"`
	ProfilePromptSHA256             string                             `json:"profilePromptSha256"`
	ProfileSelectionSHA256          string                             `json:"profileSelectionSha256"`
	ProfileInfluenceRefs            []string                           `json:"profileInfluenceRefs"`
	ProfileConflict                 bool                               `json:"profileConflict"`
	ProfileSelectionManifest        []WorkPatternProfileSelectionEntry `json:"profileSelectionManifest"`
	ProfileSelectionReason          string                             `json:"profileSelectionReason"`
	IncludedContextCount            int                                `json:"includedContextCount"`
	ExcludedContextCount            int                                `json:"excludedContextCount"`
}

func workPatternProfileSelectionFromFrozenContext(
	canonicalJSON string,
) ([]WorkPatternProfileSelectionEntry, string) {
	var context TaskContextPayload
	if err := json.Unmarshal([]byte(canonicalJSON), &context); err != nil {
		return []WorkPatternProfileSelectionEntry{}, ""
	}
	for _, section := range context.Sections {
		if section.Name != TaskContextSectionWorkProfile {
			continue
		}
		for _, evidence := range section.Evidence {
			if evidence.SourceKind != "work_pattern_profile" {
				continue
			}
			var profile workPatternProfileContextPayload
			if err := json.Unmarshal(
				[]byte(evidence.Content),
				&profile,
			); err != nil {
				continue
			}
			return append(
				[]WorkPatternProfileSelectionEntry{},
				profile.SelectionManifest...,
			), profile.SelectionReason
		}
	}
	return []WorkPatternProfileSelectionEntry{}, ""
}

func workPatternProfilePromptHashFromFrozenContext(
	canonicalJSON string,
) string {
	var context TaskContextPayload
	if err := json.Unmarshal([]byte(canonicalJSON), &context); err != nil {
		return ""
	}
	for _, section := range context.Sections {
		if section.Name != TaskContextSectionWorkProfile {
			continue
		}
		for _, evidence := range section.Evidence {
			if evidence.SourceKind == "work_pattern_profile" {
				return taskContextHash(evidence.Content)
			}
		}
	}
	return ""
}

type workPatternFrozenSelection struct {
	Version                      string
	SnapshotID                   string
	SnapshotSHA256               string
	PolicyVersion                string
	SourceCutoffAt               int64
	OnsetProfilePresent          bool
	OnsetManifest                []WorkPatternProfileSelectionEntry
	OnsetReason                  string
	OnsetSourceContextSHA256     string
	OnsetPromptSHA256            string
	OnsetSourceSelectionSHA256   string
	OnsetPromptSelectionSHA256   string
	OnsetSelectionSHA256         string
	ShadowManifest               []WorkPatternProfileSelectionEntry
	ShadowReason                 string
	ShadowSourceContextSHA256    string
	ShadowPromptSHA256           string
	ShadowSourceSelectionSHA256  string
	ShadowContextSelectionSHA256 string
	ShadowSelectionSHA256        string
}

func workPatternProfileSelectionFromEpisodeDetail(
	detail TaskEpisodeDetail,
) workPatternFrozenSelection {
	expectedPromptHash := workPatternProfilePromptHashFromFrozenContext(
		detail.Episode.FrozenContext.CanonicalJSON,
	)
	for _, evidence := range detail.Evidence {
		provenance, ok := decodeWorkPatternProfileSelectionEvidence(evidence)
		if !ok ||
			provenance.SnapshotID !=
				detail.Episode.FrozenContext.WorkProfileSnapshotID ||
			provenance.SnapshotSHA256 !=
				detail.Episode.FrozenContext.WorkProfileSHA256 ||
			provenance.PromptProjectionSHA256 != expectedPromptHash {
			continue
		}
		return workPatternFrozenSelection{
			Version:             provenance.Version,
			SnapshotID:          provenance.SnapshotID,
			SnapshotSHA256:      provenance.SnapshotSHA256,
			PolicyVersion:       provenance.PolicyVersion,
			SourceCutoffAt:      provenance.SourceCutoffAt,
			OnsetProfilePresent: provenance.OnsetProfilePresent,
			OnsetManifest: append(
				[]WorkPatternProfileSelectionEntry(nil),
				provenance.OnsetSelectionManifest...,
			),
			OnsetReason:                provenance.OnsetSelectionReason,
			OnsetSourceContextSHA256:   provenance.OnsetSourceContextSHA256,
			OnsetPromptSHA256:          provenance.OnsetPromptProjectionSHA256,
			OnsetSourceSelectionSHA256: provenance.OnsetSourceSelectionManifestSHA256,
			OnsetPromptSelectionSHA256: provenance.OnsetPromptSelectionManifestSHA256,
			OnsetSelectionSHA256:       provenance.OnsetSelectionManifestSHA256,
			ShadowManifest: append(
				[]WorkPatternProfileSelectionEntry(nil),
				provenance.SelectionManifest...,
			),
			ShadowReason:                 provenance.SelectionReason,
			ShadowSourceContextSHA256:    provenance.SourceContextSHA256,
			ShadowPromptSHA256:           provenance.PromptProjectionSHA256,
			ShadowSourceSelectionSHA256:  provenance.SourceSelectionManifestSHA256,
			ShadowContextSelectionSHA256: provenance.ShadowContextSelectionSHA256,
			ShadowSelectionSHA256:        provenance.SelectionManifestSHA256,
		}
	}
	manifest, reason := workPatternProfileSelectionFromFrozenContext(
		detail.Episode.FrozenContext.CanonicalJSON,
	)
	return workPatternFrozenSelection{
		ShadowManifest:     manifest,
		ShadowReason:       reason,
		ShadowPromptSHA256: expectedPromptHash,
	}
}

type WorkPatternProfileDebugRun struct {
	ID                   string   `json:"id"`
	TaskEpisodeID        string   `json:"taskEpisodeId"`
	CapabilityKey        string   `json:"capabilityKey"`
	Status               string   `json:"status"`
	Model                string   `json:"model"`
	PromptVersion        string   `json:"promptVersion"`
	ContextSHA256        string   `json:"contextSha256"`
	ProfileSHA256        string   `json:"profileSha256"`
	ProfileInfluenceRefs []string `json:"profileInfluenceRefs"`
	ProfileConflict      bool     `json:"profileConflict"`
	CreatedAt            int64    `json:"createdAt"`
}

type WorkPatternProfileReplayBundle struct {
	ProfileSnapshotID string   `json:"profileSnapshotId"`
	ProfileSHA256     string   `json:"profileSha256"`
	SourceCutoffAt    int64    `json:"sourceCutoffAt"`
	OnsetAttemptIDs   []string `json:"onsetAttemptIds"`
	TaskEpisodeIDs    []string `json:"taskEpisodeIds"`
	ShadowRunIDs      []string `json:"shadowRunIds"`
}

type WorkPatternProfilePromptDiagnostic struct {
	Version                 string `json:"version"`
	InfluenceMode           string `json:"influenceMode"`
	SourceSnapshotBytes     int    `json:"sourceSnapshotBytes"`
	SourceContextBytes      int    `json:"sourceContextBytes"`
	PromptProjectionBytes   int    `json:"promptProjectionBytes"`
	PromptBudgetBytes       int    `json:"promptBudgetBytes"`
	RawReferenceCount       int    `json:"rawReferenceCount"`
	SelectedProjectionCount int    `json:"selectedProjectionCount"`
	CapabilitySignalCount   int    `json:"capabilitySignalCount"`
	ContradictionCount      int    `json:"contradictionCount"`
	ManifestEntryCount      int    `json:"manifestEntryCount"`
	PromptCandidateCount    int    `json:"promptCandidateCount"`
	PromptIncludedCount     int    `json:"promptIncludedCount"`
	PromptOmittedCount      int    `json:"promptOmittedCount"`
	SourceContextSHA256     string `json:"sourceContextSha256"`
	PromptProjectionSHA256  string `json:"promptProjectionSha256"`
	SelectionManifestSHA256 string `json:"selectionManifestSha256"`
	Fits                    bool   `json:"fits"`
	Error                   string `json:"error,omitempty"`
}

type WorkPatternProfileDebugDetail struct {
	GeneratedAt      int64                                  `json:"generatedAt"`
	Snapshot         WorkPatternProfileSnapshot             `json:"snapshot"`
	PromptDiagnostic WorkPatternProfilePromptDiagnostic     `json:"promptDiagnostic"`
	Evidence         []WorkPatternProfileEvidence           `json:"evidence"`
	Corrections      []WorkPatternProfileCorrection         `json:"corrections"`
	Episodes         []WorkPatternProfileDebugEpisode       `json:"episodes"`
	ShadowRuns       []WorkPatternProfileDebugRun           `json:"shadowRuns"`
	Ablations        []WorkPatternProfileAblationRun        `json:"ablations"`
	Evaluations      []WorkPatternProfileAblationEvaluation `json:"evaluations"`
	ModelAttempts    []TaskAgentAttempt                     `json:"modelAttempts"`
	Replay           WorkPatternProfileReplayBundle         `json:"replay"`
	Rollout          WorkPatternProfileRolloutStatus        `json:"rollout"`
}

func init() {
	browserRPCAllowlist["GetWorkPatternProfileDebugSnapshot"] = true
	browserRPCAllowlist["GetWorkPatternProfileDebugDetail"] = true
}

func normalizeWorkPatternProfileDebugLimit(limit int) int {
	switch {
	case limit <= 0:
		return 50
	case limit > workPatternProfileMaximumList:
		return workPatternProfileMaximumList
	default:
		return limit
	}
}

func workPatternProfilePromptDiagnosticKeys(
	snapshot WorkPatternProfileSnapshot,
) ([]string, []string, int) {
	apps := make(map[string]struct{})
	transitions := make(map[string]struct{})
	rawReferences := 0
	for _, projection := range snapshot.Projections {
		rawReferences += len(projection.IntervalRefs) +
			len(projection.SessionRefs)
		switch projection.ScopeKind {
		case WorkPatternScopeApp:
			if value := strings.TrimSpace(projection.ScopeKey); value != "" {
				apps[value] = struct{}{}
			}
		case WorkPatternScopeTransition:
			if value := strings.TrimSpace(projection.ScopeKey); value != "" {
				transitions[value] = struct{}{}
			}
		case WorkPatternScopeWorkMode:
			for _, app := range projection.Apps {
				if value := strings.TrimSpace(app); value != "" {
					apps[value] = struct{}{}
				}
			}
		}
	}
	appKeys := make([]string, 0, len(apps))
	for value := range apps {
		appKeys = append(appKeys, value)
	}
	transitionKeys := make([]string, 0, len(transitions))
	for value := range transitions {
		transitionKeys = append(transitionKeys, value)
	}
	sort.Strings(appKeys)
	sort.Strings(transitionKeys)
	return appKeys, transitionKeys, rawReferences
}

func buildWorkPatternProfilePromptDiagnostic(
	store *ActivityStore,
	snapshot WorkPatternProfileSnapshot,
) WorkPatternProfilePromptDiagnostic {
	result := WorkPatternProfilePromptDiagnostic{
		Version:             "work-pattern-prompt-diagnostic-v2",
		InfluenceMode:       WorkPatternInfluenceProfile,
		SourceSnapshotBytes: len(snapshot.CanonicalJSON),
		PromptBudgetBytes:   workPatternProfilePromptMaximumBytes,
	}
	appKeys, transitionKeys, rawReferences :=
		workPatternProfilePromptDiagnosticKeys(snapshot)
	result.RawReferenceCount = rawReferences

	fullContext, err := buildWorkPatternProfileContext(
		store,
		snapshot,
		TaskContextSourceResult{},
		"",
		appKeys,
		transitionKeys,
		WorkPatternInfluenceProfile,
	)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.SourceContextBytes = len(fullContext.CanonicalJSON)
	result.SelectedProjectionCount = len(fullContext.SelectedProjections)
	result.CapabilitySignalCount = len(fullContext.CapabilitySignals)
	result.ContradictionCount = len(fullContext.Contradictions)

	promptProjection, err := compileWorkPatternProfilePrompt(
		fullContext,
		workPatternProfilePromptMaximumBytes,
	)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	encoded, err := encodedWorkPatternProfilePromptBytes(promptProjection)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.PromptProjectionBytes = len(encoded)
	result.SourceContextSHA256 = promptProjection.SourceContextSHA256
	result.PromptProjectionSHA256 = taskContextHash(string(encoded))
	result.SelectionManifestSHA256 =
		promptProjection.SelectionManifestSHA256
	if promptProjection.SelectionSummary != nil {
		result.ManifestEntryCount =
			promptProjection.SelectionSummary.ManifestEntryCount
		result.PromptCandidateCount =
			promptProjection.SelectionSummary.PromptCandidateCount
		result.PromptIncludedCount =
			promptProjection.SelectionSummary.PromptIncludedCount
		result.PromptOmittedCount =
			promptProjection.SelectionSummary.PromptOmittedCount
	}
	result.Fits = result.PromptProjectionBytes > 0 &&
		result.PromptProjectionBytes <= result.PromptBudgetBytes &&
		len(result.SourceContextSHA256) == 64 &&
		len(result.PromptProjectionSHA256) == 64 &&
		len(result.SelectionManifestSHA256) == 64
	if !result.Fits {
		result.Error = "compiled prompt projection failed its envelope invariant"
	}
	return result
}

func listWorkPatternProfileAblationsForSnapshot(
	store *ActivityStore,
	snapshotID string,
	limit int,
) ([]WorkPatternProfileAblationRun, error) {
	rows, err := store.db.Query(`
		SELECT `+workPatternAblationRunSelectColumns+`
		FROM work_pattern_profile_ablation_runs_v1
		WHERE profile_snapshot_id = ?
		ORDER BY created_at DESC, rowid DESC
		LIMIT ?`,
		snapshotID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]WorkPatternProfileAblationRun, 0)
	for rows.Next() {
		item, err := scanWorkPatternProfileAblationRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func listWorkPatternProfileAblationEvaluationsForSnapshot(
	store *ActivityStore,
	snapshotID string,
	limit int,
) ([]WorkPatternProfileAblationEvaluation, error) {
	rows, err := store.db.Query(`
		SELECT
			e.id, e.ablation_run_id, e.task_episode_id,
			e.shadow_evaluation_id, e.policy_version, e.neutral_score,
			e.profile_score, e.neutral_boundary_error_ms,
			e.profile_boundary_error_ms, e.winner, e.evidence_json,
			e.created_at
		FROM work_pattern_profile_ablation_evaluations_v1 e
		JOIN work_pattern_profile_ablation_runs_v1 r
			ON r.id = e.ablation_run_id
		WHERE r.profile_snapshot_id = ?
		ORDER BY e.created_at DESC, e.rowid DESC
		LIMIT ?`,
		snapshotID,
		limit,
	)
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

func listWorkPatternProfileDebugEpisodes(
	store *ActivityStore,
	snapshotID string,
	limit int,
) ([]WorkPatternProfileDebugEpisode, error) {
	rows, err := store.db.Query(`
		SELECT task_episode_id
		FROM task_episode_context_snapshots
		WHERE work_profile_snapshot_id = ?
		ORDER BY created_at DESC, rowid DESC
		LIMIT ?`,
		snapshotID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	result := make([]WorkPatternProfileDebugEpisode, 0, len(ids))
	for _, id := range ids {
		detail, err := store.GetTaskEpisodeDetail(id)
		if err != nil {
			return nil, err
		}
		episode := detail.Episode
		included, excluded := 0, 0
		for _, item := range episode.FrozenContext.Manifest {
			if item.Included {
				included++
			} else {
				excluded++
			}
		}
		selection := workPatternProfileSelectionFromEpisodeDetail(detail)
		result = append(result, WorkPatternProfileDebugEpisode{
			ID:                              episode.ID,
			Status:                          episode.CurrentStatus,
			Task:                            episode.LatestHypothesis.TaskHypothesis,
			DesiredResult:                   episode.LatestHypothesis.DesiredResultHypothesis,
			InferredStartAt:                 episode.InferredStartAt,
			ContextSnapshotID:               episode.FrozenContext.ID,
			ContextSHA256:                   episode.FrozenContext.SHA256,
			WorkProfileSHA256:               episode.FrozenContext.WorkProfileSHA256,
			ProfileSelectionVersion:         selection.Version,
			ProfileOnsetPresent:             selection.OnsetProfilePresent,
			ProfileOnsetSourceContextSHA256: selection.OnsetSourceContextSHA256,
			ProfileOnsetPromptSHA256:        selection.OnsetPromptSHA256,
			ProfileOnsetSelectionSHA256:     selection.OnsetSelectionSHA256,
			OnsetProfileSelectionManifest:   selection.OnsetManifest,
			OnsetProfileSelectionReason:     selection.OnsetReason,
			ProfileSourceContextSHA256:      selection.ShadowSourceContextSHA256,
			ProfilePromptSHA256:             selection.ShadowPromptSHA256,
			ProfileSelectionSHA256:          selection.ShadowSelectionSHA256,
			ProfileInfluenceRefs: append(
				[]string{},
				episode.LatestHypothesis.ProfileInfluenceRefs...,
			),
			ProfileConflict:          episode.LatestHypothesis.ProfileConflict,
			ProfileSelectionManifest: selection.ShadowManifest,
			ProfileSelectionReason:   selection.ShadowReason,
			IncludedContextCount:     included,
			ExcludedContextCount:     excluded,
		})
	}
	return result, nil
}

func listWorkPatternProfileDebugRuns(
	store *ActivityStore,
	snapshotID string,
	limit int,
	now int64,
) ([]WorkPatternProfileDebugRun, error) {
	rows, err := store.db.Query(`
		SELECT id
		FROM shadow_runs
		WHERE work_profile_snapshot_id = ?
		ORDER BY created_at DESC, rowid DESC
		LIMIT ?`,
		snapshotID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	result := make([]WorkPatternProfileDebugRun, 0, len(ids))
	for _, id := range ids {
		detail, err := store.GetShadowRunDetail(id, now)
		if err != nil {
			return nil, err
		}
		run := detail.Run
		result = append(result, WorkPatternProfileDebugRun{
			ID:            run.ID,
			TaskEpisodeID: run.TaskEpisodeID,
			CapabilityKey: run.CapabilityKey,
			Status:        detail.Status,
			Model:         run.Model,
			PromptVersion: run.PromptVersion,
			ContextSHA256: run.TaskContextHash,
			ProfileSHA256: run.WorkProfileSHA256,
			ProfileInfluenceRefs: append(
				[]string{},
				run.Prediction.ProfileInfluenceRefs...,
			),
			ProfileConflict: run.Prediction.ProfileConflict,
			CreatedAt:       run.CreatedAt,
		})
	}
	return result, nil
}

func workPatternProfileDebugCorrections(
	store *ActivityStore,
	snapshot WorkPatternProfileSnapshot,
) ([]WorkPatternProfileCorrection, error) {
	all, err := store.ListWorkPatternProfileCorrections(time.Now().UnixMilli())
	if err != nil {
		return nil, err
	}
	scopes := make(map[string]struct{}, len(snapshot.Projections))
	for _, projection := range snapshot.Projections {
		scopes[workPatternProjectionKey(projection)] = struct{}{}
	}
	result := make([]WorkPatternProfileCorrection, 0)
	for _, correction := range all {
		key := correction.ScopeKind + "\x00" + correction.ScopeKey
		if correction.ProfileSnapshotID == snapshot.ID {
			result = append(result, correction)
			continue
		}
		if _, ok := scopes[key]; ok &&
			correction.CreatedAt > snapshot.SourceCutoffAt {
			result = append(result, correction)
		}
	}
	return result, nil
}

type workPatternProfileDebugCounts struct {
	Evidence    int
	Corrections int
	Episodes    int
	Runs        int
	Ablations   int
}

func listWorkPatternProfileDebugSummaries(
	store *ActivityStore,
	limit int,
) ([]WorkPatternProfileDebugSummary, error) {
	rows, err := store.db.Query(`
		SELECT
			id, sha256, policy_version, status, source_window_start,
			source_cutoff_at, sample_count, session_count, confidence,
			COALESCE(json_array_length(canonical_json, '$.projections'), 0),
			COALESCE(json_array_length(canonical_json, '$.exclusions'), 0),
			created_at
		FROM work_pattern_profile_snapshots_v1
		ORDER BY source_cutoff_at DESC, created_at DESC
		LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]WorkPatternProfileDebugSummary, 0, limit)
	for rows.Next() {
		var item WorkPatternProfileDebugSummary
		if err := rows.Scan(
			&item.ID,
			&item.SHA256,
			&item.PolicyVersion,
			&item.Status,
			&item.SourceWindowStart,
			&item.SourceCutoffAt,
			&item.SampleCount,
			&item.SessionCount,
			&item.Confidence,
			&item.ProjectionCount,
			&item.ExclusionCount,
			&item.CreatedAt,
		); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func workPatternProfileDebugSummaryCounts(
	store *ActivityStore,
	snapshotIDs []string,
	now int64,
) (map[string]workPatternProfileDebugCounts, error) {
	result := make(map[string]workPatternProfileDebugCounts, len(snapshotIDs))
	if len(snapshotIDs) == 0 {
		return result, nil
	}
	placeholders := strings.TrimSuffix(
		strings.Repeat("?,", len(snapshotIDs)),
		",",
	)
	args := make([]any, 0, len(snapshotIDs)*5+1)
	appendSnapshotIDs := func() {
		for _, snapshotID := range snapshotIDs {
			args = append(args, snapshotID)
		}
	}
	appendSnapshotIDs()
	appendSnapshotIDs()
	args = append(args, now)
	appendSnapshotIDs()
	appendSnapshotIDs()
	appendSnapshotIDs()
	rows, err := store.db.Query(`
		SELECT profile_snapshot_id, 'evidence', COUNT(*)
		FROM work_pattern_profile_evidence_v1
		WHERE profile_snapshot_id IN (`+placeholders+`)
		GROUP BY profile_snapshot_id

		UNION ALL

		SELECT profile_snapshot_id, 'correction', COUNT(*)
		FROM work_pattern_profile_corrections_v1
		WHERE profile_snapshot_id IN (`+placeholders+`)
			AND created_at <= ?
		GROUP BY profile_snapshot_id

		UNION ALL

		SELECT work_profile_snapshot_id, 'episode', COUNT(*)
		FROM task_episode_context_snapshots
		WHERE work_profile_snapshot_id IN (`+placeholders+`)
		GROUP BY work_profile_snapshot_id

		UNION ALL

		SELECT work_profile_snapshot_id, 'run', COUNT(*)
		FROM shadow_runs
		WHERE work_profile_snapshot_id IN (`+placeholders+`)
		GROUP BY work_profile_snapshot_id

		UNION ALL

		SELECT profile_snapshot_id, 'ablation', COUNT(*)
		FROM work_pattern_profile_ablation_runs_v1
		WHERE profile_snapshot_id IN (`+placeholders+`)
		GROUP BY profile_snapshot_id`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var snapshotID, kind string
		var count int
		if err := rows.Scan(&snapshotID, &kind, &count); err != nil {
			return nil, err
		}
		counts := result[snapshotID]
		switch kind {
		case "evidence":
			counts.Evidence = count
		case "correction":
			counts.Corrections = count
		case "episode":
			counts.Episodes = count
		case "run":
			counts.Runs = count
		case "ablation":
			counts.Ablations = count
		}
		result[snapshotID] = counts
	}
	return result, rows.Err()
}

func (a *App) GetWorkPatternProfileDebugSnapshot(
	limit int,
) (WorkPatternProfileDebugSnapshot, error) {
	if a == nil || a.activity == nil || a.activity.store == nil {
		return WorkPatternProfileDebugSnapshot{}, errors.New(
			"activity store is unavailable",
		)
	}
	limit = normalizeWorkPatternProfileDebugLimit(limit)
	store := a.activity.store
	now := time.Now().UnixMilli()
	profiles, err := listWorkPatternProfileDebugSummaries(store, limit)
	if err != nil {
		return WorkPatternProfileDebugSnapshot{}, err
	}
	snapshotIDs := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		snapshotIDs = append(snapshotIDs, profile.ID)
	}
	summaryCounts, err := workPatternProfileDebugSummaryCounts(
		store,
		snapshotIDs,
		now,
	)
	if err != nil {
		return WorkPatternProfileDebugSnapshot{}, err
	}
	result := WorkPatternProfileDebugSnapshot{
		GeneratedAt: now,
		Limit:       limit,
		Profiles:    profiles,
	}
	for index := range result.Profiles {
		counts := summaryCounts[result.Profiles[index].ID]
		result.Profiles[index].EvidenceCount = counts.Evidence
		result.Profiles[index].CorrectionCount = counts.Corrections
		result.Profiles[index].ConsumingEpisodeCount = counts.Episodes
		result.Profiles[index].ConsumingRunCount = counts.Runs
		result.Profiles[index].AblationRunCount = counts.Ablations
	}
	result.Rollout, err = a.workPatternProfileRolloutStatusAt(now)
	if err != nil {
		return WorkPatternProfileDebugSnapshot{}, err
	}
	result.LearningInfluence, err = store.BuildShadowLearningInfluenceProof(now)
	if err != nil {
		return WorkPatternProfileDebugSnapshot{}, err
	}
	result.LearningCohorts, err =
		store.BuildShadowLearningInfluenceCohortSnapshot(now)
	if err != nil {
		return WorkPatternProfileDebugSnapshot{}, err
	}
	return result, nil
}

func (a *App) GetWorkPatternProfileDebugDetail(
	snapshotID string,
	limit int,
) (WorkPatternProfileDebugDetail, error) {
	if a == nil || a.activity == nil || a.activity.store == nil {
		return WorkPatternProfileDebugDetail{}, errors.New(
			"activity store is unavailable",
		)
	}
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" {
		return WorkPatternProfileDebugDetail{}, errors.New(
			"work pattern profile snapshot ID is required",
		)
	}
	limit = normalizeWorkPatternProfileDebugLimit(limit)
	store := a.activity.store
	snapshot, err := store.GetWorkPatternProfileSnapshot(snapshotID)
	if err != nil {
		return WorkPatternProfileDebugDetail{}, err
	}
	evidence, err := store.ListWorkPatternProfileEvidence(snapshot.ID)
	if err != nil {
		return WorkPatternProfileDebugDetail{}, err
	}
	corrections, err := workPatternProfileDebugCorrections(store, snapshot)
	if err != nil {
		return WorkPatternProfileDebugDetail{}, err
	}
	episodes, err := listWorkPatternProfileDebugEpisodes(
		store,
		snapshot.ID,
		limit,
	)
	if err != nil {
		return WorkPatternProfileDebugDetail{}, err
	}
	runs, err := listWorkPatternProfileDebugRuns(
		store,
		snapshot.ID,
		limit,
		time.Now().UnixMilli(),
	)
	if err != nil {
		return WorkPatternProfileDebugDetail{}, err
	}
	ablations, err := listWorkPatternProfileAblationsForSnapshot(
		store,
		snapshot.ID,
		limit,
	)
	if err != nil {
		return WorkPatternProfileDebugDetail{}, err
	}
	evaluations, err :=
		listWorkPatternProfileAblationEvaluationsForSnapshot(
			store,
			snapshot.ID,
			limit,
		)
	if err != nil {
		return WorkPatternProfileDebugDetail{}, err
	}
	attemptIDs := make([]string, 0, len(ablations)*2)
	for _, ablation := range ablations {
		attemptIDs = append(
			attemptIDs,
			ablation.NeutralAttemptID,
			ablation.ProfileAttemptID,
		)
	}
	attemptIDs = uniqueSortedStrings(attemptIDs)
	attempts := make([]TaskAgentAttempt, 0, len(attemptIDs))
	for _, id := range attemptIDs {
		attempt, err := store.GetTaskAgentAttempt(id)
		if err != nil {
			return WorkPatternProfileDebugDetail{}, err
		}
		attempts = append(attempts, attempt)
	}
	sort.SliceStable(attempts, func(i, j int) bool {
		if attempts[i].CreatedAt != attempts[j].CreatedAt {
			return attempts[i].CreatedAt > attempts[j].CreatedAt
		}
		return attempts[i].ID < attempts[j].ID
	})
	rollout, err := a.workPatternProfileRolloutStatus()
	if err != nil {
		return WorkPatternProfileDebugDetail{}, err
	}
	promptDiagnostic := buildWorkPatternProfilePromptDiagnostic(
		store,
		snapshot,
	)
	episodeIDs := make([]string, 0, len(episodes))
	for _, episode := range episodes {
		episodeIDs = append(episodeIDs, episode.ID)
	}
	runIDs := make([]string, 0, len(runs))
	for _, run := range runs {
		runIDs = append(runIDs, run.ID)
	}
	return WorkPatternProfileDebugDetail{
		GeneratedAt:      time.Now().UnixMilli(),
		Snapshot:         snapshot,
		PromptDiagnostic: promptDiagnostic,
		Evidence:         evidence,
		Corrections:      corrections,
		Episodes:         episodes,
		ShadowRuns:       runs,
		Ablations:        ablations,
		Evaluations:      evaluations,
		ModelAttempts:    attempts,
		Replay: WorkPatternProfileReplayBundle{
			ProfileSnapshotID: snapshot.ID,
			ProfileSHA256:     snapshot.SHA256,
			SourceCutoffAt:    snapshot.SourceCutoffAt,
			OnsetAttemptIDs:   attemptIDs,
			TaskEpisodeIDs:    uniqueSortedStrings(episodeIDs),
			ShadowRunIDs:      uniqueSortedStrings(runIDs),
		},
		Rollout: rollout,
	}, nil
}
