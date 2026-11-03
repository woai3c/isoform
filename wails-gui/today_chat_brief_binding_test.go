package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/engagement"
	"wails-gui/storage"
	"yansu-agent/agent"
)

func TestTodayChatWholeBriefRechecksCalendarAndCorrectedMemory(t *testing.T) {
	for _, source := range []string{"calendar", "memory"} {
		for _, kind := range []string{"brief"} {
			t.Run(source+"/"+kind, func(t *testing.T) {
				app, tag, _ := todaySendTestApp(t)
				now := time.Now()
				now = time.Date(now.Year(), now.Month(), now.Day(), 9, 0, 0, 0, time.Local)
				in := mobileBriefInput{Slot: "morning", WindowStart: now.Add(-time.Hour), AsOf: now, Location: time.Local}
				const confidential = "Confidential acquisition review"
				if source == "memory" {
					if _, err := app.SaveMemoryEntry("launch", "# "+confidential+"\nPrepare the transaction plan."); err != nil {
						t.Fatal(err)
					}
					in.OpenLoops = []mobileBriefCandidate{{SourceKind: "memory", SourceID: "launch", Title: confidential, Kind: "goal", Status: "active"}}
				} else {
					event := prepEvent("private-event", confidential, now.Add(2*time.Hour), 30, "", 0)
					storeEvent(t, app.store, event)
					in.Meetings = mobileBriefCalendarCandidates(app, []calendar.Event{event}, "morning", now, time.Local, "")
				}
				brief := buildMobileBriefV1(in)
				brief.Narrative = mobileBriefNarrativeTemplate(brief, "en")
				_, brief.Offers = mobileBriefClosing(brief, "en")
				if source == "calendar" && !calendarAccessGranted() {
					// A real-store snapshot captured on an authorized Mac. This
					// platform cannot grant EventKit; admission must still reject
					// the valid proof when either current permission gate is off.
					binding := &mobileBriefModelBinding{Version: 2, Synthesis: mobileBriefSynthesisDigest(brief), WeeklyRecap: mobileBriefModelDigest(brief.WeeklyRecap)}
					for _, ref := range mobileBriefNarrativeInputEvidence(brief) {
						candidate, _, ok := app.currentTodayCalendarCandidateWithPrefill(t.Context(), ref.SourceID, in)
						if !ok {
							t.Fatal("missing calendar store fixture")
						}
						binding.Sources = append(binding.Sources, mobileBriefModelSource{Evidence: ref, Digest: mobileBriefModelDigest(candidate)})
					}
					brief.modelBinding = binding
					if _, ok := app.mobileBriefModelSourceDigest(t.Context(), binding.Sources[0].Evidence, brief, todayChatSources{}); ok {
						t.Fatal("missing OS permission admitted calendar")
					}
				} else {
					bindConversationBriefFixture(t, app, &brief)
				}
				dir, _ := mobileBriefStateDir()
				if err := newMobileBriefStore(dir).save(brief, now); err != nil {
					t.Fatal(err)
				}
				refFor := func() TodayChatContextRef {
					r := TodayChatContextRef{SourceKind: kind, SourceID: mobileBriefConversationKey(app.mobileBriefForDelivery(brief)), BriefID: brief.BriefID}
					return r
				}
				ref := refFor()
				if source == "memory" || calendarAccessGranted() {
					if _, err := app.PrepareTodayChatDraft(ref); err != nil {
						t.Fatalf("readable control: %v", err)
					}
					if _, err := app.PrepareTodayChatTurn(tag, "Let's go through this brief.", ref); err != nil {
						t.Fatal(err)
					}
				}
				if source == "memory" {
					if _, err := app.SaveMemoryEntry("launch", "# Public launch plan\nThe private title has been removed."); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := app.store.SetUIState(t.Context(), permissionUIStateKey(calendarPermissionKey), "false"); err != nil {
						t.Fatal(err)
					}
				}
				if source == "memory" {
					// The brief stays open to talk about after a correction, with
					// only the current state of what it covered, and its offer
					// becomes a request about the subject's current title. The
					// corrected title never reaches the draft, history or model.
					for _, ref := range []TodayChatContextRef{ref, refFor()} {
						got, err := app.buildTodayChatContext(t.Context(), ref)
						if err != nil {
							t.Fatal(err)
						}
						if strings.Contains(got.Text, confidential) || strings.Contains(got.Text, "Brief snapshot;") || !strings.Contains(got.Text, "Public launch plan") {
							t.Fatalf("stale snapshot admitted by draft:\n%s", got.Text)
						}
					}
					runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
					app.runner = runner
					app.tagCancels = map[string]context.CancelFunc{}
					app.sessionTempDirs = map[string]string{}
					app.streams = map[string]context.CancelFunc{}
					app.runIDByTag = map[string]string{}
					app.pendingTraceIDs = map[string]string{}
					if _, err := app.SendPromptWithTodayContext("Let's go through this brief.", "", "m", "", tag, nil, nil, false, false, "en", "", StreamItemData{ID: "current-send", Kind: "user"}, refFor()); err != nil {
						t.Fatal(err)
					}
					select {
					case input := <-runner.inputs:
						prompt, history := input.PromptFunc()
						if strings.Contains(prompt+history, confidential) || strings.Contains(prompt, "Brief snapshot;") || !strings.Contains(prompt, "Public launch plan") {
							t.Fatalf("stale whole brief reached model runner:\n%s", prompt)
						}
					case <-time.After(5 * time.Second):
						t.Fatal("current brief did not reach the model")
					}
					waitForTagIdle(t, app, tag)
					return
				}
				// A newly captured revision after the edit/withdrawal must not
				// legitimize the old synthesis either.
				for i, ref := range []TodayChatContextRef{ref, refFor()} {
					if _, err := app.PrepareTodayChatDraft(ref); err == nil {
						t.Errorf("stale snapshot admitted by draft (revision %d)", i)
					}
					row := storage.StreamItemRow{ID: fmt.Sprintf("withdrawn-%d", i), Kind: "user", Content: "Let's go through this brief."}
					if _, _, err := app.admitTodayChatTurn(t.Context(), tag, row, ref, nil); err == nil {
						t.Errorf("stale snapshot admitted atomically (revision %d)", i)
					}
				}
				runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
				app.runner = runner
				app.tagCancels = map[string]context.CancelFunc{}
				app.sessionTempDirs = map[string]string{}
				app.streams = map[string]context.CancelFunc{}
				app.runIDByTag = map[string]string{}
				app.pendingTraceIDs = map[string]string{}
				_, err := app.SendPromptWithTodayContext("Let's go through this brief.", "", "m", "", tag, nil, nil, false, false, "en", "", StreamItemData{ID: "withdrawn-send", Kind: "user"}, refFor())
				if err == nil {
					t.Error("stale whole brief reached send")
				}
				select {
				case <-runner.inputs:
					t.Error("stale whole brief reached model runner")
				default:
				}
				page, err := app.store.LoadStreamItemsLatest(t.Context(), tag, 100)
				if err != nil {
					t.Fatal(err)
				}
				for _, row := range page.Items {
					if strings.HasPrefix(row.ID, "withdrawn-") {
						t.Errorf("rejected context persisted: %s", row.ID)
					}
				}
			})
		}
	}
}

func bindConversationBriefFixture(t *testing.T, app *App, brief *MobileBriefV1) {
	t.Helper()
	*brief = app.mobileBriefForDelivery(*brief)
	binding := app.captureMobileBriefModelSources(t.Context(), *brief, conversationGatherRevisionFixture(t, app))
	app.finishMobileBriefModelBinding(t.Context(), brief, binding)
	if brief.modelBinding == nil {
		t.Fatal("readable source fixture did not bind")
	}
}

func TestBriefConversationUnknownAttendanceDoesNotAssumeAttendance(t *testing.T) {
	for _, lang := range []string{"en", "zh-CN"} {
		brief := MobileBriefV1{AsOf: "2026-10-18T17:00:00Z", Timezone: "UTC"}
		key := "en"
		if lang == "zh-CN" {
			key = "zh"
		}
		text := mobileBriefTemplateItem(brief, MobileBriefItem{Kind: mobileBriefAttendanceSource, Status: "unknown"}, "Review", "Team sync", key, lang)
		want := "whether you attended"
		bad := "confirm which device you joined on"
		if lang == "zh-CN" {
			want, bad = "是否参会", "先确认你通过哪台设备参会"
		}
		if !strings.Contains(text, want) || strings.Contains(text, bad) {
			t.Fatalf("attendance presupposition (%s): %s", lang, text)
		}
	}
}

func boundMemoryConversationFixture(t *testing.T, app *App) MobileBriefV1 {
	t.Helper()
	if _, err := app.SaveMemoryEntry("launch", "# Launch plan\nPrepare the release."); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	brief := buildMobileBriefV1(mobileBriefInput{Slot: "morning", AsOf: now, Location: time.Local, OpenLoops: []mobileBriefCandidate{{SourceKind: "memory", SourceID: "launch", Title: "Launch plan", Kind: "goal", Status: "active"}}})
	brief.Narrative = mobileBriefNarrativeTemplate(brief, "en")
	_, brief.Offers = mobileBriefClosing(brief, "en")
	return brief
}

func TestTodayChatWholeBriefRequiresStoredSynthesisBinding(t *testing.T) {
	for _, change := range []string{"none", "legacy", "pre-gather-fix", "prose", "offer", "label"} {
		t.Run(change, func(t *testing.T) {
			app := newTodayContextTestApp(t, true)
			brief := boundMemoryConversationFixture(t, app)
			bindConversationBriefFixture(t, app, &brief)
			wire, err := json.Marshal(brief)
			if err != nil || strings.Contains(string(wire), "modelReadBinding") || strings.Contains(string(wire), brief.modelBinding.Synthesis) {
				t.Fatal("local binding entered phone JSON")
			}
			switch change {
			case "legacy":
				brief.modelBinding = nil
			case "pre-gather-fix":
				brief.modelBinding.Version = 1
			case "prose":
				brief.Narrative[0].Text += " Forgotten private assertion."
			case "offer":
				brief.Offers[0].Prompt += " Forgotten private offer."
			case "label":
				brief.Evidence[0].Label = "Forgotten private label"
			}
			dir, _ := mobileBriefStateDir()
			store := newMobileBriefStore(dir)
			if err := store.save(brief, time.Now()); err != nil {
				t.Fatal(err)
			}
			loaded, ok := store.load(brief.BriefID)
			if !ok {
				t.Fatal("stored brief missing")
			}
			for _, kind := range []string{"brief"} {
				ref := TodayChatContextRef{SourceKind: kind, SourceID: mobileBriefConversationKey(app.mobileBriefForDelivery(loaded)), BriefID: loaded.BriefID}
				_, err := app.PrepareTodayChatDraft(ref)
				// The brief stays open to talk about. Without its proof only the
				// current state of what it covered is attached, never its prose,
				// offers or stored labels.
				if err != nil {
					t.Fatalf("brief %s: %v", change, err)
				}
				got, err := app.buildTodayChatContext(t.Context(), ref)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(got.Text, "Brief snapshot;") != (change == "none") || !strings.Contains(got.Text, "Prepare the release") {
					t.Fatalf("brief %s context:\n%s", change, got.Text)
				}
				if change != "none" {
					for _, stale := range []string{"Forgotten private", loaded.Narrative[0].Text, loaded.Offers[0].Prompt} {
						if strings.Contains(got.Text, stale) {
							t.Fatalf("brief %s attached unproven text %q:\n%s", change, stale, got.Text)
						}
					}
				}
			}
		})
	}
}

func TestTodayChatWholeBriefRetainsUncitedInputBinding(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	brief := boundMemoryConversationFixture(t, app)
	if _, err := app.SaveMemoryEntry("background", "# Private background plan\nConfidential dependency."); err != nil {
		t.Fatal(err)
	}
	// An input consumed by synthesis can be absent from its chosen citations.
	brief.Sections.Doing = []MobileBriefItem{{ID: "doing-extra", Kind: "goal", Status: "active", Title: "Private background plan", Refs: []string{"background"}}}
	brief.Evidence = append(brief.Evidence, MobileBriefRef{Ref: "background", SourceKind: "memory", SourceID: "background", Label: "Private background plan"})
	bindConversationBriefFixture(t, app, &brief)
	if len(brief.modelBinding.Sources) != 2 {
		t.Fatal("uncited source was not bound")
	}
	// Live section removal does not remove the stored typed dependency.
	brief.Sections.Doing = nil
	dir, _ := mobileBriefStateDir()
	if err := newMobileBriefStore(dir).save(brief, time.Now()); err != nil {
		t.Fatal(err)
	}
	ref := TodayChatContextRef{SourceKind: "brief", SourceID: mobileBriefConversationKey(app.mobileBriefForDelivery(brief)), BriefID: brief.BriefID}
	if _, err := app.PrepareTodayChatDraft(ref); err != nil {
		t.Fatalf("readable control: %v", err)
	}
	if _, err := app.SaveMemoryEntry("background", "# Corrected public background\nConfidential detail removed."); err != nil {
		t.Fatal(err)
	}
	got, err := app.buildTodayChatContext(t.Context(), ref)
	if err != nil {
		t.Fatalf("current brief items: %v", err)
	}
	if strings.Contains(got.Text, "Brief snapshot;") || strings.Contains(got.Text, brief.Narrative[0].Text) || strings.Contains(got.Text, "Confidential dependency") || strings.Contains(got.Text, "Private background plan") {
		t.Fatalf("removed uncited input was re-authorized after correction:\n%s", got.Text)
	}
}

func TestMobileBriefBindingRefusesChangesDuringComposition(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	brief := boundMemoryConversationFixture(t, app)
	binding := app.captureMobileBriefModelSources(t.Context(), brief, conversationGatherRevisionFixture(t, app))
	if binding == nil {
		t.Fatal("readable initial source missing")
	}
	if _, err := app.SaveMemoryEntry("launch", "# Corrected public plan\nOriginal content removed."); err != nil {
		t.Fatal(err)
	}
	app.finishMobileBriefModelBinding(t.Context(), &brief, binding)
	if brief.modelBinding != nil {
		t.Fatal("edited source blessed old synthesis at publication")
	}
}

func TestMobileBriefGenerationPersistsModelSourceBinding(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	source := boundMemoryConversationFixture(t, app)
	now := mobileBriefAsOfTime(source)
	svc := newMobileBriefService(app)
	svc.compose = composePublishableMobileBriefForTest
	svc.now = func() time.Time { return now }
	svc.location = time.Local
	svc.sleepState = nil
	svc.speaker = mobileBriefSpeaker{}
	svc.power = func(string) (func(), error) { return func() {}, nil }
	svc.publish = func(MobileBriefV1, bool) error { return nil }
	svc.gather = func(_ context.Context, slot string, start, at time.Time) (mobileBriefInput, error) {
		return mobileBriefInput{Slot: slot, WindowStart: start, AsOf: at, Location: time.Local, OpenLoops: []mobileBriefCandidate{{SourceKind: "memory", SourceID: "launch", Title: "Launch plan", Kind: "goal", Status: "active"}}}, nil
	}
	brief, err := svc.generate(t.Context(), "morning", mobileBriefReasonPhone)
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := svc.store.load(brief.BriefID)
	if !ok || stored.modelBinding == nil {
		t.Fatal("generation did not persist source proof")
	}
	ref := TodayChatContextRef{SourceKind: "brief", SourceID: mobileBriefConversationKey(app.mobileBriefForDelivery(stored)), BriefID: stored.BriefID}
	if _, err := app.PrepareTodayChatDraft(ref); err != nil {
		t.Fatalf("generated brief unreadable: %v", err)
	}
	if _, err := app.SaveMemoryEntry("launch", "# Launch plan\nCorrected body, same title."); err != nil {
		t.Fatal(err)
	}
	got, err := app.buildTodayChatContext(t.Context(), ref)
	if err != nil {
		t.Fatalf("current brief items: %v", err)
	}
	if strings.Contains(got.Text, "Brief snapshot;") || strings.Contains(got.Text, stored.Narrative[0].Text) || !strings.Contains(got.Text, "Corrected body, same title.") {
		t.Fatalf("same-title body correction admitted frozen prose:\n%s", got.Text)
	}
}

func TestTodayChatWholeBriefBindingSurvivesCitationRenumbering(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	brief := boundMemoryConversationFixture(t, app)
	bindConversationBriefFixture(t, app, &brief)
	original := brief.Evidence[0].Ref
	for _, section := range mobileBriefSectionLines(brief.Sections) {
		for i := range section.items {
			for j, id := range section.items[i].Refs {
				if id == original {
					section.items[i].Refs[j] = "renumbered"
				}
			}
		}
	}
	for i := range brief.Narrative {
		for j, id := range brief.Narrative[i].Refs {
			if id == original {
				brief.Narrative[i].Refs[j] = "renumbered"
			}
		}
	}
	for i := range brief.Offers {
		for j, id := range brief.Offers[i].Refs {
			if id == original {
				brief.Offers[i].Refs[j] = "renumbered"
			}
		}
	}
	brief.Evidence[0].Ref = "renumbered"
	dir, _ := mobileBriefStateDir()
	if err := newMobileBriefStore(dir).save(brief, time.Now()); err != nil {
		t.Fatal(err)
	}
	ref := TodayChatContextRef{SourceKind: "brief", SourceID: mobileBriefConversationKey(app.mobileBriefForDelivery(brief)), BriefID: brief.BriefID}
	if _, err := app.PrepareTodayChatDraft(ref); err != nil {
		t.Fatalf("citation-only projection invalidated the source binding: %v", err)
	}
}

func conversationGatherRevisionFixture(t *testing.T, app *App) *mobileBriefGatherRevision {
	t.Helper()
	revision, err := app.readMobileBriefGatherRevision(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return revision
}

// The target's "Your week" is part of the displayed whole brief, but uses
// its own frozen identities rather than daily citation IDs.
func TestTodayChatWholeBriefWeeklyRecapIntegration(t *testing.T) {
	for _, kind := range []string{"brief"} {
		t.Run(kind, func(t *testing.T) {
			app := newMobileBriefPurposeTestApp(t)
			main, err := app.ensureMainChat(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			tag := main.LocalID
			ctx := t.Context()
			now := time.Date(2026, 10, 23, 18, 0, 0, 0, time.UTC)
			app.engagement = newEngagementService(app.store, nil, engagementDelivery{})
			app.engagement.now = func() time.Time { return now }
			if err := app.store.SetUIState(ctx, onboardingStateKey, `{"completed":true}`); err != nil {
				t.Fatal(err)
			}
			journeyReminderPurpose(t, app, now)
			seedYourWeekMute(t, app, now)
			brief := boundMemoryConversationFixture(t, app)
			brief.BriefID, brief.LocalDate, brief.Slot = "2026-10-23-evening", "2026-10-23", mobileBriefSlotEvening
			brief.AsOf, brief.Timezone = now.Format(time.RFC3339), "UTC"
			if len(brief.Sections.OpenLoops) == 0 {
				t.Fatal("open-loop fixture missing")
			}
			brief.Sections.OpenLoops[0].AttentionReason = "due"
			brief.Sections.OpenLoops[0].AttentionRank = 1
			bindConversationBriefFixture(t, app, &brief)
			if len(brief.WeeklyRecap) == 0 || brief.YourWeek == nil || brief.WeeklyRecap[0].Text != brief.YourWeek.Headline {
				t.Fatalf("Your week missing: %+v", brief.WeeklyRecap)
			}
			names := yourWeekModelNames(brief.YourWeek)
			if len(names) == 0 {
				t.Fatal("week has no names for a model lead")
			}
			weekLead, weekLeadFacts := validateYourWeekModelLead("A week moved forward on "+names[0]+".", brief.YourWeek, "en")
			if weekLead == "" {
				t.Fatalf("could not validate model lead for %q", names[0])
			}
			withLeadBrief := brief
			withLeadBrief.WeekLead, withLeadBrief.WeekLeadFacts = weekLead, weekLeadFacts
			withLead := *brief.YourWeek
			withLead.Headline = weekLead
			withLeadBrief.YourWeek = &withLead
			withLeadBrief.WeeklyRecap = nil
			for _, paragraph := range yourWeekParagraphs(withLeadBrief.YourWeek, "en") {
				withLeadBrief.WeeklyRecap = append(withLeadBrief.WeeklyRecap, MobileBriefPara{Text: paragraph.Text, Refs: []string{}, Role: paragraph.Role})
			}
			if mobileBriefSynthesisDigest(withLeadBrief) != brief.modelBinding.Synthesis ||
				!app.validateMobileBriefModelSources(ctx, withLeadBrief, brief.modelBinding) {
				t.Fatal("model lead changed the deterministic source binding")
			}
			lead := brief.YourWeek.Headline
			dir, _ := mobileBriefStateDir()
			if err := newMobileBriefStore(dir).save(brief, now); err != nil {
				t.Fatal(err)
			}
			refFor := func(b MobileBriefV1) TodayChatContextRef {
				r := TodayChatContextRef{SourceKind: kind, SourceID: mobileBriefConversationKey(b), BriefID: b.BriefID}
				return r
			}
			originalRef := refFor(brief)
			item, _, err := app.loadTodayChatContextItem(ctx, originalRef)
			if err != nil || !strings.Contains(item.Record.Text, `"weeklyRecap"`) || !strings.Contains(item.Record.Text, lead) {
				t.Fatalf("whole weekly payload missing: %+v %v", item, err)
			}
			if _, err := app.PrepareTodayChatDraft(originalRef); err != nil {
				t.Fatal(err)
			}
			changed := brief
			changed.WeeklyRecap = append([]MobileBriefPara(nil), brief.WeeklyRecap...)
			changed.WeeklyRecap[0].Text += " Changed recap."
			if mobileBriefConversationKey(changed) == mobileBriefConversationKey(brief) {
				t.Fatal("weekly copy absent from the revision")
			}
			if mobileBriefSynthesisDigest(changed) != mobileBriefSynthesisDigest(brief) ||
				!app.validateMobileBriefModelSources(ctx, changed, brief.modelBinding) {
				t.Fatal("cached weekly copy changed the daily synthesis proof")
			}
			// Undoing the mute withdraws its learned line from the week.
			if err := app.engagement.SetKindEnabled(ctx, engagement.KindTopicFinds, true); err != nil {
				t.Fatal(err)
			}
			current := app.mobileBriefForDelivery(brief)
			if mobileBriefConversationKey(current) == mobileBriefConversationKey(brief) {
				t.Fatal("withdrawal did not change revision")
			}
			for i, ref := range []TodayChatContextRef{originalRef, refFor(current)} {
				if i == 1 {
					// The current revision stays open to talk about, with only
					// the current state of its items (an offer: its subject):
					// no written text, no recap.
					got, err := app.buildTodayChatContext(ctx, ref)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(got.Text, `\"weeklyRecap\"`) || strings.Contains(got.Text, "Brief snapshot;") {
						t.Fatalf("withdrawn weekly context admitted:\n%s", got.Text)
					}
					continue
				}
				if _, _, err := app.loadTodayChatContextItem(ctx, ref); err == nil {
					t.Fatal("withdrawn weekly context admitted")
				}
				if _, err := app.PrepareTodayChatDraft(ref); err == nil {
					t.Fatal("withdrawn weekly draft admitted")
				}
				row := storage.StreamItemRow{ID: "withdrawn-weekly-" + kind, Kind: "user", Content: "Discuss the brief."}
				if _, _, err := app.admitTodayChatTurn(ctx, tag, row, ref, nil); err == nil {
					t.Fatal("withdrawn weekly turn admitted")
				}
			}
		})
	}
}
