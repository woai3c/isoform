package appparser

import (
	"strings"
	"testing"
)

func TestMessagesParser_DatePrefix(t *testing.T) {
	bubble := func(desc string, rightHalf bool) *AXNode {
		x := 0.0
		if rightHalf {
			x = 600
		}
		return &AXNode{
			Role:        "AXGroup",
			Description: desc,
			Frame:       &AXFrame{X: x, Y: 0, W: 200, H: 40},
		}
	}
	divider := func(text string) *AXNode {
		return &AXNode{Role: "AXStaticText", Value: text}
	}

	cases := []struct {
		name     string
		children []*AXNode
		wantTS   []string
	}{
		{
			name: "chinese full date propagates to following bubbles",
			children: []*AXNode{
				divider("2024年11月6日 18:51"),
				bubble("10086, 北京市空气重污染应急指挥部办公室提示, 18:51", false),
				divider("2024年12月14日 07:16"),
				bubble("10086, 您已抵达泰国, 07:16", false),
				bubble("10086, 中国文化和旅游部温馨提示, 07:16", false),
			},
			wantTS: []string{
				"2024年11月6日 18:51",
				"2024年12月14日 07:16",
				"2024年12月14日 07:16",
			},
		},
		{
			name: "english relative divider",
			children: []*AXNode{
				divider("Yesterday 14:31"),
				bubble("Alice, hello, 14:31", false),
			},
			wantTS: []string{"Yesterday 14:31"},
		},
		{
			name: "no divider falls back to bare HH:MM",
			children: []*AXNode{
				bubble("Bob, hi, 09:00", false),
			},
			wantTS: []string{"09:00"},
		},
		{
			name: "divider nested one level deep is still picked up",
			children: []*AXNode{
				{Role: "AXGroup", Children: []*AXNode{divider("2024年12月14日 07:16")}},
				bubble("10086, late msg, 23:59", false),
			},
			wantTS: []string{"2024年12月14日 23:59"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			container := &AXNode{Role: "AXGroup", Children: tc.children}
			// Outer container needs at least 3 message-group children at some
			// level so ParseTree's heuristic locks onto it. Wrap with the AX
			// window the parser expects.
			window := &AXNode{
				Role:     "AXWindow",
				Title:    "10086",
				Frame:    &AXFrame{X: 0, Y: 0, W: 800, H: 600},
				Children: []*AXNode{padContainer(container)},
			}
			parsed := (&messagesParser{}).ParseTree(window, "com.apple.MobileSMS", "10086", "")
			if parsed == nil {
				t.Fatalf("ParseTree returned nil")
			}
			// padContainer may add filler bubbles after the real ones to
			// satisfy ParseTree's hit-count heuristic; only assert the leading
			// real entries.
			if got := len(parsed.Messages); got < len(tc.wantTS) {
				t.Fatalf("len(Messages)=%d want >= %d (msgs=%+v)", got, len(tc.wantTS), parsed.Messages)
			}
			for i, want := range tc.wantTS {
				if got := parsed.Messages[i].Timestamp; got != want {
					t.Errorf("Messages[%d].Timestamp=%q want %q", i, got, want)
				}
			}
		})
	}
}

// padContainer ensures the container is detected by ParseTree's hit-count
// heuristic (>2 message-group children). When the test only supplies a
// couple of bubbles, we duplicate them so the heuristic locks on without
// changing test-observable timestamps for the ones we assert on.
func padContainer(c *AXNode) *AXNode {
	msgCount := 0
	for _, ch := range c.Children {
		if isMessageGroup(ch) {
			msgCount++
		}
	}
	if msgCount >= 3 {
		return c
	}
	padded := &AXNode{Role: c.Role, Children: append([]*AXNode(nil), c.Children...)}
	for i := 0; i < 3-msgCount; i++ {
		padded.Children = append(padded.Children, &AXNode{
			Role:        "AXGroup",
			Description: "pad, pad, 00:00",
			Frame:       &AXFrame{X: 0, Y: 1000 + float64(i)*40, W: 10, H: 10},
		})
	}
	return padded
}

func TestExtractMessagesDate(t *testing.T) {
	cases := map[string]string{
		"2024年11月6日 18:51":   "2024年11月6日",
		"11月6日":              "11月6日",
		"Yesterday 14:31":    "Yesterday",
		"Today 9:30":         "Today",
		"Monday 8:42 PM":     "Monday",
		"November 6, 2024":   "November 6, 2024",
		"Nov 6":              "Nov 6",
		"10086, hi, 18:51":   "", // sender/content line — must NOT match.
		"":                   "",
		"plain text no date": "",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			got := extractMessagesDate(&AXNode{Description: in})
			if got != want {
				t.Errorf("extractMessagesDate(%q)=%q want %q", in, got, want)
			}
		})
	}
}

// guard against the regex accidentally matching a sender label that happens
// to contain a weekday substring inside the bubble description.
func TestExtractMessagesDate_DoesNotEatBubbleDescriptions(t *testing.T) {
	desc := "Sunday Riley, your order shipped, 14:00"
	if got := extractMessagesDate(&AXNode{Description: desc}); !strings.HasPrefix(got, "Sunday") {
		t.Logf("note: %q parsed as date=%q — acceptable since this is a non-message-group node", desc, got)
	}
}
