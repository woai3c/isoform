package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	workPatternProfilePromptProjectionVersion = "work-pattern-prompt-v2"
	workPatternProfilePromptMaximumBytes      = 15_000
	workPatternProfilePromptMinimumBytes      = 768

	workPatternProfileSelectionEvidenceSource        = "work_pattern_profile_selection"
	workPatternProfileSelectionEvidenceVersion       = "work-pattern-selection-provenance-v3"
	workPatternProfileSelectionEvidenceV2Version     = "work-pattern-selection-provenance-v2"
	workPatternProfileSelectionEvidenceLegacyVersion = "work-pattern-selection-provenance-v1"
)

type workPatternProfileSelectionProvenance struct {
	Version                            string                             `json:"version"`
	SnapshotID                         string                             `json:"snapshotId"`
	SnapshotSHA256                     string                             `json:"snapshotSha256"`
	PolicyVersion                      string                             `json:"policyVersion"`
	SourceCutoffAt                     int64                              `json:"sourceCutoffAt"`
	OnsetProfilePresent                bool                               `json:"onsetProfilePresent"`
	OnsetSourceContextSHA256           string                             `json:"onsetSourceContextSha256,omitempty"`
	OnsetPromptProjectionSHA256        string                             `json:"onsetPromptProjectionSha256,omitempty"`
	OnsetSourceSelectionManifestSHA256 string                             `json:"onsetSourceSelectionManifestSha256,omitempty"`
	OnsetPromptSelectionManifestSHA256 string                             `json:"onsetPromptSelectionManifestSha256,omitempty"`
	OnsetSelectionManifestSHA256       string                             `json:"onsetSelectionManifestSha256,omitempty"`
	OnsetSelectionReason               string                             `json:"onsetSelectionReason,omitempty"`
	OnsetSelectionManifest             []WorkPatternProfileSelectionEntry `json:"onsetSelectionManifest,omitempty"`
	SourceContextSHA256                string                             `json:"sourceContextSha256"`
	PromptProjectionSHA256             string                             `json:"promptProjectionSha256"`
	SourceSelectionManifestSHA256      string                             `json:"sourceSelectionManifestSha256,omitempty"`
	ShadowContextSelectionSHA256       string                             `json:"shadowContextSelectionSha256,omitempty"`
	SelectionManifestSHA256            string                             `json:"selectionManifestSha256"`
	SelectionReason                    string                             `json:"selectionReason"`
	SelectionManifest                  []WorkPatternProfileSelectionEntry `json:"selectionManifest"`
}

func validatedWorkPatternProfileContextPayload(
	profile WorkPatternProfileContext,
) (workPatternProfileContextPayload, error) {
	if strings.TrimSpace(profile.SnapshotID) == "" ||
		len(strings.TrimSpace(profile.SnapshotSHA256)) != 64 ||
		profile.SourceCutoffAt <= 0 ||
		strings.TrimSpace(profile.CanonicalJSON) == "" {
		return workPatternProfileContextPayload{}, errors.New(
			"frozen work pattern profile is incomplete",
		)
	}
	var payload workPatternProfileContextPayload
	if err := json.Unmarshal([]byte(profile.CanonicalJSON), &payload); err != nil {
		return workPatternProfileContextPayload{}, fmt.Errorf(
			"decode frozen work pattern profile: %w",
			err,
		)
	}
	if payload.SnapshotID != profile.SnapshotID ||
		payload.SnapshotSHA256 != profile.SnapshotSHA256 ||
		payload.PolicyVersion != profile.PolicyVersion ||
		payload.SourceCutoffAt != profile.SourceCutoffAt ||
		normalizeWorkPatternInfluenceMode(payload.InfluenceMode) !=
			normalizeWorkPatternInfluenceMode(profile.InfluenceMode) {
		return workPatternProfileContextPayload{}, errors.New(
			"frozen work pattern profile metadata does not match its payload",
		)
	}
	return payload, nil
}

func workPatternProfileSelectionManifestSHA256(
	manifest []WorkPatternProfileSelectionEntry,
) (string, error) {
	payload, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf(
			"encode work pattern profile selection manifest: %w",
			err,
		)
	}
	return taskContextHash(string(payload)), nil
}

func workPatternProfileSelectionEntryKey(
	entry WorkPatternProfileSelectionEntry,
) string {
	kind := strings.TrimSpace(entry.Kind)
	ref := strings.TrimSpace(entry.Ref)
	if kind == "" || ref == "" {
		return ""
	}
	return kind + "\x00" + ref
}

func workPatternProfileSourceSelectionManifest(
	manifest []WorkPatternProfileSelectionEntry,
) []WorkPatternProfileSelectionEntry {
	result := make([]WorkPatternProfileSelectionEntry, 0, len(manifest))
	for _, entry := range manifest {
		entry.OnsetPromptIncluded = nil
		entry.OnsetPromptReason = ""
		entry.ShadowContextIncluded = nil
		entry.ShadowContextReason = ""
		result = append(result, entry)
	}
	return result
}

func workPatternProfileStageSelectionManifest(
	source, compact []WorkPatternProfileSelectionEntry,
	stage string,
) ([]WorkPatternProfileSelectionEntry, error) {
	packed := make(map[string]struct{}, len(compact))
	for _, entry := range compact {
		key := workPatternProfileSelectionEntryKey(entry)
		if key == "" {
			return nil, errors.New("compact work pattern selection has an empty key")
		}
		if _, exists := packed[key]; exists {
			return nil, fmt.Errorf(
				"compact work pattern selection repeats %q",
				key,
			)
		}
		packed[key] = struct{}{}
	}
	result := make([]WorkPatternProfileSelectionEntry, 0, len(source))
	seen := make(map[string]struct{}, len(source))
	for _, entry := range source {
		key := workPatternProfileSelectionEntryKey(entry)
		if key == "" {
			return nil, errors.New("source work pattern selection has an empty key")
		}
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf(
				"source work pattern selection repeats %q",
				key,
			)
		}
		seen[key] = struct{}{}
		_, included := packed[key]
		value := included
		entry.OnsetPromptIncluded = nil
		entry.OnsetPromptReason = ""
		entry.ShadowContextIncluded = nil
		entry.ShadowContextReason = ""
		switch stage {
		case "onset_prompt":
			entry.OnsetPromptIncluded = &value
			if included {
				entry.OnsetPromptReason = "selected_for_onset_prompt"
			} else if !entry.Included {
				entry.OnsetPromptReason = "not_admitted_to_onset_profile"
			} else {
				entry.OnsetPromptReason = "omitted_from_onset_prompt_budget"
			}
		case "onset_prompt_absent":
			value = false
			entry.OnsetPromptIncluded = &value
			entry.OnsetPromptReason = "onset_profile_not_supplied"
		case "shadow_context":
			entry.ShadowContextIncluded = &value
			if included {
				entry.ShadowContextReason = "selected_for_shadow_context"
			} else if !entry.Included {
				entry.ShadowContextReason = "not_admitted_to_shadow_profile"
			} else {
				entry.ShadowContextReason = "omitted_from_shadow_context_budget"
			}
		default:
			return nil, fmt.Errorf("unsupported work pattern selection stage %q", stage)
		}
		result = append(result, entry)
	}
	return result, nil
}

func summarizeWorkPatternProfileSelection(
	payload workPatternProfileContextPayload,
) WorkPatternProfilePromptSelectionSummary {
	result := WorkPatternProfilePromptSelectionSummary{
		ManifestEntryCount: len(payload.SelectionManifest),
		PromptCandidateCount: len(payload.SelectedProjections) +
			len(payload.SemanticSignals) +
			len(payload.CapabilitySignals) +
			len(payload.Contradictions),
	}
	for _, entry := range payload.SelectionManifest {
		if entry.Included {
			result.SelectedEntryCount++
		} else {
			result.OmittedEntryCount++
		}
		if entry.Kind == "contradiction" ||
			entry.Reason == "explicitly_contradicted" {
			result.ContradictionCount++
		}
		if entry.Truncated {
			result.TruncatedEntryCount++
		}
	}
	result.PromptOmittedCount = result.PromptCandidateCount
	return result
}

func compactWorkPatternProjection(
	value WorkPatternProjection,
) WorkPatternProjection {
	value.Apps = append([]string(nil), value.Apps...)
	value.Metrics = append([]WorkPatternMetric(nil), value.Metrics...)
	value.IntervalRefs = nil
	value.SessionRefs = nil
	return value
}

func workPatternProfilePromptManifestEntry(
	manifest []WorkPatternProfileSelectionEntry,
	ref, kind, evidenceClass, reason string,
	included bool,
) WorkPatternProfileSelectionEntry {
	for _, entry := range manifest {
		if entry.Ref == ref {
			result := entry
			result.Learning = nil
			result.OnsetPromptIncluded = nil
			result.OnsetPromptReason = ""
			result.ShadowContextIncluded = nil
			result.ShadowContextReason = ""
			return result
		}
	}
	return WorkPatternProfileSelectionEntry{
		Ref:           ref,
		Kind:          kind,
		EvidenceClass: evidenceClass,
		Included:      included,
		Reason:        reason,
	}
}

func appendWorkPatternProfilePromptManifest(
	current []WorkPatternProfileSelectionEntry,
	entry WorkPatternProfileSelectionEntry,
) []WorkPatternProfileSelectionEntry {
	for _, existing := range current {
		if existing.Ref == entry.Ref && existing.Kind == entry.Kind {
			return current
		}
	}
	return append(
		append([]WorkPatternProfileSelectionEntry(nil), current...),
		entry,
	)
}

func encodedWorkPatternProfilePromptBytes(
	value WorkPatternProfileContext,
) ([]byte, error) {
	value.CanonicalJSON = ""
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf(
			"encode compact work pattern profile projection: %w",
			err,
		)
	}
	return payload, nil
}

func validatePreparedWorkPatternProfilePrompt(
	value WorkPatternProfileContext,
	maxBytes int,
) error {
	if value.PromptProjectionVersion !=
		workPatternProfilePromptProjectionVersion ||
		strings.TrimSpace(value.SnapshotID) == "" ||
		len(strings.TrimSpace(value.SnapshotSHA256)) != 64 ||
		strings.TrimSpace(value.PolicyVersion) == "" ||
		value.SourceCutoffAt <= 0 ||
		len(value.SourceContextSHA256) != 64 ||
		len(value.SelectionManifestSHA256) != 64 ||
		value.SelectionSummary == nil ||
		strings.TrimSpace(value.CanonicalJSON) != "" {
		return errors.New("work pattern prompt projection is incomplete")
	}
	if normalizeWorkPatternInfluenceMode(value.InfluenceMode) ==
		WorkPatternInfluenceNeutral &&
		(len(value.SelectedProjections) > 0 ||
			len(value.SemanticSignals) > 0 ||
			len(value.CapabilitySignals) > 0 ||
			len(value.Contradictions) > 0) {
		return errors.New(
			"neutral work pattern prompt projection contains influence material",
		)
	}
	for _, projection := range value.SelectedProjections {
		if len(projection.IntervalRefs) > 0 || len(projection.SessionRefs) > 0 {
			return errors.New(
				"work pattern prompt projection contains raw interval or session refs",
			)
		}
	}
	payload, err := encodedWorkPatternProfilePromptBytes(value)
	if err != nil {
		return err
	}
	if len(payload) > maxBytes {
		return fmt.Errorf(
			"work pattern prompt projection is %d bytes, exceeds %d-byte budget",
			len(payload),
			maxBytes,
		)
	}
	return nil
}

func compileWorkPatternProfilePrompt(
	profile WorkPatternProfileContext,
	maxBytes int,
) (WorkPatternProfileContext, error) {
	if maxBytes < workPatternProfilePromptMinimumBytes {
		return WorkPatternProfileContext{}, fmt.Errorf(
			"work pattern prompt budget %d is below minimum %d",
			maxBytes,
			workPatternProfilePromptMinimumBytes,
		)
	}
	if profile.PromptProjectionVersion != "" {
		if err := validatePreparedWorkPatternProfilePrompt(
			profile,
			maxBytes,
		); err != nil {
			return WorkPatternProfileContext{}, err
		}
		return profile, nil
	}

	source, err := validatedWorkPatternProfileContextPayload(profile)
	if err != nil {
		return WorkPatternProfileContext{}, err
	}
	manifestHash, err := workPatternProfileSelectionManifestSHA256(
		source.SelectionManifest,
	)
	if err != nil {
		return WorkPatternProfileContext{}, err
	}
	summary := summarizeWorkPatternProfileSelection(source)
	result := WorkPatternProfileContext{
		SnapshotID:              source.SnapshotID,
		SnapshotSHA256:          source.SnapshotSHA256,
		PolicyVersion:           source.PolicyVersion,
		SourceCutoffAt:          source.SourceCutoffAt,
		Status:                  source.Status,
		Confidence:              source.Confidence,
		InfluenceMode:           normalizeWorkPatternInfluenceMode(source.InfluenceMode),
		PromptProjectionVersion: workPatternProfilePromptProjectionVersion,
		SourceContextSHA256:     taskContextHash(profile.CanonicalJSON),
		SelectionManifestSHA256: manifestHash,
		SelectionSummary:        &summary,
		SelectedProjections:     []WorkPatternProjection{},
		SemanticSignals:         []WorkPatternSemanticSignal{},
		CapabilitySignals:       []WorkPatternCapabilitySignal{},
		Contradictions:          []WorkPatternProfileCorrection{},
		SelectionManifest:       []WorkPatternProfileSelectionEntry{},
		SelectionReason:         source.SelectionReason,
	}

	if err := validatePreparedWorkPatternProfilePrompt(result, maxBytes); err != nil {
		return WorkPatternProfileContext{}, err
	}
	if result.InfluenceMode == WorkPatternInfluenceNeutral {
		return result, nil
	}

	tryAssign := func(
		trial WorkPatternProfileContext,
		nextSummary WorkPatternProfilePromptSelectionSummary,
	) bool {
		trial.SelectionSummary = &nextSummary
		if err := validatePreparedWorkPatternProfilePrompt(
			trial,
			maxBytes,
		); err != nil {
			return false
		}
		result = trial
		summary = nextSummary
		return true
	}
	nextSummary := func() WorkPatternProfilePromptSelectionSummary {
		next := summary
		next.PromptIncludedCount++
		next.PromptOmittedCount = max(next.PromptOmittedCount-1, 0)
		return next
	}

	// Explicit corrections are the strongest profile evidence and are packed
	// before observational priors.
	for _, correction := range source.Contradictions {
		trial := result
		trial.Contradictions = append(
			append([]WorkPatternProfileCorrection(nil), result.Contradictions...),
			correction,
		)
		trial.SelectionManifest = appendWorkPatternProfilePromptManifest(
			result.SelectionManifest,
			workPatternProfilePromptManifestEntry(
				source.SelectionManifest,
				"contradiction:"+correction.ID,
				"contradiction",
				TaskContextEvidenceExplicitUser,
				"explicitly_contradicted",
				false,
			),
		)
		tryAssign(trial, nextSummary())
	}

	for _, projection := range source.SelectedProjections {
		trial := result
		trial.SelectedProjections = append(
			append([]WorkPatternProjection(nil), result.SelectedProjections...),
			compactWorkPatternProjection(projection),
		)
		trial.SelectionManifest = appendWorkPatternProfilePromptManifest(
			result.SelectionManifest,
			workPatternProfilePromptManifestEntry(
				source.SelectionManifest,
				"projection:"+projection.ScopeKind+":"+projection.ScopeKey,
				"projection",
				projection.EvidenceKind,
				"selected_for_current_context",
				true,
			),
		)
		tryAssign(trial, nextSummary())
	}

	for _, signal := range source.SemanticSignals {
		candidate := signal
		candidate.Learning = nil
		for {
			trial := result
			trial.SemanticSignals = append(
				append([]WorkPatternSemanticSignal(nil), result.SemanticSignals...),
				candidate,
			)
			entry := workPatternProfilePromptManifestEntry(
				source.SelectionManifest,
				"semantic:"+signal.Ref,
				"semantic",
				signal.EvidenceClass,
				"selected_for_current_context",
				true,
			)
			entry.Truncated = candidate.Truncated
			if candidate.Truncated {
				entry.Reason = "selected_and_truncated_to_prompt_budget"
			}
			trial.SelectionManifest = appendWorkPatternProfilePromptManifest(
				result.SelectionManifest,
				entry,
			)
			if tryAssign(trial, nextSummary()) {
				break
			}
			if len(candidate.Content) <= taskContextMinimumFragment {
				break
			}
			nextLength := len(candidate.Content) -
				max(len(candidate.Content)/4, taskContextMinimumFragment)
			candidate.Content = truncateTaskContext(
				candidate.Content,
				nextLength,
			)
			candidate.Truncated = true
		}
	}

	for _, signal := range source.CapabilitySignals {
		trial := result
		signal.Learning = nil
		trial.CapabilitySignals = append(
			append([]WorkPatternCapabilitySignal(nil), result.CapabilitySignals...),
			signal,
		)
		trial.SelectionManifest = appendWorkPatternProfilePromptManifest(
			result.SelectionManifest,
			workPatternProfilePromptManifestEntry(
				source.SelectionManifest,
				"capability:"+signal.ProfileID,
				"capability",
				WorkPatternEvidenceEvaluated,
				"selected_for_current_context",
				true,
			),
		)
		tryAssign(trial, nextSummary())
	}

	sort.SliceStable(result.SelectionManifest, func(i, j int) bool {
		if result.SelectionManifest[i].Kind !=
			result.SelectionManifest[j].Kind {
			return result.SelectionManifest[i].Kind <
				result.SelectionManifest[j].Kind
		}
		return result.SelectionManifest[i].Ref <
			result.SelectionManifest[j].Ref
	})
	result.SelectionSummary = &summary
	if err := validatePreparedWorkPatternProfilePrompt(
		result,
		maxBytes,
	); err != nil {
		return WorkPatternProfileContext{}, err
	}
	return result, nil
}

func prepareTaskOnsetPromptInput(
	input TaskOnsetInput,
) (TaskOnsetInput, error) {
	normalized, err := normalizeTaskOnsetInput(input)
	if err != nil {
		return TaskOnsetInput{}, err
	}
	if normalized.WorkProfile == nil {
		return normalized, nil
	}

	sourceProfile := *normalized.WorkProfile
	withoutProfile := normalized
	withoutProfile.WorkProfile = nil
	basePayload, err := json.Marshal(withoutProfile)
	if err != nil {
		return TaskOnsetInput{}, fmt.Errorf(
			"marshal task onset input without work profile: %w",
			err,
		)
	}
	available := taskOnsetContextBudget - len(basePayload) - 512
	if available > workPatternProfilePromptMaximumBytes {
		available = workPatternProfilePromptMaximumBytes
	}
	if available < workPatternProfilePromptMinimumBytes {
		return TaskOnsetInput{}, fmt.Errorf(
			"task onset context leaves %d bytes for the work profile, below %d-byte minimum",
			available,
			workPatternProfilePromptMinimumBytes,
		)
	}

	for available >= workPatternProfilePromptMinimumBytes {
		compact, err := compileWorkPatternProfilePrompt(
			sourceProfile,
			available,
		)
		if err != nil {
			return TaskOnsetInput{}, err
		}
		normalized.WorkProfile = &compact
		payload, err := json.Marshal(normalized)
		if err != nil {
			return TaskOnsetInput{}, fmt.Errorf(
				"marshal task onset prompt input: %w",
				err,
			)
		}
		influenceRefs, err := json.Marshal(
			workPatternAllowedInfluenceRefs(normalized.WorkProfile),
		)
		if err != nil {
			return TaskOnsetInput{}, fmt.Errorf(
				"marshal task onset profile influence refs: %w",
				err,
			)
		}
		total := len(payload) + len(influenceRefs)
		if total <= taskOnsetContextBudget {
			return normalized, nil
		}
		if sourceProfile.PromptProjectionVersion != "" {
			break
		}
		available -= total - taskOnsetContextBudget + 128
	}
	return TaskOnsetInput{}, fmt.Errorf(
		"task onset work profile cannot fit the %d-byte context budget",
		taskOnsetContextBudget,
	)
}

func buildWorkPatternProfileSelectionEvidence(
	onsetProfile WorkPatternProfileContext,
	onsetPromptProjection WorkPatternProfileContext,
	shadowProfile WorkPatternProfileContext,
	shadowContextProjection string,
	episodeID string,
	createdAt int64,
) (TaskEpisodeEvidence, error) {
	shadowSource, err := validatedWorkPatternProfileContextPayload(shadowProfile)
	if err != nil {
		return TaskEpisodeEvidence{}, err
	}
	onsetProfilePresent := strings.TrimSpace(onsetProfile.SnapshotID) != ""
	var onsetSource workPatternProfileContextPayload
	if onsetProfilePresent {
		onsetSource, err = validatedWorkPatternProfileContextPayload(onsetProfile)
		if err != nil {
			return TaskEpisodeEvidence{}, err
		}
		if onsetSource.SnapshotID != shadowSource.SnapshotID ||
			onsetSource.SnapshotSHA256 != shadowSource.SnapshotSHA256 ||
			onsetSource.PolicyVersion != shadowSource.PolicyVersion ||
			onsetSource.SourceCutoffAt != shadowSource.SourceCutoffAt {
			return TaskEpisodeEvidence{}, errors.New(
				"onset and Shadow work pattern profiles use different immutable snapshots",
			)
		}
		if err := validatePreparedWorkPatternProfilePrompt(
			onsetPromptProjection,
			workPatternProfilePromptMaximumBytes,
		); err != nil {
			return TaskEpisodeEvidence{}, fmt.Errorf(
				"validate exact onset work pattern projection: %w",
				err,
			)
		}
	} else if strings.TrimSpace(onsetPromptProjection.SnapshotID) != "" {
		return TaskEpisodeEvidence{}, errors.New(
			"absent onset profile cannot have a prompt projection",
		)
	}
	var shadowCompact WorkPatternProfileContext
	if err := json.Unmarshal(
		[]byte(shadowContextProjection),
		&shadowCompact,
	); err != nil {
		return TaskEpisodeEvidence{}, fmt.Errorf(
			"decode compact Shadow work pattern projection: %w",
			err,
		)
	}
	if err := validatePreparedWorkPatternProfilePrompt(
		shadowCompact,
		workPatternProfilePromptMaximumBytes,
	); err != nil {
		return TaskEpisodeEvidence{}, fmt.Errorf(
			"validate exact Shadow work pattern projection: %w",
			err,
		)
	}
	onsetSourceManifest := shadowSource.SelectionManifest
	if onsetProfilePresent {
		onsetSourceManifest = onsetSource.SelectionManifest
	}
	onsetSourceManifestHash, err := workPatternProfileSelectionManifestSHA256(
		onsetSourceManifest,
	)
	if err != nil {
		return TaskEpisodeEvidence{}, err
	}
	shadowSourceManifestHash, err := workPatternProfileSelectionManifestSHA256(
		shadowSource.SelectionManifest,
	)
	if err != nil {
		return TaskEpisodeEvidence{}, err
	}
	onsetSourceHash := ""
	if onsetProfilePresent {
		onsetSourceHash = taskContextHash(onsetProfile.CanonicalJSON)
	}
	shadowSourceHash := taskContextHash(shadowProfile.CanonicalJSON)
	if shadowCompact.SnapshotID != shadowSource.SnapshotID ||
		shadowCompact.SnapshotSHA256 != shadowSource.SnapshotSHA256 ||
		shadowCompact.SourceContextSHA256 != shadowSourceHash ||
		shadowCompact.SelectionManifestSHA256 != shadowSourceManifestHash {
		return TaskEpisodeEvidence{}, errors.New(
			"compact work pattern projection does not match its exact source context",
		)
	}
	if onsetProfilePresent &&
		(onsetPromptProjection.SnapshotID != onsetSource.SnapshotID ||
			onsetPromptProjection.SnapshotSHA256 != onsetSource.SnapshotSHA256 ||
			onsetPromptProjection.SourceContextSHA256 != onsetSourceHash ||
			onsetPromptProjection.SelectionManifestSHA256 != onsetSourceManifestHash) {
		return TaskEpisodeEvidence{}, errors.New(
			"compact onset work pattern projection does not match its exact source context",
		)
	}
	onsetStage := "onset_prompt_absent"
	onsetCompactManifest := []WorkPatternProfileSelectionEntry{}
	if onsetProfilePresent {
		onsetStage = "onset_prompt"
		onsetCompactManifest = onsetPromptProjection.SelectionManifest
	}
	onsetManifest, err := workPatternProfileStageSelectionManifest(
		onsetSourceManifest,
		onsetCompactManifest,
		onsetStage,
	)
	if err != nil {
		return TaskEpisodeEvidence{}, err
	}
	shadowManifest, err := workPatternProfileStageSelectionManifest(
		shadowSource.SelectionManifest,
		shadowCompact.SelectionManifest,
		"shadow_context",
	)
	if err != nil {
		return TaskEpisodeEvidence{}, err
	}
	onsetPromptHash := ""
	if onsetProfilePresent {
		onsetPromptBytes, err := encodedWorkPatternProfilePromptBytes(
			onsetPromptProjection,
		)
		if err != nil {
			return TaskEpisodeEvidence{}, err
		}
		onsetPromptHash = taskContextHash(string(onsetPromptBytes))
	}
	onsetPromptSelectionHash, err := workPatternProfileSelectionManifestSHA256(
		onsetCompactManifest,
	)
	if err != nil {
		return TaskEpisodeEvidence{}, err
	}
	shadowContextSelectionHash, err := workPatternProfileSelectionManifestSHA256(
		shadowCompact.SelectionManifest,
	)
	if err != nil {
		return TaskEpisodeEvidence{}, err
	}
	onsetManifestHash, err := workPatternProfileSelectionManifestSHA256(onsetManifest)
	if err != nil {
		return TaskEpisodeEvidence{}, err
	}
	shadowManifestHash, err := workPatternProfileSelectionManifestSHA256(shadowManifest)
	if err != nil {
		return TaskEpisodeEvidence{}, err
	}
	provenance := workPatternProfileSelectionProvenance{
		Version:                            workPatternProfileSelectionEvidenceVersion,
		SnapshotID:                         shadowSource.SnapshotID,
		SnapshotSHA256:                     shadowSource.SnapshotSHA256,
		PolicyVersion:                      shadowSource.PolicyVersion,
		SourceCutoffAt:                     shadowSource.SourceCutoffAt,
		OnsetProfilePresent:                onsetProfilePresent,
		OnsetSourceContextSHA256:           onsetSourceHash,
		OnsetPromptProjectionSHA256:        onsetPromptHash,
		OnsetSourceSelectionManifestSHA256: onsetSourceManifestHash,
		OnsetPromptSelectionManifestSHA256: onsetPromptSelectionHash,
		OnsetSelectionManifestSHA256:       onsetManifestHash,
		OnsetSelectionReason:               onsetSource.SelectionReason,
		OnsetSelectionManifest:             onsetManifest,
		SourceContextSHA256:                shadowSourceHash,
		PromptProjectionSHA256:             taskContextHash(shadowContextProjection),
		SourceSelectionManifestSHA256:      shadowSourceManifestHash,
		ShadowContextSelectionSHA256:       shadowContextSelectionHash,
		SelectionManifestSHA256:            shadowManifestHash,
		SelectionReason:                    shadowSource.SelectionReason,
		SelectionManifest:                  shadowManifest,
	}
	payload, err := json.Marshal(provenance)
	if err != nil {
		return TaskEpisodeEvidence{}, fmt.Errorf(
			"encode work pattern selection provenance: %w",
			err,
		)
	}
	return normalizeTaskEpisodeEvidence(TaskEpisodeEvidence{
		TaskEpisodeID: episodeID,
		Role:          TaskEpisodeEvidenceContext,
		SourceKind:    workPatternProfileSelectionEvidenceSource,
		SourceID:      shadowSource.SnapshotID,
		Summary:       "Frozen work-profile selection provenance.",
		PayloadJSON:   string(payload),
		ObservedAt:    shadowSource.SourceCutoffAt,
		CreatedAt:     createdAt,
	}, episodeID, createdAt)
}

func decodeWorkPatternProfileSelectionEvidence(
	evidence TaskEpisodeEvidence,
) (workPatternProfileSelectionProvenance, bool) {
	if evidence.Role != TaskEpisodeEvidenceContext ||
		evidence.SourceKind != workPatternProfileSelectionEvidenceSource {
		return workPatternProfileSelectionProvenance{}, false
	}
	var result workPatternProfileSelectionProvenance
	if err := json.Unmarshal([]byte(evidence.PayloadJSON), &result); err != nil {
		return workPatternProfileSelectionProvenance{}, false
	}
	if (result.Version != workPatternProfileSelectionEvidenceVersion &&
		result.Version != workPatternProfileSelectionEvidenceV2Version &&
		result.Version != workPatternProfileSelectionEvidenceLegacyVersion) ||
		result.SnapshotID == "" ||
		len(result.SnapshotSHA256) != 64 ||
		len(result.SourceContextSHA256) != 64 ||
		len(result.PromptProjectionSHA256) != 64 ||
		len(result.SelectionManifestSHA256) != 64 {
		return workPatternProfileSelectionProvenance{}, false
	}
	manifestHash, err := workPatternProfileSelectionManifestSHA256(
		result.SelectionManifest,
	)
	if err != nil || manifestHash != result.SelectionManifestSHA256 {
		return workPatternProfileSelectionProvenance{}, false
	}
	if result.Version != workPatternProfileSelectionEvidenceVersion {
		return result, true
	}
	if len(result.OnsetSourceSelectionManifestSHA256) != 64 ||
		len(result.OnsetPromptSelectionManifestSHA256) != 64 ||
		len(result.OnsetSelectionManifestSHA256) != 64 ||
		len(result.SourceSelectionManifestSHA256) != 64 ||
		len(result.ShadowContextSelectionSHA256) != 64 {
		return workPatternProfileSelectionProvenance{}, false
	}
	if result.OnsetProfilePresent {
		if len(result.OnsetSourceContextSHA256) != 64 ||
			len(result.OnsetPromptProjectionSHA256) != 64 {
			return workPatternProfileSelectionProvenance{}, false
		}
	} else if result.OnsetSourceContextSHA256 != "" ||
		result.OnsetPromptProjectionSHA256 != "" {
		return workPatternProfileSelectionProvenance{}, false
	}
	onsetManifestHash, err := workPatternProfileSelectionManifestSHA256(
		result.OnsetSelectionManifest,
	)
	if err != nil || onsetManifestHash != result.OnsetSelectionManifestSHA256 {
		return workPatternProfileSelectionProvenance{}, false
	}
	for _, entry := range result.OnsetSelectionManifest {
		if entry.OnsetPromptIncluded == nil {
			return workPatternProfileSelectionProvenance{}, false
		}
	}
	for _, entry := range result.SelectionManifest {
		if entry.ShadowContextIncluded == nil {
			return workPatternProfileSelectionProvenance{}, false
		}
	}
	onsetSourceHash, err := workPatternProfileSelectionManifestSHA256(
		workPatternProfileSourceSelectionManifest(result.OnsetSelectionManifest),
	)
	if err != nil || onsetSourceHash != result.OnsetSourceSelectionManifestSHA256 {
		return workPatternProfileSelectionProvenance{}, false
	}
	shadowSourceHash, err := workPatternProfileSelectionManifestSHA256(
		workPatternProfileSourceSelectionManifest(result.SelectionManifest),
	)
	if err != nil || shadowSourceHash != result.SourceSelectionManifestSHA256 {
		return workPatternProfileSelectionProvenance{}, false
	}
	return result, true
}
