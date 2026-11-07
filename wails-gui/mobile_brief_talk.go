package main

import (
	"log"
	"net/http"
)

// POST /briefs/{id}/conversation is the phone's "Talk about this brief" when
// the brief on screen has no conversation reference of its own. The phone asks
// the computer for that brief's context by its id, and the computer answers
// with identities only: the brief's revision (its conversationKey) and the
// main chat the conversation lives in, made here if it does not exist yet.
//
// The route reads the brief through the gates that GET /briefs/{id} and the
// delivered words of Talk already use, so a withheld or withdrawn brief gets a
// refusal with no text. It never makes a brief (no /brief/refresh), posts no
// opener and sends no words. Asking again returns the same answer.
func (bridge *mobileBridge) serveBriefConversation(w http.ResponseWriter, r *http.Request, device mobileDeviceRecord, briefID string) {
	if !mobileValidBriefID(briefID) {
		mobileJSON(w, http.StatusBadRequest, nil, "the brief id is not valid")
		return
	}
	if bridge.app == nil || bridge.briefSource() == nil {
		mobileJSON(w, http.StatusServiceUnavailable, nil, errMobileBriefUnavailable.Error())
		return
	}
	// The same read as GET /briefs/{id}: a brief the sealed copy does not show has no package.
	if _, ok := bridge.briefPackage(briefID); !ok {
		mobileJSON(w, http.StatusNotFound, nil, "brief not found")
		return
	}
	brief, ok := bridge.app.todayChatDeliveredBrief(briefID)
	if !ok || brief.BriefID != briefID || brief.ConversationKey == "" || !mobileBriefHasWords(brief) ||
		bridge.app.IsClosedEyesModeEnabled() {
		mobileJSON(w, http.StatusConflict, nil, mobileBriefChangedMessage)
		return
	}
	if bridge.app.storeForMobile() == nil {
		mobileJSON(w, http.StatusServiceUnavailable, nil, errMobileChatNotReady.Error())
		return
	}
	main, err := bridge.app.ensureMainChat(bridge.ctx)
	if err != nil || main == nil {
		log.Printf("[mobile-brief-chat] conversation failed: %v", err)
		mobileJSON(w, http.StatusInternalServerError, nil, "the conversation could not be opened")
		return
	}
	mobileJSON(w, http.StatusOK, map[string]any{
		"briefId":      brief.BriefID,
		"briefVersion": brief.ConversationKey,
		"chatId":       main.LocalID,
	}, "")
}
