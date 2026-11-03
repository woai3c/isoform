package main

import (
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

const (
	purposeDebugVersion      = "purpose-debug-v1"
	purposeDebugDefaultLimit = 100
	purposeDebugMaxLimit     = 500
)

type PurposeDebugCounts struct {
	OpenLoops              int `json:"openLoops"`
	EligibleOpenLoops      int `json:"eligibleOpenLoops"`
	Events                 int `json:"events"`
	Evidence               int `json:"evidence"`
	Relations              int `json:"relations"`
	RelationEvents         int `json:"relationEvents"`
	ExtractionRuns         int `json:"extractionRuns"`
	ExtractionHypotheses   int `json:"extractionHypotheses"`
	ReconciliationReceipts int `json:"reconciliationReceipts"`
}

type PurposeDebugTruncation struct {
	ExtractionRuns bool `json:"extractionRuns"`
}

type PurposeDebugEligibility struct {
	Eligible             bool     `json:"eligible"`
	Decision             string   `json:"decision"`
	Reasons              []string `json:"reasons"`
	CurrentEvidenceCount int      `json:"currentEvidenceCount"`
	IndependentKeys      []string `json:"independentKeys"`
	HasExplicitSupport   bool     `json:"hasExplicitSupport"`
	DuplicateOfIDs       []string `json:"duplicateOfIds"`
	ProjectionReference  string   `json:"projectionReference"`
	ProjectionHash       string   `json:"projectionHash"`
}

type PurposeDebugOpenLoopSummary struct {
	Purpose         AgencyPurposeView       `json:"purpose"`
	Eligibility     PurposeDebugEligibility `json:"eligibility"`
	ContractVersion string                  `json:"contractVersion"`
	RootPayloadHash string                  `json:"rootPayloadHash"`
	EventCount      int                     `json:"eventCount"`
	EvidenceCount   int                     `json:"evidenceCount"`
	RelationCount   int                     `json:"relationCount"`
}

type PurposeDebugExtractionRunSummary struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	Model           string `json:"model"`
	CutoffAt        int64  `json:"cutoffAt"`
	ScannedCount    int    `json:"scannedCount"`
	IncludedCount   int    `json:"includedCount"`
	ExcludedCount   int    `json:"excludedCount"`
	HypothesisCount int    `json:"hypothesisCount"`
	ReceiptCount    int    `json:"receiptCount"`
	Error           string `json:"error,omitempty"`
	InputHash       string `json:"inputHash"`
	ManifestHash    string `json:"manifestHash"`
	PromptHash      string `json:"promptHash"`
	OutputHash      string `json:"outputHash,omitempty"`
	RawOutputHash   string `json:"rawOutputHash,omitempty"`
	PayloadHash     string `json:"payloadHash"`
	StartedAt       int64  `json:"startedAt"`
	CompletedAt     int64  `json:"completedAt"`
	CreatedAt       int64  `json:"createdAt"`
}

type PurposeDebugSnapshot struct {
	Version         string                              `json:"version"`
	GeneratedAt     int64                               `json:"generatedAt"`
	IntegrityStatus string                              `json:"integrityStatus"`
	Understanding   PurposeUnderstandingRuntimeSnapshot `json:"understanding"`
	Counts          PurposeDebugCounts                  `json:"counts"`
	Truncated       PurposeDebugTruncation              `json:"truncated"`
	OpenLoops       []PurposeDebugOpenLoopSummary       `json:"openLoops"`
	ExtractionRuns  []PurposeDebugExtractionRunSummary  `json:"extractionRuns"`
}

type PurposeDebugExtractionSource struct {
	Ref               string `json:"ref"`
	SourceKind        string `json:"sourceKind"`
	SourceID          string `json:"sourceId"`
	SourceVersion     string `json:"sourceVersion"`
	SourceObservedAt  int64  `json:"sourceObservedAt"`
	SourceHash        string `json:"sourceHash"`
	PromptContentHash string `json:"promptContentHash"`
	EvidenceClass     string `json:"evidenceClass"`
	PrivacyDecision   string `json:"privacyDecision"`
	IndependenceKey   string `json:"independenceKey"`
	Truncated         bool   `json:"truncated"`
}

type PurposeDebugExtractionRunDetail struct {
	Summary         PurposeDebugExtractionRunSummary     `json:"summary"`
	Query           string                               `json:"query,omitempty"`
	Sources         []PurposeDebugExtractionSource       `json:"sources"`
	CurrentPurpose  []PurposeExtractionCurrentPurpose    `json:"currentPurpose"`
	Manifest        []PurposeExtractionManifestEntry     `json:"manifest"`
	ManifestSummary PurposeExtractionManifest            `json:"manifestSummary"`
	Hypotheses      []PurposeExtractionHypothesisRecord  `json:"hypotheses"`
	Receipts        []PurposeReconciliationReceiptRecord `json:"receipts"`
}

type PurposeDebugOpenLoopDetail struct {
	Summary         PurposeDebugOpenLoopSummary `json:"summary"`
	Product         AgencyPurposeDetailView     `json:"product"`
	RootPayloadJSON string                      `json:"rootPayloadJson"`
}

func normalizePurposeDebugLimit(limit int) int {
	switch {
	case limit <= 0:
		return purposeDebugDefaultLimit
	case limit > purposeDebugMaxLimit:
		return purposeDebugMaxLimit
	default:
		return limit
	}
}

func purposeDebugStores(
	a *App,
) (*ActivityStore, *PurposeMapStore, error) {
	if a == nil || a.activity == nil || a.activity.store == nil ||
		a.activity.store.db == nil {
		return nil, nil, errors.New("Purpose diagnostics storage is unavailable")
	}
	activity := a.activity.store
	return activity, NewPurposeMapStore(activity.db), nil
}

func purposeDebugEligibility(detail PurposeOpenLoopDetail) (PurposeDebugEligibility, error) {
	currentEvidence := make([]PurposeEvidenceRecord, 0)
	independent := make(map[string]struct{})
	explicit := false
	for _, evidence := range detail.Evidence {
		if evidence.EventID != detail.Current.ID {
			continue
		}
		currentEvidence = append(currentEvidence, evidence)
		if evidence.Stance != PurposeStanceSupport && evidence.Stance != PurposeStanceUpdate {
			continue
		}
		switch evidence.EvidenceClass {
		case PurposeEvidenceExplicitUser, PurposeEvidenceCorrection:
			explicit = true
		case PurposeEvidenceObservedUser, PurposeEvidenceEvaluatedProjection:
			independent[evidence.IndependenceKey] = struct{}{}
		}
	}
	independentKeys := make([]string, 0, len(independent))
	for key := range independent {
		independentKeys = append(independentKeys, key)
	}
	sort.Strings(independentKeys)
	duplicateOf := make([]string, 0)
	for _, relation := range detail.Relations {
		if relation.Relation.SubjectID == detail.OpenLoop.ID &&
			relation.Relation.RelationType == PurposeRelationDuplicateOf &&
			relation.Current.EventType == PurposeRelationEventAsserted {
			duplicateOf = append(duplicateOf, relation.Relation.ObjectID)
		}
	}
	sort.Strings(duplicateOf)
	reference, err := purposeTaskContextReference(detail)
	if err != nil {
		return PurposeDebugEligibility{}, err
	}
	parsed, _ := parsePurposeTaskContextReference(reference)
	reasons := make([]string, 0, 3)
	if detail.Current.SupportState != PurposeSupportSupported {
		reasons = append(reasons, "current support is "+detail.Current.SupportState)
	}
	if detail.Current.LifecycleState != PurposeStateActive &&
		detail.Current.LifecycleState != PurposeStateWaiting {
		reasons = append(reasons, "current lifecycle is "+detail.Current.LifecycleState)
	}
	if len(duplicateOf) > 0 {
		reasons = append(reasons, "root is an active duplicate of another Purpose")
	}
	decision := "excluded"
	if detail.Eligible {
		decision = "included"
		reasons = []string{"supported current canonical Purpose"}
	}
	return PurposeDebugEligibility{
		Eligible: detail.Eligible, Decision: decision, Reasons: reasons,
		CurrentEvidenceCount: len(currentEvidence), IndependentKeys: independentKeys,
		HasExplicitSupport: explicit, DuplicateOfIDs: duplicateOf,
		ProjectionReference: reference, ProjectionHash: parsed.ProjectionHash,
	}, nil
}

func purposeDebugOpenLoopSummary(
	detail PurposeOpenLoopDetail,
) (PurposeDebugOpenLoopSummary, error) {
	eligibility, err := purposeDebugEligibility(detail)
	if err != nil {
		return PurposeDebugOpenLoopSummary{}, err
	}
	return PurposeDebugOpenLoopSummary{
		Purpose: agencyPurposeView(detail), Eligibility: eligibility,
		ContractVersion: detail.OpenLoop.ContractVersion,
		RootPayloadHash: detail.OpenLoop.PayloadHash,
		EventCount:      len(detail.Events), EvidenceCount: len(detail.Evidence),
		RelationCount: len(detail.Relations),
	}, nil
}

func purposeDebugExtractionSummary(
	detail PurposeExtractionRunDetail,
) PurposeDebugExtractionRunSummary {
	return PurposeDebugExtractionRunSummary{
		ID: detail.Run.ID, Status: detail.Run.Status, Model: detail.Run.Model,
		CutoffAt: detail.Run.CutoffAt, ScannedCount: detail.Manifest.ScannedCount,
		IncludedCount:   detail.Manifest.IncludedCount,
		ExcludedCount:   detail.Manifest.ExcludedCount,
		HypothesisCount: len(detail.Hypotheses), ReceiptCount: len(detail.Receipts),
		Error: detail.Run.Error, InputHash: detail.Run.InputHash,
		ManifestHash: detail.Run.ManifestHash, PromptHash: detail.Run.PromptHash,
		OutputHash: detail.Run.OutputHash, RawOutputHash: detail.Run.RawOutputHash,
		PayloadHash: detail.Run.PayloadHash, StartedAt: detail.Run.StartedAt,
		CompletedAt: detail.Run.CompletedAt, CreatedAt: detail.Run.CreatedAt,
	}
}

func purposeDebugTableCount(db *sql.DB, table string) (int, error) {
	allowed := map[string]bool{
		"purpose_open_loops_v1": true, "purpose_open_loop_events_v1": true,
		"purpose_open_loop_evidence_v1":        true,
		"purpose_open_loop_relations_v1":       true,
		"purpose_open_loop_relation_events_v1": true,
		"purpose_extraction_runs_v1":           true,
		"purpose_extraction_hypotheses_v1":     true,
		"purpose_reconciliation_receipts_v1":   true,
	}
	if !allowed[table] {
		return 0, errors.New("unsupported Purpose diagnostics table")
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// GetPurposeDebugSnapshot returns a bounded operational index while always
// returning every validated Purpose root. It performs no extraction,
// reconciliation, repair, authority decision, or execution.
func (a *App) GetPurposeDebugSnapshot(limit int) (PurposeDebugSnapshot, error) {
	activity, purpose, err := purposeDebugStores(a)
	if err != nil {
		return PurposeDebugSnapshot{}, err
	}
	limit = normalizePurposeDebugLimit(limit)
	details, err := purpose.ListDetailsAll()
	if err != nil {
		return PurposeDebugSnapshot{}, err
	}
	result := PurposeDebugSnapshot{
		Version: purposeDebugVersion, GeneratedAt: time.Now().UnixMilli(),
		IntegrityStatus: "validated", OpenLoops: []PurposeDebugOpenLoopSummary{},
		Understanding: PurposeUnderstandingRuntimeSnapshot{
			PendingTriggers: []string{},
		},
		ExtractionRuns: []PurposeDebugExtractionRunSummary{},
	}
	if service := a.purposeUnderstandingService(); service != nil {
		result.Understanding = service.Snapshot()
	}
	for _, detail := range details {
		summary, err := purposeDebugOpenLoopSummary(detail)
		if err != nil {
			return PurposeDebugSnapshot{}, err
		}
		result.OpenLoops = append(result.OpenLoops, summary)
		if detail.Eligible {
			result.Counts.EligibleOpenLoops++
		}
	}
	sort.SliceStable(result.OpenLoops, func(i, j int) bool {
		left, right := result.OpenLoops[i].Purpose, result.OpenLoops[j].Purpose
		if left.UpdatedAt != right.UpdatedAt {
			return left.UpdatedAt > right.UpdatedAt
		}
		return left.ID < right.ID
	})

	extraction := &PurposeExtractionRuntime{activity: activity, purpose: purpose}
	extractionRuns, err := extraction.ListRuns(limit)
	if err != nil {
		return PurposeDebugSnapshot{}, err
	}
	for _, detail := range extractionRuns {
		result.ExtractionRuns = append(
			result.ExtractionRuns, purposeDebugExtractionSummary(detail),
		)
	}
	tables := []struct {
		name string
		set  func(int)
	}{
		{"purpose_open_loops_v1", func(value int) { result.Counts.OpenLoops = value }},
		{"purpose_open_loop_events_v1", func(value int) { result.Counts.Events = value }},
		{"purpose_open_loop_evidence_v1", func(value int) { result.Counts.Evidence = value }},
		{"purpose_open_loop_relations_v1", func(value int) { result.Counts.Relations = value }},
		{"purpose_open_loop_relation_events_v1", func(value int) { result.Counts.RelationEvents = value }},
		{"purpose_extraction_runs_v1", func(value int) { result.Counts.ExtractionRuns = value }},
		{"purpose_extraction_hypotheses_v1", func(value int) { result.Counts.ExtractionHypotheses = value }},
		{"purpose_reconciliation_receipts_v1", func(value int) { result.Counts.ReconciliationReceipts = value }},
	}
	for _, table := range tables {
		count, err := purposeDebugTableCount(activity.db, table.name)
		if err != nil {
			return PurposeDebugSnapshot{}, err
		}
		table.set(count)
	}
	result.Truncated.ExtractionRuns = len(result.ExtractionRuns) < result.Counts.ExtractionRuns
	return result, nil
}

// GetPurposeDebugOpenLoop returns the complete validated ledger projection for
// one root.
func (a *App) GetPurposeDebugOpenLoop(
	openLoopID string,
) (PurposeDebugOpenLoopDetail, error) {
	_, purpose, err := purposeDebugStores(a)
	if err != nil {
		return PurposeDebugOpenLoopDetail{}, err
	}
	all, err := purpose.ListDetailsAll()
	if err != nil {
		return PurposeDebugOpenLoopDetail{}, err
	}
	detail, err := findPurposeDetail(all, strings.TrimSpace(openLoopID))
	if err != nil {
		return PurposeDebugOpenLoopDetail{}, err
	}
	summary, err := purposeDebugOpenLoopSummary(detail)
	if err != nil {
		return PurposeDebugOpenLoopDetail{}, err
	}
	return PurposeDebugOpenLoopDetail{
		Summary: summary, Product: agencyPurposeDetailView(detail, all),
		RootPayloadJSON: detail.OpenLoop.PayloadJSON,
	}, nil
}

// GetPurposeDebugExtractionRun returns frozen manifest provenance without raw
// source content, the model prompt, raw output, or any replay capability.
func (a *App) GetPurposeDebugExtractionRun(
	runID string,
) (PurposeDebugExtractionRunDetail, error) {
	activity, purpose, err := purposeDebugStores(a)
	if err != nil {
		return PurposeDebugExtractionRunDetail{}, err
	}
	runtime := &PurposeExtractionRuntime{activity: activity, purpose: purpose}
	detail, err := runtime.GetRun(runID)
	if err != nil {
		return PurposeDebugExtractionRunDetail{}, err
	}
	result := PurposeDebugExtractionRunDetail{
		Summary: purposeDebugExtractionSummary(detail), Query: detail.Input.Query,
		Sources: []PurposeDebugExtractionSource{},
		CurrentPurpose: append(
			[]PurposeExtractionCurrentPurpose{},
			detail.Input.CurrentPurpose...,
		),
		Manifest: append(
			[]PurposeExtractionManifestEntry(nil),
			detail.Manifest.Entries...,
		),
		ManifestSummary: detail.Manifest,
		Hypotheses:      append([]PurposeExtractionHypothesisRecord(nil), detail.Hypotheses...),
		Receipts:        append([]PurposeReconciliationReceiptRecord(nil), detail.Receipts...),
	}
	for _, source := range detail.Input.Sources {
		result.Sources = append(result.Sources, PurposeDebugExtractionSource{
			Ref: source.Ref, SourceKind: source.SourceKind, SourceID: source.SourceID,
			SourceVersion:    source.SourceVersion,
			SourceObservedAt: source.SourceObservedAt, SourceHash: source.SourceHash,
			PromptContentHash: source.PromptContentHash,
			EvidenceClass:     source.EvidenceClass,
			PrivacyDecision:   source.PrivacyDecision,
			IndependenceKey:   source.IndependenceKey, Truncated: source.Truncated,
		})
	}
	return result, nil
}
