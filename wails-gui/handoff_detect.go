package main

import (
	"context"
	"fmt"

	"wails-gui/internal/handoff"
	"wails-gui/storage"

	"github.com/google/uuid"
)

// createHandoffExecutionSession creates the chat session a handoff's execution
// streams into, routed to the Handoff sidebar group. Unlike the legacy
// appendHandoffProposalNotification it posts no proposal card — the handoff
// lives as a file surfaced via the chip and Handoffs page.
func (a *App) createHandoffExecutionSession(e handoff.Entry) (string, error) {
	if a.store == nil {
		return "", fmt.Errorf("store not initialized")
	}
	ctx := context.Background()
	projectID, workspaceID, err := a.standaloneChatProjectAndWorkspace(ctx)
	if err != nil {
		return "", err
	}
	sessionLocalID := uuid.New().String()
	label := fmt.Sprintf("Handoff: %s", e.Title)
	if len(label) > 60 {
		label = label[:57] + "..."
	}
	sess := storage.SessionRow{
		LocalID:     sessionLocalID,
		ProjectID:   projectID,
		WorkspaceID: workspaceID,
		Label:       label,
		Model:       a.chatTaskModel(),
	}
	if err := a.store.SaveSession(ctx, sess); err != nil {
		return "", fmt.Errorf("save handoff session: %w", err)
	}
	a.addSessionToHandoffsGroup(ctx, sessionLocalID)
	return sessionLocalID, nil
}
