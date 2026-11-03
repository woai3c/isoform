package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

const (
	SelfDirectiveContractVersion = "self-user-directive-v1"

	SelfDirectiveEventCreated   = "created"
	SelfDirectiveEventRevised   = "revised"
	SelfDirectiveEventRetracted = "retracted"

	selfDirectiveReasoningStatus = "explicit_user_statement"
	selfDirectiveEvidenceKind    = "explicit_user_self_directive"

	selfDirectiveMaxIdempotencyBytes = 512
	selfDirectiveMaxStatementBytes   = 12_000
	selfDirectiveMaxScopeBytes       = 4_000
	selfDirectiveMaxReasonBytes      = 4_000
)

var selfDirectiveDeniableCapabilities = map[string]struct{}{
	"tool.edit":  {},
	"tool.glob":  {},
	"tool.grep":  {},
	"tool.read":  {},
	"tool.write": {},
}

type CreateSelfDirectiveInput struct {
	IdempotencyKey     string   `json:"idempotencyKey"`
	Kind               string   `json:"kind"`
	Statement          string   `json:"statement"`
	Scope              string   `json:"scope,omitempty"`
	DeniedCapabilities []string `json:"deniedCapabilities"`
	OccurredAt         int64    `json:"occurredAt"`
}

type ReviseSelfDirectiveInput struct {
	DirectiveID         string   `json:"directiveId"`
	ExpectedSelfEntryID string   `json:"expectedSelfEntryId"`
	IdempotencyKey      string   `json:"idempotencyKey"`
	Statement           string   `json:"statement"`
	Scope               string   `json:"scope,omitempty"`
	DeniedCapabilities  []string `json:"deniedCapabilities"`
	OccurredAt          int64    `json:"occurredAt"`
}

type RetractSelfDirectiveInput struct {
	DirectiveID         string `json:"directiveId"`
	ExpectedSelfEntryID string `json:"expectedSelfEntryId"`
	IdempotencyKey      string `json:"idempotencyKey"`
	Reason              string `json:"reason"`
	OccurredAt          int64  `json:"occurredAt"`
}

type SelfDirectiveRecord struct {
	ID              string `json:"id"`
	IdempotencyKey  string `json:"idempotencyKey"`
	EntryKey        string `json:"entryKey"`
	Kind            string `json:"kind"`
	ContractVersion string `json:"contractVersion"`
	CreatedAt       int64  `json:"createdAt"`
	PayloadJSON     string `json:"payloadJson"`
	PayloadHash     string `json:"payloadHash"`
}

type SelfDirectiveEventRecord struct {
	ID                  string   `json:"id"`
	DirectiveID         string   `json:"directiveId"`
	IdempotencyKey      string   `json:"idempotencyKey"`
	Sequence            int      `json:"sequence"`
	EventType           string   `json:"eventType"`
	ExpectedSelfEntryID string   `json:"expectedSelfEntryId,omitempty"`
	SelfEntryID         string   `json:"selfEntryId"`
	SelfSourceHash      string   `json:"selfSourceHash"`
	Statement           string   `json:"statement"`
	Scope               string   `json:"scope,omitempty"`
	DeniedCapabilities  []string `json:"deniedCapabilities"`
	RetractionReason    string   `json:"retractionReason,omitempty"`
	OccurredAt          int64    `json:"occurredAt"`
	CreatedAt           int64    `json:"createdAt"`
	PayloadJSON         string   `json:"payloadJson"`
	PayloadHash         string   `json:"payloadHash"`
}

type SelfDirectiveDetail struct {
	Directive SelfDirectiveRecord        `json:"directive"`
	Events    []SelfDirectiveEventRecord `json:"events"`
	Revisions []SelfModelEntry           `json:"revisions"`
	Current   SelfModelEntry             `json:"current"`
}

type SelfDirectiveMutationResult struct {
	Directive SelfDirectiveRecord      `json:"directive"`
	Event     SelfDirectiveEventRecord `json:"event"`
	SelfEntry SelfModelEntry           `json:"selfEntry"`
}

type SelfDirectiveSummary struct {
	Directive           SelfDirectiveRecord      `json:"directive"`
	CurrentEvent        SelfDirectiveEventRecord `json:"currentEvent"`
	Current             SelfModelEntry           `json:"current"`
	RevisionCount       int                      `json:"revisionCount"`
	HasStructuredPolicy bool                     `json:"hasStructuredPolicy"`
}

type SelfDirectiveStore struct {
	db *sql.DB
}

func NewSelfDirectiveStore(db *sql.DB) *SelfDirectiveStore {
	return &SelfDirectiveStore{db: db}
}

func (store *SelfDirectiveStore) require() error {
	if store == nil || store.db == nil {
		return errors.New("Self directive storage is unavailable")
	}
	return nil
}

func normalizeSelfDirectiveText(
	value string,
	field string,
	maxBytes int,
	required bool,
) (string, error) {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", fmt.Errorf("Self directive %s is required", field)
	}
	if len(value) > maxBytes {
		return "", fmt.Errorf("Self directive %s is too long", field)
	}
	return value, nil
}

func normalizeSelfDirectiveKind(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case selfModelKindPrinciple, selfModelKindBoundary:
		return value, nil
	default:
		return "", fmt.Errorf("unsupported Self directive kind %q", value)
	}
}

func normalizeSelfDirectiveCapabilities(values []string) ([]string, error) {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := selfDirectiveDeniableCapabilities[value]; !ok {
			return nil, fmt.Errorf(
				"Self directive capability %q cannot be compiled as a deny-only boundary",
				value,
			)
		}
		normalized = append(normalized, value)
	}
	return uniqueSortedStrings(normalized), nil
}

func normalizeSelfDirectivePolicy(
	kind string,
	values []string,
) ([]string, error) {
	capabilities, err := normalizeSelfDirectiveCapabilities(values)
	if err != nil {
		return nil, err
	}
	if kind == selfModelKindPrinciple && len(capabilities) > 0 {
		return nil, errors.New("Self principles cannot contain runtime capability policy")
	}
	return capabilities, nil
}

func normalizeSelfDirectiveIdempotency(value string) (string, error) {
	return normalizeSelfDirectiveText(
		value,
		"idempotency key",
		selfDirectiveMaxIdempotencyBytes,
		true,
	)
}

func selfDirectiveEntryKey(directiveID string) string {
	return agencyPayloadHash([]byte("self-user-directive\x00" + directiveID))
}

type selfDirectiveRootPayload struct {
	ID              string `json:"id"`
	IdempotencyKey  string `json:"idempotencyKey"`
	EntryKey        string `json:"entryKey"`
	Kind            string `json:"kind"`
	ContractVersion string `json:"contractVersion"`
	CreatedAt       int64  `json:"createdAt"`
}

func selfDirectiveRootPayloadJSON(
	record SelfDirectiveRecord,
) (string, string, error) {
	payload, err := json.Marshal(selfDirectiveRootPayload{
		ID:              record.ID,
		IdempotencyKey:  record.IdempotencyKey,
		EntryKey:        record.EntryKey,
		Kind:            record.Kind,
		ContractVersion: record.ContractVersion,
		CreatedAt:       record.CreatedAt,
	})
	if err != nil {
		return "", "", err
	}
	return string(payload), agencyPayloadHash(payload), nil
}

func validateSelfDirectiveRoot(
	record SelfDirectiveRecord,
) error {
	key, err := normalizeSelfDirectiveIdempotency(record.IdempotencyKey)
	if err != nil || key != record.IdempotencyKey {
		return errors.New("Self directive root has invalid idempotency identity")
	}
	kind, err := normalizeSelfDirectiveKind(record.Kind)
	if err != nil || kind != record.Kind {
		return errors.New("Self directive root has invalid kind")
	}
	if record.ContractVersion != SelfDirectiveContractVersion || record.CreatedAt <= 0 {
		return errors.New("Self directive root has invalid contract metadata")
	}
	expectedID := deterministicAgencyID("self-user-directive", record.IdempotencyKey)
	if record.ID != expectedID || record.EntryKey != selfDirectiveEntryKey(expectedID) {
		return errors.New("Self directive root identity mismatch")
	}
	payloadJSON, payloadHash, err := selfDirectiveRootPayloadJSON(record)
	if err != nil {
		return err
	}
	if record.PayloadJSON != payloadJSON || record.PayloadHash != payloadHash ||
		agencyPayloadHash([]byte(record.PayloadJSON)) != record.PayloadHash {
		return errors.New("Self directive root payload hash mismatch")
	}
	return nil
}

const selfDirectiveRootSelectColumns = `
	id, idempotency_key, entry_key, kind, contract_version, created_at,
	payload_json, payload_hash`

func scanSelfDirectiveRoot(
	scanner interface{ Scan(dest ...any) error },
) (SelfDirectiveRecord, error) {
	var record SelfDirectiveRecord
	if err := scanner.Scan(
		&record.ID,
		&record.IdempotencyKey,
		&record.EntryKey,
		&record.Kind,
		&record.ContractVersion,
		&record.CreatedAt,
		&record.PayloadJSON,
		&record.PayloadHash,
	); err != nil {
		return SelfDirectiveRecord{}, err
	}
	if err := validateSelfDirectiveRoot(record); err != nil {
		return SelfDirectiveRecord{}, err
	}
	return record, nil
}

type selfDirectiveEventPayload struct {
	ID                  string   `json:"id"`
	DirectiveID         string   `json:"directiveId"`
	IdempotencyKey      string   `json:"idempotencyKey"`
	Sequence            int      `json:"sequence"`
	EventType           string   `json:"eventType"`
	ExpectedSelfEntryID string   `json:"expectedSelfEntryId"`
	SelfEntryID         string   `json:"selfEntryId"`
	SelfSourceHash      string   `json:"selfSourceHash"`
	Statement           string   `json:"statement"`
	Scope               string   `json:"scope"`
	DeniedCapabilities  []string `json:"deniedCapabilities"`
	RetractionReason    string   `json:"retractionReason"`
	OccurredAt          int64    `json:"occurredAt"`
	CreatedAt           int64    `json:"createdAt"`
}

func selfDirectiveEventPayloadJSON(
	event SelfDirectiveEventRecord,
) (string, string, error) {
	payload, err := json.Marshal(selfDirectiveEventPayload{
		ID:                  event.ID,
		DirectiveID:         event.DirectiveID,
		IdempotencyKey:      event.IdempotencyKey,
		Sequence:            event.Sequence,
		EventType:           event.EventType,
		ExpectedSelfEntryID: event.ExpectedSelfEntryID,
		SelfEntryID:         event.SelfEntryID,
		SelfSourceHash:      event.SelfSourceHash,
		Statement:           event.Statement,
		Scope:               event.Scope,
		DeniedCapabilities:  event.DeniedCapabilities,
		RetractionReason:    event.RetractionReason,
		OccurredAt:          event.OccurredAt,
		CreatedAt:           event.CreatedAt,
	})
	if err != nil {
		return "", "", err
	}
	return string(payload), agencyPayloadHash(payload), nil
}

func validateSelfDirectiveEvent(event SelfDirectiveEventRecord) error {
	if strings.TrimSpace(event.DirectiveID) != event.DirectiveID ||
		event.DirectiveID == "" || strings.TrimSpace(event.SelfEntryID) != event.SelfEntryID ||
		event.SelfEntryID == "" || strings.TrimSpace(event.SelfSourceHash) != event.SelfSourceHash ||
		event.SelfSourceHash == "" ||
		strings.TrimSpace(event.ExpectedSelfEntryID) != event.ExpectedSelfEntryID {
		return errors.New("Self directive event relation is incomplete")
	}
	key, err := normalizeSelfDirectiveIdempotency(event.IdempotencyKey)
	if err != nil || key != event.IdempotencyKey {
		return errors.New("Self directive event has invalid idempotency identity")
	}
	if event.ID != deterministicAgencyID("self-user-directive-event", event.IdempotencyKey) {
		return errors.New("Self directive event identity mismatch")
	}
	statement, err := normalizeSelfDirectiveText(
		event.Statement,
		"statement",
		selfDirectiveMaxStatementBytes,
		true,
	)
	if err != nil || statement != event.Statement {
		return errors.New("Self directive event has invalid statement")
	}
	scope, err := normalizeSelfDirectiveText(
		event.Scope,
		"scope",
		selfDirectiveMaxScopeBytes,
		false,
	)
	if err != nil || scope != event.Scope {
		return errors.New("Self directive event has invalid scope")
	}
	capabilities, err := normalizeSelfDirectiveCapabilities(event.DeniedCapabilities)
	if err != nil || !slices.Equal(
		capabilities,
		event.DeniedCapabilities,
	) {
		return errors.New("Self directive event has invalid denied capabilities")
	}
	reason, err := normalizeSelfDirectiveText(
		event.RetractionReason,
		"retraction reason",
		selfDirectiveMaxReasonBytes,
		false,
	)
	if err != nil || reason != event.RetractionReason {
		return errors.New("Self directive event has invalid retraction reason")
	}
	if event.Sequence < 1 || event.OccurredAt <= 0 || event.CreatedAt <= 0 ||
		event.CreatedAt != event.OccurredAt {
		return errors.New("Self directive event has invalid lifecycle metadata")
	}
	switch event.EventType {
	case SelfDirectiveEventCreated:
		if event.Sequence != 1 || event.ExpectedSelfEntryID != "" || reason != "" {
			return errors.New("Self directive created event has invalid transition metadata")
		}
	case SelfDirectiveEventRevised:
		if event.Sequence == 1 || strings.TrimSpace(event.ExpectedSelfEntryID) == "" || reason != "" {
			return errors.New("Self directive revised event has invalid transition metadata")
		}
	case SelfDirectiveEventRetracted:
		if event.Sequence == 1 || strings.TrimSpace(event.ExpectedSelfEntryID) == "" || reason == "" {
			return errors.New("Self directive retracted event has invalid transition metadata")
		}
	default:
		return errors.New("Self directive event type is unsupported")
	}
	payloadJSON, payloadHash, err := selfDirectiveEventPayloadJSON(event)
	if err != nil {
		return err
	}
	if event.PayloadJSON != payloadJSON || event.PayloadHash != payloadHash ||
		agencyPayloadHash([]byte(event.PayloadJSON)) != event.PayloadHash {
		return errors.New("Self directive event payload hash mismatch")
	}
	return nil
}

const selfDirectiveEventSelectColumns = `
	id, directive_id, idempotency_key, sequence, event_type,
	expected_self_entry_id, self_entry_id, self_source_hash, statement, scope,
	denied_capabilities_json, retraction_reason, occurred_at, created_at,
	payload_json, payload_hash`

func scanSelfDirectiveEvent(
	scanner interface{ Scan(dest ...any) error },
) (SelfDirectiveEventRecord, error) {
	var event SelfDirectiveEventRecord
	var capabilitiesJSON string
	if err := scanner.Scan(
		&event.ID,
		&event.DirectiveID,
		&event.IdempotencyKey,
		&event.Sequence,
		&event.EventType,
		&event.ExpectedSelfEntryID,
		&event.SelfEntryID,
		&event.SelfSourceHash,
		&event.Statement,
		&event.Scope,
		&capabilitiesJSON,
		&event.RetractionReason,
		&event.OccurredAt,
		&event.CreatedAt,
		&event.PayloadJSON,
		&event.PayloadHash,
	); err != nil {
		return SelfDirectiveEventRecord{}, err
	}
	if err := json.Unmarshal([]byte(capabilitiesJSON), &event.DeniedCapabilities); err != nil {
		return SelfDirectiveEventRecord{}, fmt.Errorf(
			"decode Self directive event capabilities %s: %w",
			event.ID,
			err,
		)
	}
	canonicalCapabilities, err := json.Marshal(event.DeniedCapabilities)
	if err != nil || capabilitiesJSON != string(canonicalCapabilities) {
		return SelfDirectiveEventRecord{}, errors.New(
			"Self directive event capability payload is not canonical",
		)
	}
	if err := validateSelfDirectiveEvent(event); err != nil {
		return SelfDirectiveEventRecord{}, err
	}
	return event, nil
}

func getSelfDirectiveRootTx(
	tx *sql.Tx,
	directiveID string,
) (SelfDirectiveRecord, error) {
	return scanSelfDirectiveRoot(tx.QueryRow(`
		SELECT `+selfDirectiveRootSelectColumns+`
		FROM self_user_directives_v1
		WHERE id = ?`,
		strings.TrimSpace(directiveID),
	))
}

func getSelfDirectiveEventByIdempotencyTx(
	tx *sql.Tx,
	idempotencyKey string,
) (SelfDirectiveEventRecord, error) {
	return scanSelfDirectiveEvent(tx.QueryRow(`
		SELECT `+selfDirectiveEventSelectColumns+`
		FROM self_user_directive_events_v1
		WHERE idempotency_key = ?`,
		strings.TrimSpace(idempotencyKey),
	))
}

func listSelfDirectiveEventsTx(
	tx *sql.Tx,
	directiveID string,
) ([]SelfDirectiveEventRecord, error) {
	rows, err := tx.Query(`
		SELECT `+selfDirectiveEventSelectColumns+`
		FROM self_user_directive_events_v1
		WHERE directive_id = ?
		ORDER BY sequence ASC`,
		strings.TrimSpace(directiveID),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]SelfDirectiveEventRecord, 0)
	for rows.Next() {
		event, err := scanSelfDirectiveEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func selfDirectiveEvidenceContains(
	entry SelfModelEntry,
	event SelfDirectiveEventRecord,
) bool {
	for _, evidence := range entry.Evidence {
		if evidence.SourceKind != selfDirectiveEvidenceKind ||
			evidence.SourceID != event.ID || evidence.Confidence != 1 ||
			evidence.OccurredAt != event.OccurredAt {
			continue
		}
		for _, ref := range evidence.SourceRefs {
			if ref == event.DirectiveID {
				return true
			}
		}
	}
	return false
}

func validateSelfDirectiveRelation(
	root SelfDirectiveRecord,
	events []SelfDirectiveEventRecord,
	revisions []SelfModelEntry,
	current SelfModelEntry,
) error {
	if len(events) == 0 || len(events) != len(revisions) {
		return errors.New("Self directive event and revision chain is incomplete")
	}
	if root.CreatedAt != events[0].OccurredAt ||
		events[0].IdempotencyKey != root.IdempotencyKey {
		return errors.New("Self directive creation relation mismatch")
	}
	retracted := false
	for index, event := range events {
		entry := revisions[index]
		sequence := index + 1
		if event.DirectiveID != root.ID || event.Sequence != sequence ||
			entry.ID != event.SelfEntryID || entry.SourceHash != event.SelfSourceHash ||
			entry.EntryKey != root.EntryKey || entry.Kind != root.Kind ||
			entry.Revision != sequence || entry.Statement != event.Statement ||
			entry.Scope != event.Scope || entry.ReasoningStatus != selfDirectiveReasoningStatus ||
			entry.Confidence != 1 || entry.CreatedAt != event.OccurredAt ||
			!selfDirectiveEvidenceContains(entry, event) {
			return fmt.Errorf("Self directive relation mismatch at sequence %d", sequence)
		}
		if root.Kind == selfModelKindPrinciple && len(event.DeniedCapabilities) > 0 {
			return errors.New("Self directive principle contains a capability policy")
		}
		if index > 0 {
			previous := events[index-1]
			if event.ExpectedSelfEntryID != previous.SelfEntryID ||
				event.OccurredAt < previous.OccurredAt {
				return fmt.Errorf("Self directive sequence %d has invalid predecessor", sequence)
			}
		}
		if retracted {
			return errors.New("Self directive contains an event after retraction")
		}
		expectedStatus := selfModelStatusActive
		if event.EventType == SelfDirectiveEventRetracted {
			expectedStatus = selfModelStatusRetracted
			retracted = true
			if index == 0 || event.Statement != events[index-1].Statement ||
				event.Scope != events[index-1].Scope ||
				!slices.Equal(
					event.DeniedCapabilities,
					events[index-1].DeniedCapabilities,
				) {
				return errors.New("Self directive retraction changed the directive payload")
			}
		}
		if entry.Status != expectedStatus {
			return fmt.Errorf("Self directive sequence %d has invalid Self status", sequence)
		}
	}
	latest := revisions[len(revisions)-1]
	if current.ID != latest.ID || current.EntryKey != root.EntryKey {
		return errors.New("Self directive current revision relation mismatch")
	}
	return nil
}

func loadSelfDirectiveDetailTx(
	tx *sql.Tx,
	directiveID string,
) (SelfDirectiveDetail, error) {
	root, err := getSelfDirectiveRootTx(tx, directiveID)
	if err != nil {
		return SelfDirectiveDetail{}, err
	}
	events, err := listSelfDirectiveEventsTx(tx, root.ID)
	if err != nil {
		return SelfDirectiveDetail{}, err
	}
	revisions := make([]SelfModelEntry, 0, len(events))
	for _, event := range events {
		entry, err := scanSelfModelEntry(tx.QueryRow(`
			SELECT `+selfModelSelectColumns+`
			FROM self_model_entries
			WHERE id = ?`,
			event.SelfEntryID,
		))
		if err != nil {
			return SelfDirectiveDetail{}, err
		}
		revisions = append(revisions, entry)
	}
	current, err := scanSelfModelEntry(tx.QueryRow(`
		SELECT `+selfModelSelectColumns+`
		FROM self_model_entries
		WHERE entry_key = ?
		ORDER BY revision DESC
		LIMIT 1`,
		root.EntryKey,
	))
	if err != nil {
		return SelfDirectiveDetail{}, err
	}
	if err := validateSelfDirectiveRelation(root, events, revisions, current); err != nil {
		return SelfDirectiveDetail{}, err
	}
	return SelfDirectiveDetail{
		Directive: root,
		Events:    events,
		Revisions: revisions,
		Current:   current,
	}, nil
}

func (store *SelfDirectiveStore) GetDetail(
	directiveID string,
) (SelfDirectiveDetail, error) {
	if err := store.require(); err != nil {
		return SelfDirectiveDetail{}, err
	}
	if strings.TrimSpace(directiveID) == "" {
		return SelfDirectiveDetail{}, errors.New("Self directive ID is required")
	}
	tx, err := store.db.Begin()
	if err != nil {
		return SelfDirectiveDetail{}, err
	}
	defer tx.Rollback()
	detail, err := loadSelfDirectiveDetailTx(tx, directiveID)
	if err != nil {
		return SelfDirectiveDetail{}, err
	}
	if err := tx.Commit(); err != nil {
		return SelfDirectiveDetail{}, err
	}
	return detail, nil
}

func normalizeSelfDirectiveListLimit(limit int) int {
	switch {
	case limit <= 0:
		return 200
	case limit > 1_000:
		return 1_000
	default:
		return limit
	}
}

func (store *SelfDirectiveStore) listDetails(
	limit int,
	includeAll bool,
) ([]SelfDirectiveDetail, error) {
	if err := store.require(); err != nil {
		return nil, err
	}
	tx, err := store.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	query := `
		SELECT id
		FROM self_user_directives_v1
		ORDER BY created_at DESC, id DESC`
	args := make([]any, 0, 1)
	if !includeAll {
		query += "\n\t\tLIMIT ?"
		args = append(args, normalizeSelfDirectiveListLimit(limit))
	}
	rows, err := tx.Query(query, args...)
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
	details := make([]SelfDirectiveDetail, 0, len(ids))
	for _, id := range ids {
		detail, err := loadSelfDirectiveDetailTx(tx, id)
		if err != nil {
			return nil, err
		}
		details = append(details, detail)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return details, nil
}

func selfDirectiveSummaryFromDetail(
	detail SelfDirectiveDetail,
) (SelfDirectiveSummary, error) {
	if len(detail.Events) == 0 {
		return SelfDirectiveSummary{}, errors.New(
			"Self directive detail has no current event",
		)
	}
	currentEvent := detail.Events[len(detail.Events)-1]
	return SelfDirectiveSummary{
		Directive:     detail.Directive,
		CurrentEvent:  currentEvent,
		Current:       detail.Current,
		RevisionCount: len(detail.Revisions),
		HasStructuredPolicy: detail.Directive.Kind == selfModelKindBoundary &&
			len(currentEvent.DeniedCapabilities) > 0 &&
			detail.Current.Status == selfModelStatusActive,
	}, nil
}

func (store *SelfDirectiveStore) List(
	limit int,
) ([]SelfDirectiveSummary, error) {
	details, err := store.listDetails(limit, false)
	if err != nil {
		return nil, err
	}
	items := make([]SelfDirectiveSummary, 0, len(details))
	for _, detail := range details {
		summary, err := selfDirectiveSummaryFromDetail(detail)
		if err != nil {
			return nil, err
		}
		items = append(items, summary)
	}
	return items, nil
}

func normalizeCreateSelfDirective(
	input CreateSelfDirectiveInput,
) (SelfDirectiveRecord, SelfDirectiveEventRecord, error) {
	key, err := normalizeSelfDirectiveIdempotency(input.IdempotencyKey)
	if err != nil {
		return SelfDirectiveRecord{}, SelfDirectiveEventRecord{}, err
	}
	kind, err := normalizeSelfDirectiveKind(input.Kind)
	if err != nil {
		return SelfDirectiveRecord{}, SelfDirectiveEventRecord{}, err
	}
	statement, err := normalizeSelfDirectiveText(
		input.Statement,
		"statement",
		selfDirectiveMaxStatementBytes,
		true,
	)
	if err != nil {
		return SelfDirectiveRecord{}, SelfDirectiveEventRecord{}, err
	}
	scope, err := normalizeSelfDirectiveText(
		input.Scope,
		"scope",
		selfDirectiveMaxScopeBytes,
		false,
	)
	if err != nil {
		return SelfDirectiveRecord{}, SelfDirectiveEventRecord{}, err
	}
	capabilities, err := normalizeSelfDirectivePolicy(kind, input.DeniedCapabilities)
	if err != nil {
		return SelfDirectiveRecord{}, SelfDirectiveEventRecord{}, err
	}
	if input.OccurredAt <= 0 {
		return SelfDirectiveRecord{}, SelfDirectiveEventRecord{}, errors.New(
			"Self directive occurrence time is required",
		)
	}
	id := deterministicAgencyID("self-user-directive", key)
	record := SelfDirectiveRecord{
		ID:              id,
		IdempotencyKey:  key,
		EntryKey:        selfDirectiveEntryKey(id),
		Kind:            kind,
		ContractVersion: SelfDirectiveContractVersion,
		CreatedAt:       input.OccurredAt,
	}
	record.PayloadJSON, record.PayloadHash, err = selfDirectiveRootPayloadJSON(record)
	if err != nil {
		return SelfDirectiveRecord{}, SelfDirectiveEventRecord{}, err
	}
	event := SelfDirectiveEventRecord{
		ID:                 deterministicAgencyID("self-user-directive-event", key),
		DirectiveID:        record.ID,
		IdempotencyKey:     key,
		Sequence:           1,
		EventType:          SelfDirectiveEventCreated,
		Statement:          statement,
		Scope:              scope,
		DeniedCapabilities: capabilities,
		OccurredAt:         input.OccurredAt,
		CreatedAt:          input.OccurredAt,
	}
	return record, event, nil
}

func selfDirectiveOperationMatches(
	event SelfDirectiveEventRecord,
	eventType string,
	directiveID string,
	expectedSelfEntryID string,
	statement string,
	scope string,
	capabilities []string,
	reason string,
) bool {
	return event.EventType == eventType &&
		event.DirectiveID == directiveID &&
		event.ExpectedSelfEntryID == expectedSelfEntryID &&
		event.Statement == statement && event.Scope == scope &&
		slices.Equal(event.DeniedCapabilities, capabilities) &&
		event.RetractionReason == reason
}

func selfDirectiveResultForEvent(
	detail SelfDirectiveDetail,
	event SelfDirectiveEventRecord,
) (SelfDirectiveMutationResult, error) {
	for index := range detail.Events {
		if detail.Events[index].ID == event.ID {
			return SelfDirectiveMutationResult{
				Directive: detail.Directive,
				Event:     detail.Events[index],
				SelfEntry: detail.Revisions[index],
			}, nil
		}
	}
	return SelfDirectiveMutationResult{}, errors.New(
		"Self directive idempotent event is outside its validated chain",
	)
}

func insertSelfDirectiveRootTx(
	tx *sql.Tx,
	record SelfDirectiveRecord,
) error {
	if err := validateSelfDirectiveRoot(record); err != nil {
		return err
	}
	_, err := tx.Exec(`
		INSERT INTO self_user_directives_v1 (
			id, idempotency_key, entry_key, kind, contract_version,
			created_at, payload_json, payload_hash
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID,
		record.IdempotencyKey,
		record.EntryKey,
		record.Kind,
		record.ContractVersion,
		record.CreatedAt,
		record.PayloadJSON,
		record.PayloadHash,
	)
	return err
}

func selfDirectiveEvidence(
	event SelfDirectiveEventRecord,
) SelfModelEvidence {
	statement := event.Statement
	if event.EventType == SelfDirectiveEventRetracted {
		statement = event.RetractionReason
	}
	return SelfModelEvidence{
		SourceKind: selfDirectiveEvidenceKind,
		SourceID:   event.ID,
		Statement:  statement,
		Confidence: 1,
		SourceRefs: []string{event.DirectiveID},
		OccurredAt: event.OccurredAt,
	}
}

func appendSelfDirectiveRevisionTx(
	tx *sql.Tx,
	root SelfDirectiveRecord,
	event SelfDirectiveEventRecord,
) (SelfDirectiveEventRecord, SelfModelEntry, error) {
	status := selfModelStatusActive
	if event.EventType == SelfDirectiveEventRetracted {
		status = selfModelStatusRetracted
	}
	entry, added, err := addSelfModelEntryTx(tx, SelfModelEntry{
		EntryKey:        root.EntryKey,
		Kind:            root.Kind,
		Scope:           event.Scope,
		Statement:       event.Statement,
		Status:          status,
		Confidence:      1,
		ReasoningStatus: selfDirectiveReasoningStatus,
		Evidence:        []SelfModelEvidence{selfDirectiveEvidence(event)},
		CreatedAt:       event.OccurredAt,
	})
	if err != nil {
		return SelfDirectiveEventRecord{}, SelfModelEntry{}, err
	}
	if !added || entry.Revision != event.Sequence {
		return SelfDirectiveEventRecord{}, SelfModelEntry{}, errors.New(
			"Self directive did not create the expected canonical Self revision",
		)
	}
	event.SelfEntryID = entry.ID
	event.SelfSourceHash = entry.SourceHash
	event.PayloadJSON, event.PayloadHash, err = selfDirectiveEventPayloadJSON(event)
	if err != nil {
		return SelfDirectiveEventRecord{}, SelfModelEntry{}, err
	}
	if err := validateSelfDirectiveEvent(event); err != nil {
		return SelfDirectiveEventRecord{}, SelfModelEntry{}, err
	}
	capabilitiesJSON, err := json.Marshal(event.DeniedCapabilities)
	if err != nil {
		return SelfDirectiveEventRecord{}, SelfModelEntry{}, err
	}
	_, err = tx.Exec(`
		INSERT INTO self_user_directive_events_v1 (
			id, directive_id, idempotency_key, sequence, event_type,
			expected_self_entry_id, self_entry_id, self_source_hash, statement,
			scope, denied_capabilities_json, retraction_reason, occurred_at,
			created_at, payload_json, payload_hash
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.DirectiveID,
		event.IdempotencyKey,
		event.Sequence,
		event.EventType,
		event.ExpectedSelfEntryID,
		event.SelfEntryID,
		event.SelfSourceHash,
		event.Statement,
		event.Scope,
		string(capabilitiesJSON),
		event.RetractionReason,
		event.OccurredAt,
		event.CreatedAt,
		event.PayloadJSON,
		event.PayloadHash,
	)
	if err != nil {
		return SelfDirectiveEventRecord{}, SelfModelEntry{}, err
	}
	return event, entry, nil
}

func (store *SelfDirectiveStore) Create(
	input CreateSelfDirectiveInput,
) (SelfDirectiveMutationResult, bool, error) {
	if err := store.require(); err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	root, requestedEvent, err := normalizeCreateSelfDirective(input)
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	tx, err := store.db.Begin()
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	defer tx.Rollback()
	existingEvent, eventErr := getSelfDirectiveEventByIdempotencyTx(
		tx,
		root.IdempotencyKey,
	)
	if eventErr == nil {
		detail, detailErr := loadSelfDirectiveDetailTx(tx, existingEvent.DirectiveID)
		if detailErr != nil {
			return SelfDirectiveMutationResult{}, false, detailErr
		}
		if detail.Directive.ID != root.ID || detail.Directive.Kind != root.Kind ||
			!selfDirectiveOperationMatches(
				existingEvent,
				SelfDirectiveEventCreated,
				root.ID,
				"",
				requestedEvent.Statement,
				requestedEvent.Scope,
				requestedEvent.DeniedCapabilities,
				"",
			) {
			return SelfDirectiveMutationResult{}, false, errors.New(
				"Self directive creation idempotency conflict",
			)
		}
		result, resultErr := selfDirectiveResultForEvent(detail, existingEvent)
		return result, false, resultErr
	}
	if !errors.Is(eventErr, sql.ErrNoRows) {
		return SelfDirectiveMutationResult{}, false, eventErr
	}
	if _, err := scanSelfDirectiveRoot(tx.QueryRow(`
		SELECT `+selfDirectiveRootSelectColumns+`
		FROM self_user_directives_v1
		WHERE id = ? OR idempotency_key = ? OR entry_key = ?
		LIMIT 1`,
		root.ID,
		root.IdempotencyKey,
		root.EntryKey,
	)); err == nil {
		return SelfDirectiveMutationResult{}, false, errors.New(
			"Self directive creation identity conflict",
		)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return SelfDirectiveMutationResult{}, false, err
	}
	if err := insertSelfDirectiveRootTx(tx, root); err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	event, entry, err := appendSelfDirectiveRevisionTx(tx, root, requestedEvent)
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	detail, err := loadSelfDirectiveDetailTx(tx, root.ID)
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	result, err := selfDirectiveResultForEvent(detail, event)
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	if result.SelfEntry.ID != entry.ID {
		return SelfDirectiveMutationResult{}, false, errors.New(
			"Self directive creation result changed during validation",
		)
	}
	if err := tx.Commit(); err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	return result, true, nil
}

func normalizeReviseSelfDirective(
	input ReviseSelfDirectiveInput,
) (ReviseSelfDirectiveInput, error) {
	var err error
	input.DirectiveID = strings.TrimSpace(input.DirectiveID)
	input.ExpectedSelfEntryID = strings.TrimSpace(input.ExpectedSelfEntryID)
	if input.DirectiveID == "" || input.ExpectedSelfEntryID == "" {
		return ReviseSelfDirectiveInput{}, errors.New(
			"Self directive revision requires directive and current Self entry IDs",
		)
	}
	if input.IdempotencyKey, err = normalizeSelfDirectiveIdempotency(
		input.IdempotencyKey,
	); err != nil {
		return ReviseSelfDirectiveInput{}, err
	}
	if input.Statement, err = normalizeSelfDirectiveText(
		input.Statement,
		"statement",
		selfDirectiveMaxStatementBytes,
		true,
	); err != nil {
		return ReviseSelfDirectiveInput{}, err
	}
	if input.Scope, err = normalizeSelfDirectiveText(
		input.Scope,
		"scope",
		selfDirectiveMaxScopeBytes,
		false,
	); err != nil {
		return ReviseSelfDirectiveInput{}, err
	}
	if input.OccurredAt <= 0 {
		return ReviseSelfDirectiveInput{}, errors.New(
			"Self directive revision occurrence time is required",
		)
	}
	return input, nil
}

func selfDirectiveRootUsesIdempotencyTx(
	tx *sql.Tx,
	idempotencyKey string,
) (bool, error) {
	var count int
	if err := tx.QueryRow(`
		SELECT COUNT(*)
		FROM self_user_directives_v1
		WHERE idempotency_key = ?`,
		idempotencyKey,
	).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (store *SelfDirectiveStore) Revise(
	input ReviseSelfDirectiveInput,
) (SelfDirectiveMutationResult, bool, error) {
	if err := store.require(); err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	input, err := normalizeReviseSelfDirective(input)
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	tx, err := store.db.Begin()
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	defer tx.Rollback()
	root, err := getSelfDirectiveRootTx(tx, input.DirectiveID)
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	capabilities, err := normalizeSelfDirectivePolicy(
		root.Kind,
		input.DeniedCapabilities,
	)
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	existingEvent, eventErr := getSelfDirectiveEventByIdempotencyTx(
		tx,
		input.IdempotencyKey,
	)
	if eventErr == nil {
		detail, detailErr := loadSelfDirectiveDetailTx(tx, existingEvent.DirectiveID)
		if detailErr != nil {
			return SelfDirectiveMutationResult{}, false, detailErr
		}
		if !selfDirectiveOperationMatches(
			existingEvent,
			SelfDirectiveEventRevised,
			input.DirectiveID,
			input.ExpectedSelfEntryID,
			input.Statement,
			input.Scope,
			capabilities,
			"",
		) {
			return SelfDirectiveMutationResult{}, false, errors.New(
				"Self directive revision idempotency conflict",
			)
		}
		result, resultErr := selfDirectiveResultForEvent(detail, existingEvent)
		return result, false, resultErr
	}
	if !errors.Is(eventErr, sql.ErrNoRows) {
		return SelfDirectiveMutationResult{}, false, eventErr
	}
	if used, err := selfDirectiveRootUsesIdempotencyTx(tx, input.IdempotencyKey); err != nil {
		return SelfDirectiveMutationResult{}, false, err
	} else if used {
		return SelfDirectiveMutationResult{}, false, errors.New(
			"Self directive revision idempotency key belongs to a creation",
		)
	}
	detail, err := loadSelfDirectiveDetailTx(tx, root.ID)
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	if detail.Current.Status == selfModelStatusRetracted {
		return SelfDirectiveMutationResult{}, false, errors.New(
			"retracted Self directives cannot be revised",
		)
	}
	if detail.Current.ID != input.ExpectedSelfEntryID {
		return SelfDirectiveMutationResult{}, false, errors.New(
			"Self directive revision is stale; reload the current Self entry",
		)
	}
	if input.OccurredAt < detail.Events[len(detail.Events)-1].OccurredAt {
		return SelfDirectiveMutationResult{}, false, errors.New(
			"Self directive revision cannot precede the current revision",
		)
	}
	event := SelfDirectiveEventRecord{
		ID:                  deterministicAgencyID("self-user-directive-event", input.IdempotencyKey),
		DirectiveID:         root.ID,
		IdempotencyKey:      input.IdempotencyKey,
		Sequence:            len(detail.Events) + 1,
		EventType:           SelfDirectiveEventRevised,
		ExpectedSelfEntryID: input.ExpectedSelfEntryID,
		Statement:           input.Statement,
		Scope:               input.Scope,
		DeniedCapabilities:  capabilities,
		OccurredAt:          input.OccurredAt,
		CreatedAt:           input.OccurredAt,
	}
	event, entry, err := appendSelfDirectiveRevisionTx(tx, root, event)
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	detail, err = loadSelfDirectiveDetailTx(tx, root.ID)
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	result, err := selfDirectiveResultForEvent(detail, event)
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	if result.SelfEntry.ID != entry.ID {
		return SelfDirectiveMutationResult{}, false, errors.New(
			"Self directive revision result changed during validation",
		)
	}
	if err := tx.Commit(); err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	return result, true, nil
}

func normalizeRetractSelfDirective(
	input RetractSelfDirectiveInput,
) (RetractSelfDirectiveInput, error) {
	var err error
	input.DirectiveID = strings.TrimSpace(input.DirectiveID)
	input.ExpectedSelfEntryID = strings.TrimSpace(input.ExpectedSelfEntryID)
	if input.DirectiveID == "" || input.ExpectedSelfEntryID == "" {
		return RetractSelfDirectiveInput{}, errors.New(
			"Self directive retraction requires directive and current Self entry IDs",
		)
	}
	if input.IdempotencyKey, err = normalizeSelfDirectiveIdempotency(
		input.IdempotencyKey,
	); err != nil {
		return RetractSelfDirectiveInput{}, err
	}
	if input.Reason, err = normalizeSelfDirectiveText(
		input.Reason,
		"retraction reason",
		selfDirectiveMaxReasonBytes,
		true,
	); err != nil {
		return RetractSelfDirectiveInput{}, err
	}
	if input.OccurredAt <= 0 {
		return RetractSelfDirectiveInput{}, errors.New(
			"Self directive retraction occurrence time is required",
		)
	}
	return input, nil
}

func (store *SelfDirectiveStore) Retract(
	input RetractSelfDirectiveInput,
) (SelfDirectiveMutationResult, bool, error) {
	if err := store.require(); err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	input, err := normalizeRetractSelfDirective(input)
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	tx, err := store.db.Begin()
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	defer tx.Rollback()
	root, err := getSelfDirectiveRootTx(tx, input.DirectiveID)
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	existingEvent, eventErr := getSelfDirectiveEventByIdempotencyTx(
		tx,
		input.IdempotencyKey,
	)
	if eventErr == nil {
		detail, detailErr := loadSelfDirectiveDetailTx(tx, existingEvent.DirectiveID)
		if detailErr != nil {
			return SelfDirectiveMutationResult{}, false, detailErr
		}
		var previous SelfDirectiveEventRecord
		if existingEvent.Sequence > 1 && existingEvent.Sequence <= len(detail.Events) {
			previous = detail.Events[existingEvent.Sequence-2]
		}
		if !selfDirectiveOperationMatches(
			existingEvent,
			SelfDirectiveEventRetracted,
			input.DirectiveID,
			input.ExpectedSelfEntryID,
			previous.Statement,
			previous.Scope,
			previous.DeniedCapabilities,
			input.Reason,
		) {
			return SelfDirectiveMutationResult{}, false, errors.New(
				"Self directive retraction idempotency conflict",
			)
		}
		result, resultErr := selfDirectiveResultForEvent(detail, existingEvent)
		return result, false, resultErr
	}
	if !errors.Is(eventErr, sql.ErrNoRows) {
		return SelfDirectiveMutationResult{}, false, eventErr
	}
	if used, err := selfDirectiveRootUsesIdempotencyTx(tx, input.IdempotencyKey); err != nil {
		return SelfDirectiveMutationResult{}, false, err
	} else if used {
		return SelfDirectiveMutationResult{}, false, errors.New(
			"Self directive retraction idempotency key belongs to a creation",
		)
	}
	detail, err := loadSelfDirectiveDetailTx(tx, root.ID)
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	if detail.Current.Status == selfModelStatusRetracted {
		return SelfDirectiveMutationResult{}, false, errors.New(
			"Self directive is already retracted",
		)
	}
	if detail.Current.ID != input.ExpectedSelfEntryID {
		return SelfDirectiveMutationResult{}, false, errors.New(
			"Self directive retraction is stale; reload the current Self entry",
		)
	}
	if input.OccurredAt < detail.Events[len(detail.Events)-1].OccurredAt {
		return SelfDirectiveMutationResult{}, false, errors.New(
			"Self directive retraction cannot precede the current revision",
		)
	}
	previous := detail.Events[len(detail.Events)-1]
	event := SelfDirectiveEventRecord{
		ID:                  deterministicAgencyID("self-user-directive-event", input.IdempotencyKey),
		DirectiveID:         root.ID,
		IdempotencyKey:      input.IdempotencyKey,
		Sequence:            len(detail.Events) + 1,
		EventType:           SelfDirectiveEventRetracted,
		ExpectedSelfEntryID: input.ExpectedSelfEntryID,
		Statement:           previous.Statement,
		Scope:               previous.Scope,
		DeniedCapabilities:  append([]string(nil), previous.DeniedCapabilities...),
		RetractionReason:    input.Reason,
		OccurredAt:          input.OccurredAt,
		CreatedAt:           input.OccurredAt,
	}
	event, entry, err := appendSelfDirectiveRevisionTx(tx, root, event)
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	detail, err = loadSelfDirectiveDetailTx(tx, root.ID)
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	result, err := selfDirectiveResultForEvent(detail, event)
	if err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	if result.SelfEntry.ID != entry.ID {
		return SelfDirectiveMutationResult{}, false, errors.New(
			"Self directive retraction result changed during validation",
		)
	}
	if err := tx.Commit(); err != nil {
		return SelfDirectiveMutationResult{}, false, err
	}
	return result, true, nil
}
