package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"yansu-agent/agent"
)

const (
	taskOnsetPromptVersion = "task-onset-v5"
	taskOnsetMaxEvidence   = 200
	taskOnsetMaxEpisodes   = 3
	taskOnsetMaxAlternates = 3
	taskOnsetMaxReasoning  = 3
	taskOnsetContextBudget = 48_000

	TaskReasoningBasisExplicitStatement   = "explicit_statement"
	TaskReasoningBasisRepeatedPattern     = "repeated_pattern"
	TaskReasoningBasisContextualInference = "contextual_inference"
)

// TaskOnsetEvidence is a redacted, timestamped observation available inside a
// bounded onset window. Later user behavior must never be placed in this
// structure and is rejected when its timestamp exceeds CutoffAt.
type TaskOnsetEvidence struct {
	Ref        string `json:"ref"`
	Kind       string `json:"kind"`
	SourceKind string `json:"sourceKind"`
	SourceID   string `json:"sourceId"`
	OccurredAt int64  `json:"occurredAt"`
	AppKey     string `json:"appKey,omitempty"`
	AppName    string `json:"appName,omitempty"`
	Summary    string `json:"summary"`
}

type TaskOnsetInput struct {
	SourceKind   string                     `json:"sourceKind"`
	SourceID     string                     `json:"sourceId"`
	WindowStart  int64                      `json:"windowStart"`
	CutoffAt     int64                      `json:"cutoffAt"`
	Evidence     []TaskOnsetEvidence        `json:"evidence"`
	PrivacyNotes []string                   `json:"privacyNotes,omitempty"`
	WorkProfile  *WorkPatternProfileContext `json:"workProfile,omitempty"`
}

type TaskOnsetAlternative struct {
	Task              string   `json:"task"`
	DesiredResult     string   `json:"desiredResult"`
	Confidence        float64  `json:"confidence"`
	EvidenceRefs      []string `json:"evidenceRefs"`
	ContradictionRefs []string `json:"contradictionRefs,omitempty"`
}

// TaskOnsetReasoningHypothesis is prediction-time model output. It is not an
// observed fact about why the user acted. Explicit statements are accepted as
// such only after deterministic literal-source validation.
type TaskOnsetReasoningHypothesis struct {
	Statement         string   `json:"statement"`
	Confidence        float64  `json:"confidence"`
	Basis             string   `json:"basis"`
	SourceQuote       string   `json:"sourceQuote,omitempty"`
	EvidenceRefs      []string `json:"evidenceRefs,omitempty"`
	ProfileRefs       []string `json:"profileRefs,omitempty"`
	ContradictionRefs []string `json:"contradictionRefs,omitempty"`
}

type TaskOnsetPrediction struct {
	LocalKey             string                         `json:"localKey"`
	Task                 string                         `json:"task"`
	DesiredResult        string                         `json:"desiredResult"`
	InferredStartedAt    int64                          `json:"inferredStartedAt"`
	TaskConfidence       float64                        `json:"taskConfidence"`
	BoundaryConfidence   float64                        `json:"boundaryConfidence"`
	ResultConfidence     float64                        `json:"resultConfidence"`
	EvidenceRefs         []string                       `json:"evidenceRefs"`
	ProfileInfluenceRefs []string                       `json:"profileInfluenceRefs,omitempty"`
	ProfileConflict      bool                           `json:"profileConflict,omitempty"`
	Alternatives         []TaskOnsetAlternative         `json:"alternatives,omitempty"`
	ReasoningHypotheses  []TaskOnsetReasoningHypothesis `json:"reasoningHypotheses,omitempty"`
}

type TaskOnsetResponse struct {
	Episodes []TaskOnsetPrediction `json:"episodes"`
}

func normalizeTaskOnsetInput(input TaskOnsetInput) (TaskOnsetInput, error) {
	input.SourceKind = strings.TrimSpace(input.SourceKind)
	input.SourceID = strings.TrimSpace(input.SourceID)
	if input.SourceKind == "" || input.SourceID == "" {
		return TaskOnsetInput{}, errors.New("task onset source kind and id are required")
	}
	if input.WindowStart <= 0 || input.CutoffAt <= 0 || input.CutoffAt < input.WindowStart {
		return TaskOnsetInput{}, errors.New("task onset window is invalid")
	}

	seen := make(map[string]struct{}, len(input.Evidence))
	normalized := make([]TaskOnsetEvidence, 0, min(len(input.Evidence), taskOnsetMaxEvidence))
	for _, item := range input.Evidence {
		item.Ref = strings.TrimSpace(item.Ref)
		item.Kind = strings.TrimSpace(item.Kind)
		item.SourceKind = strings.TrimSpace(item.SourceKind)
		item.SourceID = strings.TrimSpace(item.SourceID)
		item.AppKey = strings.ToLower(strings.TrimSpace(item.AppKey))
		item.AppName = strings.TrimSpace(item.AppName)
		item.Summary = strings.TrimSpace(item.Summary)
		if item.Ref == "" || item.Summary == "" || item.OccurredAt <= 0 {
			continue
		}
		if item.OccurredAt < input.WindowStart || item.OccurredAt > input.CutoffAt {
			return TaskOnsetInput{}, fmt.Errorf(
				"task onset evidence %q at %d is outside [%d,%d]",
				item.Ref, item.OccurredAt, input.WindowStart, input.CutoffAt,
			)
		}
		if _, exists := seen[item.Ref]; exists {
			continue
		}
		seen[item.Ref] = struct{}{}
		normalized = append(normalized, item)
		if len(normalized) == taskOnsetMaxEvidence {
			break
		}
	}
	sort.SliceStable(normalized, func(i, j int) bool {
		if normalized[i].OccurredAt != normalized[j].OccurredAt {
			return normalized[i].OccurredAt < normalized[j].OccurredAt
		}
		return normalized[i].Ref < normalized[j].Ref
	})
	input.Evidence = normalized

	privacy := make([]string, 0, len(input.PrivacyNotes))
	for _, note := range input.PrivacyNotes {
		if note = strings.TrimSpace(note); note != "" {
			privacy = append(privacy, note)
		}
	}
	input.PrivacyNotes = privacy
	if input.WorkProfile != nil {
		profile := *input.WorkProfile
		profile.SnapshotID = strings.TrimSpace(profile.SnapshotID)
		profile.SnapshotSHA256 = strings.ToLower(
			strings.TrimSpace(profile.SnapshotSHA256),
		)
		profile.PolicyVersion = strings.TrimSpace(profile.PolicyVersion)
		profile.InfluenceMode = normalizeWorkPatternInfluenceMode(
			profile.InfluenceMode,
		)
		if profile.SnapshotID == "" || len(profile.SnapshotSHA256) != 64 ||
			profile.PolicyVersion == "" || profile.SourceCutoffAt <= 0 {
			return TaskOnsetInput{}, errors.New(
				"task onset work profile is incomplete",
			)
		}
		if profile.SourceCutoffAt >= input.WindowStart {
			return TaskOnsetInput{}, errors.New(
				"task onset work profile must precede the observation window",
			)
		}
		input.WorkProfile = &profile
	}
	return input, nil
}

func buildTaskOnsetPrompt(input TaskOnsetInput) (string, TaskOnsetInput, error) {
	normalized, err := prepareTaskOnsetPromptInput(input)
	if err != nil {
		return "", TaskOnsetInput{}, err
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return "", TaskOnsetInput{}, fmt.Errorf("marshal task onset input: %w", err)
	}
	profileInfluenceRefs, err := json.Marshal(
		workPatternAllowedInfluenceRefs(normalized.WorkProfile),
	)
	if err != nil {
		return "", TaskOnsetInput{}, fmt.Errorf(
			"marshal task onset profile influence refs: %w",
			err,
		)
	}
	if len(payload)+len(profileInfluenceRefs) > taskOnsetContextBudget {
		return "", TaskOnsetInput{}, fmt.Errorf(
			"task onset context is %d bytes, exceeds %d-byte budget",
			len(payload)+len(profileInfluenceRefs),
			taskOnsetContextBudget,
		)
	}

	prompt := fmt.Sprintf(`You are Yansu's task-onset detector.

Your job is to identify whether the user appears to BEGIN one or more coherent
tasks inside the supplied observation window. This is not a recommendation and
not a prediction of what the user should do. Use only the timestamped evidence
below.

Hard rules:
1. The evidence ends at cutoffAt. Do not assume later actions or results.
2. A task is an outcome-oriented unit of work, not an app switch or isolated
   click.
3. Return an empty episodes array when the evidence is passive, ambiguous, or
   does not show a task beginning.
4. Multiple tasks may overlap. Return at most three distinct starts.
5. inferredStartedAt must fall between windowStart and cutoffAt.
6. Every evidenceRefs or contradictionRefs value must exactly match a supplied
   evidence ref.
7. desiredResult is a cautious hypothesis about the observable result. Do not
   smuggle later user behavior into it.
8. Alternatives preserve plausible interpretations rather than forcing false
   certainty.
9. Confidence is between 0 and 1. Lower confidence when intent or the boundary
   is only implicit.
10. workProfile, when present, is a historical prior rather than current-task
    evidence. Prefer the timestamped observation when they conflict.
11. A work pattern cannot turn an app switch into a task, prove hidden
    reasoning, or justify higher confidence without current evidence.
12. Profile confidence is not permission, capability readiness, or authority.
13. profileInfluenceRefs lists only profile fields that materially changed this
    prediction. Every value must exactly match an allowed profile influence ref
    below. Return [] when the prediction would be the same without the profile.
14. Set profileConflict true when current evidence conflicts with a cited
    profile field. In that case preserve alternatives and keep task, boundary,
    and result confidence at or below 0.6.
15. reasoningHypotheses contains zero to three competing explanations for why
    the user might choose this task or approach. These are predictions, not
    user facts. Return [] when no reason is supportable.
16. basis is explicit_statement, repeated_pattern, or contextual_inference.
    Contextual inference confidence must remain below 0.5. Repeated pattern
    requires exact profileRefs from the allowed list.
17. explicit_statement requires sourceQuote copied literally from user-entered
    text or user speech in a cited evidence ref. Never quote other-party or app
    text as the user's reason. Unsupported explicit claims are treated as
    low-confidence contextual inference by the backend.
18. A reasoning hypothesis cannot grant authority, prove a personal policy, or
    replace an independently observed result.

Return strict JSON only:
{
  "episodes": [{
    "localKey": "stable short key unique within this response",
    "task": "concise description of the work the user appears to begin",
    "desiredResult": "observable result the user appears to seek",
    "inferredStartedAt": 0,
    "taskConfidence": 0.0,
    "boundaryConfidence": 0.0,
    "resultConfidence": 0.0,
    "evidenceRefs": ["exact-ref"],
    "profileInfluenceRefs": ["exact-allowed-profile-ref"],
    "profileConflict": false,
    "alternatives": [{
      "task": "alternative interpretation",
      "desiredResult": "alternative observable result",
      "confidence": 0.0,
      "evidenceRefs": ["exact-ref"],
      "contradictionRefs": ["exact-ref"]
    }],
    "reasoningHypotheses": [{
      "statement": "one possible explanation",
      "confidence": 0.0,
      "basis": "contextual_inference",
      "sourceQuote": "",
      "evidenceRefs": ["exact-ref"],
      "profileRefs": ["exact-allowed-profile-ref"],
      "contradictionRefs": ["exact-ref"]
    }]
  }]
}

Prompt version: %s

Observation:
%s

Allowed profile influence refs:
%s`, taskOnsetPromptVersion, string(payload), string(profileInfluenceRefs))
	return prompt, normalized, nil
}

func workPatternAllowedInfluenceRefs(
	profile *WorkPatternProfileContext,
) []string {
	if profile == nil ||
		normalizeWorkPatternInfluenceMode(profile.InfluenceMode) !=
			WorkPatternInfluenceProfile {
		return []string{}
	}
	seen := make(map[string]struct{})
	for _, projection := range profile.SelectedProjections {
		for _, metric := range projection.Metrics {
			ref := strings.Join([]string{
				"metric",
				projection.ScopeKind,
				projection.ScopeKey,
				metric.Name,
			}, ":")
			seen[ref] = struct{}{}
		}
	}
	for _, signal := range profile.SemanticSignals {
		if ref := strings.TrimSpace(signal.Ref); ref != "" {
			seen["semantic:"+ref] = struct{}{}
		}
	}
	for _, signal := range profile.CapabilitySignals {
		if id := strings.TrimSpace(signal.ProfileID); id != "" {
			seen["capability:"+id] = struct{}{}
		}
	}
	for _, correction := range profile.Contradictions {
		if id := strings.TrimSpace(correction.ID); id != "" {
			seen["contradiction:"+id] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for ref := range seen {
		result = append(result, ref)
	}
	sort.Strings(result)
	return result
}

func canonicalTaskOnsetRefs(refs []string, allowed map[string]struct{}) []string {
	seen := make(map[string]struct{}, len(refs))
	result := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if _, ok := allowed[ref]; !ok {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		result = append(result, ref)
	}
	sort.Strings(result)
	return result
}

func normalizeTaskReasoningText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func taskOnsetReasoningHasExplicitCue(value string) bool {
	value = normalizeTaskReasoningText(value)
	for _, cue := range []string{
		"because",
		"so that",
		"in order to",
		"the reason",
		"this is why",
		"that's why",
		"so i can",
		"so we can",
		"my goal is",
		"our goal is",
	} {
		if strings.Contains(value, cue) {
			return true
		}
	}
	return false
}

func taskOnsetEvidenceIsUserAuthored(item TaskOnsetEvidence) bool {
	switch strings.ToLower(strings.TrimSpace(item.Kind)) {
	case "keyboard":
		return true
	case "speech":
		return strings.HasPrefix(strings.TrimSpace(item.Summary), "User speech ")
	default:
		return false
	}
}

func taskOnsetExplicitReasoningSupported(
	hypothesis TaskOnsetReasoningHypothesis,
	evidence map[string]TaskOnsetEvidence,
) bool {
	quote := normalizeTaskReasoningText(hypothesis.SourceQuote)
	if quote == "" || !taskOnsetReasoningHasExplicitCue(quote) {
		return false
	}
	for _, ref := range hypothesis.EvidenceRefs {
		item, ok := evidence[ref]
		if !ok || !taskOnsetEvidenceIsUserAuthored(item) {
			continue
		}
		if strings.Contains(normalizeTaskReasoningText(item.Summary), quote) {
			return true
		}
	}
	return false
}

func normalizeTaskOnsetReasoningHypotheses(
	input TaskOnsetInput,
	profileConflict bool,
	hypotheses []TaskOnsetReasoningHypothesis,
) []TaskOnsetReasoningHypothesis {
	allowedEvidence := make(map[string]struct{}, len(input.Evidence))
	evidenceByRef := make(map[string]TaskOnsetEvidence, len(input.Evidence))
	for _, item := range input.Evidence {
		allowedEvidence[item.Ref] = struct{}{}
		evidenceByRef[item.Ref] = item
	}
	allowedProfile := make(map[string]struct{})
	for _, ref := range workPatternAllowedInfluenceRefs(input.WorkProfile) {
		allowedProfile[ref] = struct{}{}
	}

	seen := make(map[string]struct{}, len(hypotheses))
	result := make([]TaskOnsetReasoningHypothesis, 0, min(
		len(hypotheses),
		taskOnsetMaxReasoning,
	))
	for _, hypothesis := range hypotheses {
		hypothesis.Statement = strings.TrimSpace(hypothesis.Statement)
		if hypothesis.Statement == "" {
			continue
		}
		key := normalizeTaskReasoningText(hypothesis.Statement)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		hypothesis.Confidence = clampConfidence(hypothesis.Confidence)
		hypothesis.Basis = strings.ToLower(strings.TrimSpace(hypothesis.Basis))
		hypothesis.SourceQuote = strings.TrimSpace(hypothesis.SourceQuote)
		hypothesis.EvidenceRefs = canonicalTaskOnsetRefs(
			hypothesis.EvidenceRefs,
			allowedEvidence,
		)
		hypothesis.ProfileRefs = canonicalTaskOnsetRefs(
			hypothesis.ProfileRefs,
			allowedProfile,
		)
		hypothesis.ContradictionRefs = canonicalTaskOnsetRefs(
			hypothesis.ContradictionRefs,
			allowedEvidence,
		)

		switch hypothesis.Basis {
		case TaskReasoningBasisExplicitStatement:
			if !taskOnsetExplicitReasoningSupported(hypothesis, evidenceByRef) {
				hypothesis.Basis = TaskReasoningBasisContextualInference
				hypothesis.SourceQuote = ""
				hypothesis.ProfileRefs = nil
				hypothesis.Confidence = min(hypothesis.Confidence, 0.49)
			}
		case TaskReasoningBasisRepeatedPattern:
			hypothesis.SourceQuote = ""
			if len(hypothesis.ProfileRefs) == 0 {
				hypothesis.Basis = TaskReasoningBasisContextualInference
				hypothesis.Confidence = min(hypothesis.Confidence, 0.49)
			}
		case TaskReasoningBasisContextualInference:
			hypothesis.SourceQuote = ""
			hypothesis.ProfileRefs = nil
			hypothesis.Confidence = min(hypothesis.Confidence, 0.49)
		default:
			continue
		}
		if profileConflict && hypothesis.Basis == TaskReasoningBasisRepeatedPattern {
			hypothesis.Confidence = min(hypothesis.Confidence, 0.49)
		}
		if len(hypothesis.EvidenceRefs) == 0 && len(hypothesis.ProfileRefs) == 0 {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, hypothesis)
		if len(result) == taskOnsetMaxReasoning {
			break
		}
	}
	return result
}

func normalizeTaskOnsetResponse(input TaskOnsetInput, response TaskOnsetResponse) TaskOnsetResponse {
	allowed := make(map[string]struct{}, len(input.Evidence))
	for _, evidence := range input.Evidence {
		allowed[evidence.Ref] = struct{}{}
	}
	allowedProfileRefs := make(map[string]struct{})
	for _, ref := range workPatternAllowedInfluenceRefs(input.WorkProfile) {
		allowedProfileRefs[ref] = struct{}{}
	}

	seenKeys := make(map[string]struct{}, len(response.Episodes))
	episodes := make([]TaskOnsetPrediction, 0, min(len(response.Episodes), taskOnsetMaxEpisodes))
	for _, episode := range response.Episodes {
		episode.LocalKey = strings.TrimSpace(episode.LocalKey)
		episode.Task = strings.TrimSpace(episode.Task)
		episode.DesiredResult = strings.TrimSpace(episode.DesiredResult)
		if episode.LocalKey == "" || episode.Task == "" || episode.DesiredResult == "" {
			continue
		}
		if _, exists := seenKeys[episode.LocalKey]; exists {
			continue
		}
		if episode.InferredStartedAt < input.WindowStart || episode.InferredStartedAt > input.CutoffAt {
			continue
		}
		episode.TaskConfidence = clampConfidence(episode.TaskConfidence)
		episode.BoundaryConfidence = clampConfidence(episode.BoundaryConfidence)
		episode.ResultConfidence = clampConfidence(episode.ResultConfidence)
		episode.EvidenceRefs = canonicalTaskOnsetRefs(episode.EvidenceRefs, allowed)
		episode.ProfileInfluenceRefs = canonicalTaskOnsetRefs(
			episode.ProfileInfluenceRefs,
			allowedProfileRefs,
		)
		if len(episode.ProfileInfluenceRefs) == 0 {
			episode.ProfileConflict = false
		}
		if episode.ProfileConflict {
			episode.TaskConfidence = min(episode.TaskConfidence, 0.6)
			episode.BoundaryConfidence = min(
				episode.BoundaryConfidence,
				0.6,
			)
			episode.ResultConfidence = min(episode.ResultConfidence, 0.6)
		}
		if len(episode.EvidenceRefs) == 0 {
			continue
		}

		alternatives := make([]TaskOnsetAlternative, 0, min(len(episode.Alternatives), taskOnsetMaxAlternates))
		for _, alternative := range episode.Alternatives {
			alternative.Task = strings.TrimSpace(alternative.Task)
			alternative.DesiredResult = strings.TrimSpace(alternative.DesiredResult)
			if alternative.Task == "" || alternative.DesiredResult == "" {
				continue
			}
			alternative.Confidence = clampConfidence(alternative.Confidence)
			alternative.EvidenceRefs = canonicalTaskOnsetRefs(alternative.EvidenceRefs, allowed)
			alternative.ContradictionRefs = canonicalTaskOnsetRefs(alternative.ContradictionRefs, allowed)
			if len(alternative.EvidenceRefs) == 0 {
				continue
			}
			alternatives = append(alternatives, alternative)
			if len(alternatives) == taskOnsetMaxAlternates {
				break
			}
		}
		episode.Alternatives = alternatives
		episode.ReasoningHypotheses = normalizeTaskOnsetReasoningHypotheses(
			input,
			episode.ProfileConflict,
			episode.ReasoningHypotheses,
		)
		for _, hypothesis := range episode.ReasoningHypotheses {
			episode.ProfileInfluenceRefs = append(
				episode.ProfileInfluenceRefs,
				hypothesis.ProfileRefs...,
			)
		}
		episode.ProfileInfluenceRefs = canonicalTaskOnsetRefs(
			episode.ProfileInfluenceRefs,
			allowedProfileRefs,
		)
		seenKeys[episode.LocalKey] = struct{}{}
		episodes = append(episodes, episode)
		if len(episodes) == taskOnsetMaxEpisodes {
			break
		}
	}
	response.Episodes = episodes
	return response
}

func runTaskOnsetDetection(
	ctx context.Context,
	runner agent.AgentRunner,
	model string,
	input TaskOnsetInput,
) (TaskOnsetResponse, string, string, error) {
	if runner == nil {
		return TaskOnsetResponse{}, "", "", errors.New("task onset runner is not initialized")
	}
	prompt, normalized, err := buildTaskOnsetPrompt(input)
	if err != nil {
		return TaskOnsetResponse{}, "", "", err
	}

	var result strings.Builder
	_, err = runner.ExecuteTask(ctx, &agent.TaskInput{
		Priority:          agent.PriorityBackground,
		AutoApproveTools:  false,
		Prompt:            prompt,
		Model:             model,
		PlanMode:          true,
		Ephemeral:         true,
		NoTools:           true,
		NoSystem:          true,
		SuppressTelemetry: true,
	}, func(event *agent.StreamEvent) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch event.Type {
		case "assistant":
			result.WriteString(event.Content)
		case "error":
			return errors.New(event.Error)
		}
		return nil
	})
	raw := strings.TrimSpace(result.String())
	if err != nil {
		return TaskOnsetResponse{}, raw, prompt, fmt.Errorf("execute task onset detection: %w", err)
	}

	var response TaskOnsetResponse
	if err := unmarshalJSONBestEffort(stripCodeFences(raw), &response); err != nil {
		return TaskOnsetResponse{}, raw, prompt, fmt.Errorf("parse task onset response: %w", err)
	}
	return normalizeTaskOnsetResponse(normalized, response), raw, prompt, nil
}
