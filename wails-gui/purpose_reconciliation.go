package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"yansu-agent/agent"
)

const (
	PurposeDispositionCreated      = "created"
	PurposeDispositionReinforced   = "reinforced"
	PurposeDispositionRevised      = "revised"
	PurposeDispositionWaiting      = "waiting"
	PurposeDispositionResolved     = "resolved"
	PurposeDispositionContradicted = "contradicted"
	PurposeDispositionDismissed    = "dismissed"
	PurposeDispositionDeduplicated = "deduplicated"
	PurposeDispositionNoChange     = "no_change"
	PurposeDispositionRejected     = "rejected"
)

type PurposeExtractionRunRecord struct {
	ID              string `json:"id"`
	IdempotencyKey  string `json:"idempotencyKey"`
	CutoffAt        int64  `json:"cutoffAt"`
	InputVersion    string `json:"inputVersion"`
	ManifestVersion string `json:"manifestVersion"`
	PromptVersion   string `json:"promptVersion"`
	OutputVersion   string `json:"outputVersion"`
	Model           string `json:"model"`
	Status          string `json:"status"`
	InputJSON       string `json:"inputJson"`
	InputHash       string `json:"inputHash"`
	ManifestJSON    string `json:"manifestJson"`
	ManifestHash    string `json:"manifestHash"`
	Prompt          string `json:"prompt"`
	PromptHash      string `json:"promptHash"`
	RawOutput       string `json:"rawOutput"`
	RawOutputHash   string `json:"rawOutputHash,omitempty"`
	OutputJSON      string `json:"outputJson"`
	OutputHash      string `json:"outputHash,omitempty"`
	Error           string `json:"error,omitempty"`
	StartedAt       int64  `json:"startedAt"`
	CompletedAt     int64  `json:"completedAt"`
	CreatedAt       int64  `json:"createdAt"`
	PayloadJSON     string `json:"payloadJson"`
	PayloadHash     string `json:"payloadHash"`
}

type PurposeExtractionHypothesisRecord struct {
	ID                      string   `json:"id"`
	RunID                   string   `json:"runId"`
	LocalKey                string   `json:"localKey"`
	CanonicalKey            string   `json:"canonicalKey"`
	DuplicateOfCanonicalKey string   `json:"duplicateOfCanonicalKey,omitempty"`
	Kind                    string   `json:"kind"`
	Operation               string   `json:"operation"`
	Title                   string   `json:"title"`
	DesiredOutcome          string   `json:"desiredOutcome"`
	Owner                   string   `json:"owner"`
	Counterparty            string   `json:"counterparty,omitempty"`
	Scope                   string   `json:"scope,omitempty"`
	DueKind                 string   `json:"dueKind"`
	DueValue                string   `json:"dueValue,omitempty"`
	WaitingCondition        string   `json:"waitingCondition,omitempty"`
	ReasoningStatus         string   `json:"reasoningStatus"`
	Rationale               string   `json:"rationale,omitempty"`
	Confidence              float64  `json:"confidence"`
	EvidenceRefs            []string `json:"evidenceRefs"`
	PayloadJSON             string   `json:"payloadJson"`
	PayloadHash             string   `json:"payloadHash"`
	CreatedAt               int64    `json:"createdAt"`
}

type PurposeReconciliationReceiptRecord struct {
	ID             string `json:"id"`
	IdempotencyKey string `json:"idempotencyKey"`
	RunID          string `json:"runId"`
	HypothesisID   string `json:"hypothesisId"`
	Disposition    string `json:"disposition"`
	OpenLoopID     string `json:"openLoopId,omitempty"`
	EventID        string `json:"eventId,omitempty"`
	RelationID     string `json:"relationId,omitempty"`
	Reason         string `json:"reason,omitempty"`
	SourceSetHash  string `json:"sourceSetHash"`
	OccurredAt     int64  `json:"occurredAt"`
	CreatedAt      int64  `json:"createdAt"`
	PayloadJSON    string `json:"payloadJson"`
	PayloadHash    string `json:"payloadHash"`
}

type PurposeExtractionRunDetail struct {
	Run        PurposeExtractionRunRecord           `json:"run"`
	Input      PurposeExtractionInput               `json:"input"`
	Manifest   PurposeExtractionManifest            `json:"manifest"`
	Output     *PurposeExtractionOutput             `json:"output,omitempty"`
	Hypotheses []PurposeExtractionHypothesisRecord  `json:"hypotheses"`
	Receipts   []PurposeReconciliationReceiptRecord `json:"receipts"`
}

type PurposeExtractionRunResult struct {
	Detail PurposeExtractionRunDetail `json:"detail"`
	Added  bool                       `json:"added"`
}

type PurposeExtractionRuntime struct {
	activity     *ActivityStore
	purpose      *PurposeMapStore
	runner       agent.AgentRunner
	model        string
	mailSubjects []string
}

func NewPurposeExtractionRuntime(
	activity *ActivityStore,
	runner agent.AgentRunner,
	model string,
) (*PurposeExtractionRuntime, error) {
	if activity == nil || activity.db == nil {
		return nil, errors.New("Purpose extraction runtime requires activity storage")
	}
	model = strings.TrimSpace(model)
	return &PurposeExtractionRuntime{
		activity: activity,
		purpose:  NewPurposeMapStore(activity.db),
		runner:   runner,
		model:    model,
	}, nil
}

type purposeExtractionRunPayload struct {
	ID              string `json:"id"`
	IdempotencyKey  string `json:"idempotencyKey"`
	CutoffAt        int64  `json:"cutoffAt"`
	InputVersion    string `json:"inputVersion"`
	ManifestVersion string `json:"manifestVersion"`
	PromptVersion   string `json:"promptVersion"`
	OutputVersion   string `json:"outputVersion"`
	Model           string `json:"model"`
	Status          string `json:"status"`
	InputHash       string `json:"inputHash"`
	ManifestHash    string `json:"manifestHash"`
	PromptHash      string `json:"promptHash"`
	RawOutputHash   string `json:"rawOutputHash,omitempty"`
	OutputHash      string `json:"outputHash,omitempty"`
	Error           string `json:"error,omitempty"`
	StartedAt       int64  `json:"startedAt"`
	CompletedAt     int64  `json:"completedAt"`
	CreatedAt       int64  `json:"createdAt"`
}

type purposeExtractionHypothesisPayload struct {
	ID                      string   `json:"id"`
	RunID                   string   `json:"runId"`
	LocalKey                string   `json:"localKey"`
	CanonicalKey            string   `json:"canonicalKey"`
	DuplicateOfCanonicalKey string   `json:"duplicateOfCanonicalKey,omitempty"`
	Kind                    string   `json:"kind"`
	Operation               string   `json:"operation"`
	Title                   string   `json:"title"`
	DesiredOutcome          string   `json:"desiredOutcome"`
	Owner                   string   `json:"owner"`
	Counterparty            string   `json:"counterparty,omitempty"`
	Scope                   string   `json:"scope,omitempty"`
	DueKind                 string   `json:"dueKind"`
	DueValue                string   `json:"dueValue,omitempty"`
	WaitingCondition        string   `json:"waitingCondition,omitempty"`
	ReasoningStatus         string   `json:"reasoningStatus"`
	Rationale               string   `json:"rationale,omitempty"`
	Confidence              float64  `json:"confidence"`
	EvidenceRefs            []string `json:"evidenceRefs"`
	CreatedAt               int64    `json:"createdAt"`
}

type purposeReconciliationReceiptPayload struct {
	ID             string `json:"id"`
	IdempotencyKey string `json:"idempotencyKey"`
	RunID          string `json:"runId"`
	HypothesisID   string `json:"hypothesisId"`
	Disposition    string `json:"disposition"`
	OpenLoopID     string `json:"openLoopId,omitempty"`
	EventID        string `json:"eventId,omitempty"`
	RelationID     string `json:"relationId,omitempty"`
	Reason         string `json:"reason,omitempty"`
	SourceSetHash  string `json:"sourceSetHash"`
	OccurredAt     int64  `json:"occurredAt"`
	CreatedAt      int64  `json:"createdAt"`
}

func purposeExtractionRunPayloadJSON(
	item PurposeExtractionRunRecord,
) (string, string, error) {
	return agencyStructPayload(purposeExtractionRunPayload{
		ID: item.ID, IdempotencyKey: item.IdempotencyKey, CutoffAt: item.CutoffAt,
		InputVersion: item.InputVersion, ManifestVersion: item.ManifestVersion,
		PromptVersion: item.PromptVersion, OutputVersion: item.OutputVersion,
		Model: item.Model, Status: item.Status, InputHash: item.InputHash,
		ManifestHash: item.ManifestHash, PromptHash: item.PromptHash,
		RawOutputHash: item.RawOutputHash, OutputHash: item.OutputHash,
		Error: item.Error, StartedAt: item.StartedAt, CompletedAt: item.CompletedAt,
		CreatedAt: item.CreatedAt,
	})
}

func purposeHypothesisPayloadJSON(
	item PurposeExtractionHypothesisRecord,
) (string, string, error) {
	return agencyStructPayload(purposeExtractionHypothesisPayload{
		ID: item.ID, RunID: item.RunID, LocalKey: item.LocalKey,
		CanonicalKey:            item.CanonicalKey,
		DuplicateOfCanonicalKey: item.DuplicateOfCanonicalKey,
		Kind:                    item.Kind, Operation: item.Operation, Title: item.Title,
		DesiredOutcome: item.DesiredOutcome, Owner: item.Owner,
		Counterparty: item.Counterparty, Scope: item.Scope,
		DueKind: item.DueKind, DueValue: item.DueValue,
		WaitingCondition: item.WaitingCondition,
		ReasoningStatus:  item.ReasoningStatus, Rationale: item.Rationale,
		Confidence:   item.Confidence,
		EvidenceRefs: append([]string(nil), item.EvidenceRefs...),
		CreatedAt:    item.CreatedAt,
	})
}

func purposeReceiptPayloadJSON(
	item PurposeReconciliationReceiptRecord,
) (string, string, error) {
	return agencyStructPayload(purposeReconciliationReceiptPayload{
		ID: item.ID, IdempotencyKey: item.IdempotencyKey,
		RunID: item.RunID, HypothesisID: item.HypothesisID,
		Disposition: item.Disposition, OpenLoopID: item.OpenLoopID,
		EventID: item.EventID, RelationID: item.RelationID,
		Reason: item.Reason, SourceSetHash: item.SourceSetHash,
		OccurredAt: item.OccurredAt, CreatedAt: item.CreatedAt,
	})
}

func rawPurposeHash(value string) string {
	if value == "" {
		return ""
	}
	return agencyPayloadHash([]byte(value))
}

func purposeSourceSetHash(
	hypothesis PurposeExtractionHypothesisRecord,
	sources map[string]PurposeExtractionSource,
) (string, error) {
	items := make([]string, 0, len(hypothesis.EvidenceRefs))
	for _, ref := range hypothesis.EvidenceRefs {
		source, ok := sources[ref]
		if !ok {
			return "", fmt.Errorf("Purpose source %q is unavailable", ref)
		}
		items = append(items, strings.Join([]string{
			ref, source.SourceKind, source.SourceID, source.SourceVersion,
			source.SourceHash, source.PromptContentHash,
		}, "\x00"))
	}
	sort.Strings(items)
	raw, _, err := agencyStructPayload(items)
	if err != nil {
		return "", err
	}
	return agencyPayloadHash([]byte(raw)), nil
}

const purposeExtractionRunSelectColumns = `
	id, idempotency_key, cutoff_at, input_version, manifest_version,
	prompt_version, output_version, model, status, input_json, input_hash,
	manifest_json, manifest_hash, prompt, prompt_hash, raw_output,
	raw_output_hash, output_json, output_hash, error, started_at, completed_at,
	created_at, payload_json, payload_hash`

const purposeHypothesisSelectColumns = `
	id, run_id, local_key, canonical_key, duplicate_of_canonical_key, kind,
	operation, title, desired_outcome, owner, counterparty, scope, due_kind,
	due_value, waiting_condition, reasoning_status, rationale, confidence,
	evidence_refs_json, payload_json, payload_hash, created_at`

const purposeReceiptSelectColumns = `
	id, idempotency_key, run_id, hypothesis_id, disposition, open_loop_id,
	event_id, relation_id, reason, source_set_hash, occurred_at, created_at,
	payload_json, payload_hash`

func scanPurposeExtractionRun(
	scanner interface{ Scan(dest ...any) error },
) (PurposeExtractionRunRecord, error) {
	var item PurposeExtractionRunRecord
	err := scanner.Scan(
		&item.ID, &item.IdempotencyKey, &item.CutoffAt, &item.InputVersion,
		&item.ManifestVersion, &item.PromptVersion, &item.OutputVersion,
		&item.Model, &item.Status, &item.InputJSON, &item.InputHash,
		&item.ManifestJSON, &item.ManifestHash, &item.Prompt, &item.PromptHash,
		&item.RawOutput, &item.RawOutputHash, &item.OutputJSON, &item.OutputHash,
		&item.Error, &item.StartedAt, &item.CompletedAt, &item.CreatedAt,
		&item.PayloadJSON, &item.PayloadHash,
	)
	return item, err
}

func scanPurposeHypothesis(
	scanner interface{ Scan(dest ...any) error },
) (PurposeExtractionHypothesisRecord, error) {
	var item PurposeExtractionHypothesisRecord
	var evidenceJSON string
	err := scanner.Scan(
		&item.ID, &item.RunID, &item.LocalKey, &item.CanonicalKey,
		&item.DuplicateOfCanonicalKey, &item.Kind, &item.Operation,
		&item.Title, &item.DesiredOutcome, &item.Owner, &item.Counterparty,
		&item.Scope, &item.DueKind, &item.DueValue, &item.WaitingCondition,
		&item.ReasoningStatus, &item.Rationale, &item.Confidence,
		&evidenceJSON, &item.PayloadJSON, &item.PayloadHash, &item.CreatedAt,
	)
	if err != nil {
		return PurposeExtractionHypothesisRecord{}, err
	}
	if err := json.Unmarshal([]byte(evidenceJSON), &item.EvidenceRefs); err != nil {
		return PurposeExtractionHypothesisRecord{}, fmt.Errorf(
			"decode Purpose hypothesis evidence: %w",
			err,
		)
	}
	return item, nil
}

func scanPurposeReceipt(
	scanner interface{ Scan(dest ...any) error },
) (PurposeReconciliationReceiptRecord, error) {
	var item PurposeReconciliationReceiptRecord
	err := scanner.Scan(
		&item.ID, &item.IdempotencyKey, &item.RunID, &item.HypothesisID,
		&item.Disposition, &item.OpenLoopID, &item.EventID, &item.RelationID,
		&item.Reason, &item.SourceSetHash, &item.OccurredAt, &item.CreatedAt,
		&item.PayloadJSON, &item.PayloadHash,
	)
	return item, err
}

func purposeSourcesByRef(input PurposeExtractionInput) map[string]PurposeExtractionSource {
	result := make(map[string]PurposeExtractionSource, len(input.Sources))
	for _, source := range input.Sources {
		result[source.Ref] = source
	}
	return result
}

func validatePurposeExtractionRun(
	item PurposeExtractionRunRecord,
) (PurposeExtractionInput, PurposeExtractionManifest, *PurposeExtractionOutput, error) {
	if item.ID != deterministicAgencyID("purpose-extraction-run", item.IdempotencyKey) ||
		item.CutoffAt <= 0 || item.StartedAt <= 0 || item.CompletedAt < item.StartedAt ||
		item.CreatedAt < item.CompletedAt {
		return PurposeExtractionInput{}, PurposeExtractionManifest{}, nil,
			fmt.Errorf("Purpose extraction run %q identity or timing is invalid", item.ID)
	}
	if !purposeExtractionVersionsValid(
		item.InputVersion,
		item.ManifestVersion,
		item.PromptVersion,
	) || item.OutputVersion != PurposeExtractionOutputVersion {
		return PurposeExtractionInput{}, PurposeExtractionManifest{}, nil,
			fmt.Errorf("Purpose extraction run %q version is invalid", item.ID)
	}
	if rawPurposeHash(item.InputJSON) != item.InputHash ||
		rawPurposeHash(item.ManifestJSON) != item.ManifestHash ||
		rawPurposeHash(item.Prompt) != item.PromptHash ||
		rawPurposeHash(item.RawOutput) != item.RawOutputHash ||
		rawPurposeHash(item.OutputJSON) != item.OutputHash {
		return PurposeExtractionInput{}, PurposeExtractionManifest{}, nil,
			fmt.Errorf("Purpose extraction run %q content hash is inconsistent", item.ID)
	}
	var input PurposeExtractionInput
	if err := json.Unmarshal([]byte(item.InputJSON), &input); err != nil {
		return PurposeExtractionInput{}, PurposeExtractionManifest{}, nil, err
	}
	var manifest PurposeExtractionManifest
	if err := json.Unmarshal([]byte(item.ManifestJSON), &manifest); err != nil {
		return PurposeExtractionInput{}, PurposeExtractionManifest{}, nil, err
	}
	if input.Version != item.InputVersion || input.CutoffAt != item.CutoffAt ||
		manifest.Version != item.ManifestVersion || manifest.CutoffAt != item.CutoffAt {
		return PurposeExtractionInput{}, PurposeExtractionManifest{}, nil,
			fmt.Errorf("Purpose extraction run %q input projection is inconsistent", item.ID)
	}
	if err := validatePurposeExtractionInputManifest(input, manifest); err != nil {
		return PurposeExtractionInput{}, PurposeExtractionManifest{}, nil,
			fmt.Errorf("validate Purpose extraction run %q source manifest: %w", item.ID, err)
	}
	prompt, err := buildPurposeExtractionPrompt(input)
	if err != nil {
		return PurposeExtractionInput{}, PurposeExtractionManifest{}, nil, err
	}
	if prompt != item.Prompt {
		return PurposeExtractionInput{}, PurposeExtractionManifest{}, nil,
			fmt.Errorf("Purpose extraction run %q prompt is inconsistent", item.ID)
	}
	var output *PurposeExtractionOutput
	switch item.Status {
	case PurposeExtractionStatusSucceeded:
		if item.OutputJSON == "" || item.Error != "" {
			return PurposeExtractionInput{}, PurposeExtractionManifest{}, nil,
				fmt.Errorf("Purpose extraction run %q success projection is invalid", item.ID)
		}
		parsed, err := decodePurposeExtractionOutput(item.OutputJSON, input)
		if err != nil {
			return PurposeExtractionInput{}, PurposeExtractionManifest{}, nil, err
		}
		output = &parsed
	case PurposeExtractionStatusFailed:
		if strings.TrimSpace(item.Error) == "" || item.OutputJSON != "" {
			return PurposeExtractionInput{}, PurposeExtractionManifest{}, nil,
				fmt.Errorf("Purpose extraction run %q failure projection is invalid", item.ID)
		}
	case PurposeExtractionStatusNoSources:
		if len(input.Sources) != 0 || item.OutputJSON != "" || item.RawOutput != "" || item.Error != "" {
			return PurposeExtractionInput{}, PurposeExtractionManifest{}, nil,
				fmt.Errorf("Purpose extraction run %q empty projection is invalid", item.ID)
		}
	default:
		return PurposeExtractionInput{}, PurposeExtractionManifest{}, nil,
			fmt.Errorf("Purpose extraction run %q status is invalid", item.ID)
	}
	payloadJSON, payloadHash, err := purposeExtractionRunPayloadJSON(item)
	if err != nil {
		return PurposeExtractionInput{}, PurposeExtractionManifest{}, nil, err
	}
	if payloadJSON != item.PayloadJSON || payloadHash != item.PayloadHash {
		return PurposeExtractionInput{}, PurposeExtractionManifest{}, nil,
			fmt.Errorf("Purpose extraction run %q payload is inconsistent", item.ID)
	}
	return input, manifest, output, nil
}

func hypothesisRecordAsOutput(
	item PurposeExtractionHypothesisRecord,
) PurposeExtractionHypothesis {
	return PurposeExtractionHypothesis{
		LocalKey: item.LocalKey, CanonicalKey: item.CanonicalKey,
		DuplicateOfCanonicalKey: item.DuplicateOfCanonicalKey,
		Kind:                    item.Kind, Operation: item.Operation, Title: item.Title,
		DesiredOutcome: item.DesiredOutcome, Owner: item.Owner,
		Counterparty: item.Counterparty, Scope: item.Scope,
		DueKind: item.DueKind, DueValue: item.DueValue,
		WaitingCondition: item.WaitingCondition,
		ReasoningStatus:  item.ReasoningStatus, Rationale: item.Rationale,
		Confidence:   item.Confidence,
		EvidenceRefs: append([]string(nil), item.EvidenceRefs...),
	}
}

func validatePurposeHypothesis(
	item PurposeExtractionHypothesisRecord,
	run PurposeExtractionRunRecord,
	input PurposeExtractionInput,
) error {
	if item.RunID != run.ID || item.CreatedAt != run.CreatedAt ||
		item.ID != deterministicAgencyID(
			"purpose-extraction-hypothesis",
			run.ID+"\x00"+item.LocalKey,
		) {
		return fmt.Errorf("Purpose hypothesis %q identity is invalid", item.ID)
	}
	normalized, err := normalizePurposeExtractionHypothesis(
		hypothesisRecordAsOutput(item),
		purposeSourcesByRef(input),
	)
	if err != nil {
		return err
	}
	if !purposeHypothesesEqual(normalized, hypothesisRecordAsOutput(item)) {
		return fmt.Errorf("Purpose hypothesis %q is not normalized", item.ID)
	}
	payloadJSON, payloadHash, err := purposeHypothesisPayloadJSON(item)
	if err != nil {
		return err
	}
	if payloadJSON != item.PayloadJSON || payloadHash != item.PayloadHash {
		return fmt.Errorf("Purpose hypothesis %q payload is inconsistent", item.ID)
	}
	return nil
}

func purposeHypothesesEqual(left, right PurposeExtractionHypothesis) bool {
	leftJSON, _, leftErr := agencyStructPayload(left)
	rightJSON, _, rightErr := agencyStructPayload(right)
	return leftErr == nil && rightErr == nil && leftJSON == rightJSON
}

func validatePurposeReceipt(
	item PurposeReconciliationReceiptRecord,
	run PurposeExtractionRunRecord,
	hypothesis PurposeExtractionHypothesisRecord,
	input PurposeExtractionInput,
) error {
	if item.RunID != run.ID || item.HypothesisID != hypothesis.ID ||
		item.IdempotencyKey != "purpose-reconcile:"+hypothesis.ID ||
		item.ID != deterministicAgencyID("purpose-reconciliation-receipt", item.IdempotencyKey) ||
		item.OccurredAt <= 0 || item.CreatedAt < item.OccurredAt {
		return fmt.Errorf("Purpose receipt %q identity or timing is invalid", item.ID)
	}
	switch item.Disposition {
	case PurposeDispositionCreated, PurposeDispositionReinforced,
		PurposeDispositionRevised, PurposeDispositionWaiting,
		PurposeDispositionResolved, PurposeDispositionContradicted,
		PurposeDispositionDismissed, PurposeDispositionDeduplicated,
		PurposeDispositionNoChange, PurposeDispositionRejected:
	default:
		return fmt.Errorf("Purpose receipt %q disposition is invalid", item.ID)
	}
	if item.Disposition == PurposeDispositionRejected && item.Reason == "" {
		return fmt.Errorf("Purpose receipt %q rejected without reason", item.ID)
	}
	if item.Disposition == PurposeDispositionNoChange && item.Reason == "" {
		return fmt.Errorf("Purpose receipt %q records no change without reason", item.ID)
	}
	expectedSourceSet, err := purposeSourceSetHash(hypothesis, purposeSourcesByRef(input))
	if err != nil {
		return err
	}
	if item.SourceSetHash != expectedSourceSet {
		return fmt.Errorf("Purpose receipt %q source set is inconsistent", item.ID)
	}
	payloadJSON, payloadHash, err := purposeReceiptPayloadJSON(item)
	if err != nil {
		return err
	}
	if payloadJSON != item.PayloadJSON || payloadHash != item.PayloadHash {
		return fmt.Errorf("Purpose receipt %q payload is inconsistent", item.ID)
	}
	return nil
}

func purposeReceiptEventType(disposition string) string {
	switch disposition {
	case PurposeDispositionCreated:
		return PurposeEventDiscovered
	case PurposeDispositionReinforced:
		return PurposeEventReinforced
	case PurposeDispositionRevised:
		return PurposeEventRevised
	case PurposeDispositionWaiting:
		return PurposeEventWaiting
	case PurposeDispositionResolved:
		return PurposeEventResolved
	case PurposeDispositionContradicted:
		return PurposeEventContradicted
	case PurposeDispositionDismissed:
		return PurposeEventDismissed
	default:
		return ""
	}
}

func purposeDetailEvent(
	details []PurposeOpenLoopDetail,
	eventID string,
) (PurposeOpenLoopDetail, PurposeOpenLoopEventRecord, bool) {
	for _, detail := range details {
		for _, event := range detail.Events {
			if event.ID == eventID {
				return detail, event, true
			}
		}
	}
	return PurposeOpenLoopDetail{}, PurposeOpenLoopEventRecord{}, false
}

func purposeRelationByID(
	details []PurposeOpenLoopDetail,
	relationID string,
) (PurposeRelationDetail, bool) {
	for _, detail := range details {
		for _, relation := range detail.Relations {
			if relation.Relation.ID == relationID {
				return relation, true
			}
		}
	}
	return PurposeRelationDetail{}, false
}

func validatePurposeReceiptClaim(
	item PurposeReconciliationReceiptRecord,
	hypothesis PurposeExtractionHypothesisRecord,
	input PurposeExtractionInput,
	details []PurposeOpenLoopDetail,
) error {
	sources := purposeSourcesByRef(input)
	findRoot := func(openLoopID string) (PurposeOpenLoopDetail, bool) {
		for _, detail := range details {
			if detail.OpenLoop.ID == openLoopID {
				return detail, true
			}
		}
		return PurposeOpenLoopDetail{}, false
	}

	switch item.Disposition {
	case PurposeDispositionRejected:
		if item.EventID != "" || item.RelationID != "" {
			return fmt.Errorf("Purpose receipt %q rejection claims a mutation", item.ID)
		}
		if item.OpenLoopID != "" {
			if _, ok := findRoot(item.OpenLoopID); !ok {
				return fmt.Errorf("Purpose receipt %q rejection target is missing", item.ID)
			}
		}
		return nil
	case PurposeDispositionNoChange:
		if item.OpenLoopID == "" || item.EventID != "" || item.RelationID != "" {
			return fmt.Errorf("Purpose receipt %q no-change claim is invalid", item.ID)
		}
		if _, ok := findRoot(item.OpenLoopID); !ok {
			return fmt.Errorf("Purpose receipt %q no-change target is missing", item.ID)
		}
		return nil
	case PurposeDispositionDeduplicated:
		if item.OpenLoopID == "" || item.EventID == "" || strings.TrimSpace(item.Reason) == "" {
			return fmt.Errorf("Purpose receipt %q duplicate claim is incomplete", item.ID)
		}
		target, ok := findRoot(item.OpenLoopID)
		if !ok {
			return fmt.Errorf("Purpose receipt %q duplicate target is missing", item.ID)
		}
		if target.OpenLoop.Kind != hypothesis.Kind ||
			!purposeHypothesisHasSourceOverlap(hypothesis, sources, target) {
			return fmt.Errorf("Purpose receipt %q duplicate evidence is inconsistent", item.ID)
		}
		eventDetail, event, found := purposeDetailEvent(details, item.EventID)
		if !found || eventDetail.OpenLoop.ID != item.OpenLoopID ||
			purposeNormalizedOutcome(event.DesiredOutcome) !=
				purposeNormalizedOutcome(hypothesis.DesiredOutcome) {
			return fmt.Errorf("Purpose receipt %q duplicate event is missing or inconsistent", item.ID)
		}
		if event.IdempotencyKey == purposeReconciliationEventKey(hypothesis.ID) {
			stance, err := purposeEvidenceStanceForOperation(hypothesis.Operation)
			if err != nil {
				return err
			}
			expected, err := purposeEvidenceInputsForHypothesis(hypothesis, sources, stance)
			if err != nil {
				return err
			}
			if !purposeEvidenceInputsMatch(purposeEventEvidence(eventDetail, event.ID), expected) {
				return fmt.Errorf("Purpose receipt %q duplicate event evidence differs", item.ID)
			}
		} else if !purposeEventContainsSourceOverlap(
			eventDetail,
			event.ID,
			hypothesis,
			sources,
		) {
			return fmt.Errorf("Purpose receipt %q duplicate event has no source overlap", item.ID)
		}
		if item.RelationID != "" {
			relation, found := purposeRelationByID(details, item.RelationID)
			if !found || relation.Relation.RelationType != PurposeRelationDuplicateOf ||
				relation.Relation.ObjectID != item.OpenLoopID {
				return fmt.Errorf("Purpose receipt %q duplicate relation is inconsistent", item.ID)
			}
		}
		return nil
	}

	expectedEventType := purposeReceiptEventType(item.Disposition)
	if expectedEventType == "" || item.OpenLoopID == "" || item.EventID == "" ||
		item.RelationID != "" {
		return fmt.Errorf("Purpose receipt %q mutation claim is incomplete", item.ID)
	}
	detail, event, found := purposeDetailEvent(details, item.EventID)
	if !found || detail.OpenLoop.ID != item.OpenLoopID ||
		detail.OpenLoop.CanonicalKey != hypothesis.CanonicalKey ||
		event.EventType != expectedEventType ||
		event.IdempotencyKey != purposeReconciliationEventKey(hypothesis.ID) ||
		event.OccurredAt != item.OccurredAt {
		return fmt.Errorf("Purpose receipt %q event claim is inconsistent", item.ID)
	}
	stance := PurposeStanceSupport
	if event.EventType != PurposeEventDiscovered {
		var err error
		stance, err = purposeEvidenceStanceForOperation(hypothesis.Operation)
		if err != nil {
			return err
		}
	}
	expectedEvidence, err := purposeEvidenceInputsForHypothesis(hypothesis, sources, stance)
	if err != nil {
		return err
	}
	if !purposeEvidenceInputsMatch(
		purposeEventEvidence(detail, event.ID),
		expectedEvidence,
	) {
		return fmt.Errorf("Purpose receipt %q event evidence differs", item.ID)
	}
	return nil
}

func loadPurposeExtractionRun(
	db *sql.DB,
	runID string,
) (PurposeExtractionRunDetail, error) {
	if db == nil {
		return PurposeExtractionRunDetail{}, errors.New("Purpose extraction storage is unavailable")
	}
	run, err := scanPurposeExtractionRun(db.QueryRow(`SELECT `+
		purposeExtractionRunSelectColumns+` FROM purpose_extraction_runs_v1 WHERE id = ?`, runID))
	if err != nil {
		return PurposeExtractionRunDetail{}, err
	}
	input, manifest, output, err := validatePurposeExtractionRun(run)
	if err != nil {
		return PurposeExtractionRunDetail{}, err
	}
	rows, err := db.Query(`SELECT `+purposeHypothesisSelectColumns+`
		FROM purpose_extraction_hypotheses_v1 WHERE run_id = ? ORDER BY local_key`, run.ID)
	if err != nil {
		return PurposeExtractionRunDetail{}, err
	}
	hypotheses := make([]PurposeExtractionHypothesisRecord, 0)
	for rows.Next() {
		item, scanErr := scanPurposeHypothesis(rows)
		if scanErr != nil {
			rows.Close()
			return PurposeExtractionRunDetail{}, scanErr
		}
		if err := validatePurposeHypothesis(item, run, input); err != nil {
			rows.Close()
			return PurposeExtractionRunDetail{}, err
		}
		hypotheses = append(hypotheses, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PurposeExtractionRunDetail{}, err
	}
	if err := rows.Close(); err != nil {
		return PurposeExtractionRunDetail{}, err
	}
	if output == nil && len(hypotheses) != 0 {
		return PurposeExtractionRunDetail{}, errors.New("non-successful Purpose extraction retained hypotheses")
	}
	if output != nil {
		if len(output.Hypotheses) != len(hypotheses) {
			return PurposeExtractionRunDetail{}, errors.New("Purpose extraction hypothesis count is inconsistent")
		}
		for index := range hypotheses {
			if !purposeHypothesesEqual(
				output.Hypotheses[index],
				hypothesisRecordAsOutput(hypotheses[index]),
			) {
				return PurposeExtractionRunDetail{}, errors.New("Purpose extraction output and hypotheses differ")
			}
		}
	}
	hypothesisByID := make(map[string]PurposeExtractionHypothesisRecord, len(hypotheses))
	for _, hypothesis := range hypotheses {
		hypothesisByID[hypothesis.ID] = hypothesis
	}
	receiptRows, err := db.Query(`SELECT `+purposeReceiptSelectColumns+`
		FROM purpose_reconciliation_receipts_v1 WHERE run_id = ? ORDER BY occurred_at, id`, run.ID)
	if err != nil {
		return PurposeExtractionRunDetail{}, err
	}
	receipts := make([]PurposeReconciliationReceiptRecord, 0)
	for receiptRows.Next() {
		item, scanErr := scanPurposeReceipt(receiptRows)
		if scanErr != nil {
			receiptRows.Close()
			return PurposeExtractionRunDetail{}, scanErr
		}
		hypothesis, ok := hypothesisByID[item.HypothesisID]
		if !ok {
			receiptRows.Close()
			return PurposeExtractionRunDetail{}, fmt.Errorf("Purpose receipt %q has no hypothesis", item.ID)
		}
		if err := validatePurposeReceipt(item, run, hypothesis, input); err != nil {
			receiptRows.Close()
			return PurposeExtractionRunDetail{}, err
		}
		receipts = append(receipts, item)
	}
	if err := receiptRows.Err(); err != nil {
		receiptRows.Close()
		return PurposeExtractionRunDetail{}, err
	}
	if err := receiptRows.Close(); err != nil {
		return PurposeExtractionRunDetail{}, err
	}
	if len(receipts) > 0 {
		purposeDetails, err := loadPurposeLedger(db)
		if err != nil {
			return PurposeExtractionRunDetail{}, err
		}
		for _, receipt := range receipts {
			hypothesis := hypothesisByID[receipt.HypothesisID]
			if err := validatePurposeReceiptClaim(
				receipt,
				hypothesis,
				input,
				purposeDetails,
			); err != nil {
				return PurposeExtractionRunDetail{}, err
			}
		}
	}
	return PurposeExtractionRunDetail{
		Run: run, Input: input, Manifest: manifest, Output: output,
		Hypotheses: hypotheses, Receipts: receipts,
	}, nil
}

func loadPurposeExtractionRunByIdempotency(
	db *sql.DB,
	key string,
) (PurposeExtractionRunDetail, error) {
	var runID string
	if err := db.QueryRow(`SELECT id FROM purpose_extraction_runs_v1 WHERE idempotency_key = ?`, key).Scan(&runID); err != nil {
		return PurposeExtractionRunDetail{}, err
	}
	return loadPurposeExtractionRun(db, runID)
}
