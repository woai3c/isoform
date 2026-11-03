package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMobileBriefGreetingJoinedToFirstSentence(t *testing.T) {
	one, two := 1, 2
	brief := MobileBriefV1{
		Narrative: []MobileBriefPara{
			{Text: "Good morning.", Refs: []string{"e1"}},
			{Text: "Three meetings today, and Priya is waiting on you.", Refs: []string{"e2"}},
			{Text: "The launch review moved to Thursday.", Refs: []string{"e3"}},
		},
		Cards: []MobileBriefCard{{ID: "c1", Paragraph: &one}, {ID: "c2", Paragraph: &two}, {ID: "c3"}},
		Audio: &MobileBriefAudio{Paragraphs: []MobileBriefAudioParagraph{
			{Source: mobileBriefAudioSourceNarrative, Index: 0, StartSeconds: 0},
			{Source: mobileBriefAudioSourceNarrative, Index: 1, StartSeconds: 1.2},
			{Source: mobileBriefAudioSourceNarrative, Index: 2, StartSeconds: 5},
		}},
	}
	joined := mobileBriefWithJoinedGreeting(brief)
	if len(joined.Narrative) != 2 || joined.Narrative[0].Text != "Good morning. Three meetings today, and Priya is waiting on you." {
		t.Fatalf("greeting not joined: %+v", joined.Narrative)
	}
	if strings.Join(joined.Narrative[0].Refs, ",") != "e1,e2" {
		t.Fatalf("refs not merged: %v", joined.Narrative[0].Refs)
	}
	if *joined.Cards[0].Paragraph != 0 || *joined.Cards[1].Paragraph != 1 || joined.Cards[2].Paragraph != nil {
		t.Fatalf("chips did not move with their paragraphs: %+v", joined.Cards)
	}
	starts := joined.Audio.Paragraphs
	if len(starts) != 2 || starts[0].Index != 0 || starts[0].StartSeconds != 0 || starts[1].Index != 1 || starts[1].StartSeconds != 5 {
		t.Fatalf("spoken starts did not move: %+v", starts)
	}
	if again := mobileBriefWithJoinedGreeting(joined); len(again.Narrative) != 2 || again.Narrative[0].Text != joined.Narrative[0].Text {
		t.Fatal("joining is not idempotent")
	}
	zh := mobileBriefWithJoinedGreeting(MobileBriefV1{Narrative: []MobileBriefPara{{Text: "早上好。"}, {Text: "今天有三个会。"}}})
	if len(zh.Narrative) != 1 || zh.Narrative[0].Text != "早上好。今天有三个会。" {
		t.Fatalf("zh greeting not joined: %+v", zh.Narrative)
	}
	for _, text := range []string{"Good morning, Bo.", "Hello!", "晚上好"} {
		if !mobileBriefIsGreeting(text) {
			t.Errorf("%q is a greeting", text)
		}
	}
	for _, text := range []string{"Good morning. Three meetings today.", "Morning meetings ran long and the review moved.", "Hi Priya asked about the spec yesterday and is still waiting on your answer."} {
		if mobileBriefIsGreeting(text) {
			t.Errorf("%q is not only a greeting", text)
		}
	}
	single := MobileBriefV1{Narrative: []MobileBriefPara{{Text: "Good morning."}}}
	if got := mobileBriefWithJoinedGreeting(single); len(got.Narrative) != 1 {
		t.Fatal("a lone greeting has nothing to join")
	}
}

func packageRouteData(t *testing.T, rec *httptest.ResponseRecorder) json.RawMessage {
	t.Helper()
	var wire struct {
		OK   bool            `json:"ok"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wire); err != nil || !wire.OK || rec.Code != http.StatusOK {
		t.Fatalf("route answered %d %s (%v)", rec.Code, rec.Body.String(), err)
	}
	return wire.Data
}

// The version GET /today names is the hash of the bytes GET /briefs/{id}
// serves, and a Today refresh between two reads changes neither.
func TestMobileBriefPackageVersionIsServedBytes(t *testing.T) {
	app, clock, brief, _ := dayShapeHoldFixture(t)
	svc := app.mobileBriefSvc.(*mobileBriefService)
	brief.NarrativeStatus = "ok"
	// Hold offers are withheld on delivery; use a readable calendar fact to
	// exercise greeting joining independently of that policy.
	brief.Narrative[0].Text = "Review is at 2:00 PM."
	brief.Narrative[0].Hold = nil
	brief.Narrative = append([]MobileBriefPara{{Text: "Good morning.", Refs: brief.Narrative[0].Refs}}, brief.Narrative...)
	if err := svc.store.save(brief, clock.Now()); err != nil {
		t.Fatal(err)
	}
	bridge := newMobileBriefPrivacyBridge(t, app)
	read := func() json.RawMessage {
		rec := httptest.NewRecorder()
		bridge.serveBrief(rec, httptest.NewRequest(http.MethodGet, "/briefs/"+brief.BriefID, nil), brief.BriefID)
		return packageRouteData(t, rec)
	}
	first := read()
	pkg, ok := bridge.briefPackage(brief.BriefID)
	if !ok || mobileBriefPackageVersion(first) != pkg.Version || !bytes.Equal(first, pkg.Bytes) {
		t.Fatalf("served bytes do not hash to the package version")
	}
	var document MobileBriefV1
	if err := json.Unmarshal(first, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Narrative) != 1 || !strings.HasPrefix(document.Narrative[0].Text, "Good morning. ") || document.Kind != mobileBriefSlotMorning {
		t.Fatalf("package did not join the greeting or name its kind: %+v", document.Narrative)
	}
	// A Today refresh re-projects live rows; it must not touch the package.
	clock.Advance(20 * time.Minute)
	if _, ok := svc.latestWithToday(); !ok {
		t.Fatal("Today refresh failed")
	}
	schedule, _ := svc.Schedule()
	briefs := bridge.todayBriefs(svc, clock.Now(), schedule)
	view := MobileTodayV1{}
	if len(briefs) > 0 {
		view.LatestBrief = &briefs[0]
		view.CurrentBrief = &mobileTodayBriefRef{ID: briefs[0].ID, Version: briefs[0].Version}
		if strings.HasPrefix(briefs[0].Lead, "Good morning") {
			t.Fatalf("the lead is the greeting: %q", briefs[0].Lead)
		}
	}
	second := read()
	if !bytes.Equal(first, second) {
		t.Fatalf("a Today refresh changed the served brief:\n%s\n%s", first, second)
	}
	if view.CurrentBrief == nil || view.CurrentBrief.ID != brief.BriefID || view.CurrentBrief.Version != mobileBriefPackageVersion(second) {
		t.Fatalf("Today names %+v, served version %s", view.CurrentBrief, mobileBriefPackageVersion(second))
	}
	if view.LatestBrief == nil || view.LatestBrief.BodyVersion != view.CurrentBrief.Version {
		t.Fatalf("older phones read another version: %+v", view.LatestBrief)
	}
}

type packageOnlySource struct {
	mobileBriefSource
	pkg mobileBriefPackage
}

func (s packageOnlySource) BriefPackage(id string) (mobileBriefPackage, bool) {
	return s.pkg, id == s.pkg.BriefID
}

func (packageOnlySource) Schedule() (MobileBriefSchedule, error)      { return MobileBriefSchedule{}, nil }
func (packageOnlySource) ListMobileBriefs(int) []MobileBriefSummary   { return nil }
func (packageOnlySource) GetMobileBrief(string) (MobileBriefV1, bool) { return MobileBriefV1{}, false }

func TestMobileBriefPackageServedInPartsAbove48KiB(t *testing.T) {
	large := MobileBriefV1{Version: 1, BriefID: "2026-10-28-on_request-084100", Slot: mobileBriefSlotOnRequest, NarrativeStatus: "ok"}
	for i := 0; i < 400; i++ {
		large.Sections.Moved = append(large.Sections.Moved, MobileBriefItem{ID: fmt.Sprint("m", i), Title: strings.Repeat("moved work ", 15)})
	}
	data, _ := json.Marshal(large)
	if len(data) <= mobileBriefPackageInlineMax {
		t.Fatalf("fixture is only %d bytes", len(data))
	}
	pkg := mobileBriefPackage{BriefID: large.BriefID, Version: mobileBriefPackageVersion(data), Bytes: data}
	app := &App{}
	app.mobileBriefSvc = packageOnlySource{pkg: pkg}
	bridge := newMobileBriefPrivacyBridge(t, app)
	get := func(query string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		bridge.serveBrief(rec, httptest.NewRequest(http.MethodGet, "/briefs/"+pkg.BriefID+query, nil), pkg.BriefID)
		return rec
	}
	var legacy struct {
		Chunked bool   `json:"chunked"`
		Version string `json:"version"`
		Parts   int    `json:"parts"`
	}
	if err := json.Unmarshal(packageRouteData(t, get("")), &legacy); err != nil || !legacy.Chunked || legacy.Version != pkg.Version || legacy.Parts < 2 {
		t.Fatalf("a large brief was not chunked: %+v %v", legacy, err)
	}
	var head struct {
		BriefID string `json:"briefId"`
		Version string `json:"version"`
		Parts   int    `json:"parts"`
		Data    string `json:"data"`
	}
	if err := json.Unmarshal(packageRouteData(t, get("?package=1")), &head); err != nil || head.Parts != legacy.Parts || head.BriefID != pkg.BriefID {
		t.Fatalf("package head: %+v %v", head, err)
	}
	joined, _ := base64.RawURLEncoding.DecodeString(head.Data)
	for part := 1; part < head.Parts; part++ {
		var piece struct {
			Data string `json:"data"`
		}
		if err := json.Unmarshal(packageRouteData(t, get(fmt.Sprintf("?version=%s&part=%d", head.Version, part))), &piece); err != nil {
			t.Fatal(err)
		}
		decoded, err := base64.RawURLEncoding.DecodeString(piece.Data)
		if err != nil {
			t.Fatal(err)
		}
		joined = append(joined, decoded...)
	}
	if mobileBriefPackageVersion(joined) != head.Version || !bytes.Equal(joined, data) {
		t.Fatal("the joined parts are not the package")
	}
	for _, part := range mobileBriefPackageParts(data) {
		if len(part) > 48*1024 {
			t.Fatalf("a part is %d bytes of base64", len(part))
		}
	}
	if rec := get("?version=" + strings.Repeat("0", 64) + "&part=1"); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "version_changed") {
		t.Fatalf("a stale part answered %d %s", rec.Code, rec.Body.String())
	}
	if rec := get(fmt.Sprintf("?version=%s&part=%d", head.Version, head.Parts)); rec.Code != http.StatusBadRequest {
		t.Fatalf("a part past the end answered %d", rec.Code)
	}
}

func TestMobileBriefOutboxSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 28, 8, 41, 0, 0, time.UTC)
	outbox := loadMobileBriefOutbox(dir)
	newer := mobileBriefOutboxEntry{DeviceID: "phone", BriefID: "2026-10-28-on_request-084100", AsOf: "2026-10-28T08:41:00Z", Version: "v2", Notify: true, QueuedAt: mobileTimestamp(now)}
	outbox.record(newer)
	outbox.record(mobileBriefOutboxEntry{DeviceID: "phone", BriefID: "2026-10-28-morning", AsOf: "2026-10-28T06:50:00Z", Version: "v1", Notify: true, QueuedAt: mobileTimestamp(now)})

	restarted := loadMobileBriefOutbox(dir)
	due := restarted.due(now)
	if len(due) != 1 || due[0].BriefID != newer.BriefID || !due[0].Notify {
		t.Fatalf("after a restart the outbox holds %+v", due)
	}
	restarted.handed("phone", newer.BriefID, now)
	if due := restarted.due(now.Add(30 * time.Minute)); len(due) != 0 {
		t.Fatalf("a brief the relay took was sent again at once: %+v", due)
	}
	due = restarted.due(now.Add(2 * time.Hour))
	if len(due) != 1 || due[0].Notify {
		t.Fatalf("an unconfirmed brief must be sent once more without a push: %+v", due)
	}
	restarted.handed("phone", newer.BriefID, now.Add(2*time.Hour))
	if due := restarted.due(now.Add(5 * time.Hour)); len(due) != 0 {
		t.Fatalf("a brief was sent more than twice: %+v", due)
	}
	if restarted.acknowledge("phone", "2026-10-27-evening", "2026-10-27T18:00:00Z") {
		t.Fatal("an older brief's confirmation cleared a newer one")
	}
	if !restarted.acknowledge("phone", newer.BriefID, newer.AsOf) {
		t.Fatal("the confirmation did not clear the entry")
	}
	if _, ok := loadMobileBriefOutbox(dir).entry("phone"); ok {
		t.Fatal("the cleared entry came back after a restart")
	}
	expired := loadMobileBriefOutbox(dir)
	expired.record(newer)
	if due := expired.due(now.Add(8 * 24 * time.Hour)); len(due) != 0 {
		t.Fatalf("a week-old entry was still due: %+v", due)
	}
}

func TestMobileBriefLatestPointerNeverMovesBack(t *testing.T) {
	store := newMobileBriefStore(t.TempDir())
	now := time.Date(2026, 10, 28, 9, 0, 0, 0, time.UTC)
	asked := MobileBriefV1{Version: 1, BriefID: "2026-10-28-on_request-084100", Slot: mobileBriefSlotOnRequest, LocalDate: "2026-10-28", AsOf: "2026-10-28T08:41:00Z", NarrativeStatus: "ok", Narrative: []MobileBriefPara{{Text: "Asked."}}}
	morning := MobileBriefV1{Version: 1, BriefID: "2026-10-28-morning", Slot: mobileBriefSlotMorning, LocalDate: "2026-10-28", AsOf: "2026-10-28T06:50:00Z", NarrativeStatus: "ok", Narrative: []MobileBriefPara{{Text: "Morning."}}}
	if err := store.save(asked, now); err != nil {
		t.Fatal(err)
	}
	// The morning brief's audio lands after the brief that was asked for.
	morning.Audio = &MobileBriefAudio{Status: mobileBriefAudioStatusOK}
	if err := store.save(morning, now); err != nil {
		t.Fatal(err)
	}
	if latest, ok := store.latest(); !ok || latest.BriefID != asked.BriefID {
		t.Fatalf("an older brief became latest: %s", latest.BriefID)
	}
}

func TestMobileBriefAudioRecordsParagraphStarts(t *testing.T) {
	speaker := mobileBriefSpeaker{
		voiceReady: func(string) bool { return true },
		synthesize: func(_ context.Context, _ string, text string) ([]byte, error) {
			// 0.01 s of audio per character, at 16 kHz.
			return encodeWAV(make([]float32, 160*len([]rune(text))), 16000), nil
		},
	}
	brief := MobileBriefV1{
		Version: 1, BriefID: "2026-10-28-on_request-084100", Slot: mobileBriefSlotOnRequest, AsOf: "2026-10-28T08:41:00Z", NarrativeStatus: "ok",
		Narrative: []MobileBriefPara{
			{Text: "Good morning. Three meetings today, and Priya is waiting on you.", Refs: []string{"e1"}},
			{Text: "The launch review moved to Thursday afternoon.", Refs: []string{"e2"}},
			{Text: "Want me to draft the reply to Priya?", Refs: []string{"e1"}},
		},
		Evidence: []MobileBriefRef{{Ref: "e1", SourceKind: "history", SourceID: "h1"}, {Ref: "e2", SourceKind: "history", SourceID: "h2"}},
	}
	file, ok, reason := renderMobileBriefAudio(context.Background(), speaker, t.TempDir(), brief)
	if !ok {
		t.Fatalf("no audio: %s", reason)
	}
	starts := file.Audio.Paragraphs
	if len(starts) != 3 {
		t.Fatalf("starts: %+v", starts)
	}
	text, _ := mobileBriefSpeech(brief, "en")
	segments := mobileBriefSpokenSegments(text)
	expected := 0.0
	for i, start := range starts {
		if start.Source != mobileBriefAudioSourceNarrative || start.Index != i || math.Abs(start.StartSeconds-expected) > 0.001 {
			t.Fatalf("paragraph %d starts at %+v, expected %.3f", i, start, expected)
		}
		expected += float64(len([]rune(segments[i])))*0.01 + mobileBriefParagraphGapSeconds
	}
	total := expected - mobileBriefParagraphGapSeconds
	if math.Abs(file.Audio.DurationSeconds-total) > 0.001 {
		t.Fatalf("duration %.3f, expected %.3f", file.Audio.DurationSeconds, total)
	}
}
