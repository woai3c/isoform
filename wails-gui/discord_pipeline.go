package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// DiscordScanChannel represents a Discord text channel from the scan API.
type DiscordScanChannel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type int    `json:"type"`
}

// RunDiscordScanPipeline executes the cold-start Discord scan.
// Fetches history from all authorized guilds/channels via backend proxy,
// segments into conversations, and writes to the discord-scan directory.
func (a *App) RunDiscordScanPipeline(ctx context.Context) error {
	if !a.discordPipelineRunMu.TryLock() {
		return fmt.Errorf("another Discord scan pipeline is already running")
	}
	defer a.discordPipelineRunMu.Unlock()

	token, err := loadYansuToken()
	if err != nil || token == "" {
		return fmt.Errorf("not authenticated")
	}
	baseURL := loadAPIBaseURL()

	// Get scan directory.
	scanDir, err := a.discordScanDir()
	if err != nil {
		return err
	}
	conversationsDir := filepath.Join(scanDir, "conversations")

	log.Printf("[discord-scan] Starting cold-start scan pipeline")

	// Step 1: Get authorized guilds.
	var guilds []DiscordGuild
	if err := apiGet(baseURL, token, "/api/discord/guilds", &guilds); err != nil {
		return fmt.Errorf("fetch guilds: %w", err)
	}
	log.Printf("[discord-scan] Found %d authorized guilds", len(guilds))

	var allConvs []Conversation

	// Load channel filter once for all guilds.
	channelFilter := loadConfig().DiscordScanChannelFilter

	// Step 2: For each guild, fetch channels and history.
	for _, guild := range guilds {
		log.Printf("[discord-scan] Processing guild %s", guild.GuildID)

		// Get text channels for this guild.
		var channels []DiscordScanChannel
		if err := apiGet(baseURL, token, fmt.Sprintf("/api/discord/guilds/%s/channels", guild.GuildID), &channels); err != nil {
			log.Printf("[discord-scan] Failed to fetch channels for guild %s: %v", guild.GuildID, err)
			continue
		}

		channels = filterDiscordChannels(guild.GuildID, channels, channelFilter)

		// 7-day lookback snowflake (mirrors Slack cold-start behaviour).
		sevenDaysAgo := discordSnowflakeFromTime(time.Now().Add(-7 * 24 * time.Hour))

		for _, ch := range channels {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			log.Printf("[discord-scan] Fetching history for channel #%s (%s)", ch.Name, ch.ID)

			// Fetch channel message history (past 7 days, paginated).
			messages, err := a.fetchDiscordChannelHistory(ctx, baseURL, token, ch.ID, sevenDaysAgo)
			if err != nil {
				log.Printf("[discord-scan] Failed to fetch history for channel %s: %v", ch.ID, err)
				continue
			}

			// Segment messages into conversations.
			convs := segmentDiscordConversations(messages, ch.ID)
			allConvs = append(allConvs, convs...)

			// Fetch threads for this channel.
			threads, err := a.fetchDiscordChannelThreads(ctx, baseURL, token, ch.ID, guild.GuildID)
			if err != nil {
				log.Printf("[discord-scan] Failed to fetch threads for channel %s: %v", ch.ID, err)
				continue
			}

			// Fetch history for each thread.
			for _, thread := range threads {
				if ctx.Err() != nil {
					return ctx.Err()
				}

				threadMsgs, err := a.fetchDiscordChannelHistory(ctx, baseURL, token, thread.ID, "")
				if err != nil {
					log.Printf("[discord-scan] Failed to fetch thread %s history: %v", thread.ID, err)
					continue
				}

				// Thread messages get their own conversation keyed by thread ID.
				if len(threadMsgs) > 0 {
					for i := range threadMsgs {
						threadMsgs[i].ThreadID = thread.ID
					}
					threadConvs := segmentDiscordConversations(threadMsgs, ch.ID)
					allConvs = append(allConvs, threadConvs...)
				}
			}
		}
	}

	log.Printf("[discord-scan] Segmented %d conversations total", len(allConvs))

	// Step 3: Write conversation files.
	if err := writeDiscordConversations(allConvs, conversationsDir); err != nil {
		return fmt.Errorf("write conversations: %w", err)
	}

	log.Printf("[discord-scan] Pipeline complete: %d conversations written to %s", len(allConvs), conversationsDir)
	return nil
}

// fetchDiscordChannelHistory fetches paginated message history for a channel.
// If afterID is non-empty, only fetches messages after that ID (for incremental scan).
//
// Discord ordering:
//   - No params / before=X  → newest-first (descending by ID)
//   - after=X               → oldest-first (ascending by ID)
//
// The returned slice is always normalized to newest-first so callers can treat
// messages[0] as the most recent message regardless of mode.
func (a *App) fetchDiscordChannelHistory(ctx context.Context, baseURL, token, channelID, afterID string) ([]DiscordScanMessage, error) {
	var allMessages []DiscordScanMessage

	if afterID != "" {
		// Incremental mode: fetch pages ordered oldest→newest using after= cursor.
		cursorID := afterID
		for {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			endpoint := fmt.Sprintf("/api/discord/channels/%s/history?limit=100&after=%s", channelID, cursorID)
			var messages []DiscordScanMessage
			if err := apiGet(baseURL, token, endpoint, &messages); err != nil {
				return nil, err
			}
			if len(messages) == 0 {
				break
			}
			// messages are oldest-first; append then advance cursor to the newest.
			allMessages = append(allMessages, messages...)
			cursorID = messages[len(messages)-1].ID
			if len(messages) < 100 {
				break
			}
		}
		// Reverse so the result is newest-first, matching the no-afterID path.
		for i, j := 0, len(allMessages)-1; i < j; i, j = i+1, j-1 {
			allMessages[i], allMessages[j] = allMessages[j], allMessages[i]
		}
	} else {
		// Cold-start mode: fetch pages ordered newest→oldest using before= cursor.
		beforeID := ""
		for {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			endpoint := fmt.Sprintf("/api/discord/channels/%s/history?limit=100", channelID)
			if beforeID != "" {
				endpoint += "&before=" + beforeID
			}
			var messages []DiscordScanMessage
			if err := apiGet(baseURL, token, endpoint, &messages); err != nil {
				return nil, err
			}
			if len(messages) == 0 {
				break
			}
			allMessages = append(allMessages, messages...)
			if len(messages) < 100 {
				break
			}
			// messages[last] is the oldest in this page; paginate backwards.
			beforeID = messages[len(messages)-1].ID
		}
	}

	return allMessages, nil
}

// discordThread represents a thread from the threads list API.
type discordThread struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// fetchDiscordChannelThreads fetches active + archived threads for a channel.
func (a *App) fetchDiscordChannelThreads(ctx context.Context, baseURL, token, channelID, guildID string) ([]discordThread, error) {
	var resp struct {
		Threads []discordThread `json:"threads"`
	}
	if err := apiGet(baseURL, token, fmt.Sprintf("/api/discord/channels/%s/threads?guild_id=%s", channelID, guildID), &resp); err != nil {
		return nil, err
	}
	return resp.Threads, nil
}

// discordScanDir returns the path to the Discord scan directory.
func (a *App) discordScanDir() (string, error) {
	dataRoot, err := flavorDataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataRoot, "discord-scan"), nil
}

// GetDiscordScanStatus returns the scan status for the frontend.
func (a *App) GetDiscordScanStatus() map[string]any {
	scanDir, err := a.discordScanDir()
	if err != nil {
		log.Printf("[discord-scan] resolve data folder: %v", err)
		return map[string]any{"scanned": false, "conv_count": 0}
	}
	conversationsDir := filepath.Join(scanDir, "conversations")

	entries, err := os.ReadDir(conversationsDir)
	if err != nil {
		return map[string]any{
			"scanned":    false,
			"conv_count": 0,
		}
	}

	count := 0
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			count++
		}
	}

	return map[string]any{
		"scanned":    count > 0,
		"conv_count": count,
	}
}

// apiGet is a helper used across the wails-gui package (defined elsewhere).
// Suppress unused import warning for json.
var _ = json.Marshal
