package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"yansu-agent/agent/authority"
)

const capabilityManifestPolicyVersion = "capability-manifest-v1"

type CapabilityManifestEffect struct {
	Capability string           `json:"capability"`
	Effect     authority.Effect `json:"effect"`
	Resource   string           `json:"resource,omitempty"`
	Recipient  string           `json:"recipient,omitempty"`
	Reversible bool             `json:"reversible"`
}

type CapabilityManifest struct {
	ID              string                     `json:"id"`
	InitiativeID    string                     `json:"initiativeId"`
	QualificationID string                     `json:"qualificationId"`
	PolicyVersion   string                     `json:"policyVersion"`
	PlanHash        string                     `json:"planHash"`
	RiskTier        string                     `json:"riskTier"`
	Grantable       bool                       `json:"grantable"`
	GrantMode       string                     `json:"grantMode"`
	BlockReasons    []string                   `json:"blockReasons"`
	Effects         []CapabilityManifestEffect `json:"effects"`
	CreatedAt       int64                      `json:"createdAt"`
}

type AuthorityGrantRecord struct {
	ID             string           `json:"id"`
	InitiativeID   string           `json:"initiativeId"`
	ManifestID     string           `json:"manifestId"`
	Capability     string           `json:"capability"`
	ResourcePrefix string           `json:"resourcePrefix,omitempty"`
	Effect         authority.Effect `json:"effect"`
	ExpiresAt      int64            `json:"expiresAt"`
	CreatedAt      int64            `json:"createdAt"`
	IssuedAt       int64            `json:"issuedAt,omitempty"`
	RevokedAt      int64            `json:"revokedAt,omitempty"`
	RevocationNote string           `json:"revocationNote,omitempty"`
	Status         string           `json:"status"`
}

func deterministicAuthorityID(kind, key string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(kind+"\x00"+key)).String()
}

func compileCapabilityManifest(
	qualification ShadowCapabilityQualification,
	initiative Initiative,
) (CapabilityManifest, error) {
	if strings.TrimSpace(qualification.ID) == "" ||
		strings.TrimSpace(qualification.CandidatePlanHash) == "" {
		return CapabilityManifest{}, errors.New("qualified shadow evidence is incomplete")
	}
	if initiative.ID == "" || initiative.ExecutionPlan.Automation == nil {
		return CapabilityManifest{}, errors.New("initiative automation plan is incomplete")
	}
	expectedFingerprint := qualifiedInitiativeFingerprint(
		qualification.CapabilityKey,
		qualification.CandidatePlanHash,
	)
	if initiative.Fingerprint != expectedFingerprint {
		return CapabilityManifest{}, errors.New("initiative plan no longer matches qualified shadow evidence")
	}

	effects := make([]CapabilityManifestEffect, 0, len(qualification.Effects))
	blockReasons := make([]string, 0)
	seen := make(map[string]struct{})
	for _, source := range qualification.Effects {
		effect := CapabilityManifestEffect{
			Capability: strings.TrimSpace(source.Capability),
			Effect:     source.Effect,
			Resource:   strings.TrimSpace(source.Resource),
			Recipient:  strings.TrimSpace(source.Recipient),
			Reversible: source.Reversible,
		}
		key := strings.Join([]string{
			effect.Capability,
			string(effect.Effect),
			effect.Resource,
			effect.Recipient,
			fmt.Sprint(effect.Reversible),
		}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if isShellCapability(effect.Capability) {
			// A shell command is never granted from shadow evidence: a
			// grant would cover every command the shell can run.
			blockReasons = append(blockReasons, "shell_command_requires_per_run_approval")
		}
		switch effect.Effect {
		case authority.EffectRead, authority.EffectLocalWrite:
		case authority.EffectExternalWrite:
			if effect.Resource == "" {
				blockReasons = append(blockReasons, "external_write_missing_resource_scope")
			}
			if !effect.Reversible {
				blockReasons = append(blockReasons, "irreversible_external_write_requires_per_run_approval")
			}
		case authority.EffectPrivileged:
			blockReasons = append(blockReasons, "privileged_effect_requires_per_run_approval")
		case authority.EffectUnknown:
			blockReasons = append(blockReasons, "unknown_effect_cannot_receive_authority")
		default:
			blockReasons = append(blockReasons, "unrecognized_effect_cannot_receive_authority")
		}
		if effect.Capability == "" || effect.Capability == "tool.unknown" {
			blockReasons = append(blockReasons, "unknown_capability_cannot_receive_authority")
		}
		effects = append(effects, effect)
	}
	if len(effects) == 0 {
		blockReasons = append(blockReasons, "manifest_has_no_observed_runtime_effects")
	}
	sort.Slice(effects, func(i, j int) bool {
		left := strings.Join([]string{effects[i].Capability, string(effects[i].Effect), effects[i].Resource}, "\x00")
		right := strings.Join([]string{effects[j].Capability, string(effects[j].Effect), effects[j].Resource}, "\x00")
		return left < right
	})
	blockReasons = uniqueSortedStrings(blockReasons)
	grantable := len(blockReasons) == 0
	grantMode := "scoped_expiring"
	if !grantable {
		grantMode = "per_run_approval"
	}
	return CapabilityManifest{
		ID:              deterministicAuthorityID("manifest", qualification.ID+"\x00"+initiative.ID),
		InitiativeID:    initiative.ID,
		QualificationID: qualification.ID,
		PolicyVersion:   capabilityManifestPolicyVersion,
		PlanHash:        qualification.CandidatePlanHash,
		RiskTier:        qualification.RiskTier,
		Grantable:       grantable,
		GrantMode:       grantMode,
		BlockReasons:    blockReasons,
		Effects:         effects,
		CreatedAt:       qualification.CreatedAt,
	}, nil
}

func scanCapabilityManifest(scanner interface{ Scan(dest ...any) error }) (CapabilityManifest, error) {
	var id, initiativeID, qualificationID, policyVersion, planHash, raw string
	var createdAt int64
	if err := scanner.Scan(
		&id,
		&initiativeID,
		&qualificationID,
		&policyVersion,
		&planHash,
		&raw,
		&createdAt,
	); err != nil {
		return CapabilityManifest{}, err
	}
	var item CapabilityManifest
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return CapabilityManifest{}, fmt.Errorf("decode capability manifest %s: %w", id, err)
	}
	item.ID = id
	item.InitiativeID = initiativeID
	item.QualificationID = qualificationID
	item.PolicyVersion = policyVersion
	item.PlanHash = planHash
	item.CreatedAt = createdAt
	return item, nil
}

const capabilityManifestSelectColumns = `
	id, initiative_id, qualification_id, policy_version, plan_hash, manifest_json, created_at`

func (s *ActivityStore) SaveCapabilityManifest(item CapabilityManifest) (CapabilityManifest, bool, error) {
	if s == nil || s.db == nil {
		return CapabilityManifest{}, false, errors.New("activity store is unavailable")
	}
	payload, err := json.Marshal(item)
	if err != nil {
		return CapabilityManifest{}, false, fmt.Errorf("encode capability manifest: %w", err)
	}
	result, err := s.db.Exec(`
		INSERT OR IGNORE INTO capability_manifests (
			id, initiative_id, qualification_id, policy_version, plan_hash,
			manifest_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		item.ID,
		item.InitiativeID,
		item.QualificationID,
		item.PolicyVersion,
		item.PlanHash,
		string(payload),
		item.CreatedAt,
	)
	if err != nil {
		return CapabilityManifest{}, false, fmt.Errorf("save capability manifest: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return CapabilityManifest{}, false, err
	}
	persisted, err := s.GetCapabilityManifest(item.InitiativeID)
	return persisted, affected > 0, err
}

func (s *ActivityStore) GetCapabilityManifest(initiativeID string) (CapabilityManifest, error) {
	return scanCapabilityManifest(s.db.QueryRow(`
		SELECT `+capabilityManifestSelectColumns+`
		FROM capability_manifests
		WHERE initiative_id = ?
		ORDER BY created_at DESC LIMIT 1`,
		strings.TrimSpace(initiativeID),
	))
}

func (s *ActivityStore) GetShadowQualificationByProposal(
	initiativeID string,
) (ShadowCapabilityQualification, error) {
	return scanShadowCapabilityQualification(s.db.QueryRow(`
		SELECT id, qualification_json, created_at
		FROM shadow_capability_qualifications
		WHERE proposal_initiative_id = ?
		ORDER BY created_at DESC LIMIT 1`,
		strings.TrimSpace(initiativeID),
	))
}

func (s *ActivityStore) EnsureCapabilityManifest(
	qualification ShadowCapabilityQualification,
	initiative Initiative,
) (CapabilityManifest, error) {
	existing, err := s.GetCapabilityManifest(initiative.ID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CapabilityManifest{}, err
	}
	manifest, err := compileCapabilityManifest(qualification, initiative)
	if err != nil {
		return CapabilityManifest{}, err
	}
	persisted, _, err := s.SaveCapabilityManifest(manifest)
	return persisted, err
}

func grantLifetimeForRisk(riskTier string) time.Duration {
	switch riskTier {
	case "external_write":
		return 7 * 24 * time.Hour
	case "local_write":
		return 30 * 24 * time.Hour
	default:
		return 24 * time.Hour
	}
}

func (s *ActivityStore) IssueAuthorityGrants(
	manifest CapabilityManifest,
	now time.Time,
) ([]AuthorityGrantRecord, error) {
	if !manifest.Grantable {
		return nil, fmt.Errorf(
			"capability manifest requires per-run approval: %s",
			strings.Join(manifest.BlockReasons, ", "),
		)
	}
	if now.IsZero() {
		now = time.Now()
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin authority grant issuance: %w", err)
	}
	defer tx.Rollback()

	expiresAt := now.Add(grantLifetimeForRisk(manifest.RiskTier)).UnixMilli()
	createdAt := now.UnixMilli()
	for _, effect := range manifest.Effects {
		if effect.Effect == authority.EffectRead || isShellCapability(effect.Capability) {
			continue
		}
		grantKey := strings.Join([]string{
			manifest.ID,
			effect.Capability,
			effect.Resource,
		}, "\x00")
		grantID := deterministicAuthorityID("grant", grantKey)
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO authority_grants (
				id, initiative_id, manifest_id, capability, resource_prefix,
				effect, expires_at, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			grantID,
			manifest.InitiativeID,
			manifest.ID,
			effect.Capability,
			effect.Resource,
			string(effect.Effect),
			expiresAt,
			createdAt,
		); err != nil {
			return nil, fmt.Errorf("save authority grant: %w", err)
		}
		if _, err := tx.Exec(`
			INSERT OR IGNORE INTO authority_grant_events (
				id, grant_id, event_type, reason, created_at
			) VALUES (?, ?, 'issued', 'user approved qualified initiative', ?)`,
			deterministicAuthorityID("grant-event", grantID+"\x00issued"),
			grantID,
			createdAt,
		); err != nil {
			return nil, fmt.Errorf("save authority grant issuance: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit authority grant issuance: %w", err)
	}
	return s.ListAuthorityGrants(manifest.InitiativeID)
}

func scanAuthorityGrant(scanner interface{ Scan(dest ...any) error }, now int64) (AuthorityGrantRecord, error) {
	var item AuthorityGrantRecord
	var effect string
	var issuedAt, revokedAt sql.NullInt64
	var revocationNote sql.NullString
	if err := scanner.Scan(
		&item.ID,
		&item.InitiativeID,
		&item.ManifestID,
		&item.Capability,
		&item.ResourcePrefix,
		&effect,
		&item.ExpiresAt,
		&item.CreatedAt,
		&issuedAt,
		&revokedAt,
		&revocationNote,
	); err != nil {
		return AuthorityGrantRecord{}, err
	}
	item.Effect = authority.Effect(effect)
	if issuedAt.Valid {
		item.IssuedAt = issuedAt.Int64
	}
	if revokedAt.Valid {
		item.RevokedAt = revokedAt.Int64
	}
	if revocationNote.Valid {
		item.RevocationNote = revocationNote.String
	}
	switch {
	case item.RevokedAt > 0:
		item.Status = "revoked"
	case item.IssuedAt == 0:
		item.Status = "pending"
	case item.ExpiresAt <= now:
		item.Status = "expired"
	default:
		item.Status = "active"
	}
	return item, nil
}

const authorityGrantSelectColumns = `
	g.id, g.initiative_id, g.manifest_id, g.capability, g.resource_prefix,
	g.effect, g.expires_at, g.created_at,
	(SELECT MIN(e.created_at) FROM authority_grant_events e
		WHERE e.grant_id = g.id AND e.event_type = 'issued'),
	(SELECT MIN(e.created_at) FROM authority_grant_events e
		WHERE e.grant_id = g.id AND e.event_type = 'revoked'),
	(SELECT e.reason FROM authority_grant_events e
		WHERE e.grant_id = g.id AND e.event_type = 'revoked'
		ORDER BY e.created_at DESC LIMIT 1)`

func (s *ActivityStore) ListAuthorityGrants(initiativeID string) ([]AuthorityGrantRecord, error) {
	now := time.Now().UnixMilli()
	rows, err := s.db.Query(`
		SELECT `+authorityGrantSelectColumns+`
		FROM authority_grants g
		WHERE g.initiative_id = ?
		ORDER BY g.created_at ASC, g.id ASC`,
		strings.TrimSpace(initiativeID),
	)
	if err != nil {
		return nil, fmt.Errorf("list authority grants: %w", err)
	}
	defer rows.Close()
	out := make([]AuthorityGrantRecord, 0)
	for rows.Next() {
		item, err := scanAuthorityGrant(rows, now)
		if err != nil {
			return nil, fmt.Errorf("scan authority grant: %w", err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *ActivityStore) ListActiveAuthorityGrants(
	initiativeID string,
	now time.Time,
) ([]authority.Grant, error) {
	if now.IsZero() {
		now = time.Now()
	}
	rows, err := s.db.Query(`
		SELECT g.id, g.capability, g.resource_prefix, g.expires_at
		FROM authority_grants g
		JOIN initiatives i ON i.id = g.initiative_id
		WHERE g.initiative_id = ?
			AND i.status IN ('approved', 'executing')
			AND g.expires_at > ?
			AND EXISTS (
				SELECT 1 FROM authority_grant_events issued
				WHERE issued.grant_id = g.id AND issued.event_type = 'issued'
			)
			AND NOT EXISTS (
				SELECT 1 FROM authority_grant_events revoked
				WHERE revoked.grant_id = g.id AND revoked.event_type = 'revoked'
			)
		ORDER BY g.created_at ASC, g.id ASC`,
		strings.TrimSpace(initiativeID),
		now.UnixMilli(),
	)
	if err != nil {
		return nil, fmt.Errorf("list active authority grants: %w", err)
	}
	defer rows.Close()
	out := make([]authority.Grant, 0)
	for rows.Next() {
		var grant authority.Grant
		var expiresAt int64
		if err := rows.Scan(&grant.ID, &grant.Capability, &grant.ResourcePrefix, &expiresAt); err != nil {
			return nil, err
		}
		// Shell grants issued before shell commands were barred from grants
		// are never honoured.
		if isShellCapability(grant.Capability) {
			continue
		}
		grant.ExpiresAt = time.UnixMilli(expiresAt)
		out = append(out, grant)
	}
	return out, rows.Err()
}

// isShellCapability reports a Bash capability (local.shell.execute and the
// like). No grant may cover one.
func isShellCapability(capability string) bool {
	capability = strings.TrimSpace(capability)
	return capability == "local.shell" || strings.HasPrefix(capability, "local.shell.") ||
		capability == "local.*" || capability == "*"
}

func (s *ActivityStore) RevokeAuthorityGrant(grantID, reason string) (AuthorityGrantRecord, error) {
	grantID = strings.TrimSpace(grantID)
	if grantID == "" {
		return AuthorityGrantRecord{}, errors.New("authority grant id is required")
	}
	_, err := s.db.Exec(`
		INSERT OR IGNORE INTO authority_grant_events (
			id, grant_id, event_type, reason, created_at
		) VALUES (?, ?, 'revoked', ?, ?)`,
		deterministicAuthorityID("grant-event", grantID+"\x00revoked"),
		grantID,
		strings.TrimSpace(reason),
		time.Now().UnixMilli(),
	)
	if err != nil {
		return AuthorityGrantRecord{}, fmt.Errorf("revoke authority grant: %w", err)
	}
	return scanAuthorityGrant(s.db.QueryRow(`
		SELECT `+authorityGrantSelectColumns+`
		FROM authority_grants g WHERE g.id = ?`,
		grantID,
	), time.Now().UnixMilli())
}
