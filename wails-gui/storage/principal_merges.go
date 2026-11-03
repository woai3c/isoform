package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *Store) migratePeopleMerges() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`ALTER TABLE principals ADD COLUMN merged_into TEXT NOT NULL DEFAULT ''`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
		return err
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS principal_merges (
        id TEXT PRIMARY KEY, target_id TEXT NOT NULL, merged_ids_json TEXT NOT NULL,
        receipt_json TEXT NOT NULL, created_at INTEGER NOT NULL, undone_at INTEGER NOT NULL DEFAULT 0
    )`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS principal_merge_updates (
        merge_id TEXT NOT NULL REFERENCES principal_merges(id) ON DELETE CASCADE,
        principal_id TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
        app TEXT NOT NULL, session_id TEXT NOT NULL, message_count INTEGER NOT NULL,
        first_seen_at INTEGER NOT NULL, last_seen_at INTEGER NOT NULL,
        PRIMARY KEY(merge_id,principal_id,app,session_id)
    )`); err != nil {
		return err
	}
	// Not the same: two profiles the person said are different people. The
	// merge suggester never pairs them again. Each pair is stored once, a < b.
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS principal_not_same (
        a TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
        b TEXT NOT NULL REFERENCES principals(id) ON DELETE CASCADE,
        created_at INTEGER NOT NULL,
        PRIMARY KEY(a,b)
    )`); err != nil {
		return err
	}
	return tx.Commit()
}

// MergeStateError is a stable, inspectable merge lifecycle refusal.
type MergeStateError struct {
	Code string `json:"code"`
}

func (e *MergeStateError) Error() string { return e.Code }

// Explicit table/owner allowlist: receipts never supply SQL identifiers.
var peopleMergeTables = []struct{ name, owner string }{
	{"principals", "id"}, {"principal_aliases", "principal_id"}, {"app_handles", "principal_id"},
	{"principal_sightings", "principal_id"}, {"principal_recent_messages", "principal_id"},
	{"principal_session_sightings", "principal_id"}, {"principal_contact_links", "principal_id"},
	{"principal_contact_link_updates", "principal_id"},
}

// Values retain SQLite types and int64 precision across receipt reloads.
type mergeValue struct {
	Kind string  `json:"kind"`
	Text []byte  `json:"text,omitempty"`
	Int  int64   `json:"int,omitempty"`
	Real float64 `json:"real,omitempty"`
}
type mergeStoredRow struct {
	ID     int64        `json:"id"`
	Values []mergeValue `json:"values"`
}
type mergeTableSnapshot struct {
	Columns []string         `json:"columns"`
	Rows    []mergeStoredRow `json:"rows"`
}
type peopleMergeReceipt struct {
	Version   int                  `json:"version"`
	EditedAt  int64                `json:"editedAt,omitempty"`
	Before    []mergeTableSnapshot `json:"before"`
	After     []mergeTableSnapshot `json:"after"`
	Baseline  []mergeTableSnapshot `json:"baseline"`
	AliasBase map[string]bool      `json:"aliasBase"`
}

func snapshotPeopleMerge(ctx context.Context, tx *sql.Tx, ids []string) ([]mergeTableSnapshot, error) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	var snapshots []mergeTableSnapshot
	for _, table := range peopleMergeTables {
		rows, err := tx.QueryContext(ctx, "SELECT rowid,* FROM "+table.name+" WHERE "+table.owner+" IN ("+marks+") ORDER BY rowid", args...)
		if err != nil {
			return nil, err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, err
		}
		snapshot := mergeTableSnapshot{Columns: columns[1:], Rows: []mergeStoredRow{}}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				return nil, err
			}
			row := mergeStoredRow{ID: values[0].(int64), Values: []mergeValue{}}
			for _, value := range values[1:] {
				var cell mergeValue
				switch v := value.(type) {
				case string:
					cell = mergeValue{Kind: "text"}
					if v != "" {
						cell.Text = []byte(v)
					}
				case int64:
					cell = mergeValue{Kind: "int", Int: v}
				case float64:
					cell = mergeValue{Kind: "real", Real: v}
				case nil:
					cell = mergeValue{Kind: "null"}
				default:
					rows.Close()
					return nil, fmt.Errorf("unsupported merge cell %T", value)
				}
				row.Values = append(row.Values, cell)
			}
			snapshot.Rows = append(snapshot.Rows, row)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

// MergePeople transfers the same rows as the legacy merge and hides sources.
// Each receipt owns its contribution; successive merges retain independent Undo.
func (s *Store) MergePeople(ctx context.Context, target string, sources []string) (string, PrincipalRow, error) {
	if strings.TrimSpace(target) == "" || len(sources) == 0 {
		return "", PrincipalRow{}, &MergeStateError{Code: "invalid_members"}
	}
	ids := []string{target}
	seen := map[string]bool{target: true}
	for _, id := range sources {
		if strings.TrimSpace(id) == "" || seen[id] {
			return "", PrincipalRow{}, &MergeStateError{Code: "invalid_members"}
		}
		seen[id] = true
		ids = append(ids, id)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", PrincipalRow{}, err
	}
	defer tx.Rollback()
	for i, id := range ids {
		p, err := getPrincipalTx(ctx, tx, id)
		if err != nil {
			return "", PrincipalRow{}, err
		}
		if i > 0 && p.Kind == PrincipalKindSelf {
			return "", PrincipalRow{}, &MergeStateError{Code: "self_source"}
		}
		if i > 0 {
			var linked bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM principal_contact_links WHERE principal_id=?)`, id).Scan(&linked); err != nil {
				return "", PrincipalRow{}, err
			}
			if linked {
				return "", PrincipalRow{}, errors.New("unlink the contact before merging these people")
			}
		}
	}
	before, err := snapshotPeopleMerge(ctx, tx, ids)
	if err != nil {
		return "", PrincipalRow{}, err
	}
	active, err := loadActivePeopleMerges(ctx, tx, target, "")
	if err != nil {
		return "", PrincipalRow{}, err
	}
	baseline, err := snapshotPeopleMerge(ctx, tx, []string{target})
	if err != nil {
		return "", PrincipalRow{}, err
	}
	if len(active) > 0 {
		baseline = active[0].Receipt.Baseline
	}
	aliasBase := mergeAliasBaseline(before, target, active)
	var result PrincipalRow
	for _, source := range sources {
		if err := trackMergedContactActivityTx(ctx, tx, source, target); err != nil {
			return "", PrincipalRow{}, err
		}
		result, err = mergePrincipalRowsTx(ctx, tx, source, target, true)
		if err != nil {
			return "", PrincipalRow{}, err
		}
	}
	after, err := snapshotPeopleMerge(ctx, tx, ids)
	if err != nil {
		return "", PrincipalRow{}, err
	}
	contribution := isolatePeopleMergeReceipt(target, before, after, baseline, aliasBase)
	for _, prior := range active {
		if prior.Receipt.EditedAt > contribution.EditedAt {
			contribution.EditedAt = prior.Receipt.EditedAt
		}
	}
	receipt, err := json.Marshal(contribution)
	if err != nil {
		return "", PrincipalRow{}, err
	}
	members, err := json.Marshal(sources)
	if err != nil {
		return "", PrincipalRow{}, err
	}
	id := uuid.NewString()
	if _, err := tx.ExecContext(ctx, `INSERT INTO principal_merges(id,target_id,merged_ids_json,receipt_json,created_at) VALUES(?,?,?,?,?)`, id, target, string(members), string(receipt), time.Now().UnixMilli()); err != nil {
		return "", PrincipalRow{}, err
	}
	if len(active) > 0 {
		// Inherit observations since the original target baseline. No message
		// text is retained in this journal, including empty-preview messages.
		if _, err := tx.ExecContext(ctx, `INSERT INTO principal_merge_updates
            SELECT ?,principal_id,app,session_id,message_count,first_seen_at,last_seen_at
            FROM principal_merge_updates WHERE merge_id=?`, id, active[0].ID); err != nil {
			return "", PrincipalRow{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return "", PrincipalRow{}, err
	}
	return id, result, nil
}

func mergeRowsByID(rows []mergeStoredRow) map[int64]mergeStoredRow {
	out := map[int64]mergeStoredRow{}
	for _, row := range rows {
		out[row.ID] = row
	}
	return out
}

// PeopleMergeOwner keeps person-ID signals and exact-name commitments visible
// on their surviving profile without rewriting either source ledger.
type PeopleMergeOwner struct {
	ID         string
	TargetID   string
	Name       string
	Profile    PrincipalRow
	Sessions   []PrincipalSessionSightingRow
	HasReceipt bool
}

func (s *Store) PeopleMergeOwners(ctx context.Context) ([]PeopleMergeOwner, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,merged_into,display_name,created_from FROM principals ORDER BY id`)
	if err != nil {
		return nil, err
	}
	var all []PeopleMergeOwner
	parents := map[string]string{}
	for rows.Next() {
		var row PeopleMergeOwner
		if err := rows.Scan(&row.ID, &row.TargetID, &row.Name, &row.Profile.CreatedFrom); err != nil {
			rows.Close()
			return nil, err
		}
		row.Profile.ID = row.ID
		row.Profile.DisplayName = row.Name
		parents[row.ID] = row.TargetID
		all = append(all, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Hidden child tables have moved. The source's own receipt retains its
	// original aliases and session lineage for privacy checks before folding.
	rows, err = tx.QueryContext(ctx, `SELECT target_id,merged_ids_json,receipt_json FROM principal_merges WHERE undone_at=0`)
	if err != nil {
		return nil, err
	}
	retained := map[string]PeopleMergeOwner{}
	for rows.Next() {
		var target, members, raw string
		if err := rows.Scan(&target, &members, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		receipt, err := decodePeopleMergeReceipt(raw, target)
		if err != nil {
			rows.Close()
			return nil, err
		}
		var sources []string
		if err := json.Unmarshal([]byte(members), &sources); err != nil {
			rows.Close()
			return nil, &MergeStateError{Code: "invalid_receipt"}
		}
		for _, source := range sources {
			owner := PeopleMergeOwner{HasReceipt: true}
			for _, r := range mergeOwned(receipt.Before[1], source) {
				owner.Profile.Aliases = append(owner.Profile.Aliases, mergeText(receipt.Before[1], r, "alias"))
			}
			for _, r := range mergeOwned(receipt.Before[5], source) {
				snapshot := receipt.Before[5]
				owner.Sessions = append(owner.Sessions, PrincipalSessionSightingRow{PrincipalID: source, SessionID: mergeText(snapshot, r, "session_id"), FirstSeenAt: mergeCell(snapshot, r, "first_seen_at").Int, LastSeenAt: mergeCell(snapshot, r, "last_seen_at").Int})
			}
			retained[source] = owner
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var out []PeopleMergeOwner
	for _, row := range all {
		if row.TargetID == "" {
			continue
		}
		visited := map[string]bool{row.ID: true}
		for parents[row.TargetID] != "" && !visited[row.TargetID] {
			visited[row.TargetID] = true
			row.TargetID = parents[row.TargetID]
		}
		if _, exists := parents[row.TargetID]; !exists || visited[row.TargetID] {
			continue
		}
		source := retained[row.ID]
		row.Profile.Aliases = source.Profile.Aliases
		row.Sessions = source.Sessions
		row.HasReceipt = source.HasReceipt
		out = append(out, row)
	}
	return out, tx.Commit()
}
