package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"wails-gui/internal/engagement"
	"wails-gui/internal/handoff"
	"wails-gui/internal/pii"
	"wails-gui/storage"
)

// The card is a presentation of the runner's request, never an authority grant.
// Only the live request may be answered; a card surviving restart is history.
type mainChatWorkPermission struct {
	RequestID       string `json:"requestId"`
	SessionID       string `json:"sessionId"`
	HandoffID       string `json:"handoffId"`
	ToolName        string `json:"toolName"`
	Details         string `json:"details"`
	ApproveOptionID string `json:"approveOptionId"`
	DeclineOptionID string `json:"declineOptionId"`
}

type mainChatPendingPermission struct {
	mainChatWorkPermission
	chatID           string
	toolUseID        string
	state            string
	options          map[string]bool
	acceptedOptionID string
	mobileReceipt    *storage.MobileReceipt
}

type mainChatWorkPermissions struct {
	mu        sync.Mutex
	bySession map[string]*mainChatPendingPermission
	seen      map[string]struct{}
	dirty     map[string]*mainChatPendingPermission
	// An accepted phone answer outlives presentation repair and runner close
	// until its exact transport outcome is durable.
	acceptedMobile map[string]*mainChatPendingPermission
}

var errMainChatPermissionReceiptPending = errors.New("accepted permission receipt is not durable")

func workPermissionRequestID(evt AgentEvent) string {
	identity := evt.ToolUseID
	if identity == "" {
		identity = evt.EventID
	}
	return "work-permission:" + engagement.HashPart(evt.SessionID+":"+identity)
}

// Returns true for a replay that must not reopen the original permission UI.
func (a *App) observeMainChatWorkPermission(evt *AgentEvent) bool {
	if a.store == nil || a.handoffStore == nil {
		return false
	}
	switch evt.Type {
	case "permission_request":
		if evt.SessionID == "" || evt.Tag == "" {
			return false
		}
		entries, err := a.handoffStore.List()
		if err != nil {
			return false
		}
		var work *handoff.Entry
		for i := range entries {
			if entries[i].ChatSessionID == evt.Tag && entries[i].Status == handoff.StatusInProgress {
				work = &entries[i]
				break
			}
		}
		if work == nil {
			a.workPermissions.mu.Lock()
			_, seen := a.workPermissions.seen[workPermissionRequestID(*evt)]
			a.workPermissions.mu.Unlock()
			if seen {
				return true
			}
			row, err := a.store.LoadStreamItem(context.Background(), "proactive:"+engagement.HashPart(workPermissionRequestID(*evt)))
			if err == nil && row != nil {
				return true
			}
			return false
		}
		request := &mainChatPendingPermission{
			mainChatWorkPermission: mainChatWorkPermission{RequestID: workPermissionRequestID(*evt), SessionID: evt.SessionID, HandoffID: work.ID, ToolName: mobileChatClip(evt.ToolName, mobileChatTitleMax)},
			chatID:                 evt.Tag, toolUseID: evt.ToolUseID, state: "pending", options: map[string]bool{},
		}
		for _, option := range evt.PermissionOptions {
			request.options[option.ID] = true
		}
		// Never broaden a one-time approval into session or permanent trust.
		for _, option := range []string{"allow_once", "accept", "acceptEdits"} {
			if request.options[option] {
				request.ApproveOptionID = option
				break
			}
		}
		for _, option := range []string{"reject", "deny", "decline", "plan"} {
			if request.options[option] {
				request.DeclineOptionID = option
				break
			}
		}
		// Free-form questions still use their existing question UI; their
		// answers must never be guessed from an approve/decline button.
		if request.ApproveOptionID == "" || request.DeclineOptionID == "" {
			a.workPermissions.mu.Lock()
			if previous := a.workPermissions.bySession[evt.SessionID]; previous != nil {
				if previous.state == "pending" {
					previous.state = "expired"
					_ = a.updateWorkPermissionCard(previous)
				}
				delete(a.workPermissions.bySession, evt.SessionID)
			}
			a.workPermissions.mu.Unlock()
			return false
		}
		details := evt.Content
		if len(evt.ToolInput) > 0 && string(evt.ToolInput) != "null" {
			details += "\n" + string(evt.ToolInput)
		}
		request.Details, _ = pii.RedactText(mobileChatBound(strings.TrimSpace(details), 4096))
		a.workPermissions.mu.Lock()
		defer a.workPermissions.mu.Unlock()
		if a.workPermissions.bySession == nil {
			a.workPermissions.bySession = map[string]*mainChatPendingPermission{}
			a.workPermissions.seen = map[string]struct{}{}
		}
		evt.PermissionRequestID = request.RequestID
		replayed := false
		if _, seen := a.workPermissions.seen[request.RequestID]; seen {
			current := a.workPermissions.bySession[evt.SessionID]
			if current == nil || current.RequestID != request.RequestID {
				return true
			}
			request, replayed = current, true
		} else if row, err := a.store.LoadStreamItem(context.Background(), "proactive:"+engagement.HashPart(request.RequestID)); err != nil {
			// A failed identity read cannot establish that this is a new request.
			return true
		} else if row != nil {
			return true
		}
		previous := a.workPermissions.bySession[evt.SessionID]
		if !replayed {
			if previous != nil && previous.state == "pending" {
				previous.state = "expired"
				_ = a.updateWorkPermissionCard(previous)
			}
			a.workPermissions.bySession[evt.SessionID] = request
			a.workPermissions.seen[request.RequestID] = struct{}{}
		}
		item := mainChatProactive{MomentID: request.RequestID, Kind: string(engagement.KindDecisionDue),
			State: request.state, Permission: &request.mainChatWorkPermission, Target: mainChatTarget{Kind: mainChatTargetNone},
			ActionIDs: []string{"approve", "decline"}, ActionLabels: map[string]string{},
			Evidence: []mainChatEvidenceRef{{Kind: "handoff", ID: work.ID, Target: mainChatTarget{Kind: mainChatTargetNone}}}}
		populateMainChatProactiveCopy(&item, map[string]string{"title": work.Title})
		copy := item.Localized[a.currentUILanguage()]
		if copy.Headline == "" {
			copy = item.Localized["en"]
		}
		item.Headline, item.Body, item.ActionLabels = copy.Headline, copy.Body, copy.ActionLabels
		item.sourceHash = proactiveVoiceHash(work)
		a.postProactiveItem(item, request.RequestID)
		if replayed && request.state != "pending" {
			_ = a.updateWorkPermissionCard(request)
		}
		return replayed
	case "tool_result", "close", "error":
		a.workPermissions.mu.Lock()
		defer a.workPermissions.mu.Unlock()
		for _, request := range a.workPermissions.bySession {
			if request.chatID != evt.Tag || request.state != "pending" {
				continue
			}
			if evt.Type == "tool_result" && (request.toolUseID == "" || request.toolUseID != evt.ToolUseID) {
				continue
			}
			request.state = "expired"
			_ = a.updateWorkPermissionCard(request)
			a.emitWorkPermissionResolved(request)
		}
		if evt.Type == "close" || evt.Type == "error" {
			for sessionID, request := range a.workPermissions.bySession {
				if request.chatID == evt.Tag {
					delete(a.workPermissions.bySession, sessionID)
				}
			}
		}
	}
	return false
}

// Caller holds workPermissions.mu. A failed presentation write retains its
// terminal outcome even after the runner closes or asks another question.
func (a *App) updateWorkPermissionCard(request *mainChatPendingPermission) (err error) {
	// Keep the phone's original action available until its retry key has a
	// durable outcome. Reads, replayed runner events and desktop repairs all
	// share this guard; a terminal card would strand the pending phone write.
	if a.workPermissions.acceptedMobile[request.RequestID] != nil {
		return errMainChatPermissionReceiptPending
	}
	defer func() {
		if err != nil {
			if a.workPermissions.dirty == nil {
				a.workPermissions.dirty = map[string]*mainChatPendingPermission{}
			}
			a.workPermissions.dirty[request.RequestID] = request
		} else {
			delete(a.workPermissions.dirty, request.RequestID)
		}
	}()
	ctx := context.Background()
	main, err := a.ensureMainChat(ctx)
	if err != nil {
		return err
	}
	row, err := a.store.FindProactiveStreamItem(ctx, main.LocalID, request.RequestID)
	if err != nil || row == nil {
		return err
	}
	var item mainChatProactive
	if err := json.Unmarshal([]byte(row.ToolInput), &item); err != nil {
		return err
	}
	item.State = request.state
	payload, err := json.Marshal(item)
	if err != nil {
		return err
	}
	if err := a.store.UpdateStreamItemToolInput(ctx, row.ID, string(payload)); err != nil {
		return err
	}
	a.mainChatRowsChanged(main.LocalID)
	a.emitBrowserEvent(mainChatItemsAppendedEvent, map[string]any{"sessionId": main.LocalID, "itemId": row.ID, "kind": "system", "source": mainChatSourceHandoff})
	return nil
}

// Re-reading either chat surface repairs presentation after a transient write
// failure. This only records a known answer/expiry; it never calls the runner.
func (a *App) retryWorkPermissionWrites() {
	a.workPermissions.mu.Lock()
	defer a.workPermissions.mu.Unlock()
	for _, request := range a.workPermissions.dirty {
		_ = a.updateWorkPermissionCard(request)
	}
}

func (a *App) emitWorkPermissionResolved(request *mainChatPendingPermission) {
	a.emitAgentEvent(AgentEvent{Type: "permission_resolved", Tag: request.chatID, SessionID: request.SessionID, ToolUseID: request.toolUseID, PermissionRequestID: request.RequestID})
}

// Every surface funnels through this response boundary. expectedRequestID
// prevents a stale main-chat card from answering a newer request in the run.
func (a *App) respondToWorkPermission(sessionID, optionID, expectedRequestID string) error {
	return a.respondToWorkPermissionChoice(sessionID, optionID, expectedRequestID, nil)
}

// A new phone choice cannot inherit another surface's accepted option. The
// existing callers retain their repair/replay behavior; mobile replays use the
// saved transport receipt instead. The check shares the request's mutex.
func (a *App) respondToWorkPermissionChoice(sessionID, optionID, expectedRequestID string, mobileReceipt *storage.MobileReceipt) error {
	a.workPermissions.mu.Lock()
	defer a.workPermissions.mu.Unlock()
	// Original desktop controls pass the stamped request token through the
	// existing binding; ordinary chats retain the agent-session argument.
	if strings.HasPrefix(sessionID, "work-permission:") {
		expectedRequestID, sessionID = sessionID, ""
		for id, candidate := range a.workPermissions.bySession {
			if candidate.RequestID == expectedRequestID {
				sessionID = id
				break
			}
		}
	}
	if mobileReceipt != nil {
		// Check again under the shared permission lock: two requests may have
		// passed the transport's initial receipt read concurrently.
		if replayed, err := a.replayMobileWorkPermissionReceipt(mobileReceipt); err != nil || replayed {
			if replayed {
				a.completeMobileWorkPermissionReceipt(*mobileReceipt)
			}
			return err
		}
		if accepted := a.workPermissions.acceptedMobile[expectedRequestID]; accepted != nil {
			if accepted.acceptedOptionID != optionID || accepted.mobileReceipt.RequestKey != mobileReceipt.RequestKey || accepted.mobileReceipt.Route != mobileReceipt.Route {
				return errors.New("this permission request was answered by another choice")
			}
			return a.finishMobileWorkPermission(accepted)
		}
	}
	if previous := a.workPermissions.dirty[expectedRequestID]; previous != nil {
		if err := a.updateWorkPermissionCard(previous); err != nil {
			return err
		}
		if previous.state == "acted" && mobileReceipt == nil {
			return nil
		}
		return errors.New("this permission request is no longer pending")
	}
	request := a.workPermissions.bySession[sessionID]
	if expectedRequestID != "" && (request == nil || request.RequestID != expectedRequestID) {
		return errors.New("this permission request is no longer pending")
	}
	if request != nil {
		switch request.state {
		case "acted":
			if mobileReceipt != nil {
				return errors.New("this permission request is no longer pending")
			}
			return a.updateWorkPermissionCard(request)
		case "expired":
			return errors.New("this permission request is no longer pending")
		}
		if !request.options[optionID] {
			return errors.New("the option is not offered for this request")
		}
	}
	a.mu.Lock()
	runner := a.runner
	a.mu.Unlock()
	if runner == nil {
		return fmt.Errorf("runner not initialized")
	}
	if err := runner.RespondToPermission(sessionID, optionID); err != nil {
		return err
	}
	if request != nil {
		// Remember acceptance before persisting its presentation: a failed
		// SQLite write may be retried without answering the runner twice.
		request.state = "acted"
		if mobileReceipt != nil {
			request.acceptedOptionID, request.mobileReceipt = optionID, mobileReceipt
			if a.workPermissions.acceptedMobile == nil {
				a.workPermissions.acceptedMobile = map[string]*mainChatPendingPermission{}
			}
			a.workPermissions.acceptedMobile[request.RequestID] = request
			return a.finishMobileWorkPermission(request)
		}
		a.emitWorkPermissionResolved(request)
		return a.updateWorkPermissionCard(request)
	}
	return nil
}

// Caller holds workPermissions.mu. A presentation failure cannot change an
// accepted answer into a refusal. If the independent receipt write fails, keep
// the accepted identity and return a retryable error without asking the runner
// again. No presentation read/repair or runner-close event consumes that identity.
func (a *App) finishMobileWorkPermission(request *mainChatPendingPermission) error {
	err := a.store.SaveMobileReceipt(context.Background(), *request.mobileReceipt)
	if err == nil {
		var found bool
		found, err = a.replayMobileWorkPermissionReceipt(request.mobileReceipt)
		if err == nil && !found {
			err = errors.New("permission receipt was not saved")
		}
	}
	if err != nil {
		return fmt.Errorf("%w: %v", errMainChatPermissionReceiptPending, err)
	}
	a.completeMobileWorkPermissionReceipt(*request.mobileReceipt)
	return nil
}

// A receipt write may commit even if its verification read fails. Transport
// replay must reconcile that known durable outcome before returning, because
// it deliberately does not invoke the action or answer the runner again.
func (a *App) reconcileMobileWorkPermissionReceipt(receipt storage.MobileReceipt) {
	a.workPermissions.mu.Lock()
	defer a.workPermissions.mu.Unlock()
	a.completeMobileWorkPermissionReceipt(receipt)
}

// Caller holds workPermissions.mu and has verified the durable receipt.
func (a *App) completeMobileWorkPermissionReceipt(receipt storage.MobileReceipt) {
	for requestID, request := range a.workPermissions.acceptedMobile {
		expected := request.mobileReceipt
		if expected.RequestKey != receipt.RequestKey || expected.Route != receipt.Route || expected.Status != receipt.Status || expected.Body != receipt.Body {
			continue
		}
		delete(a.workPermissions.acceptedMobile, requestID)
		a.emitWorkPermissionResolved(request)
		_ = a.updateWorkPermissionCard(request)
	}
}

func (a *App) replayMobileWorkPermissionReceipt(expected *storage.MobileReceipt) (bool, error) {
	receipt, found, err := a.store.MobileReceipt(context.Background(), expected.RequestKey)
	if err != nil {
		return false, fmt.Errorf("%w: %v", errMainChatPermissionReceiptPending, err)
	}
	if found && (receipt.Route != expected.Route || receipt.Status != expected.Status || receipt.Body != expected.Body) {
		return false, errors.New("this idempotency key was used for another request")
	}
	return found, nil
}

func (a *App) resolveMainChatWorkPermission(card *mainChatProactive, actionID string, mobileReceipt *storage.MobileReceipt) (string, error) {
	request := card.Permission
	var option string
	switch actionID {
	case "approve":
		option = request.ApproveOptionID
	case "decline":
		option = request.DeclineOptionID
	default:
		return "", errors.New("action is not available on this permission")
	}
	if err := a.respondToWorkPermissionChoice(request.SessionID, option, request.RequestID, mobileReceipt); err != nil {
		return "", err
	}
	return "acted", nil
}
