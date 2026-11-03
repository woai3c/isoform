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

	"wails-gui/internal/pii"
	"yansu-agent/agent"
)

// createTodayDuePurpose stores a commitment due tomorrow whose evidence is an
// activity session in Notes, so the test can later make that source private.
func createTodayDuePurpose(t *testing.T, a *App, title, outcome string) (PurposeOpenLoopDetail, string) {
	t.Helper()
	base := time.Now().Add(-time.Minute).UnixMilli()
	if err := a.activity.store.SaveSession(ActivitySessionRow{ID: "payroll-session", AppNames: `["Notes"]`, StartTime: base, EndTime: base + 1}, nil, nil); err != nil {
		t.Fatal(err)
	}
	evidence := purposeTestEvidence("payroll", PurposeEvidenceObservedUser, PurposeStanceSupport, base)
	evidence.SourceKind, evidence.SourceID = "activity_session", "payroll-session"
	store, err := a.purposeProductStore()
	if err != nil {
		t.Fatal(err)
	}
	due := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	input := purposeTestCreateInput("payroll", PurposeKindCommitment, "payroll", []PurposeEvidenceInput{evidence}, base)
	input.Title, input.DesiredOutcome = title, outcome
	input.DueKind, input.DueValue = PurposeDueDate, due
	created, _, err := store.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := store.GetDetail(created.OpenLoop.ID)
	if err != nil {
		t.Fatal(err)
	}
	return detail, due
}

func TestTodayChatOwnItemFactsReachContextWhileThirdPartyPIIStaysRedacted(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	const title = "Run payroll for October 2026"
	detail, due := createTodayDuePurpose(t, a, title, "Pay the team; confirm with priya@example.com using code 482913")
	ref := TodayChatContextRef{SourceKind: "purpose", SourceID: detail.OpenLoop.ID}
	got, err := a.buildTodayChatContext(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	// The item's own due date, title and state are Isoform's facts: exact.
	// Ages are checked against a fixed clock elsewhere; here the date itself.
	for _, want := range []string{`"due":"` + due, title, `"state":"`, `"noted":"`, "<today-chat-guidance>", "Never open with"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("context lacks %q:\n%s", want, got.Text)
		}
	}
	if strings.Contains(got.Text, `"due":"[REDACTED`) || !strings.Contains(got.Text, `"title":"`+title+`"`) {
		t.Errorf("the item's own due date or title was redacted:\n%s", got.Text)
	}
	// Other people's details and codes keep their redaction.
	for _, private := range []string{"priya@example.com", "482913"} {
		if strings.Contains(got.Text, private) {
			t.Errorf("third-party detail %q reached the context:\n%s", private, got.Text)
		}
	}
	draft, err := a.PrepareTodayChatDraft(ref)
	if err != nil || draft.Prefill.Title != title {
		t.Fatalf("draft title = %+v, %v", draft.Prefill, err)
	}
	// A protected source still makes the item unavailable.
	previous := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(previous) })
	pii.SetUserSensitiveApps(map[string]string{"com.apple.Notes": "Notes"})
	if _, err := a.buildTodayChatContext(context.Background(), ref); !errors.Is(err, errTodayChatItemUnavailable) {
		t.Fatalf("item from a sensitive app stayed readable: %v", err)
	}
	if _, err := a.PrepareTodayChatDraft(ref); err == nil {
		t.Fatal("draft for an item from a sensitive app was prepared")
	}
}

func TestTodayChatPromptKeepsDatesAndYearsButNotCodes(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"Where do I stand on payroll, due 2026-10-20?", "Where do I stand on payroll, due 2026-10-20?"},
		{"Due 2026-10-20T17:00:00-07:00 sharp", "Due 2026-10-20T17:00:00-07:00 sharp"},
		{"Q4 2026 planning, from 10/20/2026", "Q4 2026 planning, from 10/20/2026"},
		{"Run payroll for October 2026, due Oct 20, 2026", "Run payroll for October 2026, due Oct 20, 2026"},
		{"2026年10月20日前完成", "2026年10月20日前完成"},
		{"Use code 482913 by 2026-10-20", "Use code [REDACTED:code] by 2026-10-20"},
		// A bare four-digit number is a code, never a year.
		{"PIN 2024", "PIN [REDACTED:code]"},
		{"Your PIN is 2024", "Your PIN is [REDACTED:code]"},
		{"Invoice 2045 from Acme", "Invoice [REDACTED:code] from Acme"},
		{"Order #2031", "Order #[REDACTED:code]"},
		{"Visa card ending 2041", "Visa card ending [REDACTED:code]"},
		{"INV-2045", "INV-[REDACTED:code]"},
		{"Ring (415)2026", "Ring (415)[REDACTED:code]"},
		{"Ring 415 555 2026 today", "Ring [REDACTED:code] today"},
		{"Ring 415-555-0123 today", "Ring [REDACTED:code] today"},
		{"Order 2026-10-20-4829", "Order [REDACTED:code]"},
		{"Email priya@example.com on 2026-10-20", "Email [REDACTED:private_email] on 2026-10-20"},
	} {
		if got := todayChatRedactPrompt(tc.in); got != tc.want {
			t.Errorf("%q -> %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A kept date between a code keyword and a short code must not hide the
// keyword: the code is redacted as it is with no date in between, and
// everything #69 keeps visible stays visible.
func TestTodayChatPromptRedactsAShortCodeAcrossAKeptDate(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"the door code for 2026-10-20 is 482", "the door code for 2026-10-20 is [REDACTED:code]"},
		{"my PIN as of 2026-10-20: 731", "my PIN as of 2026-10-20: [REDACTED:code]"},
		{"door code: from 2026-10-20 use 482", "door code: from 2026-10-20 use [REDACTED:code]"},
		{"482 is the code after 2026-10-20", "[REDACTED:code] is the code after 2026-10-20"},
		{"enter code 482 on 2026-10-20", "enter code [REDACTED:code] on 2026-10-20"},
		// What #69 keeps visible still stays visible across a date.
		{"exit code on 2026-10-20 was 127", "exit code on 2026-10-20 was 127"},
		{"error code from 2026-10-20 is 500", "error code from 2026-10-20 is 500"},
		{"HTTP status code since 2026-10-20 is 404", "HTTP status code since 2026-10-20 is 404"},
		{"Python 3.12 code from 2026-10-20", "Python 3.12 code from 2026-10-20"},
		{"code on 2026-10-20 cost 450,000", "code on 2026-10-20 cost 450,000"},
		{"code review 2026-10-20, room 302", "code review 2026-10-20, room 302"},
		{"Q4 2026 code freeze, room 302", "Q4 2026 code freeze, room 302"},
	} {
		if got := todayChatRedactPrompt(tc.in); got != tc.want {
			t.Errorf("%q -> %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTodayChatContextStatesTimesAndAgesAgainstNow(t *testing.T) {
	now := time.Date(2026, 10, 19, 11, 40, 0, 0, time.Local)
	record := todayChatRecord{Kind: "activity", ID: "a", At: now.Add(-25 * time.Hour).UnixMilli(), Text: "Payroll export"}
	item := todayChatItem{Label: "Payroll export", Record: record}
	got := buildTodayChatContextSections(item, todayChatSources{Now: now, Activities: []todayChatRecord{record}})
	for _, want := range []string{"Now: 2026-10-19 Mon 11:40", `"when":"2026-10-18 Sun 10:40 (yesterday)"`, "Then give the one next step"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("context lacks %q:\n%s", want, got.Text)
		}
	}
	due := todayChatFact{Name: "due", At: time.Date(2026, 10, 17, 23, 59, 59, 0, time.Local).UnixMilli(), DateOnly: true}
	if rendered := renderTodayChatFacts([]todayChatFact{due}, now); len(rendered) != 1 || rendered[0].Value != "2026-10-17 Sat (2 days ago, overdue)" {
		t.Fatalf("overdue fact = %+v", rendered)
	}
	for kind, want := range map[string]string{"brief": "go through today's brief", todayChatCardActionKind: "Do what it asks now", "purpose": "Start with what Isoform knows"} {
		if guidance := todayChatGuidance(todayChatItem{Prefill: TodayChatPrefill{Kind: kind}}); !strings.Contains(guidance, want) {
			t.Errorf("%s guidance lacks %q: %s", kind, want, guidance)
		}
	}
}

func TestPrepareTodayChatDraftsPreparesEachActionFromOneRead(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	detail, _ := createTodayDuePurpose(t, a, "Run payroll", "Pay the team")
	writeTodayContextMemory(t, "payroll-note", "# Payroll note\n\n- **Source**: user_edit\n\nRun payroll before the cutoff.")
	refs := []TodayChatContextRef{
		{SourceKind: "purpose", SourceID: detail.OpenLoop.ID},
		{SourceKind: "memory", SourceID: "payroll-note"},
		{SourceKind: "purpose", SourceID: "missing"},
		{SourceKind: "purpose", SourceID: strings.Repeat("x", 4096)},
	}
	results := a.PrepareTodayChatDrafts(refs)
	if len(results) != len(refs) {
		t.Fatalf("results = %+v", results)
	}
	single, err := a.PrepareTodayChatDraft(refs[0])
	if err != nil || results[0].Draft == nil || !reflect.DeepEqual(*results[0].Draft, single) {
		t.Fatalf("batch draft %+v differs from single %+v (%v)", results[0], single, err)
	}
	if results[1].Draft == nil || results[1].Draft.Prefill.Title != "Payroll note" {
		t.Fatalf("memory draft = %+v", results[1])
	}
	for _, i := range []int{2, 3} {
		if results[i].Draft != nil || results[i].Error != "unavailable" {
			t.Fatalf("result %d = %+v", i, results[i])
		}
	}
	if got := a.PrepareTodayChatDrafts(nil); len(got) != 0 {
		t.Fatalf("empty batch = %+v", got)
	}
}

// An offer is a brief card: its words are written from the subject's current
// facts at the click, so words stored with an older brief are never sent, and
// a client that sends them anyway is refused.
func TestTodayBriefCardNeverSendsWordsItsFactsNoLongerSupport(t *testing.T) {
	for _, change := range []string{"corrected", "renamed", "withdrawn"} {
		t.Run(change, func(t *testing.T) {
			app, tag, _ := todaySendTestApp(t)
			brief := boundMemoryConversationFixture(t, app)
			bindConversationBriefFixture(t, app, &brief)
			// Newer briefs write more than the title into an offer's prompt.
			brief.Offers[0].Prompt = "Help me draft the interview guide for “Launch plan”, due Thursday, with the Acme numbers."
			binding := *brief.modelBinding
			binding.Synthesis = mobileBriefSynthesisDigest(brief)
			brief.modelBinding = &binding
			dir, _ := mobileBriefStateDir()
			if err := newMobileBriefStore(dir).save(brief, time.Now()); err != nil {
				t.Fatal(err)
			}
			ref := TodayChatContextRef{SourceKind: todayChatBriefCardKind, SourceID: brief.Offers[0].ID, BriefID: brief.BriefID}
			current, err := app.PrepareTodayChatDraft(ref)
			if err != nil || current.Message == "" || strings.Contains(current.Message, "Acme numbers") || !strings.Contains(current.Message, "Launch plan") {
				t.Fatalf("the card's words = %+v, %v", current, err)
			}
			switch change {
			case "corrected":
				_, err = app.SaveMemoryEntry("launch", "# Launch plan\nThe numbers moved to next week.")
			case "renamed":
				_, err = app.SaveMemoryEntry("launch", "# Release checklist\nThe numbers moved to next week.")
			case "withdrawn":
				memories, _ := memoryDir()
				err = os.Remove(filepath.Join(memories, "launch.md"))
			}
			if err != nil {
				t.Fatal(err)
			}
			draft, err := app.PrepareTodayChatDraft(ref)
			if change == "withdrawn" {
				if err == nil {
					t.Fatalf("a card about a withdrawn source was prepared: %+v", draft)
				}
			} else {
				want := map[string]string{"corrected": "Launch plan", "renamed": "Release checklist"}[change]
				if err != nil || !strings.Contains(draft.Message, want) || strings.Contains(draft.Message, "Acme numbers") || change == "renamed" && strings.Contains(draft.Message, "Launch plan") {
					t.Fatalf("the card's words after %s = %+v, %v", change, draft, err)
				}
			}

			// The stored words, shown in the box and sent unedited after they
			// stopped standing, are refused before anything reaches the model.
			ref.GeneratedMessage = brief.Offers[0].Prompt
			runner := &upcomingCaptureRunner{inputs: make(chan *agent.TaskInput, 1)}
			app.runner = runner
			app.tagCancels = map[string]context.CancelFunc{}
			app.sessionTempDirs = map[string]string{}
			app.streams = map[string]context.CancelFunc{}
			app.runIDByTag = map[string]string{}
			app.pendingTraceIDs = map[string]string{}
			stored := brief.Offers[0].Prompt
			if _, err := app.PrepareTodayChatTurn(tag, stored, ref); err == nil {
				t.Fatal("stored offer words were prepared for sending")
			}
			if _, err := app.SendPromptWithTodayContext(stored, "", "m", "", tag, nil, nil, false, false, "en", "", StreamItemData{ID: "stored-offer", Kind: "user"}, ref); err == nil {
				t.Fatal("stored offer words were sent")
			}
			// Today's panel sends the same reference through its own binding
			// and is refused the same way.
			if _, err := app.SendMainChatWithContext(stored, ref); err == nil {
				t.Fatal("stored offer words were sent from Today's panel")
			}
			select {
			case <-runner.inputs:
				t.Fatal("stored offer words reached the model")
			default:
			}
			if change == "renamed" {
				// Words the user edited go as written, naming the item as it
				// reads now.
				checked, err := app.PrepareTodayChatTurn(tag, stored+" Thanks.", ref)
				if err != nil || !strings.Contains(checked, "Release checklist") || strings.Contains(checked, "Launch plan") {
					t.Fatalf("edited words = %q, %v", checked, err)
				}
			}
			if change == "corrected" {
				// The card's current words go through with the current record.
				if _, err := app.SendPromptWithTodayContext(draft.Message, "", "m", "", tag, nil, nil, false, false, "en", "", StreamItemData{ID: "card-offer", Kind: "user"}, ref); err != nil {
					t.Fatal(err)
				}
				select {
				case input := <-runner.inputs:
					prompt, _ := input.PromptFunc()
					if strings.Contains(prompt, "Acme numbers") || !strings.Contains(prompt, "numbers moved to next week") {
						t.Fatalf("rebuilt request context:\n%s", prompt)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("rebuilt request did not reach the model")
				}
				waitForTagIdle(t, app, tag)
			}
			page, err := app.store.LoadStreamItemsLatest(context.Background(), tag, 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range page.Items {
				if strings.Contains(row.Content, "Acme numbers") {
					t.Fatalf("stored offer words entered history: %q", row.Content)
				}
			}
		})
	}
}

// A schedule's text stays exactly what stored brief proofs hashed, and since it
// is built from the schedule's own fields its run time stays readable.
func TestTodayChatScheduleTextKeepsItsProofAndReadableRunTime(t *testing.T) {
	next := time.Date(2026, 10, 20, 8, 0, 0, 0, time.Local)
	job := CronJobState{ID: "job", Name: "Daily News", ScheduleDescription: "Every morning", Enabled: true, NextRunAt: next.UnixMilli(), LastRunStatus: "success"}
	record := todayChatScheduleRecord(job)
	if want := "Daily News; Every morning; enabled: true; next run: " + next.Format(time.RFC3339) + "; last status: success"; record.Text != want {
		t.Fatalf("text = %q, want %q", record.Text, want)
	}
	if digest, _ := json.Marshal(record); strings.Contains(string(digest), "facts") || strings.Contains(string(digest), "when") {
		t.Fatalf("rendered fields entered the source digest: %s", digest)
	}
	packed, _ := packTodayChatSection(withTodayChatTimes([]todayChatRecord{record}, next.Add(-time.Hour)), todayChatItemSectionTokens)
	if !strings.Contains(packed, next.Format(time.RFC3339)) || !strings.Contains(packed, `"nextRun":"2026-10-20 Tue 08:00 (today, in 1 hour)"`) {
		t.Fatalf("run time not readable: %s", packed)
	}
}
