package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type wechatPrivacyTransport func(*http.Request) (*http.Response, error)

func (f wechatPrivacyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func wechatPrivacyResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

type wechatRefusalSignal struct {
	done chan struct{}
	once sync.Once
}

func (w *wechatRefusalSignal) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "[wechat] refused a message") {
		w.once.Do(func() { close(w.done) })
	}
	return len(p), nil
}

func newWeChatPrivacyMonitor(app *App) *wechatMonitor {
	return newWeChatMonitor(app, wechatCredentials{
		BaseURL: "https://wechat.invalid", BotToken: "test-token",
		connectorOwnerState: connectorOwnerState{OwnerID: "owner", OwnerPaired: true},
	})
}

func TestWeChatInboundLogsKeepStrangerTextAndFileNamesPrivate(t *testing.T) {
	for _, flag := range []string{"unset", "0", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			setContentLogDebugFlag(t, flag)
			output, _ := isolateActivityPrivacyLogs(t)
			runner := &connectorCountingRunner{}
			app := &App{runner: runner}
			monitor := newWeChatPrivacyMonitor(app)
			const secret = "private-stranger-message-cobalt"
			const filename = "private-stranger-file-amber.txt"
			msg := wechatMessage{FromUserID: "stranger", MessageType: wechatMessageTypeUser, CreateTimeMS: time.Now().UnixMilli(),
				ItemList: []wechatMessageItem{{Type: wechatItemTypeText, TextItem: &wechatTextItem{Text: secret}, FileName: filename}}}
			payload, err := json.Marshal(wechatGetUpdatesResponse{Msgs: []wechatMessage{msg}})
			if err != nil {
				t.Fatal(err)
			}
			refused := &wechatRefusalSignal{done: make(chan struct{})}
			log.SetOutput(io.MultiWriter(output, refused))
			polls := 0
			monitor.api.client.Transport = wechatPrivacyTransport(func(req *http.Request) (*http.Response, error) {
				polls++
				if polls == 1 {
					return wechatPrivacyResponse(req, string(payload)), nil
				}
				// Synchronize on the owner check, not a scheduling delay. The
				// timeout is only a deadlock guard for a broken poll lifecycle.
				select {
				case <-refused.done:
				case <-time.After(30 * time.Second):
					t.Error("stranger message never reached the owner check")
				}
				monitor.cancel()
				return wechatPrivacyResponse(req, `{"errcode":-14}`), nil
			})
			monitor.run()
			line := contentLogLine(t, output.String(), "[wechat]   item[0]:")
			assertActivityLogContentVisibility(t, line, flag == "1", secret, filename)
			for _, field := range []string{"text_bytes=", "file_bytes="} {
				if !strings.Contains(line, field) {
					t.Errorf("missing item size %s in %s", field, line)
				}
			}
			if runner.count() != 0 || !app.wechatLastActivity.IsZero() {
				t.Fatal("stranger message changed owner state or reached the runner")
			}
			if msg.ItemList[0].TextItem.Text != secret || msg.ItemList[0].FileName != filename {
				t.Fatal("logging changed the canonical message")
			}
		})
	}
}

func TestWeChatOwnerLogsKeepMessagePrivate(t *testing.T) {
	for _, flag := range []string{"unset", "0", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			setContentLogDebugFlag(t, flag)
			output, _ := isolateActivityPrivacyLogs(t)
			runner := &activityMarkdownTranslationFakeRunner{}
			app := &App{runner: runner, pendingTraceIDs: map[string]string{}, sessionTempDirs: map[string]string{}}
			monitor := newWeChatPrivacyMonitor(app)
			const secret = "private-owner-message-amber"
			monitor.handleMessage(context.Background(), wechatMessage{
				FromUserID: "owner", MessageType: wechatMessageTypeUser, CreateTimeMS: time.Now().UnixMilli(),
				ItemList: []wechatMessageItem{{Type: wechatItemTypeText, TextItem: &wechatTextItem{Text: secret}}},
			})
			line := contentLogLine(t, output.String(), "[wechat] received from owner:")
			assertActivityLogContentVisibility(t, line, flag == "1", secret)
			if !strings.Contains(line, "text_bytes=") || !strings.Contains(line, "images=0") {
				t.Errorf("missing message metadata: %s", line)
			}
			if runner.calls != 1 || !strings.Contains(runner.lastPrompt, secret) {
				t.Fatalf("logging changed canonical owner prompt: calls=%d prompt=%s", runner.calls, runner.lastPrompt)
			}
		})
	}
}

func TestWeChatFailureLogsKeepModelContentPrivate(t *testing.T) {
	for _, flag := range []string{"unset", "0", "1"} {
		t.Run("debug="+flag, func(t *testing.T) {
			setContentLogDebugFlag(t, flag)
			output, _ := isolateActivityPrivacyLogs(t)
			const secret = "private-wechat-provider-error-cobalt"
			runner := &suggestionErrorPrivacyRunner{err: errors.New(secret)}
			app := &App{runner: runner, pendingTraceIDs: map[string]string{}, sessionTempDirs: map[string]string{}}
			monitor := newWeChatPrivacyMonitor(app)
			monitor.api.client.Transport = wechatPrivacyTransport(func(req *http.Request) (*http.Response, error) {
				return wechatPrivacyResponse(req, `{"errcode":0}`), nil
			})
			monitor.handleMessage(context.Background(), wechatMessage{
				FromUserID: "owner", MessageType: wechatMessageTypeUser, CreateTimeMS: time.Now().UnixMilli(),
				ItemList: []wechatMessageItem{{Type: wechatItemTypeText, TextItem: &wechatTextItem{Text: "message"}}},
			})
			for _, prefix := range []string{"[wechat] agent finished:", "[wechat] sending error reply to owner:"} {
				line := contentLogLine(t, output.String(), prefix)
				assertActivityLogContentVisibility(t, line, flag == "1", secret)
				if !strings.Contains(line, "error_bytes=") {
					t.Errorf("missing error size: %s", line)
				}
			}
		})
	}
}
