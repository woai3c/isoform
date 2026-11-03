package openanswer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

// Block is untrusted model output, not evidence that a source is available or
// permission to make a promise. Window timestamps must include a timezone.
type Block struct {
	Question     string `json:"question"`
	Topic        string `json:"topic"`
	Settles      string `json:"settles"`
	Source       string `json:"source"`
	NotYet       bool   `json:"notYet"`
	WindowStart  string `json:"windowStart,omitempty"`
	WindowEnd    string `json:"windowEnd,omitempty"`
	WatchedEvent string `json:"watchedEvent,omitempty"`
}

// ParseBlock rejects malformed or ambiguous structured output. The caller
// must still bind it to the user's question and independently verify source
// availability and privacy before admission.
func ParseBlock(raw string) (Candidate, error) {
	if len(raw) == 0 || len(raw) > maxBlockBytes {
		return Candidate{}, errors.New("invalid open answer block size")
	}
	if err := validateBlockKeys(raw); err != nil {
		return Candidate{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var b Block
	if err := decoder.Decode(&b); err != nil {
		return Candidate{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Candidate{}, errors.New("invalid trailing data")
	}
	candidate := Candidate{Question: strings.TrimSpace(b.Question), Topic: strings.TrimSpace(b.Topic), Settles: b.Settles, Source: strings.TrimSpace(b.Source), NotYet: b.NotYet, WatchedEvent: strings.TrimSpace(b.WatchedEvent)}
	var err error
	if b.WindowStart != "" {
		candidate.WindowStart, err = time.Parse(time.RFC3339, b.WindowStart)
		if err != nil {
			return Candidate{}, err
		}
	}
	if b.WindowEnd != "" {
		candidate.WindowEnd, err = time.Parse(time.RFC3339, b.WindowEnd)
		if err != nil {
			return Candidate{}, err
		}
	}
	return candidate, nil
}

// encoding/json matches struct fields case-insensitively. Require the exact
// protocol keys so differently cased aliases cannot overwrite earlier claims.
// The block's event is an opaque source identifier.
func validateBlockKeys(raw string) error {
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("open answer must be an object")
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return errors.New("duplicate or invalid field")
		}
		switch key {
		case "question", "topic", "settles", "source", "notYet", "windowStart", "windowEnd", "watchedEvent":
		default:
			return errors.New("unknown open answer field")
		}
		seen[key] = true
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	return nil
}
