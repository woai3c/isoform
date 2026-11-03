package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yansu-agent/agent"
)

// A capture runner exercises the connector's real prompt and session assembly
// without sending user data to a model or posting to an external chat service.
type connectorContextRunner struct {
	connectorCountingRunner
	input *agent.TaskInput
}

func (r *connectorContextRunner) ExecuteTask(_ context.Context, input *agent.TaskInput, _ agent.StreamCallback) (string, error) {
	r.input = input
	return "", nil
}

func TestConnectorSharedRepliesOmitPersonalContextAndOldSessions(t *testing.T) {
	for _, platform := range []string{"slack", "discord", "feishu", "telegram"} {
		t.Run(platform, func(t *testing.T) {
			shortGuardTestHome(t)
			app := newBindingsTestApp(t)
			runner := &connectorContextRunner{}
			app.runner = runner
			app.pendingTraceIDs = map[string]string{}
			app.sessionTempDirs = map[string]string{}
			const privateSoul = "PRIVATE_SOUL_SENTINEL"
			const privateRelationship = "PRIVATE_PEOPLE_SENTINEL"
			path, err := soulFilePath()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(privateSoul), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := app.CreateContact("Alice Example", privateRelationship, nil, nil); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			threadKey := platform + ":owner-thread"
			if err := app.store.SetUIState(ctx, "daemon:session:"+threadKey, "old-private-session"); err != nil {
				t.Fatal(err)
			}
			appendAndSummarizeConversation(app.store, threadKey, "old question", "PRIVATE_HISTORY_SENTINEL")
			owner := connectorOwnerState{OwnerID: "owner", OwnerPaired: true}
			var run func(context.Context, string, string, string) string
			privateType, sharedType := "im", "channel"
			switch platform {
			case "slack":
				r := &slackLocalReceiver{app: app, creds: slackLocalCredentials{connectorOwnerState: owner}}
				run = r.runAgent
			case "discord":
				r := &discordLocalReceiver{app: app, creds: discordLocalCredentials{connectorOwnerState: owner}}
				run = r.runAgent
			case "feishu":
				r := &feishuLocalReceiver{app: app, creds: feishuLocalCredentials{connectorOwnerState: owner}}
				run = r.runAgent
				privateType, sharedType = "p2p", "group"
			case "telegram":
				r := &telegramLocalReceiver{app: app, creds: telegramLocalCredentials{OwnerUserID: 1, OwnerPaired: true}}
				run = r.runAgent
				privateType, sharedType = "private", "supergroup"
			}
			const query = "What do I know about Alice Example?"
			run(ctx, threadKey, query, sharedType)
			shared := runner.input
			if shared == nil {
				t.Fatal("shared conversation did not start a run")
			}
			for _, secret := range []string{privateSoul, privateRelationship, "PRIVATE_HISTORY_SENTINEL", "<memory-context>\n", "<initiative-context>\n", "<people-context>\n"} {
				if strings.Contains(shared.Prompt+shared.History, secret) {
					t.Errorf("shared input contains personal context %q", secret)
				}
			}
			if shared.SessionID != "" || shared.History != "" {
				t.Fatalf("shared input reused old session/history: session=%q history=%q", shared.SessionID, shared.History)
			}
			if !strings.Contains(shared.Prompt, query) || !shared.AutoApproveTools || !shared.DenyExternalWrites {
				t.Fatal("shared run lost its question or unattended authority boundary")
			}

			// A private DM preserves the owner's personal context and continuity.
			run(ctx, threadKey, query, privateType)
			private := runner.input
			for _, secret := range []string{privateSoul, privateRelationship} {
				if !strings.Contains(private.Prompt, secret) {
					t.Errorf("private DM lost personal context %q", secret)
				}
			}
			if private.SessionID != "old-private-session" || !strings.Contains(private.History, "PRIVATE_HISTORY_SENTINEL") {
				t.Fatal("private DM lost its existing session/history")
			}
			if shared.WorkDir == private.WorkDir {
				t.Fatal("shared run reused the old personal scratch folder")
			}

			// Safe shared turns still retain their own history and model session.
			sharedKey := connectorContextThreadKey(threadKey, false)
			if err := app.store.SetUIState(ctx, "daemon:session:"+sharedKey, "shared-session"); err != nil {
				t.Fatal(err)
			}
			appendAndSummarizeConversation(app.store, sharedKey, "shared question", "public reply")
			run(ctx, threadKey, query, sharedType)
			if runner.input.SessionID != "shared-session" || !strings.Contains(runner.input.History, "public reply") {
				t.Fatal("shared conversation lost its isolated continuity")
			}
		})
	}
}

func TestConnectorPersonalContextRequiresOwnerAndPrivateAudience(t *testing.T) {
	for _, paired := range []bool{false, true} {
		for _, chatType := range []string{"im", "p2p", "dm", "private", "channel", "group", "supergroup", "", "unknown"} {
			want := paired && (chatType == "im" || chatType == "p2p" || chatType == "dm" || chatType == "private")
			if got := connectorPersonalContext(paired, chatType); got != want {
				t.Errorf("paired=%v type=%q: got %v, want %v", paired, chatType, got, want)
			}
		}
	}
}
