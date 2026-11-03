package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	initiativeLearningMemoryBudgetChars          = 64_000
	initiativeCalibrationComparisonConfidence    = 0.70
	initiativeCalibrationMinimumEvaluatedSamples = 5
)

type InitiativeOutcome struct {
	ID                   string               `json:"id"`
	InitiativeID         string               `json:"initiativeId"`
	SourceKind           string               `json:"sourceKind"`
	SourceID             string               `json:"sourceId"`
	ObservedAction       string               `json:"observedAction"`
	ObservedResult       string               `json:"observedResult"`
	Assessment           string               `json:"assessment"`
	AssessmentReason     string               `json:"assessmentReason"`
	Confidence           float64              `json:"confidence"`
	PredictionConfidence *float64             `json:"predictionConfidence,omitempty"`
	Evidence             []InitiativeEvidence `json:"evidence"`
	CreatedAt            int64                `json:"createdAt"`
}

type InitiativeCalibrationBand struct {
	Key                           string  `json:"key"`
	MinConfidence                 float64 `json:"minConfidence"`
	MaxConfidence                 float64 `json:"maxConfidence"`
	SampleCount                   int     `json:"sampleCount"`
	EvaluatedSamples              int     `json:"evaluatedSamples"`
	ExcludedLowConfidenceOutcomes int     `json:"excludedLowConfidenceOutcomes"`
	MatchedCount                  int     `json:"matchedCount"`
	PartialCount                  int     `json:"partialCount"`
	DifferentCount                int     `json:"differentCount"`
	UnclearCount                  int     `json:"unclearCount"`
	Reliability                   float64 `json:"reliability"`
	Ready                         bool    `json:"ready"`
}

type InitiativeCalibrationSummary struct {
	Kind                          string                      `json:"kind"`
	SourceKind                    string                      `json:"sourceKind"`
	TotalOutcomes                 int                         `json:"totalOutcomes"`
	SampleCount                   int                         `json:"sampleCount"`
	EvaluatedSamples              int                         `json:"evaluatedSamples"`
	ExcludedLowConfidenceOutcomes int                         `json:"excludedLowConfidenceOutcomes"`
	MatchedCount                  int                         `json:"matchedCount"`
	PartialCount                  int                         `json:"partialCount"`
	DifferentCount                int                         `json:"differentCount"`
	UnclearCount                  int                         `json:"unclearCount"`
	Reliability                   float64                     `json:"reliability"`
	Ready                         bool                        `json:"ready"`
	MinimumEvaluatedSamples       int                         `json:"minimumEvaluatedSamples"`
	Bands                         []InitiativeCalibrationBand `json:"bands"`
}

type InitiativeActionReceipt struct {
	ID            string   `json:"id"`
	InitiativeID  string   `json:"initiativeId"`
	ExecutionRef  string   `json:"executionRef"`
	RunID         string   `json:"runId"`
	Status        string   `json:"status"`
	TriggerKind   string   `json:"triggerKind"`
	StartedAt     int64    `json:"startedAt"`
	FinishedAt    int64    `json:"finishedAt,omitempty"`
	DurationMs    int64    `json:"durationMs"`
	ResultSummary string   `json:"resultSummary"`
	Error         string   `json:"error"`
	SessionTag    string   `json:"sessionTag"`
	ItemSortStart int      `json:"itemSortStart"`
	Artifacts     []string `json:"artifacts"`
	SideEffects   []any    `json:"sideEffects"`
	CreatedAt     int64    `json:"createdAt"`
	UpdatedAt     int64    `json:"updatedAt"`
}

type CronRunLifecycleEvent struct {
	JobID string
	// JobName and SurfaceKind come from the scheduler's own job record.
	JobName        string
	SurfaceKind    string
	RunID          string
	Status         string
	TriggerKind    string
	StartedAt      int64
	FinishedAt     int64
	DurationMs     int64
	ResultSummary  string
	Error          string
	SessionTag     string
	ItemSortStart  int
	FilePaths      []string
	DeliveryTarget *DeliveryTarget
}

func normalizeInitiativeAssessment(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "matched", "partial", "different", "unclear":
		return value, nil
	default:
		return "", fmt.Errorf("unsupported initiative assessment %q", value)
	}
}

func scanInitiativeOutcome(scanner interface{ Scan(dest ...any) error }) (InitiativeOutcome, error) {
	var outcome InitiativeOutcome
	var evidenceJSON string
	var predictionConfidence sql.NullFloat64
	err := scanner.Scan(
		&outcome.ID,
		&outcome.InitiativeID,
		&outcome.SourceKind,
		&outcome.SourceID,
		&outcome.ObservedAction,
		&outcome.ObservedResult,
		&outcome.Assessment,
		&outcome.AssessmentReason,
		&outcome.Confidence,
		&predictionConfidence,
		&evidenceJSON,
		&outcome.CreatedAt,
	)
	if err != nil {
		return InitiativeOutcome{}, err
	}
	outcome.Confidence = clampConfidence(outcome.Confidence)
	if predictionConfidence.Valid {
		value := clampConfidence(predictionConfidence.Float64)
		outcome.PredictionConfidence = &value
	}
	decodeInitiativeJSON(evidenceJSON, &outcome.Evidence)
	return outcome, nil
}

const initiativeOutcomeSelectColumns = `
	id, initiative_id, source_kind, source_id, observed_action, observed_result,
	assessment, assessment_reason, confidence, prediction_confidence,
	evidence_json, created_at`

func (s *ActivityStore) SaveInitiativeOutcome(outcome InitiativeOutcome) (InitiativeOutcome, bool, error) {
	if strings.TrimSpace(outcome.InitiativeID) == "" {
		return InitiativeOutcome{}, false, errors.New("initiative outcome requires an initiative id")
	}
	if strings.TrimSpace(outcome.SourceKind) == "" || strings.TrimSpace(outcome.SourceID) == "" {
		return InitiativeOutcome{}, false, errors.New("initiative outcome requires a source")
	}
	if strings.TrimSpace(outcome.ObservedAction) == "" {
		return InitiativeOutcome{}, false, errors.New("initiative outcome requires an observed action")
	}
	assessment, err := normalizeInitiativeAssessment(outcome.Assessment)
	if err != nil {
		return InitiativeOutcome{}, false, err
	}
	outcome.Assessment = assessment
	outcome.Confidence = clampConfidence(outcome.Confidence)
	if outcome.PredictionConfidence != nil {
		value := clampConfidence(*outcome.PredictionConfidence)
		outcome.PredictionConfidence = &value
	}
	if outcome.ID == "" {
		outcome.ID = uuid.New().String()
	}
	if outcome.CreatedAt == 0 {
		outcome.CreatedAt = time.Now().UnixMilli()
	}

	tx, err := s.db.Begin()
	if err != nil {
		return InitiativeOutcome{}, false, fmt.Errorf("begin initiative outcome: %w", err)
	}
	defer tx.Rollback()

	var initiativeEvidenceJSON string
	var currentPredictionConfidence float64
	if err := tx.QueryRow(
		`SELECT evidence_json, COALESCE(raw_model_confidence, confidence)
		FROM initiatives WHERE id = ?`,
		outcome.InitiativeID,
	).Scan(&initiativeEvidenceJSON, &currentPredictionConfidence); err != nil {
		return InitiativeOutcome{}, false, err
	}
	if outcome.PredictionConfidence == nil {
		value := clampConfidence(currentPredictionConfidence)
		outcome.PredictionConfidence = &value
	}
	result, err := tx.Exec(`
		INSERT OR IGNORE INTO initiative_outcomes (
			id, initiative_id, source_kind, source_id, observed_action, observed_result,
			assessment, assessment_reason, confidence, prediction_confidence,
			evidence_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		outcome.ID, outcome.InitiativeID, outcome.SourceKind, outcome.SourceID,
		outcome.ObservedAction, outcome.ObservedResult, outcome.Assessment,
		outcome.AssessmentReason, outcome.Confidence, outcome.PredictionConfidence,
		encodeInitiativeJSON(outcome.Evidence, "[]"), outcome.CreatedAt,
	)
	if err != nil {
		return InitiativeOutcome{}, false, fmt.Errorf("save initiative outcome: %w", err)
	}
	added, err := result.RowsAffected()
	if err != nil {
		return InitiativeOutcome{}, false, fmt.Errorf("initiative outcome rows affected: %w", err)
	}
	if added == 0 {
		if err := tx.Rollback(); err != nil {
			return InitiativeOutcome{}, false, err
		}
		existing, getErr := s.GetInitiativeOutcomeBySource(outcome.InitiativeID, outcome.SourceKind, outcome.SourceID)
		return existing, false, getErr
	}

	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO initiative_observations (
			initiative_id, source_kind, source_id, evidence_json, confidence, observed_at
		) VALUES (?, ?, ?, ?, ?, ?)`,
		outcome.InitiativeID, outcome.SourceKind, outcome.SourceID,
		encodeInitiativeJSON(outcome.Evidence, "[]"), outcome.Confidence, outcome.CreatedAt,
	); err != nil {
		return InitiativeOutcome{}, false, fmt.Errorf("link outcome observation: %w", err)
	}

	var initiativeEvidence []InitiativeEvidence
	decodeInitiativeJSON(initiativeEvidenceJSON, &initiativeEvidence)
	initiativeEvidence = mergeInitiativeEvidence(initiativeEvidence, outcome.Evidence)
	if _, err := tx.Exec(`
		UPDATE initiatives
		SET evidence_json = ?, updated_at = ?
		WHERE id = ?`,
		encodeInitiativeJSON(initiativeEvidence, "[]"), outcome.CreatedAt, outcome.InitiativeID,
	); err != nil {
		return InitiativeOutcome{}, false, fmt.Errorf("update initiative outcome evidence: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return InitiativeOutcome{}, false, fmt.Errorf("commit initiative outcome: %w", err)
	}
	return outcome, true, nil
}

func (s *ActivityStore) GetInitiativeOutcomeBySource(initiativeID, sourceKind, sourceID string) (InitiativeOutcome, error) {
	return scanInitiativeOutcome(s.db.QueryRow(`
		SELECT `+initiativeOutcomeSelectColumns+`
		FROM initiative_outcomes
		WHERE initiative_id = ? AND source_kind = ? AND source_id = ?`,
		initiativeID, sourceKind, sourceID))
}

func (s *ActivityStore) GetLatestInitiativeOutcome(initiativeID string) (InitiativeOutcome, error) {
	return scanInitiativeOutcome(s.db.QueryRow(`
		SELECT `+initiativeOutcomeSelectColumns+`
		FROM initiative_outcomes
		WHERE initiative_id = ?
		ORDER BY created_at DESC, rowid DESC
		LIMIT 1`, initiativeID))
}

func (s *ActivityStore) ListInitiativeOutcomes(initiativeID string, limit int) ([]InitiativeOutcome, error) {
	query := `SELECT ` + initiativeOutcomeSelectColumns + `
		FROM initiative_outcomes
		WHERE initiative_id = ?
		ORDER BY created_at DESC, rowid DESC`
	args := []any{initiativeID}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	outcomes := make([]InitiativeOutcome, 0)
	for rows.Next() {
		outcome, err := scanInitiativeOutcome(rows)
		if err != nil {
			return nil, err
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes, rows.Err()
}

func initiativeCalibrationBands() []InitiativeCalibrationBand {
	return []InitiativeCalibrationBand{
		{Key: "low", MinConfidence: 0.00, MaxConfidence: 0.60},
		{Key: "moderate", MinConfidence: 0.60, MaxConfidence: 0.75},
		{Key: "high", MinConfidence: 0.75, MaxConfidence: 0.90},
		{Key: "very_high", MinConfidence: 0.90, MaxConfidence: 1.00},
	}
}

func initiativeCalibrationBandIndex(key string) int {
	switch key {
	case "low":
		return 0
	case "moderate":
		return 1
	case "high":
		return 2
	case "very_high":
		return 3
	default:
		return -1
	}
}

func (s *ActivityStore) GetInitiativeCalibration(kind, sourceKind string) (InitiativeCalibrationSummary, error) {
	kind = strings.TrimSpace(kind)
	sourceKind = strings.TrimSpace(sourceKind)
	summary := InitiativeCalibrationSummary{
		Kind:                    kind,
		SourceKind:              sourceKind,
		MinimumEvaluatedSamples: initiativeCalibrationMinimumEvaluatedSamples,
		Bands:                   initiativeCalibrationBands(),
	}
	rows, err := s.db.Query(`
		SELECT
			CASE
				WHEN io.prediction_confidence < 0.60 THEN 'low'
				WHEN io.prediction_confidence < 0.75 THEN 'moderate'
				WHEN io.prediction_confidence < 0.90 THEN 'high'
				ELSE 'very_high'
			END AS confidence_band,
			io.assessment,
			CASE WHEN io.confidence >= ? THEN 1 ELSE 0 END AS qualified,
			COUNT(*)
		FROM initiative_outcomes io
		JOIN initiatives i ON i.id = io.initiative_id
		WHERE io.prediction_confidence IS NOT NULL
			AND (? = '' OR i.kind = ?)
			AND (? = '' OR i.source_kind = ?)
		GROUP BY 1, 2, 3`,
		initiativeCalibrationComparisonConfidence,
		kind, kind,
		sourceKind, sourceKind,
	)
	if err != nil {
		return InitiativeCalibrationSummary{}, fmt.Errorf("query initiative calibration: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var bandKey, assessment string
		var qualified, count int
		if err := rows.Scan(&bandKey, &assessment, &qualified, &count); err != nil {
			return InitiativeCalibrationSummary{}, fmt.Errorf("scan initiative calibration: %w", err)
		}
		summary.TotalOutcomes += count
		bandIndex := initiativeCalibrationBandIndex(bandKey)
		if bandIndex < 0 {
			continue
		}
		band := &summary.Bands[bandIndex]
		if qualified == 0 {
			summary.ExcludedLowConfidenceOutcomes += count
			band.ExcludedLowConfidenceOutcomes += count
			continue
		}
		band.SampleCount += count
		summary.SampleCount += count
		switch assessment {
		case "matched":
			band.MatchedCount += count
			summary.MatchedCount += count
			band.EvaluatedSamples += count
			summary.EvaluatedSamples += count
		case "partial":
			band.PartialCount += count
			summary.PartialCount += count
			band.EvaluatedSamples += count
			summary.EvaluatedSamples += count
		case "different":
			band.DifferentCount += count
			summary.DifferentCount += count
			band.EvaluatedSamples += count
			summary.EvaluatedSamples += count
		case "unclear":
			band.UnclearCount += count
			summary.UnclearCount += count
		}
	}
	if err := rows.Err(); err != nil {
		return InitiativeCalibrationSummary{}, fmt.Errorf("iterate initiative calibration: %w", err)
	}
	for i := range summary.Bands {
		band := &summary.Bands[i]
		if band.EvaluatedSamples > 0 {
			band.Reliability = (float64(band.MatchedCount) + 0.5*float64(band.PartialCount)) /
				float64(band.EvaluatedSamples)
		}
		band.Ready = band.EvaluatedSamples >= initiativeCalibrationMinimumEvaluatedSamples
	}
	if summary.EvaluatedSamples > 0 {
		summary.Reliability = (float64(summary.MatchedCount) + 0.5*float64(summary.PartialCount)) /
			float64(summary.EvaluatedSamples)
	}
	summary.Ready = summary.EvaluatedSamples >= initiativeCalibrationMinimumEvaluatedSamples
	return summary, nil
}

func scanInitiativeActionReceipt(scanner interface{ Scan(dest ...any) error }) (InitiativeActionReceipt, error) {
	var receipt InitiativeActionReceipt
	var finishedAt sql.NullInt64
	var artifactsJSON, sideEffectsJSON string
	err := scanner.Scan(
		&receipt.ID,
		&receipt.InitiativeID,
		&receipt.ExecutionRef,
		&receipt.RunID,
		&receipt.Status,
		&receipt.TriggerKind,
		&receipt.StartedAt,
		&finishedAt,
		&receipt.DurationMs,
		&receipt.ResultSummary,
		&receipt.Error,
		&receipt.SessionTag,
		&receipt.ItemSortStart,
		&artifactsJSON,
		&sideEffectsJSON,
		&receipt.CreatedAt,
		&receipt.UpdatedAt,
	)
	if err != nil {
		return InitiativeActionReceipt{}, err
	}
	if finishedAt.Valid {
		receipt.FinishedAt = finishedAt.Int64
	}
	decodeInitiativeJSON(artifactsJSON, &receipt.Artifacts)
	decodeInitiativeJSON(sideEffectsJSON, &receipt.SideEffects)
	return receipt, nil
}

const initiativeReceiptSelectColumns = `
	id, initiative_id, execution_ref, run_id, status, trigger_kind,
	started_at, finished_at, duration_ms, result_summary, error,
	session_tag, item_sort_start, artifacts_json, side_effects_json,
	created_at, updated_at`

func (s *ActivityStore) UpsertInitiativeActionReceipt(event CronRunLifecycleEvent) (InitiativeActionReceipt, Initiative, bool, error) {
	initiative, err := s.GetInitiativeByExecutionRef(event.JobID)
	if errors.Is(err, sql.ErrNoRows) {
		return InitiativeActionReceipt{}, Initiative{}, false, nil
	}
	if err != nil {
		return InitiativeActionReceipt{}, Initiative{}, false, err
	}

	now := time.Now().UnixMilli()
	createdAt := event.StartedAt
	if createdAt == 0 {
		createdAt = now
	}
	sideEffects := make([]any, 0, 1)
	if event.DeliveryTarget != nil {
		sideEffects = append(sideEffects, map[string]any{
			"kind":   "delivery_target",
			"target": event.DeliveryTarget,
		})
	}
	receiptID := "receipt-" + event.RunID
	tx, err := s.db.Begin()
	if err != nil {
		return InitiativeActionReceipt{}, Initiative{}, false, fmt.Errorf("begin initiative action receipt: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		INSERT INTO initiative_action_receipts (
			id, initiative_id, execution_ref, run_id, status, trigger_kind,
			started_at, finished_at, duration_ms, result_summary, error,
			session_tag, item_sort_start, artifacts_json, side_effects_json,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id) DO UPDATE SET
			status = excluded.status,
			finished_at = excluded.finished_at,
			duration_ms = excluded.duration_ms,
			result_summary = excluded.result_summary,
			error = excluded.error,
			session_tag = excluded.session_tag,
			item_sort_start = excluded.item_sort_start,
			artifacts_json = excluded.artifacts_json,
			side_effects_json = excluded.side_effects_json,
			updated_at = excluded.updated_at`,
		receiptID, initiative.ID, event.JobID, event.RunID, event.Status, event.TriggerKind,
		createdAt, nullableUnixMillis(event.FinishedAt), event.DurationMs,
		event.ResultSummary, event.Error, event.SessionTag, event.ItemSortStart,
		encodeInitiativeJSON(event.FilePaths, "[]"), encodeInitiativeJSON(sideEffects, "[]"),
		createdAt, now,
	); err != nil {
		return InitiativeActionReceipt{}, Initiative{}, false, fmt.Errorf("upsert initiative action receipt: %w", err)
	}

	status := initiative.Status
	switch event.Status {
	case "running":
		status = InitiativeStatusExecuting
	case "ok":
		status = InitiativeStatusSucceeded
	case "error":
		status = InitiativeStatusFailed
	case "skipped":
		status = InitiativeStatusApproved
	}
	resolvedAt := nullableUnixMillis(event.FinishedAt)
	if event.Status == "running" {
		resolvedAt = nil
	}
	if _, err := tx.Exec(`
		UPDATE initiatives
		SET status = ?, updated_at = ?, resolved_at = ?
		WHERE id = ?`,
		status, now, resolvedAt, initiative.ID,
	); err != nil {
		return InitiativeActionReceipt{}, Initiative{}, false, fmt.Errorf("update initiative execution lifecycle: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return InitiativeActionReceipt{}, Initiative{}, false, fmt.Errorf("commit initiative action receipt: %w", err)
	}

	receipt, err := scanInitiativeActionReceipt(s.db.QueryRow(`
		SELECT `+initiativeReceiptSelectColumns+`
		FROM initiative_action_receipts
		WHERE run_id = ?`, event.RunID))
	if err != nil {
		return InitiativeActionReceipt{}, Initiative{}, false, err
	}
	updated, err := s.GetInitiative(initiative.ID)
	return receipt, updated, true, err
}

func (s *ActivityStore) ListInitiativeActionReceipts(initiativeID string, limit int) ([]InitiativeActionReceipt, error) {
	query := `SELECT ` + initiativeReceiptSelectColumns + `
		FROM initiative_action_receipts
		WHERE initiative_id = ?
		ORDER BY started_at DESC, rowid DESC`
	args := []any{initiativeID}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	receipts := make([]InitiativeActionReceipt, 0)
	for rows.Next() {
		receipt, err := scanInitiativeActionReceipt(rows)
		if err != nil {
			return nil, err
		}
		receipts = append(receipts, receipt)
	}
	return receipts, rows.Err()
}

func (a *App) recordInitiativeCronLifecycle(event CronRunLifecycleEvent) {
	if a.activity == nil || a.activity.store == nil {
		return
	}
	receipt, initiative, linked, err := a.activity.store.UpsertInitiativeActionReceipt(event)
	if err != nil {
		log.Printf("[initiative] record cron lifecycle failed for job %s run %s: %v", event.JobID, event.RunID, err)
		return
	}
	if !linked {
		return
	}
	a.emitBrowserEvent("initiative:updated", initiative)
	a.emitBrowserEvent("initiative:receipt", receipt)
	if event.Status != "running" {
		a.syncInitiativeLearningMemory(initiative.ID)
	}
}

func (a *App) syncInitiativeLearningMemory(initiativeID string) {
	if a.activity == nil || a.activity.store == nil {
		return
	}
	item, err := a.activity.store.GetInitiative(initiativeID)
	if err != nil {
		return
	}
	outcomes, _ := a.activity.store.ListInitiativeOutcomes(initiativeID, 0)
	receipts, _ := a.activity.store.ListInitiativeActionReceipts(initiativeID, 0)

	var b strings.Builder
	fmt.Fprintf(&b, "# Shadow mode learning — %s\n\n", strings.Join(strings.Fields(item.Title), " "))
	fmt.Fprintf(&b, "- **Dimension**: agentic_memory\n")
	fmt.Fprintf(&b, "- **Source**: shadow-learning\n")
	fmt.Fprintf(&b, "- **Read Provenance**: %s\n", memoryReadProvenanceJSON(memoryReadProvenance{Version: 1, Withheld: true}))
	fmt.Fprintf(&b, "- **Type**: shadow_learning\n")
	fmt.Fprintf(&b, "- **Initiative ID**: %s\n", item.ID)
	fmt.Fprintf(&b, "- **Updated**: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "- **Memory State**: %s\n\n", item.MemoryState)
	b.WriteString("## Prediction\n\n")
	fmt.Fprintf(&b, "- Predicted user action: %s\n", item.PredictedAction)
	fmt.Fprintf(&b, "- Rationale: %s\n", item.Rationale)
	fmt.Fprintf(&b, "- Current confidence: %.2f\n", item.Confidence)
	fmt.Fprintf(&b, "- Evidence observations: %d\n\n", item.ObservationCount)

	if item.LatestFeedback != nil {
		b.WriteString("## User Feedback\n\n")
		fmt.Fprintf(&b, "- Decision: %s\n", item.LatestFeedback.Decision)
		if item.LatestFeedback.CorrectedAction != "" {
			fmt.Fprintf(&b, "- Corrected action: %s\n", item.LatestFeedback.CorrectedAction)
		}
		if item.LatestFeedback.Reason != "" {
			fmt.Fprintf(&b, "- Reason: %s\n", item.LatestFeedback.Reason)
		}
		b.WriteString("\n")
	}

	if len(outcomes) > 0 {
		b.WriteString("## Observed User Actions And Results\n\n")
		for _, outcome := range outcomes {
			line := fmt.Sprintf("- %s [%s, comparison confidence %.2f",
				time.UnixMilli(outcome.CreatedAt).Format(time.RFC3339),
				outcome.Assessment,
				outcome.Confidence,
			)
			if outcome.PredictionConfidence != nil {
				line += fmt.Sprintf(", prediction confidence %.2f", *outcome.PredictionConfidence)
			}
			line += "]: " + outcome.ObservedAction
			if outcome.ObservedResult != "" {
				line += " Result: " + outcome.ObservedResult
			}
			if outcome.AssessmentReason != "" {
				line += " Adjustment: " + outcome.AssessmentReason
			}
			if b.Len()+len(line)+1 > initiativeLearningMemoryBudgetChars {
				b.WriteString("- Additional older observations remain available in the initiative outcome ledger.\n")
				break
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if len(receipts) > 0 && b.Len() < initiativeLearningMemoryBudgetChars {
		b.WriteString("## Agent Action Results\n\n")
		for _, receipt := range receipts {
			line := fmt.Sprintf("- %s [%s]: run %s",
				time.UnixMilli(receipt.StartedAt).Format(time.RFC3339),
				receipt.Status,
				receipt.RunID,
			)
			if receipt.ResultSummary != "" {
				line += " Result: " + receipt.ResultSummary
			}
			if receipt.Error != "" {
				line += " Error: " + receipt.Error
			}
			if b.Len()+len(line)+1 > initiativeLearningMemoryBudgetChars {
				b.WriteString("- Additional older receipts remain available in the action receipt ledger.\n")
				break
			}
			b.WriteString(line + "\n")
		}
	}

	dir, err := memoryDir()
	if err != nil {
		log.Printf("[initiative] resolve learning memory directory failed: %v", err)
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("[initiative] create learning memory directory failed: %v", err)
		return
	}
	path := filepath.Join(dir, "shadow-learning-"+item.ID+".md")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		log.Printf("[initiative] write learning memory failed: %v", err)
		return
	}
	a.markMemoryDirty("initiative-learning")
	a.emitBrowserEvent("memory:updated")
}
