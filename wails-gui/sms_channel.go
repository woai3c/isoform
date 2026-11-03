package main

import (
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"wails-gui/internal/engagement"
)

// Text messages through the relay (PROTOCOL.md section 18, notifications
// design section 7a). The user subscribes their own number with a code the
// relay texts them; afterwards the Engagement router can send a time-sensitive
// or needs-you moment as a text when no paired iPhone can be reached. The Mac
// writes every text (the per-kind templates in internal/engagement) and the
// relay sends it, enforcing the opt-out and the caps. Replies come back over
// the hub socket: a reply code answers the moment the last text was about, the
// way the phone's button does, and anything longer is kept in the main chat as
// a texted line, never as the user's message. The sender of a text is
// whoever the phone network says it is, which is weaker than a paired
// iPhone's keys, so a text never starts an agent turn and its words reach the
// model only marked as unverified; the user can make them their own message on
// the Mac.
//
// The relay is not trusted with the subscription either: the reply and state
// frames are not sealed, so only a number confirmed with a code on this Mac
// makes the subscription active here, and without one every relay report of
// "active", every reply and every pushed state is ignored.
//
// This file is the Mac's side: the settings file, the relay calls and the
// reply resolution. The Messages.app self-text in text_channel.go stays the
// fallback for a Mac without a subscription.

const (
	smsChannelFileName    = "sms-channel.json"
	smsChannelFileVersion = 1
	// smsCallTimeout bounds one relay call; the relay may wait on the provider.
	smsCallTimeout = 25 * time.Second
	// smsQuestionTTL is how long a reply code still answers the last text.
	smsQuestionTTL = 24 * time.Hour
	// smsHintInterval is the least time between two "reply with a code" hints.
	// A hint also needs an open question, so hints cost at most one text a day
	// of the subscriber's daily cap.
	smsHintInterval = 24 * time.Hour
	// smsSeenReplies is how many handled reply ids are remembered, so a reply
	// the relay sends again after a dropped connection is handled once.
	smsSeenReplies = 100
	// smsCodeReplyRunes is the longest reply read as a code rather than words.
	smsCodeReplyRunes = 2

	smsStateNone    = "none"
	smsStatePending = "pending_verification"
	smsStateActive  = "active"
	smsStateStopped = "stopped"
)

// Error codes. The relay's refusal codes (PROTOCOL.md 18.2) pass through as
// they are; these are the ones the Mac adds. The settings card maps every
// code to its own sentence, so an error message is always one code.
const (
	smsErrOffline       = "offline"
	smsErrInvalidNumber = "invalid_number"
	smsErrInvalidCode   = "invalid_code"
	smsErrUnknown       = "unknown"
)

// smsNumberPattern is the relay's E.164 check (relay/internal/sms/text.go).
var (
	smsNumberPattern = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)
	smsCodePattern   = regexp.MustCompile(`^[0-9]{6}$`)
)

// smsError is a refusal with the code the settings card translates.
type smsError struct {
	Code       string
	RetryAfter time.Duration
}

func (e *smsError) Error() string { return e.Code }

func smsErrorCode(err error) string {
	var refusal *smsError
	if errors.As(err, &refusal) {
		return refusal.Code
	}
	return smsErrUnknown
}

// SMSChannelStatus is what the settings card shows. Number is the verified
// number and PendingNumber the one a code went to; the relay never returns
// either, so the Mac keeps them. LastError is an error code from the last
// status read, empty when it worked.
type SMSChannelStatus struct {
	Available     bool   `json:"available"`
	State         string `json:"state"`
	Number        string `json:"number"`
	PendingNumber string `json:"pendingNumber"`
	CodeExpiresAt string `json:"codeExpiresAt"`
	SentToday     int    `json:"sentToday"`
	SentThisMonth int    `json:"sentThisMonth"`
	DailyCap      int    `json:"dailyCap"`
	MonthlyCap    int    `json:"monthlyCap"`
	LastError     string `json:"lastError"`
}

// smsQuestion is the moment the last text was about; a reply code answers it.
type smsQuestion struct {
	MomentID string    `json:"momentId"`
	Kind     string    `json:"kind"`
	SentAt   time.Time `json:"sentAt"`
}

// smsChannelFile is sms-channel.json (mode 0600, beside text-channel.json).
type smsChannelFile struct {
	Version       int          `json:"version"`
	Number        string       `json:"number"`
	PendingNumber string       `json:"pendingNumber"`
	State         string       `json:"state"`
	CodeExpiresAt time.Time    `json:"codeExpiresAt"`
	SentToday     int          `json:"sentToday"`
	SentThisMonth int          `json:"sentThisMonth"`
	DailyCap      int          `json:"dailyCap"`
	MonthlyCap    int          `json:"monthlyCap"`
	CheckedAt     time.Time    `json:"checkedAt"`
	LastError     string       `json:"lastError"`
	Question      *smsQuestion `json:"question,omitempty"`
	LastHintAt    time.Time    `json:"lastHintAt"`
	SeenReplies   []string     `json:"seenReplies"`
}

// smsRelayStatus is the relay's status object (PROTOCOL.md 18.2).
type smsRelayStatus struct {
	State         string  `json:"state"`
	CodeExpiresAt *string `json:"codeExpiresAt"`
	SentToday     int     `json:"sentToday"`
	SentThisMonth int     `json:"sentThisMonth"`
	DailyCap      int     `json:"dailyCap"`
	MonthlyCap    int     `json:"monthlyCap"`
}

// smsRelay makes one call to the relay's text routes with the hub credential.
// mobileRelayClient implements it; refusals are *smsError.
type smsRelay interface {
	smsCall(ctx context.Context, method, path string, body any) (smsRelayStatus, error)
}

// smsReplyOutcome is what a reply asks the app to do.
type smsReplyOutcome struct {
	Kind     string // smsReplyAnswer, smsReplyChat, smsReplyHint or smsReplyIgnore
	MomentID string
	Action   string
	Text     string
}

const (
	smsReplyAnswer = "answer"
	smsReplyChat   = "chat"
	smsReplyHint   = "hint"
	smsReplyIgnore = "ignore"
)

// smsChannel holds its hooks as fields so tests use a fake relay and clock.
// callMu keeps relay calls in order; fileMu guards the file's
// read-modify-write and is never held across a call.
type smsChannel struct {
	dir   string
	now   func() time.Time
	relay func() smsRelay
	lang  func() string

	callMu sync.Mutex
	fileMu sync.Mutex
}

func newSMSChannel(dir string) *smsChannel {
	return &smsChannel{
		dir:   dir,
		now:   time.Now,
		relay: func() smsRelay { return nil },
		lang:  func() string { return "en" },
	}
}

var (
	sharedSMSChannelOnce sync.Once
	sharedSMSChannelInst *smsChannel
)

// sharedSMSChannel is the app's text subscription, stored under
// ~/.yansu-agent. The app sets its relay and language hooks at startup.
func sharedSMSChannel() *smsChannel {
	sharedSMSChannelOnce.Do(func() {
		dir, err := textChannelDir()
		if err != nil {
			log.Printf("[sms] no home directory: %v", err)
		}
		sharedSMSChannelInst = newSMSChannel(dir)
	})
	return sharedSMSChannelInst
}

func (c *smsChannel) load() (smsChannelFile, error) {
	if c.dir == "" {
		return smsChannelFile{}, errTextChannelStorage
	}
	raw, err := os.ReadFile(filepath.Join(c.dir, smsChannelFileName))
	if errors.Is(err, os.ErrNotExist) {
		return smsChannelFile{Version: smsChannelFileVersion, State: smsStateNone}, nil
	}
	if err != nil {
		return smsChannelFile{}, err
	}
	var file smsChannelFile
	if err := mobileDecodeStrict(raw, &file); err != nil || file.Version != smsChannelFileVersion {
		return smsChannelFile{}, errors.New("the saved text subscription is invalid")
	}
	return file, nil
}

func (c *smsChannel) save(file smsChannelFile) error {
	if c.dir == "" {
		return errTextChannelStorage
	}
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return err
	}
	file.Version = smsChannelFileVersion
	return writeMobilePrivateJSON(filepath.Join(c.dir, smsChannelFileName), file)
}

// mutate applies change to the file under fileMu and saves it.
func (c *smsChannel) mutate(change func(*smsChannelFile)) (smsChannelFile, error) {
	c.fileMu.Lock()
	defer c.fileMu.Unlock()
	file, err := c.load()
	if err != nil {
		return smsChannelFile{}, err
	}
	change(&file)
	return file, c.save(file)
}

func (c *smsChannel) read() (smsChannelFile, error) {
	c.fileMu.Lock()
	defer c.fileMu.Unlock()
	return c.load()
}

// statusOf turns the file into the card's status.
func (c *smsChannel) statusOf(file smsChannelFile) SMSChannelStatus {
	status := SMSChannelStatus{
		Available:     c.relay() != nil,
		State:         file.State,
		Number:        file.Number,
		PendingNumber: file.PendingNumber,
		SentToday:     file.SentToday,
		SentThisMonth: file.SentThisMonth,
		DailyCap:      file.DailyCap,
		MonthlyCap:    file.MonthlyCap,
		LastError:     file.LastError,
	}
	if status.State == "" {
		status.State = smsStateNone
	}
	if !file.CodeExpiresAt.IsZero() && file.CodeExpiresAt.After(c.now()) {
		status.CodeExpiresAt = file.CodeExpiresAt.UTC().Format(time.RFC3339)
	}
	return status
}

// applyRelay records what the relay reported. A subscription the relay no
// longer has drops the verified number and the open question; a code the
// relay no longer has drops the pending number. A subscription the relay
// reports for a Mac that confirmed no number is not believed: only Confirm
// sets the number, and without it the state is pending or none.
func applyRelay(file *smsChannelFile, status smsRelayStatus, now time.Time) {
	file.State = status.State
	file.SentToday, file.SentThisMonth = status.SentToday, status.SentThisMonth
	file.DailyCap, file.MonthlyCap = status.DailyCap, status.MonthlyCap
	file.CheckedAt = now
	file.LastError = ""
	file.CodeExpiresAt = time.Time{}
	if status.CodeExpiresAt != nil {
		if expires, err := time.Parse(time.RFC3339, *status.CodeExpiresAt); err == nil {
			file.CodeExpiresAt = expires
		}
	}
	if file.CodeExpiresAt.IsZero() {
		file.PendingNumber = ""
	}
	switch status.State {
	case smsStateNone, smsStatePending:
		file.Number = ""
		file.Question = nil
	case smsStateActive, smsStateStopped:
		if file.Number == "" {
			file.State = smsStateNone
			if !file.CodeExpiresAt.IsZero() {
				file.State = smsStatePending
			}
			file.Question = nil
		}
	default:
		file.State = smsStateNone
	}
}

// call runs one relay call in order with the others. Without a relay the
// connection is off, which the card reports as offline.
func (c *smsChannel) call(ctx context.Context, method, path string, body any) (smsRelayStatus, error) {
	relay := c.relay()
	if relay == nil {
		return smsRelayStatus{}, &smsError{Code: smsErrOffline}
	}
	ctx, cancel := context.WithTimeout(ctx, smsCallTimeout)
	defer cancel()
	return relay.smsCall(ctx, method, path, body)
}

// Status reports the saved subscription without asking the relay.
func (c *smsChannel) Status() (SMSChannelStatus, error) {
	file, err := c.read()
	return c.statusOf(file), err
}

// Active reports whether texts can go out now: a number this Mac confirmed,
// active on the relay, and a relay connection to send through. It is the
// router's SMSActive.
func (c *smsChannel) Active() bool {
	file, err := c.read()
	return err == nil && file.State == smsStateActive && file.Number != "" && c.relay() != nil
}

// Refresh reads the subscription from the relay and saves it. When the relay
// cannot be asked, the saved status comes back with the reason in LastError.
func (c *smsChannel) Refresh(ctx context.Context) (SMSChannelStatus, error) {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	relayStatus, err := c.call(ctx, "GET", "/v1/hub/sms/subscription", nil)
	if err != nil {
		file, readErr := c.read()
		status := c.statusOf(file)
		status.LastError = smsErrorCode(err)
		return status, readErr
	}
	file, err := c.mutate(func(f *smsChannelFile) { applyRelay(f, relayStatus, c.now()) })
	return c.statusOf(file), err
}

// Start asks the relay to text a code to the number. An active subscription
// keeps working until the new number is confirmed. The Mac first turns texts
// on for its hub (the relay's entitlement stub), and when it has confirmed no
// number itself it first clears whatever the relay holds for its hub, so a
// subscription this Mac lost track of is verified again with a code.
func (c *smsChannel) Start(ctx context.Context, raw string) (SMSChannelStatus, error) {
	number := strings.NewReplacer(" ", "", "-", "", ".", "", "(", "", ")", "", " ", "").Replace(strings.TrimSpace(raw))
	if !smsNumberPattern.MatchString(number) {
		return SMSChannelStatus{}, &smsError{Code: smsErrInvalidNumber}
	}
	c.callMu.Lock()
	defer c.callMu.Unlock()
	saved, err := c.read()
	if err != nil {
		return SMSChannelStatus{}, err
	}
	if _, err = c.call(ctx, "POST", "/v1/hub/sms/enable", nil); err != nil {
		return SMSChannelStatus{}, err
	}
	if saved.Number == "" {
		if _, err = c.call(ctx, "DELETE", "/v1/hub/sms/subscription", nil); err != nil {
			return SMSChannelStatus{}, err
		}
	}
	relayStatus, err := c.call(ctx, "POST", "/v1/hub/sms/subscription", map[string]string{"number": number, "lang": c.lang()})
	if err != nil {
		return SMSChannelStatus{}, err
	}
	file, err := c.mutate(func(f *smsChannelFile) {
		previousNumber, previousPending := f.Number, f.PendingNumber
		applyRelay(f, relayStatus, c.now())
		switch {
		case f.State == smsStateActive && number == previousNumber:
			// The relay sends no code for the number already subscribed; a
			// code still out is the one for the earlier pending number.
			if !f.CodeExpiresAt.IsZero() {
				f.PendingNumber = previousPending
			}
		case !f.CodeExpiresAt.IsZero():
			f.PendingNumber = number
		}
	})
	return c.statusOf(file), err
}

// Confirm sends the code the user typed. A right code makes the pending
// number the subscribed one; this is the only place the number is set.
func (c *smsChannel) Confirm(ctx context.Context, code string) (SMSChannelStatus, error) {
	code = strings.TrimSpace(code)
	if !smsCodePattern.MatchString(code) {
		return SMSChannelStatus{}, &smsError{Code: smsErrInvalidCode}
	}
	c.callMu.Lock()
	defer c.callMu.Unlock()
	pending, err := c.read()
	if err != nil {
		return SMSChannelStatus{}, err
	}
	relayStatus, err := c.call(ctx, "POST", "/v1/hub/sms/subscription/confirm", map[string]string{"code": code})
	if err != nil {
		return SMSChannelStatus{}, err
	}
	file, err := c.mutate(func(f *smsChannelFile) {
		if relayStatus.State == smsStateActive && pending.PendingNumber != "" {
			f.Number = pending.PendingNumber
		}
		applyRelay(f, relayStatus, c.now())
		f.PendingNumber = ""
		f.Question = nil
	})
	return c.statusOf(file), err
}

// Stop removes the subscription on the relay and forgets the number here.
func (c *smsChannel) Stop(ctx context.Context) (SMSChannelStatus, error) {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	relayStatus, err := c.call(ctx, "DELETE", "/v1/hub/sms/subscription", nil)
	if err != nil {
		return SMSChannelStatus{}, err
	}
	file, err := c.mutate(func(f *smsChannelFile) {
		applyRelay(f, relayStatus, c.now())
		f.Number, f.PendingNumber, f.Question = "", "", nil
	})
	return c.statusOf(file), err
}

// SendMoment texts one moment and remembers a code question only when the
// sent text offers reply codes. A refusal means the Engagement service holds the
// moment and retries; a refusal that means the subscription changed (STOP,
// gone) is saved first, so the router stops choosing texts.
func (c *smsChannel) SendMoment(ctx context.Context, momentID string, kind engagement.Kind, params map[string]string) error {
	m, allowed := sanitizeEngagementMoment(engagement.Moment{Kind: kind, Params: params})
	if !allowed {
		return errors.New("sensitive source cannot be delivered")
	}
	lang := c.lang()
	body, ok := engagement.TextMessage(kind, lang, m.Params)
	if !ok {
		return errors.New("no text template for " + string(kind))
	}
	if !engagement.ValidBody(body) {
		return c.SendGeneric(ctx, momentID, kind)
	}
	return c.sendBody(ctx, momentID, kind, body, engagement.TextOffersReplies(kind, lang, body))
}

// SendGeneric texts the kind's generic line, which names nothing, for a
// moment whose text would name what the user chose to keep out of texts.
func (c *smsChannel) SendGeneric(ctx context.Context, momentID string, kind engagement.Kind) error {
	body, ok := engagement.GenericText(kind, c.lang())
	if !ok {
		return errors.New("no generic line for " + string(kind))
	}
	return c.sendBody(ctx, momentID, kind, body, false)
}

func (c *smsChannel) sendBody(ctx context.Context, momentID string, kind engagement.Kind, body string, offersReplies bool) error {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	if file, err := c.read(); err != nil || file.Number == "" {
		return &smsError{Code: "not_active"}
	}
	relayStatus, err := c.call(ctx, "POST", "/v1/hub/sms/send", map[string]string{"body": body})
	if err != nil {
		code := smsErrorCode(err)
		if _, saveErr := c.mutate(func(f *smsChannelFile) {
			switch code {
			case "opted_out":
				f.State = smsStateStopped
			case "not_active":
				f.State, f.Number, f.Question = smsStateNone, "", nil
			}
		}); saveErr != nil {
			log.Printf("[sms] recording a refused text: %v", saveErr)
		}
		return err
	}
	if _, err := c.mutate(func(f *smsChannelFile) {
		applyRelay(f, relayStatus, c.now())
		f.Question = nil
		if offersReplies {
			f.Question = &smsQuestion{MomentID: momentID, Kind: string(kind), SentAt: c.now()}
		}
	}); err != nil {
		// The text went out; a failed save must not make the service send it again.
		log.Printf("[sms] recording a sent text: %v", err)
	}
	return nil
}

// HandleReply resolves one reply the relay forwarded. Without a number this
// Mac confirmed, every reply is ignored: the frame comes from the relay
// unsealed, so it proves nothing about a subscription. A reply id seen before
// is ignored. A reply code for the last text, sent within a day, answers that
// moment once; a short reply that is no code gets a hint while that question
// is open, at most once a day; anything longer is words for the main chat. It
// returns an error only when the outcome could not be saved, so the reply is
// not acknowledged and the relay sends it again.
func (c *smsChannel) HandleReply(id, body string) (smsReplyOutcome, error) {
	outcome := smsReplyOutcome{Kind: smsReplyIgnore}
	_, err := c.mutate(func(f *smsChannelFile) {
		if f.Number == "" {
			return
		}
		for _, seen := range f.SeenReplies {
			if seen == id {
				return
			}
		}
		f.SeenReplies = append(f.SeenReplies, id)
		if len(f.SeenReplies) > smsSeenReplies {
			f.SeenReplies = f.SeenReplies[len(f.SeenReplies)-smsSeenReplies:]
		}
		text := strings.TrimSpace(body)
		now := c.now()
		q := f.Question
		open := q != nil && len(engagement.TextReplies(engagement.Kind(q.Kind))) > 0 && now.Sub(q.SentAt) < smsQuestionTTL
		if open {
			if action, ok := engagement.MatchTextReply(engagement.Kind(q.Kind), text); ok {
				outcome = smsReplyOutcome{Kind: smsReplyAnswer, MomentID: q.MomentID, Action: action}
				f.Question = nil
				return
			}
		}
		switch {
		case text == "":
		case utf8.RuneCountInString(text) <= smsCodeReplyRunes:
			if open && now.Sub(f.LastHintAt) >= smsHintInterval {
				outcome = smsReplyOutcome{Kind: smsReplyHint}
			}
		default:
			outcome = smsReplyOutcome{Kind: smsReplyChat, Text: text}
		}
	})
	return outcome, err
}

// ForgetReply drops a reply id from the handled ones, so a reply whose
// handling failed is handled when the relay sends it again.
func (c *smsChannel) ForgetReply(id string) error {
	_, err := c.mutate(func(f *smsChannelFile) {
		kept := f.SeenReplies[:0]
		for _, seen := range f.SeenReplies {
			if seen != id {
				kept = append(kept, seen)
			}
		}
		f.SeenReplies = kept
	})
	return err
}

// SendHint texts the one line that says how to answer, at most once a day:
// the day counts from the last hint that went out, so a failed one does not
// hold back the next.
func (c *smsChannel) SendHint(ctx context.Context) error {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	file, err := c.read()
	if err != nil {
		return err
	}
	if file.Number == "" || c.now().Sub(file.LastHintAt) < smsHintInterval {
		return nil
	}
	relayStatus, err := c.call(ctx, "POST", "/v1/hub/sms/send", map[string]string{"body": engagement.TextHint(c.lang())})
	if err != nil {
		return err
	}
	_, err = c.mutate(func(f *smsChannelFile) {
		applyRelay(f, relayStatus, c.now())
		f.LastHintAt = c.now()
	})
	return err
}

// ApplyState records a state the relay pushed (a STOP or START texted from the
// phone, or another Mac confirming the number). Without a number this Mac
// confirmed it changes nothing, so a pushed "active" cannot switch texts on.
func (c *smsChannel) ApplyState(state string) error {
	_, err := c.mutate(func(f *smsChannelFile) {
		if f.Number == "" {
			return
		}
		switch state {
		case smsStateActive, smsStateStopped:
			f.State = state
		case smsStatePending:
			f.State, f.Number, f.Question = state, "", nil
		default:
			f.State, f.Number, f.Question = smsStateNone, "", nil
		}
	})
	return err
}
