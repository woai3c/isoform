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
	"unicode"
	"unicode/utf8"
)

// The text channel is the last resort of the Engagement router: when a
// time-sensitive moment cannot reach the phone, the Mac texts the user's own
// number through Messages. This file is platform independent (settings, caps,
// copy). The sender and the Messages permission live in text_channel_darwin.go
// and text_channel_other.go.

const (
	textChannelConfigFileName = "text-channel.json"
	textChannelStateFileName  = "text-channel-state.json"
	textChannelFileVersion    = 1
	textChannelDailyLimit     = 3
	textChannelCooldown       = 2 * time.Hour
	textChannelTestInterval   = time.Minute
	textChannelBodyLimit      = 160
	textChannelSignOff        = "Open Isoform on your computer."
	textChannelTestBody       = "This is a test from Isoform. Open Isoform on your computer."
	textChannelDayLayout      = "2006-01-02"
	textChannelEllipsis       = "..."
)

// textChannelNumberPattern is E.164: a plus sign and 8 to 15 digits.
var textChannelNumberPattern = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)

var (
	errTextChannelStorage     = errors.New("text message settings are unavailable")
	errTextChannelOff         = errors.New("text messages are off")
	errTextChannelNoNumber    = errors.New("enter a phone number before turning on text messages")
	errTextChannelNoConsent   = errors.New("accept the text message terms before turning on text messages")
	errTextChannelUnsupported = errors.New("text messages are not available on this computer")
	errTextMessagesNotAllowed = errors.New("automation for Messages is not allowed")
	errTextChannelDailyLimit  = errors.New("the daily text limit is reached")
	errTextChannelCooldown    = errors.New("a text was sent recently")
	errTextChannelTestSoon    = errors.New("wait a minute before sending another test text")
	errTextChannelNumber      = errors.New("the phone number must be in international form, such as +14155550123")
	errTextChannelEmptyCopy   = errors.New("there is no text to send")
	errTextChannelUnverified  = errors.New("verify your phone number before turning on text messages")
	errTextChannelPaused      = errors.New("text messages are paused; send a test from Settings")
	errTextMessagesSignedOut  = errors.New("Messages is not signed in on this computer")
)

// TextChannelConfig is text-channel.json. The number is E.164. Consent is the
// time the user accepted the terms for this number; changing the number
// without accepting again clears it.
type TextChannelConfig struct {
	Enabled           bool      `json:"enabled"`
	Number            string    `json:"number"`
	IncludeNames      bool      `json:"includeNames"`
	ConsentAcceptedAt time.Time `json:"consentAcceptedAt"`
	VerifiedAt        time.Time `json:"verifiedAt,omitempty"`
	Version           int       `json:"version"`
}

// textChannelState is text-channel-state.json: the caps, the cooldown and the
// last answer macOS gave for Messages automation. Moment texts count toward
// the daily cap and the cooldown; test texts do not.
type textChannelState struct {
	Version               int       `json:"version"`
	Day                   string    `json:"day"`
	SentToday             int       `json:"sentToday"`
	LastMomentAt          time.Time `json:"lastMomentAt"`
	LastTestAt            time.Time `json:"lastTestAt"`
	LastSentAt            time.Time `json:"lastSentAt"`
	LastError             string    `json:"lastError"`
	MessagesAllowed       bool      `json:"messagesAllowed"`
	SendBlocked           bool      `json:"sendBlocked,omitempty"`
	VerificationNumber    string    `json:"verificationNumber,omitempty"`
	VerificationHash      []byte    `json:"verificationHash,omitempty"`
	VerificationSalt      []byte    `json:"verificationSalt,omitempty"`
	VerificationExpiresAt time.Time `json:"verificationExpiresAt,omitempty"`
	VerificationAttempts  int       `json:"verificationAttempts,omitempty"`
	LastVerificationAt    time.Time `json:"lastVerificationAt,omitempty"`
}

// TextChannelStatus is what the settings card shows.
type TextChannelStatus struct {
	Supported         bool   `json:"supported"`
	Enabled           bool   `json:"enabled"`
	Number            string `json:"number"`
	IncludeNames      bool   `json:"includeNames"`
	ConsentAccepted   bool   `json:"consentAccepted"`
	PermissionGranted bool   `json:"permissionGranted"`
	SentToday         int    `json:"sentToday"`
	LastSentAt        string `json:"lastSentAt"`
	LastError         string `json:"lastError"`
	Verified          bool   `json:"verified"`
	Verifying         bool   `json:"verifying"`
}

// TextChannelInput is what the settings card sends. AcceptConsent records the
// consent time for Number.
type TextChannelInput struct {
	Enabled       bool   `json:"enabled"`
	Number        string `json:"number"`
	IncludeNames  bool   `json:"includeNames"`
	AcceptConsent bool   `json:"acceptConsent"`
}

// textSender delivers one text. Implementations must not log the number or
// the body, and their errors are shown to the user, so they must not carry
// either one.
type textSender interface {
	Send(ctx context.Context, to, body string) error
}

// textAutomationState is macOS's answer about Yansu controlling Messages.
// Unknown means Messages was not running, so macOS could not answer.
type textAutomationState int

const (
	textAutomationUnknown textAutomationState = iota
	textAutomationAllowed
	textAutomationNotAllowed
)

// textChannel holds the hooks as fields so tests can use a fake clock, a fake
// sender and a fake permission without touching macOS.
type textChannel struct {
	dir        string
	now        func() time.Time
	sender     textSender
	supported  func() bool
	automation func(askUser bool) textAutomationState
	lang       func() string

	// sendMu serializes sends so the caps hold under concurrent callers.
	// stateMu guards the read-modify-write of the state file; it is held
	// briefly and never across a send, so status reads stay responsive.
	sendMu  sync.Mutex
	stateMu sync.Mutex
}

func newTextChannel(dir string) *textChannel {
	return &textChannel{
		dir:        dir,
		now:        time.Now,
		sender:     defaultTextSender(),
		supported:  textSenderSupported,
		automation: textMessagesAutomation,
	}
}

// textChannelDir is ~/.yansu-agent, next to the other Yansu settings files.
func textChannelDir() (string, error) {
	dataRoot, err := flavorDataDir()
	if err != nil {
		return "", err
	}
	return dataRoot, nil
}

func loadTextChannelConfig(dir string) (TextChannelConfig, error) {
	if dir == "" {
		return TextChannelConfig{}, errTextChannelStorage
	}
	raw, err := os.ReadFile(filepath.Join(dir, textChannelConfigFileName))
	if errors.Is(err, os.ErrNotExist) {
		return TextChannelConfig{Version: textChannelFileVersion}, nil
	}
	if err != nil {
		return TextChannelConfig{}, err
	}
	var cfg TextChannelConfig
	if err := mobileDecodeStrict(raw, &cfg); err != nil || cfg.Version != textChannelFileVersion {
		return TextChannelConfig{}, errors.New("the saved text message settings are invalid")
	}
	return cfg, nil
}

// saveTextChannelConfig writes the file with mode 0600, like the other mobile
// settings files.
func saveTextChannelConfig(dir string, cfg TextChannelConfig) error {
	if dir == "" {
		return errTextChannelStorage
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	cfg.Version = textChannelFileVersion
	return writeMobilePrivateJSON(filepath.Join(dir, textChannelConfigFileName), cfg)
}

func loadTextChannelState(dir string) (textChannelState, error) {
	if dir == "" {
		return textChannelState{}, errTextChannelStorage
	}
	raw, err := os.ReadFile(filepath.Join(dir, textChannelStateFileName))
	if errors.Is(err, os.ErrNotExist) {
		return textChannelState{Version: textChannelFileVersion}, nil
	}
	if err != nil {
		return textChannelState{}, err
	}
	var state textChannelState
	if err := mobileDecodeStrict(raw, &state); err != nil || state.Version != textChannelFileVersion {
		return textChannelState{}, errors.New("the saved text message state is invalid")
	}
	return state, nil
}

func saveTextChannelState(dir string, state textChannelState) error {
	if dir == "" {
		return errTextChannelStorage
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	state.Version = textChannelFileVersion
	return writeMobilePrivateJSON(filepath.Join(dir, textChannelStateFileName), state)
}

func textChannelDay(now time.Time) string {
	return now.Format(textChannelDayLayout)
}

// sentOn is the number of moment texts sent on the local day of now.
func (s textChannelState) sentOn(now time.Time) int {
	if s.Day != textChannelDay(now) {
		return 0
	}
	return s.SentToday
}

// nextTextChannelConfig applies a settings change. It rejects an invalid
// number, enabling without a number, and enabling without consent. Consent
// belongs to one number, so a changed number clears it unless the same call
// accepts consent for the new number.
func nextTextChannelConfig(cfg TextChannelConfig, input TextChannelInput, now time.Time) (TextChannelConfig, error) {
	number := strings.TrimSpace(input.Number)
	if number != "" && !textChannelNumberPattern.MatchString(number) {
		return cfg, errTextChannelNumber
	}
	if input.Enabled && number == "" {
		return cfg, errTextChannelNoNumber
	}
	next := cfg
	next.Enabled = input.Enabled
	next.IncludeNames = input.IncludeNames
	if number != cfg.Number {
		next.ConsentAcceptedAt = time.Time{}
		next.VerifiedAt = time.Time{}
	}
	next.Number = number
	if input.AcceptConsent && number != "" {
		next.ConsentAcceptedAt = now
	}
	if input.Enabled && next.ConsentAcceptedAt.IsZero() {
		return cfg, errTextChannelNoConsent
	}
	if input.Enabled && next.VerifiedAt.IsZero() {
		return cfg, errTextChannelUnverified
	}
	return next, nil
}

// textChannelBody builds the text. The generic copy is the default. The title
// (which can name people and work) is used only when the user allowed names.
// The body never carries the moment's params or any other raw content.
func textChannelBody(text engagementText, includeNames bool) (string, error) {
	lead := text.PushBody
	if includeNames && strings.TrimSpace(text.Title) != "" {
		lead = text.Title
	}
	lead = sanitizeTextLead(lead)
	if lead == "" {
		return "", errTextChannelEmptyCopy
	}
	return fitTextBody(lead), nil
}

// sanitizeTextLead turns control characters into spaces and collapses runs of
// whitespace, so a text is one line.
func sanitizeTextLead(s string) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	return strings.Join(strings.Fields(clean), " ")
}

// fitTextBody joins the lead and the sign-off. A lead that would push the text
// past the limit is cut at a rune boundary and ends with an ellipsis, and the
// sign-off is always kept.
func fitTextBody(lead string) string {
	full := lead + " " + textChannelSignOff
	if utf8.RuneCountInString(full) <= textChannelBodyLimit {
		return full
	}
	room := textChannelBodyLimit - utf8.RuneCountInString(textChannelEllipsis) - 1 - utf8.RuneCountInString(textChannelSignOff)
	head := strings.TrimRight(string([]rune(lead)[:room]), " ")
	return head + textChannelEllipsis + " " + textChannelSignOff
}

// Available reports whether the channel can send right now. It is the
// Engagement router's TextAvailable.
func (c *textChannel) Available() bool {
	_, err := c.usable()
	state, stateErr := c.readState()
	return err == nil && stateErr == nil && !state.SendBlocked
}

// NamesAllowed reports whether the user lets texts name things (titles,
// people, results). It is off by default and while Messages is disabled.
func (c *textChannel) NamesAllowed() bool {
	if c == nil {
		return false
	}
	cfg, err := loadTextChannelConfig(c.dir)
	return err == nil && cfg.Enabled && cfg.IncludeNames
}

// usable checks each precondition in order and names the first that fails.
func (c *textChannel) usable() (TextChannelConfig, error) {
	cfg, err := loadTextChannelConfig(c.dir)
	if err != nil {
		return cfg, err
	}
	if !cfg.Enabled {
		return cfg, errTextChannelOff
	}
	if cfg.Number == "" {
		return cfg, errTextChannelNoNumber
	}
	if cfg.ConsentAcceptedAt.IsZero() {
		return cfg, errTextChannelNoConsent
	}
	if cfg.VerifiedAt.IsZero() {
		return cfg, errTextChannelUnverified
	}
	if !c.supported() {
		return cfg, errTextChannelUnsupported
	}
	if !c.automationAllowed(false) {
		return cfg, errTextMessagesNotAllowed
	}
	return cfg, nil
}

// Status reports the settings and the recent state.
func (c *textChannel) Status() (TextChannelStatus, error) {
	cfg, cfgErr := loadTextChannelConfig(c.dir)
	state, stateErr := c.readState()
	status := TextChannelStatus{
		Supported:         c.supported(),
		Enabled:           cfg.Enabled,
		Number:            cfg.Number,
		IncludeNames:      cfg.IncludeNames,
		ConsentAccepted:   !cfg.ConsentAcceptedAt.IsZero(),
		PermissionGranted: state.MessagesAllowed,
		SentToday:         state.sentOn(c.now()),
		LastError:         state.LastError,
		Verified:          !cfg.VerifiedAt.IsZero(),
		Verifying:         state.VerificationNumber == cfg.Number && state.VerificationExpiresAt.After(c.now()) && len(state.VerificationHash) > 0 && state.VerificationAttempts < 5,
	}
	if cfg.Enabled && !status.Verified {
		status.LastError = errTextChannelUnverified.Error()
	} else if cfg.Enabled && !status.PermissionGranted {
		status.LastError = errTextMessagesNotAllowed.Error()
	}
	if !state.LastSentAt.IsZero() {
		status.LastSentAt = state.LastSentAt.Format(time.RFC3339)
	}
	return status, errors.Join(cfgErr, stateErr)
}

// SetConfig validates and saves the settings, then returns the new status. A
// file that cannot be read is replaced, and that drops consent, so the user
// has to accept the terms again.
func (c *textChannel) SetConfig(input TextChannelInput) (TextChannelStatus, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	cfg, err := loadTextChannelConfig(c.dir)
	if err != nil {
		cfg = TextChannelConfig{}
	}
	next, err := nextTextChannelConfig(cfg, input, c.now())
	if err != nil {
		return TextChannelStatus{}, err
	}
	if next.Number != cfg.Number {
		if err := c.mutateState(func(s *textChannelState) bool {
			s.VerificationHash = nil
			s.LastError = ""
			return true
		}); err != nil {
			return TextChannelStatus{}, err
		}
	}
	if err := saveTextChannelConfig(c.dir, next); err != nil {
		return TextChannelStatus{}, err
	}
	return c.Status()
}

// SendMoment sends one moment text. Refusals (caps, cooldown, settings) and
// send failures pause the channel until an explicit test succeeds. Caps and
// cooldowns can wait; setup and send failures must not retry. After a successful send it never returns an error, so a
// failed record write cannot cause a second text.
func (c *textChannel) SendMoment(ctx context.Context, text engagementText) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	cfg, err := c.usable()
	if err != nil {
		return permanentTextFailure(err)
	}
	now := c.now()
	state, err := c.readState()
	if err != nil {
		return err
	}
	if state.SendBlocked {
		return permanentTextFailure(errTextChannelPaused)
	}
	if state.sentOn(now) >= textChannelDailyLimit {
		return errTextChannelDailyLimit
	}
	if !state.LastMomentAt.IsZero() && now.Sub(state.LastMomentAt) < textChannelCooldown {
		return errTextChannelCooldown
	}
	body, err := textChannelBody(text, cfg.IncludeNames)
	if err != nil {
		return err
	}
	if c.chinese() {
		body = strings.TrimSuffix(body, textChannelSignOff) + "在电脑上打开 Isoform。"
	}
	if sendErr := c.sender.Send(ctx, cfg.Number, body); sendErr != nil {
		c.recordFailure(sendErr)
		return permanentTextFailure(sendErr)
	}
	if err := c.mutateState(func(s *textChannelState) bool {
		if s.Day != textChannelDay(now) {
			s.Day = textChannelDay(now)
			s.SentToday = 0
		}
		s.SentToday++
		s.LastMomentAt = now
		s.LastSentAt = now
		s.LastError = ""
		s.SendBlocked = false
		s.MessagesAllowed = true
		return true
	}); err != nil {
		log.Printf("[text-channel] recording a sent text: %v", err)
	}
	return nil
}

// SendTest sends the fixed test text. It is outside the daily cap and the
// cooldown, and it allows one attempt a minute.
func (c *textChannel) SendTest(ctx context.Context) (TextChannelStatus, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	cfg, err := c.usable()
	if err != nil {
		return c.statusWith(err)
	}
	now := c.now()
	state, err := c.readState()
	if err != nil {
		return c.statusWith(err)
	}
	if !state.LastTestAt.IsZero() && now.Sub(state.LastTestAt) < textChannelTestInterval {
		return c.statusWith(errTextChannelTestSoon)
	}
	// The attempt is recorded before the send, so repeated failures are also
	// limited to one a minute.
	if err := c.mutateState(func(s *textChannelState) bool {
		s.LastTestAt = now
		return true
	}); err != nil {
		return c.statusWith(err)
	}
	body := textChannelTestBody
	if c.chinese() {
		body = "这是 Isoform 发来的测试短信。请在电脑上打开 Isoform。"
	}
	if sendErr := c.sender.Send(ctx, cfg.Number, body); sendErr != nil {
		c.recordFailure(sendErr)
		return c.statusWith(sendErr)
	}
	if err := c.mutateState(func(s *textChannelState) bool {
		s.LastSentAt = now
		s.LastError = ""
		s.SendBlocked = false
		s.MessagesAllowed = true
		return true
	}); err != nil {
		log.Printf("[text-channel] recording a test text: %v", err)
	}
	return c.statusWith(nil)
}

// statusWith returns the current status with the operation's error, so the
// card can show both.
func (c *textChannel) statusWith(opErr error) (TextChannelStatus, error) {
	status, statusErr := c.Status()
	if opErr != nil {
		return status, opErr
	}
	return status, statusErr
}

func (c *textChannel) chinese() bool {
	return c.lang != nil && strings.HasPrefix(strings.ToLower(c.lang()), "zh")
}

// RequestMessagesAutomation asks macOS for permission, launching Messages
// hidden first and possibly showing the consent prompt.
func (c *textChannel) RequestMessagesAutomation() bool {
	return c.automationAllowed(true)
}

// automationAllowed asks macOS and records a definitive answer. macOS answers
// only while Messages runs, so an unknown answer keeps the last recorded one.
// A closed Messages app therefore does not turn the channel off.
func (c *textChannel) automationAllowed(askUser bool) bool {
	observed := c.automation(askUser)
	allowed := false
	if err := c.mutateState(func(s *textChannelState) bool {
		changed := false
		if observed != textAutomationUnknown {
			want := observed == textAutomationAllowed
			changed = s.MessagesAllowed != want
			s.MessagesAllowed = want
		}
		allowed = s.MessagesAllowed
		return changed
	}); err != nil {
		log.Printf("[text-channel] recording Messages permission: %v", err)
	}
	return allowed
}

// recordFailure stores the send error for the status card. A denial also
// clears the recorded permission, so the card asks for it again.
func (c *textChannel) recordFailure(sendErr error) {
	if err := c.mutateState(func(s *textChannelState) bool {
		s.LastError = sendErr.Error()
		s.SendBlocked = true
		if errors.Is(sendErr, errTextMessagesNotAllowed) {
			s.MessagesAllowed = false
		}
		return true
	}); err != nil {
		log.Printf("[text-channel] recording a send failure: %v", err)
	}
}

func (c *textChannel) readState() (textChannelState, error) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	return loadTextChannelState(c.dir)
}

// mutateState applies change to the state file under stateMu and saves it only
// when change reports a difference.
func (c *textChannel) mutateState(change func(*textChannelState) bool) error {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	state, err := loadTextChannelState(c.dir)
	if err != nil {
		return err
	}
	if !change(&state) {
		return nil
	}
	return saveTextChannelState(c.dir, state)
}

// cachedAutomationAllowed is a display snapshot, never authority to send.
// Explicit requests and delivery still query macOS and update the stored answer.
// An Apple-event status call can block even with askUser=false, so permission
// lists and Settings reads must not perform it synchronously.
func (c *textChannel) cachedAutomationAllowed() bool {
	state, err := c.readState()
	return err == nil && state.MessagesAllowed
}
