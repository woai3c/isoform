package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"wails-gui/internal/handoff"
	"wails-gui/internal/pii"
	"wails-gui/storage"
)

func newTodayContextTestApp(t *testing.T, withStores bool) *App {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "tc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	t.Setenv("YANSU_MOBILE_STATE_DIR", filepath.Join(home, "mobile"))
	a := &App{}
	if withStores {
		state, err := storage.New(filepath.Join(home, "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = state.Close() })
		activity, err := NewActivityStore(filepath.Join(home, "activity"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = activity.Close() })
		a.store = state
		a.activity = &activityMonitorState{store: activity}
	}
	return a
}

func writeTodayContextMemory(t *testing.T, id, body string) {
	t.Helper()
	dir, err := memoryDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestTodayChatContextSectionsBoundRedactAndCountNewestRecords(t *testing.T) {
	item := todayChatItem{Record: todayChatRecord{Kind: "find", ID: "item", Text: "Atlas release with Alex"}, Refs: []MobileBriefRef{{SourceKind: "memory", SourceID: "direct", Label: "Referenced note"}}}
	sources := todayChatSources{}
	for i := 0; i < 30; i++ {
		text := "Atlas password: correct-horse-battery-staple " + strings.Repeat("context 引用 <unsafe> ", 20)
		sources.Memories = append(sources.Memories, todayChatRecord{Kind: "memory", ID: fmt.Sprintf("m%02d", i), At: int64(i + 1), Text: text})
		sources.Knowledge = append(sources.Knowledge, todayChatRecord{Kind: "knowledge", ID: fmt.Sprintf("k%02d", i), At: int64(i + 1), Text: text})
		sources.Activities = append(sources.Activities, todayChatRecord{Kind: "activity", ID: fmt.Sprintf("a%02d", i), At: int64(i + 1), Text: text})
		sources.People = append(sources.People, storage.PrincipalRow{ID: fmt.Sprintf("p%02d", i), DisplayName: "Alex", UpdatedAt: int64(i + 1), Notes: text})
		sources.Purposes = append(sources.Purposes, PurposeOpenLoopDetail{OpenLoop: PurposeOpenLoopRecord{ID: fmt.Sprintf("purpose%02d", i), Kind: "commitment"}, Current: PurposeOpenLoopEventRecord{Title: "Atlas launch", DesiredOutcome: text, OccurredAt: int64(i + 1)}})
	}
	sources.Memories = append(sources.Memories, todayChatRecord{Kind: "memory", ID: "direct", At: 100, Text: "No topic words: chosen by evidence identity"})
	got := buildTodayChatContextSections(item, sources)
	if strings.Contains(got.Text, "correct-horse-battery-staple") {
		t.Fatal("password entered model context")
	}
	if !strings.Contains(got.Text, "REDACTED") {
		t.Fatal("expected redaction marker")
	}
	counts := map[string]int{}
	for section, budget := range map[string]int{"item": todayChatItemTokens, "evidence": todayChatEvidenceTokens, "memory": todayChatMemoryTokens, "knowledge": todayChatKnowledgeTokens, "activities": todayChatActivityTokens, "people": todayChatPeopleTokens, "purpose": todayChatPurposeTokens} {
		start := strings.Index(got.Text, "<"+section+">\n")
		end := strings.Index(got.Text, "</"+section+">")
		if start < 0 || end < 0 {
			t.Fatalf("missing section %s", section)
		}
		body := got.Text[start+len(section)+3 : end]
		if len(body) > budget || !utf8.ValidString(body) {
			t.Fatalf("%s size=%d budget=%d UTF8=%v", section, len(body), budget, utf8.ValidString(body))
		}
		var previous int64 = 1 << 62
		for _, line := range strings.Split(strings.TrimSpace(body), "\n") {
			var row todayChatRecord
			if err := json.Unmarshal([]byte(line), &row); err != nil {
				t.Fatal(err)
			}
			if row.At > previous {
				t.Fatalf("%s not newest first", section)
			}
			previous = row.At
			counts[section]++
		}
	}
	if got.Counts.Memories != counts["memory"]+counts["knowledge"] || got.Counts.Activities != counts["activities"] || got.Counts.People != counts["people"] {
		t.Fatalf("inaccurate counts: %+v, %v", got.Counts, counts)
	}
	if !strings.Contains(got.Text, "chosen by evidence identity") {
		t.Fatal("direct reference was lost")
	}
}

func TestTodayChatContextMemoryReadControlsAndPrivacy(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	store := a.activity.store
	for _, row := range []ActivitySessionRow{
		{ID: "safe", AppNames: `["Code"]`, StartTime: 100, EndTime: 200},
		{ID: "protected", AppNames: `["Code"]`, StartTime: 300, EndTime: 400, TriageStatus: "protected"},
		{ID: "mail", AppNames: `["Mail"]`, StartTime: 500, EndTime: 600},
	} {
		if err := store.SaveSession(row, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	writeTodayContextMemory(t, "chosen", "# Atlas\n\n- **Source**: user_edit\n\nAtlas safe current note.")
	for id, body := range map[string]string{
		"old-observation": "# Atlas\n\n- **Source**: activity\n\nAtlas obsolete observation.",
		"sensitive":       "# Atlas secret app\n\n- **Apps**: 1Password\n\nAtlas hidden password manager content.",
		"protected":       "# Atlas protected\n\n- **Activity Session**: protected\n\nAtlas protected session content.",
		"mail":            "# Atlas mail\n\n- **Activity Session**: mail\n\nSubject: Atlas confidential mail subject",
		"transcript":      "# Atlas transcript\n\n- **Source**: meeting_transcription\n\nAtlas verbatim transcript.",
		"missing":         "# Atlas missing\n\n- **Activity Session**: nonexistent\n\nAtlas missing provenance.",
		"forgotten":       "# Forgotten Atlas\n\nAtlas forgotten source.",
	} {
		writeTodayContextMemory(t, id, body)
	}
	dir, _ := memoryDir()
	forgotten, _ := os.ReadFile(filepath.Join(dir, "forgotten.md"))
	if err := recordMemoryTombstone(dir, "forgotten", []string{string(forgotten)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	knowledgeDir, _ := knowledgeDir()
	if err := os.WriteFile(filepath.Join(knowledgeDir, "guide.md"), []byte("# Atlas guide\n\nAtlas API key sk-abcdefghijklmnopqrstuvwxyz1234567890ABCD"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(knowledgeDir, "mail.md"), []byte("# Atlas mail guide\n\n- **Apps**: Mail\n\nSubject: Atlas private guide"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := a.buildTodayChatContext(context.Background(), TodayChatContextRef{SourceKind: "memory", SourceID: "chosen"})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"obsolete observation", "password manager content", "protected session content", "confidential mail subject", "verbatim transcript", "missing provenance", "forgotten source", "private guide", "sk-abcdefghijklmnopqrstuvwxyz1234567890ABCD"} {
		if strings.Contains(got.Text, forbidden) {
			t.Errorf("context exposed %q", forbidden)
		}
	}
	if got.Counts.Memories != 2 {
		t.Fatalf("counts=%+v, want one memory and one knowledge", got.Counts)
	}
	for _, id := range []string{"protected", "mail", "transcript", "forgotten", "../../outside"} {
		if _, err := a.buildTodayChatContext(context.Background(), TodayChatContextRef{SourceKind: "memory", SourceID: id}); err == nil {
			t.Errorf("accepted excluded item %q", id)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.buildTodayChatContext(context.Background(), TodayChatContextRef{SourceKind: "knowledge", SourceID: "guide"}); err == nil {
		t.Fatal("knowledge injected when corrections could not be read")
	}
}

func TestTodayChatContextActivitySevenDaysAndReadTimeFilters(t *testing.T) {
	a := newTodayContextTestApp(t, true)
	now := time.Now()
	writeTodayContextMemory(t, "atlas", "# Atlas project\n\nAtlas release work.")
	for i, fixture := range []struct {
		id, app, title, status string
		age                    time.Duration
	}{
		{"safe", "Code", "Atlas release work", "done", time.Hour},
		{"old", "Code", "Atlas old work", "done", 8 * 24 * time.Hour},
		{"protected", "Code", "Atlas protected work", "protected", 2 * time.Hour},
		{"mail", "Mail", "Atlas mail subject", "done", 3 * time.Hour},
		{"private", "Code", "Atlas - Incognito", "done", 4 * time.Hour},
		{"paused", "Code", "Atlas paused work", "done", 5 * time.Hour},
	} {
		at := now.Add(-fixture.age).UnixMilli()
		apps, _ := json.Marshal([]string{fixture.app})
		row := ActivitySessionRow{ID: fixture.id, AppNames: string(apps), StartTime: at, EndTime: at + 1000, TriageStatus: fixture.status}
		if err := a.activity.store.SaveSession(row, nil, nil); err != nil {
			t.Fatal(err)
		}
		run := testRun("run-"+fixture.id, fixture.app, at, at+1000, 1000, 1)
		run.FirstEventRowID, run.LastEventRowID = int64(i+1), int64(i+1)
		run.SessionIDs = []string{fixture.id}
		insertComposableTimelineRun(t, a.activity.store, run, ActivityTimelineGroupSummary{})
		appTimes, _ := json.Marshal(map[string]int64{fixture.app: 1000})
		if err := a.activity.store.InsertActivity(ActivityRow{ID: fixture.id, Title: fixture.title, RunIDs: fmt.Sprintf(`["%s"]`, run.ID), ActiveMsByApp: string(appTimes), StartMs: at, EndMs: at + 1000}); err != nil {
			t.Fatal(err)
		}
		if fixture.id == "paused" {
			a.saveObservationPauses([]observationPause{{Start: at + 200, End: at + 400}})
		}
	}
	got, err := a.buildTodayChatContext(context.Background(), TodayChatContextRef{SourceKind: "memory", SourceID: "atlas"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Counts.Activities != 1 || !strings.Contains(got.Text, "Atlas release work") {
		t.Fatalf("wrong related activity: %+v\n%s", got.Counts, got.Text)
	}
	for _, term := range []string{"old work", "protected work", "mail subject", "Incognito", "paused work"} {
		if strings.Contains(got.Text, term) {
			t.Errorf("included %s", term)
		}
	}
	piiBefore := pii.GetUserSensitiveApps()
	t.Cleanup(func() { pii.SetUserSensitiveApps(piiBefore) })
	pii.SetUserSensitiveApps(map[string]string{"com.example.code": "Code"})
	got, err = a.buildTodayChatContext(context.Background(), TodayChatContextRef{SourceKind: "memory", SourceID: "atlas"})
	if err != nil || got.Counts.Activities != 0 {
		t.Fatalf("read-time sensitive app filter failed: %+v %v", got.Counts, err)
	}
}

func TestTodayChatContextPurposeEvidenceClosure(t *testing.T) {
	a := newTodayContextTestApp(t, false)
	purpose := func(id, kind, source string) PurposeOpenLoopDetail {
		return PurposeOpenLoopDetail{OpenLoop: PurposeOpenLoopRecord{ID: id, Kind: "commitment"}, Current: PurposeOpenLoopEventRecord{Title: "Atlas " + id}, Evidence: []PurposeEvidenceRecord{{SourceKind: kind, SourceID: source, PrivacyDecision: TaskContextPrivacyRedact}}}
	}
	sources := todayChatSources{Memories: []todayChatRecord{{Kind: "memory", ID: "allowed", Text: "Atlas"}}, Purposes: []PurposeOpenLoopDetail{
		purpose("safe", "memory_markdown", "allowed"), purpose("parent", "purpose_open_loop", "safe"),
		purpose("mail", "mail_thread", "opaque-mail-id"), purpose("derived-mail", "purpose_open_loop", "mail"),
		purpose("forgotten", "memory_markdown", "removed"), purpose("cycle-a", "purpose_open_loop", "cycle-b"), purpose("cycle-b", "purpose_open_loop", "cycle-a"),
	}}
	kept := a.filterTodayChatPurposes(sources)
	if len(kept) != 2 || kept[0].OpenLoop.ID != "safe" || kept[1].OpenLoop.ID != "parent" {
		t.Fatalf("unsafe Purpose evidence closure: %+v", kept)
	}
	sources.Purposes[0].Evidence[0].SourceHash = agencyPayloadHash([]byte("Superseded private statement"))
	if kept := a.filterTodayChatPurposes(sources); len(kept) != 0 {
		t.Fatalf("corrected evidence revived stale Purpose text: %+v", kept)
	}
	sources.Purposes[0].Evidence[0].SourceHash = agencyPayloadHash([]byte("Atlas"))
	if kept := a.filterTodayChatPurposes(sources); len(kept) != 2 {
		t.Fatalf("current source fingerprint rejected: %+v", kept)
	}
}

func TestTodayChatContextPurposeReferencesSelectDifferentWordsAndPeople(t *testing.T) {
	purpose := PurposeOpenLoopDetail{OpenLoop: PurposeOpenLoopRecord{ID: "commitment", Kind: "commitment"}, Current: PurposeOpenLoopEventRecord{Title: "Launch", Counterparty: "Alex Chen", LifecycleState: "active"}, Evidence: []PurposeEvidenceRecord{{SourceKind: "memory_markdown", SourceID: "guidance", PrivacyDecision: TaskContextPrivacyRedact}, {SourceKind: "knowledge_markdown", SourceID: "checklist", PrivacyDecision: TaskContextPrivacyRedact}}}
	sources := todayChatSources{Purposes: []PurposeOpenLoopDetail{purpose}, Memories: []todayChatRecord{{Kind: "memory", ID: "guidance", Text: "Use the blue version."}}, Knowledge: []todayChatRecord{{Kind: "knowledge", ID: "checklist", Text: "Review the transport protocol."}}, People: []storage.PrincipalRow{{ID: "alex", DisplayName: "Alex Chen", Relationship: "colleague"}}}
	got := buildTodayChatContextSections(todayChatItem{Record: todayChatRecord{Kind: "purpose", ID: "commitment", Text: "Launch"}}, sources)
	if got.Counts.Memories != 2 || got.Counts.People != 1 || !strings.Contains(got.Text, "blue version") || !strings.Contains(got.Text, "transport protocol") {
		t.Fatalf("indirect refs or people missing: %+v\n%s", got.Counts, got.Text)
	}
}

func TestTodayChatContextHandoffOmitsFreeFormDraftsAndUnknownProvenance(t *testing.T) {
	a := newTodayContextTestApp(t, false)
	store, err := handoff.NewStore(filepath.Join(os.Getenv("HOME"), "handoffs"))
	if err != nil {
		t.Fatal(err)
	}
	a.handoffStore = store
	for _, entry := range []handoff.Entry{{ID: "explicit", Title: "Atlas review", Status: handoff.StatusActive, Source: handoff.SourceMobile, Body: "Subject: secret launch; Draft: private email; Transcript: raw discussion"}, {ID: "unknown", Title: "Atlas protected title", Status: handoff.StatusActive, Source: handoff.SourceTriage, Body: "A plan without readable provenance"}} {
		if _, err := store.Save(entry); err != nil {
			t.Fatal(err)
		}
	}
	got, err := a.buildTodayChatContext(context.Background(), TodayChatContextRef{SourceKind: "handoff", SourceID: "explicit"})
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"secret launch", "private email", "raw discussion"} {
		if strings.Contains(got.Text, private) {
			t.Errorf("included %s", private)
		}
	}
	if _, err := a.buildTodayChatContext(context.Background(), TodayChatContextRef{SourceKind: "handoff", SourceID: "unknown"}); err == nil {
		t.Fatal("unknown generated provenance accepted")
	}
}

func TestTodayChatContextScheduleOmitsDescriptionTargetAndErrors(t *testing.T) {
	record := todayChatScheduleRecord(CronJobState{ID: "job", Name: "Atlas review", ScheduleDescription: "Every morning", Enabled: true,
		Description: "Subject: confidential launch", TargetConfig: map[string]string{"draft": "private recipient body"}, LastError: "Transcript: raw recording", LastRunStatus: "failed"})
	text, _ := packTodayChatSection([]todayChatRecord{record}, todayChatItemTokens)
	for _, forbidden := range []string{"confidential launch", "private recipient body", "raw recording"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("included %s", forbidden)
		}
	}
	if !strings.Contains(text, "Atlas review") || !strings.Contains(text, "Every morning") || !strings.Contains(text, "failed") {
		t.Fatal("operational context missing")
	}
}

func TestTodayChatContextBriefRevalidatesCurrentMemoryAndEvidence(t *testing.T) {
	a := newTodayContextTestApp(t, false)
	writeTodayContextMemory(t, "note", "# Atlas corrected\n\n- **Source**: user_edit\n\nAtlas current correction.")
	now := time.Now()
	brief := buildMobileBriefV1(mobileBriefInput{Slot: "morning", AsOf: now.Add(-time.Hour), WindowStart: now.Add(-24 * time.Hour), Learned: []mobileBriefCandidate{{SourceKind: "memory", SourceID: "note", Title: "Atlas stale private title", Detail: "Superseded note", At: now.Add(-2 * time.Hour)}}})
	dir, _ := mobileBriefStateDir()
	if err := newMobileBriefStore(dir).save(brief, now); err != nil {
		t.Fatal(err)
	}
	ref := TodayChatContextRef{SourceKind: "brief_item", SourceID: brief.Sections.Learned[0].ID, BriefID: brief.BriefID}
	got, err := a.buildTodayChatContext(context.Background(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Text, "stale private") || !strings.Contains(got.Text, "current correction") || !strings.Contains(got.Text, "<evidence>") {
		t.Fatalf("stale brief context: %s", got.Text)
	}
	memoryDir, _ := memoryDir()
	if err := os.Remove(filepath.Join(memoryDir, "note.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.buildTodayChatContext(context.Background(), ref); err == nil {
		t.Fatal("removed memory survived via brief label")
	}
	ref.SourceID = "client-invented-item"
	if _, err := a.buildTodayChatContext(context.Background(), ref); err == nil {
		t.Fatal("accepted client-invented source")
	}
}
