package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const feishuScanDirName = "feishu-scan"

// FeishuChatInfo is a minimal chat descriptor used in the pipeline.
type FeishuChatInfo struct {
	ChatID   string
	Name     string
	ChatType string
}

// feishuChatsResponse mirrors the backend GET /api/feishu/chats response.
type feishuChatsResponse struct {
	Chats []feishuChatItem `json:"chats"`
}

type feishuChatItem struct {
	ChatID   string `json:"chat_id"`
	Name     string `json:"name"`
	ChatType string `json:"chat_type"`
}

// feishuChatMessagesResponse mirrors the backend paginated messages response.
type feishuChatMessagesResponse struct {
	Messages      []feishuRawMessageItem `json:"messages"`
	NextPageToken string                 `json:"next_page_token"`
}

type feishuRawMessageItem struct {
	MessageID  string `json:"message_id"`
	ThreadID   string `json:"thread_id"`
	RootID     string `json:"root_id"`
	ParentID   string `json:"parent_id"`
	ChatID     string `json:"chat_id"`
	ChatType   string `json:"chat_type"`
	MsgType    string `json:"msg_type"`
	CreateTime string `json:"create_time"`
	SenderID   string `json:"sender_id"`
	SenderType string `json:"sender_type"`
	TextBody   string `json:"content"`
}

type feishuThreadCandidate struct {
	RootMessageID string
	ThreadID      string
}

// fetchFeishuChats calls GET /api/feishu/chats on the backend.
// appID, when non-empty, is appended as &app_id=<appID> to scope results to a specific app.
func (a *App) fetchFeishuChats(ctx context.Context, baseURL, token, appID string) ([]FeishuChatInfo, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	path := "/api/feishu/chats"
	if appID != "" {
		path += "?app_id=" + url.QueryEscape(appID)
	}

	var resp feishuChatsResponse
	if err := apiGet(baseURL, token, path, &resp); err != nil {
		return nil, fmt.Errorf("fetch feishu chats: %w", err)
	}

	chats := make([]FeishuChatInfo, 0, len(resp.Chats))
	for _, c := range resp.Chats {
		chats = append(chats, FeishuChatInfo{
			ChatID:   c.ChatID,
			Name:     c.Name,
			ChatType: c.ChatType,
		})
	}
	return chats, nil
}

// fetchFeishuChatMessages fetches all messages for one chat, paginating until done.
// Writes raw JSONL to rawDir/{chatID}.jsonl.
// Returns (written message count, max seen create_time across ALL messages including
// non-text ones, error). The caller should use maxSeenTS for cursor advancement so
// that non-text-only windows still push the cursor forward.
//
// Parameters:
//   - baseURL, token: API credentials
//   - chatID: Feishu chat identifier
//   - chatName: injected into each FeishuIMMessageRaw.ChatName for segmenter context
//   - startTime: Unix seconds; messages created at or after this time are fetched
//   - rawDir: directory to write the output JSONL file
//   - appID: passed via &app_id= when non-empty (for multi-app setups)
func (a *App) fetchFeishuChatMessages(ctx context.Context, baseURL, token, chatID, chatName string, startTime int64, rawDir, appID string) (int, int64, error) {
	outFile := filepath.Join(rawDir, chatID+".jsonl")
	tmpFile := outFile + ".tmp"
	f, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, 0, fmt.Errorf("open raw file: %w", err)
	}
	renamed := false
	defer func() {
		_ = f.Close()
		if ctx.Err() != nil || !renamed {
			_ = os.Remove(tmpFile)
		}
	}()

	pageToken := ""
	msgCount := 0
	threadReplyCount := 0
	skippedEmptyCount := 0
	var maxSeenTS int64 // tracks max create_time across ALL messages (including non-text)
	threadCandidates := make([]feishuThreadCandidate, 0)
	seenThreadCandidates := make(map[string]bool)

	for {
		if ctx.Err() != nil {
			return 0, 0, ctx.Err()
		}

		requestPath := fmt.Sprintf("/api/feishu/chats/%s/messages?start_time=%d",
			url.PathEscape(chatID), startTime)
		if pageToken != "" {
			requestPath += "&page_token=" + url.QueryEscape(pageToken)
		}
		if appID != "" {
			requestPath += "&app_id=" + url.QueryEscape(appID)
		}

		var resp feishuChatMessagesResponse
		if err := apiGet(baseURL, token, requestPath, &resp); err != nil {
			return 0, 0, fmt.Errorf("fetch feishu chat messages: %w", err)
		}

		for _, item := range resp.Messages {
			// Track max create_time across ALL messages (including non-text)
			// so the cursor advances even when a batch has no text messages.
			if ts := parseCreateTimeMs(item.CreateTime); ts > maxSeenTS {
				maxSeenTS = ts
			}

			// Track non-system top-level messages as potential thread roots
			// BEFORE the text filter — threads whose root is an image/file
			// may still contain text replies worth fetching.
			if item.RootID == "" && item.MsgType != "system" {
				candidateKey := item.ThreadID
				if candidateKey == "" {
					candidateKey = item.MessageID
				}
				if !seenThreadCandidates[candidateKey] {
					threadCandidates = append(threadCandidates, feishuThreadCandidate{
						RootMessageID: item.MessageID,
						ThreadID:      item.ThreadID,
					})
					seenThreadCandidates[candidateKey] = true
				}
			}

			msg, ok := buildFeishuRawMessage(item, chatName)
			if !ok {
				skippedEmptyCount++
				continue
			}
			line, err := json.Marshal(msg)
			if err != nil {
				return 0, 0, fmt.Errorf("marshal message: %w", err)
			}
			if _, err := f.Write(append(line, '\n')); err != nil {
				return 0, 0, fmt.Errorf("write message: %w", err)
			}
			msgCount++
		}

		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}

	// Fetch thread replies for potential thread roots.
	for _, candidate := range threadCandidates {
		if ctx.Err() != nil {
			return 0, 0, ctx.Err()
		}

		threadPageToken := ""
		threadRef := candidate.RootMessageID
		if candidate.ThreadID != "" {
			threadRef = candidate.ThreadID
		}
		for {
			if ctx.Err() != nil {
				return 0, 0, ctx.Err()
			}
			threadPath := fmt.Sprintf("/api/feishu/threads/%s/messages",
				url.PathEscape(threadRef))
			if appID != "" {
				threadPath += "?app_id=" + url.QueryEscape(appID)
			}
			if threadPageToken != "" {
				sep := "?"
				if appID != "" {
					sep = "&"
				}
				threadPath += sep + "page_token=" + url.QueryEscape(threadPageToken)
			}

			var threadResp feishuChatMessagesResponse
			if err := apiGet(baseURL, token, threadPath, &threadResp); err != nil {
				log.Printf(
					"[feishu-scan] failed to fetch thread root=%s thread=%s: %v",
					candidate.RootMessageID,
					candidate.ThreadID,
					err,
				)
				break
			}

			pageReplyCount := 0
			for _, item := range threadResp.Messages {
				// Skip the root message itself (already written above).
				if item.MessageID == candidate.RootMessageID {
					continue
				}
				msg, ok := buildFeishuRawMessage(item, chatName)
				if !ok {
					skippedEmptyCount++
					continue
				}
				line, err := json.Marshal(msg)
				if err != nil {
					continue
				}
				if _, err := f.Write(append(line, '\n')); err != nil {
					return 0, 0, fmt.Errorf("write thread message: %w", err)
				}
				msgCount++
				threadReplyCount++
				pageReplyCount++
			}
			if pageReplyCount > 0 {
				log.Printf(
					"[feishu-scan] fetched %d thread replies for chat=%s root=%s thread=%s",
					pageReplyCount,
					chatID,
					candidate.RootMessageID,
					candidate.ThreadID,
				)
			}

			if threadResp.NextPageToken == "" {
				break
			}
			threadPageToken = threadResp.NextPageToken
		}
	}

	log.Printf(
		"[feishu-scan] chat=%s top_level=%d thread_candidates=%d thread_replies=%d skipped_empty=%d total=%d",
		chatID,
		msgCount-threadReplyCount,
		len(threadCandidates),
		threadReplyCount,
		skippedEmptyCount,
		msgCount,
	)

	if err := f.Sync(); err != nil {
		return 0, 0, fmt.Errorf("sync raw file: %w", err)
	}
	if err := f.Close(); err != nil {
		return 0, 0, fmt.Errorf("close raw file: %w", err)
	}
	if err := os.Rename(tmpFile, outFile); err != nil {
		return msgCount, 0, err
	}
	renamed = true
	return msgCount, maxSeenTS, nil
}

func buildFeishuRawMessage(item feishuRawMessageItem, chatName string) (FeishuIMMessageRaw, bool) {
	textBody := extractTextFromContent(item.MsgType, item.TextBody)
	if strings.TrimSpace(textBody) == "" {
		return FeishuIMMessageRaw{}, false
	}

	return FeishuIMMessageRaw{
		MessageID:  item.MessageID,
		ThreadID:   item.ThreadID,
		RootID:     item.RootID,
		ParentID:   item.ParentID,
		ChatID:     item.ChatID,
		ChatType:   item.ChatType,
		MsgType:    item.MsgType,
		CreateTime: item.CreateTime,
		SenderID:   item.SenderID,
		SenderType: item.SenderType,
		TextBody:   textBody,
		ChatName:   chatName,
	}, true
}
