package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"wails-gui/storage"
)

type searchActivityRow struct {
	id, kind, title, body, runs, basis, apps string
	start, end                               int64
}

func (a *App) searchActivity(ctx context.Context, words []string) ([]searchCandidate, error) {
	if a == nil || a.activity == nil || a.activity.store == nil || a.IsClosedEyesModeEnabled() {
		return nil, nil
	}
	privacy, err := a.mcpReadPrivacy(ctx)
	if errors.Is(err, errMCPReadPaused) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	predicate, args := storage.SearchWordsPredicate(`title || char(10) || body`, words)
	// Only generated/corrected display fields participate in matching. Raw
	// capture is consulted solely by the shared provenance/privacy checks.
	rows, err := a.activity.store.reader().QueryContext(ctx, `WITH candidates AS (
        SELECT id,'moment' AS kind,COALESCE(NULLIF(user_title,''),title) AS title,
            COALESCE(NULLIF(user_description,''),description) || char(10) || latest_change AS body,
            run_ids AS runs,'' AS basis,active_ms_by_app AS apps,start_ms AS start,end_ms AS end FROM activities
        UNION ALL
        SELECT t.id,'thread',COALESCE(NULLIF(t.user_title,''),t.title),'','',t.title_basis,'',
            (SELECT MIN(a.start_ms) FROM activity_thread_members m JOIN activities a ON a.id=m.activity_id WHERE m.thread_id=t.id),
            (SELECT MAX(a.end_ms) FROM activity_thread_members m JOIN activities a ON a.id=m.activity_id WHERE m.thread_id=t.id)
        FROM activity_threads t WHERE EXISTS(SELECT 1 FROM activity_thread_members m JOIN activities a ON a.id=m.activity_id WHERE m.thread_id=t.id)
    ) SELECT id,kind,title,body,runs,basis,apps,start,end FROM candidates WHERE `+predicate+` ORDER BY start DESC,id LIMIT 50`, args...)
	if err != nil {
		return nil, err
	}
	batch := []searchActivityRow{}
	for rows.Next() {
		var r searchActivityRow
		if err := rows.Scan(&r.id, &r.kind, &r.title, &r.body, &r.runs, &r.basis, &r.apps, &r.start, &r.end); err != nil {
			rows.Close()
			return nil, err
		}
		batch = append(batch, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []searchCandidate{}
	for _, r := range batch {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		session := ""
		target := SearchTarget{Kind: "activity_moment", ActivityID: r.id, At: r.start}
		if r.kind == "moment" {
			allowed, sid, err := a.searchMomentAllowed(ctx, privacy, r.runs, r.apps, r.start, r.end)
			if err != nil {
				return nil, err
			}
			if !allowed {
				continue
			}
			session = sid
			target.SessionID = session
		} else {
			allowed, err := a.searchThreadAllowed(ctx, privacy, r)
			if err != nil {
				return nil, err
			}
			if !allowed {
				continue
			}
			target = SearchTarget{Kind: "activity_thread", ThreadID: r.id, StartMs: r.start, EndMs: r.end}
		}
		out = append(out, searchCandidate{result: SearchResult{ID: r.kind + ":" + r.id, Place: "activity", Title: r.title, At: r.start, Target: target}, body: r.body})
	}
	return out, ctx.Err()
}

func (a *App) searchMomentAllowed(ctx context.Context, p *mcpReadPrivacy, rawRuns, rawApps string, start, end int64) (bool, string, error) {
	if !p.timeAllowed(start, end) {
		return false, "", nil
	}
	// The stored display projection can declare apps beyond a surviving run.
	// Validate those declarations as well as every original capture session.
	var apps map[string]int64
	if json.Unmarshal([]byte(rawApps), &apps) != nil {
		return false, "", nil
	}
	for app := range apps {
		if !p.appAllowed(strings.TrimPrefix(app, "app:")) {
			return false, "", nil
		}
	}
	var ids []string
	if json.Unmarshal([]byte(rawRuns), &ids) != nil || len(ids) == 0 || len(ids) > searchCandidateLimit {
		return false, "", nil
	}
	first := ""
	for _, id := range ids {
		run, err := a.activity.store.GetTimelineRunByIDContext(ctx, id)
		if err != nil {
			return false, "", err
		}
		if run == nil || run.EvidenceExcluded || !p.appAllowed(run.AppKey, run.AppName) || !p.timeAllowed(run.StartMs, run.EndMs) || len(run.SessionIDs) == 0 {
			return false, "", nil
		}
		for _, sid := range run.SessionIDs {
			allowed, err := p.sessionAllowedRead(sid)
			if err != nil {
				return false, "", err
			}
			if !allowed {
				return false, "", nil
			}
			if first == "" {
				first = sid
			}
		}
	}
	return true, first, ctx.Err()
}

func (a *App) searchThreadAllowed(ctx context.Context, p *mcpReadPrivacy, r searchActivityRow) (bool, error) {
	// Generated thread titles are readable only with every original title
	// input still present and authorized. User titles need readable members.
	var basis []string
	if json.Unmarshal([]byte(r.basis), &basis) != nil || len(basis) > searchCandidateLimit {
		return false, nil
	}
	var userTitle string
	if err := a.activity.store.reader().QueryRowContext(ctx, `SELECT user_title FROM activity_threads WHERE id=?`, r.id).Scan(&userTitle); err != nil {
		return false, err
	}
	if strings.TrimSpace(userTitle) == "" && len(basis) == 0 {
		return false, nil
	}
	rows, err := a.activity.store.reader().QueryContext(ctx, `SELECT a.id,a.run_ids,a.active_ms_by_app,a.start_ms,a.end_ms FROM activities a
        JOIN activity_thread_members m ON m.activity_id=a.id WHERE m.thread_id=? ORDER BY a.start_ms DESC LIMIT 51`, r.id)
	if err != nil {
		return false, err
	}
	members := []searchActivityRow{}
	for rows.Next() {
		var m searchActivityRow
		if err := rows.Scan(&m.id, &m.runs, &m.apps, &m.start, &m.end); err != nil {
			rows.Close()
			return false, err
		}
		members = append(members, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if len(members) == 0 || len(members) > searchCandidateLimit {
		return false, nil
	}
	readable := map[string]bool{}
	for _, m := range members {
		allowed, _, err := a.searchMomentAllowed(ctx, p, m.runs, m.apps, m.start, m.end)
		if err != nil {
			return false, err
		}
		// A thread destination must not reveal a range/title derived from a
		// hidden member, even if another member remains readable.
		if !allowed {
			return false, nil
		}
		readable[m.id] = true
	}
	if userTitle == "" {
		for _, id := range basis {
			if !readable[id] {
				return false, nil
			}
		}
	}
	return true, ctx.Err()
}
