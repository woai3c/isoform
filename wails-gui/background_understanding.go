package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	BackgroundUnderstandingPolicyVersion = "background-understanding-v2-computer-use"

	BackgroundMissionRetainedActivity = "retained_activity"
	BackgroundMissionGmail            = "gmail"

	BackgroundSourceLocalActivity = "local_activity"
	BackgroundSourceGmail         = "gmail"

	BackgroundTriggerAutomatic = "automatic"
	BackgroundTriggerManual    = "manual"

	BackgroundRunSelected  = "selected"
	BackgroundRunRunning   = "running"
	BackgroundRunCompleted = "completed"
	BackgroundRunSkipped   = "skipped"
	BackgroundRunFailed    = "failed"
	BackgroundRunCancelled = "cancelled"

	BackgroundGrantIssued  = "issued"
	BackgroundGrantRevoked = "revoked"

	BackgroundArtifactMemory             = "memory"
	BackgroundArtifactKnowledge          = "knowledge"
	BackgroundArtifactWorkPatternProfile = "work_pattern_profile"

	BackgroundMaterializationCreated  = "created"
	BackgroundMaterializationUpdated  = "updated"
	BackgroundMaterializationExisting = "existing"
	BackgroundMaterializationSkipped  = "skipped"

	BackgroundGmailComputerUseRead = "gmail.computer_use.read"

	backgroundDefaultDailyRunLimit      = 4
	backgroundDefaultMinIntervalMinutes = 360
	backgroundDefaultGmailLookbackDays  = 14
	backgroundDefaultGmailMaxItems      = 10
	backgroundMaximumRunList            = 100
)

var backgroundTerminalRunStates = map[string]bool{
	BackgroundRunCompleted: true,
	BackgroundRunSkipped:   true,
	BackgroundRunFailed:    true,
	BackgroundRunCancelled: true,
}

type BackgroundUnderstandingSettings struct {
	ID                 string `json:"id,omitempty"`
	Enabled            bool   `json:"enabled"`
	DailyRunLimit      int    `json:"dailyRunLimit"`
	MinIntervalMinutes int    `json:"minIntervalMinutes"`
	Reason             string `json:"reason,omitempty"`
	UpdatedAt          int64  `json:"updatedAt,omitempty"`
}

type SetBackgroundUnderstandingSettingsInput struct {
	IdempotencyKey     string `json:"idempotencyKey"`
	Enabled            bool   `json:"enabled"`
	DailyRunLimit      int    `json:"dailyRunLimit,omitempty"`
	MinIntervalMinutes int    `json:"minIntervalMinutes,omitempty"`
	Reason             string `json:"reason,omitempty"`
}

type BackgroundSourceGrant struct {
	ID                string   `json:"id"`
	IdempotencyKey    string   `json:"idempotencyKey"`
	Source            string   `json:"source"`
	ScopeLabel        string   `json:"scopeLabel"`
	SourceQuery       string   `json:"sourceQuery"`
	LookbackDays      int      `json:"lookbackDays"`
	MaxItemsPerRun    int      `json:"maxItemsPerRun"`
	AllowedOperations []string `json:"allowedOperations"`
	PolicyVersion     string   `json:"policyVersion"`
	IssuedAt          int64    `json:"issuedAt"`
	ExpiresAt         int64    `json:"expiresAt,omitempty"`
	Status            string   `json:"status"`
	CreatedAt         int64    `json:"createdAt"`
}

type GrantBackgroundUnderstandingSourceInput struct {
	IdempotencyKey string `json:"idempotencyKey"`
	Source         string `json:"source"`
	ScopeLabel     string `json:"scopeLabel"`
	SourceQuery    string `json:"sourceQuery"`
	LookbackDays   int    `json:"lookbackDays,omitempty"`
	MaxItemsPerRun int    `json:"maxItemsPerRun,omitempty"`
	ExpiresAt      int64  `json:"expiresAt,omitempty"`
}

type RevokeBackgroundUnderstandingSourceInput struct {
	IdempotencyKey string `json:"idempotencyKey"`
	GrantID        string `json:"grantId"`
	Reason         string `json:"reason,omitempty"`
}

type BackgroundUnderstandingRun struct {
	ID              string `json:"id"`
	IdempotencyKey  string `json:"idempotencyKey"`
	TriggerKind     string `json:"triggerKind"`
	TriggerReason   string `json:"triggerReason"`
	MissionKind     string `json:"missionKind"`
	Source          string `json:"source"`
	GrantID         string `json:"grantId,omitempty"`
	PolicyVersion   string `json:"policyVersion"`
	SourceScopeJSON string `json:"sourceScopeJson"`
	StartedAt       int64  `json:"startedAt"`
	CreatedAt       int64  `json:"createdAt"`
	CurrentState    string `json:"currentState"`
	UpdatedAt       int64  `json:"updatedAt"`
	DetailJSON      string `json:"detailJson"`
}

type BackgroundUnderstandingRunEvent struct {
	ID         string `json:"id"`
	RunID      string `json:"runId"`
	Sequence   int    `json:"sequence"`
	State      string `json:"state"`
	DetailJSON string `json:"detailJson"`
	DetailHash string `json:"detailHash"`
	OccurredAt int64  `json:"occurredAt"`
	CreatedAt  int64  `json:"createdAt"`
}

type BackgroundUnderstandingSourceItem struct {
	ID              string `json:"id"`
	RunID           string `json:"runId"`
	Source          string `json:"source"`
	SourceItemKind  string `json:"sourceItemKind"`
	ExternalIDHash  string `json:"externalIdHash"`
	ThreadIDHash    string `json:"threadIdHash,omitempty"`
	ContentHash     string `json:"contentHash"`
	AttributionKind string `json:"attributionKind"`
	ObservedAt      int64  `json:"observedAt,omitempty"`
	MetadataJSON    string `json:"metadataJson"`
	CreatedAt       int64  `json:"createdAt"`
}

type BackgroundUnderstandingMaterialization struct {
	ID                string `json:"id"`
	RunID             string `json:"runId"`
	ArtifactKind      string `json:"artifactKind"`
	ArtifactID        string `json:"artifactId"`
	Action            string `json:"action"`
	Summary           string `json:"summary"`
	SourceReceiptHash string `json:"sourceReceiptHash,omitempty"`
	CreatedAt         int64  `json:"createdAt"`
}

type BackgroundUnderstandingRunDetail struct {
	Run              BackgroundUnderstandingRun               `json:"run"`
	Events           []BackgroundUnderstandingRunEvent        `json:"events"`
	SourceItems      []BackgroundUnderstandingSourceItem      `json:"sourceItems"`
	Materializations []BackgroundUnderstandingMaterialization `json:"materializations"`
}

type backgroundUnderstandingStore struct {
	db *sql.DB
}

func newBackgroundUnderstandingStore(
	db *sql.DB,
) (*backgroundUnderstandingStore, error) {
	if db == nil {
		return nil, errors.New("background understanding storage is unavailable")
	}
	return &backgroundUnderstandingStore{db: db}, nil
}

func defaultBackgroundUnderstandingSettings() BackgroundUnderstandingSettings {
	return BackgroundUnderstandingSettings{
		Enabled:            true,
		DailyRunLimit:      backgroundDefaultDailyRunLimit,
		MinIntervalMinutes: backgroundDefaultMinIntervalMinutes,
	}
}

func backgroundJSON(value any, fallback string) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return fallback
	}
	return string(raw)
}

func backgroundBool(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (store *backgroundUnderstandingStore) Settings() (
	BackgroundUnderstandingSettings,
	error,
) {
	if store == nil || store.db == nil {
		return BackgroundUnderstandingSettings{},
			errors.New("background understanding storage is unavailable")
	}
	result := defaultBackgroundUnderstandingSettings()
	var enabled int
	err := store.db.QueryRow(`
		SELECT id, enabled, daily_run_limit, min_interval_minutes,
			reason, created_at
		FROM background_understanding_setting_events_v1
		ORDER BY created_at DESC, id DESC
		LIMIT 1`,
	).Scan(
		&result.ID,
		&enabled,
		&result.DailyRunLimit,
		&result.MinIntervalMinutes,
		&result.Reason,
		&result.UpdatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return result, nil
	}
	if err != nil {
		return BackgroundUnderstandingSettings{}, err
	}
	result.Enabled = enabled != 0
	return result, nil
}

func normalizeBackgroundSettingsInput(
	input SetBackgroundUnderstandingSettingsInput,
) (SetBackgroundUnderstandingSettingsInput, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.IdempotencyKey == "" {
		return input, errors.New(
			"background understanding settings require an idempotency key",
		)
	}
	if input.DailyRunLimit == 0 {
		input.DailyRunLimit = backgroundDefaultDailyRunLimit
	}
	if input.MinIntervalMinutes == 0 {
		input.MinIntervalMinutes = backgroundDefaultMinIntervalMinutes
	}
	if input.DailyRunLimit < 1 || input.DailyRunLimit > 24 {
		return input, errors.New(
			"background understanding daily run limit must be between 1 and 24",
		)
	}
	if input.MinIntervalMinutes < 5 || input.MinIntervalMinutes > 1440 {
		return input, errors.New(
			"background understanding interval must be between 5 and 1440 minutes",
		)
	}
	input.Reason = agencyBoundedText(input.Reason, 500)
	return input, nil
}

func (store *backgroundUnderstandingStore) SetSettings(
	input SetBackgroundUnderstandingSettingsInput,
	at int64,
) (BackgroundUnderstandingSettings, bool, error) {
	input, err := normalizeBackgroundSettingsInput(input)
	if err != nil {
		return BackgroundUnderstandingSettings{}, false, err
	}
	if at <= 0 {
		return BackgroundUnderstandingSettings{}, false,
			errors.New("background understanding settings require a timestamp")
	}
	id := deterministicAgencyID(
		"background-understanding-settings",
		input.IdempotencyKey,
	)
	result, err := store.settingsByID(id)
	if err == nil {
		return result, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return BackgroundUnderstandingSettings{}, false, err
	}
	_, err = store.db.Exec(`
		INSERT INTO background_understanding_setting_events_v1 (
			id, idempotency_key, enabled, daily_run_limit,
			min_interval_minutes, reason, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id,
		input.IdempotencyKey,
		backgroundBool(input.Enabled),
		input.DailyRunLimit,
		input.MinIntervalMinutes,
		input.Reason,
		at,
	)
	if err != nil {
		return BackgroundUnderstandingSettings{}, false, err
	}
	result, err = store.settingsByID(id)
	return result, err == nil, err
}

func (store *backgroundUnderstandingStore) settingsByID(
	id string,
) (BackgroundUnderstandingSettings, error) {
	var result BackgroundUnderstandingSettings
	var enabled int
	err := store.db.QueryRow(`
		SELECT id, enabled, daily_run_limit, min_interval_minutes,
			reason, created_at
		FROM background_understanding_setting_events_v1
		WHERE id = ?`,
		id,
	).Scan(
		&result.ID,
		&enabled,
		&result.DailyRunLimit,
		&result.MinIntervalMinutes,
		&result.Reason,
		&result.UpdatedAt,
	)
	result.Enabled = enabled != 0
	return result, err
}

func normalizeBackgroundGrantInput(
	input GrantBackgroundUnderstandingSourceInput,
	at int64,
) (GrantBackgroundUnderstandingSourceInput, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.Source = strings.ToLower(strings.TrimSpace(input.Source))
	input.ScopeLabel = agencyBoundedText(input.ScopeLabel, 120)
	input.SourceQuery = strings.Join(
		strings.Fields(strings.TrimSpace(input.SourceQuery)),
		" ",
	)
	if input.IdempotencyKey == "" {
		return input, errors.New("background source grant requires idempotency key")
	}
	if input.Source != BackgroundSourceGmail {
		return input, fmt.Errorf(
			"unsupported background understanding source %q",
			input.Source,
		)
	}
	if input.ScopeLabel == "" {
		input.ScopeLabel = "Recent correspondence"
	}
	if input.LookbackDays == 0 {
		input.LookbackDays = backgroundDefaultGmailLookbackDays
	}
	if input.MaxItemsPerRun == 0 {
		input.MaxItemsPerRun = backgroundDefaultGmailMaxItems
	}
	if input.LookbackDays < 1 || input.LookbackDays > 365 {
		return input, errors.New("Gmail lookback must be between 1 and 365 days")
	}
	if input.MaxItemsPerRun < 1 || input.MaxItemsPerRun > 50 {
		return input, errors.New("Gmail item limit must be between 1 and 50")
	}
	if input.ExpiresAt != 0 && input.ExpiresAt <= at {
		return input, errors.New("background source grant expiry must be in the future")
	}
	return input, nil
}

func (store *backgroundUnderstandingStore) GrantSource(
	input GrantBackgroundUnderstandingSourceInput,
	at int64,
) (BackgroundSourceGrant, bool, error) {
	input, err := normalizeBackgroundGrantInput(input, at)
	if err != nil {
		return BackgroundSourceGrant{}, false, err
	}
	id := deterministicAgencyID("background-source-grant", input.IdempotencyKey)
	existing, err := store.grantByID(id, at)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return BackgroundSourceGrant{}, false, err
	}

	allowed := []string{BackgroundGmailComputerUseRead}
	payload := map[string]any{
		"source":            input.Source,
		"scopeLabel":        input.ScopeLabel,
		"sourceQuery":       input.SourceQuery,
		"lookbackDays":      input.LookbackDays,
		"maxItemsPerRun":    input.MaxItemsPerRun,
		"allowedOperations": allowed,
		"policyVersion":     BackgroundUnderstandingPolicyVersion,
		"issuedAt":          at,
		"expiresAt":         input.ExpiresAt,
	}
	payloadJSON := backgroundJSON(payload, "{}")
	previousGrants, err := store.ListSourceGrants(input.Source, at)
	if err != nil {
		return BackgroundSourceGrant{}, false, err
	}
	tx, err := store.db.Begin()
	if err != nil {
		return BackgroundSourceGrant{}, false, err
	}
	defer tx.Rollback()
	for _, previous := range previousGrants {
		if previous.Status != "active" {
			continue
		}
		eventKey := input.IdempotencyKey + ":supersede:" + previous.ID
		eventPayload := backgroundJSON(
			map[string]any{
				"eventType":    BackgroundGrantRevoked,
				"reason":       "Source scope was replaced by a new grant.",
				"supersededBy": id,
			},
			"{}",
		)
		if _, err := tx.Exec(`
			INSERT INTO background_source_grant_events_v1 (
				id, grant_id, idempotency_key, event_type, reason,
				occurred_at, payload_json, payload_hash, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			deterministicAgencyID(
				"background-source-grant-event",
				eventKey,
			),
			previous.ID,
			eventKey,
			BackgroundGrantRevoked,
			"Source scope was replaced by a new grant.",
			at,
			eventPayload,
			agencyPayloadHash([]byte(eventPayload)),
			at,
		); err != nil {
			return BackgroundSourceGrant{}, false, err
		}
	}
	if _, err := tx.Exec(`
		INSERT INTO background_source_grants_v1 (
			id, idempotency_key, source, scope_label, source_query,
			lookback_days, max_items_per_run, allowed_operations_json,
			policy_version, issued_at, expires_at, payload_json,
			payload_hash, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id,
		input.IdempotencyKey,
		input.Source,
		input.ScopeLabel,
		input.SourceQuery,
		input.LookbackDays,
		input.MaxItemsPerRun,
		backgroundJSON(allowed, "[]"),
		BackgroundUnderstandingPolicyVersion,
		at,
		input.ExpiresAt,
		payloadJSON,
		agencyPayloadHash([]byte(payloadJSON)),
		at,
	); err != nil {
		return BackgroundSourceGrant{}, false, err
	}
	eventKey := input.IdempotencyKey + ":issued"
	eventPayload := backgroundJSON(
		map[string]any{"eventType": BackgroundGrantIssued},
		"{}",
	)
	if _, err := tx.Exec(`
		INSERT INTO background_source_grant_events_v1 (
			id, grant_id, idempotency_key, event_type, reason,
			occurred_at, payload_json, payload_hash, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		deterministicAgencyID("background-source-grant-event", eventKey),
		id,
		eventKey,
		BackgroundGrantIssued,
		"User allowed bounded background understanding.",
		at,
		eventPayload,
		agencyPayloadHash([]byte(eventPayload)),
		at,
	); err != nil {
		return BackgroundSourceGrant{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return BackgroundSourceGrant{}, false, err
	}
	result, err := store.grantByID(id, at)
	return result, err == nil, err
}

func (store *backgroundUnderstandingStore) RevokeSource(
	input RevokeBackgroundUnderstandingSourceInput,
	at int64,
) (BackgroundSourceGrant, bool, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.GrantID = strings.TrimSpace(input.GrantID)
	input.Reason = agencyBoundedText(input.Reason, 500)
	if input.IdempotencyKey == "" || input.GrantID == "" {
		return BackgroundSourceGrant{}, false,
			errors.New("background source revocation requires grant and idempotency keys")
	}
	grant, err := store.grantByID(input.GrantID, at)
	if err != nil {
		return BackgroundSourceGrant{}, false, err
	}
	eventID := deterministicAgencyID(
		"background-source-grant-event",
		input.IdempotencyKey,
	)
	var found int
	err = store.db.QueryRow(`
		SELECT COUNT(*)
		FROM background_source_grant_events_v1
		WHERE id = ?`,
		eventID,
	).Scan(&found)
	if err != nil {
		return BackgroundSourceGrant{}, false, err
	}
	if found > 0 {
		updated, err := store.grantByID(input.GrantID, at)
		return updated, false, err
	}
	payloadJSON := backgroundJSON(
		map[string]any{
			"eventType": BackgroundGrantRevoked,
			"reason":    input.Reason,
		},
		"{}",
	)
	_, err = store.db.Exec(`
		INSERT INTO background_source_grant_events_v1 (
			id, grant_id, idempotency_key, event_type, reason,
			occurred_at, payload_json, payload_hash, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		eventID,
		grant.ID,
		input.IdempotencyKey,
		BackgroundGrantRevoked,
		input.Reason,
		at,
		payloadJSON,
		agencyPayloadHash([]byte(payloadJSON)),
		at,
	)
	if err != nil {
		return BackgroundSourceGrant{}, false, err
	}
	updated, err := store.grantByID(input.GrantID, at)
	return updated, err == nil, err
}

const backgroundGrantSelect = `
	grant.id, grant.idempotency_key, grant.source, grant.scope_label,
	grant.source_query, grant.lookback_days, grant.max_items_per_run,
	grant.allowed_operations_json, grant.policy_version, grant.issued_at,
	grant.expires_at, grant.created_at,
	COALESCE((
		SELECT event.event_type
		FROM background_source_grant_events_v1 event
		WHERE event.grant_id = grant.id
		ORDER BY event.occurred_at DESC, event.id DESC
		LIMIT 1
	), '')`

func scanBackgroundSourceGrant(
	scanner interface{ Scan(...any) error },
	at int64,
) (BackgroundSourceGrant, error) {
	var result BackgroundSourceGrant
	var allowedJSON, latestEvent string
	if err := scanner.Scan(
		&result.ID,
		&result.IdempotencyKey,
		&result.Source,
		&result.ScopeLabel,
		&result.SourceQuery,
		&result.LookbackDays,
		&result.MaxItemsPerRun,
		&allowedJSON,
		&result.PolicyVersion,
		&result.IssuedAt,
		&result.ExpiresAt,
		&result.CreatedAt,
		&latestEvent,
	); err != nil {
		return BackgroundSourceGrant{}, err
	}
	if err := json.Unmarshal(
		[]byte(allowedJSON),
		&result.AllowedOperations,
	); err != nil {
		return BackgroundSourceGrant{}, err
	}
	switch {
	case latestEvent == BackgroundGrantRevoked:
		result.Status = "revoked"
	case result.ExpiresAt > 0 && result.ExpiresAt <= at:
		result.Status = "expired"
	case latestEvent == BackgroundGrantIssued:
		result.Status = "active"
	default:
		result.Status = "invalid"
	}
	return result, nil
}

func (store *backgroundUnderstandingStore) grantByID(
	id string,
	at int64,
) (BackgroundSourceGrant, error) {
	return scanBackgroundSourceGrant(store.db.QueryRow(`
		SELECT `+backgroundGrantSelect+`
		FROM background_source_grants_v1 grant
		WHERE grant.id = ?`,
		strings.TrimSpace(id),
	), at)
}

func (store *backgroundUnderstandingStore) ListSourceGrants(
	source string,
	at int64,
) ([]BackgroundSourceGrant, error) {
	source = strings.TrimSpace(source)
	query := `
		SELECT ` + backgroundGrantSelect + `
		FROM background_source_grants_v1 grant`
	args := []any{}
	if source != "" {
		query += ` WHERE grant.source = ?`
		args = append(args, source)
	}
	query += ` ORDER BY grant.issued_at DESC, grant.id DESC`
	rows, err := store.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []BackgroundSourceGrant{}
	for rows.Next() {
		item, err := scanBackgroundSourceGrant(rows, at)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *backgroundUnderstandingStore) ActiveSourceGrant(
	source string,
	at int64,
) (BackgroundSourceGrant, error) {
	items, err := store.ListSourceGrants(source, at)
	if err != nil {
		return BackgroundSourceGrant{}, err
	}
	for _, item := range items {
		if item.Status == "active" {
			return item, nil
		}
	}
	return BackgroundSourceGrant{}, sql.ErrNoRows
}

func normalizeBackgroundRunRoot(
	run BackgroundUnderstandingRun,
) (BackgroundUnderstandingRun, error) {
	run.IdempotencyKey = strings.TrimSpace(run.IdempotencyKey)
	run.TriggerKind = strings.TrimSpace(run.TriggerKind)
	run.TriggerReason = agencyBoundedText(run.TriggerReason, 500)
	run.MissionKind = strings.TrimSpace(run.MissionKind)
	run.Source = strings.TrimSpace(run.Source)
	run.GrantID = strings.TrimSpace(run.GrantID)
	if run.IdempotencyKey == "" || run.TriggerReason == "" ||
		run.StartedAt <= 0 {
		return run, errors.New(
			"background understanding run requires idempotency, reason, and timestamp",
		)
	}
	if run.TriggerKind != BackgroundTriggerAutomatic &&
		run.TriggerKind != BackgroundTriggerManual {
		return run, fmt.Errorf(
			"invalid background understanding trigger %q",
			run.TriggerKind,
		)
	}
	switch run.MissionKind {
	case BackgroundMissionRetainedActivity:
		if run.Source != BackgroundSourceLocalActivity || run.GrantID != "" {
			return run, errors.New(
				"retained activity mission must use local activity without a grant",
			)
		}
	case BackgroundMissionGmail:
		if run.Source != BackgroundSourceGmail || run.GrantID == "" {
			return run, errors.New("Gmail mission requires an exact source grant")
		}
	default:
		return run, fmt.Errorf(
			"invalid background understanding mission %q",
			run.MissionKind,
		)
	}
	if !json.Valid([]byte(run.SourceScopeJSON)) {
		return run, errors.New("background understanding source scope must be JSON")
	}
	run.PolicyVersion = BackgroundUnderstandingPolicyVersion
	run.CreatedAt = run.StartedAt
	run.ID = deterministicAgencyID(
		"background-understanding-run",
		run.IdempotencyKey,
	)
	return run, nil
}

func (store *backgroundUnderstandingStore) AddRun(
	run BackgroundUnderstandingRun,
	selectedDetail any,
) (BackgroundUnderstandingRun, bool, error) {
	run, err := normalizeBackgroundRunRoot(run)
	if err != nil {
		return BackgroundUnderstandingRun{}, false, err
	}
	existing, err := store.GetRun(run.ID)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return BackgroundUnderstandingRun{}, false, err
	}
	detailJSON := backgroundJSON(selectedDetail, "{}")
	tx, err := store.db.Begin()
	if err != nil {
		return BackgroundUnderstandingRun{}, false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		INSERT INTO background_understanding_runs_v1 (
			id, idempotency_key, trigger_kind, trigger_reason,
			mission_kind, source, grant_id, policy_version,
			source_scope_json, started_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID,
		run.IdempotencyKey,
		run.TriggerKind,
		run.TriggerReason,
		run.MissionKind,
		run.Source,
		run.GrantID,
		run.PolicyVersion,
		run.SourceScopeJSON,
		run.StartedAt,
		run.CreatedAt,
	); err != nil {
		return BackgroundUnderstandingRun{}, false, err
	}
	if _, err := tx.Exec(`
		INSERT INTO background_understanding_run_events_v1 (
			id, run_id, sequence, state, detail_json, detail_hash,
			occurred_at, created_at
		) VALUES (?, ?, 1, ?, ?, ?, ?, ?)`,
		deterministicAgencyID(
			"background-understanding-run-event",
			run.ID+"\x001",
		),
		run.ID,
		BackgroundRunSelected,
		detailJSON,
		agencyPayloadHash([]byte(detailJSON)),
		run.StartedAt,
		run.StartedAt,
	); err != nil {
		return BackgroundUnderstandingRun{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return BackgroundUnderstandingRun{}, false, err
	}
	persisted, err := store.GetRun(run.ID)
	return persisted, err == nil, err
}

func validBackgroundRunTransition(from, to string) bool {
	switch from {
	case BackgroundRunSelected:
		return to == BackgroundRunRunning ||
			to == BackgroundRunSkipped ||
			to == BackgroundRunCancelled ||
			to == BackgroundRunFailed
	case BackgroundRunRunning:
		return backgroundTerminalRunStates[to]
	default:
		return false
	}
}

func (store *backgroundUnderstandingStore) AppendRunEvent(
	runID, state string,
	detail any,
	at int64,
) (BackgroundUnderstandingRunEvent, error) {
	runID = strings.TrimSpace(runID)
	state = strings.TrimSpace(state)
	if runID == "" || at <= 0 {
		return BackgroundUnderstandingRunEvent{},
			errors.New("background run event requires run and timestamp")
	}
	tx, err := store.db.Begin()
	if err != nil {
		return BackgroundUnderstandingRunEvent{}, err
	}
	defer tx.Rollback()
	var sequence int
	var from string
	var previousAt int64
	if err := tx.QueryRow(`
		SELECT sequence, state, occurred_at
		FROM background_understanding_run_events_v1
		WHERE run_id = ?
		ORDER BY sequence DESC
		LIMIT 1`,
		runID,
	).Scan(&sequence, &from, &previousAt); err != nil {
		return BackgroundUnderstandingRunEvent{}, err
	}
	if !validBackgroundRunTransition(from, state) {
		return BackgroundUnderstandingRunEvent{}, fmt.Errorf(
			"invalid background run transition %s -> %s",
			from,
			state,
		)
	}
	if at < previousAt {
		return BackgroundUnderstandingRunEvent{}, fmt.Errorf(
			"background run event timestamp %d precedes prior event %d",
			at,
			previousAt,
		)
	}
	sequence++
	detailJSON := backgroundJSON(detail, "{}")
	event := BackgroundUnderstandingRunEvent{
		ID: deterministicAgencyID(
			"background-understanding-run-event",
			fmt.Sprintf("%s\x00%d", runID, sequence),
		),
		RunID:      runID,
		Sequence:   sequence,
		State:      state,
		DetailJSON: detailJSON,
		DetailHash: agencyPayloadHash([]byte(detailJSON)),
		OccurredAt: at,
		CreatedAt:  at,
	}
	if _, err := tx.Exec(`
		INSERT INTO background_understanding_run_events_v1 (
			id, run_id, sequence, state, detail_json, detail_hash,
			occurred_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.RunID,
		event.Sequence,
		event.State,
		event.DetailJSON,
		event.DetailHash,
		event.OccurredAt,
		event.CreatedAt,
	); err != nil {
		return BackgroundUnderstandingRunEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return BackgroundUnderstandingRunEvent{}, err
	}
	return event, nil
}

const backgroundRunSelect = `
	run.id, run.idempotency_key, run.trigger_kind, run.trigger_reason,
	run.mission_kind, run.source, run.grant_id, run.policy_version,
	run.source_scope_json, run.started_at, run.created_at,
	current.state, current.occurred_at, current.detail_json`

func scanBackgroundRun(
	scanner interface{ Scan(...any) error },
) (BackgroundUnderstandingRun, error) {
	var result BackgroundUnderstandingRun
	err := scanner.Scan(
		&result.ID,
		&result.IdempotencyKey,
		&result.TriggerKind,
		&result.TriggerReason,
		&result.MissionKind,
		&result.Source,
		&result.GrantID,
		&result.PolicyVersion,
		&result.SourceScopeJSON,
		&result.StartedAt,
		&result.CreatedAt,
		&result.CurrentState,
		&result.UpdatedAt,
		&result.DetailJSON,
	)
	return result, err
}

func (store *backgroundUnderstandingStore) GetRun(
	id string,
) (BackgroundUnderstandingRun, error) {
	return scanBackgroundRun(store.db.QueryRow(`
		SELECT `+backgroundRunSelect+`
		FROM background_understanding_runs_v1 run
		JOIN background_understanding_run_events_v1 current
			ON current.run_id = run.id
			AND current.sequence = (
				SELECT MAX(candidate.sequence)
				FROM background_understanding_run_events_v1 candidate
				WHERE candidate.run_id = run.id
			)
		WHERE run.id = ?`,
		strings.TrimSpace(id),
	))
}

func (store *backgroundUnderstandingStore) ListRuns(
	limit int,
) ([]BackgroundUnderstandingRun, error) {
	if limit <= 0 || limit > backgroundMaximumRunList {
		limit = backgroundMaximumRunList
	}
	rows, err := store.db.Query(`
		SELECT `+backgroundRunSelect+`
		FROM background_understanding_runs_v1 run
		JOIN background_understanding_run_events_v1 current
			ON current.run_id = run.id
			AND current.sequence = (
				SELECT MAX(candidate.sequence)
				FROM background_understanding_run_events_v1 candidate
				WHERE candidate.run_id = run.id
			)
		ORDER BY run.created_at DESC, run.id DESC
		LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []BackgroundUnderstandingRun{}
	for rows.Next() {
		item, err := scanBackgroundRun(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (store *backgroundUnderstandingStore) CountRunsSince(
	since int64,
) (int, error) {
	var result int
	err := store.db.QueryRow(`
		SELECT COUNT(*)
		FROM background_understanding_runs_v1
		WHERE created_at >= ?`,
		since,
	).Scan(&result)
	return result, err
}

func (store *backgroundUnderstandingStore) DailyUsageSince(
	since int64,
	dailyRunLimit int,
	reserveGmail bool,
) (backgroundUnderstandingDailyUsage, error) {
	result := backgroundUnderstandingDailyUsage{
		LocalRunLimit: dailyRunLimit,
		RunsByMissionKind: map[string]int{
			BackgroundMissionRetainedActivity: 0,
			BackgroundMissionGmail:            0,
		},
	}
	if store == nil || store.db == nil {
		return result, errors.New(
			"background understanding storage is unavailable",
		)
	}
	if dailyRunLimit < 1 {
		return result, errors.New(
			"background understanding daily run limit must be positive",
		)
	}
	rows, err := store.db.Query(`
		SELECT mission_kind
		FROM background_understanding_runs_v1
		WHERE created_at >= ?`,
		since,
	)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var mission string
		if err := rows.Scan(&mission); err != nil {
			return result, err
		}
		result.Attempts++
		result.RunsByMissionKind[mission]++
	}
	if err := rows.Err(); err != nil {
		return result, err
	}

	if reserveGmail {
		result.LocalRunLimit = max(0, dailyRunLimit-1)
	}
	localRuns := result.RunsByMissionKind[BackgroundMissionRetainedActivity]
	gmailRuns := result.RunsByMissionKind[BackgroundMissionGmail]
	result.Consumed = min(localRuns, result.LocalRunLimit) + gmailRuns
	for mission, count := range result.RunsByMissionKind {
		if mission == BackgroundMissionRetainedActivity ||
			mission == BackgroundMissionGmail {
			continue
		}
		result.Consumed += count
	}
	if reserveGmail && gmailRuns == 0 && result.Consumed < dailyRunLimit {
		result.Reserved = 1
	}
	return result, nil
}

func (store *backgroundUnderstandingStore) LatestRunForMission(
	mission string,
) (BackgroundUnderstandingRun, error) {
	if store == nil || store.db == nil {
		return BackgroundUnderstandingRun{}, errors.New(
			"background understanding storage is unavailable",
		)
	}
	return scanBackgroundRun(store.db.QueryRow(`
		SELECT `+backgroundRunSelect+`
		FROM background_understanding_runs_v1 run
		JOIN background_understanding_run_events_v1 current
			ON current.run_id = run.id
			AND current.sequence = (
				SELECT MAX(candidate.sequence)
				FROM background_understanding_run_events_v1 candidate
				WHERE candidate.run_id = run.id
			)
		WHERE run.mission_kind = ?
		ORDER BY run.created_at DESC, run.id DESC
		LIMIT 1`,
		strings.TrimSpace(mission),
	))
}

func (store *backgroundUnderstandingStore) AddSourceItem(
	item BackgroundUnderstandingSourceItem,
) (BackgroundUnderstandingSourceItem, bool, error) {
	item.RunID = strings.TrimSpace(item.RunID)
	item.Source = strings.TrimSpace(item.Source)
	item.SourceItemKind = strings.TrimSpace(item.SourceItemKind)
	item.ExternalIDHash = strings.TrimSpace(item.ExternalIDHash)
	item.ThreadIDHash = strings.TrimSpace(item.ThreadIDHash)
	item.ContentHash = strings.TrimSpace(item.ContentHash)
	item.AttributionKind = strings.TrimSpace(item.AttributionKind)
	if item.RunID == "" || item.Source == "" || item.SourceItemKind == "" ||
		item.ExternalIDHash == "" || item.ContentHash == "" {
		return BackgroundUnderstandingSourceItem{}, false,
			errors.New("background source item requires run, source, kind, and hashes")
	}
	if item.Source != BackgroundSourceLocalActivity &&
		item.Source != BackgroundSourceGmail {
		return BackgroundUnderstandingSourceItem{}, false,
			fmt.Errorf("invalid background source %q", item.Source)
	}
	switch item.AttributionKind {
	case "self", "other", "unknown", "aggregate":
	default:
		return BackgroundUnderstandingSourceItem{}, false,
			fmt.Errorf("invalid background source attribution %q", item.AttributionKind)
	}
	if !json.Valid([]byte(item.MetadataJSON)) {
		return BackgroundUnderstandingSourceItem{}, false,
			errors.New("background source metadata must be valid JSON")
	}
	if item.CreatedAt <= 0 {
		item.CreatedAt = time.Now().UnixMilli()
	}
	item.ID = deterministicAgencyID(
		"background-understanding-source-item",
		item.RunID+"\x00"+item.ExternalIDHash+"\x00"+item.ContentHash,
	)
	result := store.db.QueryRow(`
		SELECT id, run_id, source, source_item_kind, external_id_hash,
			thread_id_hash, content_hash, attribution_kind, observed_at,
			metadata_json, created_at
		FROM background_understanding_source_items_v1
		WHERE id = ?`,
		item.ID,
	)
	var existing BackgroundUnderstandingSourceItem
	err := result.Scan(
		&existing.ID,
		&existing.RunID,
		&existing.Source,
		&existing.SourceItemKind,
		&existing.ExternalIDHash,
		&existing.ThreadIDHash,
		&existing.ContentHash,
		&existing.AttributionKind,
		&existing.ObservedAt,
		&existing.MetadataJSON,
		&existing.CreatedAt,
	)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return BackgroundUnderstandingSourceItem{}, false, err
	}
	tx, err := store.db.Begin()
	if err != nil {
		return BackgroundUnderstandingSourceItem{}, false, err
	}
	defer tx.Rollback()
	var runSource, runState string
	if err := tx.QueryRow(`
		SELECT run.source, current.state
		FROM background_understanding_runs_v1 run
		JOIN background_understanding_run_events_v1 current
			ON current.run_id = run.id
			AND current.sequence = (
				SELECT MAX(candidate.sequence)
				FROM background_understanding_run_events_v1 candidate
				WHERE candidate.run_id = run.id
			)
		WHERE run.id = ?`,
		item.RunID,
	).Scan(&runSource, &runState); err != nil {
		return BackgroundUnderstandingSourceItem{}, false, err
	}
	if runState != BackgroundRunRunning {
		return BackgroundUnderstandingSourceItem{}, false, fmt.Errorf(
			"background source item requires a running mission, got %s",
			runState,
		)
	}
	if runSource != item.Source {
		return BackgroundUnderstandingSourceItem{}, false, fmt.Errorf(
			"background source item source %s does not match run source %s",
			item.Source,
			runSource,
		)
	}
	_, err = tx.Exec(`
		INSERT INTO background_understanding_source_items_v1 (
			id, run_id, source, source_item_kind, external_id_hash,
			thread_id_hash, content_hash, attribution_kind, observed_at,
			metadata_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID,
		item.RunID,
		item.Source,
		item.SourceItemKind,
		item.ExternalIDHash,
		item.ThreadIDHash,
		item.ContentHash,
		item.AttributionKind,
		item.ObservedAt,
		item.MetadataJSON,
		item.CreatedAt,
	)
	if err != nil {
		return BackgroundUnderstandingSourceItem{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return BackgroundUnderstandingSourceItem{}, false, err
	}
	return item, true, nil
}

func (store *backgroundUnderstandingStore) SourceItemSeen(
	source, externalIDHash, contentHash string,
) (bool, error) {
	var count int
	err := store.db.QueryRow(`
		SELECT COUNT(*)
		FROM background_understanding_source_items_v1
		WHERE source = ? AND external_id_hash = ? AND content_hash = ?`,
		strings.TrimSpace(source),
		strings.TrimSpace(externalIDHash),
		strings.TrimSpace(contentHash),
	).Scan(&count)
	return count > 0, err
}

func (store *backgroundUnderstandingStore) AddMaterialization(
	item BackgroundUnderstandingMaterialization,
) (BackgroundUnderstandingMaterialization, bool, error) {
	memoryMutationMu.RLock()
	defer memoryMutationMu.RUnlock()
	item.RunID = strings.TrimSpace(item.RunID)
	item.ArtifactKind = strings.TrimSpace(item.ArtifactKind)
	item.ArtifactID = strings.TrimSpace(item.ArtifactID)
	if item.ArtifactKind == BackgroundArtifactMemory && item.ArtifactID != "" {
		if err := validateMemoryForgetSource(store.db, "memory", item.ArtifactID, ""); err != nil {
			return BackgroundUnderstandingMaterialization{}, false, err
		}
	}
	item.Action = strings.TrimSpace(item.Action)
	item.Summary = agencyBoundedText(item.Summary, 500)
	if item.RunID == "" || item.ArtifactKind == "" || item.ArtifactID == "" ||
		item.Action == "" {
		return BackgroundUnderstandingMaterialization{}, false,
			errors.New("background materialization requires run, artifact, and action")
	}
	switch item.ArtifactKind {
	case BackgroundArtifactMemory,
		BackgroundArtifactKnowledge,
		BackgroundArtifactWorkPatternProfile:
	default:
		return BackgroundUnderstandingMaterialization{}, false,
			fmt.Errorf(
				"invalid background materialization artifact %q",
				item.ArtifactKind,
			)
	}
	switch item.Action {
	case BackgroundMaterializationCreated,
		BackgroundMaterializationUpdated,
		BackgroundMaterializationExisting,
		BackgroundMaterializationSkipped:
	default:
		return BackgroundUnderstandingMaterialization{}, false,
			fmt.Errorf(
				"invalid background materialization action %q",
				item.Action,
			)
	}
	if item.CreatedAt <= 0 {
		item.CreatedAt = time.Now().UnixMilli()
	}
	item.ID = deterministicAgencyID(
		"background-understanding-materialization",
		strings.Join([]string{
			item.RunID,
			item.ArtifactKind,
			item.ArtifactID,
			item.Action,
		}, "\x00"),
	)
	result, err := store.materializationByID(item.ID)
	if err == nil {
		return result, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return BackgroundUnderstandingMaterialization{}, false, err
	}
	tx, err := store.db.Begin()
	if err != nil {
		return BackgroundUnderstandingMaterialization{}, false, err
	}
	defer tx.Rollback()
	var runState string
	if err := tx.QueryRow(`
		SELECT current.state
		FROM background_understanding_run_events_v1 current
		WHERE current.run_id = ?
		ORDER BY current.sequence DESC
		LIMIT 1`,
		item.RunID,
	).Scan(&runState); err != nil {
		return BackgroundUnderstandingMaterialization{}, false, err
	}
	if runState != BackgroundRunRunning {
		return BackgroundUnderstandingMaterialization{}, false, fmt.Errorf(
			"background materialization requires a running mission, got %s",
			runState,
		)
	}
	_, err = tx.Exec(`
		INSERT INTO background_understanding_materializations_v1 (
			id, run_id, artifact_kind, artifact_id, action, summary,
			source_receipt_hash, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID,
		item.RunID,
		item.ArtifactKind,
		item.ArtifactID,
		item.Action,
		item.Summary,
		item.SourceReceiptHash,
		item.CreatedAt,
	)
	if err != nil {
		return BackgroundUnderstandingMaterialization{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return BackgroundUnderstandingMaterialization{}, false, err
	}
	return item, true, nil
}

func (store *backgroundUnderstandingStore) materializationByID(
	id string,
) (BackgroundUnderstandingMaterialization, error) {
	var result BackgroundUnderstandingMaterialization
	err := store.db.QueryRow(`
		SELECT id, run_id, artifact_kind, artifact_id, action, summary,
			source_receipt_hash, created_at
		FROM background_understanding_materializations_v1
		WHERE id = ?`,
		id,
	).Scan(
		&result.ID,
		&result.RunID,
		&result.ArtifactKind,
		&result.ArtifactID,
		&result.Action,
		&result.Summary,
		&result.SourceReceiptHash,
		&result.CreatedAt,
	)
	return result, err
}

func (store *backgroundUnderstandingStore) RunDetail(
	runID string,
) (BackgroundUnderstandingRunDetail, error) {
	run, err := store.GetRun(runID)
	if err != nil {
		return BackgroundUnderstandingRunDetail{}, err
	}
	detail := BackgroundUnderstandingRunDetail{
		Run:              run,
		Events:           []BackgroundUnderstandingRunEvent{},
		SourceItems:      []BackgroundUnderstandingSourceItem{},
		Materializations: []BackgroundUnderstandingMaterialization{},
	}
	eventRows, err := store.db.Query(`
		SELECT id, run_id, sequence, state, detail_json, detail_hash,
			occurred_at, created_at
		FROM background_understanding_run_events_v1
		WHERE run_id = ?
		ORDER BY sequence ASC`,
		runID,
	)
	if err != nil {
		return BackgroundUnderstandingRunDetail{}, err
	}
	for eventRows.Next() {
		var item BackgroundUnderstandingRunEvent
		if err := eventRows.Scan(
			&item.ID,
			&item.RunID,
			&item.Sequence,
			&item.State,
			&item.DetailJSON,
			&item.DetailHash,
			&item.OccurredAt,
			&item.CreatedAt,
		); err != nil {
			eventRows.Close()
			return BackgroundUnderstandingRunDetail{}, err
		}
		detail.Events = append(detail.Events, item)
	}
	if err := eventRows.Err(); err != nil {
		eventRows.Close()
		return BackgroundUnderstandingRunDetail{}, err
	}
	if err := eventRows.Close(); err != nil {
		return BackgroundUnderstandingRunDetail{}, err
	}

	sourceRows, err := store.db.Query(`
		SELECT id, run_id, source, source_item_kind, external_id_hash,
			thread_id_hash, content_hash, attribution_kind, observed_at,
			metadata_json, created_at
		FROM background_understanding_source_items_v1
		WHERE run_id = ?
		ORDER BY created_at ASC, id ASC`,
		runID,
	)
	if err != nil {
		return BackgroundUnderstandingRunDetail{}, err
	}
	for sourceRows.Next() {
		var item BackgroundUnderstandingSourceItem
		if err := sourceRows.Scan(
			&item.ID,
			&item.RunID,
			&item.Source,
			&item.SourceItemKind,
			&item.ExternalIDHash,
			&item.ThreadIDHash,
			&item.ContentHash,
			&item.AttributionKind,
			&item.ObservedAt,
			&item.MetadataJSON,
			&item.CreatedAt,
		); err != nil {
			sourceRows.Close()
			return BackgroundUnderstandingRunDetail{}, err
		}
		detail.SourceItems = append(detail.SourceItems, item)
	}
	if err := sourceRows.Err(); err != nil {
		sourceRows.Close()
		return BackgroundUnderstandingRunDetail{}, err
	}
	if err := sourceRows.Close(); err != nil {
		return BackgroundUnderstandingRunDetail{}, err
	}

	materializationRows, err := store.db.Query(`
		SELECT id, run_id, artifact_kind, artifact_id, action, summary,
			source_receipt_hash, created_at
		FROM background_understanding_materializations_v1
		WHERE run_id = ?
		ORDER BY created_at ASC, id ASC`,
		runID,
	)
	if err != nil {
		return BackgroundUnderstandingRunDetail{}, err
	}
	defer materializationRows.Close()
	for materializationRows.Next() {
		var item BackgroundUnderstandingMaterialization
		if err := materializationRows.Scan(
			&item.ID,
			&item.RunID,
			&item.ArtifactKind,
			&item.ArtifactID,
			&item.Action,
			&item.Summary,
			&item.SourceReceiptHash,
			&item.CreatedAt,
		); err != nil {
			return BackgroundUnderstandingRunDetail{}, err
		}
		detail.Materializations = append(detail.Materializations, item)
	}
	return detail, materializationRows.Err()
}
