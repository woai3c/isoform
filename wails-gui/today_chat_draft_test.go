package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"wails-gui/internal/calendar"
	"wails-gui/internal/pii"
	"wails-gui/storage"
)

func TestTodayChatPrefillKindsUseSourceFacts(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	for source, kind := range map[string]string{"activity": "activity", "summary": "activity", "meeting": "meeting", mobileBriefCalendarSource: "meeting", "purpose": "purpose", "handoff": "open_loop", "history": "open_loop", "deliverable": "file", mobileBriefFileSource: "file", "find": "news", "news": "news", mobileBriefWebSource: "news", "memory": "generic", "schedule": "generic"} {
		t.Run(source, func(t *testing.T) {
			record := todayChatRecord{At: now.Add(-time.Hour).UnixMilli(), DurationMinutes: 18}
			got := todayChatSourcePrefill(source, record, now)
			if got.Kind != kind || got.OccurredAt != record.At || got.StartsAt != 0 {
				t.Fatalf("prefill = %+v", got)
			}
			if (kind == "activity" && got.DurationMinutes != 18) || (kind != "activity" && got.DurationMinutes != 0) {
				t.Fatalf("duration = %+v", got)
			}
		})
	}
}

func TestTodayChatCalendarPrefillOnlyTimesUpcomingTimedEvents(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name             string
		offset           time.Duration
		allDay           bool
		starts, occurred bool
	}{
		{"upcoming", time.Hour, false, true, false},
		{"past", -time.Hour, false, false, true},
		{"in progress", -time.Minute, false, false, true},
		{"all day", time.Hour, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev := calendar.Event{Start: now.Add(tc.offset), End: now.Add(tc.offset + time.Hour), AllDay: tc.allDay, Attendees: []calendar.Attendee{{Name: "Me", IsMe: true}, {Name: "Tim"}, {Name: "Declined", Status: calendar.StatusDeclined}, {Email: "unnamed@example.com"}}}
			got := todayChatCalendarPrefill(ev, now)
			if got.Kind != "meeting" || (got.StartsAt != 0) != tc.starts || (got.OccurredAt != 0) != tc.occurred || !reflect.DeepEqual(got.People, []string{"Tim"}) {
				t.Fatalf("prefill = %+v", got)
			}
		})
	}
}

func TestTodayChatPurposeWaitingSinceRequiresCompleteTransitionChain(t *testing.T) {
	active := PurposeOpenLoopEventRecord{ID: "active", LifecycleState: PurposeStateActive, OccurredAt: 10}
	waiting := PurposeOpenLoopEventRecord{ID: "waiting", PredecessorEventID: "active", LifecycleState: PurposeStateWaiting, OccurredAt: 20}
	revised := PurposeOpenLoopEventRecord{ID: "revised", PredecessorEventID: "waiting", LifecycleState: PurposeStateWaiting, OccurredAt: 30}
	for _, tc := range []struct {
		name    string
		current PurposeOpenLoopEventRecord
		events  []PurposeOpenLoopEventRecord
		want    int64
	}{
		{"active", active, []PurposeOpenLoopEventRecord{active}, 0},
		{"transition", waiting, []PurposeOpenLoopEventRecord{active, waiting}, 20},
		{"revision", revised, []PurposeOpenLoopEventRecord{active, waiting, revised}, 20},
		{"missing transition", revised, []PurposeOpenLoopEventRecord{revised}, 0},
		{"missing before transition", revised, []PurposeOpenLoopEventRecord{waiting, revised}, 0},
		{"cycle", revised, []PurposeOpenLoopEventRecord{{ID: "waiting", PredecessorEventID: "revised", LifecycleState: PurposeStateWaiting, OccurredAt: 20}}, 0},
		{"backdated", revised, []PurposeOpenLoopEventRecord{{ID: "waiting", LifecycleState: PurposeStateWaiting, OccurredAt: 40}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := todayChatPurposePrefill(PurposeOpenLoopDetail{Current: tc.current, Events: tc.events})
			if got.WaitingSince != tc.want || (got.Kind == "open_loop") != (tc.current.LifecycleState == PurposeStateWaiting) {
				t.Fatalf("prefill = %+v, want since %d", got, tc.want)
			}
		})
	}
}

func saveTodayDraftActivity(t *testing.T, app *App, id, title string, at time.Time, minutes int) {
	t.Helper()
	start, end := at.Add(-time.Duration(minutes)*time.Minute).UnixMilli(), at.UnixMilli()
	if err := app.activity.store.SaveSession(ActivitySessionRow{ID: id, AppNames: `["Code"]`, StartTime: start, EndTime: end, TriageStatus: "done"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	run := testRun("run-"+id, "Code", start, end, end-start, 1)
	run.FirstEventRowID, run.LastEventRowID = 1, 1
	run.SessionIDs = []string{id}
	insertComposableTimelineRun(t, app.activity.store, run, ActivityTimelineGroupSummary{})
	apps, _ := json.Marshal(map[string]int64{"Code": end - start})
	runs, _ := json.Marshal([]string{run.ID})
	if err := app.activity.store.InsertActivity(ActivityRow{ID: id, Title: title, Description: "Implemented Atlas release checks with Alex Chen", LatestChange: "Validation passed", RunIDs: string(runs), ActiveMsByApp: string(apps), StartMs: start, EndMs: end}); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareTodayChatDraftCountsMatchSendContextWithoutWritingHistory(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	now := time.Now().Add(-time.Minute)
	const title = "Atlas launch with Alex Chen"
	saveTodayDraftActivity(t, app, "selected", title, now, 18)
	writeTodayContextMemory(t, "atlas", "# Atlas plan\n\n- **Source**: user_edit\n\nAtlas release checklist")
	knowledge, err := knowledgeDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(knowledge, "atlas.md"), []byte("# Atlas guide\n\nAtlas release guide"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.CreatePrincipal(context.Background(), storage.PrincipalRow{ID: "alex", Kind: storage.PrincipalKindContact, DisplayName: "Alex Chen", Relationship: "colleague"}); err != nil {
		t.Fatal(err)
	}
	ref := TodayChatContextRef{SourceKind: "activity", SourceID: "selected"}
	draft, err := app.PrepareTodayChatDraft(ref)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Prefill.Kind != "activity" || draft.Prefill.Title != title || draft.Prefill.DurationMinutes != 18 || draft.Prefill.OccurredAt != now.UnixMilli() {
		t.Fatalf("prefill = %+v", draft.Prefill)
	}
	context, err := app.buildTodayChatContext(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if draft.Counts != context.Counts || draft.Counts != (TodayChatContextCounts{Memories: 2, Activities: 1, People: 1}) {
		t.Fatalf("preview=%+v send=%+v", draft.Counts, context.Counts)
	}
	if strings.Index(context.Text, "<item-activity>") > strings.Index(context.Text, "<item>") || !strings.Contains(context.Text, "Implemented Atlas release checks") {
		t.Fatalf("selected work summary did not lead: %s", context.Text)
	}
	// Opening another source must not reserve the main chat or persist a turn.
	if len(app.activeTags) != 0 {
		t.Fatal("preview reserved a chat turn")
	}
}

func TestTodayChatItemTitleStaysVerbatimWithinUTF8Budgets(t *testing.T) {
	for _, title := range []string{"Atlas <release> \"quoted\" 研发", strings.Repeat("研", 240), "Atlas 4111 1111 1111 1111", strings.Repeat("研", 1000)} {
		t.Run(title[:min(len(title), 20)], func(t *testing.T) {
			item := todayChatItem{Label: title, Record: todayChatRecord{Kind: "activity", ID: "selected", Text: strings.Repeat("Summary 研发 ", 700)}}
			result := buildTodayChatContextSections(item, todayChatSources{Activities: []todayChatRecord{item.Record, {Kind: "activity", ID: "other", At: 100, Text: "Summary related"}}})
			var total, activities int
			for _, section := range []string{"item-activity", "item", "activities"} {
				start, end := strings.Index(result.Text, "<"+section+">\n"), strings.Index(result.Text, "</"+section+">")
				if start < 0 {
					continue
				}
				body := result.Text[start+len(section)+3 : end]
				if !utf8.ValidString(body) {
					t.Fatal("invalid UTF-8")
				}
				if section != "item" {
					total += len(body)
				}
				for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
					var record todayChatRecord
					if err := json.Unmarshal([]byte(line), &record); err != nil {
						t.Fatal(err)
					}
					if section == "item" {
						redacted, _ := pii.RedactText(title)
						if len(redacted) < 1000 && record.Title != redacted {
							t.Fatalf("title = %q, want %q", record.Title, redacted)
						}
						if len(body) > todayChatItemSectionTokens {
							t.Fatal("item budget exceeded")
						}
					} else {
						activities++
					}
				}
			}
			if total > todayChatActivityTokens || result.Counts.Activities != activities {
				t.Fatalf("activity budget/count: %d, %+v, packed %d", total, result.Counts, activities)
			}
		})
	}
}

func TestPrepareTodayChatDraftZeroCountsAndForgottenSource(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	t.Setenv(topicsStateDirEnvVar, t.TempDir())
	if _, err := app.topicsSvc().store.update(func(state *topicsStateFile) {
		enabled := true
		state.Enabled = &enabled
		state.Finds = topicFindsState{RanOn: time.Now().Format("2006-01-02"), Items: []TopicFind{{ID: "find", Title: "A new discovery", URL: "https://example.com/discovery"}}}
	}); err != nil {
		t.Fatal(err)
	}
	draft, err := app.PrepareTodayChatDraft(TodayChatContextRef{SourceKind: "find", SourceID: "find"})
	if err != nil || draft.Prefill.Kind != "news" || draft.Prefill.Title != "A new discovery" || draft.Counts != (TodayChatContextCounts{}) {
		t.Fatalf("empty context draft: %+v, %v", draft, err)
	}
	writeTodayContextMemory(t, "forget", "# Atlas forgotten title\n\nAtlas forgotten body")
	checkMemoryFolderNow(app)
	ref := TodayChatContextRef{SourceKind: "memory", SourceID: "forget"}
	if _, err := app.PrepareTodayChatDraft(ref); err != nil {
		t.Fatal(err)
	}
	dir, _ := memoryDir()
	body, _ := os.ReadFile(filepath.Join(dir, "forget.md"))
	if err := recordMemoryTombstone(dir, "forget", []string{string(body)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.PrepareTodayChatDraft(ref); !errors.Is(err, errTodayChatItemUnavailable) {
		t.Fatalf("forgotten preview = %v", err)
	}
}

func TestPrepareTodayChatDraftPurposeSectionsIgnoreOnlyDisplayDecorations(t *testing.T) {
	for _, section := range []string{"workingToward", "commitments", "waitingOnYou", "openLoops"} {
		t.Run(section, func(t *testing.T) {
			app := newTodayContextTestApp(t, true)
			now := time.Now()
			writeTodayContextMemory(t, "source", "# Atlas source\n\nAtlas independent source")
			evidence := purposeTestEvidence("source", PurposeEvidenceExplicitUser, PurposeStanceSupport, now.Add(-time.Minute).UnixMilli())
			evidence.SourceKind, evidence.SourceID = "memory", "source"
			evidence.SourceHash = agencyPayloadHash([]byte("# Atlas source\n\nAtlas independent source"))
			detail := createTodayContextPurpose(t, app, "purpose-draft", "Atlas commitment", evidence)
			candidate := mobileBriefCandidate{SourceKind: "purpose", SourceID: detail.OpenLoop.ID, Title: "Cached Atlas title", At: now.Add(-time.Minute), Evidence: []mobileBriefCandidate{{SourceKind: "activity_session", SourceID: "display-only-unavailable", Title: "Untrusted private cached decoration", At: now}}}
			in := mobileBriefInput{Slot: mobileBriefSlotMorning, WindowStart: now.Add(-time.Hour), AsOf: now}
			switch section {
			case "workingToward":
				in.WorkingToward = []mobileBriefCandidate{candidate}
			case "commitments":
				in.Commitments = []mobileBriefCandidate{candidate}
			case "waitingOnYou":
				in.WaitingOnYou = []mobileBriefCandidate{candidate}
			case "openLoops":
				in.OpenLoops = []mobileBriefCandidate{candidate}
			}
			brief := buildMobileBriefV1(in)
			items := append(append(brief.Sections.WorkingToward, brief.Sections.Commitments...), brief.Sections.WaitingOnYou...)
			items = append(items, brief.Sections.OpenLoops...)
			if len(items) != 1 || len(items[0].Refs) != 2 {
				t.Fatalf("missing decorated Purpose card: %+v", items)
			}
			dir, _ := mobileBriefStateDir()
			if err := newMobileBriefStore(dir).save(brief, now); err != nil {
				t.Fatal(err)
			}
			ref := TodayChatContextRef{SourceKind: "brief_item", SourceID: items[0].ID, BriefID: brief.BriefID}
			draft, err := app.PrepareTodayChatDraft(ref)
			wantKind := "purpose"
			if section == "waitingOnYou" || section == "openLoops" {
				wantKind = "open_loop"
			}
			if err != nil || draft.Prefill.Kind != wantKind || draft.Prefill.Title != "Atlas commitment" || draft.Counts.Memories != 1 {
				t.Fatalf("Purpose preview=%+v err=%v", draft, err)
			}
			bundle, err := app.buildTodayChatContext(context.Background(), ref)
			if err != nil || strings.Contains(bundle.Text, "Untrusted private") || strings.Contains(bundle.Text, "Cached Atlas") {
				t.Fatalf("decorations entered context: %s, %v", bundle.Text, err)
			}
			memory, _ := memoryDir()
			if err := os.Remove(filepath.Join(memory, "source.md")); err != nil {
				t.Fatal(err)
			}
			if _, err := app.PrepareTodayChatDraft(ref); !errors.Is(err, errTodayChatItemUnavailable) {
				t.Fatalf("missing canonical evidence accepted: %v", err)
			}
		})
	}
}

func TestPrepareTodayChatDraftRechecksCalendarPermissionWithoutCachedLabels(t *testing.T) {
	app := newTodayContextTestApp(t, true)
	now := time.Now()
	brief := buildMobileBriefV1(mobileBriefInput{Slot: mobileBriefSlotMorning, WindowStart: now.Add(-time.Hour), AsOf: now, Meetings: []mobileBriefCandidate{{SourceKind: mobileBriefCalendarSource, SourceID: "event", Title: "Private calendar title", At: now.Add(time.Hour)}}})
	dir, _ := mobileBriefStateDir()
	if err := newMobileBriefStore(dir).save(brief, now); err != nil {
		t.Fatal(err)
	}
	if err := app.store.SetUIState(context.Background(), permissionUIStateKey(calendarPermissionKey), "false"); err != nil {
		t.Fatal(err)
	}
	ref := TodayChatContextRef{SourceKind: "brief_item", SourceID: brief.Sections.Meetings[0].ID, BriefID: brief.BriefID}
	if _, err := app.PrepareTodayChatDraft(ref); !errors.Is(err, errTodayChatItemUnavailable) {
		t.Fatalf("disabled calendar preview = %v", err)
	}
}
