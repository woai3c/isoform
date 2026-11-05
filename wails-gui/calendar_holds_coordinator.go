package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// holdCoordinator serializes hold operations. The host schedules runDue at
// the saved deadline and on startup; the row, not the page, owns the intent.
type holdCoordinator struct {
	mu             sync.Mutex
	db             *sql.DB
	calendar       holdEventStore
	now            func() time.Time
	verifyCoverage func(holdEvent) error
	onOutcome      func(sourceKind, outcome, reason string, elapsed time.Duration)
	// onChange hears each persisted hold transition. id is empty for a change
	// that is not about one hold, such as an offer dismissal.
	onChange func(id string)
}

// changed announces one persisted transition. Call it after the row's new
// state is written, once per transition, and never for a write that changed
// nothing.
func (c *holdCoordinator) changed(id string) {
	if c.onChange != nil {
		c.onChange(id)
	}
}

type holdRecord struct {
	ID            string
	EventID       string
	ExternalID    string
	CalendarID    string
	Title         string
	Start         time.Time
	End           time.Time
	SourceKind    string
	SourceID      string
	SubjectKey    string
	State         string
	FailureReason string
	CreatedAt     time.Time
	WriteAfter    time.Time
	RemovedAt     time.Time
}

func (c *holdCoordinator) record(id string) (holdRecord, error) {
	var h holdRecord
	var start, end, created, writeAfter, removed int64
	err := c.db.QueryRow(`SELECT id, event_identifier, external_identifier, calendar_id, title, start, end, source_kind, source_id, subject_key, state, created_at, write_after, removed_at, failure_reason FROM holds WHERE id = ?`, id).Scan(&h.ID, &h.EventID, &h.ExternalID, &h.CalendarID, &h.Title, &start, &end, &h.SourceKind, &h.SourceID, &h.SubjectKey, &h.State, &created, &writeAfter, &removed, &h.FailureReason)
	if err != nil {
		return h, err
	}
	h.Start, h.End, h.CreatedAt = time.UnixMilli(start), time.UnixMilli(end), time.UnixMilli(created)
	if writeAfter != 0 {
		h.WriteAfter = time.UnixMilli(writeAfter)
	}
	if removed != 0 {
		h.RemovedAt = time.UnixMilli(removed)
	}
	return h, nil
}

// activeForSource lets a reopened panel follow its persisted hold, including
// the brief Undo window after a removal.
func (c *holdCoordinator) activeForSource(thing HoldThing, now time.Time) (*holdRecord, error) {
	var id string
	err := c.db.QueryRow(`SELECT id FROM holds WHERE subject_key = ? AND start > ? AND source_active = 1 AND (state IN ('pending_write', 'checking', 'held', 'moved') OR (state = 'failed' AND failure_reason = 'unconfirmed') OR (state = 'removed' AND event_identifier <> '' AND removed_at > ?)) ORDER BY created_at DESC LIMIT 1`, holdSubjectKey(thing), now.UnixMilli(), now.Add(-5*time.Second).UnixMilli()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	h, err := c.record(id)
	if err != nil {
		return nil, err
	}
	return &h, nil
}

func (h holdRecord) event() holdEvent {
	return holdEvent{CalendarID: h.CalendarID, Title: h.Title, Start: h.Start, End: h.End}
}

func (c *holdCoordinator) report(h holdRecord, outcome, reason string) {
	if c.onOutcome == nil {
		return
	}
	elapsed := c.now().Sub(h.CreatedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed > 7*24*time.Hour {
		elapsed = 7 * 24 * time.Hour
	}
	c.onOutcome(h.SourceKind, outcome, reason, elapsed)
}

func (c *holdCoordinator) releaseStarted(now time.Time) error {
	_, err := c.db.Exec(`UPDATE holds SET source_active = 0 WHERE source_active = 1 AND start <= ? AND state IN ('held', 'moved', 'checking', 'removed', 'gone', 'failed')`, now.UnixMilli())
	return err
}

// begin persists the user's Hold after checking, but does not write an event.
func (c *holdCoordinator) begin(h holdRecord) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if h.ID == "" || h.SourceID == "" {
		return errHoldUnavailable
	}
	if c.verifyCoverage == nil {
		return errHoldUnavailable
	}
	if err := c.verifyCoverage(h.event()); err != nil {
		return err
	}
	if err := checkHoldWindow(c.calendar, h.event(), now); err != nil {
		return err
	}
	if err := c.releaseStarted(now); err != nil {
		return err
	}
	_, err := c.db.Exec(`INSERT INTO holds (id, calendar_id, title, start, end, source_kind, source_id, subject_key, state, created_at, write_after) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending_write', ?, ?)`, h.ID, h.CalendarID, h.Title, h.Start.UnixMilli(), h.End.UnixMilli(), h.SourceKind, h.SourceID, holdSubjectKey(HoldThing{SourceKind: h.SourceKind, SourceID: h.SourceID}), now.UnixMilli(), now.Add(5*time.Second).UnixMilli())
	if err == nil {
		c.changed(h.ID)
	}
	return err
}

func (c *holdCoordinator) undo(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	h, err := c.record(id)
	if err != nil {
		return err
	}
	result, err := c.db.Exec(`UPDATE holds SET state = 'removed', write_after = 0, removed_at = ? WHERE id = ? AND state = 'pending_write' AND write_after > ?`, now.UnixMilli(), id, now.UnixMilli())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errHoldUnavailable
	}
	c.changed(id)
	c.report(h, "undone", "")
	return nil
}

// Only a definite EventKit rejection is retried. A serialization or process
// failure after a possible write must never cause an automatic duplicate.
func (c *holdCoordinator) saveWithRetry(event holdEvent) (holdEventIdentity, error) {
	identity, err := c.calendar.Save(event)
	if errors.Is(err, errHoldWriteFailed) {
		return c.calendar.Save(event)
	}
	return identity, err
}

// recoverWrites only adopts a single exact post-record event. Anything
// ambiguous stays in Calendar and is never treated as an event Isoform owns.
func (c *holdCoordinator) recoverWrites() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows, err := c.db.Query(`SELECT id FROM holds WHERE state = 'checking'`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		h, err := c.record(id)
		if err != nil {
			return err
		}
		events, err := c.calendar.ReadWindow(h.Start, h.End)
		if err != nil {
			return err
		}
		matches := make([]holdEventIdentity, 0, 1)
		for _, event := range events {
			start, startErr := time.Parse(time.RFC3339, event.Start)
			end, endErr := time.Parse(time.RFC3339, event.End)
			created := time.Unix(0, int64(event.CreationTimestamp*float64(time.Second)))
			if startErr == nil && endErr == nil && start.Equal(h.Start) && end.Equal(h.End) && event.CalendarID == h.CalendarID && event.Title == h.Title && event.LocalID != "" && event.LocalID != h.EventID && event.CreationTimestamp > 0 && created.After(h.CreatedAt) && len(event.Attendees) == 0 {
				matches = append(matches, holdEventIdentity{EventIdentifier: event.LocalID, ExternalIdentifier: event.ExternalID})
			}
		}
		if len(matches) == 1 {
			_, err = c.db.Exec(`UPDATE holds SET state = 'held', event_identifier = ?, external_identifier = ?, write_after = 0, removed_at = 0 WHERE id = ? AND state = 'checking'`, matches[0].EventIdentifier, matches[0].ExternalIdentifier, id)
			if err == nil {
				c.changed(id)
				c.report(h, "held", "")
			}
		} else {
			_, err = c.db.Exec(`UPDATE holds SET state = 'failed', failure_reason = 'unconfirmed' WHERE id = ? AND state = 'checking'`, id)
			if err == nil {
				c.changed(id)
				c.report(h, "failed", "write_failed")
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// runDue is safe to call after navigation or restart and serializes with Undo.
func (c *holdCoordinator) runDue() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if err := c.releaseStarted(now); err != nil {
		return err
	}
	rows, err := c.db.Query(`SELECT id FROM holds WHERE state = 'pending_write' AND write_after <= ? ORDER BY write_after, id`, now.UnixMilli())
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		h, err := c.record(id)
		if err != nil {
			return err
		}
		coverageErr := errHoldCoverageUnknown
		if c.verifyCoverage != nil {
			coverageErr = c.verifyCoverage(h.event())
		}
		// A restart can precede the first calendar sync. Keep the confirmed
		// intent pending while coverage is unknown, but never write a past slot.
		if errors.Is(coverageErr, errHoldCoverageUnknown) && h.Start.After(now) {
			continue
		}
		if coverageErr != nil || checkHoldWindow(c.calendar, h.event(), now) != nil {
			reason := "slot_unavailable"
			if !h.Start.After(now) {
				reason = "time_passed"
			}
			if _, updateErr := c.db.Exec(`UPDATE holds SET state = 'failed', failure_reason = ? WHERE id = ? AND state = 'pending_write'`, reason, id); updateErr != nil {
				return updateErr
			}
			c.changed(id)
			c.report(h, "failed", reason)
			continue
		}
		// Persist the in-flight state before calling EventKit. If the process or
		// database fails after a save, recovery must not blindly write a duplicate.
		claim, err := c.db.Exec(`UPDATE holds SET state = 'checking' WHERE id = ? AND state = 'pending_write'`, id)
		if err != nil {
			return err
		}
		claimed, err := claim.RowsAffected()
		if err != nil {
			return err
		}
		if claimed != 1 {
			continue
		}
		c.changed(id)
		identity, err := c.saveWithRetry(h.event())
		if err != nil || identity.EventIdentifier == "" {
			// Preserve the in-flight state if a write may have succeeded.
			if errors.Is(err, errHoldWriteFailed) || errors.Is(err, errCalendarNotAuthorized) || errors.Is(err, errHoldCalendarUnwritable) {
				reason := ""
				if errors.Is(err, errHoldCalendarUnwritable) {
					reason = "calendar_unavailable"
				}
				if _, updateErr := c.db.Exec(`UPDATE holds SET state = 'failed', failure_reason = ? WHERE id = ? AND state = 'checking'`, reason, id); updateErr != nil {
					return updateErr
				}
				c.changed(id)
			}
			c.report(h, "failed", "write_failed")
			continue
		}
		if _, err := c.db.Exec(`UPDATE holds SET state = 'held', event_identifier = ?, external_identifier = ?, write_after = 0 WHERE id = ? AND state = 'checking'`, identity.EventIdentifier, identity.ExternalIdentifier, id); err != nil {
			return fmt.Errorf("record saved hold: %w", err)
		}
		c.changed(id)
		c.report(h, "held", "")
	}
	return nil
}

func (c *holdCoordinator) remove(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, err := c.record(id)
	if err != nil {
		return err
	}
	if (h.State != "held" && h.State != "moved") || h.EventID == "" {
		return errHoldUnavailable
	}
	// Read the current event before deleting it. A title-only restore cannot
	// preserve alerts, invitees or other fields the person added in Calendar.
	snapshot, err := c.calendar.Lookup(h.EventID, h.ExternalID)
	if err != nil {
		c.report(h, "failed", "remove_failed")
		return err
	}
	if snapshot == nil {
		c.report(h, "failed", "remove_failed")
		return errHoldUnavailable
	}
	if snapshot.HasUnsupportedEdits || snapshot.Title != h.Title || snapshot.CalendarID != h.CalendarID {
		c.report(h, "failed", "remove_failed")
		return errHoldEdited
	}
	start, startErr := time.Parse(time.RFC3339, snapshot.Start)
	end, endErr := time.Parse(time.RFC3339, snapshot.End)
	if startErr != nil || endErr != nil || !start.After(c.now()) || !end.After(start) || snapshot.EventID == "" || snapshot.CalendarID == "" || strings.TrimSpace(snapshot.Title) == "" {
		c.report(h, "failed", "remove_failed")
		return errHoldUnavailable
	}
	// Persist the person's latest edits before removing the event, so Undo
	// can reproduce the fields the hold contract supports.
	state := h.State
	if !start.Equal(h.Start) || !end.Equal(h.End) {
		state = "moved"
	}
	// A move the person made in Calendar is persisted before the removal, so
	// a failed removal leaves a visible change that the page must hear about.
	movedBeforeRemove := state != h.State || !start.Equal(h.Start) || !end.Equal(h.End)
	if _, err := c.db.Exec(`UPDATE holds SET event_identifier = ?, calendar_id = ?, title = ?, start = ?, end = ?, state = ? WHERE id = ? AND state IN ('held', 'moved')`, snapshot.EventID, snapshot.CalendarID, snapshot.Title, start.UnixMilli(), end.UnixMilli(), state, id); err != nil {
		return err
	}
	err = c.calendar.Remove(snapshot.EventID)
	if errors.Is(err, errHoldWriteFailed) {
		err = c.calendar.Remove(snapshot.EventID)
	}
	if err != nil {
		c.report(h, "failed", "remove_failed")
		if movedBeforeRemove {
			c.changed(id)
		}
		return err
	}
	_, err = c.db.Exec(`UPDATE holds SET state = 'removed', removed_at = ? WHERE id = ?`, c.now().UnixMilli(), id)
	if err == nil {
		c.changed(id)
		c.report(h, "removed", "")
	}
	return err
}

func (c *holdCoordinator) undoRemove(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, err := c.record(id)
	if err != nil {
		return err
	}
	now := c.now()
	if h.State != "removed" || h.EventID == "" || h.RemovedAt.IsZero() || now.Before(h.RemovedAt) || !now.Before(h.RemovedAt.Add(5*time.Second)) || !h.Start.After(now) {
		return errHoldUnavailable
	}
	// Undo restores the just-removed event, including a time the person moved
	// outside work or quiet-hour rules. It is not a new time proposal.
	event := h.event()
	if _, err := c.db.Exec(`UPDATE holds SET state = 'checking' WHERE id = ? AND state = 'removed'`, id); err != nil {
		return err
	}
	c.changed(id)
	identity, err := c.saveWithRetry(event)
	if err != nil {
		if errors.Is(err, errHoldWriteFailed) || errors.Is(err, errCalendarNotAuthorized) || errors.Is(err, errHoldCalendarUnwritable) {
			if _, updateErr := c.db.Exec(`UPDATE holds SET state = 'removed' WHERE id = ? AND state = 'checking'`, id); updateErr != nil {
				return updateErr
			}
			c.changed(id)
		}
		c.report(h, "failed", "restore_failed")
		return err
	}
	if identity.EventIdentifier == "" {
		return errors.New("calendar did not return an event identifier")
	}
	_, err = c.db.Exec(`UPDATE holds SET state = 'held', event_identifier = ?, external_identifier = ?, removed_at = 0 WHERE id = ?`, identity.EventIdentifier, identity.ExternalIdentifier, id)
	if err == nil {
		c.changed(id)
		c.report(h, "undone", "")
	}
	return err
}

// reconcile follows only identifiers from Isoform's own records. An EventKit
// error leaves the prior state untouched; absence means the user removed it.
func (c *holdCoordinator) reconcile() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows, err := c.db.Query(`SELECT id FROM holds WHERE state IN ('held', 'moved') AND event_identifier <> ''`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range ids {
		h, err := c.record(id)
		if err != nil {
			return err
		}
		snapshot, err := c.calendar.Lookup(h.EventID, h.ExternalID)
		if err != nil {
			return err
		}
		if snapshot == nil {
			if _, err := c.db.Exec(`UPDATE holds SET state = 'gone' WHERE id = ?`, id); err != nil {
				return err
			}
			c.changed(id)
			continue
		}
		start, errStart := time.Parse(time.RFC3339, snapshot.Start)
		end, errEnd := time.Parse(time.RFC3339, snapshot.End)
		if errStart != nil || errEnd != nil || !end.After(start) || snapshot.CalendarID == "" || snapshot.EventID == "" {
			return errHoldUnavailable
		}
		state := h.State
		if !start.Equal(h.Start) || !end.Equal(h.End) {
			state = "moved"
		}
		// Keep the originally written title and calendar so a later Remove can
		// recognize edits even after reconciling a moved event.
		if state == h.State && start.Equal(h.Start) && end.Equal(h.End) && snapshot.EventID == h.EventID {
			continue
		}
		if _, err := c.db.Exec(`UPDATE holds SET state = ?, event_identifier = ?, start = ?, end = ? WHERE id = ?`, state, snapshot.EventID, start.UnixMilli(), end.UnixMilli(), id); err != nil {
			return err
		}
		c.changed(id)
	}
	return nil
}
