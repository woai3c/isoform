package appparser

import (
	"strings"
	"testing"
)

func TestCalendarParser_SkipsPopoverDateTimeRow(t *testing.T) {
	staticText := func(value string) *AXNode {
		return &AXNode{Role: "AXStaticText", Value: value}
	}
	chip := func(desc string) *AXNode {
		return &AXNode{Role: "AXButton", Description: desc}
	}

	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			chip("isoform 早会, 11:00, 工作"),
			chip("Maya 陶艺展, 09:00, 兴趣"),
			// This is the noisy node produced by the event-detail popover's
			// date/time row. It must NOT be turned into a third event.
			staticText("2027年9月15日  11:00 – 12:00"),
		},
	}

	parser := &calendarParser{}
	content := parser.ParseTree(tree, "com.apple.iCal", "Calendar", "")
	if content == nil {
		t.Fatalf("expected non-nil ParsedContent")
	}
	if got, want := strings.Count(content.Body, "\n")+1, 2; got != want {
		t.Fatalf("expected %d event lines, got %d:\n%s", want, got, content.Body)
	}
	if strings.Contains(content.Body, "2027年9月15日") {
		t.Errorf("popover date row leaked into output:\n%s", content.Body)
	}
}

func TestGoogleCalendarParser_AllDayHolidaySkipsTimeMetadata(t *testing.T) {
	node := func(role, value string) *AXNode {
		return &AXNode{Role: role, Value: value}
	}

	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role: "AXWebArea",
				Children: []*AXNode{
					node("AXStaticText", "9月 5日 (星期日)有 1 个全天活动"),
					node("AXStaticText", "全天，Halcyon Founders Day，日历：Halcyon 节假日，2027年9月5日"),
					node("AXStaticText", "9月 6日 (星期一)没有全天活动"),
					node("AXStaticText", "9月 5日 (星期日) 上午11:00 至 下午12:00 时区·不重复"),
					node("AXStaticText", "上午11:00"),
					node("AXStaticText", "下午12:00"),
				},
			},
		},
	}

	content := (&googleCalendarParser{}).ParseTree(tree, "com.google.Chrome", "Google 日历", "https://calendar.google.com/calendar/u/0/r")
	if content == nil {
		t.Fatalf("expected non-nil ParsedContent")
	}
	if !strings.Contains(content.Body, "Halcyon Founders Day") {
		t.Fatalf("expected Halcyon Founders Day in parsed body:\n%s", content.Body)
	}
	for _, unexpected := range []string{"没有全天活动", "有 1 个全天活动", "时区", "不重复", "上午11:00", "下午12:00"} {
		if strings.Contains(content.Body, unexpected) {
			t.Fatalf("metadata/status row %q leaked into body:\n%s", unexpected, content.Body)
		}
	}
}

func TestGoogleCalendarParser_EventEditorCombinesTitleAndTime(t *testing.T) {
	node := func(role, value string) *AXNode {
		return &AXNode{Role: role, Value: value}
	}

	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role: "AXWebArea",
				Children: []*AXNode{
					node("AXButton", "创建"),
					node("AXButton", "关闭"),
					node("AXTextField", "isoform早会"),
					node("AXStaticText", "活动"),
					node("AXStaticText", "任务"),
					node("AXStaticText", "预约安排 新"),
					node("AXStaticText", "9月 15日 (星期三) 上午11:00 至 下午12:00 时区·不重复"),
					node("AXStaticText", "9月 15日 (星期三)"),
					node("AXStaticText", "上午11:00"),
					node("AXStaticText", "–"),
					node("AXStaticText", "下午12:00"),
					node("AXStaticText", "时区"),
					node("AXStaticText", "不重复"),
					node("AXButton", "保存"),
				},
			},
		},
	}

	content := (&googleCalendarParser{}).ParseTree(tree, "com.google.Chrome", "Google 日历", "https://calendar.google.com/calendar/u/0/r")
	if content == nil {
		t.Fatalf("expected non-nil ParsedContent")
	}
	if !strings.Contains(content.Body, "isoform早会") {
		t.Fatalf("expected editor event title in parsed body:\n%s", content.Body)
	}
	if !strings.Contains(content.Body, "上午11:00 – 下午12:00") {
		t.Fatalf("expected editor time range in parsed body:\n%s", content.Body)
	}
	for _, unexpected := range []string{"创建", "关闭", "活动", "任务", "预约安排", "时区", "不重复", "保存"} {
		if strings.Contains(content.Body, unexpected) {
			t.Fatalf("editor UI label %q leaked into body:\n%s", unexpected, content.Body)
		}
	}
}

func TestGoogleCalendarParser_EmptyEditorTitleDoesNotBecomeEvent(t *testing.T) {
	node := func(role, value string) *AXNode {
		return &AXNode{Role: role, Value: value}
	}

	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role: "AXWebArea",
				Children: []*AXNode{
					node("AXButton", "创建"),
					node("AXTextField", "添加标题"),
					node("AXStaticText", "9月 15日 (星期三) 下午5:30 至 下午6:30 时区·不重复"),
					node("AXStaticText", "下午5:30"),
					node("AXStaticText", "–"),
					node("AXStaticText", "下午6:30"),
					node("AXButton", "保存"),
				},
			},
		},
	}

	content := (&googleCalendarParser{}).ParseTree(tree, "com.google.Chrome", "Google 日历", "https://calendar.google.com/calendar/u/0/r")
	if content == nil {
		t.Fatalf("expected Google Calendar parser to claim the page")
	}
	if strings.TrimSpace(content.Body) != "" {
		t.Fatalf("empty editor title should not produce an event:\n%s", content.Body)
	}
	if content.Metadata["app"] != "google_calendar" {
		t.Fatalf("expected google_calendar metadata, got %+v", content.Metadata)
	}
}

func TestCalendarParseDescription_RecoversEndTime(t *testing.T) {
	cases := []struct {
		name      string
		desc      string
		wantTitle string
		wantTime  string
		wantLoc   string
	}{
		{
			name:      "chinese leaked 结束于 fragment",
			desc:      "Maya 陶艺展. 开始于2027年9月3日, 09:00, ，结束于10:00。",
			wantTitle: "Maya 陶艺展. 开始于2027年9月3日",
			wantTime:  "09:00 – 10:00",
			wantLoc:   "",
		},
		{
			name:      "english leaked ends at fragment",
			desc:      "Standup, starts 9:00 AM, ends at 9:30 AM",
			wantTitle: "Standup",
			wantTime:  "starts 9:00 AM – 9:30 AM",
			wantLoc:   "",
		},
		{
			name:      "real location preserved alongside end time",
			desc:      "Lunch, 12:00, 结束于13:00, 公司食堂",
			wantTitle: "Lunch",
			wantTime:  "12:00 – 13:00",
			wantLoc:   "公司食堂",
		},
		{
			name:      "already a range — do not double-append",
			desc:      "Meeting, 10:00 – 11:00, ends at 11:00",
			wantTitle: "Meeting",
			wantTime:  "10:00 – 11:00",
			wantLoc:   "",
		},
		{
			name:      "regular location with no end-time leak — untouched",
			desc:      "Standup, 10 AM, Work",
			wantTitle: "Standup",
			wantTime:  "10 AM",
			wantLoc:   "Work",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := calendarParseDescription(tc.desc)
			if ev.Title != tc.wantTitle {
				t.Errorf("Title=%q want %q", ev.Title, tc.wantTitle)
			}
			if strings.TrimSpace(ev.Time) != tc.wantTime {
				t.Errorf("Time=%q want %q", ev.Time, tc.wantTime)
			}
			if ev.Location != tc.wantLoc {
				t.Errorf("Location=%q want %q", ev.Location, tc.wantLoc)
			}
		})
	}
}

func TestCalendarParseDescription_TimeFirstAllDayTitle(t *testing.T) {
	cases := []struct {
		name      string
		desc      string
		wantTitle string
		wantTime  string
	}{
		{
			name:      "english all-day first",
			desc:      "All day, Halcyon Founders Day, Calendar: Halcyon Holidays, September 5, 2027",
			wantTitle: "Halcyon Founders Day",
			wantTime:  "All day",
		},
		{
			name:      "chinese all-day first",
			desc:      "全天，Halcyon Founders Day，日历：Halcyon 节假日，2027年9月5日",
			wantTitle: "Halcyon Founders Day",
			wantTime:  "全天",
		},
		{
			name:      "time first meeting",
			desc:      "10am to 10:30am, 1:1 with Priya, No location, September 14, 2027",
			wantTitle: "1:1 with Priya",
			wantTime:  "10am to 10:30am",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := calendarParseDescription(tc.desc)
			if ev.Title != tc.wantTitle {
				t.Errorf("Title=%q want %q", ev.Title, tc.wantTitle)
			}
			if ev.Time != tc.wantTime {
				t.Errorf("Time=%q want %q", ev.Time, tc.wantTime)
			}
		})
	}
}

func TestCalendarRenderBody_NoStrayAtSign(t *testing.T) {
	// Regression shape: every line ended with " @ ，结束于HH:MM。" because the
	// leaked end-time fragment fell through to Location.
	ev := calendarParseDescription("Maya 陶艺展. 开始于2027年9月3日, 09:00, ，结束于10:00。")
	body := calendarRenderBody([]calendarEvent{ev})
	if strings.Contains(body, "@") {
		t.Errorf("body should not contain '@' for an event with no real location:\n%s", body)
	}
	if !strings.Contains(body, "09:00 – 10:00") {
		t.Errorf("expected merged time range in body:\n%s", body)
	}
}

func TestCalendarLabelHasNonTemporalContent(t *testing.T) {
	cases := []struct {
		label string
		want  bool
	}{
		// Pure date+time strings from detail popovers — should be filtered.
		{"2027年9月15日  11:00 – 12:00", false},
		{"11:00 – 12:00, 2027年9月15日", false},
		{"September 15, 2027  11:00 – 12:00", false},
		{"15 September 2027 11:00 – 12:00", false},
		{"2027-09-15 11:00 – 12:00", false},
		{"2027/09/15, 11:00 – 12:00", false},
		{"9月 5日 (星期日) 上午11:00 至 下午12:00 时区·不重复", false},
		{"上午11:00", false},
		{"下午12:00", false},
		// Real event chip descriptions — must be kept.
		{"isoform 早会, 11:00, 工作", true},
		{"Maya 陶艺展, 09:00, 兴趣", true},
		{"Standup, 10 AM, Work", true},
		{"all-day, Anniversary", true},
		{"全天，Halcyon Founders Day，日历：Halcyon 节假日，2027年9月5日", true},
	}
	for _, tc := range cases {
		got := calendarLabelHasNonTemporalContent(tc.label)
		if got != tc.want {
			t.Errorf("calendarLabelHasNonTemporalContent(%q) = %v, want %v", tc.label, got, tc.want)
		}
	}
}

func TestCalendarLabelIsNonEventStatus(t *testing.T) {
	cases := []string{
		"9月 5日 (星期日)有 1 个全天活动",
		"9月 6日 (星期一)没有全天活动",
		"1 all day event, Sunday, September 5",
		"No all day events, Monday, September 6",
	}
	for _, label := range cases {
		if !calendarLabelIsNonEventStatus(label) {
			t.Errorf("calendarLabelIsNonEventStatus(%q) = false, want true", label)
		}
	}
	if calendarLabelIsNonEventStatus("全天，Halcyon Founders Day，日历：Halcyon 节假日，2027年9月5日") {
		t.Errorf("real all-day event should not be classified as a status row")
	}
}
