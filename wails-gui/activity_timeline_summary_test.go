package main

import (
	"fmt"
	"strings"
	"testing"

	"wails-gui/internal/platform/activitymon"
)

func timelineSummaryTestGroup() ActivityTimelineGroup {
	return ActivityTimelineGroup{
		ID:      "tg_test",
		AppKey:  "app:com.tencent.xinWeChat",
		AppName: "WeChat",
		Events: []ActivityTimelineEvent{
			{
				ActivityEvent: activitymon.ActivityEvent{Kind: activitymon.EventAppSwitch, AppName: "WeChat"},
				EventID:       "ae_1",
			},
			{
				ActivityEvent: activitymon.ActivityEvent{Kind: activitymon.EventClick, ClickRole: "AXScrollArea"},
				EventID:       "ae_2",
			},
			{
				ActivityEvent: activitymon.ActivityEvent{Kind: activitymon.EventClick, ClickRole: "AXScrollArea"},
				EventID:       "ae_3",
			},
		},
	}
}

func TestSanitizeTimelineGroupSummary_DropsAppSwitchOnlyItems(t *testing.T) {
	group := timelineSummaryTestGroup()
	resp := timelineGroupSummaryResponse{
		Title: "阅读「大势已定，和统当先」",
		Timeline: []ActivityTimelineSemanticItem{
			{EventIDs: []string{"ae_1"}, Action: "Switched to WeChat app"},
			{EventIDs: []string{"ae_2", "ae_3"}, Action: "阅读环球时报文章", Target: "大势已定，和统当先"},
		},
	}
	summary := sanitizeTimelineGroupSummary(group, resp)
	if len(summary.Timeline) != 1 {
		t.Fatalf("timeline len = %d, want 1 (app-switch item dropped): %+v", len(summary.Timeline), summary.Timeline)
	}
	if summary.Timeline[0].Target != "大势已定，和统当先" {
		t.Fatalf("kept item = %+v, want the article-reading step", summary.Timeline[0])
	}
}

func TestSanitizeTimelineGroupSummary_KeepsSwitchOnlyItemWhenNothingElseSurvives(t *testing.T) {
	group := timelineSummaryTestGroup()
	resp := timelineGroupSummaryResponse{
		Title: "Open WeChat",
		Timeline: []ActivityTimelineSemanticItem{
			{EventIDs: []string{"ae_1"}, Action: "Opened WeChat"},
		},
	}
	summary := sanitizeTimelineGroupSummary(group, resp)
	if len(summary.Timeline) != 1 {
		t.Fatalf("timeline len = %d, want 1 (sole switch-only item kept): %+v", len(summary.Timeline), summary.Timeline)
	}
}

func TestBuildTimelineGroupSummaryPrompt_ScreenContentSection(t *testing.T) {
	group := timelineSummaryTestGroup()
	screen := "- [15:55:25] title=\"大势已定，和统当先\" text=\"6月10日，台湾《中国时报》先后刊发专栏文章\"\n"
	language := responseLanguageInstruction("English")

	prompt := buildTimelineGroupSummaryPrompt(group, screen, language)
	if !strings.HasPrefix(prompt, language) {
		t.Fatalf("prompt must lead with the UI-language instruction:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Screen content, chronological:") {
		t.Fatalf("prompt missing screen content section:\n%s", prompt)
	}
	if !strings.Contains(prompt, "大势已定，和统当先") {
		t.Fatalf("prompt missing screen content payload")
	}
	if !strings.Contains(prompt, "VERBATIM") {
		t.Fatalf("prompt missing verbatim-quoting rule")
	}

	without := buildTimelineGroupSummaryPrompt(group, "", "")
	if strings.Contains(without, "Screen content, chronological:") {
		t.Fatalf("empty screen content must omit the section")
	}
}

func TestRenderTimelineScreenContent_HeadOfBodyTruncation(t *testing.T) {
	longBody := strings.Repeat("内容", timelineSummaryScreenBodyMaxRunes) // 2x the cap in runes
	observations := []Observation{
		{
			StartMs: 1_700_000_000_000,
			Title:   "大势已定，和统当先",
			URL:     "https://mp.weixin.qq.com/s/abc",
			Body:    longBody,
		},
		{StartMs: 1_700_000_060_000, Body: "second screen"},
	}
	out := renderTimelineScreenContent(observations)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[0], `title="大势已定，和统当先"`) {
		t.Fatalf("line missing title: %s", lines[0])
	}
	if !strings.Contains(lines[0], `url="https://mp.weixin.qq.com/s/abc"`) {
		t.Fatalf("line missing url: %s", lines[0])
	}
	if !strings.Contains(lines[0], "…") {
		t.Fatalf("long body should be head-truncated with ellipsis: %s", lines[0])
	}
	bodyRunes := []rune(longBody)
	head := string(bodyRunes[:timelineSummaryScreenBodyMaxRunes])
	if !strings.Contains(lines[0], head) {
		t.Fatalf("truncation must keep the body head")
	}
	if strings.Contains(lines[0], string(bodyRunes[:timelineSummaryScreenBodyMaxRunes+2])) {
		t.Fatalf("body exceeded the %d-rune cap", timelineSummaryScreenBodyMaxRunes)
	}
}

func TestRenderTimelineSummaryEvent_TypedTextAndIMERawKeys(t *testing.T) {
	typed := ActivityTimelineEvent{
		ActivityEvent: activitymon.ActivityEvent{
			Kind:         activitymon.EventKeyboard,
			AppName:      "WeChat",
			KeyChars:     []string{"y", "e", "Space"},
			TypedText:    "也不输上去了",
			IMEComposing: true,
		},
		EventID: "ae_10",
	}
	line := renderTimelineSummaryEvent(typed)
	if !strings.Contains(line, `typed="也不输上去了"`) {
		t.Fatalf("line missing typed text: %s", line)
	}
	if !strings.Contains(line, "imeRawKeys=") || strings.Contains(line, "chars=") {
		t.Fatalf("IME burst must label raw keys imeRawKeys, not chars: %s", line)
	}

	plain := ActivityTimelineEvent{
		ActivityEvent: activitymon.ActivityEvent{
			Kind:     activitymon.EventKeyboard,
			KeyChars: []string{"l", "s"},
		},
		EventID: "ae_11",
	}
	line = renderTimelineSummaryEvent(plain)
	if !strings.Contains(line, `chars="ls"`) || strings.Contains(line, "typed=") {
		t.Fatalf("plain burst must keep chars= without typed=: %s", line)
	}

	action, target := timelineEventActionAndTarget(typed.ActivityEvent)
	if action != "typed" || target != "也不输上去了" {
		t.Fatalf("action/target = %q/%q, want typed/composed text", action, target)
	}
	imeNoTyped := typed
	imeNoTyped.TypedText = ""
	action, target = timelineEventActionAndTarget(imeNoTyped.ActivityEvent)
	if action != "typed via IME" || target != "" {
		t.Fatalf("IME burst without typed text = %q/%q, want 'typed via IME' with empty target", action, target)
	}
}

func TestSampleObservationsEvenly(t *testing.T) {
	observations := make([]Observation, 30)
	for i := range observations {
		observations[i] = Observation{Index: i}
	}
	sampled := sampleObservationsEvenly(observations, timelineSummaryScreenMaxEntries)
	if len(sampled) != timelineSummaryScreenMaxEntries {
		t.Fatalf("sampled len = %d, want %d", len(sampled), timelineSummaryScreenMaxEntries)
	}
	if sampled[0].Index != 0 || sampled[len(sampled)-1].Index != 29 {
		t.Fatalf("sampling must keep first and last, got first=%d last=%d", sampled[0].Index, sampled[len(sampled)-1].Index)
	}
	short := sampleObservationsEvenly(observations[:5], timelineSummaryScreenMaxEntries)
	if len(short) != 5 {
		t.Fatalf("short input must pass through, got %d", len(short))
	}
}

func TestTimelineSummaryPreservesPathLinesWithinCut(t *testing.T) {
	body := strings.Repeat("generic terminal status\n", 30) + "~/.sample-agent/activity 60.3 GB\n"
	got := compactTimelineEvidenceText(body, 280)
	if !strings.HasPrefix(got, "~/.sample-agent/activity 60.3 GB") {
		t.Fatalf("path line lost in summary cut: %q", got)
	}
	if len([]rune(got)) > 281 { // compactText appends the truncation marker.
		t.Fatalf("summary body over budget: %d", len([]rune(got)))
	}
}

func TestTimelineSummaryKeepsEntityFrameBeforeEvenSampling(t *testing.T) {
	observations := make([]Observation, 30)
	for i := range observations {
		observations[i] = Observation{Index: i, StartMs: int64(i), Body: "plain terminal output"}
	}
	observations[7].Body = "~/.sample-agent/activity 60 GB"
	picked := sampleEvidenceObservations(observations, 4)
	if len(picked) != 4 {
		t.Fatalf("len = %d, want 4", len(picked))
	}
	for _, observation := range picked {
		if observation.Index == 7 {
			return
		}
	}
	t.Fatalf("evidence-bearing AX frame was dropped: %+v", picked)
}

func TestTimelineSummarySamplingKeepsFinalObservation(t *testing.T) {
	for _, tc := range []struct {
		name          string
		evidenceCount int
	}{
		{name: "every observation has identifiers", evidenceCount: 100},
		{name: "only the first six have identifiers", evidenceCount: 6},
		{name: "no identifier observations", evidenceCount: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			observations := make([]Observation, 100)
			for i := range observations {
				body := fmt.Sprintf("plain terminal output %d", i)
				if i < tc.evidenceCount {
					body += " ~/.sample-agent/activity 60 GB"
				}
				observations[i] = Observation{Index: i, StartMs: int64(i), Body: body}
			}
			picked := sampleEvidenceObservations(observations, timelineSummaryScreenMaxEntries)
			if len(picked) != timelineSummaryScreenMaxEntries {
				t.Fatalf("sample length = %d, want %d", len(picked), timelineSummaryScreenMaxEntries)
			}
			if picked[0].Index != 0 || picked[len(picked)-1].Index != 99 {
				t.Fatalf("lost run endpoints: first=%d last=%d", picked[0].Index, picked[len(picked)-1].Index)
			}
			lateContext := false
			for i, observation := range picked {
				if i > 0 && picked[i-1].Index >= observation.Index {
					t.Fatalf("sample is duplicated or out of order: %+v", picked)
				}
				lateContext = lateContext || (observation.Index >= 70 && observation.Index < 99)
			}
			if !lateContext {
				t.Fatalf("sample omitted later context before the final observation: %+v", picked)
			}
		})
	}
}

func TestTimelineSummarySamplingKeepsEndpointsWithSmallBudget(t *testing.T) {
	observations := []Observation{
		{Index: 0}, {Index: 1, Body: "~/.sample-agent/activity 60 GB"}, {Index: 2},
	}
	for _, limit := range []int{1, 2} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			picked := sampleEvidenceObservations(observations, limit)
			if len(picked) != limit || picked[len(picked)-1].Index != 2 {
				t.Fatalf("small sample lost final observation: %+v", picked)
			}
			if limit == 2 && picked[0].Index != 0 {
				t.Fatalf("two-slot sample lost first observation: %+v", picked)
			}
		})
	}
}

func TestTimelineSummarySamplesBeforeAXAugmentation(t *testing.T) {
	observations := make([]Observation, 100)
	snapshots := make([]ActivitySnapshotRow, 100)
	for i := range observations {
		observations[i] = Observation{Index: i, Body: "concise parsed body", SnapshotIDs: []int64{int64(i + 1)}}
		snapshots[i] = ActivitySnapshotRow{ID: int64(i + 1), AXText: "Edit unrelated prose"}
	}
	const pathLine = "~/.sample-agent/activity 60 GB"
	snapshots[7].AXText = "Edit unrelated prose\n" + pathLine + "\n" + pathLine
	// Repeated AX lines from a collapsed observation should only be appended
	// once, and the original parsed observations should remain untouched.
	snapshots = append(snapshots, ActivitySnapshotRow{ID: 101, AXText: pathLine})
	observations[7].SnapshotIDs = append(observations[7].SnapshotIDs, 101)
	picked := sampleTimelineScreenObservations(observations, snapshots, 4)
	if len(picked) != 4 || picked[len(picked)-1].Index != 99 {
		t.Fatalf("sampling lost final state: %+v", picked)
	}
	found := false
	for _, observation := range picked {
		if strings.Contains(observation.Body, "Edit unrelated prose") {
			t.Fatalf("AX prose was appended as identifier evidence: %q", observation.Body)
		}
		if observation.Index == 7 {
			found = true
			if strings.Count(observation.Body, pathLine) != 1 {
				t.Fatalf("AX identifier was omitted or duplicated: %q", observation.Body)
			}
		}
	}
	if !found {
		t.Fatal("sampling missed identifier present only in AX")
	}
	for _, observation := range observations {
		if observation.Body != "concise parsed body" {
			t.Fatalf("sampling mutated the original observation: %+v", observation)
		}
	}
}
