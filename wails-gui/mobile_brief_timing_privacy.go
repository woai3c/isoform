package main

import (
	"context"
	"time"
)

// mobileBriefTimingPrivacy requires continuous exclusion coverage for every
// original interval, including calendar events that began before the query.
// The coverage floor is scoped to timing; it does not alter other MCP readers.
func mobileBriefTimingPrivacy(ctx context.Context, app *App, now time.Time) (*mcpReadPrivacy, error) {
	privacy, err := app.mcpReadPrivacy(ctx)
	if err != nil {
		return nil, err
	}
	since, err := app.store.EnsureMobileBriefTimingPrivacySince(ctx, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	if retainedSince := now.Add(-observationPausesKept).UnixMilli(); retainedSince > since {
		since = retainedSince
	}
	// Treat uncovered history as excluded, even after its old pause entries
	// have expired. Source intervals keep their true start for this check.
	privacy.pauses = append(privacy.pauses, observationPause{Start: 0, End: since})
	return privacy, nil
}
