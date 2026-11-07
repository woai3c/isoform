package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/handoff"
	"wails-gui/internal/pii"
	"wails-gui/storage"
)

func TestReleaseEphemeralBoundaryRedactsAndHonorsClosedEyes(t *testing.T) {
	a := newBindingsTestApp(t)
	runner := &modelPrivacyTaskRecorder{memoryTranslationFakeRunner: memoryTranslationFakeRunner{response: "Safe answer", ready: true}}
	a.runner = runner
	if _, err := a.runEphemeralClaudeModelContext(t.Context(), "Question "+activityPrivacySecret, "m"); err != nil {
		t.Fatal(err)
	}
	if len(runner.inputs) != 1 || strings.Contains(runner.inputs[0].Prompt, activityPrivacySecret) || !strings.Contains(runner.inputs[0].Prompt, "[REDACTED:api_key]") {
		t.Fatal("ephemeral boundary did not redact the actual dispatched prompt")
	}
	a.closedEyes.enabled.Store(true)
	if _, err := a.runEphemeralClaudeModelContext(t.Context(), "Question", "m"); err == nil || len(runner.inputs) != 1 {
		t.Fatal("Closed Eyes dispatched an ephemeral request")
	}
}

func TestReleaseSideChatInheritsSourceWithdrawal(t *testing.T) {
	bridge, _ := admittedSourceDispatchFixture(t)
	a := bridge.app
	parent, err := a.store.LoadSession(t.Context(), "own-chat")
	if err != nil || parent == nil {
		t.Fatalf("load source-bound parent: %v", err)
	}
	if err := a.store.SaveSession(t.Context(), storage.SessionRow{LocalID: "release-side", ProjectID: parent.ProjectID, WorkspaceID: parent.WorkspaceID, Kind: storage.SessionKindSide, ParentID: parent.LocalID}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.AppendStreamItem(t.Context(), "release-side", storage.StreamItemRow{ID: "side-answer", Kind: "assistant", Content: "SOURCE_DERIVED_SIDE_ANSWER"}); err != nil {
		t.Fatal(err)
	}
	rows, bound, err := a.sourceBoundChatHistory(t.Context(), "release-side")
	if err != nil || !bound || len(rows) != 1 || len(a.mobileOfferHistoryRows(t.Context(), rows)) != 1 {
		t.Fatalf("readable child history: %v %v %v", rows, bound, err)
	}
	before, err := a.store.LoadStreamItems(t.Context(), "own-chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.BringBackToMainChat("release-side"); !errors.Is(err, errChatSourceAccessChanged) {
		t.Fatalf("source-bound summary escaped to an unbound report: %v", err)
	}
	after, err := a.store.LoadStreamItems(t.Context(), "own-chat")
	if err != nil || len(after) != len(before) {
		t.Fatal("source-bound bring-back persisted an unguarded main report")
	}
	if err := a.activity.store.DeleteSession("idea-source-2"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.sourceBoundChatHistory(t.Context(), "release-side"); !errors.Is(err, errChatSourceAccessChanged) {
		t.Fatalf("side model history retained withdrawn parent: %v", err)
	}
	if got := a.mobileOfferHistoryRows(t.Context(), rows); len(got) != 0 {
		t.Fatalf("phone history retained withdrawn side text: %+v", got)
	}
}

func TestReleaseEngagementQueueRechecksAllChannelsAndReplacementSources(t *testing.T) {
	for _, state := range []string{"readable", "closed-eyes", "protected", "replacement"} {
		t.Run(state, func(t *testing.T) {
			isolateActivityPrivacyTest(t)
			rig := newEngagementTestRig(t)
			hrig := newHandoffTestRig(t)
			a := hrig.app
			a.store, a.engagement = rig.store, rig.svc
			rig.svc.prepareSource, rig.svc.sourceAllowsRead = a.prepareEngagementRead, a.engagementDeliveryAllowsRead
			session := seedMobileBriefPurposeRun(t, a, "queue-source", 1, rig.clock.Add(-time.Hour), 1000, []string{"Editor"})
			work, err := hrig.handoffs.Save(handoff.Entry{ID: "queue-work", Source: handoff.SourceTriage, SourceID: session, AppName: "Editor", Title: "Private project", Status: handoff.StatusBlocked})
			if err != nil {
				t.Fatal(err)
			}
			sent := 0
			deliver := func(context.Context, storage.EngagementMomentRow, engagement.Moment, engagement.Verdict, engagementText) error {
				sent++
				return nil
			}
			rig.svc.deliver = engagementDelivery{Push: deliver, Desktop: deliver, Text: deliver, Email: deliver}
			rig.presence = engagement.Presence{MacForeground: true, PhoneRegistered: true, SMSActive: true, TextAvailable: true, EmailActive: true}
			row, err := rig.svc.Submit(t.Context(), engagement.Moment{Kind: engagement.KindWorkBlocked, SubjectID: work.ID, Params: map[string]string{"title": work.Title, "question": "your attention."}, Channels: []engagement.Channel{engagement.ChannelPush, engagement.ChannelDesktop, engagement.ChannelText, engagement.ChannelEmail}})
			if err != nil {
				t.Fatal(err)
			}
			if !a.engagementDeliveryAllowsRead(t.Context(), momentFromRow(row)) {
				t.Fatalf("initial source proof invalid: %+v", row)
			}
			switch state {
			case "closed-eyes":
				a.closedEyes.enabled.Store(true)
			case "protected":
				if _, err := a.activity.store.db.Exec("UPDATE activity_sessions SET triage_status='protected' WHERE id=?", session); err != nil {
					t.Fatal(err)
				}
			case "replacement":
				work.SourceID = seedMobileBriefPurposeRun(t, a, "replacement", 2, rig.clock.Add(-time.Hour), 1000, []string{"Editor"})
				if _, err := hrig.handoffs.Save(work); err != nil {
					t.Fatal(err)
				}
			}
			rig.svc.Pass(t.Context())
			want := 0
			if state == "readable" {
				want = 4
			}
			if sent != want {
				t.Fatalf("state %s sent %d messages", state, sent)
			}
			if state != "readable" {
				stored, err := rig.store.GetEngagementMoment(t.Context(), row.ID)
				if err != nil || stored == nil || stored.Params != "{}" {
					t.Fatal("withdrawn pending Params were not scrubbed")
				}
			}
		})
	}
}

func TestReleaseEngagementReceiptRejectsAlreadyStaleProducerFields(t *testing.T) {
	a := newBindingsTestApp(t)
	p, err := a.store.CreatePrincipal(t.Context(), storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Current public name"})
	if err != nil {
		t.Fatal(err)
	}
	m := engagement.Moment{Kind: engagement.KindPersonReappeared, SubjectID: p.ID, Params: map[string]string{"name": p.DisplayName, "where": "Discord"}}
	clean, allowed := a.prepareEngagementRead(t.Context(), m)
	if !allowed || !a.engagementDeliveryAllowsRead(t.Context(), clean) {
		t.Fatal("current producer fields must receive a usable receipt")
	}
	m.Params["name"] = "WITHDRAWN_OLD_NAME"
	if _, allowed := a.prepareEngagementRead(t.Context(), m); allowed {
		t.Fatal("fresh source lent permission to already stale producer fields")
	}
}

func TestReleaseUnboundReplayWithdrawsEveryRepresentation(t *testing.T) {
	a := newBindingsTestApp(t)
	const secret = "WITHDRAWN_CATALOG_DETAIL"
	for _, tool := range []string{mainChatProactiveTool, mainChatStatusTool} {
		var payload any
		if tool == mainChatProactiveTool {
			child := mainChatProactive{MomentID: "old-child", Kind: string(engagement.KindWorkBlocked), Headline: secret, Body: secret, Localized: map[string]mainChatProactiveCopy{"en": {Body: secret}, "zh-CN": {Body: secret}}, Target: mainChatTarget{Kind: "handoff", ID: secret}}
			payload = mainChatProactive{MomentID: "digest", Kind: "digest", Headline: secret, Body: secret, Items: []mainChatProactive{child}}
		} else {
			payload = mainChatStatus{Entries: []mainChatStatusEntry{{ID: "old-entry", Kind: string(engagement.KindPlaybookResult), Text: secret, Localized: map[string]string{"en": secret, "zh-CN": secret}}}}
		}
		raw, _ := json.Marshal(payload)
		content, input := a.projectProactiveVoiceDelivery(t.Context(), tool, secret, string(raw))
		if strings.Contains(content+input, secret) {
			t.Fatalf("%s leaked old prose: %s %s", tool, content, input)
		}
	}
}

func TestReleasePeopleProfileRequiresReadableSessionLineage(t *testing.T) {
	isolateActivityPrivacyTest(t)
	rig := newHandoffTestRig(t)
	p, err := rig.app.store.CreatePrincipal(t.Context(), storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Inferred identity", Notes: "DERIVED_PROFILE_DETAIL", CreatedFrom: "auto_discord"})
	if err != nil {
		t.Fatal(err)
	}
	session := seedMobileBriefPurposeRun(t, rig.app, "person-source", 1, time.Now().Add(-time.Hour), 1000, []string{"Editor"})
	if err := rig.app.store.BumpPrincipalSighting(t.Context(), p.ID, "discord", session, time.Now().UnixMilli(), "hello"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rig.app.injectPeopleContext("Inferred identity"), "DERIVED_PROFILE_DETAIL") {
		t.Fatal("readable inferred profile missing")
	}
	if _, err := rig.activity.db.Exec("UPDATE activity_sessions SET triage_status='protected' WHERE id=?", session); err != nil {
		t.Fatal(err)
	}
	if got := rig.app.injectPeopleContext("Inferred identity"); got != "" {
		t.Fatalf("protected profile reached model: %s", got)
	}
	seed := rig.app.buildResearchSeed(t.Context(), p, "User question")
	if seed.Names != "" || seed.Recent != "" {
		t.Fatal("protected profile reached research seed")
	}
}

func TestReleaseUntypedNotificationsUseGenericCopyAndClosedEyes(t *testing.T) {
	a := newBindingsTestApp(t)
	for _, language := range []string{"en", "zh-CN"} {
		if err := a.store.SetUIState(t.Context(), uiLanguageStateKey, language); err != nil {
			t.Fatal(err)
		}
		title, body, allowed := a.notificationForDelivery(activityPrivacySecret, activityPrivacySecret, "")
		if !allowed || strings.Contains(title+body, activityPrivacySecret) {
			t.Fatal("unbound caller prose reached banner")
		}
	}
	a.closedEyes.enabled.Store(true)
	if _, _, allowed := a.notificationForDelivery("Title", "Body", ""); allowed {
		t.Fatal("Closed Eyes allowed banner")
	}
}

func TestReleasePeopleContextRechecksProtectedSourcesAndLegacyDossier(t *testing.T) {
	isolateActivityPrivacyTest(t)
	a := newBindingsTestApp(t)
	p, err := a.store.CreatePrincipal(t.Context(), storage.PrincipalRow{Kind: storage.PrincipalKindContact, DisplayName: "Release Person"})
	if err != nil {
		t.Fatal(err)
	}
	for i, source := range []struct{ app, text string }{{"slack", "WITHDRAWN_PEOPLE_PREVIEW"}, {"discord", "Readable message " + activityPrivacySecret}} {
		if _, err := a.store.BindAppHandle(t.Context(), p.ID, source.app, source.text, source.text); err != nil {
			t.Fatal(err)
		}
		if err := a.store.BumpPrincipalSighting(t.Context(), p.ID, source.app, "", time.Now().UnixMilli()+int64(i), source.text); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.store.SavePrincipalResearch(t.Context(), storage.PrincipalResearchRow{PrincipalID: p.ID, Status: "ready", Markdown: "UNBOUND_DOSSIER_DETAIL"}); err != nil {
		t.Fatal(err)
	}
	before := a.injectPeopleContext("Release Person")
	if !strings.Contains(before, "WITHDRAWN_PEOPLE_PREVIEW") || !strings.Contains(before, "Readable message") {
		t.Fatalf("fixture must expose both readable channels before withdrawal: %s", before)
	}
	pii.AddUserSensitiveApp("com.tinyspeck.slackmacgap", "Private Slack")
	got := a.injectPeopleContext("Release Person")
	for _, secret := range []string{"WITHDRAWN_PEOPLE_PREVIEW", "UNBOUND_DOSSIER_DETAIL", activityPrivacySecret} {
		if strings.Contains(got, secret) {
			t.Errorf("People context leaked %q", secret)
		}
	}
	if !strings.Contains(got, "Readable message [REDACTED:api_key]") {
		t.Fatal("readable redacted evidence was lost")
	}
	a.closedEyes.enabled.Store(true)
	if got := a.injectPeopleContext("Release Person"); got != "" {
		t.Fatal("Closed Eyes replayed People context")
	}
	seed := a.buildResearchSeed(t.Context(), p, "User supplied question")
	if seed.Names != "" || seed.Recent != "" || seed.Handles != "" || seed.Stats != "" || seed.Extra != "User supplied question" {
		t.Fatalf("Closed Eyes seed = %+v", seed)
	}
}

func TestReleaseCompletedHandoffHistoryKeepsReadGateWithoutLiveCard(t *testing.T) {
	for _, withdrawn := range []bool{false, true} {
		t.Run(map[bool]string{false: "readable", true: "withdrawn"}[withdrawn], func(t *testing.T) {
			isolateActivityPrivacyTest(t)
			rig := newHandoffTestRig(t)
			now := time.Now()
			session := seedMobileBriefPurposeRun(t, rig.app, "release-work", 1, now.Add(-time.Hour), 1000, []string{"Editor"})
			work, err := rig.handoffs.Save(handoff.Entry{ID: "release-history", Title: "HISTORICAL_WORK_DETAIL", AppName: "Editor", Source: handoff.SourceTriage, SourceID: session, Status: handoff.StatusCompleted})
			if err != nil {
				t.Fatal(err)
			}
			if !rig.app.mobileBriefHandoffReadable(t.Context(), work) {
				t.Fatal("readable completed-work control is invalid")
			}
			brief := buildMobileBriefV1(mobileBriefInput{Slot: mobileBriefSlotMorning, AsOf: now, Location: time.UTC})
			// Today has removed the completed card; only a narrative citation
			// retains its source identity. It still gates the entire synthesis.
			brief.Evidence = []MobileBriefRef{{Ref: "e1", SourceKind: "history", SourceID: work.ID, Label: work.Title}}
			brief.Narrative = []MobileBriefPara{{Text: "HISTORICAL_WORK_DETAIL", Refs: []string{"e1"}}}
			brief.NarrativeStatus = "ok"
			brief.Audio = &MobileBriefAudio{Status: mobileBriefAudioStatusOK}
			if withdrawn {
				_, err = rig.activity.db.Exec("UPDATE activity_sessions SET triage_status='protected' WHERE id=?", session)
				if err != nil {
					t.Fatal(err)
				}
			}
			got := rig.app.mobileBriefForDelivery(brief)
			raw, _ := json.Marshal(got)
			if strings.Contains(string(raw), "HISTORICAL_WORK_DETAIL") == withdrawn {
				t.Fatalf("withdrawn=%v, wire=%s", withdrawn, raw)
			}
			if withdrawn && (got.Audio != nil || len(got.Narrative) != 0) {
				t.Fatal("withdrawn work kept synthesis/audio")
			}
		})
	}
}

func TestReleaseAudioPublicationRejectsChangedSynthesisAtSameAsOf(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "withdrawn-during-render"}[changed], func(t *testing.T) {
			now := mobileBriefTestDay(8, 0)
			svc, _ := newTestMobileBriefService(t, &fakeMobileBriefClock{now: now})
			svc.app = nil
			brief := buildMobileBriefV1(mobileBriefInput{Slot: mobileBriefSlotMorning, AsOf: now, Location: time.UTC})
			brief.Narrative = []MobileBriefPara{{Text: "Original source synthesis", Refs: []string{"e1"}}}
			brief.NarrativeStatus = "ok" // only model text is read aloud (design R74)
			if err := svc.store.save(brief, now); err != nil {
				t.Fatal(err)
			}
			svc.speaker = fakeSpeaker("en", testWAV(1), nil)
			svc.speaker.synthesize = func(context.Context, string, string) ([]byte, error) {
				if changed {
					current := brief
					current.Narrative = nil
					current.NarrativeStatus = "unavailable"
					if err := svc.store.save(current, now); err != nil {
						t.Fatal(err)
					}
				}
				return testWAV(1), nil
			}
			svc.addAudio(t.Context(), brief, now)
			stored, ok := svc.store.load(brief.BriefID)
			if !ok || (stored.Audio != nil) == changed {
				t.Fatalf("changed=%v audio=%+v", changed, stored.Audio)
			}
		})
	}
}
