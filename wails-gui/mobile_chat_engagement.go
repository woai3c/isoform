package main

import (
	"net/http"
	"strings"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

// Check the visible owning chat before a phone can measure or change a card.
func (bridge *mobileBridge) validateProactiveChat(w http.ResponseWriter, chatID, momentID string) bool {
	if !mobileValidID(chatID) || !mobileValidID(momentID) {
		mobileJSON(w, http.StatusBadRequest, nil, "the chat or message id is not valid")
		return false
	}
	store := bridge.app.storeForMobile()
	if store == nil || bridge.app.engagementService() == nil {
		mobileJSON(w, http.StatusServiceUnavailable, nil, errEngagementUnavailable.Error())
		return false
	}
	chat, found, err := store.MobileChat(bridge.ctx, chatID)
	if err != nil {
		mobileJSON(w, http.StatusInternalServerError, nil, "the chat could not be read")
		return false
	}
	if !found || chat.Kind != storage.SessionKindMain {
		mobileJSON(w, http.StatusNotFound, nil, "the proactive message was not found")
		return false
	}
	if _, _, err := bridge.app.mainChatMeasuredItem(bridge.ctx, chatID, momentID); err != nil {
		mobileJSON(w, http.StatusNotFound, nil, "the proactive message was not found")
		return false
	}
	return true
}

func (bridge *mobileBridge) serveProactiveReceipt(w http.ResponseWriter, r *http.Request, device mobileDeviceRecord, chatID, momentID string) {
	var input struct {
		IdempotencyKey string `json:"idempotencyKey"`
		State          string `json:"state"`
	}
	if !mobileDecode(w, r, &input) {
		return
	}
	key, valid := mobileKey(device.DeviceID, input.IdempotencyKey)
	if !valid {
		mobileJSON(w, http.StatusBadRequest, nil, "an idempotency key of 1 to 128 bytes is required")
		return
	}
	switch input.State {
	case storage.EngagementReceiptDelivered, storage.EngagementReceiptOpened:
	default:
		mobileJSON(w, http.StatusBadRequest, nil, "only delivered and opened card receipts are accepted")
		return
	}
	if !bridge.validateProactiveChat(w, chatID, momentID) {
		return
	}
	bridge.runReceipted(w, device, key, r.Method+" "+r.URL.Path, func(w http.ResponseWriter) {
		if err := bridge.app.recordMainChatProactiveReceipt(momentID, input.State, engagement.ChannelPush); err != nil {
			mobileJSON(w, http.StatusConflict, nil, "the receipt could not be saved")
			return
		}
		mobileJSON(w, http.StatusOK, map[string]bool{"accepted": true}, "")
	})
}

func (bridge *mobileBridge) serveProactiveAction(w http.ResponseWriter, r *http.Request, device mobileDeviceRecord, chatID, momentID string) {
	var input struct {
		IdempotencyKey string `json:"idempotencyKey"`
		ActionID       string `json:"actionId"`
	}
	if !mobileDecode(w, r, &input) {
		return
	}
	key, valid := mobileKey(device.DeviceID, input.IdempotencyKey)
	if !valid {
		mobileJSON(w, http.StatusBadRequest, nil, "an idempotency key of 1 to 128 bytes is required")
		return
	}
	actionID := strings.TrimSpace(input.ActionID)
	if !mobileNoticeActionPattern.MatchString(actionID) {
		mobileJSON(w, http.StatusBadRequest, nil, "an action id is required")
		return
	}
	// This additive route only changes notification preferences. Other card
	// actions keep their established phone routes and scopes; chat:write must
	// never grant handoff creation or scheduling through a suggestion card.
	if actionID != engagement.ActionMuteKind {
		mobileJSON(w, http.StatusForbidden, nil, "this action is not available through the proactive mute route")
		return
	}
	if !mobileRequireScope(w, device, mobileScopePush) {
		return
	}
	if !bridge.validateProactiveChat(w, chatID, momentID) {
		return
	}
	bridge.runReceipted(w, device, key, r.Method+" "+r.URL.Path, func(w http.ResponseWriter) {
		state, err := bridge.app.resolveMainChatProactiveAction(momentID, actionID, engagement.ChannelPush, "", nil)
		if err != nil {
			mobileJSON(w, http.StatusConflict, nil, "the action is not available on this message")
			return
		}
		mobileJSON(w, http.StatusOK, map[string]string{"state": state}, "")
	})
}
