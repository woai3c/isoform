package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/interests"
	"wails-gui/internal/pii"
	"wails-gui/storage"
)

// Offer routes use real isolated activity, topics and receipt stores. None of
// these helpers starts a monitor, application or model runner.
func mobileTestOfferBridge(t *testing.T) *mobileBridge {
	t.Helper()
	app := newMCPReadTestApp(t)
	t.Setenv(agentHomeEnv, t.TempDir())
	seedMCPBriefSources(t, app)
	now := time.Now()
	if _, err := app.topicsSvc().store.update(func(st *topicsStateFile) {
		st.Finds.Items[0].Topic = "Project tools"
		st.Finds.Items[0].LookedAt = now.AddDate(0, 0, -1).Format(time.RFC3339)
	}); err != nil {
		t.Fatal(err)
	}
	saveProposedAutomation(t, app.activity.store, "idea", "Weekly project summary", 0.8, "manual", []time.Time{now.AddDate(0, 0, -3), now.AddDate(0, 0, -2), now.AddDate(0, 0, -1)})
	for _, id := range []string{"idea-source-0", "idea-source-1", "idea-source-2"} {
		seedMCPReadActivity(t, app, id, "Editor", "com.example.editor", "Project summary", "", "done")
	}
	if _, err := app.store.CreateEngagementMoment(t.Context(), storage.EngagementMomentRow{
		ID: "idea-moment", Kind: "suggestion", SubjectID: "idea", DedupeKey: "test-idea", ExpiresAt: now.Add(time.Hour).UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
	bridge, err := newMobileBridge(app, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	return bridge
}

func mobileTestOffers(t *testing.T, bridge *mobileBridge) []mobileOffer {
	t.Helper()
	response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeOffersRead), http.MethodGet, "/offers", "")
	if response.Code != http.StatusOK {
		t.Fatalf("offers returned %d: %s", response.Code, response.Body.String())
	}
	var view mobileOfferList
	if err := json.Unmarshal(mobileTestDecodeReply(t, response).Data, &view); err != nil {
		t.Fatal(err)
	}
	return view.Offers
}

func mobileTestOfferSend(t *testing.T, bridge *mobileBridge, device mobileDeviceRecord, key, text string, refs []mobileOfferReference) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(mobileChatMessageInput{IdempotencyKey: key, Text: text, OfferReferences: refs})
	if err != nil {
		t.Fatal(err)
	}
	path := "/chats/own-chat/messages"
	if len(refs) != 0 {
		path = "/chats/own-chat/offer-messages"
	}
	return mobileTestChatCall(bridge, device, http.MethodPost, path, string(body))
}

func mobileTestOfferRunner(t *testing.T, bridge *mobileBridge) *mobileTestTurns {
	t.Helper()
	// The source-reader fixture has a chat but no sidebar workspace. A phone
	// chat must be visible through the normal project/workspace join.
	if err := bridge.app.store.EnsureProjectStructure(t.Context(), storage.ProjectRow{ID: "project", Name: "Project", Path: t.TempDir()}, []storage.WorkspaceRow{{ID: "offer-workspace", ProjectID: "project", Label: "Main", IsDefault: true}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := bridge.app.store.SaveSession(t.Context(), storage.SessionRow{LocalID: "own-chat", ProjectID: "project", WorkspaceID: "offer-workspace", Kind: storage.SessionKindMain, Label: "Main chat"}); err != nil {
		t.Fatal(err)
	}
	turns := &mobileTestTurns{}
	mobileChatRunnerOverride = mobileTestMessageOverride(turns)
	t.Cleanup(func() {
		mobileChatOfferDispatchHook = nil
		mobileChatOfferCommitHook = nil
		mobileTestResetChatRunner(t, bridge)
	})
	return turns
}

func TestMobileOfferChatPreservesReferenceAndRedactsEditedText(t *testing.T) {
	for _, index := range []int{0, 1} {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			bridge := mobileTestOfferBridge(t)
			turns := mobileTestOfferRunner(t, bridge)
			offer := mobileTestOffers(t, bridge)[index]
			if len(offer.Version) != 64 || offer.WhyLocalized["en"] != offer.Why || offer.WhyLocalized["zh-CN"] == "" {
				t.Fatalf("missing stable reference/localized copy: %+v", offer)
			}
			if again := mobileTestOffers(t, bridge)[index]; again.Version != offer.Version {
				t.Fatal("unchanged offer has unstable version")
			}
			refs := []mobileOfferReference{{ID: offer.ID, Version: offer.Version}}
			edited := offer.Prompt + "\nPlease use " + activityPrivacySecret + " for the draft."
			device := mobileTestDevice(mobileScopeChatWrite, mobileScopeOffersRead)
			response := mobileTestOfferSend(t, bridge, device, "edited", edited, refs)
			if response.Code != http.StatusAccepted {
				t.Fatalf("send = %d %s", response.Code, response.Body.String())
			}
			mobileTestWaitFor(t, "source-derived turn completes", func() bool { return turns.count() == 1 && !bridge.chatRunning("own-chat") })
			rows, err := bridge.app.store.LoadStreamItems(t.Context(), "own-chat")
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 2 || rows[0].Kind != "user" || rows[0].Content != redactActivityModelText(edited) || strings.Contains(rows[0].Content, activityPrivacySecret) {
				t.Fatalf("stored edit = %+v", rows)
			}
			var origin struct {
				OfferReferences []mobileOfferReference `json:"offerReferences"`
				JobID           string                 `json:"jobId"`
				Output          string                 `json:"output"`
			}
			if err := json.Unmarshal([]byte(rows[0].AutomationContext), &origin); err != nil || len(origin.OfferReferences) != 1 || origin.OfferReferences[0] != refs[0] || origin.JobID != "" || origin.Output != "" {
				t.Fatalf("source reference was lost or became automation output: %+v %v", origin, err)
			}
			turns.mu.Lock()
			got := turns.prompts[0]
			turns.mu.Unlock()
			if got != rows[0].Content {
				t.Fatalf("runner got unfiltered edit: %q", got)
			}
		})
	}
}

func TestMobileOfferChatRejectsWithdrawnAndChangedSources(t *testing.T) {
	for _, scenario := range []string{"suggestion deleted", "find deleted", "hidden", "paused", "description changed", "observation replaced", "topic provenance replaced", "topic source replaced", "edited mail subject", "scope revoked"} {
		t.Run(scenario, func(t *testing.T) {
			bridge := mobileTestOfferBridge(t)
			turns := mobileTestOfferRunner(t, bridge)
			offers := mobileTestOffers(t, bridge)
			offer := offers[0]
			if scenario == "find deleted" || strings.HasPrefix(scenario, "topic ") {
				offer = offers[1]
			}
			refs := []mobileOfferReference{{ID: offer.ID, Version: offer.Version}}
			text := offer.Prompt + "\nMake this useful for today."
			device := mobileTestDevice(mobileScopeChatWrite, mobileScopeOffersRead)
			want := http.StatusConflict
			switch scenario {
			case "suggestion deleted":
				if err := bridge.app.activity.store.DeleteSession("idea-source-2"); err != nil {
					t.Fatal(err)
				}
			case "find deleted":
				if err := bridge.app.activity.store.DeleteSession("brief-capture"); err != nil {
					t.Fatal(err)
				}
			case "hidden":
				if err := bridge.app.store.SetUIState(t.Context(), "sensitive_apps", `{"com.example.editor":"Editor"}`); err != nil {
					t.Fatal(err)
				}
			case "paused":
				bridge.app.closedEyes.enabled.Store(true)
			case "description changed":
				item, err := bridge.app.activity.store.GetInitiative("idea")
				if err != nil {
					t.Fatal(err)
				}
				item.ExecutionPlan.Automation.Description += " Updated scope."
				if _, err := bridge.app.activity.store.db.Exec(`UPDATE initiatives SET execution_plan_json=? WHERE id='idea'`, encodeInitiativeJSON(item.ExecutionPlan, "{}")); err != nil {
					t.Fatal(err)
				}
			case "observation replaced":
				seedMCPReadActivity(t, bridge.app, "replacement-source", "Editor", "com.example.editor", "Same visible text", "", "done")
				if _, err := bridge.app.activity.store.db.Exec(`UPDATE initiative_observations SET source_id='replacement-source' WHERE source_id='idea-source-2'`); err != nil {
					t.Fatal(err)
				}
			case "topic provenance replaced":
				if _, err := bridge.app.topicsSvc().store.update(func(st *topicsStateFile) {
					st.Finds.Sent[0].ReadSources.Evidence = []interests.Ref{{Source: interests.SourceChat, Key: "chat:own-chat"}}
				}); err != nil {
					t.Fatal(err)
				}
			case "topic source replaced":
				seedMCPReadActivity(t, bridge.app, "replacement-source", "Editor", "com.example.editor", "Same visible text", "", "done")
				if _, err := bridge.app.activity.store.db.Exec(`UPDATE activity_timeline_groups SET session_ids='["replacement-source"]' WHERE id='brief-run'`); err != nil {
					t.Fatal(err)
				}
			case "edited mail subject":
				subject := "Confidential payroll decision for board review"
				if _, err := bridge.app.store.UpsertMailMessages(t.Context(), []storage.MailMessageRow{{ID: "private-mail", Mailbox: storage.MailboxInbox, Subject: subject, At: time.Now().UnixMilli()}}, time.Now().UnixMilli()); err != nil {
					t.Fatal(err)
				}
				text += "\n" + subject
			case "scope revoked":
				device = mobileTestDevice(mobileScopeChatWrite)
				want = http.StatusForbidden
			}
			if scenario != "edited mail subject" && scenario != "scope revoked" {
				changedButReadable := scenario == "description changed" || scenario == "observation replaced" || strings.HasPrefix(scenario, "topic ")
				foundCurrent := false
				for _, current := range mobileTestOffers(t, bridge) {
					if current.ID == offer.ID && current.Version == offer.Version {
						t.Fatal("withdrawal/change failed to invalidate original source reference")
					}
					if current.ID == offer.ID {
						foundCurrent = true
						if scenario != "description changed" && current.Prompt != offer.Prompt {
							t.Fatal("source replacement fixture unexpectedly changed visible text")
						}
					}
				}
				if changedButReadable && !foundCurrent {
					t.Fatal("readable source replacement vanished instead of receiving a new version")
				}
			}
			response := mobileTestOfferSend(t, bridge, device, "queued-after-read", text, refs)
			if response.Code != want {
				t.Fatalf("source-derived send = %d %s, want %d", response.Code, response.Body.String(), want)
			}
			rows, err := bridge.app.store.LoadStreamItems(t.Context(), "own-chat")
			if err != nil || len(rows) != 0 || turns.count() != 0 {
				t.Fatalf("rejected source was published/dispatched: %+v %v (%d turns)", rows, err, turns.count())
			}
		})
	}
}

func TestMobileOfferChatQueuedBusyRetryRechecksWithdrawnSource(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	turns := mobileTestOfferRunner(t, bridge)
	offer := mobileTestOffers(t, bridge)[0]
	refs := []mobileOfferReference{{ID: offer.ID, Version: offer.Version}}
	device := mobileTestDevice(mobileScopeChatWrite, mobileScopeOffersRead)
	if response := mobileTestOfferSend(t, bridge, device, "hold", "hold", nil); response.Code != http.StatusAccepted {
		t.Fatalf("hold: %d %s", response.Code, response.Body.String())
	}
	mobileTestWaitFor(t, "ordinary held turn", func() bool { return turns.count() == 1 })
	queued := mobileTestOfferSend(t, bridge, device, "offline-queued-key", offer.Prompt, refs)
	if queued.Code != http.StatusConflict || !strings.Contains(queued.Body.String(), "busy") {
		t.Fatalf("initial queue attempt: %d %s", queued.Code, queued.Body.String())
	}
	if err := bridge.app.activity.store.DeleteSession("idea-source-2"); err != nil {
		t.Fatal(err)
	}
	bridge.cancelChatTurn("own-chat")
	mobileTestWaitFor(t, "held turn stopped", func() bool { return !bridge.chatRunning("own-chat") })
	for i := 0; i < 2; i++ {
		retry := mobileTestOfferSend(t, bridge, device, "offline-queued-key", offer.Prompt, refs)
		if retry.Code != http.StatusConflict || !strings.Contains(retry.Body.String(), "no longer available") {
			t.Fatalf("retry admitted withdrawn queue: %d %s", retry.Code, retry.Body.String())
		}
	}
	rows, err := bridge.app.store.LoadStreamItems(t.Context(), "own-chat")
	if err != nil || len(rows) != 1 || rows[0].Content != "hold" || turns.count() != 1 {
		t.Fatalf("queued edit was published: %+v %v, dispatches=%d", rows, err, turns.count())
	}
}

func TestMobileOfferChatRechecksAtDispatchAndRollsBack(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	turns := mobileTestOfferRunner(t, bridge)
	offer := mobileTestOffers(t, bridge)[0]
	mobileChatOfferDispatchHook = func() {
		rows, err := bridge.app.store.LoadStreamItems(t.Context(), "own-chat")
		if err != nil || len(rows) != 0 {
			t.Fatalf("source-derived text became durable before final admission: %+v %v", rows, err)
		}
		if err := bridge.app.activity.store.DeleteSession("idea-source-2"); err != nil {
			t.Fatal(err)
		}
	}
	response := mobileTestOfferSend(t, bridge, mobileTestDevice(mobileScopeChatWrite, mobileScopeOffersRead), "dispatch-race", offer.Prompt, []mobileOfferReference{{ID: offer.ID, Version: offer.Version}})
	if response.Code != http.StatusConflict {
		t.Fatalf("dispatch race: %d %s", response.Code, response.Body.String())
	}
	rows, err := bridge.app.store.LoadStreamItems(t.Context(), "own-chat")
	if err != nil || len(rows) != 0 || turns.count() != 0 {
		t.Fatalf("withdrawn dispatch left durable rows: %+v %v, turns=%d", rows, err, turns.count())
	}
}

func TestMobileOfferChatRejectsMalformedReferences(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	turns := mobileTestOfferRunner(t, bridge)
	offer := mobileTestOffers(t, bridge)[0]
	valid := mobileOfferReference{ID: offer.ID, Version: offer.Version}
	for _, suffix := range []string{"/offer-messages", "/offer-messages/"} {
		missing := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatWrite, mobileScopeOffersRead), http.MethodPost, "/chats/own-chat"+suffix, `{"idempotencyKey":"missing-source","text":"cached idea"}`)
		if missing.Code != http.StatusBadRequest {
			t.Fatalf("route %s accepted missing provenance: %d %s", suffix, missing.Code, missing.Body.String())
		}
	}
	cases := [][]mobileOfferReference{
		{{ID: offer.ID}}, {{ID: "unknown.idea", Version: offer.Version}}, {{ID: "find.", Version: offer.Version}},
		{{ID: offer.ID, Version: strings.Repeat("x", 64)}}, {valid, valid},
		make([]mobileOfferReference, mobileOfferReferencesMax+1),
	}
	for i, refs := range cases {
		response := mobileTestOfferSend(t, bridge, mobileTestDevice(mobileScopeChatWrite, mobileScopeOffersRead), strconv.Itoa(i), offer.Prompt, refs)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("malformed references %d: %d %s", i, response.Code, response.Body.String())
		}
	}
	if turns.count() != 0 {
		t.Fatal("malformed reference dispatched a turn")
	}
}

func TestMobileOfferChatSourceChangeDuringInsertNeverPublishes(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	turns := mobileTestOfferRunner(t, bridge)
	offer := mobileTestOffers(t, bridge)[0]
	checked := false
	mobileChatOfferCommitHook = func() {
		// INSERT has run on its writer connection. A separate phone read must
		// still see no row while validation/commit is outstanding.
		read := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			read <- mobileTestChatCall(bridge, mobileTestDevice(mobileScopeChatRead), http.MethodGet, "/chats/own-chat", "")
		}()
		select {
		case response := <-read:
			if response.Code != http.StatusOK {
				t.Fatalf("concurrent read: %d %s", response.Code, response.Body.String())
			}
			var detail mobileTestChatDetail
			if err := json.Unmarshal(mobileTestDecodeReply(t, response).Data, &detail); err != nil || len(detail.Items) != 0 {
				t.Fatalf("unadmitted source text was visible: %+v %v", detail, err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("transaction exhausted the normal store's read connection")
		}
		// Source observations can be corrected while a row is uncommitted.
		// Explicit DeleteSession is fenced with admission and is covered by
		// the before-admission withdrawal test; do not re-enter that mutex.
		if _, err := bridge.app.activity.store.db.Exec(`UPDATE initiative_observations SET source_id='withdrawn-source' WHERE source_id='idea-source-2'`); err != nil {
			t.Fatal(err)
		}
		checked = true
	}
	response := mobileTestOfferSend(t, bridge, mobileTestDevice(mobileScopeChatWrite, mobileScopeOffersRead), "insertion-withdrawal", offer.Prompt, []mobileOfferReference{{ID: offer.ID, Version: offer.Version}})
	if !checked || response.Code != http.StatusConflict {
		t.Fatalf("withdrawal during insertion: checked=%v %d %s", checked, response.Code, response.Body.String())
	}
	rows, err := bridge.app.store.LoadStreamItems(t.Context(), "own-chat")
	if err != nil || len(rows) != 0 || turns.count() != 0 {
		t.Fatalf("rejected transaction published: %+v %v, turns=%d", rows, err, turns.count())
	}
}

// Summary publication owns the activity privacy fence while reading policy
// through the normal Store. Admission must wait for that fence before taking
// SQLite's writer lock, so another normal writer can still finish.
func TestMobileOfferChatSummaryFenceDoesNotBlockNormalWriter(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	mobileTestOfferRunner(t, bridge)
	offer := mobileTestOffers(t, bridge)[0]
	admitting, inserted := make(chan struct{}), make(chan struct{})
	mobileChatOfferDispatchHook = func() { close(admitting) }
	mobileChatOfferCommitHook = func() { close(inserted) }
	response := make(chan *httptest.ResponseRecorder, 1)
	fenceErr := bridge.app.activity.store.withSummarySources(activitySummarySourceSet{}, func() error {
		go func() {
			response <- mobileTestOfferSend(t, bridge, mobileTestDevice(mobileScopeChatWrite, mobileScopeOffersRead), "summary-fence", offer.Prompt, []mobileOfferReference{{ID: offer.ID, Version: offer.Version}})
		}()
		select {
		case <-admitting:
		case <-time.After(3 * time.Second):
			return errors.New("admission did not reach the privacy fence")
		}
		// On the old lock order INSERT reaches its hook while the summary
		// still owns the fence. The corrected order waits before INSERT.
		insertedBeforeFence := false
		select {
		case <-inserted:
			insertedBeforeFence = true
		case <-time.After(time.Second):
		}
		writeResult := make(chan error, 1)
		go func() { writeResult <- bridge.app.store.SetUIState(t.Context(), "admission-lock-probe", "saved") }()
		allowed := bridge.app.summarySourcesAllowRead(nil)
		writeErr := <-writeResult
		if writeErr != nil {
			return fmt.Errorf("admission blocked the writer while summary held its privacy fence: %w", writeErr)
		}
		if !allowed {
			return errors.New("summary could not re-read privacy policy")
		}
		if insertedBeforeFence {
			return errors.New("admission acquired SQLite's writer lock before the summary privacy fence")
		}
		return nil
	})
	select {
	case result := <-response:
		if result.Code != http.StatusAccepted {
			t.Fatalf("admission after summary: %d %s", result.Code, result.Body.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("admission did not finish after summary released its fence")
	}
	if fenceErr != nil {
		t.Fatal(fenceErr)
	}
}

func TestMobileOfferChatReadOnlyAdmissionKeepsLivePauseGate(t *testing.T) {
	for _, when := range []string{"before_read", "during_read"} {
		t.Run(when, func(t *testing.T) {
			bridge := mobileTestOfferBridge(t)
			turns := mobileTestOfferRunner(t, bridge)
			offer := mobileTestOffers(t, bridge)[0]
			paused := false
			if when == "before_read" {
				mobileChatOfferCommitHook = func() {
					bridge.app.closedEyes.enabled.Store(true)
					paused = true
				}
			} else {
				originalMail := mobileGoalMailRows
				t.Cleanup(func() { mobileGoalMailRows = originalMail })
				mobileGoalMailRows = func(reader *App) ([]storage.MailMessageRow, error) {
					if reader != bridge.app {
						if reader.store == bridge.app.store {
							t.Fatal("admission reused the normal Store connection")
						}
						bridge.app.closedEyes.enabled.Store(true)
						paused = true
					}
					return originalMail(reader)
				}
			}
			response := mobileTestOfferSend(t, bridge, mobileTestDevice(mobileScopeChatWrite, mobileScopeOffersRead), "live-pause", offer.Prompt, []mobileOfferReference{{ID: offer.ID, Version: offer.Version}})
			if !paused || response.Code != http.StatusConflict {
				t.Fatalf("live pause was ignored: paused=%v %d %s", paused, response.Code, response.Body.String())
			}
			rows, err := bridge.app.store.LoadStreamItems(t.Context(), "own-chat")
			if err != nil || len(rows) != 0 || turns.count() != 0 {
				t.Fatalf("paused admission published: %+v %v, turns=%d", rows, err, turns.count())
			}
		})
	}
}

func TestMobileOfferFeedbackRecordsPhoneAttribution(t *testing.T) {
	for _, kind := range []string{"suggestion", "find"} {
		for _, phone := range []bool{true, false} {
			t.Run(kind+" phone="+strconv.FormatBool(phone), func(t *testing.T) {
				bridge := mobileTestOfferBridge(t)
				app := bridge.app
				app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
				momentID := "idea-moment"
				offerID := "suggestion.idea"
				if kind == "find" {
					momentID, offerID = "find-moment", "find.find-one"
					if _, err := app.store.CreateEngagementMoment(t.Context(), storage.EngagementMomentRow{ID: momentID, Kind: "topic_finds", SubjectID: "find-one", DedupeKey: "test-find"}); err != nil {
						t.Fatal(err)
					}
					if _, err := app.topicsSvc().store.update(func(st *topicsStateFile) { st.Finds.MomentID = momentID; st.Finds.NotifiedOn = st.Finds.RanOn }); err != nil {
						t.Fatal(err)
					}
				}
				if err := app.store.SetEngagementMomentState(t.Context(), momentID, storage.EngagementStateSent, "test", storage.EngagementMomentRow{Channel: "push", SentAt: time.Now().UnixMilli()}); err != nil {
					t.Fatal(err)
				}
				want := engagement.ChannelDesktop
				if phone {
					want = engagement.ChannelPush
					response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeOffersDecide), http.MethodPost, "/offers/"+offerID+"/decline", `{"idempotencyKey":"attribution","decision":"dismiss"}`)
					if response.Code != http.StatusOK {
						t.Fatalf("phone decline: %d %s", response.Code, response.Body.String())
					}
				} else if kind == "suggestion" {
					if _, err := app.ResolveTodaySuggestion(TodaySuggestionDecision{ID: "idea", Decision: "dismiss"}); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := app.recordTopicFindFeedback(TopicFindFeedbackInput{FindID: "find-one", Action: "less"}); err != nil {
						t.Fatal(err)
					}
				}
				receipts, err := app.store.ListEngagementReceipts(t.Context(), momentID)
				wantState, wantAction := storage.EngagementReceiptDismissed, "dismiss"
				if kind == "find" {
					wantState, wantAction = storage.EngagementReceiptOpened, "less"
				}
				if err != nil || len(receipts) != 3 || receipts[0].State != "created" || receipts[1].State != "sent" ||
					receipts[2].Channel != string(want) || receipts[2].State != wantState || receipts[2].Action != wantAction {
					t.Fatalf("attribution: %+v %v want channel=%s state=%s action=%s after lifecycle receipts", receipts, err, want, wantState, wantAction)
				}
			})
		}
	}
}

func TestMobileOffersProjectSuggestionsAndFindsWithoutExecution(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	offers := mobileTestOffers(t, bridge)
	if len(offers) != 2 || offers[0].ID != "suggestion.idea" || offers[1].ID != "find.find-one" {
		t.Fatalf("offers = %+v", offers)
	}
	if !strings.Contains(offers[0].Why, "3 times across 3 days") || !strings.Contains(offers[1].Prompt, "https://example.org/find") {
		t.Fatalf("observation and handoff text = %+v", offers)
	}
	if offers[1].Why != "You spent time on Project tools yesterday" || strings.Contains(offers[1].Why, "new project release") || !strings.Contains(offers[1].Prompt, "A new project release") {
		t.Fatalf("the find's why must explain the observation separately from the prompt: %+v", offers[1])
	}
	// The phone's evidence is one stated fact with its source and time (Phone Threads §2); the
	// initiative's own evidence list, quotes and source IDs never leave the Mac.
	if offers[0].Evidence == nil || offers[0].Evidence.Text != "You did this 3 times across 3 days." || offers[0].Evidence.At == "" ||
		offers[1].Evidence == nil || offers[1].Evidence.Source != "screen" || offers[1].Evidence.Text != offers[1].Why {
		t.Fatalf("offer evidence = %+v / %+v", offers[0].Evidence, offers[1].Evidence)
	}
	raw, _ := json.Marshal(offers)
	for _, excluded := range []string{"Never disclose", "Draft and send", "source-0", "executionPlan", `"evidence":[`, "sourceId", "quote"} {
		if strings.Contains(string(raw), excluded) {
			t.Fatalf("offer projected internal data %q", excluded)
		}
	}
	item, err := bridge.app.activity.store.GetInitiative("idea")
	if err != nil || item.Status != InitiativeStatusProposed {
		t.Fatalf("reading Ideas mutated the suggestion: %+v %v", item, err)
	}
}

func TestMobileOffersRequireScopesAndRejectApproval(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	for _, tc := range []struct{ method, path, body, scope string }{
		{http.MethodGet, "/offers", "", mobileScopeOffersRead},
		{http.MethodPost, "/offers/find-more", "", mobileScopeOffersRead},
		{http.MethodPost, "/handoffs/hf-test/start", `{"idempotencyKey":"start"}`, mobileScopeHandoff},
		{http.MethodPost, "/offers/suggestion.idea/decline", `{"idempotencyKey":"k","decision":"dismiss"}`, mobileScopeOffersDecide},
	} {
		response := mobileTestChatCall(bridge, mobileTestDevice(), tc.method, tc.path, tc.body)
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), tc.scope) {
			t.Fatalf("scope check = %d %s", response.Code, response.Body.String())
		}
	}
	for _, decision := range []string{"approve", "do_once", "add_to_upcoming", ""} {
		response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeOffersDecide), http.MethodPost, "/offers/suggestion.idea/decline", `{"idempotencyKey":"k","decision":"`+decision+`"}`)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("decision %q returned %d", decision, response.Code)
		}
	}
	response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeOffersDecide), http.MethodPost, "/offers/suggestion.idea/decline", `{"decision":"dismiss"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatal("decline accepted no idempotency key")
	}
}

func TestMobileOfferDeclineReplaysAndDoesNotResurface(t *testing.T) {
	for _, id := range []string{"suggestion.idea", "find.find-one"} {
		t.Run(id, func(t *testing.T) {
			bridge := mobileTestOfferBridge(t)
			device := mobileTestDevice(mobileScopeOffersDecide)
			path := "/offers/" + id + "/decline"
			body := `{"idempotencyKey":"one-decline","decision":"dismiss"}`
			first := mobileTestChatCall(bridge, device, http.MethodPost, path, body)
			second := mobileTestChatCall(bridge, device, http.MethodPost, path, body)
			if first.Code != http.StatusOK || first.Body.String() != second.Body.String() {
				t.Fatalf("decline/replay = %d %s / %d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
			}
			for _, offer := range mobileTestOffers(t, bridge) {
				if offer.ID == id {
					t.Fatal("dismissed idea resurfaced")
				}
			}
			other := mobileTestChatCall(bridge, device, http.MethodPost, "/offers/another/decline", body)
			if other.Code != http.StatusConflict {
				t.Fatal("idempotency key crossed routes")
			}
			if id == "find.find-one" {
				state := bridge.app.topicsSvc().store.load()
				if len(state.Finds.Feedback) != 1 || state.Finds.Feedback[0].Action != "less" || state.Overrides["interest"].Removed {
					t.Fatalf("decline should downweight once, without deleting topic: %+v", state.Finds)
				}
			}
		})
	}
}

func TestMobileOfferDeclineSerializesAcrossPhones(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	devices := []mobileDeviceRecord{mobileTestDevice(mobileScopeOffersDecide), mobileTestDevice(mobileScopeOffersDecide)}
	devices[1].DeviceID = "second-phone"
	codes := make(chan int, 2)
	var group sync.WaitGroup
	for _, device := range devices {
		group.Add(1)
		go func(device mobileDeviceRecord) {
			defer group.Done()
			response := mobileTestChatCall(bridge, device, http.MethodPost, "/offers/find.find-one/decline", `{"idempotencyKey":"decline","decision":"dismiss"}`)
			codes <- response.Code
		}(device)
	}
	group.Wait()
	close(codes)
	counts := map[int]int{}
	for code := range codes {
		counts[code]++
	}
	if counts[http.StatusOK] != 1 || counts[http.StatusNotFound] != 1 || len(bridge.app.topicsSvc().store.load().Finds.Feedback) != 1 {
		t.Fatalf("concurrent declines = %v", counts)
	}
}

func TestMobileOffersRecheckPrivacyAndUnknownSources(t *testing.T) {
	for _, reason := range []string{"closed eyes", "screen off", "missing suggestion source", "missing topic lineage", "withdrawn topic source", "expired suggestion", "unobserved link"} {
		t.Run(reason, func(t *testing.T) {
			bridge := mobileTestOfferBridge(t)
			app := bridge.app
			want := 1
			switch reason {
			case "closed eyes":
				app.closedEyes.enabled.Store(true)
				want = 0
			case "screen off":
				if err := app.store.SetUIState(t.Context(), permissionUIStateKey("screen_recording"), "false"); err != nil {
					t.Fatal(err)
				}
				want = 0
			case "missing suggestion source":
				if err := app.activity.store.DeleteSession("idea-source-2"); err != nil {
					t.Fatal(err)
				}
			case "missing topic lineage":
				if _, err := app.topicsSvc().store.update(func(st *topicsStateFile) { st.Finds.Sent[0].ReadSources = nil }); err != nil {
					t.Fatal(err)
				}
			case "withdrawn topic source":
				if err := app.activity.store.DeleteSession("brief-capture"); err != nil {
					t.Fatal(err)
				}
			case "expired suggestion":
				if err := app.store.SetEngagementMomentState(t.Context(), "idea-moment", storage.EngagementStateSuppressed, "expired", storage.EngagementMomentRow{}); err != nil {
					t.Fatal(err)
				}
			case "unobserved link":
				if _, err := app.topicsSvc().store.update(func(st *topicsStateFile) { st.Finds.Items[0].URL = "https://unobserved.invalid/private" }); err != nil {
					t.Fatal(err)
				}
			}
			if offers := mobileTestOffers(t, bridge); len(offers) != want {
				t.Fatalf("%s: %d offers, want %d: %+v", reason, len(offers), want, offers)
			}
		})
	}
}

func TestMobileOffersHoldMailSubjectsAndRedactSecrets(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	if _, err := bridge.app.store.UpsertMailMessages(t.Context(), []storage.MailMessageRow{{ID: "mail", Mailbox: storage.MailboxInbox, Subject: "Weekly project summary", At: time.Now().UnixMilli()}}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if offers := mobileTestOffers(t, bridge); len(offers) != 1 || offers[0].Kind != "topic_find" {
		t.Fatalf("mail subject hold = %+v", offers)
	}
	text := mobileOfferText(strings.Repeat("a", 195)+" "+activityPrivacySecret, 210)
	if strings.Contains(text, "sk-proj-") {
		t.Fatal("secret was truncated before redaction")
	}
}

func TestMobileOffersWithholdCalendarHoldTitles(t *testing.T) {
	for _, tc := range []struct {
		title   string
		allowed bool
	}{
		{"Hold today 2 to 4 PM for the Q4 plan", false},
		{"留出今天下午 2 点到 4 点", false},
		{"为Q4计划留出周一上午", false},
		{"Hold a block for the plan", false},
		{"Review the Q4 plan", true},
		{"Block pop-up ads", true},
	} {
		offer := mobileOffer{ID: "suggestion.test", Title: tc.title, Prompt: "Plan work"}
		_, allowed := mobileOfferForPhone(offer, nil)
		if allowed != tc.allowed {
			t.Errorf("%q allowed=%v, want %v", tc.title, allowed, tc.allowed)
		}
	}
}

func TestMobileOfferDeclineCannotChangeHeldOrMissingIdeas(t *testing.T) {
	for _, reason := range []string{"paused", "hidden source", "removed topic", "missing idea"} {
		t.Run(reason, func(t *testing.T) {
			bridge := mobileTestOfferBridge(t)
			id := "find.find-one"
			switch reason {
			case "paused":
				bridge.app.closedEyes.enabled.Store(true)
			case "hidden source":
				if err := bridge.app.store.SetUIState(t.Context(), "sensitive_apps", `{"com.example.editor":"Editor"}`); err != nil {
					t.Fatal(err)
				}
				id = "suggestion.idea"
			case "removed topic":
				if _, err := bridge.app.topicsSvc().store.update(func(st *topicsStateFile) {
					if st.Overrides == nil {
						st.Overrides = map[string]topicOverride{}
					}
					st.Overrides["interest"] = topicOverride{Removed: true}
				}); err != nil {
					t.Fatal(err)
				}
			case "missing idea":
				id = "find.no-longer-present"
			}
			response := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeOffersDecide), http.MethodPost, "/offers/"+id+"/decline", `{"idempotencyKey":"held","decision":"dismiss"}`)
			if response.Code != http.StatusNotFound {
				t.Fatalf("held idea returned %d: %s", response.Code, response.Body.String())
			}
			item, err := bridge.app.activity.store.GetInitiative("idea")
			if err != nil || item.Status != InitiativeStatusProposed || len(bridge.app.topicsSvc().store.load().Finds.Feedback) != 0 {
				t.Fatalf("held idea changed feedback: %+v %v", item, err)
			}
		})
	}
}

func TestMobileOffersCheckOriginalTextBeforeDisplayBounds(t *testing.T) {
	for _, field := range []string{"suggestion title", "suggestion description", "find title", "find detail"} {
		t.Run(field, func(t *testing.T) {
			bridge := mobileTestOfferBridge(t)
			subject := strings.Repeat("Confidential project roadmap ", 48) + "Board decision"
			if _, err := bridge.app.store.UpsertMailMessages(t.Context(), []storage.MailMessageRow{{ID: "long-mail", Mailbox: storage.MailboxInbox, Subject: subject, At: time.Now().UnixMilli()}}, time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			wantKind := "suggestion"
			if strings.HasPrefix(field, "suggestion") {
				item, err := bridge.app.activity.store.GetInitiative("idea")
				if err != nil {
					t.Fatal(err)
				}
				if field == "suggestion title" {
					item.ExecutionPlan.Automation.Name = subject
				} else {
					item.ExecutionPlan.Automation.Description = subject
				}
				if _, err := bridge.app.activity.store.db.Exec(`UPDATE initiatives SET execution_plan_json = ? WHERE id = ?`, encodeInitiativeJSON(item.ExecutionPlan, "{}"), item.ID); err != nil {
					t.Fatal(err)
				}
				wantKind = "topic_find"
			} else if _, err := bridge.app.topicsSvc().store.update(func(st *topicsStateFile) {
				if field == "find title" {
					st.Finds.Items[0].Title = subject
				} else {
					st.Finds.Items[0].WhatsNew = subject
				}
			}); err != nil {
				t.Fatal(err)
			}
			if offers := mobileTestOffers(t, bridge); len(offers) != 1 || offers[0].Kind != wantKind {
				t.Fatalf("%s escaped the full-subject hold after truncation: %+v", field, offers)
			}
		})
	}
	for _, field := range []string{"title", "description"} {
		t.Run("credential in "+field, func(t *testing.T) {
			bridge := mobileTestOfferBridge(t)
			item, err := bridge.app.activity.store.GetInitiative("idea")
			if err != nil {
				t.Fatal(err)
			}
			text := strings.Repeat("a", 150) + " " + activityPrivacySecret
			if field == "title" {
				item.ExecutionPlan.Automation.Name = text
			} else {
				item.ExecutionPlan.Automation.Description = text
			}
			if _, err := bridge.app.activity.store.db.Exec(`UPDATE initiatives SET execution_plan_json = ? WHERE id = ?`, encodeInitiativeJSON(item.ExecutionPlan, "{}"), item.ID); err != nil {
				t.Fatal(err)
			}
			offers := mobileTestOffers(t, bridge)
			raw, err := json.Marshal(offers)
			if err != nil {
				t.Fatal(err)
			}
			if len(offers) != 2 || strings.Contains(string(raw), "sk-proj-") || !strings.Contains(string(raw), "[REDACTED:") {
				t.Fatalf("credential crossed the Today card boundary before redaction: %s", raw)
			}
		})
	}
}

func TestMobileBriefItemKeysFollowTheSourceRecord(t *testing.T) {
	first := buildMobileBriefV1(mobileBriefGoldenInput())
	later := mobileBriefGoldenInput()
	later.AsOf = later.AsOf.Add(26 * time.Hour)
	// A later brief lists the same records in another order, with one more in front.
	later.Moved = append([]mobileBriefCandidate{{SourceKind: "history", SourceID: "h-new", Title: "Booked the venue",
		At: later.AsOf.Add(-time.Hour)}}, later.Moved[1], later.Moved[0])
	second := buildMobileBriefV1(later)

	keys := func(brief MobileBriefV1) map[string]string {
		out := map[string]string{}
		for _, item := range brief.Sections.Moved {
			out[item.Title] = item.Key
		}
		return out
	}
	before, after := keys(first), keys(second)
	for title, key := range before {
		if key == "" || after[title] != key {
			t.Fatalf("%q: key %q became %q in a later brief", title, key, after[title])
		}
	}
	if again := buildMobileBriefV1(mobileBriefGoldenInput()); !jsonEqual(t, again.Sections, first.Sections) {
		t.Fatal("rebuilding the same input changed the items")
	}

	seen := map[string]bool{}
	for name, items := range mobileBriefSectionsByName(t, first) {
		for _, item := range items {
			if !strings.HasPrefix(item.Key, name+".") || !mobileValidID(item.Key) {
				t.Fatalf("%s item %q has key %q", name, item.ID, item.Key)
			}
			if seen[item.Key] {
				t.Fatalf("key %q is used twice", item.Key)
			}
			seen[item.Key] = true
		}
	}
}

func TestMobileBriefItemKeysTellSectionsAndRepeatsApart(t *testing.T) {
	asOf := time.Date(2026, 10, 9, 14, 0, 0, 0, time.UTC)
	record := mobileBriefCandidate{SourceKind: "history", SourceID: "h-1", Title: "Sent the deck", At: asOf.Add(-time.Hour)}
	repeat := record
	repeat.Title = "Sent the deck again"
	brief := buildMobileBriefV1(mobileBriefInput{
		Location: time.UTC, AsOf: asOf,
		Moved: []mobileBriefCandidate{record, repeat},
		Doing: []mobileBriefCandidate{record},
	})
	moved, loops := brief.Sections.Moved, brief.Sections.Doing
	if len(moved) != 2 || len(loops) != 1 {
		t.Fatalf("sections = %+v", brief.Sections)
	}
	if moved[0].Key == loops[0].Key || !strings.HasPrefix(loops[0].Key, "doing.") {
		t.Fatalf("one record in two sections shares key %q", moved[0].Key)
	}
	if moved[1].Key != moved[0].Key+"-2" {
		t.Fatalf("a repeated record got %q after %q", moved[1].Key, moved[0].Key)
	}
}

func TestMobileBriefFindsCarryKeys(t *testing.T) {
	asOf := time.Date(2026, 10, 9, 15, 0, 0, 0, time.UTC)
	brief := mobileBriefWithFinds(buildMobileBriefV1(mobileBriefGoldenInput()),
		[]TopicFind{{ID: "find-1", Topic: "Wails", Title: "Wails 2.12.1", URL: "https://wails.io/changelog"}}, asOf, time.UTC)
	find := brief.Sections.Watching[0]
	if find.Kind != mobileBriefFindSource || find.Key != mobileBriefItemKey(mobileBriefSectionWatching, mobileBriefFindSource, "find-1") {
		t.Fatalf("find item = %+v", find)
	}
}

// mobileTestFeedbackBridge serves one brief with a find, a memory and a site,
// over a real receipt store and topics store.
func mobileTestFeedbackBridge(t *testing.T) (*mobileBridge, MobileBriefV1) {
	t.Helper()
	bridge, _ := mobileTestChatBridge(t)
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	svc := bridge.app.topicsSvc()
	svc.now = func() time.Time { return topicTestNow }
	svc.collect = func(now time.Time) topicInput { return topicInput{Now: now} }
	if _, err := svc.store.update(func(st *topicsStateFile) {
		st.Finds.Sent = findsSelection().Sent
		st.Finds.Items = []TopicFind{{ID: "f1", TopicID: "t1", Topic: "Wails drag regions", Title: "Wails 2.12.1"}}
	}); err != nil {
		t.Fatal(err)
	}
	asOf := time.Date(2026, 10, 9, 14, 0, 0, 0, time.UTC)
	brief := buildMobileBriefV1(mobileBriefInput{
		Slot: "morning", Location: time.UTC, AsOf: asOf,
		Watching: []mobileBriefCandidate{{SourceKind: mobileBriefWebSource, SourceID: "site:github.com", Title: "github.com", At: asOf}},
		Learned:  []mobileBriefCandidate{{SourceKind: "memory", SourceID: "mem-1", Title: "Prefers short replies", At: asOf}},
	})
	brief = mobileBriefWithFinds(brief, []TopicFind{{ID: "f1", Topic: "Wails drag regions", Title: "Wails 2.12.1"}}, asOf, time.UTC)
	// Stored older briefs may still carry browsing items. Feedback on those
	// remains supported even though the new builder no longer publishes them.
	legacy := &mobileBriefBuilder{asOf: asOf, evidence: brief.Evidence, index: map[string]string{}}
	brief.Sections.Watching = append(brief.Sections.Watching, legacy.section(mobileBriefSectionWatching, []mobileBriefCandidate{{SourceKind: mobileBriefWebSource, SourceID: "site:github.com", Title: "github.com", At: asOf}})...)
	brief.Evidence = legacy.evidence
	bridge.app.mobileBriefSvc = &mobileTestBriefSource{brief: &brief}
	mobileTestOpenTopicGate(t)
	return bridge, brief
}

// mobileTestOpenTopicGate lets phone feedback reweight topics for one test, as if
// observing were on and Watch my screen allowed. Production asks topicLabelsMayLeave.
func mobileTestOpenTopicGate(t *testing.T) {
	t.Helper()
	previous := mobileBriefFeedbackTopicsOpen
	mobileBriefFeedbackTopicsOpen = func(*App) bool { return true }
	t.Cleanup(func() { mobileBriefFeedbackTopicsOpen = previous })
}

// mobileTestDefaultTopicGate puts back the production gate for one test.
func mobileTestDefaultTopicGate(t *testing.T) {
	t.Helper()
	previous := mobileBriefFeedbackTopicsOpen
	mobileBriefFeedbackTopicsOpen = mobileBriefFeedbackTopicsOpenDefault
	t.Cleanup(func() { mobileBriefFeedbackTopicsOpen = previous })
}

func mobileTestFeedbackPath(brief MobileBriefV1, itemID string) string {
	return "/briefs/" + brief.BriefID + "/items/" + itemID + "/feedback"
}

func mobileTestItemKey(t *testing.T, brief MobileBriefV1, sourceKind string) string {
	t.Helper()
	for _, items := range mobileBriefSectionsByName(t, brief) {
		for _, item := range items {
			if target, ok := findMobileBriefFeedbackTarget(brief, item.Key); ok && target.SourceKind == sourceKind {
				return item.Key
			}
		}
	}
	t.Fatalf("no %s item in the brief", sourceKind)
	return ""
}

func TestMobileBriefFeedbackRouteEnforcesScopeAndValidation(t *testing.T) {
	bridge, brief := mobileTestFeedbackBridge(t)
	site := mobileTestItemKey(t, brief, mobileBriefWebSource)
	all := append(mobileTestChatScopes(), mobileScopeBriefRead, mobileScopeBriefFeedback)
	cases := []struct {
		name   string
		path   string
		body   string
		scopes []string
		status int
		want   string
	}{
		{"feedback needs brief:feedback", mobileTestFeedbackPath(brief, site), `{"idempotencyKey":"k1","action":"like"}`, mobileTestChatScopes(), http.StatusForbidden, "brief:feedback"},
		{"an unknown action is refused", mobileTestFeedbackPath(brief, site), `{"idempotencyKey":"k2","action":"love"}`, all, http.StatusBadRequest, "like or not_right"},
		{"the key is required", mobileTestFeedbackPath(brief, site), `{"action":"like"}`, all, http.StatusBadRequest, "idempotency key"},
		{"unknown fields are refused", mobileTestFeedbackPath(brief, site), `{"idempotencyKey":"k3","action":"like","title":"x"}`, all, http.StatusBadRequest, "invalid JSON"},
		{"a malformed brief id is refused", "/briefs/..%2F/items/" + site + "/feedback", `{"idempotencyKey":"k4","action":"like"}`, all, http.StatusBadRequest, "brief id"},
		{"an unknown brief is 404", "/briefs/2026-10-01-morning/items/" + site + "/feedback", `{"idempotencyKey":"k5","action":"like"}`, all, http.StatusNotFound, "no longer on your computer"},
		{"an unknown item is 404", mobileTestFeedbackPath(brief, "watching.0000000000000000"), `{"idempotencyKey":"k6","action":"like"}`, all, http.StatusNotFound, "no longer in the brief"},
		{"a site is recorded", mobileTestFeedbackPath(brief, site), `{"idempotencyKey":"k7","action":"like"}`, all, http.StatusOK, `"effect":"recorded"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := mobileTestChatCall(bridge, mobileTestDevice(tc.scopes...), http.MethodPost, tc.path, tc.body)
			if got.Code != tc.status || !strings.Contains(got.Body.String(), tc.want) {
				t.Fatalf("%d %s, want %d containing %q", got.Code, got.Body.String(), tc.status, tc.want)
			}
		})
	}
	entries := readMobileBriefFeedback(bridge.stateDir)
	if len(entries) != 1 || entries[0].Key != site || entries[0].Section != "watching" || entries[0].Action != "like" {
		t.Fatalf("recorded feedback = %+v", entries)
	}
}

func TestMobileBriefFeedbackOnAFindReweightsItsTopicOnce(t *testing.T) {
	bridge, brief := mobileTestFeedbackBridge(t)
	find := mobileTestItemKey(t, brief, mobileBriefFindSource)
	device := mobileTestDevice(mobileScopeBriefRead, mobileScopeBriefFeedback)
	body := `{"idempotencyKey":"like-1","action":"like"}`
	first := mobileTestChatCall(bridge, device, http.MethodPost, mobileTestFeedbackPath(brief, find), body)
	again := mobileTestChatCall(bridge, device, http.MethodPost, mobileTestFeedbackPath(brief, find), body)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), `"effect":"topic_more"`) || again.Body.String() != first.Body.String() {
		t.Fatalf("like replay: %s / %s", first.Body.String(), again.Body.String())
	}
	state := bridge.app.topicsSvc().store.load()
	if w := state.Weights["wails drag regions"]; w != topicsFeedbackMore || len(state.Finds.Feedback) != 1 {
		t.Fatalf("the like ran more than once: weight %v, feedback %+v", w, state.Finds.Feedback)
	}

	less := mobileTestChatCall(bridge, device, http.MethodPost, mobileTestFeedbackPath(brief, find), `{"idempotencyKey":"nr-1","action":"not_right"}`)
	if !strings.Contains(less.Body.String(), `"effect":"topic_less"`) {
		t.Fatalf("not right on a find: %s", less.Body.String())
	}
	if item := bridge.app.topicsSvc().store.load().Finds.Items[0]; item.Feedback != "less" {
		t.Fatalf("the find was not marked less: %+v", item)
	}
}

func TestMobileBriefRelevanceDoesNotCorrectMemory(t *testing.T) {
	bridge, brief := mobileTestFeedbackBridge(t)
	removed := false
	mobileMemoryRejectOverride = func(_, _ string) error { removed = true; return nil }
	t.Cleanup(func() { mobileMemoryRejectOverride = nil })
	device := mobileTestDevice(mobileScopeBriefFeedback)
	memory := mobileTestItemKey(t, brief, "memory")
	got := mobileTestChatCall(bridge, device, http.MethodPost, mobileTestFeedbackPath(brief, memory),
		`{"idempotencyKey":"irrelevant-memory","action":"not_relevant"}`)
	if got.Code != http.StatusOK || removed || !strings.Contains(got.Body.String(), `"effect":"recorded"`) {
		t.Fatalf("relevance feedback corrected memory: %d %s, removed=%v", got.Code, got.Body.String(), removed)
	}
	find := mobileTestItemKey(t, brief, mobileBriefFindSource)
	got = mobileTestChatCall(bridge, device, http.MethodPost, mobileTestFeedbackPath(brief, find),
		`{"idempotencyKey":"irrelevant-find","action":"not_relevant"}`)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"effect":"topic_less"`) {
		t.Fatalf("relevance feedback on find: %d %s", got.Code, got.Body.String())
	}
	status := mobileTestChatCall(bridge, device, http.MethodGet, "/status", "")
	if status.Code != http.StatusOK || !strings.Contains(status.Body.String(), `"briefRelevanceFeedback":true`) {
		t.Fatalf("status capability: %d %s", status.Code, status.Body.String())
	}
}

func TestMobileBriefFeedbackLeavesTopicsAloneWhileLabelsStayOnTheMac(t *testing.T) {
	bridge, brief := mobileTestFeedbackBridge(t)
	mobileBriefFeedbackTopicsOpen = func(*App) bool { return false }
	find := mobileTestItemKey(t, brief, mobileBriefFindSource)
	got := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeBriefFeedback), http.MethodPost,
		mobileTestFeedbackPath(brief, find), `{"idempotencyKey":"closed-1","action":"like"}`)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"effect":"recorded"`) {
		t.Fatalf("feedback with the gate closed: %d %s", got.Code, got.Body.String())
	}
	if state := bridge.app.topicsSvc().store.load(); len(state.Weights) != 0 || len(state.Finds.Feedback) != 0 {
		t.Fatalf("phone feedback changed the search while labels stay on the Mac: %+v", state.Weights)
	}
}

func TestMobileBriefFeedbackUsesTheProductionGateWhileObservingIsPaused(t *testing.T) {
	bridge, brief := mobileTestFeedbackBridge(t)
	mobileTestDefaultTopicGate(t)
	bridge.app.closedEyes.enabled.Store(true)
	find := mobileTestItemKey(t, brief, mobileBriefFindSource)
	got := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeBriefFeedback), http.MethodPost,
		mobileTestFeedbackPath(brief, find), `{"idempotencyKey":"paused-1","action":"like"}`)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"effect":"recorded"`) {
		t.Fatalf("feedback while observing is paused: %d %s", got.Code, got.Body.String())
	}
	if state := bridge.app.topicsSvc().store.load(); len(state.Weights) != 0 || len(state.Finds.Feedback) != 0 {
		t.Fatalf("the production gate let phone feedback change the search: %+v", state.Weights)
	}
}

func TestMobileBriefFeedbackRecordFileIsPrivateAndKeepsTheNewest500(t *testing.T) {
	bridge, _ := mobileTestChatBridge(t)
	for i := 1; i <= mobileBriefFeedbackLimit+5; i++ {
		bridge.recordBriefFeedback(mobileBriefFeedbackEntry{Key: "watching." + strconv.Itoa(i), Action: "like"})
	}
	entries := readMobileBriefFeedback(bridge.stateDir)
	if len(entries) != mobileBriefFeedbackLimit || entries[0].Key != "watching.6" ||
		entries[len(entries)-1].Key != "watching."+strconv.Itoa(mobileBriefFeedbackLimit+5) {
		t.Fatalf("kept %d entries, first %q", len(entries), entries[0].Key)
	}
	info, err := os.Stat(filepath.Join(bridge.stateDir, mobileBriefFeedbackFileName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("feedback file mode = %v, %v", info.Mode().Perm(), err)
	}
}

func TestMobileMemoryCorrectionsAnswerRemovedWhenOnlyTheLogFailed(t *testing.T) {
	bridge, brief := mobileTestFeedbackBridge(t)
	mobileMemoryRejectOverride = func(memoryID, reason string) error {
		// The entry is deleted, then the rejection log cannot be written.
		return memoryRejectionLogError{errors.New("append rejection log: disk full")}
	}
	t.Cleanup(func() { mobileMemoryRejectOverride = nil })
	device := mobileTestDevice(mobileScopeBriefFeedback, mobileScopeMemoryFix)
	feedback := mobileTestChatCall(bridge, device, http.MethodPost, mobileTestFeedbackPath(brief, mobileTestItemKey(t, brief, "memory")),
		`{"idempotencyKey":"log-1","action":"not_right","reason":"Not me"}`)
	if feedback.Code != http.StatusOK || !strings.Contains(feedback.Body.String(), `"effect":"memory_removed"`) {
		t.Fatalf("feedback after a failed log line: %d %s", feedback.Code, feedback.Body.String())
	}
	correction := mobileTestChatCall(bridge, device, http.MethodPost, "/memory/mem-2/reject-attribution",
		`{"idempotencyKey":"log-2","reason":"Not me"}`)
	if correction.Code != http.StatusOK || !strings.Contains(correction.Body.String(), `"removed":true`) {
		t.Fatalf("correction after a failed log line: %d %s", correction.Code, correction.Body.String())
	}
}

func TestMobileBriefFeedbackNotRightOnAMemoryUsesTheCorrection(t *testing.T) {
	bridge, brief := mobileTestFeedbackBridge(t)
	var removed []string
	mobileMemoryRejectOverride = func(memoryID, reason string) error {
		if memoryID == "gone" {
			return fs.ErrNotExist
		}
		removed = append(removed, memoryID+":"+reason)
		return nil
	}
	t.Cleanup(func() { mobileMemoryRejectOverride = nil })
	memory := mobileTestItemKey(t, brief, "memory")
	path := mobileTestFeedbackPath(brief, memory)

	noFix := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeBriefFeedback), http.MethodPost, path,
		`{"idempotencyKey":"m0","action":"not_right","reason":"Not me"}`)
	if noFix.Code != http.StatusForbidden || !strings.Contains(noFix.Body.String(), "memory:correct") || len(removed) != 0 {
		t.Fatalf("removing a memory without memory:correct: %d %s", noFix.Code, noFix.Body.String())
	}

	device := mobileTestDevice(mobileScopeBriefFeedback, mobileScopeMemoryFix)
	like := mobileTestChatCall(bridge, device, http.MethodPost, path, `{"idempotencyKey":"m1","action":"like"}`)
	if !strings.Contains(like.Body.String(), `"effect":"recorded"`) || len(removed) != 0 {
		t.Fatalf("like on a memory: %s, removed %v", like.Body.String(), removed)
	}
	body := `{"idempotencyKey":"m2","action":"not_right","reason":"Not me"}`
	first := mobileTestChatCall(bridge, device, http.MethodPost, path, body)
	again := mobileTestChatCall(bridge, device, http.MethodPost, path, body)
	if !strings.Contains(first.Body.String(), `"effect":"memory_removed"`) || again.Body.String() != first.Body.String() {
		t.Fatalf("not right replay: %s / %s", first.Body.String(), again.Body.String())
	}
	if len(removed) != 1 || removed[0] != "mem-1:Not me" {
		t.Fatalf("the correction ran %d times: %v", len(removed), removed)
	}
	blank := mobileTestChatCall(bridge, device, http.MethodPost, path, `{"idempotencyKey":"m3","action":"not_right"}`)
	if blank.Code != http.StatusOK || removed[len(removed)-1] != "mem-1:"+mobileBriefFeedbackNoReason {
		t.Fatalf("not right without a reason: %s, removed %v", blank.Body.String(), removed)
	}
}

func TestMobileBriefFeedbackFindsItemsOfBriefsStoredBeforeKeys(t *testing.T) {
	bridge, brief := mobileTestFeedbackBridge(t)
	old := brief
	old.Sections.Watching = append([]MobileBriefItem(nil), brief.Sections.Watching...)
	for i := range old.Sections.Watching {
		old.Sections.Watching[i].Key = ""
	}
	bridge.app.mobileBriefSvc = &mobileTestBriefSource{brief: &old}
	if old.Sections.Watching[0].Kind != mobileBriefFindSource {
		t.Fatalf("the brief does not lead with its find: %+v", old.Sections.Watching[0])
	}
	got := mobileTestChatCall(bridge, mobileTestDevice(mobileScopeBriefFeedback), http.MethodPost,
		mobileTestFeedbackPath(old, old.Sections.Watching[0].ID), `{"idempotencyKey":"old-1","action":"like"}`)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"effect":"topic_more"`) {
		t.Fatalf("feedback by id on an older brief: %d %s", got.Code, got.Body.String())
	}
	if w := bridge.app.topicsSvc().store.load().Weights["wails drag regions"]; w != topicsFeedbackMore {
		t.Fatalf("the find's topic weighs %v after a like by id", w)
	}
}

func TestMobileDeviceStoreKeepsAPhoneWithAScopeThisMacDoesNotKnow(t *testing.T) {
	dir := t.TempDir()
	bridge, err := newMobileBridge(&App{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	phone := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead, mobileScopeBriefFeedback})
	bridge.Stop()

	// A newer Mac granted a scope this build has never heard of.
	path := filepath.Join(dir, "devices.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	newer := strings.Replace(string(raw), `"brief:feedback"`, `"brief:feedback", "drafts:send"`, 1)
	if newer == string(raw) {
		t.Fatal("the stored record has no scopes to extend")
	}
	if err := os.WriteFile(path, []byte(newer), 0o600); err != nil {
		t.Fatal(err)
	}

	reloaded, err := newMobileBridge(&App{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reloaded.Stop)
	record := reloaded.devices[phone.deviceID]
	if record == nil {
		t.Fatal("the phone was unpaired because it holds a newer scope")
	}
	if !mobileHasScope(record.Scopes, mobileScopeBriefFeedback) || mobileHasScope(record.Scopes, "drafts:send") {
		t.Fatalf("effective scopes = %v", record.Scopes)
	}
	reloaded.mu.Lock()
	err = reloaded.persistDevicesLocked()
	reloaded.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(stored), `"drafts:send"`) || !strings.Contains(string(stored), phone.deviceID) {
		t.Fatalf("the stored record lost the phone or its newer scope: %s", stored)
	}
}

func TestMobileReleaseFourScopesAreKnown(t *testing.T) {
	scopes := []string{mobileScopeBriefFeedback, mobileScopeOffersRead, mobileScopeOffersDecide,
		mobileScopeGoalsRead, mobileScopeGoalsWrite, mobileScopeLibraryRead}
	normalized, err := mobileNormalizeScopes(append(scopes, mobileScopeBriefRead))
	if err != nil || len(normalized) != len(scopes)+1 {
		t.Fatalf("release 4 scopes = %v, %v", normalized, err)
	}
	if _, err := mobileNormalizeScopes([]string{"drafts:send"}); err == nil {
		t.Fatal("drafts:send waits for its own release and must not be accepted yet")
	}
}

func mobileBriefSectionsByName(t *testing.T, brief MobileBriefV1) map[string][]MobileBriefItem {
	t.Helper()
	raw, err := json.Marshal(brief.Sections)
	if err != nil {
		t.Fatal(err)
	}
	var sections map[string][]MobileBriefItem
	if err := json.Unmarshal(raw, &sections); err != nil {
		t.Fatal(err)
	}
	return sections
}

func jsonEqual(t *testing.T, a, b any) bool {
	t.Helper()
	left, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	right, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(left) == string(right)
}

// The final mail-subject read occurs after mobileOffers has resolved and checked
// every offer source. Exercise a real activity-store mutation at that boundary,
// before the checked stream item's INSERT commits, without opening an app/model.
func TestMobileOfferAdmissionFencesLateActivityProtection(t *testing.T) {
	for _, withdraw := range []bool{false, true} {
		name := "unchanged_control"
		if withdraw {
			name = "protected_after_final_source_read"
		}
		t.Run(name, func(t *testing.T) {
			bridge := mobileTestOfferBridge(t)
			turns := mobileTestOfferRunner(t, bridge)
			offer := mobileTestOffers(t, bridge)[0]
			if offer.ID != "suggestion.idea" {
				t.Fatalf("unexpected fixture offer: %+v", offer)
			}
			refs := []mobileOfferReference{{ID: offer.ID, Version: offer.Version}}
			originalMail := mobileGoalMailRows
			t.Cleanup(func() { mobileGoalMailRows = originalMail })
			finalReaderCalls := 0
			reachedBoundary := false
			mobileGoalMailRows = func(reader *App) ([]storage.MailMessageRow, error) {
				rows, err := originalMail(reader)
				if err != nil || reader == bridge.app {
					return rows, err
				}
				finalReaderCalls++
				if finalReaderCalls != 2 {
					return rows, err
				}
				// Call one was inside mobileOffers. Call two is the later full
				// edited-text hold in mobileOfferChatText, after version validation.
				reachedBoundary = true
				visible, err := reader.store.LoadStreamItems(t.Context(), "own-chat")
				if err != nil || len(visible) != 0 {
					t.Fatalf("boundary was not before commit: rows=%+v err=%v", visible, err)
				}
				if withdraw {
					// This production storage method writes the activity DB only.
					// It takes neither readPrivacyMu nor the main Store writer lock.
					if err := bridge.app.activity.store.UpdateTriageStatus("idea-source-2", "protected"); err != nil {
						t.Fatal(err)
					}
				}
				return rows, nil
			}
			response := mobileTestOfferSend(t, bridge, mobileTestDevice(mobileScopeChatWrite, mobileScopeOffersRead), "late-activity-protection", offer.Prompt, refs)
			mobileGoalMailRows = originalMail
			if !reachedBoundary {
				t.Fatalf("final admission boundary not exercised: reader calls=%d", finalReaderCalls)
			}
			if response.Code == http.StatusAccepted {
				mobileTestWaitFor(t, "late-protection fixture runner", func() bool {
					return turns.count() == 1 && !bridge.chatRunning("own-chat")
				})
			}
			rows, err := bridge.app.store.LoadStreamItems(t.Context(), "own-chat")
			if err != nil {
				t.Fatal(err)
			}
			currentStillContainsOriginal := false
			for _, current := range mobileTestOffers(t, bridge) {
				if current.ID == offer.ID && current.Version == offer.Version {
					currentStillContainsOriginal = true
				}
			}
			if !withdraw {
				if response.Code != http.StatusAccepted || len(rows) != 2 || turns.count() != 1 || !currentStillContainsOriginal {
					t.Fatalf("unchanged control: status=%d rows=%+v turns=%d current=%v body=%s", response.Code, rows, turns.count(), currentStillContainsOriginal, response.Body.String())
				}
				return
			}
			if currentStillContainsOriginal {
				t.Fatal("fixture mutation did not withdraw the source-bound offer")
			}
			if response.Code != http.StatusConflict || len(rows) != 0 || turns.count() != 0 {
				t.Fatalf("source protected before commit was admitted: status=%d rows=%+v turns=%d body=%s", response.Code, rows, turns.count(), response.Body.String())
			}
		})
	}
}

func TestMobileOfferPublicationKeepsFileAndPolicyWritersBehindCommit(t *testing.T) {
	for _, kind := range []string{"memory", "knowledge", "topics", "interests", "interests forget", "brief", "handoff", "sensitive apps", "voice apps", "closed eyes"} {
		t.Run(kind, func(t *testing.T) {
			bridge := mobileTestOfferBridge(t)
			mobileTestOfferRunner(t, bridge)
			app := bridge.app
			memory, _ := memoryDir()
			knowledge, _ := knowledgeDir()
			brief, _ := mobileBriefStateDir()
			oldSensitive := pii.GetUserSensitiveApps()
			oldVoice := GetUserVoiceInputApps()
			t.Cleanup(func() { pii.SetUserSensitiveApps(oldSensitive); SetUserVoiceInputApps(oldVoice) })
			var mutate func() error
			switch kind {
			case "memory", "knowledge":
				dir, id := memory, "publication-source"
				if kind == "knowledge" {
					dir, id = knowledge, "knowledge:publication-source"
				}
				mutate = func() error {
					unlock := memoryFileLocks.Acquire(id)
					defer unlock()
					return os.WriteFile(filepath.Join(dir, "publication-source.md"), []byte("# Changed source"), 0600)
				}
			case "topics":
				mutate = func() error {
					_, err := app.topicsSvc().store.update(func(st *topicsStateFile) { st.Enabled = new(bool) })
					return err
				}
			case "interests":
				mutate = func() error {
					_, err := app.interestsSvc().store.update(func(st *interestsStateFile) { st.Interests = nil })
					return err
				}
			case "interests forget":
				mutate = app.interestsSvc().store.forget
			case "brief":
				dir := filepath.Join(brief, mobileBriefDirName)
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				mutate = func() error {
					return writeMobileBriefFileAtomic(dir, "publication.json", []byte(`{"changed":true}`))
				}
			case "handoff":
				mutate = func() error { return app.handoffStore.Delete("work") }
			case "sensitive apps":
				mutate = func() error { pii.AddUserSensitiveApp("com.example.editor", "Private"); return nil }
			case "voice apps":
				mutate = func() error { AddUserVoiceInputApp("com.example.editor", "Private"); return nil }
			case "closed eyes":
				// Exercise the setter's state critical section without its UI
				// callbacks, monitor changes or any application launch.
				mutate = func() error {
					app.closedEyes.mu.Lock()
					defer app.closedEyes.mu.Unlock()
					app.closedEyes.enabled.Store(true)
					return nil
				}
			}
			publication, err := app.beginMobileOfferPublication(t.Context(), app.mobileOfferAdmissionReader(t.Context()))
			if err != nil {
				t.Fatal(err)
			}
			defer publication.release()
			started, done := make(chan struct{}), make(chan error, 1)
			go func() { close(started); done <- mutate() }()
			<-started
			select {
			case err := <-done:
				t.Fatalf("%s changed before publication completed: %v", kind, err)
			case <-time.After(20 * time.Millisecond):
			}
			if err := publication.lockActivity(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := publication.validateFiles(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := app.store.AppendStreamItem(t.Context(), "own-chat", storage.StreamItemRow{ID: "fenced-publication", Kind: "user", Content: "Source was still readable"}); err != nil {
				t.Fatal(err)
			}
			publication.release()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("source writer did not resume after publication")
			}
		})
	}
}

func TestMobileOfferPublicationCancellationReleasesPartialGuards(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	app := bridge.app
	app.activity.store.readPrivacyMu.Lock()
	defer app.activity.store.readPrivacyMu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if publication, err := app.beginMobileOfferPublication(ctx, app.mobileOfferAdmissionReader(ctx)); !errors.Is(err, context.DeadlineExceeded) {
		if publication != nil {
			publication.release()
		}
		t.Fatalf("blocked publication ignored cancellation: %v", err)
	}
	if !app.closedEyes.mu.TryLock() {
		t.Fatal("partial acquisition retained the pause guard")
	}
	app.closedEyes.mu.Unlock()
	if !memoryFileLocks.publication.TryLock() {
		t.Fatal("partial acquisition retained the memory guard")
	}
	memoryFileLocks.publication.Unlock()
}

func TestMobileOfferPublicationRejectsLateRawFileChanges(t *testing.T) {
	for _, kind := range []string{"memory", "knowledge", "legacy history", "semantic control", "topics", "interests", "brief", "handoff"} {
		t.Run(kind, func(t *testing.T) {
			bridge := mobileTestOfferBridge(t)
			turns := mobileTestOfferRunner(t, bridge)
			offer := mobileTestOffers(t, bridge)[0]
			memory, _ := memoryDir()
			knowledge, _ := knowledgeDir()
			topics, _ := topicsStateDir()
			brief, _ := mobileBriefStateDir()
			path := ""
			switch kind {
			case "memory":
				path = filepath.Join(memory, "late-source.md")
			case "knowledge":
				path = filepath.Join(knowledge, "late-source.md")
			case "legacy history":
				path = filepath.Join(memory, memoryHistoryDirName, "source", "previous.md")
			case "semantic control":
				path = filepath.Join(memory, ".semantic", "control.json")
			case "topics":
				path = filepath.Join(topics, topicsStateFileName)
			case "interests":
				path = filepath.Join(topics, interestsStateFileName)
			case "brief":
				path = filepath.Join(brief, mobileBriefDirName, "late-source.json")
			case "handoff":
				path = filepath.Join(bridge.app.handoffStore.Dir(), "work.md")
			}
			originalMail := mobileGoalMailRows
			t.Cleanup(func() { mobileGoalMailRows = originalMail })
			calls, changed := 0, false
			mobileGoalMailRows = func(reader *App) ([]storage.MailMessageRow, error) {
				rows, err := originalMail(reader)
				if reader != bridge.app {
					calls++
					if calls == 2 {
						if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
							t.Fatal(err)
						}
						data, _ := os.ReadFile(path)
						if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
							t.Fatal(err)
						}
						changed = true
					}
				}
				return rows, err
			}
			response := mobileTestOfferSend(t, bridge, mobileTestDevice(mobileScopeChatWrite, mobileScopeOffersRead), "late-file", offer.Prompt, []mobileOfferReference{{ID: offer.ID, Version: offer.Version}})
			mobileGoalMailRows = originalMail
			rows, err := bridge.app.store.LoadStreamItems(t.Context(), "own-chat")
			if !changed || response.Code != http.StatusConflict || err != nil || len(rows) != 0 || turns.count() != 0 {
				t.Fatalf("late file edit admitted: kind=%s changed=%v status=%d rows=%+v turns=%d err=%v", kind, changed, response.Code, rows, turns.count(), err)
			}
		})
	}
}

func TestMobileOfferPublicationAllowsLegacyMemoryProvenanceRead(t *testing.T) {
	bridge := mobileTestOfferBridge(t)
	dir, _ := memoryDir()
	content := "# Legacy source\n\n- **Source**: manual\n\nOriginal content"
	if err := os.WriteFile(filepath.Join(dir, "legacy.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	before := memoryReadCapturedProvenance(dir, "legacy", content)
	publication, err := bridge.app.beginMobileOfferPublication(t.Context(), bridge.app.mobileOfferAdmissionReader(t.Context()))
	if err != nil {
		t.Fatal(err)
	}
	defer publication.release()
	read := make(chan memoryReadProvenance, 1)
	go func() { read <- memoryReadCapturedProvenance(dir, "legacy", content) }()
	select {
	case current := <-read:
		if !jsonEqual(t, before, current) {
			t.Fatalf("publication changed provenance: before=%+v current=%+v", before, current)
		}
	case <-time.After(time.Second):
		publication.release()
		<-read
		t.Fatal("legacy provenance reader recursively acquired the publication barrier")
	}
}

// Settings changes the live maps before saving ui_state. A read that already
// checked the source must reject that change even while DB/file stamps match.
func TestMobileOfferHistoryRejectsLateLivePolicyChanges(t *testing.T) {
	for _, surface := range []string{"history", "stored text"} {
		for _, policy := range []string{"unchanged", "sensitive apps", "voice apps", "closed eyes"} {
			t.Run(surface+"/"+policy, func(t *testing.T) {
				oldSensitive, oldVoice := pii.GetUserSensitiveApps(), GetUserVoiceInputApps()
				pii.SetUserSensitiveApps(nil)
				SetUserVoiceInputApps(nil)
				t.Cleanup(func() { pii.SetUserSensitiveApps(oldSensitive); SetUserVoiceInputApps(oldVoice) })
				bridge := mobileTestOfferBridge(t)
				app := bridge.app
				offer := mobileTestOffers(t, bridge)[0]
				prompt, metadata, err := app.mobileOfferChatAdmission(t.Context(), offer.Prompt, []mobileOfferReference{{ID: offer.ID, Version: offer.Version}})
				if err != nil || metadata == "" {
					t.Fatalf("prepare admitted proof: %v", err)
				}
				rows := []storage.StreamItemRow{
					{ID: "ordinary", Kind: "user", Content: "Earlier ordinary message"},
					{ID: "source", Kind: "user", Content: prompt, AutomationContext: metadata},
					{ID: "derived", Kind: "assistant", Content: "Derived source response"},
				}
				if got := app.mobileOfferHistoryRows(t.Context(), rows); len(got) != len(rows) {
					t.Fatal("readable control was withheld before the policy change")
				}
				mainBefore, err := app.store.ReadRevision(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				activityBefore, err := storage.DatabaseRevision(t.Context(), app.activity.store.db)
				if err != nil {
					t.Fatal(err)
				}
				originalMail := mobileGoalMailRows
				t.Cleanup(func() { mobileGoalMailRows = originalMail })
				calls := 0
				mobileGoalMailRows = func(reader *App) ([]storage.MailMessageRow, error) {
					mail, err := originalMail(reader)
					calls++
					if err == nil {
						switch policy {
						case "sensitive apps":
							pii.AddUserSensitiveApp("com.example.editor", "Editor")
						case "voice apps":
							AddUserVoiceInputApp("com.example.editor", "Editor")
						case "closed eyes":
							app.closedEyes.mu.Lock()
							app.closedEyes.enabled.Store(true)
							app.closedEyes.mu.Unlock()
						}
					}
					return mail, err
				}
				accepted := false
				switch surface {
				case "history":
					got := app.mobileOfferHistoryRows(t.Context(), rows)
					accepted = len(got) == len(rows)
					if !accepted && (len(got) != 1 || got[0].ID != "ordinary") {
						t.Fatalf("changed policy removed the ordinary prefix: %+v", got)
					}
				case "stored text":
					_, accepted = app.mobileOfferStoredText(t.Context(), prompt, metadata)
				}
				mobileGoalMailRows = originalMail
				mainAfter, mainErr := app.store.ReadRevision(t.Context())
				activityAfter, activityErr := storage.DatabaseRevision(t.Context(), app.activity.store.db)
				if mainErr != nil || activityErr != nil || mainBefore != mainAfter || activityBefore != activityAfter || calls != 1 {
					t.Fatalf("fixture did not isolate a live-only final-read change: calls=%d mainErr=%v activityErr=%v", calls, mainErr, activityErr)
				}
				if accepted != (policy == "unchanged") {
					t.Fatalf("late live policy %q: accepted=%v", policy, accepted)
				}
			})
		}
	}
}
