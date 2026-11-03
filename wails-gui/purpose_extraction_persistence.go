package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const purposeExtractionMaxErrorBytes = 8_000

func purposeExtractionErrorText(err error) string {
	if err == nil {
		return ""
	}
	value := strings.TrimSpace(err.Error())
	if value == "" {
		value = "Purpose extraction failed"
	}
	return truncateAtRuneBoundary(value, purposeExtractionMaxErrorBytes)
}

func purposeExtractionRecords(
	request PurposeExtractionRequest,
	model string,
	execution PurposeExtractionExecution,
	executionErr error,
) (PurposeExtractionRunRecord, []PurposeExtractionHypothesisRecord, error) {
	inputJSON, inputHash, err := agencyStructPayload(execution.Input)
	if err != nil {
		return PurposeExtractionRunRecord{}, nil, err
	}
	manifestJSON, manifestHash, err := agencyStructPayload(execution.Manifest)
	if err != nil {
		return PurposeExtractionRunRecord{}, nil, err
	}
	if err := validatePurposeExtractionInputManifest(
		execution.Input,
		execution.Manifest,
	); err != nil {
		return PurposeExtractionRunRecord{}, nil, err
	}
	promptVersion, ok := purposeExtractionPromptVersionForInput(
		execution.Input.Version,
	)
	if !ok || !purposeExtractionVersionsValid(
		execution.Input.Version,
		execution.Manifest.Version,
		promptVersion,
	) {
		return PurposeExtractionRunRecord{}, nil, errors.New(
			"Purpose extraction version combination is invalid",
		)
	}
	if strings.TrimSpace(execution.Prompt) == "" {
		return PurposeExtractionRunRecord{}, nil, errors.New("Purpose extraction prompt is required")
	}
	if execution.StartedAt <= 0 || execution.CompletedAt < execution.StartedAt {
		return PurposeExtractionRunRecord{}, nil, errors.New("Purpose extraction timing is invalid")
	}

	status := PurposeExtractionStatusSucceeded
	if executionErr != nil {
		status = PurposeExtractionStatusFailed
	} else if len(execution.Input.Sources) == 0 {
		status = PurposeExtractionStatusNoSources
	} else if execution.Output == nil {
		return PurposeExtractionRunRecord{}, nil, errors.New("successful Purpose extraction has no output")
	}
	outputJSON := ""
	outputHash := ""
	if status == PurposeExtractionStatusSucceeded {
		outputJSON, outputHash, err = agencyStructPayload(*execution.Output)
		if err != nil {
			return PurposeExtractionRunRecord{}, nil, err
		}
	}
	createdAt := time.Now().UnixMilli()
	if createdAt < execution.CompletedAt {
		createdAt = execution.CompletedAt
	}
	run := PurposeExtractionRunRecord{
		ID:              deterministicAgencyID("purpose-extraction-run", request.IdempotencyKey),
		IdempotencyKey:  request.IdempotencyKey,
		CutoffAt:        request.CutoffAt,
		InputVersion:    execution.Input.Version,
		ManifestVersion: execution.Manifest.Version,
		PromptVersion:   promptVersion,
		OutputVersion:   PurposeExtractionOutputVersion,
		Model:           strings.TrimSpace(model),
		Status:          status,
		InputJSON:       inputJSON,
		InputHash:       inputHash,
		ManifestJSON:    manifestJSON,
		ManifestHash:    manifestHash,
		Prompt:          execution.Prompt,
		PromptHash:      rawPurposeHash(execution.Prompt),
		RawOutput:       execution.RawOutput,
		RawOutputHash:   rawPurposeHash(execution.RawOutput),
		OutputJSON:      outputJSON,
		OutputHash:      outputHash,
		Error:           purposeExtractionErrorText(executionErr),
		StartedAt:       execution.StartedAt,
		CompletedAt:     execution.CompletedAt,
		CreatedAt:       createdAt,
	}
	run.PayloadJSON, run.PayloadHash, err = purposeExtractionRunPayloadJSON(run)
	if err != nil {
		return PurposeExtractionRunRecord{}, nil, err
	}
	if _, _, _, err := validatePurposeExtractionRun(run); err != nil {
		return PurposeExtractionRunRecord{}, nil, err
	}

	hypotheses := make([]PurposeExtractionHypothesisRecord, 0)
	if execution.Output != nil && status == PurposeExtractionStatusSucceeded {
		hypotheses = make([]PurposeExtractionHypothesisRecord, 0, len(execution.Output.Hypotheses))
		for _, output := range execution.Output.Hypotheses {
			item := PurposeExtractionHypothesisRecord{
				ID: deterministicAgencyID(
					"purpose-extraction-hypothesis",
					run.ID+"\x00"+output.LocalKey,
				),
				RunID:                   run.ID,
				LocalKey:                output.LocalKey,
				CanonicalKey:            output.CanonicalKey,
				DuplicateOfCanonicalKey: output.DuplicateOfCanonicalKey,
				Kind:                    output.Kind,
				Operation:               output.Operation,
				Title:                   output.Title,
				DesiredOutcome:          output.DesiredOutcome,
				Owner:                   output.Owner,
				Counterparty:            output.Counterparty,
				Scope:                   output.Scope,
				DueKind:                 output.DueKind,
				DueValue:                output.DueValue,
				WaitingCondition:        output.WaitingCondition,
				ReasoningStatus:         output.ReasoningStatus,
				Rationale:               output.Rationale,
				Confidence:              output.Confidence,
				EvidenceRefs:            append([]string(nil), output.EvidenceRefs...),
				CreatedAt:               run.CreatedAt,
			}
			item.PayloadJSON, item.PayloadHash, err = purposeHypothesisPayloadJSON(item)
			if err != nil {
				return PurposeExtractionRunRecord{}, nil, err
			}
			if err := validatePurposeHypothesis(item, run, execution.Input); err != nil {
				return PurposeExtractionRunRecord{}, nil, err
			}
			hypotheses = append(hypotheses, item)
		}
	}
	return run, hypotheses, nil
}

func insertPurposeExtractionRunTx(
	tx *sql.Tx,
	item PurposeExtractionRunRecord,
) error {
	_, err := tx.Exec(`INSERT INTO purpose_extraction_runs_v1 (
		id, idempotency_key, cutoff_at, input_version, manifest_version,
		prompt_version, output_version, model, status, input_json, input_hash,
		manifest_json, manifest_hash, prompt, prompt_hash, raw_output,
		raw_output_hash, output_json, output_hash, error, started_at, completed_at,
		created_at, payload_json, payload_hash
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.IdempotencyKey, item.CutoffAt, item.InputVersion,
		item.ManifestVersion, item.PromptVersion, item.OutputVersion, item.Model,
		item.Status, item.InputJSON, item.InputHash, item.ManifestJSON,
		item.ManifestHash, item.Prompt, item.PromptHash, item.RawOutput,
		item.RawOutputHash, item.OutputJSON, item.OutputHash, item.Error,
		item.StartedAt, item.CompletedAt, item.CreatedAt, item.PayloadJSON,
		item.PayloadHash,
	)
	return err
}

func insertPurposeHypothesisTx(
	tx *sql.Tx,
	item PurposeExtractionHypothesisRecord,
) error {
	evidenceJSON, _, err := agencyStructPayload(item.EvidenceRefs)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO purpose_extraction_hypotheses_v1 (
		id, run_id, local_key, canonical_key, duplicate_of_canonical_key, kind,
		operation, title, desired_outcome, owner, counterparty, scope, due_kind,
		due_value, waiting_condition, reasoning_status, rationale, confidence,
		evidence_refs_json, payload_json, payload_hash, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.RunID, item.LocalKey, item.CanonicalKey,
		item.DuplicateOfCanonicalKey, item.Kind, item.Operation, item.Title,
		item.DesiredOutcome, item.Owner, item.Counterparty, item.Scope,
		item.DueKind, item.DueValue, item.WaitingCondition, item.ReasoningStatus,
		item.Rationale, item.Confidence, evidenceJSON, item.PayloadJSON,
		item.PayloadHash, item.CreatedAt,
	)
	return err
}

func persistPurposeExtractionRun(
	db *sql.DB,
	run PurposeExtractionRunRecord,
	hypotheses []PurposeExtractionHypothesisRecord,
) (PurposeExtractionRunDetail, bool, error) {
	memoryMutationMu.RLock()
	defer memoryMutationMu.RUnlock()
	if db == nil {
		return PurposeExtractionRunDetail{}, false, errors.New("Purpose extraction storage is unavailable")
	}
	var input PurposeExtractionInput
	if err := json.Unmarshal([]byte(run.InputJSON), &input); err != nil {
		return PurposeExtractionRunDetail{}, false, err
	}
	for _, source := range input.Sources {
		if err := validateMemoryForgetSource(db, source.SourceKind, source.SourceID, source.Content, source.Ref); err != nil {
			return PurposeExtractionRunDetail{}, false, err
		}
	}
	for _, current := range input.CurrentPurpose {
		if err := validateMemoryForgetSource(db, "purpose_open_loop", current.OpenLoopID, ""); err != nil {
			return PurposeExtractionRunDetail{}, false, err
		}
	}
	if existing, err := loadPurposeExtractionRunByIdempotency(db, run.IdempotencyKey); err == nil {
		if existing.Run.PayloadHash != run.PayloadHash {
			return PurposeExtractionRunDetail{}, false, errors.New("Purpose extraction idempotency conflict")
		}
		return existing, false, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return PurposeExtractionRunDetail{}, false, err
	}
	tx, err := db.Begin()
	if err != nil {
		return PurposeExtractionRunDetail{}, false, err
	}
	defer tx.Rollback()
	if err := insertPurposeExtractionRunTx(tx, run); err != nil {
		return PurposeExtractionRunDetail{}, false, err
	}
	for _, hypothesis := range hypotheses {
		if err := insertPurposeHypothesisTx(tx, hypothesis); err != nil {
			return PurposeExtractionRunDetail{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return PurposeExtractionRunDetail{}, false, err
	}
	detail, err := loadPurposeExtractionRun(db, run.ID)
	if err != nil {
		return PurposeExtractionRunDetail{}, false, err
	}
	return detail, true, nil
}

func purposeExtractionRequestMatches(
	detail PurposeExtractionRunDetail,
	request PurposeExtractionRequest,
	model string,
) bool {
	return detail.Run.IdempotencyKey == request.IdempotencyKey &&
		detail.Run.CutoffAt == request.CutoffAt &&
		detail.Input.Query == request.Query &&
		detail.Run.Model == strings.TrimSpace(model)
}

func (runtime *PurposeExtractionRuntime) existingRunForRequest(
	request PurposeExtractionRequest,
) (PurposeExtractionRunResult, bool, error) {
	existing, err := loadPurposeExtractionRunByIdempotency(
		runtime.activity.db,
		request.IdempotencyKey,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PurposeExtractionRunResult{}, false, nil
	}
	if err != nil {
		return PurposeExtractionRunResult{}, false, err
	}
	if !purposeExtractionRequestMatches(existing, request, runtime.model) {
		return PurposeExtractionRunResult{}, true, errors.New(
			"Purpose extraction idempotency conflict",
		)
	}
	if existing.Run.Status == PurposeExtractionStatusSucceeded {
		existing, err = runtime.Reconcile(existing.Run.ID)
		if err != nil {
			return PurposeExtractionRunResult{
				Detail: existing,
				Added:  false,
			}, true, err
		}
	}
	result := PurposeExtractionRunResult{Detail: existing, Added: false}
	if existing.Run.Status == PurposeExtractionStatusFailed {
		return result, true, errors.New(existing.Run.Error)
	}
	return result, true, nil
}

// runFrozen invokes extraction against one already-validated source snapshot.
// The scheduler uses this path so its stable fingerprint and model prompt can
// never refer to two different gathers if durable context changes mid-cycle.
func (runtime *PurposeExtractionRuntime) runFrozen(
	ctx context.Context,
	request PurposeExtractionRequest,
	input PurposeExtractionInput,
	manifest PurposeExtractionManifest,
) (PurposeExtractionRunResult, error) {
	if runtime == nil || runtime.activity == nil || runtime.activity.db == nil {
		return PurposeExtractionRunResult{}, errors.New("Purpose extraction runtime is unavailable")
	}
	request, err := normalizePurposeExtractionRequest(request)
	if err != nil {
		return PurposeExtractionRunResult{}, err
	}
	if input.CutoffAt != request.CutoffAt ||
		manifest.CutoffAt != request.CutoffAt ||
		input.Query != request.Query {
		return PurposeExtractionRunResult{}, errors.New(
			"Purpose frozen extraction input does not match its request",
		)
	}
	if err := validatePurposeExtractionInputManifest(input, manifest); err != nil {
		return PurposeExtractionRunResult{}, err
	}
	if existing, found, err := runtime.existingRunForRequest(request); found || err != nil {
		return existing, err
	}

	execution, executionErr := runPurposeExtractionModel(
		ctx,
		runtime.runner,
		runtime.model,
		input,
		manifest,
	)
	if errors.Is(executionErr, context.Canceled) ||
		errors.Is(executionErr, context.DeadlineExceeded) {
		return PurposeExtractionRunResult{}, executionErr
	}
	run, hypotheses, recordErr := purposeExtractionRecords(
		request,
		runtime.model,
		execution,
		executionErr,
	)
	if recordErr != nil {
		return PurposeExtractionRunResult{}, recordErr
	}
	detail, added, err := persistPurposeExtractionRun(
		runtime.activity.db,
		run,
		hypotheses,
	)
	if err != nil {
		return PurposeExtractionRunResult{}, err
	}
	result := PurposeExtractionRunResult{Detail: detail, Added: added}
	if executionErr != nil {
		return result, executionErr
	}
	if detail.Run.Status == PurposeExtractionStatusSucceeded {
		detail, err = runtime.Reconcile(detail.Run.ID)
		result.Detail = detail
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

// Run freezes one complete source projection, invokes a no-tools model at most
// once for the idempotency key, persists the hypotheses, and then applies only
// deterministic reconciliation. A stored failed run is returned rather than
// silently retried under the same key.
func (runtime *PurposeExtractionRuntime) Run(
	ctx context.Context,
	request PurposeExtractionRequest,
) (PurposeExtractionRunResult, error) {
	if runtime == nil || runtime.activity == nil || runtime.activity.db == nil {
		return PurposeExtractionRunResult{}, errors.New("Purpose extraction runtime is unavailable")
	}
	request, err := normalizePurposeExtractionRequest(request)
	if err != nil {
		return PurposeExtractionRunResult{}, err
	}
	if existing, found, err := runtime.existingRunForRequest(request); found || err != nil {
		return existing, err
	}

	input, manifest, err := gatherPurposeExtractionInput(runtime.activity, request)
	if err != nil {
		return PurposeExtractionRunResult{}, err
	}
	input.MailPrivacy = newPurposeExtractionMailPrivacy(runtime.mailSubjects)
	return runtime.runFrozen(ctx, request, input, manifest)
}

func (runtime *PurposeExtractionRuntime) GetRun(
	runID string,
) (PurposeExtractionRunDetail, error) {
	if runtime == nil || runtime.activity == nil || runtime.activity.db == nil {
		return PurposeExtractionRunDetail{}, errors.New("Purpose extraction runtime is unavailable")
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return PurposeExtractionRunDetail{}, errors.New("Purpose extraction run ID is required")
	}
	return loadPurposeExtractionRun(runtime.activity.db, runID)
}

func (runtime *PurposeExtractionRuntime) ListRuns(
	limit int,
) ([]PurposeExtractionRunDetail, error) {
	if runtime == nil || runtime.activity == nil || runtime.activity.db == nil {
		return nil, errors.New("Purpose extraction runtime is unavailable")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := runtime.activity.db.Query(`
		SELECT id FROM purpose_extraction_runs_v1
		ORDER BY created_at DESC, id DESC LIMIT ?`, limit)
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
	result := make([]PurposeExtractionRunDetail, 0, len(ids))
	for _, id := range ids {
		detail, err := loadPurposeExtractionRun(runtime.activity.db, id)
		if err != nil {
			return nil, err
		}
		result = append(result, detail)
	}
	return result, nil
}
