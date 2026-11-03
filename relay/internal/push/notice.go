package push

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
	"isoform-relay/internal/wire"
)

// Interruption levels are the APNs values the hub chooses per moment.
const (
	// LevelPassive is delivered quietly, at a time APNs picks.
	LevelPassive = "passive"
	// LevelActive is delivered now with the default sound.
	LevelActive = "active"
	// LevelTimeSensitive is delivered now and may break a Focus.
	LevelTimeSensitive = "time-sensitive"
)

// Categories name the notification actions the phone registers.
const (
	// CategoryDecision carries Approve, Decline, Snooze and Open.
	CategoryDecision = "YANSU_DECISION"
	// CategoryMoment carries Open and Later.
	CategoryMoment = "YANSU_MOMENT"
	// CategoryBrief carries Open.
	CategoryBrief = "YANSU_BRIEF"
)

const (
	// MaxNoticeCT bounds the sealed notice text in characters. The sealed text
	// plus the fixed routing keys keeps each APNs payload under MaxPayloadBytes.
	MaxNoticeCT = 3500
	// MaxPayloadBytes is the APNs payload limit.
	MaxPayloadBytes = 4096
	// maxCollapseID is the APNs limit for apns-collapse-id.
	maxCollapseID = 64
)

// ErrInvalidNotice reports notice fields that break the protocol. The message
// is static so it never echoes content.
var ErrInvalidNotice = errors.New("notice fields are not valid")

var (
	momentPattern   = regexp.MustCompile(`^[a-z][a-z_]{0,39}$`)
	collapsePattern = regexp.MustCompile(`^[!-~]{1,64}$`)
)

// Notice is one sealed moment queued for a device. The relay stores and sends
// these fields as given and never opens CT. ID is the notice id: the phone
// reads it from the APNs payload to open CT and to post receipts. It is not the
// APNs notification id, which stays a relay-generated value.
type Notice struct {
	ID       string
	Moment   string
	CT       string
	Category string
	Collapse string
	Level    string
}

// Prepare checks the notice and returns a copy with the default collapse id,
// which is the notice id. An empty collapse id means the default.
func (n Notice) Prepare() (Notice, error) {
	if !wire.ValidUUID(n.ID) || !momentPattern.MatchString(n.Moment) || !validNoticeCT(n.CT) {
		return Notice{}, ErrInvalidNotice
	}
	switch n.Category {
	case CategoryDecision, CategoryMoment, CategoryBrief:
	default:
		return Notice{}, ErrInvalidNotice
	}
	switch n.Level {
	case LevelPassive, LevelActive, LevelTimeSensitive:
	default:
		return Notice{}, ErrInvalidNotice
	}
	if n.Collapse == "" {
		n.Collapse = n.ID
	}
	if !collapsePattern.MatchString(n.Collapse) || len(n.Collapse) > maxCollapseID {
		return Notice{}, ErrInvalidNotice
	}
	return n, nil
}

// validNoticeCT accepts unpadded base64url within MaxNoticeCT characters. The
// relay cannot check the AEAD tag, so it checks only the shape and the size.
func validNoticeCT(ct string) bool {
	return len(ct) <= MaxNoticeCT && wire.ValidCiphertext(ct, MaxNoticeCT)
}

// EnqueueNotice queues one moment push for a device with a push registration.
// Like Enqueue, a device without a registration gets no row and no error.
func EnqueueNotice(ctx context.Context, q Querier, deviceID, eventID string, n Notice, now time.Time) error {
	prepared, err := n.Prepare()
	if err != nil {
		return err
	}
	var registered int
	if err = q.QueryRowContext(ctx, "SELECT count(*) FROM push_devices WHERE device_id=?", deviceID).Scan(&registered); err != nil {
		return err
	}
	if registered == 0 {
		return nil
	}
	_, err = q.ExecContext(ctx, `INSERT INTO push_outbox(device_id,event_id,notification_id,kind,state,created_at,expires_at,next_at,moment_kind,notice_id,ct,category,collapse_id,level)
	VALUES(?,?,?,?,'queued',?,?,?,?,?,?,?,?,?) ON CONFLICT(device_id,event_id) DO NOTHING`,
		deviceID, eventID, uuid.NewString(), KindMoment, now.UnixMilli(), now.Add(Expiry).UnixMilli(), now.UnixMilli(),
		prepared.Moment, prepared.ID, prepared.CT, prepared.Category, prepared.Collapse, prepared.Level)
	return err
}
