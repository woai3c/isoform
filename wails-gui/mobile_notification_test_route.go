package main

import (
	"crypto/sha256"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// serveNotificationTest sends only to the requesting phone. A successful
// response confirms submission to the relay connection, not APNs delivery.
func (bridge *mobileBridge) serveNotificationTest(w http.ResponseWriter, r *http.Request, device mobileDeviceRecord) {
	var input struct {
		IdempotencyKey string `json:"idempotencyKey"`
		Language       string `json:"language"`
	}
	if !mobileDecode(w, r, &input) {
		return
	}
	key, ok := mobileKey(device.DeviceID, input.IdempotencyKey)
	if !ok {
		mobileJSON(w, http.StatusBadRequest, nil, "an idempotency key is required")
		return
	}
	switch input.Language {
	case "en", "zh-Hans":
	default:
		mobileJSON(w, http.StatusBadRequest, nil, "unsupported language")
		return
	}
	bridge.runReceipted(w, device, key, r.Method+" "+r.URL.Path, func(w http.ResponseWriter) {
		// The relay accepts canonical version 4 IDs. Derive one from the scoped
		// request key so a retry has the same notice identity.
		sum := sha256.Sum256([]byte("isoform:notification-test:" + key))
		var noticeID uuid.UUID
		copy(noticeID[:], sum[:16])
		noticeID[6] = (noticeID[6] & 0x0f) | 0x40
		noticeID[8] = (noticeID[8] & 0x3f) | 0x80
		id := noticeID.String()
		title, body := "Test from your computer", "This notification traveled through your computer and relay."
		if input.Language == "zh-Hans" {
			title, body = "来自电脑的测试", "这条通知经过你的电脑和中继送达。"
		}
		now := time.Now().UTC()
		notice := mobileNotice{ID: id, Kind: "notification_test", Title: title, Body: body,
			Category: "YANSU_MOMENT", Level: "active", CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
		if err := bridge.publishMobileNotice(notice, []mobileDeviceRecord{device}); err != nil {
			mobileJSON(w, http.StatusServiceUnavailable, nil, "the test could not be submitted")
			return
		}
		mobileJSON(w, http.StatusOK, map[string]string{"noticeId": id}, "")
	})
}
