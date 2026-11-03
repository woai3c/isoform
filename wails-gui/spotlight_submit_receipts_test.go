package main

import (
	"errors"
	"sync"
	"testing"
)

func TestSpotlightSubmitRetryReconcilesAcceptedRequest(t *testing.T) {
	var receipts sync.Map
	request := spotlightSubmitRequest{RequestID: "turn-1", Prompt: "Keep this"}
	calls := 0
	submit := func() (spotlightSubmitResponse, error) {
		calls++
		return spotlightSubmitResponse{SessionLocalID: "existing"}, nil
	}
	first, err := onceSpotlightSubmit(&receipts, request, submit)
	if err != nil {
		t.Fatal(err)
	}
	// The caller may not have received first. Retry must recover its session.
	again, err := onceSpotlightSubmit(&receipts, request, submit)
	if err != nil || again != first || calls != 1 {
		t.Fatalf("retry=%+v calls=%d err=%v", again, calls, err)
	}
	request.Prompt = "Different text"
	if _, err := onceSpotlightSubmit(&receipts, request, submit); err == nil {
		t.Fatal("reused request ID accepted different content")
	}
}

func TestSpotlightSubmitRetryAfterRejectedRequest(t *testing.T) {
	var receipts sync.Map
	request := spotlightSubmitRequest{RequestID: "turn-1", Prompt: "Keep this"}
	rejected := errors.New("offline")
	if _, err := onceSpotlightSubmit(&receipts, request, func() (spotlightSubmitResponse, error) { return spotlightSubmitResponse{}, rejected }); !errors.Is(err, rejected) {
		t.Fatal(err)
	}
	result, err := onceSpotlightSubmit(&receipts, request, func() (spotlightSubmitResponse, error) { return spotlightSubmitResponse{SessionLocalID: "new"}, nil })
	if err != nil || result.SessionLocalID != "new" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
