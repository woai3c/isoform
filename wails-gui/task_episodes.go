package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	TaskEpisodeStatusCandidate = "candidate"
	TaskEpisodeStatusActive    = "active"
	TaskEpisodeStatusPaused    = "paused"
	TaskEpisodeStatusCompleted = "completed"
	TaskEpisodeStatusAbandoned = "abandoned"
	TaskEpisodeStatusMerged    = "merged"
	TaskEpisodeStatusSplit     = "split"
	TaskEpisodeStatusExpired   = "expired"

	TaskEpisodeEvidenceOnset         = "onset"
	TaskEpisodeEvidenceContext       = "context"
	TaskEpisodeEvidenceTask          = "task"
	TaskEpisodeEvidenceDesiredResult = "desired_result"
	TaskEpisodeEvidenceAction        = "action"
	TaskEpisodeEvidenceResult        = "result"
	TaskEpisodeEvidenceReasoning     = "reasoning"
	TaskEpisodeEvidenceContradiction = "contradiction"

	TaskEpisodeLinkParent     = "parent"
	TaskEpisodeLinkMergedInto = "merged_into"
	TaskEpisodeLinkSplitInto  = "split_into"

	taskEpisodeDefaultListLimit = 100
	taskEpisodeMaxListLimit     = 500
)

// TaskEpisodeFrozenContext is the exact production context available at the
// inferred task start. CutoffAt is required to equal the episode's inferred
// start, preventing later user actions or results from entering shadow input.
type TaskEpisodeFrozenContext struct {
	ID                    string                     `json:"id"`
	TaskEpisodeID         string                     `json:"taskEpisodeId"`
	CutoffAt              int64                      `json:"cutoffAt"`
	CanonicalJSON         string                     `json:"canonicalJson"`
	SHA256                string                     `json:"sha256"`
	Manifest              []TaskContextManifestEntry `json:"manifest"`
	WorkProfileSnapshotID string                     `json:"workProfileSnapshotId,omitempty"`
	WorkProfileSHA256     string                     `json:"workProfileSha256,omitempty"`
	CreatedAt             int64                      `json:"createdAt"`
}

type TaskEpisodeEvidence struct {
	ID            string `json:"id"`
	TaskEpisodeID string `json:"taskEpisodeId"`
	Role          string `json:"role"`
	SourceKind    string `json:"sourceKind"`
	SourceID      string `json:"sourceId"`
	Summary       string `json:"summary,omitempty"`
	PayloadJSON   string `json:"payloadJson,omitempty"`
	ContentHash   string `json:"contentHash"`
	ObservedAt    int64  `json:"observedAt"`
	CreatedAt     int64  `json:"createdAt"`
}

type TaskEpisodeHypothesisAlternative struct {
	Kind        string   `json:"kind"`
	Statement   string   `json:"statement"`
	Confidence  float64  `json:"confidence"`
	EvidenceIDs []string `json:"evidenceIds,omitempty"`
}

type TaskEpisodeReasoningHypothesis struct {
	Statement                string   `json:"statement"`
	Confidence               float64  `json:"confidence"`
	Basis                    string   `json:"basis"`
	SourceQuote              string   `json:"sourceQuote,omitempty"`
	EvidenceIDs              []string `json:"evidenceIds,omitempty"`
	ProfileRefs              []string `json:"profileRefs,omitempty"`
	ContradictionEvidenceIDs []string `json:"contradictionEvidenceIds,omitempty"`
}

type TaskEpisodeHypothesisRevision struct {
	ID                      string                             `json:"id"`
	TaskEpisodeID           string                             `json:"taskEpisodeId"`
	Revision                int                                `json:"revision"`
	TaskHypothesis          string                             `json:"taskHypothesis"`
	DesiredResultHypothesis string                             `json:"desiredResultHypothesis"`
	TaskConfidence          float64                            `json:"taskConfidence"`
	BoundaryConfidence      float64                            `json:"boundaryConfidence"`
	DesiredResultConfidence float64                            `json:"desiredResultConfidence"`
	ProfileInfluenceRefs    []string                           `json:"profileInfluenceRefs,omitempty"`
	ProfileConflict         bool                               `json:"profileConflict,omitempty"`
	Alternatives            []TaskEpisodeHypothesisAlternative `json:"alternatives"`
	ReasoningHypotheses     []TaskEpisodeReasoningHypothesis   `json:"reasoningHypotheses,omitempty"`
	EvidenceIDs             []string                           `json:"evidenceIds"`
	CreatedAt               int64                              `json:"createdAt"`
}

type TaskEpisodeTransition struct {
	ID            string `json:"id"`
	TaskEpisodeID string `json:"taskEpisodeId"`
	Sequence      int    `json:"sequence"`
	FromStatus    string `json:"fromStatus,omitempty"`
	ToStatus      string `json:"toStatus"`
	Reason        string `json:"reason,omitempty"`
	OccurredAt    int64  `json:"occurredAt"`
	CreatedAt     int64  `json:"createdAt"`
}

// TaskEpisodeLink directions are explicit:
//   - parent: source is a child and target is its parent
//   - merged_into: source was merged into target
//   - split_into: source was split into target
type TaskEpisodeLink struct {
	ID              string `json:"id"`
	SourceEpisodeID string `json:"sourceEpisodeId"`
	TargetEpisodeID string `json:"targetEpisodeId"`
	Relation        string `json:"relation"`
	CreatedAt       int64  `json:"createdAt"`
}

type CreateTaskEpisodeInput struct {
	ID                string                        `json:"id"`
	InferredStartAt   int64                         `json:"inferredStartAt"`
	DetectedAt        int64                         `json:"detectedAt"`
	InitialStatus     string                        `json:"initialStatus"`
	FrozenContext     TaskEpisodeFrozenContext      `json:"frozenContext"`
	InitialEvidence   []TaskEpisodeEvidence         `json:"initialEvidence"`
	InitialHypothesis TaskEpisodeHypothesisRevision `json:"initialHypothesis"`
	CreatedAt         int64                         `json:"createdAt"`
}

// TaskEpisode is an observed or provisional unit of user work. It is not an
// Initiative, an execution request, or a memory. CurrentStatus and
// LatestHypothesis are derived from immutable append-only records.
type TaskEpisode struct {
	ID                string                        `json:"id"`
	InferredStartAt   int64                         `json:"inferredStartAt"`
	DetectedAt        int64                         `json:"detectedAt"`
	CurrentStatus     string                        `json:"currentStatus"`
	CreatePayloadHash string                        `json:"createPayloadHash"`
	FrozenContext     TaskEpisodeFrozenContext      `json:"frozenContext"`
	LatestHypothesis  TaskEpisodeHypothesisRevision `json:"latestHypothesis"`
	CreatedAt         int64                         `json:"createdAt"`
}

type TaskEpisodeDetail struct {
	Episode     TaskEpisode                     `json:"episode"`
	Hypotheses  []TaskEpisodeHypothesisRevision `json:"hypotheses"`
	Evidence    []TaskEpisodeEvidence           `json:"evidence"`
	Transitions []TaskEpisodeTransition         `json:"transitions"`
	Links       []TaskEpisodeLink               `json:"links"`
}

type taskEpisodeCreatePayload struct {
	ID                string                        `json:"id"`
	InferredStartAt   int64                         `json:"inferredStartAt"`
	DetectedAt        int64                         `json:"detectedAt"`
	InitialStatus     string                        `json:"initialStatus"`
	FrozenContext     TaskEpisodeFrozenContext      `json:"frozenContext"`
	InitialEvidence   []TaskEpisodeEvidence         `json:"initialEvidence"`
	InitialHypothesis TaskEpisodeHypothesisRevision `json:"initialHypothesis"`
	InitialTransition TaskEpisodeTransition         `json:"initialTransition"`
	CreatedAt         int64                         `json:"createdAt"`
}

func normalizeTaskEpisodeListLimit(limit int) int {
	switch {
	case limit <= 0:
		return taskEpisodeDefaultListLimit
	case limit > taskEpisodeMaxListLimit:
		return taskEpisodeMaxListLimit
	default:
		return limit
	}
}

func taskEpisodePayloadHash(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func deterministicTaskEpisodeID(kind, key string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(kind+"\x00"+key)).String()
}

func canonicalTaskEpisodeJSON(raw string, required bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if required {
			return "", errors.New("task episode context requires JSON")
		}
		return "{}", nil
	}
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("decode task episode JSON: %w", err)
	}
	if value == nil && required {
		return "", errors.New("task episode context cannot be null")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return "", errors.New("task episode JSON contains multiple values")
		}
		return "", fmt.Errorf("decode trailing task episode JSON: %w", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode canonical task episode JSON: %w", err)
	}
	return string(canonical), nil
}

func normalizeTaskEpisodeStatus(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case TaskEpisodeStatusCandidate,
		TaskEpisodeStatusActive,
		TaskEpisodeStatusPaused,
		TaskEpisodeStatusCompleted,
		TaskEpisodeStatusAbandoned,
		TaskEpisodeStatusMerged,
		TaskEpisodeStatusSplit,
		TaskEpisodeStatusExpired:
		return value, nil
	default:
		return "", fmt.Errorf("unsupported task episode status %q", value)
	}
}

func normalizeTaskEpisodeEvidenceRole(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case TaskEpisodeEvidenceOnset,
		TaskEpisodeEvidenceContext,
		TaskEpisodeEvidenceTask,
		TaskEpisodeEvidenceDesiredResult,
		TaskEpisodeEvidenceAction,
		TaskEpisodeEvidenceResult,
		TaskEpisodeEvidenceReasoning,
		TaskEpisodeEvidenceContradiction:
		return value, nil
	default:
		return "", fmt.Errorf("unsupported task episode evidence role %q", value)
	}
}

func normalizeTaskEpisodeLinkRelation(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case TaskEpisodeLinkParent, TaskEpisodeLinkMergedInto, TaskEpisodeLinkSplitInto:
		return value, nil
	default:
		return "", fmt.Errorf("unsupported task episode link relation %q", value)
	}
}

func validateTaskEpisodeConfidence(name string, value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		return fmt.Errorf("%s must be between 0 and 1", name)
	}
	return nil
}

func normalizeTaskEpisodeIDs(values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, errors.New("task episode evidence IDs cannot be empty")
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out, nil
}

func normalizeTaskEpisodeContext(
	input TaskEpisodeFrozenContext,
	episodeID string,
	inferredStartAt, createdAt int64,
) (TaskEpisodeFrozenContext, error) {
	input.TaskEpisodeID = strings.TrimSpace(input.TaskEpisodeID)
	if input.TaskEpisodeID == "" {
		input.TaskEpisodeID = episodeID
	}
	if input.TaskEpisodeID != episodeID {
		return TaskEpisodeFrozenContext{}, errors.New("frozen context belongs to a different task episode")
	}
	if input.CutoffAt == 0 {
		input.CutoffAt = inferredStartAt
	}
	if input.CutoffAt != inferredStartAt {
		return TaskEpisodeFrozenContext{}, errors.New("frozen context cutoff must equal the inferred task start")
	}
	canonical, err := canonicalTaskEpisodeJSON(input.CanonicalJSON, true)
	if err != nil {
		return TaskEpisodeFrozenContext{}, err
	}
	expectedHash := taskEpisodePayloadHash([]byte(canonical))
	input.SHA256 = strings.ToLower(strings.TrimSpace(input.SHA256))
	if input.SHA256 != "" && input.SHA256 != expectedHash {
		return TaskEpisodeFrozenContext{}, errors.New("frozen context SHA-256 does not match canonical JSON")
	}
	input.CanonicalJSON = canonical
	input.SHA256 = expectedHash
	input.WorkProfileSnapshotID = strings.TrimSpace(
		input.WorkProfileSnapshotID,
	)
	input.WorkProfileSHA256 = strings.ToLower(strings.TrimSpace(
		input.WorkProfileSHA256,
	))
	if (input.WorkProfileSnapshotID == "") != (input.WorkProfileSHA256 == "") {
		return TaskEpisodeFrozenContext{}, errors.New(
			"frozen context work profile reference is incomplete",
		)
	}
	if input.WorkProfileSHA256 != "" && len(input.WorkProfileSHA256) != 64 {
		return TaskEpisodeFrozenContext{}, errors.New(
			"frozen context work profile hash is invalid",
		)
	}
	if input.Manifest == nil {
		input.Manifest = []TaskContextManifestEntry{}
	}
	if input.ID == "" {
		input.ID = deterministicTaskEpisodeID("task-context", episodeID)
	}
	if input.CreatedAt == 0 {
		input.CreatedAt = createdAt
	}
	return input, nil
}

func normalizeTaskEpisodeEvidence(
	input TaskEpisodeEvidence,
	episodeID string,
	defaultCreatedAt int64,
) (TaskEpisodeEvidence, error) {
	input.TaskEpisodeID = strings.TrimSpace(input.TaskEpisodeID)
	if input.TaskEpisodeID == "" {
		input.TaskEpisodeID = episodeID
	}
	if input.TaskEpisodeID != episodeID {
		return TaskEpisodeEvidence{}, errors.New("task episode evidence belongs to a different episode")
	}
	var err error
	if input.Role, err = normalizeTaskEpisodeEvidenceRole(input.Role); err != nil {
		return TaskEpisodeEvidence{}, err
	}
	input.SourceKind = strings.TrimSpace(input.SourceKind)
	input.SourceID = strings.TrimSpace(input.SourceID)
	input.Summary = strings.TrimSpace(input.Summary)
	if input.SourceKind == "" || input.SourceID == "" {
		return TaskEpisodeEvidence{}, errors.New("task episode evidence requires source kind and id")
	}
	if input.ObservedAt <= 0 {
		return TaskEpisodeEvidence{}, errors.New("task episode evidence requires a stable observed timestamp")
	}
	payloadWasProvided := strings.TrimSpace(input.PayloadJSON) != "" &&
		strings.TrimSpace(input.PayloadJSON) != "{}"
	canonical, err := canonicalTaskEpisodeJSON(input.PayloadJSON, false)
	if err != nil {
		return TaskEpisodeEvidence{}, err
	}
	input.PayloadJSON = canonical
	input.ContentHash = strings.ToLower(strings.TrimSpace(input.ContentHash))
	if payloadWasProvided {
		expected := taskEpisodePayloadHash([]byte(canonical))
		if input.ContentHash != "" && input.ContentHash != expected {
			return TaskEpisodeEvidence{}, errors.New("task episode evidence hash does not match payload")
		}
		input.ContentHash = expected
	} else if input.ContentHash == "" {
		input.ContentHash = taskEpisodePayloadHash([]byte(strings.Join([]string{
			input.Role,
			input.SourceKind,
			input.SourceID,
			input.Summary,
		}, "\x00")))
	}
	if input.ID == "" {
		input.ID = deterministicTaskEpisodeID("task-evidence", strings.Join([]string{
			episodeID,
			input.Role,
			input.SourceKind,
			input.SourceID,
			input.ContentHash,
			fmt.Sprint(input.ObservedAt),
		}, "\x00"))
	}
	if input.CreatedAt == 0 {
		input.CreatedAt = defaultCreatedAt
	}
	return input, nil
}

func normalizeTaskEpisodeHypothesisContent(
	input TaskEpisodeHypothesisRevision,
	episodeID string,
) (TaskEpisodeHypothesisRevision, error) {
	input.EvidenceIDs = append([]string(nil), input.EvidenceIDs...)
	input.ProfileInfluenceRefs = append(
		[]string(nil),
		input.ProfileInfluenceRefs...,
	)
	input.Alternatives = append(
		[]TaskEpisodeHypothesisAlternative(nil),
		input.Alternatives...,
	)
	input.ReasoningHypotheses = append(
		[]TaskEpisodeReasoningHypothesis(nil),
		input.ReasoningHypotheses...,
	)
	for i := range input.Alternatives {
		input.Alternatives[i].EvidenceIDs = append(
			[]string(nil),
			input.Alternatives[i].EvidenceIDs...,
		)
	}
	for i := range input.ReasoningHypotheses {
		input.ReasoningHypotheses[i].EvidenceIDs = append(
			[]string(nil),
			input.ReasoningHypotheses[i].EvidenceIDs...,
		)
		input.ReasoningHypotheses[i].ProfileRefs = append(
			[]string(nil),
			input.ReasoningHypotheses[i].ProfileRefs...,
		)
		input.ReasoningHypotheses[i].ContradictionEvidenceIDs = append(
			[]string(nil),
			input.ReasoningHypotheses[i].ContradictionEvidenceIDs...,
		)
	}
	input.TaskEpisodeID = strings.TrimSpace(input.TaskEpisodeID)
	if input.TaskEpisodeID == "" {
		input.TaskEpisodeID = episodeID
	}
	if input.TaskEpisodeID != episodeID {
		return TaskEpisodeHypothesisRevision{}, errors.New("task episode hypothesis belongs to a different episode")
	}
	input.TaskHypothesis = strings.TrimSpace(input.TaskHypothesis)
	input.DesiredResultHypothesis = strings.TrimSpace(input.DesiredResultHypothesis)
	if input.TaskHypothesis == "" || input.DesiredResultHypothesis == "" {
		return TaskEpisodeHypothesisRevision{}, errors.New("task and desired-result hypotheses are required")
	}
	if err := validateTaskEpisodeConfidence("task hypothesis confidence", input.TaskConfidence); err != nil {
		return TaskEpisodeHypothesisRevision{}, err
	}
	if err := validateTaskEpisodeConfidence("boundary confidence", input.BoundaryConfidence); err != nil {
		return TaskEpisodeHypothesisRevision{}, err
	}
	if err := validateTaskEpisodeConfidence("desired-result hypothesis confidence", input.DesiredResultConfidence); err != nil {
		return TaskEpisodeHypothesisRevision{}, err
	}
	var err error
	if input.EvidenceIDs, err = normalizeTaskEpisodeIDs(input.EvidenceIDs); err != nil {
		return TaskEpisodeHypothesisRevision{}, err
	}
	if len(input.EvidenceIDs) == 0 {
		return TaskEpisodeHypothesisRevision{}, errors.New("task episode hypothesis requires evidence")
	}
	if input.ProfileInfluenceRefs, err = normalizeTaskEpisodeIDs(
		input.ProfileInfluenceRefs,
	); err != nil {
		return TaskEpisodeHypothesisRevision{}, fmt.Errorf(
			"normalize profile influence refs: %w",
			err,
		)
	}
	if len(input.ProfileInfluenceRefs) == 0 {
		input.ProfileConflict = false
	}
	if input.ProfileConflict {
		input.TaskConfidence = math.Min(input.TaskConfidence, 0.6)
		input.BoundaryConfidence = math.Min(input.BoundaryConfidence, 0.6)
		input.DesiredResultConfidence = math.Min(
			input.DesiredResultConfidence,
			0.6,
		)
	}
	for i := range input.Alternatives {
		alternative := &input.Alternatives[i]
		alternative.Kind = strings.ToLower(strings.TrimSpace(alternative.Kind))
		if alternative.Kind != "task" && alternative.Kind != "desired_result" {
			return TaskEpisodeHypothesisRevision{}, fmt.Errorf(
				"unsupported task episode hypothesis alternative kind %q",
				alternative.Kind,
			)
		}
		alternative.Statement = strings.TrimSpace(alternative.Statement)
		if alternative.Statement == "" {
			return TaskEpisodeHypothesisRevision{}, errors.New("task episode hypothesis alternative requires a statement")
		}
		if err := validateTaskEpisodeConfidence("task episode alternative confidence", alternative.Confidence); err != nil {
			return TaskEpisodeHypothesisRevision{}, err
		}
		if alternative.EvidenceIDs, err = normalizeTaskEpisodeIDs(alternative.EvidenceIDs); err != nil {
			return TaskEpisodeHypothesisRevision{}, err
		}
	}
	if len(input.ReasoningHypotheses) > taskOnsetMaxReasoning {
		return TaskEpisodeHypothesisRevision{}, fmt.Errorf(
			"task episode hypothesis has %d reasoning hypotheses, exceeds %d",
			len(input.ReasoningHypotheses),
			taskOnsetMaxReasoning,
		)
	}
	seenReasoning := make(map[string]struct{}, len(input.ReasoningHypotheses))
	allowedProfileRefs := make(map[string]struct{}, len(input.ProfileInfluenceRefs))
	for _, ref := range input.ProfileInfluenceRefs {
		allowedProfileRefs[ref] = struct{}{}
	}
	for i := range input.ReasoningHypotheses {
		hypothesis := &input.ReasoningHypotheses[i]
		hypothesis.Statement = strings.TrimSpace(hypothesis.Statement)
		hypothesis.Basis = strings.ToLower(strings.TrimSpace(hypothesis.Basis))
		hypothesis.SourceQuote = strings.TrimSpace(hypothesis.SourceQuote)
		if hypothesis.Statement == "" {
			return TaskEpisodeHypothesisRevision{}, errors.New(
				"task episode reasoning hypothesis requires a statement",
			)
		}
		key := normalizeTaskReasoningText(hypothesis.Statement)
		if _, duplicate := seenReasoning[key]; duplicate {
			return TaskEpisodeHypothesisRevision{}, errors.New(
				"task episode reasoning hypotheses must be unique",
			)
		}
		seenReasoning[key] = struct{}{}
		if err := validateTaskEpisodeConfidence(
			"task episode reasoning confidence",
			hypothesis.Confidence,
		); err != nil {
			return TaskEpisodeHypothesisRevision{}, err
		}
		if hypothesis.EvidenceIDs, err = normalizeTaskEpisodeIDs(
			hypothesis.EvidenceIDs,
		); err != nil {
			return TaskEpisodeHypothesisRevision{}, err
		}
		if hypothesis.ProfileRefs, err = normalizeTaskEpisodeIDs(
			hypothesis.ProfileRefs,
		); err != nil {
			return TaskEpisodeHypothesisRevision{}, err
		}
		if hypothesis.ContradictionEvidenceIDs, err = normalizeTaskEpisodeIDs(
			hypothesis.ContradictionEvidenceIDs,
		); err != nil {
			return TaskEpisodeHypothesisRevision{}, err
		}
		switch hypothesis.Basis {
		case TaskReasoningBasisExplicitStatement:
			if hypothesis.SourceQuote == "" ||
				!taskOnsetReasoningHasExplicitCue(hypothesis.SourceQuote) {
				return TaskEpisodeHypothesisRevision{}, errors.New(
					"explicit task reasoning requires a source quote with a rationale cue",
				)
			}
		case TaskReasoningBasisRepeatedPattern:
			hypothesis.SourceQuote = ""
			if len(hypothesis.ProfileRefs) == 0 {
				return TaskEpisodeHypothesisRevision{}, errors.New(
					"repeated-pattern task reasoning requires profile evidence",
				)
			}
			for _, ref := range hypothesis.ProfileRefs {
				if _, allowed := allowedProfileRefs[ref]; !allowed {
					return TaskEpisodeHypothesisRevision{}, fmt.Errorf(
						"task reasoning references unbound profile evidence %q",
						ref,
					)
				}
			}
			if input.ProfileConflict {
				hypothesis.Confidence = min(hypothesis.Confidence, 0.49)
			}
		case TaskReasoningBasisContextualInference:
			hypothesis.SourceQuote = ""
			hypothesis.ProfileRefs = nil
			hypothesis.Confidence = min(hypothesis.Confidence, 0.49)
		default:
			return TaskEpisodeHypothesisRevision{}, fmt.Errorf(
				"unsupported task reasoning basis %q",
				hypothesis.Basis,
			)
		}
		if len(hypothesis.EvidenceIDs) == 0 && len(hypothesis.ProfileRefs) == 0 {
			return TaskEpisodeHypothesisRevision{}, errors.New(
				"task episode reasoning hypothesis requires evidence",
			)
		}
	}
	return input, nil
}

func taskEpisodeHypothesisIdentity(input TaskEpisodeHypothesisRevision) (string, error) {
	payload, err := json.Marshal(struct {
		TaskEpisodeID           string                             `json:"taskEpisodeId"`
		TaskHypothesis          string                             `json:"taskHypothesis"`
		DesiredResultHypothesis string                             `json:"desiredResultHypothesis"`
		TaskConfidence          float64                            `json:"taskConfidence"`
		BoundaryConfidence      float64                            `json:"boundaryConfidence"`
		DesiredResultConfidence float64                            `json:"desiredResultConfidence"`
		ProfileInfluenceRefs    []string                           `json:"profileInfluenceRefs,omitempty"`
		ProfileConflict         bool                               `json:"profileConflict,omitempty"`
		Alternatives            []TaskEpisodeHypothesisAlternative `json:"alternatives"`
		ReasoningHypotheses     []TaskEpisodeReasoningHypothesis   `json:"reasoningHypotheses,omitempty"`
		EvidenceIDs             []string                           `json:"evidenceIds"`
	}{
		TaskEpisodeID:           input.TaskEpisodeID,
		TaskHypothesis:          input.TaskHypothesis,
		DesiredResultHypothesis: input.DesiredResultHypothesis,
		TaskConfidence:          input.TaskConfidence,
		BoundaryConfidence:      input.BoundaryConfidence,
		DesiredResultConfidence: input.DesiredResultConfidence,
		ProfileInfluenceRefs:    input.ProfileInfluenceRefs,
		ProfileConflict:         input.ProfileConflict,
		Alternatives:            input.Alternatives,
		ReasoningHypotheses:     input.ReasoningHypotheses,
		EvidenceIDs:             input.EvidenceIDs,
	})
	if err != nil {
		return "", err
	}
	return deterministicTaskEpisodeID("task-hypothesis", taskEpisodePayloadHash(payload)), nil
}

func normalizeCreateTaskEpisode(input CreateTaskEpisodeInput) (
	CreateTaskEpisodeInput,
	TaskEpisodeTransition,
	string,
	string,
	error,
) {
	input.InitialEvidence = append([]TaskEpisodeEvidence(nil), input.InitialEvidence...)
	input.ID = strings.TrimSpace(input.ID)
	if input.ID == "" {
		input.ID = uuid.NewString()
	}
	if input.InferredStartAt <= 0 {
		return CreateTaskEpisodeInput{}, TaskEpisodeTransition{}, "", "", errors.New("task episode requires an inferred start time")
	}
	if input.DetectedAt < input.InferredStartAt {
		return CreateTaskEpisodeInput{}, TaskEpisodeTransition{}, "", "", errors.New("task episode detection cannot precede its inferred start")
	}
	var err error
	if input.InitialStatus, err = normalizeTaskEpisodeStatus(input.InitialStatus); err != nil {
		return CreateTaskEpisodeInput{}, TaskEpisodeTransition{}, "", "", err
	}
	if input.CreatedAt == 0 {
		input.CreatedAt = input.DetectedAt
	}
	if input.FrozenContext, err = normalizeTaskEpisodeContext(
		input.FrozenContext,
		input.ID,
		input.InferredStartAt,
		input.CreatedAt,
	); err != nil {
		return CreateTaskEpisodeInput{}, TaskEpisodeTransition{}, "", "", err
	}
	if len(input.InitialEvidence) == 0 {
		return CreateTaskEpisodeInput{}, TaskEpisodeTransition{}, "", "", errors.New("task episode creation requires onset evidence")
	}
	for i := range input.InitialEvidence {
		input.InitialEvidence[i], err = normalizeTaskEpisodeEvidence(
			input.InitialEvidence[i],
			input.ID,
			input.CreatedAt,
		)
		if err != nil {
			return CreateTaskEpisodeInput{}, TaskEpisodeTransition{}, "", "", err
		}
	}
	sort.Slice(input.InitialEvidence, func(i, j int) bool {
		return input.InitialEvidence[i].ID < input.InitialEvidence[j].ID
	})
	if len(input.InitialHypothesis.EvidenceIDs) == 0 {
		input.InitialHypothesis.EvidenceIDs = make([]string, 0, len(input.InitialEvidence))
		for _, evidence := range input.InitialEvidence {
			input.InitialHypothesis.EvidenceIDs = append(input.InitialHypothesis.EvidenceIDs, evidence.ID)
		}
	}
	input.InitialHypothesis, err = normalizeTaskEpisodeHypothesisContent(
		input.InitialHypothesis,
		input.ID,
	)
	if err != nil {
		return CreateTaskEpisodeInput{}, TaskEpisodeTransition{}, "", "", err
	}
	if input.InitialHypothesis.Revision == 0 {
		input.InitialHypothesis.Revision = 1
	}
	if input.InitialHypothesis.Revision != 1 {
		return CreateTaskEpisodeInput{}, TaskEpisodeTransition{}, "", "", errors.New("initial task episode hypothesis must be revision 1")
	}
	if input.InitialHypothesis.ID == "" {
		input.InitialHypothesis.ID, err = taskEpisodeHypothesisIdentity(input.InitialHypothesis)
		if err != nil {
			return CreateTaskEpisodeInput{}, TaskEpisodeTransition{}, "", "", err
		}
	}
	if input.InitialHypothesis.CreatedAt == 0 {
		input.InitialHypothesis.CreatedAt = input.CreatedAt
	}
	evidenceByID := make(map[string]struct{}, len(input.InitialEvidence))
	for _, evidence := range input.InitialEvidence {
		evidenceByID[evidence.ID] = struct{}{}
	}
	for _, evidenceID := range taskEpisodeHypothesisEvidenceIDs(input.InitialHypothesis) {
		if _, ok := evidenceByID[evidenceID]; !ok {
			return CreateTaskEpisodeInput{}, TaskEpisodeTransition{}, "", "", fmt.Errorf(
				"initial task episode hypothesis references unknown evidence %q",
				evidenceID,
			)
		}
	}
	initialTransition := TaskEpisodeTransition{
		ID: deterministicTaskEpisodeID(
			"task-transition",
			strings.Join([]string{input.ID, input.InitialStatus, fmt.Sprint(input.DetectedAt)}, "\x00"),
		),
		TaskEpisodeID: input.ID,
		Sequence:      1,
		ToStatus:      input.InitialStatus,
		Reason:        "task episode detected",
		OccurredAt:    input.DetectedAt,
		CreatedAt:     input.CreatedAt,
	}
	createPayload := taskEpisodeCreatePayload{
		ID:                input.ID,
		InferredStartAt:   input.InferredStartAt,
		DetectedAt:        input.DetectedAt,
		InitialStatus:     input.InitialStatus,
		FrozenContext:     input.FrozenContext,
		InitialEvidence:   input.InitialEvidence,
		InitialHypothesis: input.InitialHypothesis,
		InitialTransition: initialTransition,
		CreatedAt:         input.CreatedAt,
	}
	payload, err := json.Marshal(createPayload)
	if err != nil {
		return CreateTaskEpisodeInput{}, TaskEpisodeTransition{}, "", "", fmt.Errorf(
			"encode task episode creation payload: %w",
			err,
		)
	}
	return input, initialTransition, string(payload), taskEpisodePayloadHash(payload), nil
}

func taskEpisodeHypothesisEvidenceIDs(input TaskEpisodeHypothesisRevision) []string {
	ids := append([]string(nil), input.EvidenceIDs...)
	for _, alternative := range input.Alternatives {
		ids = append(ids, alternative.EvidenceIDs...)
	}
	for _, hypothesis := range input.ReasoningHypotheses {
		ids = append(ids, hypothesis.EvidenceIDs...)
		ids = append(ids, hypothesis.ContradictionEvidenceIDs...)
	}
	normalized, _ := normalizeTaskEpisodeIDs(ids)
	return normalized
}

func taskEpisodeExists(tx *sql.Tx, episodeID string) error {
	var exists int
	if err := tx.QueryRow(`SELECT 1 FROM task_episodes WHERE id = ?`, episodeID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("task episode %q does not exist", episodeID)
		}
		return err
	}
	return nil
}

func validateTaskEpisodeEvidenceReferences(
	tx *sql.Tx,
	episodeID string,
	evidenceIDs []string,
) error {
	for _, evidenceID := range evidenceIDs {
		var evidenceEpisodeID string
		if err := tx.QueryRow(
			`SELECT task_episode_id FROM task_episode_evidence WHERE id = ?`,
			evidenceID,
		).Scan(&evidenceEpisodeID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("task episode hypothesis references unknown evidence %q", evidenceID)
			}
			return err
		}
		if evidenceEpisodeID != episodeID {
			return fmt.Errorf("task episode hypothesis evidence %q belongs to another episode", evidenceID)
		}
	}
	return nil
}

func insertTaskEpisodeEvidence(tx *sql.Tx, item TaskEpisodeEvidence) error {
	if _, err := tx.Exec(`
		INSERT INTO task_episode_evidence (
			id, task_episode_id, role, source_kind, source_id, summary,
			payload_json, content_hash, observed_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID,
		item.TaskEpisodeID,
		item.Role,
		item.SourceKind,
		item.SourceID,
		item.Summary,
		item.PayloadJSON,
		item.ContentHash,
		item.ObservedAt,
		item.CreatedAt,
	); err != nil {
		return fmt.Errorf("insert task episode evidence: %w", err)
	}
	return nil
}

func insertTaskEpisodeHypothesis(tx *sql.Tx, item TaskEpisodeHypothesisRevision) error {
	alternativesJSON, err := json.Marshal(item.Alternatives)
	if err != nil {
		return err
	}
	reasoningJSON, err := json.Marshal(item.ReasoningHypotheses)
	if err != nil {
		return err
	}
	evidenceJSON, err := json.Marshal(item.EvidenceIDs)
	if err != nil {
		return err
	}
	profileInfluenceJSON, err := json.Marshal(struct {
		Refs     []string `json:"refs"`
		Conflict bool     `json:"conflict"`
	}{
		Refs:     item.ProfileInfluenceRefs,
		Conflict: item.ProfileConflict,
	})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO task_episode_hypotheses (
			id, task_episode_id, revision, task_hypothesis,
			desired_result_hypothesis, task_confidence,
			boundary_confidence, desired_result_confidence, alternatives_json,
			evidence_ids_json, profile_influence_json,
			reasoning_hypotheses_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID,
		item.TaskEpisodeID,
		item.Revision,
		item.TaskHypothesis,
		item.DesiredResultHypothesis,
		item.TaskConfidence,
		item.BoundaryConfidence,
		item.DesiredResultConfidence,
		string(alternativesJSON),
		string(evidenceJSON),
		string(profileInfluenceJSON),
		string(reasoningJSON),
		item.CreatedAt,
	); err != nil {
		return fmt.Errorf("insert task episode hypothesis: %w", err)
	}
	return nil
}

// CreateTaskEpisode atomically stores the episode, its immutable start
// context, onset evidence, initial hypothesis, and initial status transition.
// Reusing an episode ID is idempotent only when the normalized creation
// payload is byte-for-byte identical.
func (s *ActivityStore) CreateTaskEpisode(input CreateTaskEpisodeInput) (TaskEpisode, bool, error) {
	if s == nil || s.db == nil {
		return TaskEpisode{}, false, errors.New("activity store is unavailable")
	}
	normalized, initialTransition, createJSON, createHash, err := normalizeCreateTaskEpisode(input)
	if err != nil {
		return TaskEpisode{}, false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return TaskEpisode{}, false, fmt.Errorf("begin task episode creation: %w", err)
	}
	defer tx.Rollback()

	var existingPayload string
	err = tx.QueryRow(
		`SELECT create_payload_json FROM task_episodes WHERE id = ?`,
		normalized.ID,
	).Scan(&existingPayload)
	switch {
	case err == nil:
		if existingPayload != createJSON {
			return TaskEpisode{}, false, fmt.Errorf(
				"task episode %q already has a different immutable creation payload",
				normalized.ID,
			)
		}
		if err := tx.Rollback(); err != nil {
			return TaskEpisode{}, false, err
		}
		existing, getErr := s.GetTaskEpisode(normalized.ID)
		return existing, false, getErr
	case !errors.Is(err, sql.ErrNoRows):
		return TaskEpisode{}, false, err
	}

	if _, err := tx.Exec(`
		INSERT INTO task_episodes (
			id, inferred_start_at, detected_at, create_payload_hash,
			create_payload_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?)`,
		normalized.ID,
		normalized.InferredStartAt,
		normalized.DetectedAt,
		createHash,
		createJSON,
		normalized.CreatedAt,
	); err != nil {
		return TaskEpisode{}, false, fmt.Errorf("insert task episode: %w", err)
	}
	context := normalized.FrozenContext
	manifestJSON, err := json.Marshal(context.Manifest)
	if err != nil {
		return TaskEpisode{}, false, fmt.Errorf(
			"encode task episode context manifest: %w",
			err,
		)
	}
	if _, err := tx.Exec(`
		INSERT INTO task_episode_context_snapshots (
			id, task_episode_id, cutoff_at, canonical_json, sha256,
			manifest_json, work_profile_snapshot_id, work_profile_sha256,
			created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		context.ID,
		context.TaskEpisodeID,
		context.CutoffAt,
		context.CanonicalJSON,
		context.SHA256,
		string(manifestJSON),
		context.WorkProfileSnapshotID,
		context.WorkProfileSHA256,
		context.CreatedAt,
	); err != nil {
		return TaskEpisode{}, false, fmt.Errorf("insert task episode frozen context: %w", err)
	}
	if err := insertTaskContextManifestStats(
		tx,
		deriveTaskContextManifestStats(
			normalized.ID,
			context.ID,
			context.Manifest,
			manifestJSON,
			normalized.InferredStartAt,
			context.CreatedAt,
		),
	); err != nil {
		return TaskEpisode{}, false, err
	}
	for _, evidence := range normalized.InitialEvidence {
		if err := insertTaskEpisodeEvidence(tx, evidence); err != nil {
			return TaskEpisode{}, false, err
		}
	}
	if err := insertTaskEpisodeHypothesis(tx, normalized.InitialHypothesis); err != nil {
		return TaskEpisode{}, false, err
	}
	if _, err := tx.Exec(`
		INSERT INTO task_episode_transitions (
			id, task_episode_id, sequence, from_status, to_status,
			reason, occurred_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		initialTransition.ID,
		initialTransition.TaskEpisodeID,
		initialTransition.Sequence,
		initialTransition.FromStatus,
		initialTransition.ToStatus,
		initialTransition.Reason,
		initialTransition.OccurredAt,
		initialTransition.CreatedAt,
	); err != nil {
		return TaskEpisode{}, false, fmt.Errorf("insert task episode initial transition: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return TaskEpisode{}, false, fmt.Errorf("commit task episode creation: %w", err)
	}
	episode, err := s.GetTaskEpisode(normalized.ID)
	return episode, true, err
}

const taskEpisodeSelectColumns = `
	e.id, e.inferred_start_at, e.detected_at, current_transition.to_status,
	e.create_payload_hash, e.created_at,
	context.id, context.task_episode_id, context.cutoff_at,
	context.canonical_json, context.sha256, context.manifest_json,
	context.work_profile_snapshot_id, context.work_profile_sha256,
	context.created_at,
	hypothesis.id, hypothesis.task_episode_id, hypothesis.revision,
	hypothesis.task_hypothesis, hypothesis.desired_result_hypothesis,
	hypothesis.task_confidence, hypothesis.boundary_confidence,
	hypothesis.desired_result_confidence,
	hypothesis.alternatives_json, hypothesis.evidence_ids_json,
	hypothesis.profile_influence_json, hypothesis.reasoning_hypotheses_json,
	hypothesis.created_at`

func scanTaskEpisode(scanner interface{ Scan(dest ...any) error }) (TaskEpisode, error) {
	var episode TaskEpisode
	var alternativesJSON, evidenceJSON, profileInfluenceJSON, reasoningJSON, manifestJSON string
	if err := scanner.Scan(
		&episode.ID,
		&episode.InferredStartAt,
		&episode.DetectedAt,
		&episode.CurrentStatus,
		&episode.CreatePayloadHash,
		&episode.CreatedAt,
		&episode.FrozenContext.ID,
		&episode.FrozenContext.TaskEpisodeID,
		&episode.FrozenContext.CutoffAt,
		&episode.FrozenContext.CanonicalJSON,
		&episode.FrozenContext.SHA256,
		&manifestJSON,
		&episode.FrozenContext.WorkProfileSnapshotID,
		&episode.FrozenContext.WorkProfileSHA256,
		&episode.FrozenContext.CreatedAt,
		&episode.LatestHypothesis.ID,
		&episode.LatestHypothesis.TaskEpisodeID,
		&episode.LatestHypothesis.Revision,
		&episode.LatestHypothesis.TaskHypothesis,
		&episode.LatestHypothesis.DesiredResultHypothesis,
		&episode.LatestHypothesis.TaskConfidence,
		&episode.LatestHypothesis.BoundaryConfidence,
		&episode.LatestHypothesis.DesiredResultConfidence,
		&alternativesJSON,
		&evidenceJSON,
		&profileInfluenceJSON,
		&reasoningJSON,
		&episode.LatestHypothesis.CreatedAt,
	); err != nil {
		return TaskEpisode{}, err
	}
	if err := json.Unmarshal([]byte(alternativesJSON), &episode.LatestHypothesis.Alternatives); err != nil {
		return TaskEpisode{}, fmt.Errorf("decode task episode alternatives %s: %w", episode.ID, err)
	}
	if err := json.Unmarshal([]byte(evidenceJSON), &episode.LatestHypothesis.EvidenceIDs); err != nil {
		return TaskEpisode{}, fmt.Errorf("decode task episode evidence IDs %s: %w", episode.ID, err)
	}
	var profileInfluence struct {
		Refs     []string `json:"refs"`
		Conflict bool     `json:"conflict"`
	}
	if err := json.Unmarshal(
		[]byte(profileInfluenceJSON),
		&profileInfluence,
	); err != nil {
		return TaskEpisode{}, fmt.Errorf(
			"decode task episode profile influence %s: %w",
			episode.ID,
			err,
		)
	}
	episode.LatestHypothesis.ProfileInfluenceRefs = profileInfluence.Refs
	episode.LatestHypothesis.ProfileConflict = profileInfluence.Conflict
	if err := json.Unmarshal(
		[]byte(reasoningJSON),
		&episode.LatestHypothesis.ReasoningHypotheses,
	); err != nil {
		return TaskEpisode{}, fmt.Errorf(
			"decode task episode reasoning hypotheses %s: %w",
			episode.ID,
			err,
		)
	}
	if err := json.Unmarshal([]byte(manifestJSON), &episode.FrozenContext.Manifest); err != nil {
		return TaskEpisode{}, fmt.Errorf(
			"decode task episode context manifest %s: %w",
			episode.ID,
			err,
		)
	}
	return episode, nil
}

const taskEpisodeCurrentJoins = `
	JOIN task_episode_context_snapshots context
		ON context.task_episode_id = e.id
	JOIN task_episode_transitions current_transition
		ON current_transition.task_episode_id = e.id
		AND current_transition.sequence = (
			SELECT MAX(candidate_transition.sequence)
			FROM task_episode_transitions candidate_transition
			WHERE candidate_transition.task_episode_id = e.id
		)
	JOIN task_episode_hypotheses hypothesis
		ON hypothesis.task_episode_id = e.id
		AND hypothesis.revision = (
			SELECT MAX(candidate_hypothesis.revision)
			FROM task_episode_hypotheses candidate_hypothesis
			WHERE candidate_hypothesis.task_episode_id = e.id
		)`

func (s *ActivityStore) GetTaskEpisode(id string) (TaskEpisode, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return TaskEpisode{}, errors.New("task episode ID is required")
	}
	return scanTaskEpisode(s.db.QueryRow(`
		SELECT `+taskEpisodeSelectColumns+`
		FROM task_episodes e
		`+taskEpisodeCurrentJoins+`
		WHERE e.id = ?`,
		id,
	))
}

// ListTaskEpisodes returns overlapping episodes independently. It never
// coalesces by time, source, or status. Ordering is deterministic and newest
// inferred starts appear first.
func (s *ActivityStore) ListTaskEpisodes(status string, limit int) ([]TaskEpisode, error) {
	status = strings.TrimSpace(status)
	if status != "" {
		var err error
		if status, err = normalizeTaskEpisodeStatus(status); err != nil {
			return nil, err
		}
	}
	limit = normalizeTaskEpisodeListLimit(limit)
	rows, err := s.db.Query(`
		SELECT `+taskEpisodeSelectColumns+`
		FROM task_episodes e
		`+taskEpisodeCurrentJoins+`
		WHERE (? = '' OR current_transition.to_status = ?)
		ORDER BY e.inferred_start_at DESC, e.detected_at DESC, e.id ASC
		LIMIT ?`,
		status,
		status,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list task episodes: %w", err)
	}
	defer rows.Close()
	items := make([]TaskEpisode, 0)
	for rows.Next() {
		item, err := scanTaskEpisode(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanTaskEpisodeEvidence(scanner interface{ Scan(dest ...any) error }) (TaskEpisodeEvidence, error) {
	var item TaskEpisodeEvidence
	if err := scanner.Scan(
		&item.ID,
		&item.TaskEpisodeID,
		&item.Role,
		&item.SourceKind,
		&item.SourceID,
		&item.Summary,
		&item.PayloadJSON,
		&item.ContentHash,
		&item.ObservedAt,
		&item.CreatedAt,
	); err != nil {
		return TaskEpisodeEvidence{}, err
	}
	return item, nil
}

func taskEpisodeEvidenceEquivalent(left, right TaskEpisodeEvidence) bool {
	return left.TaskEpisodeID == right.TaskEpisodeID &&
		left.Role == right.Role &&
		left.SourceKind == right.SourceKind &&
		left.SourceID == right.SourceID &&
		left.Summary == right.Summary &&
		left.PayloadJSON == right.PayloadJSON &&
		left.ContentHash == right.ContentHash &&
		left.ObservedAt == right.ObservedAt
}

func getTaskEpisodeEvidenceByID(
	queryer interface {
		QueryRow(query string, args ...any) *sql.Row
	},
	id string,
) (TaskEpisodeEvidence, error) {
	return scanTaskEpisodeEvidence(queryer.QueryRow(`
		SELECT id, task_episode_id, role, source_kind, source_id, summary,
			payload_json, content_hash, observed_at, created_at
		FROM task_episode_evidence
		WHERE id = ?`,
		id,
	))
}

func (s *ActivityStore) AppendTaskEpisodeEvidence(
	input TaskEpisodeEvidence,
) (TaskEpisodeEvidence, bool, error) {
	if s == nil || s.db == nil {
		return TaskEpisodeEvidence{}, false, errors.New("activity store is unavailable")
	}
	episodeID := strings.TrimSpace(input.TaskEpisodeID)
	if episodeID == "" {
		return TaskEpisodeEvidence{}, false, errors.New("task episode evidence requires an episode ID")
	}
	normalized, err := normalizeTaskEpisodeEvidence(input, episodeID, time.Now().UnixMilli())
	if err != nil {
		return TaskEpisodeEvidence{}, false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return TaskEpisodeEvidence{}, false, err
	}
	defer tx.Rollback()
	if err := taskEpisodeExists(tx, episodeID); err != nil {
		return TaskEpisodeEvidence{}, false, err
	}
	existing, err := getTaskEpisodeEvidenceByID(tx, normalized.ID)
	switch {
	case err == nil:
		if !taskEpisodeEvidenceEquivalent(existing, normalized) {
			return TaskEpisodeEvidence{}, false, fmt.Errorf(
				"task episode evidence %q already has a different immutable payload",
				normalized.ID,
			)
		}
		return existing, false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return TaskEpisodeEvidence{}, false, err
	}
	result, err := tx.Exec(`
		INSERT OR IGNORE INTO task_episode_evidence (
			id, task_episode_id, role, source_kind, source_id, summary,
			payload_json, content_hash, observed_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		normalized.ID,
		normalized.TaskEpisodeID,
		normalized.Role,
		normalized.SourceKind,
		normalized.SourceID,
		normalized.Summary,
		normalized.PayloadJSON,
		normalized.ContentHash,
		normalized.ObservedAt,
		normalized.CreatedAt,
	)
	if err != nil {
		return TaskEpisodeEvidence{}, false, fmt.Errorf("append task episode evidence: %w", err)
	}
	added, err := result.RowsAffected()
	if err != nil {
		return TaskEpisodeEvidence{}, false, err
	}
	if added == 0 {
		existing, err = scanTaskEpisodeEvidence(tx.QueryRow(`
			SELECT id, task_episode_id, role, source_kind, source_id, summary,
				payload_json, content_hash, observed_at, created_at
			FROM task_episode_evidence
			WHERE task_episode_id = ? AND role = ? AND source_kind = ?
				AND source_id = ? AND content_hash = ? AND observed_at = ?`,
			normalized.TaskEpisodeID,
			normalized.Role,
			normalized.SourceKind,
			normalized.SourceID,
			normalized.ContentHash,
			normalized.ObservedAt,
		))
		if err != nil {
			return TaskEpisodeEvidence{}, false, err
		}
		if !taskEpisodeEvidenceEquivalent(existing, normalized) {
			return TaskEpisodeEvidence{}, false, errors.New(
				"task episode evidence key already has a different immutable payload",
			)
		}
		return existing, false, nil
	}
	if err := tx.Commit(); err != nil {
		return TaskEpisodeEvidence{}, false, err
	}
	return normalized, true, nil
}

func (s *ActivityStore) ListTaskEpisodeEvidence(
	episodeID, role string,
	limit int,
) ([]TaskEpisodeEvidence, error) {
	episodeID = strings.TrimSpace(episodeID)
	if episodeID == "" {
		return nil, errors.New("task episode ID is required")
	}
	role = strings.TrimSpace(role)
	if role != "" {
		var err error
		if role, err = normalizeTaskEpisodeEvidenceRole(role); err != nil {
			return nil, err
		}
	}
	limit = normalizeTaskEpisodeListLimit(limit)
	rows, err := s.db.Query(`
		SELECT id, task_episode_id, role, source_kind, source_id, summary,
			payload_json, content_hash, observed_at, created_at
		FROM task_episode_evidence
		WHERE task_episode_id = ? AND (? = '' OR role = ?)
		ORDER BY observed_at ASC, created_at ASC, id ASC
		LIMIT ?`,
		episodeID,
		role,
		role,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TaskEpisodeEvidence, 0)
	for rows.Next() {
		item, err := scanTaskEpisodeEvidence(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanTaskEpisodeHypothesis(
	scanner interface{ Scan(dest ...any) error },
) (TaskEpisodeHypothesisRevision, error) {
	var item TaskEpisodeHypothesisRevision
	var alternativesJSON, evidenceJSON, profileInfluenceJSON, reasoningJSON string
	if err := scanner.Scan(
		&item.ID,
		&item.TaskEpisodeID,
		&item.Revision,
		&item.TaskHypothesis,
		&item.DesiredResultHypothesis,
		&item.TaskConfidence,
		&item.BoundaryConfidence,
		&item.DesiredResultConfidence,
		&alternativesJSON,
		&evidenceJSON,
		&profileInfluenceJSON,
		&reasoningJSON,
		&item.CreatedAt,
	); err != nil {
		return TaskEpisodeHypothesisRevision{}, err
	}
	if err := json.Unmarshal([]byte(alternativesJSON), &item.Alternatives); err != nil {
		return TaskEpisodeHypothesisRevision{}, err
	}
	if err := json.Unmarshal([]byte(evidenceJSON), &item.EvidenceIDs); err != nil {
		return TaskEpisodeHypothesisRevision{}, err
	}
	var profileInfluence struct {
		Refs     []string `json:"refs"`
		Conflict bool     `json:"conflict"`
	}
	if err := json.Unmarshal(
		[]byte(profileInfluenceJSON),
		&profileInfluence,
	); err != nil {
		return TaskEpisodeHypothesisRevision{}, err
	}
	item.ProfileInfluenceRefs = profileInfluence.Refs
	item.ProfileConflict = profileInfluence.Conflict
	if err := json.Unmarshal(
		[]byte(reasoningJSON),
		&item.ReasoningHypotheses,
	); err != nil {
		return TaskEpisodeHypothesisRevision{}, err
	}
	return item, nil
}

func taskEpisodeHypothesisEquivalent(
	left, right TaskEpisodeHypothesisRevision,
) bool {
	left.ID, right.ID = "", ""
	left.CreatedAt, right.CreatedAt = 0, 0
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func getTaskEpisodeHypothesisByID(
	queryer interface {
		QueryRow(query string, args ...any) *sql.Row
	},
	id string,
) (TaskEpisodeHypothesisRevision, error) {
	return scanTaskEpisodeHypothesis(queryer.QueryRow(`
		SELECT id, task_episode_id, revision, task_hypothesis,
			desired_result_hypothesis, task_confidence,
			boundary_confidence, desired_result_confidence, alternatives_json,
			evidence_ids_json, profile_influence_json,
			reasoning_hypotheses_json, created_at
		FROM task_episode_hypotheses
		WHERE id = ?`,
		id,
	))
}

func (s *ActivityStore) AppendTaskEpisodeHypothesis(
	input TaskEpisodeHypothesisRevision,
) (TaskEpisodeHypothesisRevision, bool, error) {
	if s == nil || s.db == nil {
		return TaskEpisodeHypothesisRevision{}, false, errors.New("activity store is unavailable")
	}
	episodeID := strings.TrimSpace(input.TaskEpisodeID)
	if episodeID == "" {
		return TaskEpisodeHypothesisRevision{}, false, errors.New("task episode hypothesis requires an episode ID")
	}
	normalized, err := normalizeTaskEpisodeHypothesisContent(input, episodeID)
	if err != nil {
		return TaskEpisodeHypothesisRevision{}, false, err
	}
	if normalized.ID == "" {
		normalized.ID, err = taskEpisodeHypothesisIdentity(normalized)
		if err != nil {
			return TaskEpisodeHypothesisRevision{}, false, err
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return TaskEpisodeHypothesisRevision{}, false, err
	}
	defer tx.Rollback()
	if err := taskEpisodeExists(tx, episodeID); err != nil {
		return TaskEpisodeHypothesisRevision{}, false, err
	}
	existing, err := getTaskEpisodeHypothesisByID(tx, normalized.ID)
	switch {
	case err == nil:
		if normalized.Revision == 0 {
			normalized.Revision = existing.Revision
		}
		if !taskEpisodeHypothesisEquivalent(existing, normalized) {
			return TaskEpisodeHypothesisRevision{}, false, fmt.Errorf(
				"task episode hypothesis %q already has a different immutable payload",
				normalized.ID,
			)
		}
		return existing, false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return TaskEpisodeHypothesisRevision{}, false, err
	}
	var latestRevision int
	if err := tx.QueryRow(`
		SELECT COALESCE(MAX(revision), 0)
		FROM task_episode_hypotheses
		WHERE task_episode_id = ?`,
		episodeID,
	).Scan(&latestRevision); err != nil {
		return TaskEpisodeHypothesisRevision{}, false, err
	}
	if normalized.Revision == 0 {
		normalized.Revision = latestRevision + 1
	}
	if normalized.Revision != latestRevision+1 {
		return TaskEpisodeHypothesisRevision{}, false, fmt.Errorf(
			"task episode hypothesis revision must be %d",
			latestRevision+1,
		)
	}
	if err := validateTaskEpisodeEvidenceReferences(
		tx,
		episodeID,
		taskEpisodeHypothesisEvidenceIDs(normalized),
	); err != nil {
		return TaskEpisodeHypothesisRevision{}, false, err
	}
	if normalized.CreatedAt == 0 {
		normalized.CreatedAt = time.Now().UnixMilli()
	}
	alternativesJSON, err := json.Marshal(normalized.Alternatives)
	if err != nil {
		return TaskEpisodeHypothesisRevision{}, false, err
	}
	reasoningJSON, err := json.Marshal(normalized.ReasoningHypotheses)
	if err != nil {
		return TaskEpisodeHypothesisRevision{}, false, err
	}
	evidenceJSON, err := json.Marshal(normalized.EvidenceIDs)
	if err != nil {
		return TaskEpisodeHypothesisRevision{}, false, err
	}
	profileInfluenceJSON, err := json.Marshal(struct {
		Refs     []string `json:"refs"`
		Conflict bool     `json:"conflict"`
	}{
		Refs:     normalized.ProfileInfluenceRefs,
		Conflict: normalized.ProfileConflict,
	})
	if err != nil {
		return TaskEpisodeHypothesisRevision{}, false, err
	}
	result, err := tx.Exec(`
		INSERT OR IGNORE INTO task_episode_hypotheses (
			id, task_episode_id, revision, task_hypothesis,
			desired_result_hypothesis, task_confidence,
			boundary_confidence, desired_result_confidence, alternatives_json,
			evidence_ids_json, profile_influence_json,
			reasoning_hypotheses_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		normalized.ID,
		normalized.TaskEpisodeID,
		normalized.Revision,
		normalized.TaskHypothesis,
		normalized.DesiredResultHypothesis,
		normalized.TaskConfidence,
		normalized.BoundaryConfidence,
		normalized.DesiredResultConfidence,
		string(alternativesJSON),
		string(evidenceJSON),
		string(profileInfluenceJSON),
		string(reasoningJSON),
		normalized.CreatedAt,
	)
	if err != nil {
		return TaskEpisodeHypothesisRevision{}, false, fmt.Errorf("append task episode hypothesis: %w", err)
	}
	added, err := result.RowsAffected()
	if err != nil {
		return TaskEpisodeHypothesisRevision{}, false, err
	}
	if added == 0 {
		existing, err = scanTaskEpisodeHypothesis(tx.QueryRow(`
			SELECT id, task_episode_id, revision, task_hypothesis,
				desired_result_hypothesis, task_confidence,
				boundary_confidence, desired_result_confidence, alternatives_json,
				evidence_ids_json, profile_influence_json,
				reasoning_hypotheses_json, created_at
			FROM task_episode_hypotheses
			WHERE task_episode_id = ? AND revision = ?`,
			episodeID,
			normalized.Revision,
		))
		if err != nil {
			return TaskEpisodeHypothesisRevision{}, false, err
		}
		if !taskEpisodeHypothesisEquivalent(existing, normalized) {
			return TaskEpisodeHypothesisRevision{}, false, errors.New(
				"task episode hypothesis revision already has a different immutable payload",
			)
		}
		return existing, false, nil
	}
	if err := tx.Commit(); err != nil {
		return TaskEpisodeHypothesisRevision{}, false, err
	}
	return normalized, true, nil
}

func (s *ActivityStore) ListTaskEpisodeHypotheses(
	episodeID string,
	limit int,
) ([]TaskEpisodeHypothesisRevision, error) {
	episodeID = strings.TrimSpace(episodeID)
	if episodeID == "" {
		return nil, errors.New("task episode ID is required")
	}
	limit = normalizeTaskEpisodeListLimit(limit)
	rows, err := s.db.Query(`
		SELECT id, task_episode_id, revision, task_hypothesis,
			desired_result_hypothesis, task_confidence,
			boundary_confidence, desired_result_confidence, alternatives_json,
			evidence_ids_json, profile_influence_json,
			reasoning_hypotheses_json, created_at
		FROM task_episode_hypotheses
		WHERE task_episode_id = ?
		ORDER BY revision ASC, id ASC
		LIMIT ?`,
		episodeID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TaskEpisodeHypothesisRevision, 0)
	for rows.Next() {
		item, err := scanTaskEpisodeHypothesis(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanTaskEpisodeTransition(
	scanner interface{ Scan(dest ...any) error },
) (TaskEpisodeTransition, error) {
	var item TaskEpisodeTransition
	if err := scanner.Scan(
		&item.ID,
		&item.TaskEpisodeID,
		&item.Sequence,
		&item.FromStatus,
		&item.ToStatus,
		&item.Reason,
		&item.OccurredAt,
		&item.CreatedAt,
	); err != nil {
		return TaskEpisodeTransition{}, err
	}
	return item, nil
}

func taskEpisodeTransitionEquivalent(left, right TaskEpisodeTransition) bool {
	return left.TaskEpisodeID == right.TaskEpisodeID &&
		left.Sequence == right.Sequence &&
		left.FromStatus == right.FromStatus &&
		left.ToStatus == right.ToStatus &&
		left.Reason == right.Reason &&
		left.OccurredAt == right.OccurredAt
}

func taskEpisodeTransitionAllowed(from, to string) bool {
	switch from {
	case TaskEpisodeStatusCandidate:
		return to == TaskEpisodeStatusActive ||
			to == TaskEpisodeStatusAbandoned ||
			to == TaskEpisodeStatusMerged ||
			to == TaskEpisodeStatusSplit ||
			to == TaskEpisodeStatusExpired
	case TaskEpisodeStatusActive:
		return to == TaskEpisodeStatusPaused ||
			to == TaskEpisodeStatusCompleted ||
			to == TaskEpisodeStatusAbandoned ||
			to == TaskEpisodeStatusMerged ||
			to == TaskEpisodeStatusSplit ||
			to == TaskEpisodeStatusExpired
	case TaskEpisodeStatusPaused:
		return to == TaskEpisodeStatusActive ||
			to == TaskEpisodeStatusCompleted ||
			to == TaskEpisodeStatusAbandoned ||
			to == TaskEpisodeStatusMerged ||
			to == TaskEpisodeStatusSplit ||
			to == TaskEpisodeStatusExpired
	default:
		return false
	}
}

func getTaskEpisodeTransitionByID(
	queryer interface {
		QueryRow(query string, args ...any) *sql.Row
	},
	id string,
) (TaskEpisodeTransition, error) {
	return scanTaskEpisodeTransition(queryer.QueryRow(`
		SELECT id, task_episode_id, sequence, from_status, to_status,
			reason, occurred_at, created_at
		FROM task_episode_transitions
		WHERE id = ?`,
		id,
	))
}

func (s *ActivityStore) AppendTaskEpisodeTransition(
	input TaskEpisodeTransition,
) (TaskEpisodeTransition, bool, error) {
	if s == nil || s.db == nil {
		return TaskEpisodeTransition{}, false, errors.New("activity store is unavailable")
	}
	input.TaskEpisodeID = strings.TrimSpace(input.TaskEpisodeID)
	if input.TaskEpisodeID == "" {
		return TaskEpisodeTransition{}, false, errors.New("task episode transition requires an episode ID")
	}
	var err error
	if input.ToStatus, err = normalizeTaskEpisodeStatus(input.ToStatus); err != nil {
		return TaskEpisodeTransition{}, false, err
	}
	if strings.TrimSpace(input.FromStatus) != "" {
		if input.FromStatus, err = normalizeTaskEpisodeStatus(input.FromStatus); err != nil {
			return TaskEpisodeTransition{}, false, err
		}
	}
	input.Reason = strings.TrimSpace(input.Reason)
	if input.OccurredAt <= 0 {
		return TaskEpisodeTransition{}, false, errors.New("task episode transition requires a stable occurrence timestamp")
	}
	if input.ID == "" {
		input.ID = deterministicTaskEpisodeID("task-transition", strings.Join([]string{
			input.TaskEpisodeID,
			input.ToStatus,
			input.Reason,
			fmt.Sprint(input.OccurredAt),
		}, "\x00"))
	}
	tx, err := s.db.Begin()
	if err != nil {
		return TaskEpisodeTransition{}, false, err
	}
	defer tx.Rollback()
	if err := taskEpisodeExists(tx, input.TaskEpisodeID); err != nil {
		return TaskEpisodeTransition{}, false, err
	}
	existing, err := getTaskEpisodeTransitionByID(tx, input.ID)
	switch {
	case err == nil:
		if input.Sequence == 0 {
			input.Sequence = existing.Sequence
		}
		if input.FromStatus == "" {
			input.FromStatus = existing.FromStatus
		}
		if !taskEpisodeTransitionEquivalent(existing, input) {
			return TaskEpisodeTransition{}, false, fmt.Errorf(
				"task episode transition %q already has a different immutable payload",
				input.ID,
			)
		}
		return existing, false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return TaskEpisodeTransition{}, false, err
	}
	current, err := scanTaskEpisodeTransition(tx.QueryRow(`
		SELECT id, task_episode_id, sequence, from_status, to_status,
			reason, occurred_at, created_at
		FROM task_episode_transitions
		WHERE task_episode_id = ?
		ORDER BY sequence DESC
		LIMIT 1`,
		input.TaskEpisodeID,
	))
	if err != nil {
		return TaskEpisodeTransition{}, false, err
	}
	if input.FromStatus == "" {
		input.FromStatus = current.ToStatus
	}
	if input.FromStatus != current.ToStatus {
		return TaskEpisodeTransition{}, false, fmt.Errorf(
			"task episode transition expected current status %q, got %q",
			current.ToStatus,
			input.FromStatus,
		)
	}
	if !taskEpisodeTransitionAllowed(input.FromStatus, input.ToStatus) {
		return TaskEpisodeTransition{}, false, fmt.Errorf(
			"task episode transition from %q to %q is not allowed",
			input.FromStatus,
			input.ToStatus,
		)
	}
	if input.Sequence == 0 {
		input.Sequence = current.Sequence + 1
	}
	if input.Sequence != current.Sequence+1 {
		return TaskEpisodeTransition{}, false, fmt.Errorf(
			"task episode transition sequence must be %d",
			current.Sequence+1,
		)
	}
	if input.OccurredAt < current.OccurredAt {
		return TaskEpisodeTransition{}, false, errors.New(
			"task episode transition cannot occur before the previous transition",
		)
	}
	if input.CreatedAt == 0 {
		input.CreatedAt = time.Now().UnixMilli()
	}
	result, err := tx.Exec(`
		INSERT OR IGNORE INTO task_episode_transitions (
			id, task_episode_id, sequence, from_status, to_status,
			reason, occurred_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		input.ID,
		input.TaskEpisodeID,
		input.Sequence,
		input.FromStatus,
		input.ToStatus,
		input.Reason,
		input.OccurredAt,
		input.CreatedAt,
	)
	if err != nil {
		return TaskEpisodeTransition{}, false, fmt.Errorf("append task episode transition: %w", err)
	}
	added, err := result.RowsAffected()
	if err != nil {
		return TaskEpisodeTransition{}, false, err
	}
	if added == 0 {
		existing, err = scanTaskEpisodeTransition(tx.QueryRow(`
			SELECT id, task_episode_id, sequence, from_status, to_status,
				reason, occurred_at, created_at
			FROM task_episode_transitions
			WHERE task_episode_id = ? AND sequence = ?`,
			input.TaskEpisodeID,
			input.Sequence,
		))
		if err != nil {
			return TaskEpisodeTransition{}, false, err
		}
		if !taskEpisodeTransitionEquivalent(existing, input) {
			return TaskEpisodeTransition{}, false, errors.New(
				"task episode transition sequence already has a different immutable payload",
			)
		}
		return existing, false, nil
	}
	if err := tx.Commit(); err != nil {
		return TaskEpisodeTransition{}, false, err
	}
	return input, true, nil
}

func (s *ActivityStore) ListTaskEpisodeTransitions(
	episodeID string,
	limit int,
) ([]TaskEpisodeTransition, error) {
	episodeID = strings.TrimSpace(episodeID)
	if episodeID == "" {
		return nil, errors.New("task episode ID is required")
	}
	limit = normalizeTaskEpisodeListLimit(limit)
	rows, err := s.db.Query(`
		SELECT id, task_episode_id, sequence, from_status, to_status,
			reason, occurred_at, created_at
		FROM task_episode_transitions
		WHERE task_episode_id = ?
		ORDER BY sequence ASC, id ASC
		LIMIT ?`,
		episodeID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TaskEpisodeTransition, 0)
	for rows.Next() {
		item, err := scanTaskEpisodeTransition(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanTaskEpisodeLink(scanner interface{ Scan(dest ...any) error }) (TaskEpisodeLink, error) {
	var item TaskEpisodeLink
	if err := scanner.Scan(
		&item.ID,
		&item.SourceEpisodeID,
		&item.TargetEpisodeID,
		&item.Relation,
		&item.CreatedAt,
	); err != nil {
		return TaskEpisodeLink{}, err
	}
	return item, nil
}

func taskEpisodeLinkEquivalent(left, right TaskEpisodeLink) bool {
	return left.SourceEpisodeID == right.SourceEpisodeID &&
		left.TargetEpisodeID == right.TargetEpisodeID &&
		left.Relation == right.Relation
}

func getTaskEpisodeLinkByID(
	queryer interface {
		QueryRow(query string, args ...any) *sql.Row
	},
	id string,
) (TaskEpisodeLink, error) {
	return scanTaskEpisodeLink(queryer.QueryRow(`
		SELECT id, source_episode_id, target_episode_id, relation, created_at
		FROM task_episode_links
		WHERE id = ?`,
		id,
	))
}

func (s *ActivityStore) AppendTaskEpisodeLink(
	input TaskEpisodeLink,
) (TaskEpisodeLink, bool, error) {
	if s == nil || s.db == nil {
		return TaskEpisodeLink{}, false, errors.New("activity store is unavailable")
	}
	input.SourceEpisodeID = strings.TrimSpace(input.SourceEpisodeID)
	input.TargetEpisodeID = strings.TrimSpace(input.TargetEpisodeID)
	if input.SourceEpisodeID == "" || input.TargetEpisodeID == "" {
		return TaskEpisodeLink{}, false, errors.New("task episode link requires source and target episodes")
	}
	if input.SourceEpisodeID == input.TargetEpisodeID {
		return TaskEpisodeLink{}, false, errors.New("task episode cannot link to itself")
	}
	var err error
	if input.Relation, err = normalizeTaskEpisodeLinkRelation(input.Relation); err != nil {
		return TaskEpisodeLink{}, false, err
	}
	if input.ID == "" {
		input.ID = deterministicTaskEpisodeID("task-link", strings.Join([]string{
			input.SourceEpisodeID,
			input.TargetEpisodeID,
			input.Relation,
		}, "\x00"))
	}
	if input.CreatedAt == 0 {
		input.CreatedAt = time.Now().UnixMilli()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return TaskEpisodeLink{}, false, err
	}
	defer tx.Rollback()
	if err := taskEpisodeExists(tx, input.SourceEpisodeID); err != nil {
		return TaskEpisodeLink{}, false, err
	}
	if err := taskEpisodeExists(tx, input.TargetEpisodeID); err != nil {
		return TaskEpisodeLink{}, false, err
	}
	existing, err := getTaskEpisodeLinkByID(tx, input.ID)
	switch {
	case err == nil:
		if !taskEpisodeLinkEquivalent(existing, input) {
			return TaskEpisodeLink{}, false, fmt.Errorf(
				"task episode link %q already has a different immutable payload",
				input.ID,
			)
		}
		return existing, false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return TaskEpisodeLink{}, false, err
	}
	result, err := tx.Exec(`
		INSERT OR IGNORE INTO task_episode_links (
			id, source_episode_id, target_episode_id, relation, created_at
		) VALUES (?, ?, ?, ?, ?)`,
		input.ID,
		input.SourceEpisodeID,
		input.TargetEpisodeID,
		input.Relation,
		input.CreatedAt,
	)
	if err != nil {
		return TaskEpisodeLink{}, false, fmt.Errorf("append task episode link: %w", err)
	}
	added, err := result.RowsAffected()
	if err != nil {
		return TaskEpisodeLink{}, false, err
	}
	if added == 0 {
		existing, err = scanTaskEpisodeLink(tx.QueryRow(`
			SELECT id, source_episode_id, target_episode_id, relation, created_at
			FROM task_episode_links
			WHERE source_episode_id = ? AND target_episode_id = ? AND relation = ?`,
			input.SourceEpisodeID,
			input.TargetEpisodeID,
			input.Relation,
		))
		if err != nil {
			return TaskEpisodeLink{}, false, err
		}
		if !taskEpisodeLinkEquivalent(existing, input) {
			return TaskEpisodeLink{}, false, errors.New(
				"task episode link key already has a different immutable payload",
			)
		}
		return existing, false, nil
	}
	if err := tx.Commit(); err != nil {
		return TaskEpisodeLink{}, false, err
	}
	return input, true, nil
}

func (s *ActivityStore) ListTaskEpisodeLinks(
	episodeID string,
	limit int,
) ([]TaskEpisodeLink, error) {
	episodeID = strings.TrimSpace(episodeID)
	if episodeID == "" {
		return nil, errors.New("task episode ID is required")
	}
	limit = normalizeTaskEpisodeListLimit(limit)
	rows, err := s.db.Query(`
		SELECT id, source_episode_id, target_episode_id, relation, created_at
		FROM task_episode_links
		WHERE source_episode_id = ? OR target_episode_id = ?
		ORDER BY created_at ASC, relation ASC, source_episode_id ASC,
			target_episode_id ASC, id ASC
		LIMIT ?`,
		episodeID,
		episodeID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TaskEpisodeLink, 0)
	for rows.Next() {
		item, err := scanTaskEpisodeLink(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *ActivityStore) GetTaskEpisodeDetail(id string) (TaskEpisodeDetail, error) {
	episode, err := s.GetTaskEpisode(id)
	if err != nil {
		return TaskEpisodeDetail{}, err
	}
	hypotheses, err := s.ListTaskEpisodeHypotheses(id, taskEpisodeMaxListLimit)
	if err != nil {
		return TaskEpisodeDetail{}, err
	}
	evidence, err := s.ListTaskEpisodeEvidence(id, "", taskEpisodeMaxListLimit)
	if err != nil {
		return TaskEpisodeDetail{}, err
	}
	transitions, err := s.ListTaskEpisodeTransitions(id, taskEpisodeMaxListLimit)
	if err != nil {
		return TaskEpisodeDetail{}, err
	}
	links, err := s.ListTaskEpisodeLinks(id, taskEpisodeMaxListLimit)
	if err != nil {
		return TaskEpisodeDetail{}, err
	}
	return TaskEpisodeDetail{
		Episode:     episode,
		Hypotheses:  hypotheses,
		Evidence:    evidence,
		Transitions: transitions,
		Links:       links,
	}, nil
}
