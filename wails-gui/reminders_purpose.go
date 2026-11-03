package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"time"
	"unicode/utf8"

	"wails-gui/internal/calendar"
	"wails-gui/storage"
)

// Open reminders count as commitments. After every reminders read, each
// reminder is reconciled with the Purpose Map: an open one gets a commitment
// (dated when the reminder is, so commitment_slipping fires as it comes due
// or goes overdue), a renamed or rescheduled one revises it, a ticked-off one
// resolves it, a deleted one dismisses it, and one ticked off and then
// reopened in Reminders reopens it. The user wrote the reminder, so its
// evidence is explicit_user and the commitment is supported from the start.
//
// Only the reminder's own changes are written. A commitment the Purpose
// reconciler or the user changed in Isoform is not changed back unless the
// reminder itself changes, and one closed by anything other than the
// reminder is not reopened.
const (
	reminderPurposeSourceKind = "reminder"
	reminderPurposeConfidence = 0.9
	// reminderCommitmentLimit bounds how many commitments made from
	// reminders are open at once, so a long shopping list does not crowd the
	// Purpose Map. New ones are made soonest due first, undated last, while
	// fewer than this are open.
	reminderCommitmentLimit = 100
	// reminderTitleMaxRunes keeps a pasted wall of text out of a title.
	reminderTitleMaxRunes = 300
)

// reminderReconcileResult counts what one reconciliation wrote.
type reminderReconcileResult struct {
	Created   int
	Revised   int
	Resolved  int
	Dismissed int
	Reopened  int
	Skipped   int
}

func (r reminderReconcileResult) changed() bool {
	return r.Created+r.Revised+r.Resolved+r.Dismissed+r.Reopened > 0
}

// reminderPurposeRef is the bounded identity of a reminder inside the Purpose
// Map: a hash of its uid, so an arbitrarily long external identifier never
// reaches the ledger's key limits.
func reminderPurposeRef(uid string) string {
	sum := sha256.Sum256([]byte(uid))
	return hex.EncodeToString(sum[:16])
}

// reminderPurposeKey is the idempotency key of the commitment made from one
// reminder; its root id derives from it.
func reminderPurposeKey(uid string) string {
	return "reminder:" + reminderPurposeRef(uid)
}

func reminderPurposeRootID(uid string) string {
	return deterministicAgencyID("purpose-open-loop", reminderPurposeKey(uid))
}

// reminderFromRow is the stored reminder as the pure package sees it.
func reminderFromRow(row storage.ReminderRow) calendar.Reminder {
	r := calendar.Reminder{
		UID: row.UID, Title: row.Title, List: row.List, Priority: row.Priority,
		DueAllDay: row.DueAllDay, Completed: row.CompletedAt > 0,
	}
	if row.DueAt != 0 {
		r.Due = time.UnixMilli(row.DueAt)
	}
	if row.CompletedAt > 0 {
		r.CompletedAt = time.UnixMilli(row.CompletedAt)
	}
	return r
}

func reminderCommitmentTitle(title string) string {
	if utf8.RuneCountInString(title) <= reminderTitleMaxRunes {
		return title
	}
	runes := []rune(title)
	return string(runes[:reminderTitleMaxRunes])
}

func reminderCommitmentScope(list string) string {
	if list == "" {
		return "From Reminders"
	}
	return fmt.Sprintf("From the Reminders list %q", list)
}

func reminderCommitmentDue(r calendar.Reminder, loc *time.Location) (string, string) {
	if !r.HasDue() {
		return PurposeDueNone, ""
	}
	return PurposeDueDate, r.DueValue(loc)
}

// reminderEvidence is the one piece of evidence a reminder lends an event.
// The source hash is the reminder's fingerprint, which is how a later
// reconciliation tells whether the reminder changed since it last wrote.
func reminderEvidence(r calendar.Reminder, stance string, observedAt int64, loc *time.Location) PurposeEvidenceInput {
	fingerprint := r.Fingerprint(loc)
	ref := reminderPurposeRef(r.UID)
	return PurposeEvidenceInput{
		SourceKind:       reminderPurposeSourceKind,
		SourceID:         ref,
		SourceVersion:    fingerprint[:16],
		SourceObservedAt: observedAt,
		SourceHash:       fingerprint,
		EvidenceClass:    PurposeEvidenceExplicitUser,
		Stance:           stance,
		IndependenceKey:  "reminder:" + ref,
		PrivacyDecision:  TaskContextPrivacyRedact,
	}
}

// reminderCreateInput is the commitment an open reminder becomes.
func reminderCreateInput(r calendar.Reminder, occurredAt int64, loc *time.Location) CreatePurposeOpenLoopInput {
	title := reminderCommitmentTitle(r.Title)
	dueKind, dueValue := reminderCommitmentDue(r, loc)
	return CreatePurposeOpenLoopInput{
		IdempotencyKey: reminderPurposeKey(r.UID),
		CanonicalKey:   reminderPurposeKey(r.UID),
		Kind:           PurposeKindCommitment,
		Title:          title,
		DesiredOutcome: title,
		Owner:          PurposeOwnerUser,
		Scope:          reminderCommitmentScope(r.List),
		DueKind:        dueKind,
		DueValue:       dueValue,
		// Unknown reasoning cannot carry a rationale (purpose_map.go rejects it).
		ReasoningStatus: PurposeReasoningUnknown,
		Confidence:      reminderPurposeConfidence,
		OccurredAt:      occurredAt,
		Evidence:        []PurposeEvidenceInput{reminderEvidence(r, PurposeStanceSupport, occurredAt, loc)},
	}
}

// reminderAppendInput moves the commitment one step. The idempotency key
// names the predecessor, so a retry after a crash never writes twice and a
// reminder that changes back and forth still gets one event per change.
func reminderAppendInput(detail PurposeOpenLoopDetail, r calendar.Reminder, eventType, stance string, occurredAt int64, loc *time.Location) AppendPurposeOpenLoopEventInput {
	current := purposeCurrentLedgerEvent(detail)
	input := AppendPurposeOpenLoopEventInput{
		OpenLoopID:      detail.OpenLoop.ID,
		ExpectedEventID: current.ID,
		IdempotencyKey:  fmt.Sprintf("%s:%s:%s:%s", reminderPurposeKey(r.UID), eventType, current.ID, r.Fingerprint(loc)[:16]),
		EventType:       eventType,
		Title:           current.Title,
		DesiredOutcome:  current.DesiredOutcome,
		Owner:           current.Owner,
		Counterparty:    current.Counterparty,
		Scope:           current.Scope,
		DueKind:         current.DueKind,
		DueValue:        current.DueValue,
		ReasoningStatus: PurposeReasoningUnknown,
		Confidence:      reminderPurposeConfidence,
		OccurredAt:      occurredAt,
		Evidence:        []PurposeEvidenceInput{reminderEvidence(r, stance, occurredAt, loc)},
	}
	// A revision keeps a waiting commitment waiting; every other event
	// leaves the waiting state, and the ledger rejects a stale condition.
	if eventType == PurposeEventRevised {
		input.WaitingCondition = current.WaitingCondition
	}
	// A revision or a reopening carries what the reminder says now.
	if eventType == PurposeEventRevised || eventType == PurposeEventReopened {
		title := reminderCommitmentTitle(r.Title)
		input.Title, input.DesiredOutcome = title, title
		input.Scope = reminderCommitmentScope(r.List)
		input.DueKind, input.DueValue = reminderCommitmentDue(r, loc)
	}
	return input
}

// lastReminderEvidence is the evidence of the latest event a reminder wrote,
// and whether that event is the commitment's current one.
func lastReminderEvidence(detail PurposeOpenLoopDetail) (PurposeEvidenceRecord, bool, bool) {
	byEvent := map[string]PurposeEvidenceRecord{}
	for _, evidence := range detail.Evidence {
		if evidence.SourceKind == reminderPurposeSourceKind {
			byEvent[evidence.EventID] = evidence
		}
	}
	var last PurposeEvidenceRecord
	lastSequence, lastID := 0, ""
	for _, event := range detail.Events {
		if evidence, ok := byEvent[event.ID]; ok && event.Sequence > lastSequence {
			last, lastSequence, lastID = evidence, event.Sequence, event.ID
		}
	}
	return last, lastID != "", lastID != "" && lastID == detail.Current.ID
}

// reconcileReminderCommitments brings the Purpose Map in line with the
// stored reminders. rows come open first, soonest due first, which is the
// order the commitment limit applies in.
func reconcileReminderCommitments(store *PurposeMapStore, rows []storage.ReminderRow, now time.Time, loc *time.Location) (reminderReconcileResult, error) {
	var res reminderReconcileResult
	if store == nil {
		return res, fmt.Errorf("Purpose storage is unavailable")
	}
	if loc == nil {
		loc = time.Local
	}
	details, err := store.ListDetailsAll()
	if err != nil {
		return res, err
	}
	byID := make(map[string]PurposeOpenLoopDetail, len(details))
	for _, detail := range details {
		byID[detail.OpenLoop.ID] = detail
	}
	// Never ahead of the wall clock: the ledger rejects future events.
	occurredAt := now.UnixMilli()
	if wall := time.Now().UnixMilli(); occurredAt > wall {
		occurredAt = wall
	}
	// Open commitments of reminders still open; one whose reminder was just
	// closed is resolved in this pass and frees its place.
	live := 0
	for _, row := range rows {
		if row.CompletedAt != 0 || row.DeletedAt != 0 {
			continue
		}
		if detail, ok := byID[reminderPurposeRootID(row.UID)]; ok && purposeStateOpen(detail.Current.LifecycleState) {
			live++
		}
	}
	for _, row := range rows {
		r := reminderFromRow(row)
		open := row.CompletedAt == 0 && row.DeletedAt == 0
		detail, exists := byID[reminderPurposeRootID(r.UID)]
		if !exists {
			if !open {
				continue
			}
			if live >= reminderCommitmentLimit {
				res.Skipped++
				continue
			}
			if _, _, err := store.Create(reminderCreateInput(r, occurredAt, loc)); err != nil {
				log.Printf("[reminders] commitment for a reminder: %v", err)
				continue
			}
			res.Created++
			live++
			continue
		}
		at := occurredAt
		if at < detail.Current.OccurredAt {
			at = detail.Current.OccurredAt
		}
		eventType, stance := reminderTransition(detail, r, open, row.DeletedAt > 0, loc)
		if eventType == "" {
			continue
		}
		if _, _, err := store.AppendEvent(reminderAppendInput(detail, r, eventType, stance, at, loc)); err != nil {
			log.Printf("[reminders] %s a reminder commitment: %v", eventType, err)
			continue
		}
		switch eventType {
		case PurposeEventRevised:
			res.Revised++
		case PurposeEventResolved:
			res.Resolved++
		case PurposeEventDismissed:
			res.Dismissed++
		case PurposeEventReopened:
			res.Reopened++
		}
	}
	return res, nil
}

// purposeStateOpen reports whether a commitment in this state is still open.
func purposeStateOpen(state string) bool {
	switch state {
	case PurposeStateCandidate, PurposeStateActive, PurposeStateWaiting:
		return true
	default:
		return false
	}
}

// reminderTransition decides the one event a reminder's state calls for, or
// none.
func reminderTransition(detail PurposeOpenLoopDetail, r calendar.Reminder, open, deleted bool, loc *time.Location) (string, string) {
	state := detail.Current.LifecycleState
	last, wrote, current := lastReminderEvidence(detail)
	if open {
		switch state {
		case PurposeStateCandidate, PurposeStateActive, PurposeStateWaiting:
			if !wrote || last.SourceHash != r.Fingerprint(loc) {
				return PurposeEventRevised, PurposeStanceUpdate
			}
		case PurposeStateResolved, PurposeStateDismissed:
			// Reopen only what the reminder closed: ticked off, then
			// unticked, or deleted and restored.
			if current && (last.Stance == PurposeStanceResolve || last.Stance == PurposeStanceDismiss) {
				return PurposeEventReopened, PurposeStanceSupport
			}
		}
		return "", ""
	}
	switch state {
	case PurposeStateActive, PurposeStateWaiting:
		if deleted {
			return PurposeEventDismissed, PurposeStanceDismiss
		}
		return PurposeEventResolved, PurposeStanceResolve
	case PurposeStateCandidate:
		// A candidate cannot be resolved; a closed reminder dismisses it.
		return PurposeEventDismissed, PurposeStanceDismiss
	}
	return "", ""
}

// closeReminderCommitments dismisses every open commitment made from the
// given reminders. Reading stopped, so they can no longer be kept current;
// the dismissal carries the reminder's own evidence, which is what lets a
// later read reopen the ones still open in Reminders. It returns how many it
// closed, and the first error of one it could not close.
func closeReminderCommitments(store *PurposeMapStore, rows []storage.ReminderRow, now time.Time, loc *time.Location) (int, error) {
	if store == nil {
		return 0, fmt.Errorf("Purpose storage is unavailable")
	}
	if loc == nil {
		loc = time.Local
	}
	details, err := store.ListDetailsAll()
	if err != nil {
		return 0, err
	}
	byID := make(map[string]PurposeOpenLoopDetail, len(details))
	for _, detail := range details {
		byID[detail.OpenLoop.ID] = detail
	}
	occurredAt := now.UnixMilli()
	if wall := time.Now().UnixMilli(); occurredAt > wall {
		occurredAt = wall
	}
	closed := 0
	var firstErr error
	for _, row := range rows {
		detail, ok := byID[reminderPurposeRootID(row.UID)]
		if !ok {
			continue
		}
		if !purposeStateOpen(detail.Current.LifecycleState) {
			continue
		}
		at := occurredAt
		if at < detail.Current.OccurredAt {
			at = detail.Current.OccurredAt
		}
		input := reminderAppendInput(detail, reminderFromRow(row), PurposeEventDismissed, PurposeStanceDismiss, at, loc)
		if _, _, err := store.AppendEvent(input); err != nil {
			log.Printf("[reminders] close a reminder commitment: %v", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		closed++
	}
	return closed, firstErr
}
