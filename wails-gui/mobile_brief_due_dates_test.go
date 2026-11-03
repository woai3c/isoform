package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestMobileBriefPurposeDueDatesReachEverySection(t *testing.T) {
	app := newMobileBriefPurposeTestApp(t)
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	// It is already October 17 in UTC, but still October 16 on the Mac.
	now := time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC)
	cases := []struct {
		name             string
		asOf             time.Time
		dueKind          string
		dueValue         string
		wantDueAt        string
		commitmentStatus string
	}{
		{name: "due_today", asOf: now, dueKind: PurposeDueDate, dueValue: "2026-10-16", wantDueAt: "2026-10-17T06:59:59Z", commitmentStatus: "due_today"},
		{name: "overdue", asOf: now, dueKind: PurposeDueDate, dueValue: "2026-10-15", wantDueAt: "2026-10-16T06:59:59Z", commitmentStatus: "slipping"},
		{name: "future", asOf: now, dueKind: PurposeDueDate, dueValue: "2026-10-17", wantDueAt: "2026-10-18T06:59:59Z"},
		{name: "none", asOf: now, dueKind: PurposeDueNone},
		{name: "window_is_not_a_deadline", asOf: now, dueKind: PurposeDueWindow, dueValue: "2026-10-16"},
		{name: "invalid_date", asOf: now, dueKind: PurposeDueDate, dueValue: "later this week"},
		{name: "timestamp_today", asOf: now, dueKind: PurposeDueDate, dueValue: "2026-10-16T21:30:00-07:00", wantDueAt: "2026-10-17T04:30:00Z", commitmentStatus: "due_today"},
		{name: "timestamp_overdue", asOf: now, dueKind: PurposeDueDate, dueValue: "2026-10-15T21:30:00-07:00", wantDueAt: "2026-10-16T04:30:00Z", commitmentStatus: "slipping"},
		{name: "daylight_saving_end", asOf: time.Date(2026, 11, 1, 7, 30, 0, 0, time.UTC), dueKind: PurposeDueDate, dueValue: "2026-11-01", wantDueAt: "2026-11-02T07:59:59Z", commitmentStatus: "due_today"},
	}
	for _, kind := range []string{PurposeKindGoal, PurposeKindCommitment} {
		for _, tc := range cases {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				detail := mobileBriefPurposeFixture(kind+"-"+tc.name, tc.asOf)
				detail.OpenLoop.Kind = kind
				detail.Current.Counterparty = "Dana"
				detail.Current.DueKind, detail.Current.DueValue = tc.dueKind, tc.dueValue
				in := mobileBriefInput{AsOf: tc.asOf, Location: loc}
				collectMobileBriefPurposes(app, &in, []PurposeOpenLoopDetail{detail}, nil)
				brief := buildMobileBriefV1(in)
				if len(brief.Sections.WorkingToward) != 1 || len(brief.Sections.OpenLoops) != 1 || len(brief.Sections.WaitingOnYou) != 1 {
					t.Fatalf("purpose missing from progress, legacy or waiting section: %+v", brief.Sections)
				}
				items := []MobileBriefItem{brief.Sections.WorkingToward[0], brief.Sections.OpenLoops[0], brief.Sections.WaitingOnYou[0]}
				if kind == PurposeKindCommitment && tc.commitmentStatus != "" {
					if len(brief.Sections.Commitments) != 1 || brief.Sections.Commitments[0].Status != tc.commitmentStatus {
						t.Fatalf("deadline commitment = %+v, want status %q", brief.Sections.Commitments, tc.commitmentStatus)
					}
					items = append(items, brief.Sections.Commitments[0])
				} else if len(brief.Sections.Commitments) != 0 {
					t.Fatalf("unexpected commitment for kind %q and due %q: %+v", kind, tc.dueValue, brief.Sections.Commitments)
				}
				for _, item := range items {
					if item.DueAt != tc.wantDueAt {
						t.Errorf("%s dueAt = %q, want %q", item.ID, item.DueAt, tc.wantDueAt)
					}
					if item.At != "" || item.LastTouch != "" {
						t.Errorf("deadline became observed activity: %+v", item)
					}
				}
				if brief.Sections.OpenLoops[0].Status != PurposeStateActive {
					t.Fatal("deadline changed the Purpose lifecycle state")
				}
			})
		}
	}
}

func TestMobileBriefDueAtIsOptionalOnTheWire(t *testing.T) {
	for _, dueAt := range []string{"", "2026-10-17T06:59:59Z"} {
		t.Run(fmt.Sprintf("has_due_%t", dueAt != ""), func(t *testing.T) {
			item := MobileBriefItem{ID: "openLoops-1", Title: "Send the draft", Status: PurposeStateActive, DueAt: dueAt}
			raw, err := json.Marshal(item)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), `"dueAt"`) != (dueAt != "") {
				t.Fatalf("optional deadline encoding = %s", raw)
			}
			var decoded MobileBriefItem
			if err := json.Unmarshal(raw, &decoded); err != nil || decoded.DueAt != dueAt {
				t.Fatalf("deadline round trip = %+v, %v", decoded, err)
			}
			// The old phone decodes only the fields it knows.
			var oldPhone struct{ ID, Title, Status string }
			if err := json.Unmarshal(raw, &oldPhone); err != nil || oldPhone.ID != item.ID || oldPhone.Title != item.Title || oldPhone.Status != item.Status {
				t.Fatalf("old phone deadline compatibility = %+v, %v", oldPhone, err)
			}
		})
	}
}
