package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"wails-gui/internal/platform/activitymon"
)

const (
	taskShadowDebugDefaultLimit         = 50
	taskShadowDebugMaxLimit             = 200
	taskShadowDebugMaxRuns              = 100
	taskShadowDuplicateOnsetDetailLimit = 20

	taskShadowReleaseGateVersion    = "task-shadow-release-gate-v7"
	taskShadowReasoningDebugVersion = "task-reasoning-debug-v2"
)

// TaskShadowDebugEpisodeSummary is the bounded, content-safe index record used
// by the internal debugger. Raw frozen context and evidence payloads are only
// represented by hashes and selection metadata.
type TaskShadowDebugEpisodeSummary struct {
	ID                       string   `json:"id"`
	Status                   string   `json:"status"`
	TaskHypothesis           string   `json:"taskHypothesis"`
	DesiredResultHypothesis  string   `json:"desiredResultHypothesis"`
	TaskConfidence           float64  `json:"taskConfidence"`
	BoundaryConfidence       float64  `json:"boundaryConfidence"`
	DesiredResultConfidence  float64  `json:"desiredResultConfidence"`
	InferredStartAt          int64    `json:"inferredStartAt"`
	DetectedAt               int64    `json:"detectedAt"`
	ContextSnapshotID        string   `json:"contextSnapshotId"`
	ContextHash              string   `json:"contextHash"`
	ContextIncludedCount     int      `json:"contextIncludedCount"`
	ContextExcludedCount     int      `json:"contextExcludedCount"`
	LinkedShadowRunIDs       []string `json:"linkedShadowRunIds"`
	LinkedShadowRunCount     int      `json:"linkedShadowRunCount"`
	TaskAgentAttemptCount    int      `json:"taskAgentAttemptCount"`
	PersistedEvaluationCount int      `json:"persistedEvaluationCount"`
	LatestPairWorkflow       string   `json:"latestPairWorkflow,omitempty"`
	LatestPairState          string   `json:"latestPairState,omitempty"`
	LatestPairReasons        []string `json:"latestPairReasons"`
	CreatedAt                int64    `json:"createdAt"`
}

type TaskShadowDebugSnapshot struct {
	GeneratedAt       int64                                   `json:"generatedAt"`
	Limit             int                                     `json:"limit"`
	EnabledWorkflows  []string                                `json:"enabledWorkflows"`
	ReasoningVersions TaskShadowDebugReasoningCurrentVersions `json:"reasoningVersions"`
	LifecycleHealth   TaskShadowLifecycleHealth               `json:"lifecycleHealth"`
	ReleaseGate       TaskShadowReleaseGate                   `json:"releaseGate"`
	Continuation      TaskShadowContinuationSnapshot          `json:"continuation"`
	QualityCampaign   TaskShadowQualityCampaign               `json:"qualityCampaign"`
	Episodes          []TaskShadowDebugEpisodeSummary         `json:"episodes"`
}

type TaskShadowLifecycleAttemptHealth struct {
	ID            string `json:"id"`
	Status        string `json:"status"`
	Model         string `json:"model"`
	PromptVersion string `json:"promptVersion"`
	StartedAt     int64  `json:"startedAt"`
	CompletedAt   int64  `json:"completedAt"`
}

type TaskShadowDuplicateOnsetCluster struct {
	SourceKind      string   `json:"sourceKind"`
	SourceID        string   `json:"sourceId"`
	EvidenceRef     string   `json:"evidenceRef"`
	ObservedAt      int64    `json:"observedAt"`
	InferredStartAt int64    `json:"inferredStartAt"`
	EpisodeIDs      []string `json:"episodeIds"`
}

type TaskShadowLifecycleHealth struct {
	PolicyVersion                   string                            `json:"policyVersion"`
	PromptVersion                   string                            `json:"promptVersion"`
	ShadowPromptVersion             string                            `json:"shadowPromptVersion"`
	CandidateHorizonMs              int64                             `json:"candidateHorizonMs"`
	ActiveHorizonMs                 int64                             `json:"activeHorizonMs"`
	PausedHorizonMs                 int64                             `json:"pausedHorizonMs"`
	EvidenceRenews                  bool                              `json:"evidenceRenews"`
	ActivitySourcePolicy            string                            `json:"activitySourcePolicy"`
	EpisodeCount                    int                               `json:"episodeCount"`
	OpenCount                       int                               `json:"openCount"`
	OverdueOpenCount                int                               `json:"overdueOpenCount"`
	MaterializedResultEpisodeCount  int                               `json:"materializedResultEpisodeCount"`
	StatusCounts                    map[string]int                    `json:"statusCounts"`
	PairStateCounts                 map[string]int                    `json:"pairStateCounts"`
	DuplicateOnsetClusterCount      int                               `json:"duplicateOnsetClusterCount"`
	DuplicateOnsetEpisodeCount      int                               `json:"duplicateOnsetEpisodeCount"`
	DuplicateOnsetClusters          []TaskShadowDuplicateOnsetCluster `json:"duplicateOnsetClusters"`
	DuplicateOnsetClustersTruncated bool                              `json:"duplicateOnsetClustersTruncated"`
	LatestAttempt                   *TaskShadowLifecycleAttemptHealth `json:"latestAttempt,omitempty"`
	GeneratedAt                     int64                             `json:"generatedAt"`
}

type TaskShadowReleaseGateStep struct {
	Key        string `json:"key"`
	Label      string `json:"label"`
	Complete   bool   `json:"complete"`
	EvidenceID string `json:"evidenceId,omitempty"`
}

type TaskShadowReleaseGate struct {
	Version               string                                       `json:"version"`
	Stage                 string                                       `json:"stage"`
	Complete              bool                                         `json:"complete"`
	CanObserveNaturalTask bool                                         `json:"canObserveNaturalTask"`
	Monitoring            bool                                         `json:"monitoring"`
	ForegroundApp         string                                       `json:"foregroundApp,omitempty"`
	ForegroundBundleID    string                                       `json:"foregroundBundleId,omitempty"`
	LatestTaskEpisodeID   string                                       `json:"latestTaskEpisodeId,omitempty"`
	LatestShadowRunID     string                                       `json:"latestShadowRunId,omitempty"`
	LatestPairState       string                                       `json:"latestPairState,omitempty"`
	SelectedWorkflow      string                                       `json:"selectedWorkflow,omitempty"`
	RequiredShadowPrompt  string                                       `json:"requiredShadowPrompt"`
	SelectedShadowPrompt  string                                       `json:"selectedShadowPrompt,omitempty"`
	CurrentGeneration     bool                                         `json:"currentGeneration"`
	HistoricalShadowRuns  []string                                     `json:"historicalShadowRunIds"`
	ObserverProvenance    TaskShadowReleaseObserverProvenance          `json:"observerProvenance"`
	LearningResolution    *ShadowTaskLearningMaterializationResolution `json:"learningResolution,omitempty"`
	RequiredAction        string                                       `json:"requiredAction"`
	Reasons               []string                                     `json:"reasons"`
	Steps                 []TaskShadowReleaseGateStep                  `json:"steps"`
}

type TaskShadowDebugFrozenContext struct {
	ID                    string                     `json:"id"`
	TaskEpisodeID         string                     `json:"taskEpisodeId"`
	CutoffAt              int64                      `json:"cutoffAt"`
	SHA256                string                     `json:"sha256"`
	Manifest              []TaskContextManifestEntry `json:"manifest"`
	ManifestEntryCount    int                        `json:"manifestEntryCount"`
	ManifestIncludedCount int                        `json:"manifestIncludedCount"`
	ManifestExcludedCount int                        `json:"manifestExcludedCount"`
	ManifestTruncated     bool                       `json:"manifestTruncated"`
	CanonicalJSONRedacted bool                       `json:"canonicalJsonRedacted"`
	CreatedAt             int64                      `json:"createdAt"`
}

type TaskShadowDebugEvidence struct {
	ID              string `json:"id"`
	TaskEpisodeID   string `json:"taskEpisodeId"`
	Role            string `json:"role"`
	SourceKind      string `json:"sourceKind"`
	SourceID        string `json:"sourceId"`
	Summary         string `json:"summary,omitempty"`
	ContentHash     string `json:"contentHash"`
	PayloadRedacted bool   `json:"payloadRedacted"`
	ObservedAt      int64  `json:"observedAt"`
	CreatedAt       int64  `json:"createdAt"`
}

type TaskShadowDebugResultDestination struct {
	Status             string   `json:"status"`
	ValidationReason   string   `json:"validationReason"`
	Current            bool     `json:"current"`
	Valid              bool     `json:"valid"`
	Workflow           string   `json:"workflow,omitempty"`
	ResultSHA256       string   `json:"resultSha256,omitempty"`
	DestinationSHA256  string   `json:"destinationSha256,omitempty"`
	Version            string   `json:"version,omitempty"`
	AppBundleID        string   `json:"appBundleId,omitempty"`
	AppName            string   `json:"appName,omitempty"`
	WindowTitle        string   `json:"windowTitle,omitempty"`
	SemanticRole       string   `json:"semanticRole,omitempty"`
	CaptureMethod      string   `json:"captureMethod,omitempty"`
	EvidenceIDs        []string `json:"evidenceIds"`
	SourceEvidenceRefs []string `json:"sourceEvidenceRefs"`
	LatestObservedAt   int64    `json:"latestObservedAt"`
}

type TaskShadowDebugEpisode struct {
	ID                string                        `json:"id"`
	InferredStartAt   int64                         `json:"inferredStartAt"`
	DetectedAt        int64                         `json:"detectedAt"`
	CurrentStatus     string                        `json:"currentStatus"`
	ObservationLease  TaskLifecycleObservationLease `json:"observationLease"`
	CreatePayloadHash string                        `json:"createPayloadHash"`
	FrozenContext     TaskShadowDebugFrozenContext  `json:"frozenContext"`
	LatestHypothesis  TaskEpisodeHypothesisRevision `json:"latestHypothesis"`
	CreatedAt         int64                         `json:"createdAt"`
}

type TaskShadowDebugEpisodeDetail struct {
	Episode            TaskShadowDebugEpisode             `json:"episode"`
	Hypotheses         []TaskEpisodeHypothesisRevision    `json:"hypotheses"`
	Evidence           []TaskShadowDebugEvidence          `json:"evidence"`
	ResultDestinations []TaskShadowDebugResultDestination `json:"resultDestinations"`
	Transitions        []TaskEpisodeTransition            `json:"transitions"`
	Links              []TaskEpisodeLink                  `json:"links"`
}

type TaskShadowDebugProductionRollout struct {
	Workflow         string   `json:"workflow"`
	EnabledWorkflows []string `json:"enabledWorkflows"`
	State            string   `json:"state"`
	Eligible         bool     `json:"eligible"`
	Reason           string   `json:"reason"`
}

// TaskShadowDebugExecutionAttempt keeps execution decisions and actions while
// suppressing duplicated production context, assistant output, and file
// contents. Artifact path, size, and digest remain available for diagnosis.
type TaskShadowDebugExecutionAttempt struct {
	Attempt                       ShadowExecutionAttempt `json:"attempt"`
	PromptRedacted                bool                   `json:"promptRedacted"`
	AssistantOutputRedacted       bool                   `json:"assistantOutputRedacted"`
	ArtifactContentsRedactedCount int                    `json:"artifactContentsRedactedCount"`
}

// TaskShadowDebugEvaluation preserves the persisted comparison result but
// removes duplicate model IO. The task-agent attempt ledger below is the exact
// prompt/raw/error source of truth.
type TaskShadowDebugEvaluation struct {
	Evaluation                 ShadowRunEvaluation `json:"evaluation"`
	ComparatorPromptRedacted   bool                `json:"comparatorPromptRedacted"`
	EvaluatorRawRedacted       bool                `json:"evaluatorRawRedacted"`
	ShadowResultRedacted       bool                `json:"shadowResultRedacted"`
	ShadowArtifactDataRedacted bool                `json:"shadowArtifactDataRedacted"`
}

type TaskShadowDebugReasoningCurrentVersions struct {
	OnsetPrompt       string `json:"onsetPrompt"`
	LifecyclePrompt   string `json:"lifecyclePrompt"`
	ShadowPrompt      string `json:"shadowPrompt"`
	ResultDestination string `json:"resultDestination"`
	EvaluationInput   string `json:"evaluationInput"`
	EvaluationOutput  string `json:"evaluationOutput"`
	Comparator        string `json:"comparator"`
	Evaluator         string `json:"evaluator"`
}

type TaskShadowDebugReasoningVersionCount struct {
	Version string `json:"version"`
	Count   int    `json:"count"`
	Current bool   `json:"current"`
}

type TaskShadowDebugReasoningVersionLineage struct {
	Current           TaskShadowDebugReasoningCurrentVersions `json:"current"`
	OnsetPrompts      []TaskShadowDebugReasoningVersionCount  `json:"onsetPrompts"`
	LifecyclePrompts  []TaskShadowDebugReasoningVersionCount  `json:"lifecyclePrompts"`
	ShadowPrompts     []TaskShadowDebugReasoningVersionCount  `json:"shadowPrompts"`
	EvaluationInputs  []TaskShadowDebugReasoningVersionCount  `json:"evaluationInputs"`
	EvaluationOutputs []TaskShadowDebugReasoningVersionCount  `json:"evaluationOutputs"`
	Comparators       []TaskShadowDebugReasoningVersionCount  `json:"comparators"`
	Evaluators        []TaskShadowDebugReasoningVersionCount  `json:"evaluators"`
}

// TaskShadowDebugReasoningHypothesis remains prediction material. Validation
// only checks that its persisted provenance is internally resolvable; it does
// not turn the hypothesis into an observed fact about the user.
type TaskShadowDebugReasoningHypothesis struct {
	Statement          string   `json:"statement"`
	Confidence         float64  `json:"confidence"`
	Basis              string   `json:"basis"`
	SourceQuote        string   `json:"sourceQuote,omitempty"`
	EvidenceRefs       []string `json:"evidenceRefs"`
	ProfileRefs        []string `json:"profileRefs"`
	ContradictionRefs  []string `json:"contradictionRefs"`
	QuoteValidation    string   `json:"quoteValidation"`
	ProvenanceResolved bool     `json:"provenanceResolved"`
	ValidationReason   string   `json:"validationReason,omitempty"`
}

type TaskShadowDebugReasoningPrediction struct {
	ShadowRunID   string                               `json:"shadowRunId"`
	PromptVersion string                               `json:"promptVersion"`
	Current       bool                                 `json:"current"`
	Hypotheses    []TaskShadowDebugReasoningHypothesis `json:"hypotheses"`
}

type TaskShadowDebugObservedReasoning struct {
	EvidenceID       string   `json:"evidenceId"`
	Kind             string   `json:"kind"`
	Version          string   `json:"version,omitempty"`
	SourceKind       string   `json:"sourceKind"`
	SourceID         string   `json:"sourceId"`
	SourceQuote      string   `json:"sourceQuote,omitempty"`
	EvidenceRefs     []string `json:"evidenceRefs"`
	ObservedAt       int64    `json:"observedAt"`
	Valid            bool     `json:"valid"`
	ValidationReason string   `json:"validationReason"`
}

type TaskShadowDebugReasoningComparison struct {
	EvaluationID            string                               `json:"evaluationId"`
	ShadowRunID             string                               `json:"shadowRunId"`
	ComparatorVersion       string                               `json:"comparatorVersion"`
	InputVersion            string                               `json:"inputVersion,omitempty"`
	OutputVersion           string                               `json:"outputVersion,omitempty"`
	EvaluatorVersion        string                               `json:"evaluatorVersion,omitempty"`
	EvaluatorModel          string                               `json:"evaluatorModel"`
	Current                 bool                                 `json:"current"`
	PredictionBound         bool                                 `json:"predictionBound"`
	PredictionBindingReason string                               `json:"predictionBindingReason"`
	PredictedHypotheses     []TaskShadowDebugReasoningHypothesis `json:"predictedHypotheses"`
	SupportedEvidenceRefs   []string                             `json:"supportedEvidenceRefs"`
	Assessment              string                               `json:"assessment"`
	Score                   *float64                             `json:"score"`
	Confidence              float64                              `json:"confidence"`
	Reason                  string                               `json:"reason"`
	EvidenceRefs            []string                             `json:"evidenceRefs"`
	CitationsValid          bool                                 `json:"citationsValid"`
	Unknown                 bool                                 `json:"unknown"`
}

type TaskShadowDebugReasoningCounts struct {
	OnsetHypotheses          int `json:"onsetHypotheses"`
	ShadowHypotheses         int `json:"shadowHypotheses"`
	ObservedReasoning        int `json:"observedReasoning"`
	InvalidObservedReasoning int `json:"invalidObservedReasoning"`
	Comparisons              int `json:"comparisons"`
	KnownComparisons         int `json:"knownComparisons"`
	UnknownComparisons       int `json:"unknownComparisons"`
}

type TaskShadowDebugReasoning struct {
	Version         string                                 `json:"version"`
	Versions        TaskShadowDebugReasoningVersionLineage `json:"versions"`
	OnsetHypotheses []TaskShadowDebugReasoningHypothesis   `json:"onsetHypotheses"`
	Predictions     []TaskShadowDebugReasoningPrediction   `json:"predictions"`
	Observed        []TaskShadowDebugObservedReasoning     `json:"observed"`
	Comparisons     []TaskShadowDebugReasoningComparison   `json:"comparisons"`
	Counts          TaskShadowDebugReasoningCounts         `json:"counts"`
	Coverage        string                                 `json:"coverage"`
}

type TaskShadowDebugRun struct {
	Run                   ShadowRun                         `json:"run"`
	Status                string                            `json:"status"`
	PairReadiness         ShadowTaskPairReadiness           `json:"pairReadiness"`
	Evidence              []ShadowRunEvidence               `json:"evidence"`
	Observations          []ShadowRunObservation            `json:"observations"`
	ExecutionAttempts     []TaskShadowDebugExecutionAttempt `json:"executionAttempts"`
	Evaluations           []TaskShadowDebugEvaluation       `json:"evaluations"`
	ContextPromptRedacted bool                              `json:"contextPromptRedacted"`
	ExecutionPlanRedacted bool                              `json:"executionPlanRedacted"`
}

type TaskShadowDebugLearning struct {
	Runs             []ShadowTaskLearningRun             `json:"runs"`
	Revisions        []ShadowTaskLearningRevision        `json:"revisions"`
	Materializations []ShadowTaskLearningMaterialization `json:"materializations"`
}

type TaskShadowDebugCorrelationIDs struct {
	TaskEpisodeID       string   `json:"taskEpisodeId"`
	ContextSnapshotID   string   `json:"contextSnapshotId"`
	ContextHash         string   `json:"contextHash"`
	ContextRefs         []string `json:"contextRefs"`
	ShadowRunIDs        []string `json:"shadowRunIds"`
	ExecutionAttemptIDs []string `json:"executionAttemptIds"`
	ObservationIDs      []string `json:"observationIds"`
	EvaluationIDs       []string `json:"evaluationIds"`
	TaskAgentAttemptIDs []string `json:"taskAgentAttemptIds"`
	LearningRunIDs      []string `json:"learningRunIds"`
	LearningRevisionIDs []string `json:"learningRevisionIds"`
	MaterializationIDs  []string `json:"materializationIds"`
}

type TaskShadowDebugExclusion struct {
	Stage         string `json:"stage"`
	CorrelationID string `json:"correlationId"`
	SourceKind    string `json:"sourceKind,omitempty"`
	SourceID      string `json:"sourceId,omitempty"`
	Reason        string `json:"reason"`
}

type TaskShadowDebugDetail struct {
	GeneratedAt               int64                            `json:"generatedAt"`
	Limit                     int                              `json:"limit"`
	SelectedShadowRunID       string                           `json:"selectedShadowRunId,omitempty"`
	ProductionRollout         TaskShadowDebugProductionRollout `json:"productionRollout"`
	Episode                   TaskShadowDebugEpisodeDetail     `json:"episode"`
	Runs                      []TaskShadowDebugRun             `json:"runs"`
	Reasoning                 TaskShadowDebugReasoning         `json:"reasoning"`
	TaskAgentAttempts         []TaskAgentAttempt               `json:"taskAgentAttempts"`
	Learning                  TaskShadowDebugLearning          `json:"learning"`
	Correlations              TaskShadowDebugCorrelationIDs    `json:"correlations"`
	Exclusions                []TaskShadowDebugExclusion       `json:"exclusions"`
	releaseObserverProvenance TaskShadowReleaseObserverProvenance
	releaseLearningRunID      string
	releaseLearningResolution *ShadowTaskLearningMaterializationResolution
}

func init() {
	// Browser preview uses the same localhost-only, explicit method allowlist as
	// the existing Shadow debugger. No mutation endpoint is registered here.
	browserRPCAllowlist["GetTaskShadowDebugSnapshot"] = true
	browserRPCAllowlist["GetTaskShadowDebugDetail"] = true
}

func normalizeTaskShadowDebugLimit(limit int) int {
	switch {
	case limit <= 0:
		return taskShadowDebugDefaultLimit
	case limit > taskShadowDebugMaxLimit:
		return taskShadowDebugMaxLimit
	default:
		return limit
	}
}

func taskShadowDebugStore(app *App) (*ActivityStore, error) {
	if app == nil || app.activity == nil || app.activity.store == nil ||
		app.activity.store.db == nil {
		return nil, errors.New("task shadow debugger requires an initialized activity store")
	}
	return app.activity.store, nil
}

func taskShadowDebugFirst[T any](items []T, limit int) []T {
	if len(items) == 0 {
		return []T{}
	}
	if limit > len(items) {
		limit = len(items)
	}
	result := make([]T, limit)
	copy(result, items[:limit])
	return result
}

type taskShadowLifecycleEpisodeFact struct {
	ID                      string
	InferredStartAt         int64
	DetectedAt              int64
	CurrentStatus           string
	TaskHypothesis          string
	DesiredResultHypothesis string
	LeaseAnchorAt           int64
}

func taskShadowLifecycleEpisodeFacts(
	store *ActivityStore,
) ([]taskShadowLifecycleEpisodeFact, error) {
	rows, err := store.db.Query(`
		SELECT
			episode.id,
			episode.inferred_start_at,
			episode.detected_at,
			current_transition.to_status,
			hypothesis.task_hypothesis,
			hypothesis.desired_result_hypothesis,
			MAX(episode.inferred_start_at, episode.detected_at,
				current_transition.occurred_at)
		FROM task_episodes episode
		JOIN task_episode_transitions current_transition
			ON current_transition.task_episode_id = episode.id
			AND current_transition.sequence = (
				SELECT MAX(candidate.sequence)
				FROM task_episode_transitions candidate
				WHERE candidate.task_episode_id = episode.id
			)
		JOIN task_episode_hypotheses hypothesis
			ON hypothesis.task_episode_id = episode.id
			AND hypothesis.revision = (
				SELECT MAX(candidate.revision)
				FROM task_episode_hypotheses candidate
				WHERE candidate.task_episode_id = episode.id
			)
		ORDER BY episode.inferred_start_at ASC,
			episode.detected_at ASC, episode.id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	facts := make([]taskShadowLifecycleEpisodeFact, 0)
	for rows.Next() {
		var fact taskShadowLifecycleEpisodeFact
		if err := rows.Scan(
			&fact.ID,
			&fact.InferredStartAt,
			&fact.DetectedAt,
			&fact.CurrentStatus,
			&fact.TaskHypothesis,
			&fact.DesiredResultHypothesis,
			&fact.LeaseAnchorAt,
		); err != nil {
			return nil, err
		}
		facts = append(facts, fact)
	}
	return facts, rows.Err()
}

func taskShadowLifecycleResultEvidence(
	store *ActivityStore,
) (map[string][]TaskEpisodeEvidence, error) {
	rows, err := store.db.Query(`
		SELECT id, task_episode_id, role, source_kind, source_id, summary,
			payload_json, content_hash, observed_at, created_at
		FROM task_episode_evidence
		WHERE role = ?
		ORDER BY task_episode_id ASC, observed_at ASC, created_at ASC, id ASC`,
		TaskEpisodeEvidenceResult,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string][]TaskEpisodeEvidence)
	for rows.Next() {
		item, err := scanTaskEpisodeEvidence(rows)
		if err != nil {
			return nil, err
		}
		result[item.TaskEpisodeID] = append(result[item.TaskEpisodeID], item)
	}
	return result, rows.Err()
}

func taskShadowDuplicateOnsetClusters(
	store *ActivityStore,
	limit int,
) ([]TaskShadowDuplicateOnsetCluster, int, int, error) {
	if store == nil || store.db == nil {
		return nil, 0, 0, errors.New("activity store is unavailable")
	}
	if limit <= 0 || limit > taskShadowDuplicateOnsetDetailLimit {
		limit = taskShadowDuplicateOnsetDetailLimit
	}
	index, err := store.taskEpisodeOnsetIdentityIndex(0)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("list exact task onset anchors: %w", err)
	}
	clusters := make([]TaskShadowDuplicateOnsetCluster, 0)
	affectedEpisodes := make(map[string]struct{})
	for _, records := range index.byKey {
		if len(records) < 2 {
			continue
		}
		first := records[0]
		cluster := TaskShadowDuplicateOnsetCluster{
			SourceKind:      first.Anchor.SourceKind,
			SourceID:        first.Anchor.SourceID,
			EvidenceRef:     first.Anchor.Ref,
			ObservedAt:      first.Anchor.ObservedAt,
			InferredStartAt: first.InferredStartAt,
			EpisodeIDs:      make([]string, 0, len(records)),
		}
		for _, record := range records {
			cluster.EpisodeIDs = append(cluster.EpisodeIDs, record.EpisodeID)
			affectedEpisodes[record.EpisodeID] = struct{}{}
		}
		clusters = append(clusters, cluster)
	}
	sort.Slice(clusters, func(i, j int) bool {
		if clusters[i].ObservedAt != clusters[j].ObservedAt {
			return clusters[i].ObservedAt > clusters[j].ObservedAt
		}
		left := strings.Join([]string{
			clusters[i].SourceKind,
			clusters[i].SourceID,
			clusters[i].EvidenceRef,
		}, "\x00")
		right := strings.Join([]string{
			clusters[j].SourceKind,
			clusters[j].SourceID,
			clusters[j].EvidenceRef,
		}, "\x00")
		return left < right
	})
	total := len(clusters)
	if len(clusters) > limit {
		clusters = clusters[:limit]
	}
	return clusters, total, len(affectedEpisodes), nil
}

func taskShadowLifecycleHealth(
	store *ActivityStore,
	generatedAt int64,
	indexedEpisodes []TaskShadowDebugEpisodeSummary,
) (TaskShadowLifecycleHealth, error) {
	statusCounts := map[string]int{
		TaskEpisodeStatusCandidate: 0,
		TaskEpisodeStatusActive:    0,
		TaskEpisodeStatusPaused:    0,
		TaskEpisodeStatusCompleted: 0,
		TaskEpisodeStatusAbandoned: 0,
		TaskEpisodeStatusMerged:    0,
		TaskEpisodeStatusSplit:     0,
		TaskEpisodeStatusExpired:   0,
	}
	health := TaskShadowLifecycleHealth{
		PolicyVersion:          taskLifecycleExpiryPolicyVersion,
		PromptVersion:          taskLifecyclePromptVersion,
		ShadowPromptVersion:    shadowTaskPromptVersion,
		CandidateHorizonMs:     taskLifecycleCandidateHorizon.Milliseconds(),
		ActiveHorizonMs:        taskLifecycleActiveHorizon.Milliseconds(),
		PausedHorizonMs:        taskLifecyclePausedHorizon.Milliseconds(),
		EvidenceRenews:         false,
		ActivitySourcePolicy:   taskLifecycleActivitySourcePolicy,
		StatusCounts:           statusCounts,
		PairStateCounts:        make(map[string]int),
		DuplicateOnsetClusters: []TaskShadowDuplicateOnsetCluster{},
		GeneratedAt:            generatedAt,
	}
	duplicateClusters, duplicateClusterCount, duplicateEpisodeCount, err :=
		taskShadowDuplicateOnsetClusters(
			store,
			taskShadowDuplicateOnsetDetailLimit,
		)
	if err != nil {
		return TaskShadowLifecycleHealth{}, err
	}
	health.DuplicateOnsetClusters = duplicateClusters
	health.DuplicateOnsetClusterCount = duplicateClusterCount
	health.DuplicateOnsetEpisodeCount = duplicateEpisodeCount
	health.DuplicateOnsetClustersTruncated =
		duplicateClusterCount > len(duplicateClusters)
	facts, err := taskShadowLifecycleEpisodeFacts(store)
	if err != nil {
		return TaskShadowLifecycleHealth{}, err
	}
	resultEvidence, err := taskShadowLifecycleResultEvidence(store)
	if err != nil {
		return TaskShadowLifecycleHealth{}, err
	}
	indexedByID := make(
		map[string]TaskShadowDebugEpisodeSummary,
		len(indexedEpisodes),
	)
	for _, episode := range indexedEpisodes {
		indexedByID[episode.ID] = episode
	}
	unresolvedEpisodeIDs := make([]string, 0)
	for _, fact := range facts {
		indexed, ok := indexedByID[fact.ID]
		if ok && (indexed.LinkedShadowRunCount == 0 ||
			indexed.LatestPairState != "readiness_error") {
			continue
		}
		unresolvedEpisodeIDs = append(unresolvedEpisodeIDs, fact.ID)
	}
	unresolvedRuns, _, err := taskShadowDebugIndexRuns(
		store,
		unresolvedEpisodeIDs,
	)
	if err != nil {
		return TaskShadowLifecycleHealth{}, err
	}
	health.EpisodeCount = len(facts)
	for _, fact := range facts {
		status := fact.CurrentStatus
		health.StatusCounts[status]++
		if taskLifecycleEpisodeOpen(status) {
			health.OpenCount++
			horizon, ok := taskLifecycleObservationHorizon(status)
			if ok && fact.LeaseAnchorAt > 0 &&
				generatedAt >= fact.LeaseAnchorAt &&
				generatedAt-fact.LeaseAnchorAt >= horizon.Milliseconds() {
				health.OverdueOpenCount++
			}
		}
		for _, receipt := range taskShadowDebugResultDestinations(
			resultEvidence[fact.ID],
			taskWorkflowCapabilityKey(
				fact.TaskHypothesis,
				fact.DesiredResultHypothesis,
				fact.ID,
			),
			taskShadowDebugMaxLimit,
		) {
			if receipt.Current && receipt.Valid {
				health.MaterializedResultEpisodeCount++
				break
			}
		}
		if indexed, ok := indexedByID[fact.ID]; ok &&
			indexed.LinkedShadowRunCount == 0 {
			continue
		}
		if indexed, ok := indexedByID[fact.ID]; ok &&
			indexed.LatestPairState != "readiness_error" {
			health.PairStateCounts[indexed.LatestPairState]++
			continue
		}
		runs := unresolvedRuns[fact.ID]
		if len(runs) == 0 {
			continue
		}
		pair, err := BuildShadowTaskPairReadiness(
			store,
			runs[len(runs)-1].ID,
		)
		if err != nil {
			return TaskShadowLifecycleHealth{}, err
		}
		health.PairStateCounts[pair.State]++
	}
	attempts, err := store.ListTaskAgentAttempts(TaskAgentAttemptFilter{
		Phase: TaskAgentAttemptPhaseLifecycle,
		Limit: 1,
	})
	if err != nil {
		return TaskShadowLifecycleHealth{}, err
	}
	if len(attempts) > 0 {
		attempt := attempts[0]
		health.LatestAttempt = &TaskShadowLifecycleAttemptHealth{
			ID:            attempt.ID,
			Status:        attempt.Status,
			Model:         attempt.ModelName,
			PromptVersion: attempt.PromptVersion,
			StartedAt:     attempt.StartedAt,
			CompletedAt:   attempt.CompletedAt,
		}
	}
	return health, nil
}

func taskShadowDebugLast[T any](items []T, limit int) []T {
	if len(items) == 0 {
		return []T{}
	}
	if limit > len(items) {
		limit = len(items)
	}
	result := make([]T, limit)
	copy(result, items[len(items)-limit:])
	return result
}

// taskShadowDebugManifest keeps the response bounded while ensuring source
// material that actually entered the frozen context cannot be displaced by a
// long prefix of exclusions. Relative order is preserved within each class.
func taskShadowDebugManifest(
	items []TaskContextManifestEntry,
	limit int,
) []TaskContextManifestEntry {
	if len(items) <= limit {
		return append([]TaskContextManifestEntry(nil), items...)
	}
	result := make([]TaskContextManifestEntry, 0, limit)
	for _, included := range []bool{true, false} {
		for _, item := range items {
			if item.Included != included {
				continue
			}
			result = append(result, item)
			if len(result) == limit {
				return result
			}
		}
	}
	return result
}

func taskShadowDebugSortedIDs(values map[string]struct{}, limit int) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return taskShadowDebugFirst(result, limit)
}

func taskShadowDebugProductionRollout(
	episode TaskEpisode,
) TaskShadowDebugProductionRollout {
	return taskShadowDebugProductionRolloutFields(
		episode.ID,
		episode.LatestHypothesis.TaskHypothesis,
		episode.LatestHypothesis.DesiredResultHypothesis,
		taskEpisodeHasFrozenSourceContext(episode),
	)
}

func taskShadowDebugProductionRolloutFields(
	episodeID, task, desiredResult string,
	hasFrozenSourceContext bool,
) TaskShadowDebugProductionRollout {
	workflow := taskWorkflowCapabilityKey(task, desiredResult, episodeID)
	enabledWorkflows := taskWorkflowProductionShadowWorkflows()
	if !taskWorkflowProductionShadowEligible(workflow) {
		return TaskShadowDebugProductionRollout{
			Workflow:         workflow,
			EnabledWorkflows: enabledWorkflows,
			State:            "outside_production_rollout",
			Eligible:         false,
			Reason: fmt.Sprintf(
				"workflow %q is not enabled for production shadow execution",
				workflow,
			),
		}
	}
	readinessErr := taskWorkflowFrozenContextSafetyError(
		workflow,
		task,
		hasFrozenSourceContext,
	)
	if readinessErr == nil {
		return TaskShadowDebugProductionRollout{
			Workflow:         workflow,
			EnabledWorkflows: enabledWorkflows,
			State:            "eligible",
			Eligible:         true,
			Reason:           "workflow is enabled for the current production shadow rollout",
		}
	}
	if errors.Is(
		readinessErr,
		ErrTaskWorkflowInsufficientFrozenContext,
	) {
		return TaskShadowDebugProductionRollout{
			Workflow:         workflow,
			EnabledWorkflows: enabledWorkflows,
			State:            "insufficient_frozen_context",
			Eligible:         false,
			Reason:           readinessErr.Error(),
		}
	}
	return TaskShadowDebugProductionRollout{
		Workflow:         workflow,
		EnabledWorkflows: enabledWorkflows,
		State:            "readiness_error",
		Eligible:         false,
		Reason:           readinessErr.Error(),
	}
}

func taskShadowReleaseGateSteps() []TaskShadowReleaseGateStep {
	return []TaskShadowReleaseGateStep{
		{Key: "observer_source", Label: "Real activity source found"},
		{Key: "observer_origin", Label: "Task start verified"},
		{Key: "task_episode", Label: "Your task completed"},
		{Key: "shadow_attempt", Label: "Isoform's shadow attempt completed"},
		{Key: "user_result", Label: "Your result captured"},
		{Key: "result_destination", Label: "Your result source verified"},
		{Key: "shadow_result", Label: "Isoform's result captured"},
		{Key: "evaluation", Label: "Results compared across 8 dimensions"},
		{Key: "learning", Label: "Safe learning update recorded"},
	}
}

func taskShadowReleaseGateLocked(appName, bundleID string) bool {
	appName = strings.ToLower(strings.TrimSpace(appName))
	bundleID = strings.ToLower(strings.TrimSpace(bundleID))
	return appName == "loginwindow" ||
		bundleID == "com.apple.loginwindow" ||
		strings.HasSuffix(bundleID, ".loginwindow")
}

func taskShadowReleaseGateSetStep(
	steps []TaskShadowReleaseGateStep,
	key, evidenceID string,
	complete bool,
) {
	for index := range steps {
		if steps[index].Key != key {
			continue
		}
		steps[index].Complete = complete
		if complete {
			steps[index].EvidenceID = strings.TrimSpace(evidenceID)
		}
		return
	}
}

func taskShadowReleaseGateLearningRunID(
	learning TaskShadowDebugLearning,
	evaluationIDs []string,
) string {
	allowed := make(map[string]struct{}, len(evaluationIDs))
	for _, id := range evaluationIDs {
		if id = strings.TrimSpace(id); id != "" {
			allowed[id] = struct{}{}
		}
	}
	if len(allowed) == 0 {
		return ""
	}
	for _, run := range learning.Runs {
		if run.PolicyVersion != ShadowTaskLearningPolicyVersion ||
			run.Projection.PolicyVersion != ShadowTaskLearningPolicyVersion ||
			run.AuthorityEffect != ShadowTaskLearningAuthorityNone ||
			run.Projection.AuthorityEffect != ShadowTaskLearningAuthorityNone {
			continue
		}
		for _, exclusion := range run.Projection.ExcludedEvaluations {
			if _, ok := allowed[strings.TrimSpace(exclusion.EvaluationID)]; ok {
				return run.ID
			}
		}
		for _, candidate := range run.Projection.Candidates {
			for _, item := range candidate.Evidence {
				if _, ok := allowed[item.EvaluationID]; ok {
					return run.ID
				}
			}
			for _, item := range candidate.Dimensions {
				if _, ok := allowed[item.EvaluationID]; ok {
					return run.ID
				}
			}
		}
	}
	return ""
}

func taskShadowReleaseGateWorkflowEnabled(workflow string) bool {
	workflow = strings.TrimSpace(workflow)
	if !taskWorkflowTextResult(workflow) {
		return false
	}
	for _, enabled := range taskWorkflowProductionShadowWorkflows() {
		if enabled == workflow {
			return true
		}
	}
	return false
}

func taskShadowReleaseGateCurrentRun(
	detail *TaskShadowDebugDetail,
) *TaskShadowDebugRun {
	if detail == nil {
		return nil
	}
	for index := 0; index < len(detail.Runs); index++ {
		run := &detail.Runs[index]
		if taskShadowReleaseGateWorkflowEnabled(run.Run.CapabilityKey) &&
			shadowTaskRunUsesCurrentGeneration(run.Run) {
			return run
		}
	}
	return nil
}

func taskShadowReleaseGateHistoricalRuns(
	detail *TaskShadowDebugDetail,
) []string {
	if detail == nil {
		return []string{}
	}
	result := make([]string, 0)
	for _, run := range detail.Runs {
		if !taskShadowReleaseGateWorkflowEnabled(run.Run.CapabilityKey) ||
			shadowTaskRunUsesCurrentGeneration(run.Run) {
			continue
		}
		result = append(result, run.Run.ID)
	}
	return uniqueSortedStrings(result)
}

func taskShadowReleaseGateFromDetail(
	detail *TaskShadowDebugDetail,
	monitoring bool,
	foreground activitymon.AppInfo,
) TaskShadowReleaseGate {
	result := TaskShadowReleaseGate{
		Version:              taskShadowReleaseGateVersion,
		Stage:                "waiting_for_natural_task",
		Monitoring:           monitoring,
		ForegroundApp:        strings.TrimSpace(foreground.AppName),
		ForegroundBundleID:   strings.TrimSpace(foreground.BundleID),
		RequiredShadowPrompt: shadowTaskPromptVersion,
		HistoricalShadowRuns: []string{},
		ObserverProvenance:   newTaskShadowReleaseObserverProvenance(""),
		RequiredAction: "Complete one ordinary direct-text task in an " +
			"observed writing surface.",
		Reasons: []string{},
		Steps:   taskShadowReleaseGateSteps(),
	}

	if detail != nil {
		currentEvaluationIDs := []string{}
		result.LatestTaskEpisodeID = detail.Episode.Episode.ID
		result.ObserverProvenance = detail.releaseObserverProvenance
		if result.ObserverProvenance.Version == "" {
			result.ObserverProvenance =
				newTaskShadowReleaseObserverProvenance(
					detail.Episode.Episode.ID,
				)
		}
		taskShadowReleaseGateSetStep(
			result.Steps,
			"observer_source",
			result.ObserverProvenance.TraceID,
			result.ObserverProvenance.SourceComplete,
		)
		taskShadowReleaseGateSetStep(
			result.Steps,
			"observer_origin",
			result.ObserverProvenance.ProductionOnsetAttemptID,
			result.ObserverProvenance.OriginComplete,
		)
		if !result.ObserverProvenance.OriginComplete &&
			result.ObserverProvenance.Reason != "" {
			result.Reasons = append(
				result.Reasons,
				result.ObserverProvenance.Reason,
			)
		}
		result.SelectedWorkflow = detail.ProductionRollout.Workflow
		result.HistoricalShadowRuns =
			taskShadowReleaseGateHistoricalRuns(detail)
		if run := taskShadowReleaseGateCurrentRun(detail); run != nil {
			result.CurrentGeneration = true
			result.SelectedWorkflow = run.Run.CapabilityKey
			result.SelectedShadowPrompt = run.Run.PromptVersion
			result.LatestShadowRunID = run.Run.ID
			result.LatestPairState = run.PairReadiness.State
			result.Reasons = append(
				result.Reasons,
				run.PairReadiness.Reasons...,
			)
			taskShadowReleaseGateSetStep(
				result.Steps,
				"task_episode",
				detail.Episode.Episode.ID,
				detail.Episode.Episode.CurrentStatus ==
					TaskEpisodeStatusCompleted,
			)
			executionID := run.Run.ID
			if len(run.ExecutionAttempts) > 0 {
				executionID =
					run.ExecutionAttempts[0].Attempt.ID
			}
			taskShadowReleaseGateSetStep(
				result.Steps,
				"shadow_attempt",
				executionID,
				run.PairReadiness.ShadowAttemptStatus == "completed",
			)
			taskShadowReleaseGateSetStep(
				result.Steps,
				"user_result",
				run.PairReadiness.UserResultSHA256,
				run.PairReadiness.UserResultMaterialized,
			)
			taskShadowReleaseGateSetStep(
				result.Steps,
				"result_destination",
				run.PairReadiness.UserResultDestinationSHA256,
				run.PairReadiness.UserResultMaterialized &&
					run.PairReadiness.UserResultDestinationSHA256 != "",
			)
			taskShadowReleaseGateSetStep(
				result.Steps,
				"shadow_result",
				run.PairReadiness.ShadowResultSHA256,
				run.PairReadiness.ShadowResultMaterialized,
			)
			currentEvaluationIDs = append(
				currentEvaluationIDs,
				run.PairReadiness.CurrentEvaluationIDs...,
			)
			if len(currentEvaluationIDs) > 0 {
				taskShadowReleaseGateSetStep(
					result.Steps,
					"evaluation",
					currentEvaluationIDs[0],
					true,
				)
			}
		} else if len(result.HistoricalShadowRuns) > 0 {
			result.Reasons = append(
				result.Reasons,
				"historical_shadow_prompt_generation_only",
			)
		}
		learningRunID := ""
		if len(currentEvaluationIDs) > 0 {
			learningRunID = strings.TrimSpace(detail.releaseLearningRunID)
			if learningRunID == "" {
				learningRunID = taskShadowReleaseGateLearningRunID(
					detail.Learning,
					currentEvaluationIDs,
				)
			}
			if detail.releaseLearningResolution != nil &&
				detail.releaseLearningResolution.RunID == learningRunID {
				result.LearningResolution = detail.releaseLearningResolution
			}
		}
		if learningRunID != "" && result.LearningResolution != nil {
			taskShadowReleaseGateSetStep(
				result.Steps,
				"learning",
				learningRunID,
				result.LearningResolution.Complete,
			)
		}
	}

	result.Complete = true
	for _, step := range result.Steps {
		if !step.Complete {
			result.Complete = false
			break
		}
	}
	if result.Complete {
		result.Stage = "complete"
		result.RequiredAction = "Natural production parity evidence is complete."
		return result
	}
	if !monitoring {
		result.Stage = "observer_paused"
		result.RequiredAction = "Enable observation before starting the task."
		result.Reasons = append(
			result.Reasons,
			"activity observation is not running",
		)
		return result
	}
	if taskShadowReleaseGateLocked(
		foreground.AppName,
		foreground.BundleID,
	) {
		result.Stage = "desktop_locked"
		result.RequiredAction = "Unlock the desktop, then complete one ordinary " +
			"direct-text task in an observed writing surface."
		result.Reasons = append(
			result.Reasons,
			"the frontmost system surface is loginwindow",
		)
		return result
	}
	result.CanObserveNaturalTask = true
	if detail == nil {
		return result
	}
	if detail.ProductionRollout.State == "insufficient_frozen_context" {
		result.Stage = "frozen_context_insufficient"
		result.RequiredAction = "Continue working normally. This episode remains " +
			"observation-only while Isoform uses bounded background learning; the next " +
			"source-backed direct-text task can produce parity evidence."
		result.Reasons = append(
			result.Reasons,
			detail.ProductionRollout.Reason,
		)
		return result
	}
	if !result.CurrentGeneration {
		if len(result.HistoricalShadowRuns) > 0 {
			result.Stage = "waiting_for_current_shadow_generation"
			result.RequiredAction = "Continue working normally. Retained shadow " +
				"attempts are historical; the next eligible direct-text task will create " +
				"current-generation parity evidence."
			return result
		}
		if detail.Episode.Episode.CurrentStatus != TaskEpisodeStatusCompleted {
			result.Stage = "task_episode_pending"
			result.RequiredAction = "Finish the observed direct-text task with a " +
				"source-backed send, submit, publish, or explicit completion."
			return result
		}
		result.Stage = "shadow_attempt_pending"
		result.RequiredAction = "Wait for or inspect why the current isolated " +
			"shadow attempt was not created."
		return result
	}
	for _, step := range result.Steps {
		if step.Complete {
			continue
		}
		switch step.Key {
		case "observer_source":
			result.Stage = "observer_source_pending"
			result.RequiredAction = "Continue working normally. The next clean " +
				"instrumented direct-text task can establish exact source provenance."
		case "observer_origin":
			result.Stage = "observer_origin_pending"
			result.RequiredAction = "Inspect the linked Observer trace, then wait " +
				"for a later ordinary task if its onset binding is incomplete."
		case "task_episode":
			result.Stage = "task_episode_pending"
			result.RequiredAction = "Finish the observed direct-text task with a " +
				"source-backed send, submit, publish, or explicit completion."
		case "shadow_attempt":
			result.Stage = "shadow_attempt_pending"
			result.RequiredAction = "Inspect the isolated shadow attempt and its " +
				"failure reason."
		case "user_result":
			result.Stage = "user_result_pending"
			result.RequiredAction = "Complete the direct-text task through a " +
				"source-backed send, submit, publish, or explicit completion."
		case "result_destination":
			result.Stage = "result_destination_pending"
			result.RequiredAction = "Inspect why the observed result lacks a " +
				"current verified destination receipt."
		case "shadow_result":
			result.Stage = "shadow_result_pending"
			result.RequiredAction = "Inspect why the isolated run did not produce " +
				"a complete text artifact."
		case "evaluation":
			result.Stage = "evaluation_pending"
			result.RequiredAction = "Wait for or inspect the deterministic pair " +
				"readiness and evaluator attempt."
		case "learning":
			if result.LearningResolution != nil &&
				result.LearningResolution.InvalidCount > 0 {
				result.Stage = "learning_materialization_invalid"
				result.RequiredAction = "Inspect the conflicting immutable learning materialization receipts."
			} else if result.LearningResolution != nil {
				result.Stage = "learning_materialization_pending"
				result.RequiredAction = "Wait for deterministic local learning materialization to recover."
			} else {
				result.Stage = "learning_pending"
				result.RequiredAction = "Inspect the evaluated-only learning projection."
			}
		}
		break
	}
	return result
}

func taskShadowDebugObserverState(a *App) (bool, activitymon.AppInfo) {
	if a == nil || a.activity == nil || a.activity.monitor == nil {
		return false, activitymon.AppInfo{}
	}
	monitoring := a.activity.monitor.IsRunning()
	if !monitoring {
		return false, activitymon.AppInfo{}
	}
	return true, activitymon.FrontmostAppInfo()
}

func taskShadowReleaseGateDetail(
	a *App,
	episodes []taskShadowDebugEpisodeIndexRow,
	runsByEpisode map[string][]taskShadowDebugRunIndexRow,
	evaluationCounts map[string]int,
	limit int,
) (*TaskShadowDebugDetail, error) {
	store, err := taskShadowDebugStore(a)
	if err != nil {
		return nil, err
	}
	repeatedOnsets, err := store.taskEpisodeRepeatedOnsetSet()
	if err != nil {
		return nil, err
	}
	selection := taskShadowReleaseGateIndexSelectionExcluding(
		episodes,
		runsByEpisode,
		evaluationCounts,
		repeatedOnsets,
	)
	if selection.FirstEligibleEpisodeID == "" {
		return nil, nil
	}
	observerCorpus, err := loadTaskShadowReleaseObserverCorpus(
		store,
		time.Now().UnixMilli(),
	)
	if err != nil {
		return nil, err
	}
	learningRuns, err := store.ListShadowTaskLearningRuns(
		shadowTaskLearningListMax,
	)
	if err != nil {
		return nil, err
	}
	learningByEvaluation := taskShadowContinuationLearningIndex(learningRuns)
	loaded := make(map[string]TaskShadowDebugDetail)
	loadDetail := func(episodeID string) (TaskShadowDebugDetail, error) {
		if detail, ok := loaded[episodeID]; ok {
			return detail, nil
		}
		detail, err := a.GetTaskShadowDebugDetail(
			episodeID,
			"",
			limit,
		)
		if err != nil {
			return TaskShadowDebugDetail{}, err
		}
		detail.releaseObserverProvenance =
			buildTaskShadowReleaseObserverProvenance(
				&detail,
				observerCorpus,
			)
		if run := taskShadowReleaseGateCurrentRun(&detail); run != nil {
			for _, evaluationID := range run.PairReadiness.CurrentEvaluationIDs {
				if learningRunID := learningByEvaluation[evaluationID]; learningRunID != "" {
					detail.releaseLearningRunID = learningRunID
					resolution, resolutionErr := BuildShadowTaskLearningMaterializationResolution(
						store,
						learningRunID,
					)
					if resolutionErr != nil {
						return TaskShadowDebugDetail{}, resolutionErr
					}
					detail.releaseLearningResolution = &resolution
					break
				}
			}
		}
		loaded[episodeID] = detail
		return detail, nil
	}

	for _, episodeID := range selection.PossibleCompleteEpisodeIDs {
		detail, err := loadDetail(episodeID)
		if err != nil {
			return nil, err
		}
		if taskShadowReleaseGateFromDetail(
			&detail,
			false,
			activitymon.AppInfo{},
		).Complete {
			return &detail, nil
		}
	}
	if selection.FirstCurrentEpisodeID != "" {
		detail, err := loadDetail(selection.FirstCurrentEpisodeID)
		if err != nil {
			return nil, err
		}
		return &detail, nil
	}
	detail, err := loadDetail(selection.FirstEligibleEpisodeID)
	if err != nil {
		return nil, err
	}
	return &detail, nil
}

type taskShadowReleaseGateIndexPlan struct {
	PossibleCompleteEpisodeIDs []string
	FirstCurrentEpisodeID      string
	FirstEligibleEpisodeID     string
}

func taskShadowReleaseGateIndexSelectionExcluding(
	episodes []taskShadowDebugEpisodeIndexRow,
	runsByEpisode map[string][]taskShadowDebugRunIndexRow,
	evaluationCounts map[string]int,
	repeatedOnsets map[string]struct{},
) taskShadowReleaseGateIndexPlan {
	result := taskShadowReleaseGateIndexPlan{
		PossibleCompleteEpisodeIDs: []string{},
	}
	for _, indexedEpisode := range episodes {
		episode := indexedEpisode.Summary
		if _, repeated := repeatedOnsets[episode.ID]; repeated {
			continue
		}
		workflow := taskWorkflowCapabilityKey(
			episode.TaskHypothesis,
			episode.DesiredResultHypothesis,
			episode.ID,
		)
		episodeRuns := runsByEpisode[episode.ID]
		if len(episodeRuns) > 0 {
			workflow = episodeRuns[len(episodeRuns)-1].CapabilityKey
		}
		if !taskShadowReleaseGateWorkflowEnabled(
			workflow,
		) {
			continue
		}
		if result.FirstEligibleEpisodeID == "" {
			result.FirstEligibleEpisodeID = episode.ID
		}
		hasCurrentRun := false
		for _, run := range episodeRuns {
			if taskShadowReleaseGateWorkflowEnabled(run.CapabilityKey) &&
				run.PromptVersion == shadowTaskPromptVersion {
				hasCurrentRun = true
				break
			}
		}
		if !hasCurrentRun {
			continue
		}
		if result.FirstCurrentEpisodeID == "" {
			result.FirstCurrentEpisodeID = episode.ID
		}
		if episode.Status == TaskEpisodeStatusCompleted &&
			evaluationCounts[episode.ID] > 0 {
			result.PossibleCompleteEpisodeIDs = append(
				result.PossibleCompleteEpisodeIDs,
				episode.ID,
			)
		}
	}
	return result
}

func listTaskShadowDebugRuns(
	store *ActivityStore,
	taskEpisodeID string,
	limit int,
) ([]ShadowRun, error) {
	if store == nil || store.db == nil {
		return nil, errors.New("activity store is unavailable")
	}
	taskEpisodeID = strings.TrimSpace(taskEpisodeID)
	if taskEpisodeID == "" {
		return nil, errors.New("task episode ID is required")
	}
	if limit <= 0 || limit > taskShadowDebugMaxRuns {
		limit = taskShadowDebugMaxRuns
	}
	rows, err := store.db.Query(`
		SELECT `+shadowRunSelectColumns+`
		FROM shadow_runs
		WHERE mode = 'task_mirror' AND task_episode_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT ?`,
		taskEpisodeID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ShadowRun, 0, limit)
	for rows.Next() {
		item, err := scanShadowRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result, nil
}

type taskShadowDebugEpisodeIndexRow struct {
	Summary                TaskShadowDebugEpisodeSummary
	HasFrozenSourceContext bool
}

type taskShadowDebugRunIndexRow struct {
	ID                    string
	TaskEpisodeID         string
	CapabilityKey         string
	PromptVersion         string
	ObservationHorizonEnd int64
	CreatedAt             int64
}

type taskShadowDebugContextIndexStats struct {
	IncludedCount          int
	ExcludedCount          int
	HasFrozenSourceContext bool
}

func taskShadowDebugContextStats(
	store *ActivityStore,
	episodeIDs []string,
) (map[string]taskShadowDebugContextIndexStats, error) {
	result := make(map[string]taskShadowDebugContextIndexStats)
	if len(episodeIDs) == 0 {
		return result, nil
	}
	placeholders, args := taskShadowDebugEpisodeIDQuery(episodeIDs)
	rows, err := store.db.Query(`
		SELECT task_episode_id, included_count, excluded_count,
			frozen_source_context_count
		FROM task_context_manifest_stats_v1
		WHERE task_episode_id IN (`+placeholders+`)
		ORDER BY task_episode_id ASC`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var episodeID string
		var hasFrozenSourceContext int
		var stats taskShadowDebugContextIndexStats
		if err := rows.Scan(
			&episodeID,
			&stats.IncludedCount,
			&stats.ExcludedCount,
			&hasFrozenSourceContext,
		); err != nil {
			return nil, err
		}
		stats.HasFrozenSourceContext = hasFrozenSourceContext != 0
		result[episodeID] = stats
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, episodeID := range episodeIDs {
		if _, ok := result[episodeID]; !ok {
			return nil, fmt.Errorf(
				"task episode %q has no context manifest stats",
				episodeID,
			)
		}
	}
	return result, nil
}

func taskShadowDebugEpisodeIndexRows(
	store *ActivityStore,
	limit int,
) ([]taskShadowDebugEpisodeIndexRow, error) {
	rows, err := store.db.Query(`
		SELECT
			episode.id,
			current_transition.to_status,
			hypothesis.task_hypothesis,
			hypothesis.desired_result_hypothesis,
			hypothesis.task_confidence,
			hypothesis.boundary_confidence,
			hypothesis.desired_result_confidence,
			episode.inferred_start_at,
			episode.detected_at,
			context.id,
			context.sha256,
			episode.created_at
		FROM task_episodes episode
		JOIN task_episode_context_snapshots context
			ON context.task_episode_id = episode.id
		JOIN task_episode_transitions current_transition
			ON current_transition.task_episode_id = episode.id
			AND current_transition.sequence = (
				SELECT MAX(candidate.sequence)
				FROM task_episode_transitions candidate
				WHERE candidate.task_episode_id = episode.id
			)
		JOIN task_episode_hypotheses hypothesis
			ON hypothesis.task_episode_id = episode.id
			AND hypothesis.revision = (
				SELECT MAX(candidate.revision)
				FROM task_episode_hypotheses candidate
				WHERE candidate.task_episode_id = episode.id
			)
		ORDER BY episode.inferred_start_at DESC,
			episode.detected_at DESC, episode.id ASC
		LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	result := make([]taskShadowDebugEpisodeIndexRow, 0, limit)
	for rows.Next() {
		var item taskShadowDebugEpisodeIndexRow
		if err := rows.Scan(
			&item.Summary.ID,
			&item.Summary.Status,
			&item.Summary.TaskHypothesis,
			&item.Summary.DesiredResultHypothesis,
			&item.Summary.TaskConfidence,
			&item.Summary.BoundaryConfidence,
			&item.Summary.DesiredResultConfidence,
			&item.Summary.InferredStartAt,
			&item.Summary.DetectedAt,
			&item.Summary.ContextSnapshotID,
			&item.Summary.ContextHash,
			&item.Summary.CreatedAt,
		); err != nil {
			rows.Close()
			return nil, err
		}
		item.Summary.LinkedShadowRunIDs = []string{}
		item.Summary.LatestPairReasons = []string{}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	statsByEpisode, err := taskShadowDebugContextStats(
		store,
		taskShadowDebugEpisodeIndexIDs(result),
	)
	if err != nil {
		return nil, err
	}
	for index := range result {
		stats := statsByEpisode[result[index].Summary.ID]
		result[index].Summary.ContextIncludedCount = stats.IncludedCount
		result[index].Summary.ContextExcludedCount = stats.ExcludedCount
		result[index].HasFrozenSourceContext = stats.HasFrozenSourceContext
	}
	return result, nil
}

func taskShadowDebugEpisodeIDQuery(
	episodeIDs []string,
) (string, []any) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(episodeIDs)), ",")
	args := make([]any, 0, len(episodeIDs))
	for _, episodeID := range episodeIDs {
		args = append(args, episodeID)
	}
	return placeholders, args
}

func taskShadowDebugIndexRuns(
	store *ActivityStore,
	episodeIDs []string,
) (map[string][]taskShadowDebugRunIndexRow, map[string]int, error) {
	result := make(map[string][]taskShadowDebugRunIndexRow)
	counts := make(map[string]int)
	if len(episodeIDs) == 0 {
		return result, counts, nil
	}
	placeholders, args := taskShadowDebugEpisodeIDQuery(episodeIDs)
	args = append(args, taskShadowDebugMaxRuns)
	rows, err := store.db.Query(`
		WITH ranked_runs AS (
			SELECT id, task_episode_id, capability_key, prompt_version,
				observation_horizon_end, created_at,
				COUNT(*) OVER (
					PARTITION BY task_episode_id
				) AS total_count,
				ROW_NUMBER() OVER (
					PARTITION BY task_episode_id
					ORDER BY created_at DESC, id DESC
				) AS recency_rank
			FROM shadow_runs
			WHERE mode = 'task_mirror'
				AND task_episode_id IN (`+placeholders+`)
		)
		SELECT id, task_episode_id, capability_key, prompt_version,
			observation_horizon_end, created_at, total_count
		FROM ranked_runs
		WHERE recency_rank <= ?
		ORDER BY task_episode_id ASC, created_at ASC, id ASC`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item taskShadowDebugRunIndexRow
		var totalCount int
		if err := rows.Scan(
			&item.ID,
			&item.TaskEpisodeID,
			&item.CapabilityKey,
			&item.PromptVersion,
			&item.ObservationHorizonEnd,
			&item.CreatedAt,
			&totalCount,
		); err != nil {
			return nil, nil, err
		}
		result[item.TaskEpisodeID] = append(result[item.TaskEpisodeID], item)
		counts[item.TaskEpisodeID] = totalCount
	}
	return result, counts, rows.Err()
}

func taskShadowDebugIndexEvaluations(
	store *ActivityStore,
	episodeIDs []string,
) (map[string]int, error) {
	counts := make(map[string]int)
	if len(episodeIDs) == 0 {
		return counts, nil
	}
	placeholders, args := taskShadowDebugEpisodeIDQuery(episodeIDs)
	rows, err := store.db.Query(`
		SELECT run.task_episode_id, COUNT(*)
		FROM shadow_run_evaluations evaluation
		JOIN shadow_runs run ON run.id = evaluation.shadow_run_id
		WHERE run.mode = 'task_mirror'
			AND run.task_episode_id IN (`+placeholders+`)
		GROUP BY run.task_episode_id
		ORDER BY run.task_episode_id ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var episodeID string
		var count int
		if err := rows.Scan(&episodeID, &count); err != nil {
			return nil, err
		}
		counts[episodeID] = count
	}
	return counts, rows.Err()
}

func taskShadowDebugIndexAttemptCounts(
	store *ActivityStore,
	episodeIDs []string,
) (map[string]int, error) {
	counts := make(map[string]int)
	if len(episodeIDs) == 0 {
		return counts, nil
	}
	placeholders, args := taskShadowDebugEpisodeIDQuery(episodeIDs)
	queryArgs := make([]any, 0, len(args)*3)
	queryArgs = append(queryArgs, args...)
	queryArgs = append(queryArgs, args...)
	queryArgs = append(queryArgs, args...)
	rows, err := store.db.Query(`
		WITH attempt_links AS (
			SELECT id AS attempt_id, task_episode_id AS episode_id
			FROM task_agent_attempts
			WHERE task_episode_id IN (`+placeholders+`)

			UNION ALL

			SELECT attempt.id AS attempt_id, run.task_episode_id AS episode_id
			FROM task_agent_attempts attempt
			JOIN shadow_runs run ON run.id = attempt.shadow_run_id
			WHERE run.mode = 'task_mirror'
				AND run.task_episode_id IN (`+placeholders+`)

			UNION ALL

			SELECT attempt.id AS attempt_id, run.task_episode_id AS episode_id
			FROM task_agent_attempts attempt
			JOIN shadow_run_evaluations evaluation
				ON evaluation.id = attempt.shadow_evaluation_id
			JOIN shadow_runs run ON run.id = evaluation.shadow_run_id
			WHERE run.mode = 'task_mirror'
				AND run.task_episode_id IN (`+placeholders+`)
		)
		SELECT episode_id, COUNT(DISTINCT attempt_id)
		FROM attempt_links
		WHERE episode_id != ''
		GROUP BY episode_id
		ORDER BY episode_id ASC`, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var episodeID string
		var count int
		if err := rows.Scan(&episodeID, &count); err != nil {
			return nil, err
		}
		counts[episodeID] = count
	}
	return counts, rows.Err()
}

func taskShadowDebugEpisodeIndexIDs(
	indexRows []taskShadowDebugEpisodeIndexRow,
) []string {
	episodeIDs := make([]string, 0, len(indexRows))
	for _, item := range indexRows {
		episodeIDs = append(episodeIDs, item.Summary.ID)
	}
	return episodeIDs
}

func taskShadowDebugEpisodeSummariesFromIndex(
	store *ActivityStore,
	indexRows []taskShadowDebugEpisodeIndexRow,
	runs map[string][]taskShadowDebugRunIndexRow,
	runCounts map[string]int,
) ([]TaskShadowDebugEpisodeSummary, error) {
	episodeIDs := taskShadowDebugEpisodeIndexIDs(indexRows)
	evaluationCounts, err := taskShadowDebugIndexEvaluations(store, episodeIDs)
	if err != nil {
		return nil, err
	}
	attemptCounts, err := taskShadowDebugIndexAttemptCounts(store, episodeIDs)
	if err != nil {
		return nil, err
	}
	result := make([]TaskShadowDebugEpisodeSummary, 0, len(indexRows))
	for _, item := range indexRows {
		summary := item.Summary
		linkedRuns := runs[summary.ID]
		runIDs := make([]string, 0, len(linkedRuns))
		for _, run := range linkedRuns {
			runIDs = append(runIDs, run.ID)
		}
		summary.LinkedShadowRunIDs = taskShadowDebugLast(runIDs, 20)
		summary.LinkedShadowRunCount = runCounts[summary.ID]
		summary.TaskAgentAttemptCount = attemptCounts[summary.ID]
		summary.PersistedEvaluationCount = evaluationCounts[summary.ID]
		if len(linkedRuns) > 0 {
			latestRun := linkedRuns[len(linkedRuns)-1]
			if strings.TrimSpace(latestRun.PromptVersion) != shadowTaskPromptVersion {
				summary.LatestPairWorkflow = latestRun.CapabilityKey
				summary.LatestPairState = ShadowTaskPairStateHistoricalGeneration
				summary.LatestPairReasons = []string{
					"historical_shadow_prompt_generation",
				}
			} else {
				pair, pairErr := BuildShadowTaskPairReadiness(store, latestRun.ID)
				if pairErr != nil {
					summary.LatestPairWorkflow = latestRun.CapabilityKey
					summary.LatestPairState = "readiness_error"
					summary.LatestPairReasons = []string{pairErr.Error()}
				} else {
					summary.LatestPairWorkflow = pair.Workflow
					summary.LatestPairState = pair.State
					summary.LatestPairReasons = pair.Reasons
				}
			}
		} else {
			rollout := taskShadowDebugProductionRolloutFields(
				summary.ID,
				summary.TaskHypothesis,
				summary.DesiredResultHypothesis,
				item.HasFrozenSourceContext,
			)
			summary.LatestPairWorkflow = rollout.Workflow
			if rollout.Eligible {
				summary.LatestPairState = ShadowTaskPairStateShadowPending
				summary.LatestPairReasons = []string{
					"eligible production task has no shadow run",
				}
			} else {
				summary.LatestPairState = rollout.State
				summary.LatestPairReasons = []string{rollout.Reason}
			}
		}
		result = append(result, summary)
	}
	return result, nil
}

// GetTaskShadowDebugSnapshot returns a read-only, bounded index plus derived
// continuation and release health for internal DevTools. It contains no frozen
// context JSON or raw evidence payloads.
func (a *App) GetTaskShadowDebugSnapshot(limit int) (TaskShadowDebugSnapshot, error) {
	store, err := taskShadowDebugStore(a)
	if err != nil {
		return TaskShadowDebugSnapshot{}, err
	}
	limit = normalizeTaskShadowDebugLimit(limit)
	allIndexRows, err := taskShadowDebugEpisodeIndexRows(
		store,
		taskEpisodeMaxListLimit,
	)
	if err != nil {
		return TaskShadowDebugSnapshot{}, err
	}
	allEpisodeIDs := taskShadowDebugEpisodeIndexIDs(allIndexRows)
	allRuns, allRunCounts, err := taskShadowDebugIndexRuns(
		store,
		allEpisodeIDs,
	)
	if err != nil {
		return TaskShadowDebugSnapshot{}, err
	}
	allEvaluationCounts, err := taskShadowDebugIndexEvaluations(
		store,
		allEpisodeIDs,
	)
	if err != nil {
		return TaskShadowDebugSnapshot{}, err
	}
	visibleIndexRows := allIndexRows
	if len(visibleIndexRows) > limit {
		visibleIndexRows = visibleIndexRows[:limit]
	}
	episodes, err := taskShadowDebugEpisodeSummariesFromIndex(
		store,
		visibleIndexRows,
		allRuns,
		allRunCounts,
	)
	if err != nil {
		return TaskShadowDebugSnapshot{}, err
	}
	result := TaskShadowDebugSnapshot{
		GeneratedAt:       time.Now().UnixMilli(),
		Limit:             limit,
		EnabledWorkflows:  taskWorkflowProductionShadowWorkflows(),
		ReasoningVersions: taskShadowDebugReasoningCurrentVersions(),
		Episodes:          episodes,
	}
	result.LifecycleHealth, err = taskShadowLifecycleHealth(
		store,
		result.GeneratedAt,
		result.Episodes,
	)
	if err != nil {
		return TaskShadowDebugSnapshot{}, err
	}
	result.Continuation, err = BuildTaskShadowContinuationSnapshot(
		store,
		limit,
		result.GeneratedAt,
	)
	if err != nil {
		return TaskShadowDebugSnapshot{}, err
	}
	releaseDetail, err := taskShadowReleaseGateDetail(
		a,
		allIndexRows,
		allRuns,
		allEvaluationCounts,
		limit,
	)
	if err != nil {
		result.ReleaseGate = TaskShadowReleaseGate{
			Version:              taskShadowReleaseGateVersion,
			Stage:                "diagnostic_error",
			RequiredShadowPrompt: shadowTaskPromptVersion,
			HistoricalShadowRuns: []string{},
			ObserverProvenance:   newTaskShadowReleaseObserverProvenance(""),
			RequiredAction:       "Inspect the release-gate diagnostic error.",
			Reasons:              []string{err.Error()},
			Steps:                taskShadowReleaseGateSteps(),
		}
		return result, nil
	}
	monitoring, foreground := taskShadowDebugObserverState(a)
	result.ReleaseGate = taskShadowReleaseGateFromDetail(
		releaseDetail,
		monitoring,
		foreground,
	)
	result.QualityCampaign, err = buildTaskShadowQualityCampaignFromIndex(
		store,
		limit,
		result.GeneratedAt,
		allIndexRows,
		allRuns,
	)
	if err != nil {
		return TaskShadowDebugSnapshot{}, err
	}
	return result, nil
}

func taskShadowDebugEvidence(
	evidence []TaskEpisodeEvidence,
	limit int,
) []TaskShadowDebugEvidence {
	evidence = taskShadowDebugLast(evidence, limit)
	result := make([]TaskShadowDebugEvidence, 0, len(evidence))
	for _, item := range evidence {
		payload := strings.TrimSpace(item.PayloadJSON)
		result = append(result, TaskShadowDebugEvidence{
			ID:              item.ID,
			TaskEpisodeID:   item.TaskEpisodeID,
			Role:            item.Role,
			SourceKind:      item.SourceKind,
			SourceID:        item.SourceID,
			Summary:         item.Summary,
			ContentHash:     item.ContentHash,
			PayloadRedacted: payload != "" && payload != "{}",
			ObservedAt:      item.ObservedAt,
			CreatedAt:       item.CreatedAt,
		})
	}
	return result
}

func taskShadowDebugResultDestinations(
	evidence []TaskEpisodeEvidence,
	workflow string,
	limit int,
) []TaskShadowDebugResultDestination {
	resultEvidence := make([]TaskEpisodeEvidence, 0)
	for _, item := range evidence {
		if item.Role == TaskEpisodeEvidenceResult {
			resultEvidence = append(resultEvidence, item)
		}
	}
	resultEvidence = taskShadowDebugLast(resultEvidence, limit)

	grouped := make(map[string]*TaskShadowDebugResultDestination)
	order := make([]string, 0, len(resultEvidence))
	for _, item := range resultEvidence {
		receipt := TaskShadowDebugResultDestination{
			Status:           "historical_unbound",
			ValidationReason: "result evidence has no destination-bound materialization",
			EvidenceIDs:      []string{item.ID},
			LatestObservedAt: item.ObservedAt,
		}
		var envelope struct {
			MaterializedResult *TaskLifecycleMaterializedResult `json:"materializedResult"`
		}
		payload := strings.TrimSpace(item.PayloadJSON)
		if payload != "" {
			if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
				receipt.Status = "invalid_payload"
				receipt.ValidationReason = "result evidence payload is malformed"
			} else if materialized := envelope.MaterializedResult; materialized != nil {
				receipt.Workflow = strings.TrimSpace(materialized.Workflow)
				receipt.ResultSHA256 = strings.ToLower(
					strings.TrimSpace(materialized.SHA256),
				)
				receipt.DestinationSHA256 = strings.ToLower(
					strings.TrimSpace(materialized.DestinationSHA256),
				)
				receipt.SourceEvidenceRefs = normalizeShadowTaskEvaluationList(
					materialized.EvidenceRefs,
				)
				switch {
				case materialized.Destination == nil &&
					receipt.DestinationSHA256 == "":
					receipt.Status = "historical_missing_destination"
					receipt.ValidationReason = "materialized result predates destination provenance"
				case materialized.Destination == nil ||
					receipt.DestinationSHA256 == "":
					receipt.Status = "invalid_destination"
					receipt.ValidationReason = "destination object and digest must appear together"
				default:
					destination, err := normalizeTaskLifecycleResultDestination(
						materialized.Destination,
					)
					if destination != nil {
						receipt.Version = destination.Version
						receipt.AppBundleID = destination.AppBundleID
						receipt.AppName = destination.AppName
						receipt.WindowTitle = destination.WindowTitle
						receipt.SemanticRole = destination.SemanticRole
						receipt.CaptureMethod = destination.CaptureMethod
						receipt.Current = destination.Version ==
							taskLifecycleResultDestinationVersion
					}
					if err != nil {
						receipt.Status = "invalid_destination"
						receipt.ValidationReason = err.Error()
					} else if !receipt.Current {
						receipt.Status = "historical_destination_version"
						receipt.ValidationReason = "destination contract is historical"
					} else {
						validated, validationErr := taskEpisodeEvidenceMaterializedResult(
							item,
							workflow,
						)
						if validationErr != nil || validated == nil {
							receipt.Status = "invalid_destination"
							if validationErr != nil {
								receipt.ValidationReason = validationErr.Error()
							} else {
								receipt.ValidationReason = "destination-bound result is unavailable"
							}
						} else {
							receipt.Status = "current_valid"
							receipt.ValidationReason = "content, workflow, destination, and digests verified"
							receipt.Valid = true
						}
					}
				}
			}
		}

		key := strings.Join([]string{
			receipt.Status,
			receipt.Workflow,
			receipt.ResultSHA256,
			receipt.DestinationSHA256,
			receipt.Version,
			receipt.AppBundleID,
			receipt.WindowTitle,
		}, "\x00")
		if receipt.ResultSHA256 == "" && receipt.DestinationSHA256 == "" {
			key += "\x00" + item.ID
		}
		if existing, ok := grouped[key]; ok {
			existing.EvidenceIDs = append(existing.EvidenceIDs, item.ID)
			existing.SourceEvidenceRefs = append(
				existing.SourceEvidenceRefs,
				receipt.SourceEvidenceRefs...,
			)
			existing.LatestObservedAt = max(
				existing.LatestObservedAt,
				receipt.LatestObservedAt,
			)
			continue
		}
		copyReceipt := receipt
		grouped[key] = &copyReceipt
		order = append(order, key)
	}

	result := make([]TaskShadowDebugResultDestination, 0, len(order))
	for _, key := range order {
		item := *grouped[key]
		item.EvidenceIDs = uniqueSortedStrings(item.EvidenceIDs)
		item.SourceEvidenceRefs = uniqueSortedStrings(item.SourceEvidenceRefs)
		result = append(result, item)
	}
	sort.SliceStable(result, func(left, right int) bool {
		if result[left].LatestObservedAt != result[right].LatestObservedAt {
			return result[left].LatestObservedAt > result[right].LatestObservedAt
		}
		return result[left].Status < result[right].Status
	})
	return result
}

func taskShadowDebugReasoningCurrentVersions() TaskShadowDebugReasoningCurrentVersions {
	return TaskShadowDebugReasoningCurrentVersions{
		OnsetPrompt:       taskOnsetPromptVersion,
		LifecyclePrompt:   taskLifecyclePromptVersion,
		ShadowPrompt:      shadowTaskPromptVersion,
		ResultDestination: taskLifecycleResultDestinationVersion,
		EvaluationInput:   ShadowTaskEvaluationInputVersion,
		EvaluationOutput:  ShadowTaskEvaluationOutputVersion,
		Comparator:        ShadowTaskComparatorVersion,
		Evaluator:         ShadowTaskEvaluatorVersion,
	}
}

func taskShadowDebugReasoningVersionCounts(
	versions []string,
	current string,
) []TaskShadowDebugReasoningVersionCount {
	counts := make(map[string]int)
	for _, version := range versions {
		version = strings.TrimSpace(version)
		if version != "" {
			counts[version]++
		}
	}
	result := make([]TaskShadowDebugReasoningVersionCount, 0, len(counts))
	for version, count := range counts {
		result = append(result, TaskShadowDebugReasoningVersionCount{
			Version: version,
			Count:   count,
			Current: version == current,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Current != result[j].Current {
			return result[i].Current
		}
		return result[i].Version < result[j].Version
	})
	return result
}

func taskShadowDebugReasoningRefsResolved(
	refs []string,
	allowed map[string]struct{},
) bool {
	for _, ref := range refs {
		if _, ok := allowed[strings.TrimSpace(ref)]; !ok {
			return false
		}
	}
	return true
}

func taskShadowDebugReasoningHypothesis(
	statement string,
	confidence float64,
	basis, sourceQuote string,
	evidenceRefs, profileRefs, contradictionRefs []string,
	allowedEvidence, allowedProfile map[string]struct{},
	quoteValidation string,
) TaskShadowDebugReasoningHypothesis {
	item := TaskShadowDebugReasoningHypothesis{
		Statement:         strings.TrimSpace(statement),
		Confidence:        confidence,
		Basis:             strings.TrimSpace(basis),
		SourceQuote:       strings.TrimSpace(sourceQuote),
		EvidenceRefs:      append([]string{}, evidenceRefs...),
		ProfileRefs:       append([]string{}, profileRefs...),
		ContradictionRefs: append([]string{}, contradictionRefs...),
		QuoteValidation:   quoteValidation,
	}
	item.ProvenanceResolved = item.Statement != "" &&
		taskShadowDebugReasoningRefsResolved(item.EvidenceRefs, allowedEvidence) &&
		taskShadowDebugReasoningRefsResolved(item.ContradictionRefs, allowedEvidence) &&
		taskShadowDebugReasoningRefsResolved(item.ProfileRefs, allowedProfile)
	if len(item.EvidenceRefs) == 0 && len(item.ProfileRefs) == 0 {
		item.ProvenanceResolved = false
	}
	switch item.Basis {
	case TaskReasoningBasisExplicitStatement:
		if item.SourceQuote == "" {
			item.ProvenanceResolved = false
			item.QuoteValidation = "missing"
		}
	case TaskReasoningBasisRepeatedPattern:
		if len(item.ProfileRefs) == 0 {
			item.ProvenanceResolved = false
		}
	case TaskReasoningBasisContextualInference:
		if item.Confidence >= 0.5 {
			item.ProvenanceResolved = false
		}
	default:
		item.ProvenanceResolved = false
	}
	if item.ProvenanceResolved {
		item.ValidationReason = "frozen provenance resolves"
	} else {
		item.ValidationReason = "frozen provenance is incomplete or violates the current contract"
	}
	return item
}

func taskShadowDebugOnsetReasoning(
	detail TaskEpisodeDetail,
) []TaskShadowDebugReasoningHypothesis {
	allowedEvidence := make(map[string]struct{}, len(detail.Evidence))
	for _, item := range detail.Evidence {
		if item.ObservedAt <= detail.Episode.FrozenContext.CutoffAt {
			allowedEvidence[item.ID] = struct{}{}
		}
	}
	allowedProfile := make(
		map[string]struct{},
		len(detail.Episode.LatestHypothesis.ProfileInfluenceRefs),
	)
	for _, ref := range detail.Episode.LatestHypothesis.ProfileInfluenceRefs {
		allowedProfile[ref] = struct{}{}
	}
	result := make(
		[]TaskShadowDebugReasoningHypothesis,
		0,
		len(detail.Episode.LatestHypothesis.ReasoningHypotheses),
	)
	for _, hypothesis := range detail.Episode.LatestHypothesis.ReasoningHypotheses {
		quoteValidation := "not_applicable"
		if hypothesis.Basis == TaskReasoningBasisExplicitStatement {
			quoteValidation = "validated_at_onset"
		}
		result = append(result, taskShadowDebugReasoningHypothesis(
			hypothesis.Statement,
			hypothesis.Confidence,
			hypothesis.Basis,
			hypothesis.SourceQuote,
			hypothesis.EvidenceIDs,
			hypothesis.ProfileRefs,
			hypothesis.ContradictionEvidenceIDs,
			allowedEvidence,
			allowedProfile,
			quoteValidation,
		))
	}
	return result
}

func taskShadowDebugObservedReasoning(
	evidence []TaskEpisodeEvidence,
	limit int,
) []TaskShadowDebugObservedReasoning {
	result := make([]TaskShadowDebugObservedReasoning, 0)
	for _, item := range evidence {
		if item.Role != TaskEpisodeEvidenceReasoning {
			continue
		}
		view := TaskShadowDebugObservedReasoning{
			EvidenceID:       item.ID,
			Kind:             "legacy_untyped",
			SourceKind:       item.SourceKind,
			SourceID:         item.SourceID,
			SourceQuote:      item.Summary,
			EvidenceRefs:     []string{},
			ObservedAt:       item.ObservedAt,
			ValidationReason: "legacy reasoning evidence has no typed actor or quote validation",
		}
		if item.SourceKind == "task_lifecycle_reasoning" {
			payload, err := validateTaskLifecycleReasoningEvidence(item)
			if err != nil {
				var raw taskLifecycleReasoningEvidencePayload
				_ = json.Unmarshal([]byte(item.PayloadJSON), &raw)
				view.Kind = "invalid"
				view.Version = strings.TrimSpace(raw.Version)
				view.SourceQuote = strings.TrimSpace(raw.SourceQuote)
				view.EvidenceRefs = uniqueSortedStrings(raw.EvidenceRefs)
				view.ValidationReason = err.Error()
			} else {
				view.Version = payload.Version
				view.Kind = payload.Kind
				view.SourceQuote = payload.SourceQuote
				view.EvidenceRefs = uniqueSortedStrings(payload.EvidenceRefs)
				view.Valid = true
				view.ValidationReason = "literal user-authored lifecycle evidence validated at capture"
			}
		}
		result = append(result, view)
	}
	return taskShadowDebugLast(result, limit)
}

func taskShadowDebugPredictionReasoning(
	run ShadowRun,
	allowedEvidence map[string]struct{},
) TaskShadowDebugReasoningPrediction {
	allowedProfile := make(map[string]struct{}, len(run.Prediction.ProfileInfluenceRefs))
	for _, ref := range run.Prediction.ProfileInfluenceRefs {
		allowedProfile[ref] = struct{}{}
	}
	hypotheses := make(
		[]TaskShadowDebugReasoningHypothesis,
		0,
		len(run.Prediction.ReasoningHypotheses),
	)
	for _, hypothesis := range run.Prediction.ReasoningHypotheses {
		quoteValidation := "not_applicable"
		if hypothesis.Basis == TaskReasoningBasisExplicitStatement {
			quoteValidation = "frozen_prediction_quote"
		}
		hypotheses = append(hypotheses, taskShadowDebugReasoningHypothesis(
			hypothesis.Statement,
			hypothesis.Confidence,
			hypothesis.Basis,
			hypothesis.SourceQuote,
			hypothesis.EvidenceRefs,
			hypothesis.ProfileRefs,
			hypothesis.ContradictionRefs,
			allowedEvidence,
			allowedProfile,
			quoteValidation,
		))
	}
	return TaskShadowDebugReasoningPrediction{
		ShadowRunID:   run.ID,
		PromptVersion: run.PromptVersion,
		Current:       run.PromptVersion == shadowTaskPromptVersion,
		Hypotheses:    hypotheses,
	}
}

func taskShadowDebugReasoningPayloadEqual(
	left, right []ShadowReasoningHypothesis,
) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func taskShadowDebugReasoningComparison(
	run ShadowRun,
	evaluation ShadowRunEvaluation,
) TaskShadowDebugReasoningComparison {
	result := TaskShadowDebugReasoningComparison{
		EvaluationID:            evaluation.ID,
		ShadowRunID:             run.ID,
		ComparatorVersion:       evaluation.ComparisonVersion,
		EvaluatorVersion:        evaluation.EvaluatorVersion,
		EvaluatorModel:          evaluation.EvaluatorModel,
		PredictionBindingReason: "historical evaluation has no typed reasoning input",
		PredictedHypotheses:     []TaskShadowDebugReasoningHypothesis{},
		SupportedEvidenceRefs:   []string{},
		Assessment:              evaluation.Reasoning.Assessment,
		Score:                   evaluation.Reasoning.Score,
		Confidence:              evaluation.Reasoning.Confidence,
		Reason:                  evaluation.Reasoning.Reason,
		EvidenceRefs:            append([]string{}, evaluation.Reasoning.EvidenceRefs...),
	}
	allowedReasoning := make(map[string]struct{})
	if evaluation.TaskEvaluationInput != nil {
		input := evaluation.TaskEvaluationInput
		result.InputVersion = input.Version
		result.PredictionBound = taskShadowDebugReasoningPayloadEqual(
			run.Prediction.ReasoningHypotheses,
			input.PredictedReasoning,
		)
		if result.PredictionBound {
			result.PredictionBindingReason = "comparator input matches the frozen Shadow prediction"
		} else {
			result.PredictionBindingReason = "comparator input differs from the frozen Shadow prediction"
		}
		allowedEvidence := make(map[string]struct{}, len(input.Evidence))
		for _, item := range input.Evidence {
			allowedEvidence[item.ID] = struct{}{}
			switch item.Kind {
			case ShadowTaskEvidenceExplicitReasoning,
				ShadowTaskEvidenceExplicitCorrection,
				ShadowTaskEvidenceRepeatedDecision:
				allowedReasoning[item.ID] = struct{}{}
				result.SupportedEvidenceRefs = append(
					result.SupportedEvidenceRefs,
					item.ID,
				)
			}
		}
		allowedProfile := make(map[string]struct{}, len(run.Prediction.ProfileInfluenceRefs))
		for _, ref := range run.Prediction.ProfileInfluenceRefs {
			allowedProfile[ref] = struct{}{}
		}
		for _, hypothesis := range input.PredictedReasoning {
			quoteValidation := "not_applicable"
			if hypothesis.Basis == TaskReasoningBasisExplicitStatement {
				quoteValidation = "frozen_comparator_input"
			}
			result.PredictedHypotheses = append(
				result.PredictedHypotheses,
				taskShadowDebugReasoningHypothesis(
					hypothesis.Statement,
					hypothesis.Confidence,
					hypothesis.Basis,
					hypothesis.SourceQuote,
					hypothesis.EvidenceRefs,
					hypothesis.ProfileRefs,
					hypothesis.ContradictionRefs,
					allowedEvidence,
					allowedProfile,
					quoteValidation,
				),
			)
		}
	}
	if evaluation.TaskEvaluation != nil {
		typed := evaluation.TaskEvaluation
		result.OutputVersion = typed.Version
		result.Assessment = typed.Reasoning.Assessment
		result.Score = typed.Reasoning.Score
		result.Confidence = typed.Reasoning.Confidence
		result.Reason = typed.Reasoning.Reason
		result.EvidenceRefs = append([]string{}, typed.Reasoning.EvidenceRefs...)
	}
	result.SupportedEvidenceRefs = uniqueSortedStrings(result.SupportedEvidenceRefs)
	result.EvidenceRefs = uniqueSortedStrings(result.EvidenceRefs)
	result.Unknown = result.Assessment == ShadowTaskAssessmentUnknown || result.Score == nil
	if result.Unknown {
		result.CitationsValid = result.Score == nil && len(result.EvidenceRefs) == 0
	} else {
		result.CitationsValid = len(result.EvidenceRefs) > 0 &&
			taskShadowDebugReasoningRefsResolved(result.EvidenceRefs, allowedReasoning)
	}
	result.Current = evaluation.ComparisonVersion == ShadowTaskComparatorVersion &&
		evaluation.EvaluatorVersion == ShadowTaskEvaluatorVersion &&
		result.InputVersion == ShadowTaskEvaluationInputVersion &&
		result.OutputVersion == ShadowTaskEvaluationOutputVersion &&
		run.PromptVersion == shadowTaskPromptVersion
	return result
}

func taskShadowDebugReasoning(
	episode TaskEpisodeDetail,
	runs []TaskShadowDebugRun,
	attempts []TaskAgentAttempt,
	limit int,
) TaskShadowDebugReasoning {
	current := taskShadowDebugReasoningCurrentVersions()
	result := TaskShadowDebugReasoning{
		Version:         taskShadowReasoningDebugVersion,
		OnsetHypotheses: taskShadowDebugOnsetReasoning(episode),
		Predictions:     make([]TaskShadowDebugReasoningPrediction, 0, len(runs)),
		Observed:        taskShadowDebugObservedReasoning(episode.Evidence, limit),
		Comparisons:     []TaskShadowDebugReasoningComparison{},
		Versions: TaskShadowDebugReasoningVersionLineage{
			Current: current,
		},
	}
	onsetVersions := make([]string, 0)
	lifecycleVersions := make([]string, 0)
	shadowVersions := make([]string, 0, len(runs))
	inputVersions := make([]string, 0)
	outputVersions := make([]string, 0)
	comparatorVersions := make([]string, 0)
	evaluatorVersions := make([]string, 0)
	shadowEvidence := make(map[string]struct{}, len(episode.Evidence))
	for _, item := range episode.Evidence {
		if item.ObservedAt <= episode.Episode.FrozenContext.CutoffAt {
			shadowEvidence["observed:"+item.ID] = struct{}{}
		}
	}
	for _, attempt := range attempts {
		switch attempt.Phase {
		case TaskAgentAttemptPhaseOnset:
			onsetVersions = append(onsetVersions, attempt.PromptVersion)
		case TaskAgentAttemptPhaseLifecycle:
			lifecycleVersions = append(lifecycleVersions, attempt.PromptVersion)
		}
	}
	for _, observed := range result.Observed {
		if observed.Version != "" {
			lifecycleVersions = append(lifecycleVersions, observed.Version)
		}
	}
	for _, run := range runs {
		result.Predictions = append(
			result.Predictions,
			taskShadowDebugPredictionReasoning(run.Run, shadowEvidence),
		)
		shadowVersions = append(shadowVersions, run.Run.PromptVersion)
		for _, item := range run.Evaluations {
			evaluation := item.Evaluation
			comparison := taskShadowDebugReasoningComparison(
				run.Run,
				evaluation,
			)
			result.Comparisons = append(result.Comparisons, comparison)
			comparatorVersions = append(
				comparatorVersions,
				evaluation.ComparisonVersion,
			)
			evaluatorVersions = append(
				evaluatorVersions,
				evaluation.EvaluatorVersion,
			)
			if comparison.InputVersion != "" {
				inputVersions = append(inputVersions, comparison.InputVersion)
			}
			if comparison.OutputVersion != "" {
				outputVersions = append(outputVersions, comparison.OutputVersion)
			}
		}
	}
	result.Comparisons = taskShadowDebugLast(result.Comparisons, limit)
	result.Versions.OnsetPrompts = taskShadowDebugReasoningVersionCounts(
		onsetVersions,
		current.OnsetPrompt,
	)
	result.Versions.LifecyclePrompts = taskShadowDebugReasoningVersionCounts(
		lifecycleVersions,
		current.LifecyclePrompt,
	)
	result.Versions.ShadowPrompts = taskShadowDebugReasoningVersionCounts(
		shadowVersions,
		current.ShadowPrompt,
	)
	result.Versions.EvaluationInputs = taskShadowDebugReasoningVersionCounts(
		inputVersions,
		current.EvaluationInput,
	)
	result.Versions.EvaluationOutputs = taskShadowDebugReasoningVersionCounts(
		outputVersions,
		current.EvaluationOutput,
	)
	result.Versions.Comparators = taskShadowDebugReasoningVersionCounts(
		comparatorVersions,
		current.Comparator,
	)
	result.Versions.Evaluators = taskShadowDebugReasoningVersionCounts(
		evaluatorVersions,
		current.Evaluator,
	)
	result.Counts.OnsetHypotheses = len(result.OnsetHypotheses)
	for _, prediction := range result.Predictions {
		result.Counts.ShadowHypotheses += len(prediction.Hypotheses)
	}
	for _, observed := range result.Observed {
		if observed.Valid {
			result.Counts.ObservedReasoning++
		} else {
			result.Counts.InvalidObservedReasoning++
		}
	}
	result.Counts.Comparisons = len(result.Comparisons)
	for _, comparison := range result.Comparisons {
		if comparison.Unknown {
			result.Counts.UnknownComparisons++
		} else {
			result.Counts.KnownComparisons++
		}
	}
	switch {
	case result.Counts.Comparisons == 0 &&
		result.Counts.OnsetHypotheses == 0 &&
		result.Counts.ObservedReasoning == 0:
		result.Coverage = "no_reasoning_signal"
	case result.Counts.Comparisons == 0:
		result.Coverage = "not_evaluated"
	case result.Counts.KnownComparisons == 0:
		result.Coverage = "unknown"
	case result.Counts.UnknownComparisons == 0:
		result.Coverage = "supported"
	default:
		result.Coverage = "partial"
	}
	return result
}

func taskShadowDebugEpisodeDetail(
	detail TaskEpisodeDetail,
	generatedAt int64,
	limit int,
) TaskShadowDebugEpisodeDetail {
	context := detail.Episode.FrozenContext
	workflow := taskWorkflowCapabilityKey(
		detail.Episode.LatestHypothesis.TaskHypothesis,
		detail.Episode.LatestHypothesis.DesiredResultHypothesis,
		detail.Episode.ID,
	)
	manifest := taskShadowDebugManifest(context.Manifest, limit)
	manifestIncludedCount := 0
	for _, item := range context.Manifest {
		if item.Included {
			manifestIncludedCount++
		}
	}
	return TaskShadowDebugEpisodeDetail{
		Episode: TaskShadowDebugEpisode{
			ID:              detail.Episode.ID,
			InferredStartAt: detail.Episode.InferredStartAt,
			DetectedAt:      detail.Episode.DetectedAt,
			CurrentStatus:   detail.Episode.CurrentStatus,
			ObservationLease: taskLifecycleObservationLease(
				detail,
				generatedAt,
			),
			CreatePayloadHash: detail.Episode.CreatePayloadHash,
			FrozenContext: TaskShadowDebugFrozenContext{
				ID:                    context.ID,
				TaskEpisodeID:         context.TaskEpisodeID,
				CutoffAt:              context.CutoffAt,
				SHA256:                context.SHA256,
				Manifest:              manifest,
				ManifestEntryCount:    len(context.Manifest),
				ManifestIncludedCount: manifestIncludedCount,
				ManifestExcludedCount: len(context.Manifest) - manifestIncludedCount,
				ManifestTruncated:     len(manifest) < len(context.Manifest),
				CanonicalJSONRedacted: strings.TrimSpace(context.CanonicalJSON) != "",
				CreatedAt:             context.CreatedAt,
			},
			LatestHypothesis: detail.Episode.LatestHypothesis,
			CreatedAt:        detail.Episode.CreatedAt,
		},
		Hypotheses: taskShadowDebugLast(detail.Hypotheses, limit),
		Evidence:   taskShadowDebugEvidence(detail.Evidence, limit),
		ResultDestinations: taskShadowDebugResultDestinations(
			detail.Evidence,
			workflow,
			limit,
		),
		Transitions: taskShadowDebugLast(detail.Transitions, limit),
		Links:       taskShadowDebugLast(detail.Links, limit),
	}
}

func sanitizeTaskShadowDebugRun(run ShadowRun, limit int) (ShadowRun, bool, bool) {
	contextRedacted := strings.TrimSpace(run.Context.PromptContext) != ""
	run.Context.PromptContext = ""
	run.Context.References = taskShadowDebugLast(run.Context.References, limit)
	for index := range run.Context.References {
		run.Context.References[index].Content = ""
	}
	run.Prediction.ReasoningHypotheses = taskShadowDebugLast(
		run.Prediction.ReasoningHypotheses,
		limit,
	)
	run.Prediction.RequiredCapabilities = taskShadowDebugFirst(
		run.Prediction.RequiredCapabilities,
		limit,
	)
	run.Prediction.PlannedEffects = taskShadowDebugFirst(
		run.Prediction.PlannedEffects,
		limit,
	)
	planRedacted := false
	if run.Prediction.CandidateExecutionPlan != nil {
		plan := *run.Prediction.CandidateExecutionPlan
		planRedacted = strings.TrimSpace(plan.Prompt) != ""
		plan.Prompt = ""
		run.Prediction.CandidateExecutionPlan = &plan
	}
	return run, contextRedacted, planRedacted
}

func sanitizeTaskShadowDebugExecutionAttempt(
	input ShadowExecutionAttempt,
	limit int,
) TaskShadowDebugExecutionAttempt {
	promptRedacted := strings.TrimSpace(input.Prompt) != "" ||
		strings.TrimSpace(input.Plan.Prompt) != ""
	outputRedacted := strings.TrimSpace(input.AssistantOutput) != ""
	input.Prompt = ""
	input.Plan.Prompt = ""
	input.AssistantOutput = ""
	input.Decisions = taskShadowDebugLast(input.Decisions, limit)
	input.Actions = taskShadowDebugLast(input.Actions, limit)
	input.Artifacts = taskShadowDebugLast(input.Artifacts, limit)
	artifactRedactions := 0
	for index := range input.Artifacts {
		if input.Artifacts[index].Content != "" {
			artifactRedactions++
		}
		input.Artifacts[index].Content = ""
	}
	return TaskShadowDebugExecutionAttempt{
		Attempt:                       input,
		PromptRedacted:                promptRedacted,
		AssistantOutputRedacted:       outputRedacted,
		ArtifactContentsRedactedCount: artifactRedactions,
	}
}

func sanitizeTaskShadowDebugEvaluation(
	input ShadowRunEvaluation,
	limit int,
) TaskShadowDebugEvaluation {
	promptRedacted := strings.TrimSpace(input.ComparatorPrompt) != ""
	rawRedacted := strings.TrimSpace(input.EvaluatorRaw) != ""
	input.ComparatorPrompt = ""
	input.EvaluatorRaw = ""
	shadowResultRedacted := false
	shadowArtifactsRedacted := false
	if input.TaskEvaluationInput != nil {
		evaluationInput := *input.TaskEvaluationInput
		evaluationInput.QualityCriteria = taskShadowDebugFirst(
			evaluationInput.QualityCriteria,
			limit,
		)
		evaluationInput.PolicyBoundaries = taskShadowDebugFirst(
			evaluationInput.PolicyBoundaries,
			limit,
		)
		evaluationInput.Evidence = taskShadowDebugLast(evaluationInput.Evidence, limit)
		evaluationInput.Shadow.Plan = taskShadowDebugFirst(
			evaluationInput.Shadow.Plan,
			limit,
		)
		evaluationInput.Shadow.Actions = taskShadowDebugLast(
			evaluationInput.Shadow.Actions,
			limit,
		)
		evaluationInput.Shadow.Artifacts = taskShadowDebugLast(
			evaluationInput.Shadow.Artifacts,
			limit,
		)
		evaluationInput.Observed.Actions = taskShadowDebugLast(
			evaluationInput.Observed.Actions,
			limit,
		)
		evaluationInput.Observed.Artifacts = taskShadowDebugLast(
			evaluationInput.Observed.Artifacts,
			limit,
		)
		if strings.TrimSpace(evaluationInput.Shadow.Result) != "" {
			shadowResultRedacted = true
			evaluationInput.Shadow.Result = ""
		}
		for index := range evaluationInput.Shadow.Artifacts {
			if evaluationInput.Shadow.Artifacts[index].Summary != "" {
				shadowArtifactsRedacted = true
			}
			evaluationInput.Shadow.Artifacts[index].Summary =
				"isolated artifact content redacted"
		}
		for index := range evaluationInput.Evidence {
			if evaluationInput.Evidence[index].Origin == ShadowTaskEvidenceOriginShadow &&
				evaluationInput.Evidence[index].Kind == ShadowTaskEvidenceArtifact {
				if evaluationInput.Evidence[index].Summary != "" {
					shadowArtifactsRedacted = true
				}
				evaluationInput.Evidence[index].Summary =
					"isolated artifact content redacted"
			}
		}
		input.TaskEvaluationInput = &evaluationInput
	}
	return TaskShadowDebugEvaluation{
		Evaluation:                 input,
		ComparatorPromptRedacted:   promptRedacted,
		EvaluatorRawRedacted:       rawRedacted,
		ShadowResultRedacted:       shadowResultRedacted,
		ShadowArtifactDataRedacted: shadowArtifactsRedacted,
	}
}

func sanitizeTaskShadowDebugRunDetail(
	detail ShadowRunDetail,
	executionAttempts []ShadowExecutionAttempt,
	pairReadiness ShadowTaskPairReadiness,
	limit int,
) TaskShadowDebugRun {
	run, contextRedacted, planRedacted := sanitizeTaskShadowDebugRun(
		detail.Run,
		limit,
	)
	evidence := taskShadowDebugLast(detail.Evidence, limit)
	for index := range evidence {
		evidence[index].Content = ""
	}
	observations := taskShadowDebugLast(detail.Observations, limit)
	for observationIndex := range observations {
		observations[observationIndex].Evidence = taskShadowDebugLast(
			observations[observationIndex].Evidence,
			limit,
		)
		observations[observationIndex].ReasoningEvidence = taskShadowDebugLast(
			observations[observationIndex].ReasoningEvidence,
			limit,
		)
		for evidenceIndex := range observations[observationIndex].Evidence {
			observations[observationIndex].Evidence[evidenceIndex].Content = ""
		}
	}
	executionAttempts = taskShadowDebugLast(executionAttempts, limit)
	safeAttempts := make([]TaskShadowDebugExecutionAttempt, 0, len(executionAttempts))
	for _, attempt := range executionAttempts {
		safeAttempts = append(
			safeAttempts,
			sanitizeTaskShadowDebugExecutionAttempt(attempt, limit),
		)
	}
	evaluations := taskShadowDebugLast(detail.Evaluations, limit)
	safeEvaluations := make([]TaskShadowDebugEvaluation, 0, len(evaluations))
	for _, evaluation := range evaluations {
		safeEvaluations = append(
			safeEvaluations,
			sanitizeTaskShadowDebugEvaluation(evaluation, limit),
		)
	}
	return TaskShadowDebugRun{
		Run:                   run,
		Status:                detail.Status,
		PairReadiness:         pairReadiness,
		Evidence:              evidence,
		Observations:          observations,
		ExecutionAttempts:     safeAttempts,
		Evaluations:           safeEvaluations,
		ContextPromptRedacted: contextRedacted,
		ExecutionPlanRedacted: planRedacted,
	}
}

func taskShadowDebugAttemptMapAppend(
	target map[string]TaskAgentAttempt,
	items []TaskAgentAttempt,
) {
	for _, item := range items {
		target[item.ID] = item
	}
}

func listTaskShadowDebugAgentAttempts(
	store *ActivityStore,
	taskEpisodeID string,
	runIDs, evaluationIDs []string,
	limit int,
) ([]TaskAgentAttempt, error) {
	collected := make(map[string]TaskAgentAttempt)
	items, err := store.ListTaskAgentAttempts(TaskAgentAttemptFilter{
		TaskEpisodeID: taskEpisodeID,
		Limit:         taskAgentAttemptMaxListLimit,
	})
	if err != nil {
		return nil, err
	}
	taskShadowDebugAttemptMapAppend(collected, items)
	for _, runID := range runIDs {
		items, err = store.ListTaskAgentAttempts(TaskAgentAttemptFilter{
			ShadowRunID: runID,
			Limit:       taskAgentAttemptMaxListLimit,
		})
		if err != nil {
			return nil, err
		}
		taskShadowDebugAttemptMapAppend(collected, items)
	}
	for _, evaluationID := range evaluationIDs {
		items, err = store.ListTaskAgentAttempts(TaskAgentAttemptFilter{
			ShadowEvaluationID: evaluationID,
			Limit:              taskAgentAttemptMaxListLimit,
		})
		if err != nil {
			return nil, err
		}
		taskShadowDebugAttemptMapAppend(collected, items)
	}
	result := make([]TaskAgentAttempt, 0, len(collected))
	for _, item := range collected {
		result = append(result, item)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt != result[j].CreatedAt {
			return result[i].CreatedAt > result[j].CreatedAt
		}
		return result[i].ID > result[j].ID
	})
	return taskShadowDebugFirst(result, limit), nil
}

func taskShadowDebugLearningCandidateMatches(
	candidate ShadowTaskLearningCandidate,
	taskEpisodeID string,
	runIDs, evaluationIDs map[string]struct{},
) bool {
	for _, item := range candidate.Evidence {
		if item.TaskEpisodeID == taskEpisodeID {
			return true
		}
		if _, ok := runIDs[item.ShadowRunID]; ok {
			return true
		}
		if _, ok := evaluationIDs[item.EvaluationID]; ok {
			return true
		}
	}
	for _, item := range candidate.Dimensions {
		if _, ok := evaluationIDs[item.EvaluationID]; ok {
			return true
		}
	}
	return false
}

func taskShadowDebugLearningRunMatches(
	run ShadowTaskLearningRun,
	taskEpisodeID string,
	runIDs, evaluationIDs map[string]struct{},
) bool {
	for _, candidate := range run.Projection.Candidates {
		if taskShadowDebugLearningCandidateMatches(
			candidate,
			taskEpisodeID,
			runIDs,
			evaluationIDs,
		) {
			return true
		}
	}
	for _, exclusion := range run.Projection.ExcludedEvaluations {
		if _, ok := evaluationIDs[exclusion.EvaluationID]; ok {
			return true
		}
	}
	return false
}

func sanitizeTaskShadowDebugLearningCandidate(
	candidate ShadowTaskLearningCandidate,
	limit int,
) ShadowTaskLearningCandidate {
	candidate.Evidence = taskShadowDebugLast(candidate.Evidence, limit)
	candidate.Dimensions = taskShadowDebugLast(candidate.Dimensions, limit)
	candidate.Qualification.Reasons = taskShadowDebugFirst(
		candidate.Qualification.Reasons,
		limit,
	)
	return candidate
}

func sanitizeTaskShadowDebugLearningRun(
	run ShadowTaskLearningRun,
	limit int,
) ShadowTaskLearningRun {
	run.Projection.ExcludedEvaluations = taskShadowDebugLast(
		run.Projection.ExcludedEvaluations,
		limit,
	)
	run.Projection.Candidates = taskShadowDebugLast(
		run.Projection.Candidates,
		limit,
	)
	for index := range run.Projection.Candidates {
		run.Projection.Candidates[index] = sanitizeTaskShadowDebugLearningCandidate(
			run.Projection.Candidates[index],
			limit,
		)
	}
	return run
}

func listTaskShadowDebugLearning(
	store *ActivityStore,
	taskEpisodeID string,
	runIDs, evaluationIDs map[string]struct{},
	limit int,
) (TaskShadowDebugLearning, []TaskShadowDebugExclusion, error) {
	allRuns, err := store.ListShadowTaskLearningRuns(shadowTaskLearningListMax)
	if err != nil {
		return TaskShadowDebugLearning{}, nil, err
	}
	runs := make([]ShadowTaskLearningRun, 0)
	exclusions := make([]TaskShadowDebugExclusion, 0)
	runIDSet := make(map[string]struct{})
	for _, run := range allRuns {
		if !taskShadowDebugLearningRunMatches(
			run,
			taskEpisodeID,
			runIDs,
			evaluationIDs,
		) {
			continue
		}
		runIDSet[run.ID] = struct{}{}
		for _, exclusion := range run.Projection.ExcludedEvaluations {
			if _, ok := evaluationIDs[exclusion.EvaluationID]; !ok {
				continue
			}
			reason := strings.TrimSpace(exclusion.Reason)
			if reason == "" {
				reason = "evaluation excluded from learning"
			}
			exclusions = append(exclusions, TaskShadowDebugExclusion{
				Stage:         "learning",
				CorrelationID: exclusion.EvaluationID,
				Reason:        reason,
			})
		}
		runs = append(runs, sanitizeTaskShadowDebugLearningRun(run, limit))
	}
	runs = taskShadowDebugFirst(runs, limit)

	allRevisions, err := store.ListShadowTaskLearningRevisions(
		"",
		"",
		shadowTaskLearningListMax,
	)
	if err != nil {
		return TaskShadowDebugLearning{}, nil, err
	}
	revisions := make([]ShadowTaskLearningRevision, 0)
	revisionIDs := make(map[string]struct{})
	for _, revision := range allRevisions {
		if _, ok := runIDSet[revision.RunID]; !ok &&
			!taskShadowDebugLearningCandidateMatches(
				revision.Candidate,
				taskEpisodeID,
				runIDs,
				evaluationIDs,
			) {
			continue
		}
		revision.Candidate = sanitizeTaskShadowDebugLearningCandidate(
			revision.Candidate,
			limit,
		)
		revisions = append(revisions, revision)
		revisionIDs[revision.Candidate.RevisionID] = struct{}{}
	}
	revisions = taskShadowDebugFirst(revisions, limit)
	revisionIDs = make(map[string]struct{}, len(revisions))
	for _, revision := range revisions {
		revisionIDs[revision.Candidate.RevisionID] = struct{}{}
	}

	allMaterializations, err := store.ListShadowTaskLearningMaterializations(
		shadowTaskLearningListMax,
	)
	if err != nil {
		return TaskShadowDebugLearning{}, nil, err
	}
	materializations := make([]ShadowTaskLearningMaterialization, 0)
	for _, item := range allMaterializations {
		if _, ok := revisionIDs[item.RevisionID]; ok {
			materializations = append(materializations, item)
		}
	}
	materializations = taskShadowDebugFirst(materializations, limit)
	exclusions = taskShadowDebugFirst(exclusions, limit)
	return TaskShadowDebugLearning{
		Runs:             runs,
		Revisions:        revisions,
		Materializations: materializations,
	}, exclusions, nil
}

func taskShadowDebugContextExclusions(
	manifest []TaskContextManifestEntry,
	limit int,
) []TaskShadowDebugExclusion {
	result := make([]TaskShadowDebugExclusion, 0)
	for _, item := range manifest {
		if item.Included {
			continue
		}
		reason := strings.TrimSpace(item.Reason)
		if reason == "" {
			reason = "context source not selected"
		}
		result = append(result, TaskShadowDebugExclusion{
			Stage:         "context_selection",
			CorrelationID: item.Ref,
			SourceKind:    item.SourceKind,
			SourceID:      item.SourceID,
			Reason:        reason,
		})
	}
	return taskShadowDebugFirst(result, limit)
}

func taskShadowDebugCorrelations(
	episode TaskEpisode,
	runs []TaskShadowDebugRun,
	attempts []TaskAgentAttempt,
	learning TaskShadowDebugLearning,
	limit int,
) TaskShadowDebugCorrelationIDs {
	contextRefs := make(map[string]struct{})
	runIDs := make(map[string]struct{})
	executionIDs := make(map[string]struct{})
	observationIDs := make(map[string]struct{})
	evaluationIDs := make(map[string]struct{})
	attemptIDs := make(map[string]struct{})
	learningRunIDs := make(map[string]struct{})
	revisionIDs := make(map[string]struct{})
	materializationIDs := make(map[string]struct{})
	for _, item := range episode.FrozenContext.Manifest {
		contextRefs[item.Ref] = struct{}{}
	}
	for _, run := range runs {
		runIDs[run.Run.ID] = struct{}{}
		for _, attempt := range run.ExecutionAttempts {
			executionIDs[attempt.Attempt.ID] = struct{}{}
		}
		for _, observation := range run.Observations {
			observationIDs[observation.ID] = struct{}{}
		}
		for _, evaluation := range run.Evaluations {
			evaluationIDs[evaluation.Evaluation.ID] = struct{}{}
		}
	}
	for _, attempt := range attempts {
		attemptIDs[attempt.ID] = struct{}{}
	}
	for _, run := range learning.Runs {
		learningRunIDs[run.ID] = struct{}{}
	}
	for _, revision := range learning.Revisions {
		revisionIDs[revision.Candidate.RevisionID] = struct{}{}
	}
	for _, item := range learning.Materializations {
		materializationIDs[item.ID] = struct{}{}
	}
	return TaskShadowDebugCorrelationIDs{
		TaskEpisodeID:       episode.ID,
		ContextSnapshotID:   episode.FrozenContext.ID,
		ContextHash:         episode.FrozenContext.SHA256,
		ContextRefs:         taskShadowDebugSortedIDs(contextRefs, limit),
		ShadowRunIDs:        taskShadowDebugSortedIDs(runIDs, limit),
		ExecutionAttemptIDs: taskShadowDebugSortedIDs(executionIDs, limit),
		ObservationIDs:      taskShadowDebugSortedIDs(observationIDs, limit),
		EvaluationIDs:       taskShadowDebugSortedIDs(evaluationIDs, limit),
		TaskAgentAttemptIDs: taskShadowDebugSortedIDs(attemptIDs, limit),
		LearningRunIDs:      taskShadowDebugSortedIDs(learningRunIDs, limit),
		LearningRevisionIDs: taskShadowDebugSortedIDs(revisionIDs, limit),
		MaterializationIDs:  taskShadowDebugSortedIDs(materializationIDs, limit),
	}
}

func resolveTaskShadowDebugEpisode(
	store *ActivityStore,
	taskEpisodeID, shadowRunID string,
) (TaskEpisodeDetail, *ShadowRun, error) {
	taskEpisodeID = strings.TrimSpace(taskEpisodeID)
	shadowRunID = strings.TrimSpace(shadowRunID)
	var selectedRun *ShadowRun
	if shadowRunID != "" {
		run, err := store.GetShadowRun(shadowRunID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return TaskEpisodeDetail{}, nil, fmt.Errorf(
					"task shadow run %q not found",
					shadowRunID,
				)
			}
			return TaskEpisodeDetail{}, nil, err
		}
		if run.Mode != "task_mirror" || strings.TrimSpace(run.TaskEpisodeID) == "" {
			return TaskEpisodeDetail{}, nil, errors.New(
				"selected shadow run is not a task mirror",
			)
		}
		if taskEpisodeID != "" && taskEpisodeID != run.TaskEpisodeID {
			return TaskEpisodeDetail{}, nil, errors.New(
				"selected shadow run belongs to a different task episode",
			)
		}
		taskEpisodeID = run.TaskEpisodeID
		selectedRun = &run
	}
	if taskEpisodeID == "" {
		return TaskEpisodeDetail{}, nil, errors.New(
			"task episode ID or task shadow run ID is required",
		)
	}
	detail, err := store.GetTaskEpisodeDetail(taskEpisodeID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TaskEpisodeDetail{}, nil, fmt.Errorf(
				"task episode %q not found",
				taskEpisodeID,
			)
		}
		return TaskEpisodeDetail{}, nil, err
	}
	return detail, selectedRun, nil
}

// GetTaskShadowDebugDetail is a strictly read-only internal view. Supplying a
// shadowRunID narrows run-specific ledgers while retaining the TaskEpisode's
// onset/lifecycle model attempts. Supplying an empty run ID returns the newest
// bounded set of task-mirror runs linked to the episode.
func (a *App) GetTaskShadowDebugDetail(
	taskEpisodeID, shadowRunID string,
	limit int,
) (TaskShadowDebugDetail, error) {
	store, err := taskShadowDebugStore(a)
	if err != nil {
		return TaskShadowDebugDetail{}, err
	}
	limit = normalizeTaskShadowDebugLimit(limit)
	episodeDetail, selectedRun, err := resolveTaskShadowDebugEpisode(
		store,
		taskEpisodeID,
		shadowRunID,
	)
	if err != nil {
		return TaskShadowDebugDetail{}, err
	}

	var linkedRuns []ShadowRun
	if selectedRun != nil {
		linkedRuns = []ShadowRun{*selectedRun}
	} else {
		linkedRuns, err = listTaskShadowDebugRuns(
			store,
			episodeDetail.Episode.ID,
			min(limit, taskShadowDebugMaxRuns),
		)
		if err != nil {
			return TaskShadowDebugDetail{}, err
		}
	}

	runViews := make([]TaskShadowDebugRun, 0, len(linkedRuns))
	runIDSet := make(map[string]struct{}, len(linkedRuns))
	evaluationIDSet := make(map[string]struct{})
	observationIDSet := make(map[string]struct{})
	executionIDSet := make(map[string]struct{})
	for _, run := range linkedRuns {
		runIDSet[run.ID] = struct{}{}
		detail, err := store.GetShadowRunDetail(run.ID, time.Now().UnixMilli())
		if err != nil {
			return TaskShadowDebugDetail{}, err
		}
		executionAttempts, err := store.ListShadowExecutionAttempts(run.ID)
		if err != nil {
			return TaskShadowDebugDetail{}, err
		}
		pairReadiness, readinessErr := BuildShadowTaskPairReadiness(
			store,
			run.ID,
		)
		if readinessErr != nil {
			pairReadiness = ShadowTaskPairReadiness{
				Workflow: run.CapabilityKey,
				State:    "readiness_error",
				Reasons:  []string{readinessErr.Error()},
			}
		}
		detail.Evidence = taskShadowDebugLast(detail.Evidence, limit)
		detail.Observations = taskShadowDebugLast(detail.Observations, limit)
		detail.Evaluations = taskShadowDebugLast(detail.Evaluations, limit)
		executionAttempts = taskShadowDebugLast(executionAttempts, limit)
		for _, observation := range detail.Observations {
			observationIDSet[observation.ID] = struct{}{}
		}
		for _, evaluation := range detail.Evaluations {
			evaluationIDSet[evaluation.ID] = struct{}{}
		}
		for _, attempt := range executionAttempts {
			executionIDSet[attempt.ID] = struct{}{}
		}
		runViews = append(
			runViews,
			sanitizeTaskShadowDebugRunDetail(
				detail,
				executionAttempts,
				pairReadiness,
				limit,
			),
		)
	}

	runIDs := taskShadowDebugSortedIDs(runIDSet, taskShadowDebugMaxLimit)
	evaluationIDs := taskShadowDebugSortedIDs(
		evaluationIDSet,
		taskShadowDebugMaxLimit,
	)
	taskAgentAttempts, err := listTaskShadowDebugAgentAttempts(
		store,
		episodeDetail.Episode.ID,
		runIDs,
		evaluationIDs,
		limit,
	)
	if err != nil {
		return TaskShadowDebugDetail{}, err
	}
	learning, learningExclusions, err := listTaskShadowDebugLearning(
		store,
		episodeDetail.Episode.ID,
		runIDSet,
		evaluationIDSet,
		limit,
	)
	if err != nil {
		return TaskShadowDebugDetail{}, err
	}
	exclusions := append(
		taskShadowDebugContextExclusions(
			episodeDetail.Episode.FrozenContext.Manifest,
			limit,
		),
		learningExclusions...,
	)
	exclusions = taskShadowDebugFirst(exclusions, limit)

	selectedRunID := ""
	if selectedRun != nil {
		selectedRunID = selectedRun.ID
	}
	generatedAt := time.Now().UnixMilli()
	result := TaskShadowDebugDetail{
		GeneratedAt:         generatedAt,
		Limit:               limit,
		SelectedShadowRunID: selectedRunID,
		ProductionRollout: taskShadowDebugProductionRollout(
			episodeDetail.Episode,
		),
		Episode: taskShadowDebugEpisodeDetail(
			episodeDetail,
			generatedAt,
			limit,
		),
		Runs:              runViews,
		TaskAgentAttempts: taskAgentAttempts,
		Learning:          learning,
		Exclusions:        exclusions,
	}
	result.Reasoning = taskShadowDebugReasoning(
		episodeDetail,
		result.Runs,
		result.TaskAgentAttempts,
		limit,
	)
	result.Correlations = taskShadowDebugCorrelations(
		episodeDetail.Episode,
		result.Runs,
		result.TaskAgentAttempts,
		result.Learning,
		limit,
	)

	// These sets are intentionally populated while reading all selected records
	// so future debugger fields can add correlations without another DB pass.
	_ = observationIDSet
	_ = executionIDSet
	return result, nil
}
