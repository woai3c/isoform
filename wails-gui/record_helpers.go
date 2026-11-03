package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Record helpers shared by the Purpose, Self Dream, self-directive, first-run
// learning and background-understanding stores. They came from the Agency
// backend, which was removed on 2026-10-13; the names keep their "agency"
// prefix because stored ids and hashes are derived from them and every caller
// already uses these names. None of them depends on Agency state.

// agencyPayloadHash is the hex SHA-256 of payload.
func agencyPayloadHash(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// deterministicAgencyID derives a stable UUID from kind and key. Stored rows
// use it as their id, so its output must never change.
func deterministicAgencyID(kind, key string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(kind+"\x00"+key)).String()
}

// agencyStructPayload encodes value and returns the JSON with its hash.
func agencyStructPayload(value any) (string, string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", "", fmt.Errorf("encode agency payload: %w", err)
	}
	return string(payload), agencyPayloadHash(payload), nil
}

// normalizeAgencyComparisonText lowercases value and collapses its whitespace.
func normalizeAgencyComparisonText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

// agencyContext is the app context, or Background before startup.
func agencyContext(a *App) context.Context {
	if a != nil && a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// agencyTimestamp formats Unix milliseconds as RFC 3339 in UTC; zero and
// negative values are empty.
func agencyTimestamp(milliseconds int64) string {
	if milliseconds <= 0 {
		return ""
	}
	return time.UnixMilli(milliseconds).UTC().Format(time.RFC3339Nano)
}

// agencyBoundedText trims value and cuts it to maxRunes, marking a cut with
// "...".
func agencyBoundedText(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return strings.TrimSpace(string(runes[:maxRunes])) + "..."
}
