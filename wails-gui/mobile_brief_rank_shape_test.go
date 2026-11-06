package main

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
)

// The shape of a rejected scores reply names how it failed, with counts and
// error classes only. The marker stands in for user text and must never appear.
func TestMorningBriefReplyShapeNamesTheFailureWithoutText(t *testing.T) {
	const marker = "Zephyr Ledger"
	const extraKey = "gladlyWithheld"
	item := `{"id":"tax","critique":"` + marker + ` is due today.","score":1,"pick":"need"}`
	for _, tc := range []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "unknown field on an item",
			raw:  `{"items":[{"id":"tax","critique":"` + marker + `","score":1,"pick":"need","` + extraKey + `":"x"}],"leadId":"","openings":[]}`,
			want: []string{"first=object", "objects=1", "items=1", "unknown_field=1"},
		},
		{
			name: "string score",
			raw:  `{"items":[{"id":"tax","critique":"` + marker + `","score":"1","pick":"need"}],"leadId":"","openings":[]}`,
			want: []string{"items=1", "type_mismatch=1"},
		},
		{
			name: "wrapped object",
			raw:  `{"scores":{"items":[` + item + `],"leadId":"","openings":[]}}`,
			want: []string{"objects=1", "no_items=1", "items=0"},
		},
		{
			name: "bare array of items",
			raw:  `[` + item + `]`,
			want: []string{"first=array", "objects=1", "no_items=1"},
		},
		{
			name: "truncated reply",
			raw:  "```json\n{\"items\":[" + item + ",{\"id\":\"tax\",\"crit",
			want: []string{"first=fence", "fence=true", "objects=1", "no_items=1", "unparsed=2"},
		},
		{
			name: "prose only",
			raw:  "I could not rank " + marker + " today.",
			want: []string{"first=text", "objects=0", "unparsed=0"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shape := morningBriefReplyShape(tc.raw)
			for _, want := range tc.want {
				if !strings.Contains(shape, want) {
					t.Fatalf("shape %q does not contain %q", shape, want)
				}
			}
			for _, private := range []string{marker, extraKey, "Zephyr", "tax", "critique"} {
				if strings.Contains(shape, private) {
					t.Fatalf("shape %q carries reply text %q", shape, private)
				}
			}
		})
	}
}

// A rejected reply is logged with its shape and the code, and the reply text is
// not written to the log.
func TestMorningBriefScoresLogsShapeAndNotText(t *testing.T) {
	const marker = "Quartz Harbor"
	raw := `{"scores":{"items":[{"id":"tax","critique":"` + marker + `","score":1,"pick":"need"}]}}`
	var logs bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previous)

	call := func(context.Context, string) (string, error) { return raw, nil }
	if _, _, err := scoreMorningBrief(context.Background(), "Test Person", "", nil, nil, morningBriefTaxCandidates(), call); err == nil {
		t.Fatal("a wrapped reply was accepted")
	}
	out := logs.String()
	if !strings.Contains(out, "shape: first=object") || !strings.Contains(out, "no_items=1") {
		t.Fatalf("the rejected reply was not logged with its shape: %q", out)
	}
	if strings.Contains(out, marker) {
		t.Fatalf("the log carries reply text: %q", out)
	}
}
