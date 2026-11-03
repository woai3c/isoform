package calendar

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// Reminder is one EventKit reminder after normalisation. Due is zero when
// the reminder has no due date; DueAllDay marks a due day without a time, in
// which case Due is local midnight of that day.
type Reminder struct {
	// UID is the calendarItemExternalIdentifier, or the local identifier
	// when the external one is empty.
	UID     string
	LocalID string
	Title   string
	ListID  string
	// List is the title of the Reminders list the reminder is in.
	List      string
	Due       time.Time
	DueAllDay bool
	// Priority follows EventKit: 0 none, 1 to 4 high, 5 medium, 6 to 9 low.
	Priority    int
	Completed   bool
	CompletedAt time.Time
}

// RawReminder is one reminder as the EventKit bridge serialises it. Due is
// an RFC 3339 time when the reminder has a due time; DueDay is a local
// "2006-01-02" day when it has a due day only.
type RawReminder struct {
	UID         string `json:"uid"`
	LocalID     string `json:"localId"`
	Title       string `json:"title"`
	ListID      string `json:"listId"`
	ListTitle   string `json:"listTitle"`
	Due         string `json:"due"`
	DueDay      string `json:"dueDay"`
	Priority    int    `json:"priority"`
	Completed   bool   `json:"completed"`
	CompletedAt string `json:"completedAt"`
}

// Priority bands of a reminder, as EventKit defines them.
const (
	ReminderPriorityNone   = "none"
	ReminderPriorityHigh   = "high"
	ReminderPriorityMedium = "medium"
	ReminderPriorityLow    = "low"
)

// NormalizeReminder turns one bridge record into a Reminder, reading a due
// day in loc. It returns false when the record has no identity or no title:
// an untitled reminder says nothing Isoform could act on.
func NormalizeReminder(raw RawReminder, loc *time.Location) (Reminder, bool) {
	if loc == nil {
		loc = time.Local
	}
	uid := strings.TrimSpace(raw.UID)
	if uid == "" {
		uid = strings.TrimSpace(raw.LocalID)
	}
	title := cleanText(raw.Title)
	if uid == "" || title == "" {
		return Reminder{}, false
	}
	r := Reminder{
		UID:       uid,
		LocalID:   strings.TrimSpace(raw.LocalID),
		Title:     title,
		ListID:    strings.TrimSpace(raw.ListID),
		List:      cleanText(raw.ListTitle),
		Priority:  raw.Priority,
		Completed: raw.Completed,
	}
	if r.Priority < 0 || r.Priority > 9 {
		r.Priority = 0
	}
	if due, ok := parseTime(raw.Due); ok {
		r.Due = due
	} else if day, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(raw.DueDay), loc); err == nil {
		r.Due = day
		r.DueAllDay = true
	}
	if r.Completed {
		if at, ok := parseTime(raw.CompletedAt); ok {
			r.CompletedAt = at
		}
	}
	return r, true
}

// PriorityBand names the band of an EventKit priority.
func PriorityBand(priority int) string {
	switch {
	case priority >= 1 && priority <= 4:
		return ReminderPriorityHigh
	case priority == 5:
		return ReminderPriorityMedium
	case priority >= 6 && priority <= 9:
		return ReminderPriorityLow
	default:
		return ReminderPriorityNone
	}
}

// HasDue reports whether the reminder has a due day or time.
func (r Reminder) HasDue() bool { return !r.Due.IsZero() }

// DueValue renders the due date the way the Purpose Map stores it: a bare
// "2006-01-02" day for a due day without a time (read as the end of that
// local day), RFC 3339 otherwise. It is empty without a due date.
func (r Reminder) DueValue(loc *time.Location) string {
	if r.Due.IsZero() {
		return ""
	}
	if loc == nil {
		loc = time.Local
	}
	if r.DueAllDay {
		return r.Due.In(loc).Format("2006-01-02")
	}
	return r.Due.UTC().Format(time.RFC3339)
}

// DueBy is when the reminder counts as overdue: the due time, or the end of
// the due day for a due day without a time.
func (r Reminder) DueBy(loc *time.Location) time.Time {
	if r.Due.IsZero() {
		return time.Time{}
	}
	if !r.DueAllDay {
		return r.Due
	}
	if loc == nil {
		loc = time.Local
	}
	day := r.Due.In(loc)
	return time.Date(day.Year(), day.Month(), day.Day()+1, 0, 0, 0, 0, loc)
}

// Overdue reports whether an open reminder is past its due time at now.
func (r Reminder) Overdue(now time.Time, loc *time.Location) bool {
	if r.Completed || r.Due.IsZero() {
		return false
	}
	return now.After(r.DueBy(loc))
}

// Fingerprint hashes what a commitment made from the reminder carries: the
// title and the due value. A different fingerprint means the commitment needs
// a revision.
func (r Reminder) Fingerprint(loc *time.Location) string {
	sum := sha256.Sum256([]byte(r.Title + "\x00" + r.DueValue(loc)))
	return hex.EncodeToString(sum[:])
}
