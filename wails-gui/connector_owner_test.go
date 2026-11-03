package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/slack-go/slack"
	"yansu-agent/agent"
)

// connectorCountingRunner counts the agent runs a connector starts.
type connectorCountingRunner struct {
	mu   sync.Mutex
	runs int
}

func (r *connectorCountingRunner) Init(context.Context) error { return nil }
func (r *connectorCountingRunner) IsReady() bool              { return true }
func (r *connectorCountingRunner) ExecuteTask(context.Context, *agent.TaskInput, agent.StreamCallback) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs++
	return "", nil
}
func (r *connectorCountingRunner) CancelRun(string) error                   { return nil }
func (r *connectorCountingRunner) RespondToPermission(string, string) error { return nil }
func (r *connectorCountingRunner) CancelPermission(string)                  {}
func (r *connectorCountingRunner) Shutdown() error                          { return nil }

func (r *connectorCountingRunner) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.runs
}

// newConnectorTestApp is an App with a scratch HOME, whose runner only counts runs.
func newConnectorTestApp(t *testing.T) (*App, *connectorCountingRunner) {
	t.Helper()
	setTestHome(t)
	runner := &connectorCountingRunner{}
	app := &App{
		runner:          runner,
		pendingTraceIDs: map[string]string{},
		sessionTempDirs: map[string]string{},
	}
	return app, runner
}

// fakeConnectorAPI answers every connector API call with a success body. It
// stands in for Slack, Feishu and WeChat so the tests send nothing for real.
func fakeConnectorAPI(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_, _ = io.Copy(io.Discard, req.Body)
		_, _ = w.Write([]byte(`{"ok":true,"ts":"1.1","code":0,"msg":"ok","errcode":0,"ret":0,` +
			`"tenant_access_token":"t-test","expire":7200,"data":{}}`))
	}))
	t.Cleanup(server.Close)
	return server
}

// connectorFakeDiscord answers Discord REST calls with an empty message.
type connectorFakeDiscord struct{}

func (connectorFakeDiscord) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"1","channel_id":"C","content":"ok"}`)),
		Request:    req,
	}, nil
}

// connectedBefore is the connect time used by the tests: a minute ago, so a
// message sent now is newer and one from an hour before is older.
func connectedBefore() time.Time {
	return time.Now().Add(-time.Minute)
}

// TestSlackLocalAnswersOnlyItsPairedOwner covers the Slack owner gate: the
// pairing message saves the owner and starts no run, strangers are refused once
// paired, and a message from before the connect time is ignored.
func TestSlackLocalAnswersOnlyItsPairedOwner(t *testing.T) {
	app, runner := newConnectorTestApp(t)
	connected := connectedBefore()
	creds := slackLocalCredentials{BotToken: "xoxb-test", AppToken: "xapp-test", BotUserID: "UBOT",
		ConnectedAt: connected.Format(time.RFC3339)}
	if _, err := ensureConnectorPairing(&creds.connectorOwnerState); err != nil {
		t.Fatal(err)
	}
	if err := saveSlackLocalCredentials(&creds); err != nil {
		t.Fatal(err)
	}
	server := fakeConnectorAPI(t)
	r := newSlackLocalReceiver(app, creds)
	r.api = slack.New(creds.BotToken, slack.OptionAPIURL(server.URL+"/"))
	ctx := context.Background()
	slackTS := func(at time.Time) string { return fmt.Sprintf("%d.000001", at.Unix()) }

	r.handleMessage(ctx, "USTRANGER", "D1", "what is on today", "", slackTS(time.Now()), "", "", "im")
	if runner.count() != 0 {
		t.Fatal("a stranger's message started a run before pairing")
	}

	r.handleMessage(ctx, "UOWNER", "D2", creds.PairingCode, "", slackTS(time.Now()), "", "", "im")
	saved := loadSlackLocalCredentials()
	if saved == nil || saved.OwnerID != "UOWNER" || !saved.OwnerPaired || saved.PairingCode != "" {
		t.Fatalf("saved credentials = %+v, want paired owner UOWNER and no code", saved)
	}
	if runner.count() != 0 {
		t.Fatal("the pairing message started a run")
	}

	r.handleMessage(ctx, "USTRANGER", "D1", "hello", "", slackTS(time.Now()), "", "", "im")
	if runner.count() != 0 {
		t.Fatal("a stranger's message started a run after pairing")
	}

	r.handleMessage(ctx, "UOWNER", "D2", "hello", "", slackTS(time.Now()), "", "", "im")
	if runner.count() != 1 {
		t.Fatalf("owner's message runs = %d, want 1", runner.count())
	}

	r.handleMessage(ctx, "UOWNER", "D2", "old", "", slackTS(connected.Add(-time.Hour)), "", "", "im")
	if runner.count() != 1 {
		t.Fatal("a message from before the app was connected started a run")
	}
}

// TestDiscordLocalAnswersOnlyItsPairedOwner covers the Discord owner gate. Only
// a direct message can pair, so a server message with the code is refused.
func TestDiscordLocalAnswersOnlyItsPairedOwner(t *testing.T) {
	app, runner := newConnectorTestApp(t)
	connected := connectedBefore()
	creds := discordLocalCredentials{BotToken: "discord-test", BotUserID: "BOT1",
		ConnectedAt: connected.Format(time.RFC3339)}
	if _, err := ensureConnectorPairing(&creds.connectorOwnerState); err != nil {
		t.Fatal(err)
	}
	if err := saveDiscordLocalCredentials(&creds); err != nil {
		t.Fatal(err)
	}
	r, err := newDiscordLocalReceiver(app, creds)
	if err != nil {
		t.Fatal(err)
	}
	r.session.Client = &http.Client{Transport: connectorFakeDiscord{}}
	message := func(author, content string, at time.Time, dm bool) *discordgo.MessageCreate {
		m := &discordgo.MessageCreate{Message: &discordgo.Message{
			ID: "m1", ChannelID: "C" + author, Content: content, Timestamp: at,
			Author: &discordgo.User{ID: author, Username: "user-" + author},
		}}
		if !dm {
			m.GuildID = "G1"
			m.Mentions = []*discordgo.User{{ID: "BOT1"}}
		}
		return m
	}

	r.onMessage(message("USTRANGER", "what is on today", time.Now(), true))
	if runner.count() != 0 {
		t.Fatal("a stranger's direct message started a run before pairing")
	}

	r.onMessage(message("UOWNER", "<@BOT1> "+creds.PairingCode, time.Now(), false))
	if saved := loadDiscordLocalCredentials(); saved == nil || saved.paired() {
		t.Fatal("a server message paired the bot")
	}

	r.onMessage(message("UOWNER", creds.PairingCode, time.Now(), true))
	saved := loadDiscordLocalCredentials()
	if saved == nil || saved.OwnerID != "UOWNER" || !saved.OwnerPaired || saved.PairingCode != "" {
		t.Fatalf("saved credentials = %+v, want paired owner UOWNER and no code", saved)
	}
	if runner.count() != 0 {
		t.Fatal("the pairing message started a run")
	}

	r.onMessage(message("USTRANGER", "hello", time.Now(), true))
	if runner.count() != 0 {
		t.Fatal("a stranger's direct message started a run after pairing")
	}

	r.onMessage(message("UOWNER", "hello", time.Now(), true))
	if runner.count() != 1 {
		t.Fatalf("owner's message runs = %d, want 1", runner.count())
	}

	r.onMessage(message("UOWNER", "old", connected.Add(-time.Hour), true))
	if runner.count() != 1 {
		t.Fatal("a message from before the bot was connected started a run")
	}
}

// TestFeishuLocalAnswersOnlyItsPairedOwner covers the Feishu owner gate. Only a
// direct (p2p) message can pair, and a message from before the connect time is
// ignored.
func TestFeishuLocalAnswersOnlyItsPairedOwner(t *testing.T) {
	app, runner := newConnectorTestApp(t)
	connected := connectedBefore()
	creds := feishuLocalCredentials{AppID: "cli_test", AppSecret: "secret", Brand: "feishu",
		ConnectedAt: connected.Format(time.RFC3339)}
	if _, err := ensureConnectorPairing(&creds.connectorOwnerState); err != nil {
		t.Fatal(err)
	}
	if err := saveFeishuLocalCredentials(&creds); err != nil {
		t.Fatal(err)
	}
	server := fakeConnectorAPI(t)
	r := newFeishuLocalReceiver(app, creds)
	r.poster.baseURL = server.URL
	ctx := context.Background()
	message := func(sender, chatType, text string, at time.Time) feishuLocalMessage {
		return feishuLocalMessage{
			SenderOpenID: sender, SenderType: "user", MessageID: "om_1", ChatID: "oc_1",
			ChatType: chatType, MessageType: "text", Content: `{"text":"` + text + `"}`,
			CreateTime: strconv.FormatInt(at.UnixMilli(), 10),
		}
	}

	r.handleMessage(ctx, message("ou_stranger", "p2p", "what is on today", time.Now()))
	if runner.count() != 0 {
		t.Fatal("a stranger's message started a run before pairing")
	}

	r.handleMessage(ctx, message("ou_owner", "group", creds.PairingCode, time.Now()))
	if saved := loadFeishuLocalCredentials("feishu"); saved == nil || saved.paired() {
		t.Fatal("a group message paired the app")
	}

	r.handleMessage(ctx, message("ou_owner", "p2p", creds.PairingCode, time.Now()))
	saved := loadFeishuLocalCredentials("feishu")
	if saved == nil || saved.OwnerID != "ou_owner" || !saved.OwnerPaired || saved.PairingCode != "" {
		t.Fatalf("saved credentials = %+v, want paired owner ou_owner and no code", saved)
	}
	if runner.count() != 0 {
		t.Fatal("the pairing message started a run")
	}

	r.handleMessage(ctx, message("ou_stranger", "p2p", "hello", time.Now()))
	if runner.count() != 0 {
		t.Fatal("a stranger's message started a run after pairing")
	}

	r.handleMessage(ctx, message("ou_owner", "p2p", "hello", time.Now()))
	if runner.count() != 1 {
		t.Fatalf("owner's message runs = %d, want 1", runner.count())
	}

	r.handleMessage(ctx, message("ou_owner", "p2p", "old", connected.Add(-time.Hour)))
	if runner.count() != 1 {
		t.Fatal("a message from before the app was connected started a run")
	}
}

// TestWeChatMonitorAnswersOnlyItsPairedOwner covers the WeChat owner gate: the
// pairing message saves the owner, strangers are refused once paired, and a
// message from before the connect time is ignored.
func TestWeChatMonitorAnswersOnlyItsPairedOwner(t *testing.T) {
	app, runner := newConnectorTestApp(t)
	connected := connectedBefore()
	server := fakeConnectorAPI(t)
	creds := wechatCredentials{BotToken: "wx-test", BaseURL: server.URL, BotID: "bot1", UserID: "me1",
		ConnectedAt: connected.Format(time.RFC3339)}
	if _, err := ensureConnectorPairing(&creds.connectorOwnerState); err != nil {
		t.Fatal(err)
	}
	if err := saveWeChatCredentials(&creds); err != nil {
		t.Fatal(err)
	}
	m := newWeChatMonitor(app, creds)
	ctx := context.Background()
	message := func(sender, text string, at time.Time) wechatMessage {
		return wechatMessage{
			FromUserID: sender, MessageType: wechatMessageTypeUser, CreateTimeMS: at.UnixMilli(),
			ItemList: []wechatMessageItem{{Type: wechatItemTypeText, TextItem: &wechatTextItem{Text: text}}},
		}
	}

	m.handleMessage(ctx, message("wx_stranger", "what is on today", time.Now()))
	if runner.count() != 0 {
		t.Fatal("a stranger's message started a run before pairing")
	}

	m.handleMessage(ctx, message("wx_owner", creds.PairingCode, time.Now()))
	saved := loadWeChatCredentials()
	if saved == nil || saved.OwnerID != "wx_owner" || !saved.OwnerPaired || saved.PairingCode != "" {
		t.Fatalf("saved credentials = %+v, want paired owner wx_owner and no code", saved)
	}
	if runner.count() != 0 {
		t.Fatal("the pairing message started a run")
	}

	m.handleMessage(ctx, message("wx_stranger", "hello", time.Now()))
	if runner.count() != 0 {
		t.Fatal("a stranger's message started a run after pairing")
	}

	m.handleMessage(ctx, message("wx_owner", "hello", time.Now()))
	if runner.count() != 1 {
		t.Fatalf("owner's message runs = %d, want 1", runner.count())
	}

	m.handleMessage(ctx, message("wx_owner", "old", connected.Add(-time.Hour)))
	if runner.count() != 1 {
		t.Fatal("a message from before the bot was connected started a run")
	}
}
