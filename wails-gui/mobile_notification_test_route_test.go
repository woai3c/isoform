package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestMobileNotificationDeliveryTestIsScopedAndReceipted(t *testing.T) {
	bridge, relay := mobileTestHub(t)
	_, store := mobileTestChatBridge(t)
	bridge.app.store = store
	phone, other := newMobileTestPhone(t), newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopePush})
	mobileTestAddDevice(t, bridge, other, []string{mobileScopePush})
	relay.acceptRegistration(t, mobileTestListed(phone, other))
	mobileTestWaitFor(t, "connected", func() bool { return bridge.connectionStatus().RelayState == mobileRelayConnected })
	records := mobileTestRecords(t, bridge, phone, other)
	status := mobileTestChatCall(bridge, records[0], http.MethodGet, "/status", "")
	var capabilities struct {
		Data struct {
			NotificationTest bool `json:"notificationTest"`
		} `json:"data"`
	}
	if status.Code != http.StatusOK || json.Unmarshal(status.Body.Bytes(), &capabilities) != nil || !capabilities.Data.NotificationTest {
		t.Fatalf("status capability = %d %s", status.Code, status.Body.String())
	}
	body := `{"idempotencyKey":"delivery-test-1","language":"en"}`
	denied := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeBriefRead), http.MethodPost, "/notifications/test", body)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("scope = %d", denied.Code)
	}
	bad := mobileTestChatCall(bridge, records[0], http.MethodPost, "/notifications/test", `{"idempotencyKey":"bad","language":"xx"}`)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("language = %d", bad.Code)
	}
	first := mobileTestChatCall(bridge, records[0], http.MethodPost, "/notifications/test", body)
	if first.Code != http.StatusOK {
		t.Fatalf("test = %d %s", first.Code, first.Body.String())
	}
	var response struct {
		Data struct {
			NoticeID string `json:"noticeId"`
		} `json:"data"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	frame := relay.nextFrame(t, "out")
	var out mobileNoticeFrame
	if err := json.Unmarshal(frame.raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Env.Dev != phone.deviceID || out.NoticeID != response.Data.NoticeID || out.Kind != "notification_test" {
		t.Fatalf("frame = %+v", out)
	}
	opened, err := openMobileNotice(mobileTestNoticeKey(t, phone, bridge.identity.Public, bridge.identity.HubID), bridge.identity.HubID, phone.deviceID, out.NoticeID, out.CT)
	if err != nil || opened.Title != "Test from your computer" {
		t.Fatalf("notice = %+v %v", opened, err)
	}
	replay := mobileTestChatCall(bridge, records[0], http.MethodPost, "/notifications/test", body)
	if replay.Code != http.StatusOK || replay.Body.String() != first.Body.String() {
		t.Fatalf("replay = %d %s", replay.Code, replay.Body.String())
	}
	relay.expectNoFrame(t, "out", 150*time.Millisecond)
}
