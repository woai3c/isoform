package main

import (
	"testing"
	"time"
)

func TestSelfDirectiveAPIsExposeOnlyValidatedUserOperations(t *testing.T) {
	activity := newTestStore(t)
	app := &App{activity: &activityMonitorState{store: activity}}
	created, _, err := NewSelfDirectiveStore(activity.db).Create(CreateSelfDirectiveInput{
		IdempotencyKey: "self-directive-api:create",
		Kind:           selfModelKindPrinciple,
		Statement:      "Prefer decisions that preserve a reversible path.",
		Scope:          "Product decisions",
		OccurredAt:     time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}

	for range 2 {
		detail, err := app.GetSelfDirectiveDetail(created.Directive.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(detail.Events) != 1 || len(detail.Revisions) != 1 ||
			detail.Current.ID != created.SelfEntry.ID {
			t.Fatalf("unexpected directive detail: %+v", detail)
		}
	}
	if got := countSelfDirectiveRows(
		t,
		activity.db,
		"self_user_directive_events_v1",
	); got != 1 {
		t.Fatalf("repeated reads created %d events, want 1", got)
	}

	retracted, err := app.RetractSelfDirective(RetractSelfDirectiveRequest{
		DirectiveID:         created.Directive.ID,
		ExpectedSelfEntryID: created.SelfEntry.ID,
		IdempotencyKey:      "self-directive-api:retract",
		Reason:              "This needs a more precise replacement.",
	})
	if err != nil || retracted.Event.EventType != SelfDirectiveEventRetracted ||
		retracted.SelfEntry.Status != selfModelStatusRetracted {
		t.Fatalf("unexpected API retraction: result=%+v err=%v", retracted, err)
	}
	detail, err := app.GetSelfDirectiveDetail(created.Directive.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Revisions) != 2 ||
		detail.Current.Status != selfModelStatusRetracted {
		t.Fatalf("unexpected retracted detail: %+v", detail)
	}
}

func TestSelfDirectiveAPIsRejectUnavailableOrInvalidState(t *testing.T) {
	app := &App{}
	if _, err := app.GetSelfDirectiveDetail("directive"); err == nil {
		t.Fatal("detail accepted unavailable Self directive storage")
	}
	if _, err := app.RetractSelfDirective(RetractSelfDirectiveRequest{}); err == nil {
		t.Fatal("retract accepted unavailable Self directive storage")
	}

	activity := newTestStore(t)
	available := &App{activity: &activityMonitorState{store: activity}}
	if _, err := available.GetSelfDirectiveDetail(" "); err == nil {
		t.Fatal("detail accepted an empty directive ID")
	}
}
