package main

import "testing"

// The browser-mode RPC bridge is reachable by any local process while it is
// enabled. Pairing, device revocation and brief generation stay native-window
// only so a local page can never pair a phone or push a brief.
func TestMobileBindingsStayOffTheBrowserRPCAllowlist(t *testing.T) {
	for _, name := range []string{
		"GetMobileConnectionStatus",
		"GetTodayTopics",
		"ListMainChatTopicItems",
		"SendMainChatWithTopic",
		"EnableMobileConnection",
		"EnableMobileConnectionUsingRelay",
		"DisableMobileConnection",
		"GenerateMobileConnectionCode",
		"ConfirmMobilePairing",
		"RevokeMobileDevice",
		"GetMobileBriefSchedule",
		"SetMobileBriefSchedule",
		"GenerateMobileBriefNow",
	} {
		if browserRPCAllowlist[name] {
			t.Errorf("%s must not be callable through the browser RPC bridge", name)
		}
	}
}
