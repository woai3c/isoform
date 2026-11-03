package main

import (
	"context"
	"database/sql"
	"time"

	"wails-gui/storage"
)

// Only events already admitted to the privacy-filtered strip can become
// interactive holds. A title is never taken from the holds table here.
func markHeldStripEvents(shape *TodayDayShape, rows []storage.CalendarEventRow, db *sql.DB, now time.Time) error {
	visible := make(map[string]int, len(shape.Events))
	for i, event := range shape.Events {
		visible[event.ID] = i
	}
	type identity struct {
		id, localID, calendarID string
		start, end              int64
	}
	query, err := db.Query(`SELECT id, event_identifier, calendar_id, start, end FROM holds WHERE state IN ('held', 'moved') AND source_active = 1 AND start > ? AND event_identifier <> ''`, now.UnixMilli())
	if err != nil {
		return err
	}
	var holds []identity
	for query.Next() {
		var item identity
		if err := query.Scan(&item.id, &item.localID, &item.calendarID, &item.start, &item.end); err != nil {
			query.Close()
			return err
		}
		holds = append(holds, item)
	}
	if err := query.Err(); err != nil {
		query.Close()
		return err
	}
	if err := query.Close(); err != nil {
		return err
	}
	for _, row := range rows {
		index, shown := visible[row.ID]
		if !shown || row.Source != "eventkit" || row.LocalID == "" {
			continue
		}
		for _, hold := range holds {
			if row.LocalID == hold.localID && row.CalendarID == hold.calendarID && row.StartAt == hold.start && row.EndAt == hold.end {
				shape.Events[index].HoldID = hold.id
				break
			}
		}
	}
	return nil
}

func (a *App) markTodayHeldEvents(shape *TodayDayShape, day, now time.Time) {
	c, err := a.currentHoldCoordinator()
	if err != nil || !calendarAccessGranted() {
		return
	}
	ctx := context.Background()
	value, err := a.store.GetUIState(ctx, calendarOfferHoldsKey)
	if err != nil || value == "false" {
		return
	}
	rows, err := a.store.ListCalendarEvents(ctx, day.UnixMilli(), day.AddDate(0, 0, 1).UnixMilli(), false)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = markHeldStripEvents(shape, rows, c.db, now)
}
