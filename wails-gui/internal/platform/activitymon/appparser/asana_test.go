package appparser

import (
	"strings"
	"testing"
)

// TestAsanaParser_HomeDashboardWidget covers the realistic shape
// observed against Asana Desktop's home dashboard: each "My Tasks"
// widget packs the entire visible task list into a single AXGroup's
// description string, with rows separated by repeating verb phrases.
// The widget AXGroup is tagged with the MyTasksWidget* class prefix.
func TestAsanaParser_HomeDashboardWidget(t *testing.T) {
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "● 首頁 - Asana",
		Children: []*AXNode{
			{Role: "AXHeading", Title: "我的任務"},
			{
				Role:         "AXGroup",
				DOMClassList: []string{"Scrollable", "MyTasksWidgetTaskList"},
				Description:  "標記為完成 開啟任務對話框「任務 1」 Pengsi的第一個專案 5 月 20 日 – 22 日 標記為完成 開啟任務對話框「任務 2」 Pengsi的第一個專案 今天 – 5 月 25 日",
			},
			// Empty-state placeholder widget — must NOT contribute rows.
			{Role: "AXHeading", Title: "我已指派的任務"},
			{
				Role:  "AXStaticText",
				Value: "「我已指派的任務」小工具可讓您檢視您已指派的任務清單，其中包含本週截止、即將截止、逾期或已完成的任務。",
			},
		},
	}

	p := &asanaParser{}
	got := p.ParseTree(tree, "com.electron.asana", "● 首頁 - Asana", "")
	if got == nil {
		t.Fatal("expected ParsedContent, got nil")
	}
	if got.ContentType != "task_list" {
		t.Errorf("contentType: got %q want task_list", got.ContentType)
	}
	if got.Title != "首頁" {
		t.Errorf("title: got %q want 首頁", got.Title)
	}
	for _, want := range []string{"任務 1", "任務 2", "Pengsi的第一個專案", "5 月 20 日 – 22 日", "今天 – 5 月 25 日"} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("body missing %q: %q", want, got.Body)
		}
	}
	// The empty-state widget's help text must NOT leak into the body.
	if strings.Contains(got.Body, "小工具可讓您檢視") {
		t.Errorf("body should not contain widget help text: %q", got.Body)
	}
}

// TestAsanaParser_NestedWidgetDeduplication guards the seen-key dedup
// in asanaExtractWidgetTasks: Asana nests MyTasksWidgetContent-* inside
// MyTasksWidgetTaskList, so the same row appears in both descriptions.
func TestAsanaParser_NestedWidgetDeduplication(t *testing.T) {
	innerDesc := "標記為完成 開啟任務對話框「Buy milk」 Personal 今天"
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role:         "AXGroup",
				DOMClassList: []string{"MyTasksWidgetTaskList"},
				Description:  innerDesc,
				Children: []*AXNode{
					{
						Role:         "AXGroup",
						DOMClassList: []string{"MyTasksWidgetContent-upcomingTasks"},
						Description:  innerDesc,
					},
				},
			},
		},
	}
	p := &asanaParser{}
	got := p.ParseTree(tree, "com.electron.asana", "", "")
	if got == nil {
		t.Fatal("expected ParsedContent, got nil")
	}
	if strings.Count(got.Body, "Buy milk") != 1 {
		t.Errorf("expected exactly one Buy milk line, got: %q", got.Body)
	}
}

// TestAsanaParser_EnglishLocale exercises the ASCII-quote / curly-quote
// alternation in the regex — operators running Asana in en-US see the
// hint formatted with straight or curly double quotes rather than the
// Chinese full-width brackets.
func TestAsanaParser_EnglishLocale(t *testing.T) {
	cases := []struct {
		name string
		desc string
	}{
		{
			name: "straight quotes",
			desc: `Mark complete Open task dialog "Buy milk" Personal Today Mark complete Open task dialog "Call mom" Personal Tomorrow`,
		},
		{
			name: "curly quotes",
			desc: "Mark complete Open task dialog “Buy milk” Personal Today Mark complete Open task dialog “Call mom” Personal Tomorrow",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tree := &AXNode{
				Role: "AXWindow",
				Children: []*AXNode{
					{
						Role:         "AXGroup",
						DOMClassList: []string{"MyTasksWidgetTaskList"},
						Description:  tc.desc,
					},
				},
			}
			p := &asanaParser{}
			got := p.ParseTree(tree, "com.electron.asana", "", "")
			if got == nil {
				t.Fatal("expected ParsedContent, got nil")
			}
			for _, want := range []string{"Buy milk", "Call mom"} {
				if !strings.Contains(got.Body, want) {
					t.Errorf("body missing %q: %q", want, got.Body)
				}
			}
		})
	}
}

// TestAsanaParser_PerRowLayout covers the A/B layout Asana ships where
// each home-dashboard task is its own MyTasksWidgetTaskRow-task AXGroup
// instead of being packed into one ARIA hint. Task names are NOT in
// the AX tree in this layout (rendered visually only); project + date
// come from child AXLink / AXButton nodes. The parser must surface
// what it can AND flag axCoverage=metadata_only so OCR fusion runs.
func TestAsanaParser_PerRowLayout(t *testing.T) {
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "● 首頁 - Asana",
		Children: []*AXNode{
			{
				Role:         "AXGroup",
				DOMClassList: []string{"MyTasksWidgetTaskList"},
				Children: []*AXNode{
					{
						Role:         "AXGroup",
						DOMClassList: []string{"TaskRow", "MyTasksWidgetTaskRow-task"},
						Children: []*AXNode{
							{Role: "AXGroup", DOMClassList: []string{"ItemRowTwoColumnStructure-left"}},
							{
								Role:         "AXLink",
								Description:  "Pengsi的第一個專案",
								DOMClassList: []string{"PotTokenizerPill", "PotPillsContainer-potPillLink"},
							},
							{
								Role:         "AXButton",
								Title:        "5 月 20 日 – 22 日",
								DOMClassList: []string{"DueDateContainerButton-dueDateButton"},
							},
						},
					},
					{
						Role:         "AXGroup",
						DOMClassList: []string{"TaskRow", "MyTasksWidgetTaskRow-task"},
						Children: []*AXNode{
							{Role: "AXGroup", DOMClassList: []string{"ItemRowTwoColumnStructure-left"}},
							{
								Role:         "AXLink",
								Description:  "Pengsi的第一個專案",
								DOMClassList: []string{"PotTokenizerPill", "PotPillsContainer-potPillLink"},
							},
							{
								Role:         "AXButton",
								Title:        "今天 – 5 月 25 日",
								DOMClassList: []string{"DueDateContainerButton-dueDateButton"},
							},
						},
					},
				},
			},
		},
	}
	p := &asanaParser{}
	got := p.ParseTree(tree, "com.electron.asana", "● 首頁 - Asana", "")
	if got == nil {
		t.Fatal("expected ParsedContent, got nil")
	}
	if got.ContentType != "task_list" {
		t.Errorf("contentType: got %q want task_list", got.ContentType)
	}
	if got.Metadata["axCoverage"] != AXCoverageMetadataOnly {
		t.Errorf("axCoverage: got %q want %q — names missing so OCR fusion must run",
			got.Metadata["axCoverage"], AXCoverageMetadataOnly)
	}
	if !IsPartialAX(got) {
		t.Error("IsPartialAX should be true so OCR splice triggers")
	}
	for _, want := range []string{"Pengsi的第一個專案", "5 月 20 日 – 22 日", "今天 – 5 月 25 日", "(task name not in AX)"} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("body missing %q: %q", want, got.Body)
		}
	}
}

// TestAsanaParser_TaskListViewMetadataOnly locks down the "My Tasks"
// list-view fallback. Asana renders that page as an AXTable with a
// StandardHeaderStructure AXRow (column labels) plus a 1-pixel
// SpreadsheetRow spacer — the actual task rows live in canvas / WebGL
// and are absent from the AX tree. The parser must NOT pretend the
// chrome is the body; it should emit a task_list stub with
// axCoverage=metadata_only and surface the column hint so consumers
// know "Asana list view, OCR has the rows".
func TestAsanaParser_TaskListViewMetadataOnly(t *testing.T) {
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "● 我的任務 - Asana",
		Children: []*AXNode{
			{
				Role: "AXTable",
				Children: []*AXNode{
					{
						Role:         "AXRow",
						Title:        "名稱 顯示任務名稱的排序選項 截止日期 顯示適用於「截止日期」欄的選項 協作者 顯示任務協作者欄的選項 專案 顯示適用於「專案」欄的選項",
						DOMClassList: []string{"StandardHeaderStructure", "Stack"},
					},
					{
						Role:         "AXRow",
						DOMClassList: []string{"SpreadsheetRow", "SpreadsheetGridDropTarget"},
						Frame:        &AXFrame{H: 1},
					},
				},
			},
		},
	}
	p := &asanaParser{}
	got := p.ParseTree(tree, "com.electron.asana", "● 我的任務 - Asana", "")
	if got == nil {
		t.Fatal("expected ParsedContent, got nil")
	}
	if got.ContentType != "task_list" {
		t.Errorf("contentType: got %q want task_list", got.ContentType)
	}
	if got.Metadata["axCoverage"] != AXCoverageMetadataOnly {
		t.Errorf("axCoverage: got %q want metadata_only", got.Metadata["axCoverage"])
	}
	if !IsPartialAX(got) {
		t.Error("IsPartialAX should be true so OCR fusion runs")
	}
	if !strings.Contains(got.Body, "我的任務") {
		t.Errorf("body should contain title: %q", got.Body)
	}
	if !strings.Contains(got.Body, "名稱") {
		t.Errorf("body should contain column hint: %q", got.Body)
	}
}

// TestAsanaExtractTableColumns_EnglishMultiWordColumns guards against
// the older "every-other-token" heuristic that broke for multi-word
// English column names like "Due date" or "Last modified". The header
// title is "<col1> <hint 1> <col2> <hint 2> ..." with each column
// followed by its localised sort/options hint. Splitting by hint-verb
// prefix correctly returns the first column even when it has spaces.
func TestAsanaExtractTableColumns_EnglishMultiWordColumns(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  string
	}{
		{
			name:  "en_due_date",
			title: "Task name Show options for Task name column Due date Show options for Due date column",
			want:  "Task name",
		},
		{
			name:  "en_sort_ascending",
			title: "Last modified Sort ascending by Last modified Priority Show options for Priority column",
			want:  "Last modified",
		},
		{
			name:  "zh_tw",
			title: "名稱 顯示任務名稱的排序選項 截止日期 顯示適用於「截止日期」欄的選項",
			want:  "名稱",
		},
		{
			name:  "zh_cn",
			title: "名称 显示任务名称的排序选项 截止日期 显示适用于「截止日期」列的选项",
			want:  "名称",
		},
		{
			name:  "no_hint_short_falls_back_to_full_string",
			title: "Task name",
			want:  "Task name",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tree := &AXNode{
				Role: "AXWindow",
				Children: []*AXNode{{
					Role: "AXTable",
					Children: []*AXNode{{
						Role:         "AXRow",
						Title:        tc.title,
						DOMClassList: []string{"StandardHeaderStructure"},
					}},
				}},
			}
			got := asanaExtractTableColumns(tree)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAsanaParser_ProjectViewFallback locks down the second extraction
// path — when no MyTasksWidget* container is present (e.g. inside a
// project list), Asana renders a proper AXTable/AXList with AXRow
// children that the row-counter heuristic picks up.
func TestAsanaParser_ProjectViewFallback(t *testing.T) {
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "Pengsi的第一個專案 — Asana",
		Children: []*AXNode{
			{
				Role: "AXTable",
				Children: []*AXNode{
					{Role: "AXRow", Children: []*AXNode{
						{Role: "AXStaticText", Value: "Design login flow"},
						{Role: "AXStaticText", Value: "In progress"},
					}},
					{Role: "AXRow", Children: []*AXNode{
						{Role: "AXStaticText", Value: "Write docs"},
						{Role: "AXStaticText", Value: "Not started"},
					}},
					{Role: "AXRow", Children: []*AXNode{
						{Role: "AXStaticText", Value: "Ship beta"},
						{Role: "AXStaticText", Value: "Done"},
					}},
				},
			},
		},
	}
	p := &asanaParser{}
	got := p.ParseTree(tree, "com.electron.asana", "Pengsi的第一個專案 — Asana", "")
	if got == nil {
		t.Fatal("expected ParsedContent, got nil")
	}
	if got.ContentType != "task_list" {
		t.Errorf("contentType: got %q want task_list", got.ContentType)
	}
	if got.Title != "Pengsi的第一個專案" {
		t.Errorf("title: got %q", got.Title)
	}
	for _, want := range []string{"Design login flow", "Write docs", "Ship beta"} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("body missing %q: %q", want, got.Body)
		}
	}
}
