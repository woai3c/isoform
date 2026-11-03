package main

import (
	"context"
	"encoding/json"
	"strings"

	"wails-gui/internal/pii"
)

const topicEventScanLimit = 200
const topicThreadSourceLimit = 64

type topicEventScan struct {
	items   []MainChatTopicItem
	before  topicItemCursor
	more    bool
	scanned int
}

// Events remain live Activity projections. One bounded candidate scan serves a
// read; the page cursor can advance even when that scan finds no readable event.
func (a *App) chatTopicEvents(ctx context.Context, keys ChatTopicKeys, before topicItemCursor, limit int) ([]MainChatTopicItem, error) {
	scan, err := a.scanChatTopicEvents(ctx, keys, before, limit)
	return scan.items, err
}
func (a *App) scanChatTopicEvents(ctx context.Context, keys ChatTopicKeys, before topicItemCursor, limit int) (topicEventScan, error) {
	result := topicEventScan{items: []MainChatTopicItem{}, before: before}
	if a.IsClosedEyesModeEnabled() || a.activity == nil || a.activity.store == nil || len(keys.ThreadKeys) == 0 {
		return result, nil
	}
	wanted := map[string]bool{}
	for _, key := range keys.ThreadKeys {
		wanted[key] = true
	}
	// Materialize the raw candidate budget before joining threads. Otherwise a
	// sparse thread registry could still walk the entire activities table.
	rows, err := a.activity.store.reader().QueryContext(ctx, `WITH candidates AS MATERIALIZED (
 SELECT id,end_ms FROM activities
 WHERE end_ms < ? OR (end_ms = ? AND 'event:' || id < ?)
 ORDER BY end_ms DESC,id DESC LIMIT ?)
 SELECT a.id,a.end_ms,COALESCE(m.thread_id,''),COALESCE(t.purpose_key,'')
 FROM candidates a LEFT JOIN activity_thread_members m ON m.activity_id=a.id
 LEFT JOIN activity_threads t ON t.id=m.thread_id
 ORDER BY a.end_ms DESC,a.id DESC`, before.At, before.At, before.ID, topicEventScanLimit+1)
	if err != nil {
		return result, err
	}
	type candidate struct {
		id              string
		at              int64
		thread, purpose string
	}
	batch := []candidate{}
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.at, &c.thread, &c.purpose); err != nil {
			rows.Close()
			return result, err
		}
		batch = append(batch, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, err
	}
	matchedThreads := map[string]bool{}
	checkedThreads := map[string]bool{}
	for i, c := range batch {
		if i == topicEventScanLimit {
			result.more = true
			break
		}
		result.scanned++
		result.before = topicItemCursor{TopicID: before.TopicID, At: c.at, ID: "event:" + c.id}
		if c.thread != "" {
			if !checkedThreads[c.thread] {
				matched, err := a.chatTopicThreadMatches(ctx, c.thread, c.purpose, wanted)
				if err != nil {
					return result, err
				}
				matchedThreads[c.thread], checkedThreads[c.thread] = matched, true
			}
			if matchedThreads[c.thread] {
				record, row, allowed := a.chatTopicActivity(ctx, c.id)
				if allowed {
					text, _ := pii.RedactText(strings.TrimSpace(row.Title))
					text = truncateAtRuneBoundary(pii.RedactCodes(text), todayChatItemTokens)
					result.items = append(result.items, MainChatTopicItem{ID: "event:" + record.ID, Kind: "event", At: record.At, Actor: "user", Text: text})
				}
			}
		}
		if len(result.items) >= limit {
			result.more = i+1 < len(batch)
			break
		}
	}
	return result, ctx.Err()
}

func (a *App) chatTopicThreadMatches(ctx context.Context, threadID, purpose string, wanted map[string]bool) (bool, error) {
	if purpose != "" && wanted[purpose] {
		return true, nil
	}
	rows, err := a.activity.store.reader().QueryContext(ctx, `SELECT activity_id FROM activity_thread_members WHERE thread_id = ? ORDER BY activity_id LIMIT ?`, threadID, topicThreadSourceLimit)
	if err != nil {
		return false, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return false, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		if _, _, ok := a.chatTopicActivity(ctx, id); !ok {
			continue
		}
		row, err := a.activity.store.GetActivityByID(id)
		if err != nil {
			return false, err
		}
		if row == nil {
			continue
		}
		for key := range threadSureKeys(threadMomentFromRow(*row), nil) {
			if wanted[key] {
				return true, nil
			}
		}
	}
	return false, ctx.Err()
}

// Slack text stays withheld alongside Mail, including a mixed-source run
// whose visible app label alone would hide the original channel provenance.
func (a *App) chatTopicActivity(ctx context.Context, id string) (todayChatRecord, *ActivityRow, bool) {
	record, ok := a.todayChatActivityByID(ctx, id)
	if !ok {
		return todayChatRecord{}, nil, false
	}
	row, err := a.activity.store.GetActivityByID(id)
	if err != nil || row == nil {
		return todayChatRecord{}, nil, false
	}
	held := func(name string) bool { return strings.Contains(strings.ToLower(name), "slack") }
	apps, _ := topicAppTime(row.ActiveMsByApp)
	for _, app := range apps {
		if held(app) {
			return todayChatRecord{}, nil, false
		}
	}
	for _, id := range composeParseStrings(row.RunIDs) {
		run, err := a.activity.store.GetTimelineRunByIDContext(ctx, id)
		if err != nil || run == nil || held(run.AppName) || held(run.AppKey) {
			return todayChatRecord{}, nil, false
		}
		sessions, err := a.activity.store.GetSessionsByIDsContext(ctx, run.SessionIDs)
		if err != nil {
			return todayChatRecord{}, nil, false
		}
		for _, session := range sessions {
			for _, app := range composeParseStrings(session.AppNames) {
				if held(app) {
					return todayChatRecord{}, nil, false
				}
			}
		}
	}
	return record, row, true
}

func (a *App) chatTopicRecentThreadTitles(ctx context.Context, events []MainChatTopicItem) ([]string, error) {
	titles := []string{}
	seen := map[string]bool{}
	if a.activity == nil || a.activity.store == nil {
		return titles, nil
	}
	store := a.activity.store
	for _, event := range events {
		var thread activityThreadRow
		var basis string
		err := store.reader().QueryRowContext(ctx, `SELECT t.id,t.title,t.title_basis,t.user_title
            FROM activity_threads t JOIN activity_thread_members m ON m.thread_id=t.id
            WHERE m.activity_id=?`, strings.TrimPrefix(event.ID, "event:")).Scan(&thread.ID, &thread.Title, &basis, &thread.UserTitle)
		if err != nil {
			return nil, err
		}
		if seen[thread.ID] {
			continue
		}
		seen[thread.ID] = true
		if json.Unmarshal([]byte(basis), &thread.TitleBasis) != nil || len(thread.TitleBasis) == 0 || len(thread.TitleBasis) > topicThreadSourceLimit {
			thread.TitleBasis = nil
			thread.Title = ""
		}
		live := map[string]ActivityRow{}
		for _, id := range thread.TitleBasis {
			if _, row, ok := a.chatTopicActivity(ctx, id); ok {
				live[id] = *row
			}
		}
		title, _ := activityThreadDisplayTitle(thread, live, event.Text)
		title, _ = pii.RedactText(title)
		titles = append(titles, truncateAtRuneBoundary(pii.RedactCodes(title), todayChatItemTokens))
	}
	return titles, ctx.Err()
}
