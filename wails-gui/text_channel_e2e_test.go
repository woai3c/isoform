//go:build e2e_messages && darwin

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

// TestTextChannelSendsAMessage sends one real proactive moment through the
// engagement router and Messages to the number verified in Settings. It needs
// a signed-in Mac with Automation allowed; it is outside the default suite:
//
//	YANSU_TEST_TEXT_CONFIG_DIR=/path/to/test-data go test -tags e2e_messages -run '^TestTextChannelSendsAMessage$' -count=1 .
func TestTextChannelSendsAMessage(t *testing.T) {
	dir := os.Getenv("YANSU_TEST_TEXT_CONFIG_DIR")
	if dir == "" {
		t.Fatal("set YANSU_TEST_TEXT_CONFIG_DIR to the data directory verified in Settings")
	}
	channel := newTextChannel(dir)
	status, err := channel.Status()
	if err != nil || !status.Verified || !channel.Available() {
		t.Fatalf("verify your own number, allow Messages and turn on texts in Settings first: %v", err)
	}
	st, err := storage.New(filepath.Join(t.TempDir(), "engagement.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sms := newSMSChannel(t.TempDir()) // no relay or phone pairing
	svc := newEngagementService(st, func(time.Time) engagement.Presence {
		return engagement.Presence{MacIdle: time.Hour, TextAvailable: channel.Available()}
	}, engagementDelivery{Text: func(ctx context.Context, row storage.EngagementMomentRow, m engagement.Moment, v engagement.Verdict, text engagementText) error {
		return sendEngagementText(ctx, sms, channel, row, m, v, text)
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	row, err := svc.Submit(ctx, engagement.Moment{Kind: engagement.KindWorkBlocked, SubjectID: "real-messages-readiness", DueAt: time.Now().Add(time.Hour), HardDeadline: true})
	if err != nil {
		t.Fatal(err)
	}
	svc.Pass(ctx)
	stored, err := st.GetEngagementMoment(ctx, row.ID)
	if err != nil || stored.State != storage.EngagementStateSent || stored.Channel != "text" {
		t.Fatalf("proactive moment was not sent through Messages: %+v, %v", stored, err)
	}
	after, err := channel.Status()
	if err != nil || after.SentToday != status.SentToday+1 || after.LastError != "" {
		t.Fatalf("sent text did not consume the cap: %+v, %v", after, err)
	}
}
