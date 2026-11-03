package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDaemonPersonalContextRequiresBoundOwnerAndPrivateAudience(t *testing.T) {
	for _, platform := range []string{"slack", "feishu", "discord", "telegram"} {
		t.Run(platform, func(t *testing.T) {
			shortGuardTestHome(t)
			app := newBindingsTestApp(t)
			runner := &connectorContextRunner{}
			app.runner = runner
			app.pendingTraceIDs = map[string]string{}
			app.sessionTempDirs = map[string]string{}
			const secret = "DAEMON_PRIVATE_SOUL_SENTINEL"
			path, err := soulFilePath()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(secret), 0o600); err != nil {
				t.Fatal(err)
			}
			d := newDaemonClient(app, platform, "", "", "team", "owner")
			run := d.handleSlackEvent
			privateTypes := []string{"p2p"}
			// Keep the thread discriminator equal across chat types so this also
			// proves the privacy namespace, independent of platform thread shapes.
			const channel, thread, event = "D1", "root", "reply"
			oldKey := d.externalConversationKey(channel, thread, event, "group")
			if err := app.store.SetUIState(context.Background(), "daemon:session:"+oldKey, "old-personal-session"); err != nil {
				t.Fatal(err)
			}
			appendAndSummarizeConversation(app.store, oldKey, "old question", "OLD_PRIVATE_HISTORY")
			for _, sender := range []string{"stranger", ""} {
				for _, chatType := range []string{"group", privateTypes[0]} {
					run("relay", sender, channel, thread, event, chatType, "question", nil)
					if runner.input != nil {
						t.Fatalf("non-owner %q started a %q run", sender, chatType)
					}
				}
			}
			for _, chatType := range []string{"group", "channel", "", "unknown"} {
				runner.input = nil
				run("relay", "owner", channel, thread, event, chatType, "question", nil)
				input := runner.input
				if input == nil {
					t.Fatalf("owner's %q message did not run", chatType)
				}
				if strings.Contains(input.Prompt+input.History, secret) || strings.Contains(input.History, "OLD_PRIVATE_HISTORY") || input.SessionID != "" {
					t.Fatalf("shared %q run reused personal context or an old session", chatType)
				}
				if !input.AutoApproveTools || !input.DenyExternalWrites {
					t.Fatal("shared run lost its unattended authority boundary")
				}
			}
			sharedDir := runner.input.WorkDir
			for _, chatType := range privateTypes {
				run("relay", "owner", channel, thread, event, chatType, "question", nil)
				if !strings.Contains(runner.input.Prompt, secret) || runner.input.SessionID != "old-personal-session" {
					t.Fatalf("owner's private %q conversation lost context or session continuity", chatType)
				}
				if runner.input.WorkDir == sharedDir {
					t.Fatal("shared run reused the personal scratch folder")
				}
			}
			sharedKey := connectorContextThreadKey(oldKey, false)
			if err := app.store.SetUIState(context.Background(), "daemon:session:"+sharedKey, "shared-session"); err != nil {
				t.Fatal(err)
			}
			run("relay", "owner", channel, thread, event, "group", "question", nil)
			if runner.input.SessionID != "shared-session" || strings.Contains(runner.input.Prompt, secret) {
				t.Fatal("shared continuation lost its isolated session")
			}
			d.userID = ""
			runner.input = nil
			run("relay", "owner", channel, thread, event, privateTypes[0], "question", nil)
			if runner.input != nil {
				t.Fatal("daemon without a bound owner started a run")
			}
		})
	}
}

func TestTelegramOwnerGroupMentionUsesSharedContext(t *testing.T) {
	shortGuardTestHome(t)
	app := newBindingsTestApp(t)
	runner := &connectorContextRunner{}
	app.runner = runner
	app.pendingTraceIDs = map[string]string{}
	app.sessionTempDirs = map[string]string{}
	r := newTelegramLocalReceiver(app, telegramLocalCredentials{
		BotID: 123, BotUsername: "isoform_test", OwnerUserID: 456, OwnerPaired: true,
		ConnectedAt: time.Now().Add(-time.Minute).Format(time.RFC3339),
	})
	msg := telegramMessage{
		MessageID: 1, Date: time.Now().Unix(), Text: "@isoform_test hello",
		From: &telegramUser{ID: 456}, Chat: telegramChat{ID: -100, Type: "supergroup"},
	}
	r.handleMessage(context.Background(), msg)
	if runner.input == nil || strings.Contains(runner.input.Prompt, "<soul-context>\n") {
		t.Fatal("owner group mention did not use a non-personal prompt")
	}
	if !strings.Contains(runner.input.WorkDir, "shared-context-v1") {
		t.Fatalf("group mention reused the legacy workspace: %s", runner.input.WorkDir)
	}
	runner.input = nil
	msg.From.ID = 789
	r.handleMessage(context.Background(), msg)
	if runner.input != nil {
		t.Fatal("non-owner group mention started a run")
	}
}
