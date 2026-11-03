package main

import "database/sql"

// Keep the indexed expression identical to the predecessor query so SQLite
// can seek a run's app directly without decoding intervening other-app rows.
const activityEventAppKeySQL = `CASE WHEN json_valid(data) THEN
	CASE WHEN trim(COALESCE(json_extract(data,'$.appBundleId'),''))!='' THEN trim(json_extract(data,'$.appBundleId'))
	WHEN trim(COALESCE(json_extract(data,'$.appName'),''))!='' THEN 'app:'||trim(json_extract(data,'$.appName')) ELSE 'unknown' END
	ELSE '' END`

const activityTimelineRunPredecessorSQL = `SELECT id,sort_order,data FROM activity_events
	WHERE session_id=? AND id>=? AND id<=? AND timestamp>=? AND timestamp<?
	AND ` + activityEventAppKeySQL + `=? ORDER BY timestamp DESC,id DESC LIMIT 1`

func migrateActivityEventContextIndex(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_activity_events_context ON activity_events
		(session_id, ` + activityEventAppKeySQL + `, timestamp DESC, id DESC)`)
	return err
}
