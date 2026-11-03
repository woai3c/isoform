package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"
)

type activePeopleMerge struct {
	ID      string
	Sources []string
	Receipt peopleMergeReceipt
}

func loadActivePeopleMerges(ctx context.Context, tx *sql.Tx, target, except string) ([]activePeopleMerge, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,merged_ids_json,receipt_json FROM principal_merges WHERE target_id=? AND undone_at=0 AND id!=? ORDER BY created_at,rowid`, target, except)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []activePeopleMerge
	for rows.Next() {
		var m activePeopleMerge
		var members, raw string
		if err := rows.Scan(&m.ID, &members, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(members), &m.Sources); err != nil {
			return nil, err
		}
		receipt, err := decodePeopleMergeReceipt(raw, target)
		if err != nil {
			return nil, err
		}
		m.Receipt = receipt
		out = append(out, m)
	}
	return out, rows.Err()
}

func mergeCell(s mergeTableSnapshot, r mergeStoredRow, column string) mergeValue {
	for i, c := range s.Columns {
		if c == column && i < len(r.Values) {
			return r.Values[i]
		}
	}
	return mergeValue{}
}
func mergeText(s mergeTableSnapshot, r mergeStoredRow, column string) string {
	return string(mergeCell(s, r, column).Text)
}
func mergeSet(s mergeTableSnapshot, r *mergeStoredRow, column string, value mergeValue) {
	for i, c := range s.Columns {
		if c == column {
			r.Values[i] = value
			return
		}
	}
}
func mergeOwned(s mergeTableSnapshot, owner string) []mergeStoredRow {
	var rows []mergeStoredRow
	for _, r := range s.Rows {
		if mergeText(s, r, "principal_id") == owner || mergeText(s, r, "id") == owner {
			rows = append(rows, r)
		}
	}
	return rows
}
func mergeAliasKey(alias string) string { return base64.StdEncoding.EncodeToString([]byte(alias)) }
func mergeClaims(m activePeopleMerge, alias string) bool {
	for _, source := range m.Sources {
		for _, r := range mergeOwned(m.Receipt.Before[1], source) {
			if mergeText(m.Receipt.Before[1], r, "alias") == alias {
				return true
			}
		}
	}
	return false
}
func mergeAliasBaseline(before []mergeTableSnapshot, target string, active []activePeopleMerge) map[string]bool {
	out := map[string]bool{}
	existing := map[string]bool{}
	for _, r := range mergeOwned(before[1], target) {
		existing[mergeText(before[1], r, "alias")] = true
	}
	for _, r := range before[1].Rows {
		if mergeText(before[1], r, "principal_id") == target {
			continue
		}
		alias := mergeText(before[1], r, "alias")
		base := existing[alias]
		for _, m := range active {
			if mergeClaims(m, alias) {
				base = m.Receipt.AliasBase[mergeAliasKey(alias)]
				break
			}
		}
		out[mergeAliasKey(alias)] = base
	}
	return out
}

func mergeSQLValue(v mergeValue) (any, error) {
	switch v.Kind {
	case "text":
		return string(v.Text), nil
	case "int":
		return v.Int, nil
	case "real":
		return v.Real, nil
	case "null":
		return nil, nil
	default:
		return nil, &MergeStateError{Code: "invalid_receipt"}
	}
}

// These tables have logical composite keys; rowid is retained when available
// for exact restoration, but a later observation may have reused a freed rowid.
func restoreMergeRow(ctx context.Context, tx *sql.Tx, index int, s mergeTableSnapshot, r mergeStoredRow, insert bool) error {
	values := make([]any, 0, len(r.Values)+1)
	if len(r.Values) != len(s.Columns) {
		return &MergeStateError{Code: "invalid_receipt"}
	}
	for _, v := range r.Values {
		value, err := mergeSQLValue(v)
		if err != nil {
			return err
		}
		values = append(values, value)
	}
	table := peopleMergeTables[index].name
	if !insert {
		assignments := make([]string, len(s.Columns))
		for i, c := range s.Columns {
			assignments[i] = c + "=?"
		}
		values = append(values, r.ID)
		_, err := tx.ExecContext(ctx, "UPDATE "+table+" SET "+strings.Join(assignments, ",")+" WHERE rowid=?", values...)
		return err
	}
	// Child table rowids are not public identifiers. Move a reused rowid out
	// of the receipt's range without deleting its later observation.
	if _, err := tx.ExecContext(ctx, "UPDATE "+table+" SET rowid=(SELECT COALESCE(MAX(rowid),0)+1 FROM "+table+") WHERE rowid=?", r.ID); err != nil {
		return err
	}
	values = append([]any{r.ID}, values...)
	_, err := tx.ExecContext(ctx, "INSERT INTO "+table+"(rowid,"+strings.Join(s.Columns, ",")+") VALUES("+strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")+")", values...)
	return err
}

type mergeObservation struct {
	App, Session       string
	Count, First, Last int64
}

func mergeObservations(ctx context.Context, tx *sql.Tx, id string) ([]mergeObservation, error) {
	rows, err := tx.QueryContext(ctx, `SELECT app,session_id,message_count,first_seen_at,last_seen_at FROM principal_merge_updates WHERE merge_id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []mergeObservation
	for rows.Next() {
		var r mergeObservation
		if err := rows.Scan(&r.App, &r.Session, &r.Count, &r.First, &r.Last); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func mergeAggregateKey(index int, s mergeTableSnapshot, r mergeStoredRow) string {
	switch index {
	case 3:
		return mergeText(s, r, "app")
	case 5:
		return mergeText(s, r, "session_id")
	case 7:
		return mergeText(s, r, "app") + "\x00" + mergeText(s, r, "session_id")
	default:
		return ""
	}
}
func mergeKeyRow(index int, s mergeTableSnapshot, rows []mergeStoredRow, key string) (mergeStoredRow, bool) {
	for _, r := range rows {
		if mergeAggregateKey(index, s, r) == key {
			return r, true
		}
	}
	return mergeStoredRow{}, false
}

func undoMergeAggregate(ctx context.Context, tx *sql.Tx, index int, target string, receipt peopleMergeReceipt, active []activePeopleMerge, updates []mergeObservation, current mergeTableSnapshot) error {
	before, after, base := receipt.Before[index], receipt.After[index], receipt.Baseline[index]
	beforeTarget, afterTarget := mergeOwned(before, target), mergeOwned(after, target)
	keys := map[string]bool{}
	for _, r := range afterTarget {
		key := mergeAggregateKey(index, after, r)
		old, ok := mergeKeyRow(index, before, beforeTarget, key)
		contributed := false
		for _, sourceRow := range before.Rows {
			if mergeText(before, sourceRow, "principal_id") != target && mergeAggregateKey(index, before, sourceRow) == key {
				contributed = true
			}
		}
		if index == 7 {
			for _, sourceRow := range receipt.Before[3].Rows {
				if mergeText(receipt.Before[3], sourceRow, "principal_id") != target && mergeText(receipt.Before[3], sourceRow, "app")+"\x00" == key {
					contributed = true
				}
			}
			for _, sourceRow := range receipt.Before[5].Rows {
				if mergeText(receipt.Before[5], sourceRow, "principal_id") != target && "\x00"+mergeText(receipt.Before[5], sourceRow, "session_id") == key {
					contributed = true
				}
			}
		}
		if contributed || !ok || !reflect.DeepEqual(old.Values, r.Values) {
			keys[key] = true
		}
	}
	for key := range keys {
		live, ok := mergeKeyRow(index, current, mergeOwned(current, target), key)
		if !ok {
			return &MergeStateError{Code: "merge_conflict"}
		}
		original, hasOriginal := mergeKeyRow(index, base, mergeOwned(base, target), key)
		var count, first, last int64
		present := false
		add := func(n, lo, hi int64) {
			count += n
			if !present || lo < first {
				first = lo
			}
			if !present || hi > last {
				last = hi
			}
			present = true
		}
		if hasOriginal {
			add(mergeCell(base, original, "message_count").Int, mergeCell(base, original, "first_seen_at").Int, mergeCell(base, original, "last_seen_at").Int)
		}
		for _, m := range active {
			for _, source := range m.Sources {
				switch index {
				case 3, 5:
					snap := m.Receipt.Before[index]
					if r, ok := mergeKeyRow(index, snap, mergeOwned(snap, source), key); ok {
						add(mergeCell(snap, r, "message_count").Int, mergeCell(snap, r, "first_seen_at").Int, mergeCell(snap, r, "last_seen_at").Int)
					}
				case 7:
					for _, r := range mergeOwned(m.Receipt.Before[3], source) {
						s := m.Receipt.Before[3]
						if mergeText(s, r, "app")+"\x00" == key {
							add(mergeCell(s, r, "message_count").Int, mergeCell(s, r, "first_seen_at").Int, mergeCell(s, r, "last_seen_at").Int)
						}
					}
					for _, r := range mergeOwned(m.Receipt.Before[5], source) {
						s := m.Receipt.Before[5]
						if "\x00"+mergeText(s, r, "session_id") == key {
							add(0, mergeCell(s, r, "first_seen_at").Int, mergeCell(s, r, "last_seen_at").Int)
						}
					}
				}
			}
		}
		for _, u := range updates {
			match := false
			switch index {
			case 3:
				match = u.App == key
			case 5:
				match = u.Session != "" && u.Session == key
			case 7:
				match = u.App+"\x00"+u.Session == key
			}
			if match {
				add(u.Count, u.First, u.Last)
			}
		}
		if index != 5 {
			old, _ := mergeKeyRow(index, before, beforeTarget, key)
			post, _ := mergeKeyRow(index, after, afterTarget, key)
			expected := mergeCell(current, live, "message_count").Int - (mergeCell(after, post, "message_count").Int - mergeCell(before, old, "message_count").Int)
			if expected != count {
				return &MergeStateError{Code: "merge_conflict"}
			}
		}
		table := peopleMergeTables[index].name
		if !present {
			if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE rowid=?", live.ID); err != nil {
				return err
			}
			continue
		}
		mergeSet(current, &live, "message_count", mergeValue{Kind: "int", Int: count})
		mergeSet(current, &live, "first_seen_at", mergeValue{Kind: "int", Int: first})
		mergeSet(current, &live, "last_seen_at", mergeValue{Kind: "int", Int: last})
		if err := restoreMergeRow(ctx, tx, index, current, live, false); err != nil {
			return err
		}
	}
	return nil
}

// UndoMerge restores the source contribution and preserves later observations
// and profile edits. Undo a containing merge first when its target is hidden.
func (s *Store) UndoMerge(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var target, members, raw string
	var undone int64
	err = tx.QueryRowContext(ctx, `SELECT target_id,merged_ids_json,receipt_json,undone_at FROM principal_merges WHERE id=?`, id).Scan(&target, &members, &raw, &undone)
	if errors.Is(err, sql.ErrNoRows) {
		return &MergeStateError{Code: "merge_not_found"}
	}
	if err != nil {
		return err
	}
	if undone != 0 {
		return &MergeStateError{Code: "already_undone"}
	}
	if _, err := getPrincipalTx(ctx, tx, target); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &MergeStateError{Code: "merge_dependency"}
		}
		return err
	}
	var sources []string
	var receipt peopleMergeReceipt
	if err := json.Unmarshal([]byte(members), &sources); err != nil {
		return err
	}
	receipt, err = decodePeopleMergeReceipt(raw, target)
	if err != nil {
		return err
	}
	current, err := snapshotPeopleMerge(ctx, tx, append([]string{target}, sources...))
	if err != nil {
		return err
	}
	for i := range peopleMergeTables {
		if !reflect.DeepEqual(receipt.Before[i].Columns, current[i].Columns) || !reflect.DeepEqual(receipt.After[i].Columns, current[i].Columns) || !reflect.DeepEqual(receipt.Baseline[i].Columns, current[i].Columns) {
			return &MergeStateError{Code: "invalid_receipt"}
		}
	}
	active, err := loadActivePeopleMerges(ctx, tx, target, id)
	if err != nil {
		return err
	}
	updates, err := mergeObservations(ctx, tx, id)
	if err != nil {
		return err
	}
	for _, source := range sources {
		var owner string
		if err := tx.QueryRowContext(ctx, `SELECT merged_into FROM principals WHERE id=?`, source).Scan(&owner); err != nil {
			return err
		}
		if owner != target {
			return &MergeStateError{Code: "merge_conflict"}
		}
	}
	// Only merged_into and the merge's timestamp changed on principals.
	for _, r := range current[0].Rows {
		owner := mergeText(current[0], r, "id")
		if owner != target {
			if _, err := tx.ExecContext(ctx, `UPDATE principals SET merged_into='' WHERE id=?`, owner); err != nil {
				return err
			}
			continue
		}
		post := mergeOwned(receipt.After[0], target)[0]
		original := mergeOwned(receipt.Baseline[0], target)[0]
		same := true
		for j, c := range current[0].Columns {
			if c != "updated_at" && !reflect.DeepEqual(r.Values[j], post.Values[j]) {
				same = false
			}
		}
		if same && reflect.DeepEqual(mergeCell(current[0], r, "updated_at"), mergeCell(receipt.After[0], post, "updated_at")) {
			stamp := mergeCell(receipt.Baseline[0], original, "updated_at").Int
			if receipt.EditedAt != 0 {
				stamp = receipt.EditedAt
			}
			for _, other := range active {
				post := mergeOwned(other.Receipt.After[0], target)[0]
				if at := mergeCell(other.Receipt.After[0], post, "updated_at").Int; at > stamp {
					stamp = at
				}
				if other.Receipt.EditedAt > stamp {
					stamp = other.Receipt.EditedAt
				}
			}
			if _, err := tx.ExecContext(ctx, `UPDATE principals SET updated_at=? WHERE id=?`, stamp, target); err != nil {
				return err
			}
		}
	}
	// Handles keep their stable IDs and contact provenance. Never delete them.
	for _, source := range sources {
		for _, r := range mergeOwned(receipt.Before[2], source) {
			result, err := tx.ExecContext(ctx, `UPDATE app_handles SET principal_id=? WHERE id=? AND principal_id=?`, source, mergeCell(receipt.Before[2], r, "id").Int, target)
			if err != nil {
				return err
			}
			if n, _ := result.RowsAffected(); n != 1 {
				return &MergeStateError{Code: "merge_conflict"}
			}
		}
	}
	for _, index := range []int{3, 5, 7} {
		if err := undoMergeAggregate(ctx, tx, index, target, receipt, active, updates, current[index]); err != nil {
			return err
		}
	}
	// Remove aliases introduced solely by this merge. Shared aliases remain
	// until the final active receipt claiming them is undone.
	aliases := map[string]bool{}
	for _, source := range sources {
		for _, r := range mergeOwned(receipt.Before[1], source) {
			aliases[mergeText(receipt.Before[1], r, "alias")] = true
		}
	}
	for alias := range aliases {
		keep := receipt.AliasBase[mergeAliasKey(alias)]
		for _, m := range active {
			keep = keep || mergeClaims(m, alias)
		}
		if !keep {
			if _, err := tx.ExecContext(ctx, `DELETE FROM principal_aliases WHERE principal_id=? AND alias=?`, target, alias); err != nil {
				return err
			}
		}
	}
	// Receipt-created previews may have aged out. New previews retain their
	// ownership even when the rolling message window changed in the meantime.
	beforeMessages := mergeRowsByID(receipt.Before[4].Rows)
	for _, r := range mergeOwned(receipt.After[4], target) {
		if _, exists := beforeMessages[r.ID]; exists {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM principal_recent_messages WHERE principal_id=? AND seen_at=? AND app=? AND preview=?`, target, mergeCell(receipt.After[4], r, "seen_at").Int, mergeText(receipt.After[4], r, "app"), mergeText(receipt.After[4], r, "preview")); err != nil {
			return err
		}
	}
	for _, index := range []int{1, 3, 4, 5} {
		for _, source := range sources {
			for _, r := range mergeOwned(receipt.Before[index], source) {
				if err := restoreMergeRow(ctx, tx, index, receipt.Before[index], r, true); err != nil {
					return err
				}
			}
		}
	}
	if err := undoMergedContactAliases(ctx, tx, target, receipt, active); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM principal_merge_updates WHERE merge_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE principal_merges SET undone_at=?,receipt_json='{}' WHERE id=?`, time.Now().UnixMilli(), id); err != nil {
		return err
	}
	return tx.Commit()
}

func undoMergedContactAliases(ctx context.Context, tx *sql.Tx, target string, receipt peopleMergeReceipt, active []activePeopleMerge) error {
	baseline := mergeOwned(receipt.Baseline[6], target)
	if len(baseline) == 0 {
		return nil
	}
	original := mergeText(receipt.Baseline[6], baseline[0], "receipt")
	var before, live contactLinkReceipt
	if err := json.Unmarshal([]byte(original), &before); err != nil {
		return err
	}
	var raw string
	if err := tx.QueryRowContext(ctx, `SELECT receipt FROM principal_contact_links WHERE principal_id=?`, target).Scan(&raw); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(raw), &live); err != nil {
		return err
	}
	live.AddedAliases = nil
	for _, alias := range before.AddedAliases {
		claimed := false
		for _, m := range active {
			claimed = claimed || mergeClaims(m, alias)
		}
		if !claimed {
			live.AddedAliases = append(live.AddedAliases, alias)
		}
	}
	updated, err := json.Marshal(live)
	if err != nil {
		return err
	}
	if reflect.DeepEqual(live, before) {
		updated = []byte(original)
	}
	_, err = tx.ExecContext(ctx, `UPDATE principal_contact_links SET receipt=? WHERE principal_id=?`, string(updated), target)
	return err
}

// The editor resends every alias. Only newly added aliases acquire ownership
// from a later save; resending an existing merged alias does not adopt it.
func preserveEditedMergeAliases(ctx context.Context, tx *sql.Tx, target string, previous, aliases []string, editedAt int64) error {
	existing := map[string]bool{}
	for _, alias := range previous {
		existing[alias] = true
	}
	var added []string
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias != "" && !existing[alias] {
			added = append(added, alias)
		}
	}
	active, err := loadActivePeopleMerges(ctx, tx, target, "")
	if err != nil {
		return err
	}
	for _, m := range active {
		m.Receipt.EditedAt = editedAt
		if m.Receipt.AliasBase == nil {
			m.Receipt.AliasBase = map[string]bool{}
		}
		for _, alias := range added {
			m.Receipt.AliasBase[mergeAliasKey(alias)] = true
		}
		raw, err := json.Marshal(m.Receipt)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE principal_merges SET receipt_json=? WHERE id=?`, string(raw), m.ID); err != nil {
			return err
		}
	}
	return nil
}

func validPeopleMergeReceipt(r peopleMergeReceipt, target string) bool {
	if r.Version != peopleMergeReceiptVersion {
		return false
	}
	for _, snapshots := range [][]mergeTableSnapshot{r.Before, r.After, r.Baseline} {
		if len(snapshots) != len(peopleMergeTables) || len(mergeOwned(snapshots[0], target)) != 1 {
			return false
		}
		for _, snapshot := range snapshots {
			for _, row := range snapshot.Rows {
				if len(row.Values) != len(snapshot.Columns) {
					return false
				}
				for _, v := range row.Values {
					if _, err := mergeSQLValue(v); err != nil {
						return false
					}
				}
			}
		}
	}
	return true
}
