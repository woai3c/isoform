package main

import (
	"database/sql"
	"encoding/json"
	"regexp"
	"strings"
)

var invoiceCheckin = regexp.MustCompile(`(?i)^send (.+?) your invoice payment check-in[.]?$`)

func plainInvoiceFollowup(text string) string {
	match := invoiceCheckin.FindStringSubmatch(strings.TrimSpace(text))
	if len(match) != 2 {
		return text
	}
	return "Ask " + match[1] + " when the invoice will be paid"
}

// Keep suggestion IDs, moment references, and dismissal state while repairing
// the saved English wording. Unrecognized wording is left intact.
func migrateNoticedPlainInvoiceFollowup(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT date, language, items FROM activity_noticed WHERE language = 'en'`)
	if err != nil {
		return err
	}
	type stored struct{ date, language, items string }
	var records []stored
	for rows.Next() {
		var row stored
		if err := rows.Scan(&row.date, &row.language, &row.items); err != nil {
			rows.Close()
			return err
		}
		records = append(records, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, row := range records {
		var items []map[string]json.RawMessage
		if err := json.Unmarshal([]byte(row.items), &items); err != nil {
			continue
		}
		changed := false
		for _, item := range items {
			var kind, old string
			if json.Unmarshal(item["kind"], &kind) != nil || json.Unmarshal(item["text"], &old) != nil || kind != noticedKindFollowUp {
				continue
			}
			next := plainInvoiceFollowup(old)
			if next == old {
				continue
			}
			item["text"], _ = json.Marshal(next)
			changed = true
			oldKey, newKey := "text:"+kind+":"+noticedNormalize(old), "text:"+kind+":"+noticedNormalize(next)
			if _, err := tx.Exec(`INSERT OR IGNORE INTO activity_noticed_dismissals(key, created_at) SELECT ?, created_at FROM activity_noticed_dismissals WHERE key = ?`, newKey, oldKey); err != nil {
				return err
			}
		}
		if changed {
			raw, err := json.Marshal(items)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE activity_noticed SET items = ? WHERE date = ? AND language = ?`, string(raw), row.date, row.language); err != nil {
				return err
			}
		}
	}
	return nil
}
