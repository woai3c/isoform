package main

// filterDiscordChannels applies the per-guild channel filter to a list of channels.
//
// Semantics:
//   - nil or empty filter map: no filtering, return all channels unchanged.
//   - Guild present with non-empty list: keep only channels whose ID is in that list.
//   - Guild present with empty list: skip that guild entirely (return nil).
func filterDiscordChannels(guildID string, channels []DiscordScanChannel, filter map[string][]string) []DiscordScanChannel {
	if len(filter) == 0 {
		return channels
	}

	allowedIDs, guildInFilter := filter[guildID]
	if !guildInFilter {
		// Guild not mentioned in filter — no restriction, keep all channels.
		return channels
	}
	if len(allowedIDs) == 0 {
		// Guild explicitly has empty list — skip entirely.
		return nil
	}

	allowed := make(map[string]struct{}, len(allowedIDs))
	for _, id := range allowedIDs {
		allowed[id] = struct{}{}
	}

	filtered := make([]DiscordScanChannel, 0, len(channels))
	for _, ch := range channels {
		if _, ok := allowed[ch.ID]; ok {
			filtered = append(filtered, ch)
		}
	}
	return filtered
}
