package appparser

import (
	"strings"
	"testing"
)

func cb(value string) *AXNode {
	return &AXNode{Role: "AXCheckBox", Value: value}
}

func staticText(v string) *AXNode {
	return &AXNode{Role: "AXStaticText", Value: v}
}

// TestRemindersParser_SwiftUICell reproduces the macOS 15+ failure mode:
// each reminder is wrapped in AXCell (SwiftUI), not AXRow/AXGroup. The
// old container check (`Role == "AXRow" || Role == "AXGroup"`) skipped
// these rows so Reminders activity never produced a parsed task list.
func TestRemindersParser_SwiftUICell(t *testing.T) {
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "旗标 — 提醒事项",
		Children: []*AXNode{
			{
				Role: "AXScrollArea",
				Children: []*AXNode{
					{
						Role: "AXCell",
						Children: []*AXNode{
							cb("0"),
							staticText("isoform 早会"),
							staticText("提醒  2025/5/13 11:00"),
						},
					},
				},
			},
		},
	}

	parsed := (&remindersParser{}).ParseTree(tree, "com.apple.reminders", "旗标 — 提醒事项", "")
	if parsed == nil {
		t.Fatalf("expected non-nil parsed content for SwiftUI Reminders layout")
	}
	if !strings.Contains(parsed.Body, "isoform 早会") {
		t.Errorf("body should contain reminder title, got: %q", parsed.Body)
	}
	if !strings.Contains(parsed.Body, "2025/5/13 11:00") {
		t.Errorf("body should include the due-time note, got: %q", parsed.Body)
	}
}

// TestRemindersParser_AppKitRowStillWorks pins the legacy AppKit layout
// (AXRow containing the checkbox) so the refactor doesn't regress
// existing macOS versions.
func TestRemindersParser_AppKitRowStillWorks(t *testing.T) {
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role: "AXScrollArea",
				Children: []*AXNode{
					{
						Role: "AXRow",
						Children: []*AXNode{
							cb("1"), // completed
							staticText("buy milk"),
						},
					},
					{
						Role: "AXRow",
						Children: []*AXNode{
							cb("0"),
							staticText("call dentist"),
						},
					},
				},
			},
		},
	}
	parsed := (&remindersParser{}).ParseTree(tree, "com.apple.reminders", "Today — Reminders", "")
	if parsed == nil {
		t.Fatalf("expected non-nil parsed content")
	}
	if !strings.Contains(parsed.Body, "[x] buy milk") {
		t.Errorf("completed task should render with [x], got: %q", parsed.Body)
	}
	if !strings.Contains(parsed.Body, "[ ] call dentist") {
		t.Errorf("open task should render with [ ], got: %q", parsed.Body)
	}
}

// TestRemindersParser_NestedSwiftUIContainers covers the case where the
// checkbox is buried two or three levels deep under nested AXLayoutItem /
// AXGroup wrappers — SwiftUI lists routinely nest containers and the
// container resolver must walk past them to find a usable row root.
func TestRemindersParser_NestedSwiftUIContainers(t *testing.T) {
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role: "AXLayoutItem",
				Children: []*AXNode{
					{
						Role: "AXGroup",
						Children: []*AXNode{
							{
								Role: "AXButton",
								Children: []*AXNode{
									cb("0"),
								},
							},
							staticText("submit expense report"),
						},
					},
				},
			},
		},
	}
	parsed := (&remindersParser{}).ParseTree(tree, "com.apple.reminders", "Work — Reminders", "")
	if parsed == nil || !strings.Contains(parsed.Body, "submit expense report") {
		t.Fatalf("expected to find nested reminder, got: %+v", parsed)
	}
}

// TestRemindersParser_NoCheckboxReturnsNil ensures we don't fabricate a
// task list from a Reminders window that's just showing the empty-state
// or sidebar (no AXCheckBox anywhere).
func TestRemindersParser_NoCheckboxReturnsNil(t *testing.T) {
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{Role: "AXOutline", Children: []*AXNode{
				{Role: "AXRow", Children: []*AXNode{staticText("今天")}},
				{Role: "AXRow", Children: []*AXNode{staticText("旗标")}},
			}},
		},
	}
	if got := (&remindersParser{}).ParseTree(tree, "com.apple.reminders", "Reminders", ""); got != nil {
		t.Errorf("expected nil for empty task list, got: %+v", got)
	}
}

// TestRemindersParser_IgnoresCheckboxActionLabel reproduces the bug
// where the AXCheckBox carries the toggle's action label
// ("标记为完成" — "Mark as complete") and the synthesized AXCell.Description
// carries a comma-joined VoiceOver string that overlaps the individual
// AXStaticText nodes. The parser must ignore both noise sources and use
// the AXStaticText title.
func TestRemindersParser_IgnoresCheckboxActionLabel(t *testing.T) {
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "旗标 — 提醒事项",
		Children: []*AXNode{
			{
				Role: "AXCell",
				// Synthesized VoiceOver label — must not pollute output.
				Description: "未完成、旗标、isoform 早会、提醒、明天 11:00",
				Children: []*AXNode{
					{Role: "AXCheckBox", Title: "标记为完成"},
					staticText("isoform 早会"),
					staticText("提醒  明天 11:00"),
				},
			},
		},
	}

	parsed := (&remindersParser{}).ParseTree(tree, "com.apple.reminders", "旗标 — 提醒事项", "")
	if parsed == nil {
		t.Fatalf("expected non-nil parsed content")
	}
	if strings.Contains(parsed.Body, "标记为完成") {
		t.Errorf("checkbox action label leaked into body: %q", parsed.Body)
	}
	if strings.Contains(parsed.Body, "未完成、旗标") {
		t.Errorf("container synthesized label leaked into body: %q", parsed.Body)
	}
	if !strings.HasPrefix(parsed.Body, "- [ ] isoform 早会") {
		t.Errorf("expected static-text title at line start, got: %q", parsed.Body)
	}
	if !strings.Contains(parsed.Body, "明天 11:00") {
		t.Errorf("subtitle missing: %q", parsed.Body)
	}
	// Title appears exactly once even though it was also embedded in the
	// container's synthesized description.
	if c := strings.Count(parsed.Body, "isoform 早会"); c != 1 {
		t.Errorf("title should appear exactly once, found %d: %q", c, parsed.Body)
	}
}

// TestRemindersParser_DedupesMirroredTitle guards against macOS builds
// that surface the title on both the checkbox AND a sibling static text.
// The reminder must appear exactly once.
func TestRemindersParser_DedupesMirroredTitle(t *testing.T) {
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role: "AXCell",
				Children: []*AXNode{
					{Role: "AXCheckBox", Title: "renew passport"},
					staticText("renew passport"),
					staticText("Due tomorrow"),
				},
			},
		},
	}
	parsed := (&remindersParser{}).ParseTree(tree, "com.apple.reminders", "", "")
	if parsed == nil {
		t.Fatalf("expected non-nil parsed content")
	}
	if c := strings.Count(parsed.Body, "renew passport"); c != 1 {
		t.Errorf("title should appear once, found %d times: %q", c, parsed.Body)
	}
	if !strings.Contains(parsed.Body, "Due tomorrow") {
		t.Errorf("subtitle missing: %q", parsed.Body)
	}
}

// TestRemindersParser_IgnoresInfoPopoverFields reproduces the bug
// where opening the (i) detail panel on a reminder polluted the
// output: popover toggles ("紧急", "旗标", "位置", "发信息时") each
// became a fake task, and the host reminder's subtitle inhaled every
// AXStaticText inside the popover ("日期, 明天, 时间, 12:00, 紧急,
// 重复, ..."). Now the popover subtree is skipped both when picking
// row containers and when collecting subtitle text.
func TestRemindersParser_IgnoresInfoPopoverFields(t *testing.T) {
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "旗标 — 提醒事项",
		Children: []*AXNode{
			// Reminder row with an Info popover nested inside it.
			{
				Role: "AXCell",
				Children: []*AXNode{
					{Role: "AXCheckBox"},
					staticText("提交resume"),
					staticText("提醒  明天 12:00"),
					{
						Role: "AXPopover",
						Children: []*AXNode{
							staticText("日期"),
							staticText("明天"),
							staticText("时间"),
							staticText("12:00"),
							{
								Role: "AXGroup",
								Children: []*AXNode{
									{Role: "AXCheckBox"},
									staticText("紧急"),
									staticText("将此提醒事项标记为紧急以设定闹钟。"),
								},
							},
							{
								Role: "AXGroup",
								Children: []*AXNode{
									{Role: "AXCheckBox"},
									staticText("旗标"),
								},
							},
							{
								Role: "AXGroup",
								Children: []*AXNode{
									{Role: "AXCheckBox"},
									staticText("位置"),
								},
							},
							{
								Role: "AXGroup",
								Children: []*AXNode{
									{Role: "AXCheckBox"},
									staticText("发信息时"),
								},
							},
						},
					},
				},
			},
		},
	}

	parsed := (&remindersParser{}).ParseTree(tree, "com.apple.reminders", "旗标 — 提醒事项", "")
	if parsed == nil {
		t.Fatalf("expected non-nil parsed content")
	}

	lines := strings.Split(strings.TrimSpace(parsed.Body), "\n")
	if len(lines) != 1 {
		t.Errorf("expected exactly 1 reminder line, got %d:\n%s", len(lines), parsed.Body)
	}

	// "12:00" appears in the legitimate subtitle "提醒  明天 12:00" — exclude.
	// Other popover field names must NOT appear anywhere.
	for _, noise := range []string{"紧急", "旗标", "位置", "发信息时", "日期", "时间", "将此提醒事项"} {
		if strings.Contains(parsed.Body, noise) {
			t.Errorf("popover field %q leaked into body: %q", noise, parsed.Body)
		}
	}
	if !strings.Contains(parsed.Body, "提交resume") {
		t.Errorf("real reminder title missing: %q", parsed.Body)
	}
	if !strings.Contains(parsed.Body, "明天 12:00") {
		t.Errorf("real reminder subtitle missing: %q", parsed.Body)
	}
}

// TestRemindersParser_DedupesSiblingCheckboxes guards against the
// rare case where two AXCheckBox nodes end up sharing the same row
// container (e.g. a "completed" checkbox alongside a "flagged" one).
// The row should be emitted once, not twice.
func TestRemindersParser_DedupesSiblingCheckboxes(t *testing.T) {
	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role: "AXCell",
				Children: []*AXNode{
					cb("0"),
					cb("0"), // hypothetical second checkbox in the same row
					staticText("water plants"),
				},
			},
		},
	}
	parsed := (&remindersParser{}).ParseTree(tree, "com.apple.reminders", "", "")
	if parsed == nil {
		t.Fatalf("expected non-nil parsed content")
	}
	if strings.Count(parsed.Body, "water plants") != 1 {
		t.Errorf("task should appear exactly once, got body: %q", parsed.Body)
	}
}
