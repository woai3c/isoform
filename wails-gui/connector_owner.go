package main

import (
	"crypto/rand"
	"time"
)

// connectorOwnerState is the owner gate the local chat connectors share:
// Slack, Discord, Feishu and WeChat embed it in their saved credentials, and
// Telegram keeps the same fields inline. A connector is paired once the owner
// sends the pairing code shown in the app. Until then only that pairing
// message is accepted; after it, only the owner's messages start runs.
type connectorOwnerState struct {
	OwnerID     string `json:"owner_id,omitempty"`
	OwnerName   string `json:"owner_name,omitempty"`
	OwnerPaired bool   `json:"owner_paired,omitempty"`
	// PairingCode is shown in the app until the owner sends it.
	PairingCode string `json:"pairing_code,omitempty"`
}

// paired reports whether an owner was set by the pairing code.
func (s connectorOwnerState) paired() bool {
	return s.OwnerPaired && s.OwnerID != ""
}

// connectorPersonalContext permits personal prompt blocks only when both the
// sender and the reply audience are private. Unknown chat types fail closed.
func connectorPersonalContext(paired bool, chatType string) bool {
	return paired && (chatType == "im" || chatType == "p2p" || chatType == "dm" || chatType == "private")
}

// connectorContextThreadKey keeps shared conversations away from sessions,
// history and scratch files created before personal context was restricted.
// Those older sessions can already contain private prompt blocks or replies.
func connectorContextThreadKey(threadKey string, personal bool) string {
	if personal {
		return threadKey
	}
	return threadKey + ":shared-context-v1"
}

// ensureConnectorPairing gives an unpaired connector a pairing code and clears
// the code once it is paired. It reports whether s changed.
func ensureConnectorPairing(s *connectorOwnerState) (bool, error) {
	if s.paired() {
		if s.PairingCode == "" {
			return false, nil
		}
		s.PairingCode = ""
		return true, nil
	}
	if s.PairingCode != "" {
		return false, nil
	}
	code, err := newConnectorPairingCode()
	if err != nil {
		return false, err
	}
	s.PairingCode = code
	return true, nil
}

// newConnectorPairingCode returns a random code such as "K7QF-M2XP" (40 bits).
func newConnectorPairingCode() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	code := make([]byte, 0, 9)
	for i, b := range buf {
		if i == 4 {
			code = append(code, '-')
		}
		code = append(code, telegramPairingAlphabet[int(b)%len(telegramPairingAlphabet)])
	}
	return string(code), nil
}

// connectorPairingMatches reports whether text is the pairing code. Case,
// dashes and spaces are ignored, and a "/start CODE" message counts too.
func connectorPairingMatches(s connectorOwnerState, text string) bool {
	code := normalizePairingText(s.PairingCode)
	return code != "" && normalizePairingText(text) == code
}

// connectorConnectedAt reads the connect time saved with the credentials. A
// missing or unreadable time falls back to fallback, which is never zero, so a
// damaged credential file cannot let older messages through.
func connectorConnectedAt(raw string, fallback time.Time) time.Time {
	connectedAt, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return fallback
	}
	return connectedAt
}

// connectorMessageIsStale reports whether a message was sent before the
// connector connected, or has no usable send time. Messages queued before the
// connect, or replayed from an earlier session, are never answered.
func connectorMessageIsStale(sentAt, connectedAt time.Time) bool {
	if sentAt.IsZero() {
		return true
	}
	return sentAt.Before(connectedAt.Truncate(time.Second))
}
