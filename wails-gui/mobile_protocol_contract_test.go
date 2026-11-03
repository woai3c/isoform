package main

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestMobileTodayProtocolContract(t *testing.T) {
	raw, err := os.ReadFile("../mobile-protocol/PROTOCOL.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"`GET /today`", "`brief:read`", "`needsYou`", "`needsYouCount`", "`briefRequest`", "`readyAt`", "`noticeId`", "`createdAt`", "`kind`", "`priority`", "`noticePacing`", "`activeToday`", "`keepAwake`", "`instantPush`", "`briefSchedule`", `event: "today_changed"`} {
		if !strings.Contains(string(raw), token) {
			t.Fatalf("Today contract is missing %s", token)
		}
	}
}

func TestMobileProtocolDocumentedReceiptStatesMatchValidator(t *testing.T) {
	raw, err := os.ReadFile("../mobile-protocol/PROTOCOL.md")
	if err != nil {
		t.Fatal(err)
	}
	document := string(raw)
	var route string
	for _, line := range strings.Split(document, "\n") {
		if strings.HasPrefix(line, "| `POST /v1/notices/{noticeId}/receipts`") {
			route = line
			break
		}
	}
	field := regexp.MustCompile(`"state":([^,]+),`).FindStringSubmatch(route)
	if len(field) != 2 {
		t.Fatal("receipt route is missing its state contract")
	}
	var documented []string
	for _, match := range regexp.MustCompile(`"([a-z_]+)"`).FindAllStringSubmatch(field[1], -1) {
		documented = append(documented, match[1])
		if !mobileNoticeReceiptValid(match[1], "open") {
			t.Fatalf("documented receipt state %q is rejected by the hub", match[1])
		}
	}
	var accepted []string
	for _, state := range []string{"pending", "scheduled", "sent", "delivered", "opened", "acted", "dismissed", "muted", "read", ""} {
		if mobileNoticeReceiptValid(state, "open") {
			accepted = append(accepted, state)
		}
	}
	slices.Sort(documented)
	slices.Sort(accepted)
	if !slices.Equal(documented, accepted) {
		t.Fatalf("documented receipt states %v differ from accepted states %v", documented, accepted)
	}
	section := strings.SplitN(document, "### 14.6 Receipts (phone to relay)", 2)
	if len(section) != 2 {
		t.Fatal("receipt section is missing")
	}
	sectionText := strings.SplitN(section[1], "### 14.7", 2)[0]
	code := regexp.MustCompile("(?s)```json\\n(.*?)\\n```").FindStringSubmatch(sectionText)
	if len(code) != 2 {
		t.Fatal("receipt JSON example is missing")
	}
	var example struct {
		State  string `json:"state"`
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(code[1]), &example); err != nil {
		t.Fatal(err)
	}
	if !mobileNoticeReceiptValid(example.State, example.Action) {
		t.Fatalf("documented receipt example is rejected: %s", code[1])
	}
	if mobileNoticeReceiptValid("acted", "") || mobileNoticeReceiptValid("pending", "") {
		t.Fatal("receipt validator accepted a moment state or an actionless acted receipt")
	}
}
