package main

import (
	"fmt"
	"strings"
)

// browserURLSighting is one captured browser URL and when it was seen.
type browserURLSighting struct {
	URL       string
	Timestamp int64
}

// ListRecentBrowserURLSightings returns the newest snapshot URLs since
// sinceMs whose URL contains one of the host fragments, newest first. It is
// how the calendar reader learns that a web calendar was open on screen.
// Protected sessions are skipped.
func (s *ActivityStore) ListRecentBrowserURLSightings(hostFragments []string, sinceMs int64, limit int) ([]browserURLSighting, error) {
	if len(hostFragments) == 0 || limit <= 0 {
		return nil, nil
	}
	clauses := make([]string, 0, len(hostFragments))
	args := []any{sinceMs}
	for _, fragment := range hostFragments {
		fragment = strings.TrimSpace(fragment)
		if fragment == "" {
			continue
		}
		clauses = append(clauses, "s.browser_url LIKE ?")
		args = append(args, "%"+fragment+"%")
	}
	if len(clauses) == 0 {
		return nil, nil
	}
	args = append(args, limit)
	rows, err := s.db.Query(`
		SELECT s.browser_url, MAX(s.timestamp)
		FROM activity_snapshots s
		JOIN activity_sessions sess ON sess.id = s.session_id
		WHERE s.timestamp >= ? AND COALESCE(s.browser_url, '') != ''
		  AND (`+strings.Join(clauses, " OR ")+`)
		  AND COALESCE(sess.triage_status, '') != 'protected'
		GROUP BY s.browser_url
		ORDER BY MAX(s.timestamp) DESC
		LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list browser url sightings: %w", err)
	}
	defer rows.Close()
	var out []browserURLSighting
	for rows.Next() {
		var sighting browserURLSighting
		if err := rows.Scan(&sighting.URL, &sighting.Timestamp); err != nil {
			return nil, err
		}
		out = append(out, sighting)
	}
	return out, rows.Err()
}
