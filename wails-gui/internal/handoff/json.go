package handoff

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

func validateID(id string) error {
	if id == "" || strings.TrimSpace(id) != id || strings.HasPrefix(id, ".") ||
		filepath.Base(id) != id || strings.ContainsAny(id, "/\\\x00\r\n") {
		return errors.New("handoff: invalid entry id")
	}
	return nil
}

// decodeJSONEntry tolerates absent optional fields and unknown fields/statuses.
// Invalid JSON remains on disk for repair and never falls back to an older
// markdown copy. The filename is the identity used by the store.
func decodeJSONEntry(id string, data []byte) (Entry, error) {
	if len(strings.TrimSpace(string(data))) == 0 || strings.TrimSpace(string(data))[0] != '{' {
		return Entry{}, fmt.Errorf("handoff: decode %s: expected an object", id)
	}
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		return Entry{}, fmt.Errorf("handoff: decode %s: %w", id, err)
	}
	e.ID = id
	return normalizeReadEntry(e), nil
}

func normalizeReadEntry(e Entry) Entry {
	if e.Status == "" {
		e.Status = StatusActive
	}
	if e.UpdatedAt == 0 {
		e.UpdatedAt = e.CreatedAt
	}
	if e.LastActivityAt == 0 {
		e.LastActivityAt = e.UpdatedAt
	}
	if len(e.Criteria) == 0 {
		e.Criteria = CriteriaFromBody(e.Body)
	}
	return e
}
