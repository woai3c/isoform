package storage

import (
	"context"
	"database/sql"
	"encoding/json"
)

type contactActivitySnapshot struct {
	Sightings []PrincipalSightingRow
	Recent    []PrincipalRecentMessageRow
	Sessions  []PrincipalSessionSightingRow
}

func snapshotContactActivity(ctx context.Context, tx *sql.Tx, id string) (contactActivitySnapshot, error) {
	var out contactActivitySnapshot
	rows, err := tx.QueryContext(ctx, `SELECT app,message_count,first_seen_at,last_seen_at FROM principal_sightings WHERE principal_id=?`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var s PrincipalSightingRow
		if err := rows.Scan(&s.App, &s.MessageCount, &s.FirstSeenAt, &s.LastSeenAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Sightings = append(out.Sightings, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT app,seen_at,preview FROM principal_recent_messages WHERE principal_id=?`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var s PrincipalRecentMessageRow
		if err := rows.Scan(&s.App, &s.SeenAt, &s.Preview); err != nil {
			rows.Close()
			return out, err
		}
		out.Recent = append(out.Recent, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	rows, err = tx.QueryContext(ctx, `SELECT session_id,first_seen_at,last_seen_at FROM principal_session_sightings WHERE principal_id=?`, id)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var s PrincipalSessionSightingRow
		if err := rows.Scan(&s.SessionID, &s.FirstSeenAt, &s.LastSeenAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Sessions = append(out.Sessions, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	return out, nil
}

func restoreContactActivity(ctx context.Context, tx *sql.Tx, dst string, r contactLinkReceipt) error {
	// Restore the destination baseline and add only observations made after
	// linking. The original contact's pre-link counts and sessions go back to it.
	if _, err := tx.ExecContext(ctx, `DELETE FROM principal_sightings WHERE principal_id=?; DELETE FROM principal_session_sightings WHERE principal_id=?`, dst, dst); err != nil {
		return err
	}
	for id, activity := range map[string]contactActivitySnapshot{dst: r.DestinationActivity, r.Source.ID: r.SourceActivity} {
		for _, s := range activity.Sightings {
			if _, err := tx.ExecContext(ctx, `INSERT INTO principal_sightings(principal_id,app,message_count,first_seen_at,last_seen_at) VALUES(?,?,?,?,?)`, id, s.App, s.MessageCount, s.FirstSeenAt, s.LastSeenAt); err != nil {
				return err
			}
		}
		for _, s := range activity.Sessions {
			if _, err := tx.ExecContext(ctx, `INSERT INTO principal_session_sightings(principal_id,session_id,first_seen_at,last_seen_at) VALUES(?,?,?,?)`, id, s.SessionID, s.FirstSeenAt, s.LastSeenAt); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO principal_sightings(principal_id,app,message_count,first_seen_at,last_seen_at)
        SELECT principal_id,app,SUM(message_count),MIN(first_seen_at),MAX(last_seen_at) FROM principal_contact_link_updates WHERE principal_id=? AND app!='' GROUP BY app
        ON CONFLICT(principal_id,app) DO UPDATE SET message_count=message_count+excluded.message_count,first_seen_at=MIN(first_seen_at,excluded.first_seen_at),last_seen_at=MAX(last_seen_at,excluded.last_seen_at)`, dst); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO principal_session_sightings(principal_id,session_id,first_seen_at,last_seen_at)
        SELECT principal_id,session_id,MIN(first_seen_at),MAX(last_seen_at) FROM principal_contact_link_updates WHERE principal_id=? AND session_id!='' GROUP BY session_id
        ON CONFLICT(principal_id,session_id) DO UPDATE SET first_seen_at=MIN(first_seen_at,excluded.first_seen_at),last_seen_at=MAX(last_seen_at,excluded.last_seen_at)`, dst); err != nil {
		return err
	}
	existing := map[int64]bool{}
	for _, s := range r.DestinationActivity.Recent {
		existing[s.SeenAt] = true
	}
	for _, s := range r.SourceActivity.Recent {
		if !existing[s.SeenAt] {
			if _, err := tx.ExecContext(ctx, `DELETE FROM principal_recent_messages WHERE principal_id=? AND app=? AND seen_at=? AND preview=?`, dst, s.App, s.SeenAt, s.Preview); err != nil {
				return err
			}
		}
	}
	for id, activity := range map[string]contactActivitySnapshot{dst: r.DestinationActivity, r.Source.ID: r.SourceActivity} {
		for _, s := range activity.Recent {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO principal_recent_messages(principal_id,app,seen_at,preview) VALUES(?,?,?,?)`, id, s.App, s.SeenAt, s.Preview); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM principal_recent_messages WHERE principal_id=? AND seen_at NOT IN(SELECT seen_at FROM principal_recent_messages WHERE principal_id=? ORDER BY seen_at DESC LIMIT ?)`, id, id, defaultRecentMessagesWindow); err != nil {
			return err
		}
	}
	return nil
}

// A later deliberate merge into a linked person is independent of the contact
// link. Keep that contribution when undoing the older link.
func trackMergedContactActivityTx(ctx context.Context, tx *sql.Tx, src, dst string) error {
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT receipt FROM principal_contact_links WHERE principal_id=?`, dst).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	var receipt contactLinkReceipt
	if err := json.Unmarshal([]byte(raw), &receipt); err != nil {
		return err
	}
	principal, err := getPrincipalTx(ctx, tx, src)
	if err != nil {
		return err
	}
	introduced := map[string]bool{}
	for _, alias := range principal.Aliases {
		introduced[alias] = true
	}
	kept := receipt.AddedAliases[:0]
	for _, alias := range receipt.AddedAliases {
		if !introduced[alias] {
			kept = append(kept, alias)
		}
	}
	receipt.AddedAliases = kept
	updated, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE principal_contact_links SET receipt=? WHERE principal_id=?`, string(updated), dst); err != nil {
		return err
	}
	activity, err := snapshotContactActivity(ctx, tx, src)
	if err != nil {
		return err
	}
	for _, s := range activity.Sightings {
		if _, err := tx.ExecContext(ctx, `INSERT INTO principal_contact_link_updates(principal_id,app,session_id,message_count,first_seen_at,last_seen_at) VALUES(?,?,'',?,?,?)
            ON CONFLICT(principal_id,app,session_id) DO UPDATE SET message_count=message_count+excluded.message_count,first_seen_at=MIN(first_seen_at,excluded.first_seen_at),last_seen_at=MAX(last_seen_at,excluded.last_seen_at)`, dst, s.App, s.MessageCount, s.FirstSeenAt, s.LastSeenAt); err != nil {
			return err
		}
	}
	for _, s := range activity.Sessions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO principal_contact_link_updates(principal_id,app,session_id,message_count,first_seen_at,last_seen_at) VALUES(?,'',?,0,?,?)
            ON CONFLICT(principal_id,app,session_id) DO UPDATE SET first_seen_at=MIN(first_seen_at,excluded.first_seen_at),last_seen_at=MAX(last_seen_at,excluded.last_seen_at)`, dst, s.SessionID, s.FirstSeenAt, s.LastSeenAt); err != nil {
			return err
		}
	}
	return nil
}
