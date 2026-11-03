package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A GET /today must answer from the cached projection. A slow builder that is
// still running must not hold the request, and the request must not start one.
func TestMobileTodayServesCachedSnapshotWithoutBuilding(t *testing.T) {
	bridge := &mobileBridge{}
	var builds atomic.Int32
	bridge.today.build = func() (MobileTodayV1, error) {
		builds.Add(1)
		return MobileTodayV1{GeneratedAt: "cached-1", Briefs: []mobileTodayBrief{}, NeedsYou: []mobileTodayNeed{}, Topics: []mobileTopicView{}}, nil
	}
	if _, err := bridge.rebuildToday(); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	defer close(release)
	bridge.today.build = func() (MobileTodayV1, error) {
		builds.Add(1)
		<-release
		return MobileTodayV1{GeneratedAt: "slow"}, nil
	}
	before := builds.Load()
	device := mobileDeviceRecord{}
	device.Scopes = []string{mobileScopeBriefRead}
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		bridge.serveToday(recorder, httptest.NewRequest(http.MethodGet, "/v1/today", nil), device)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("GET /today waited on a build instead of answering from the cache")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /today status = %d, want 200", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "cached-1") {
		t.Fatalf("GET /today did not return the cached snapshot: %s", recorder.Body.String())
	}
	if builds.Load() != before {
		t.Fatal("GET /today started a build on the request path")
	}
}

// Before the first build, or after a withdrawal, GET /today answers 503 at once
// and never builds on the request path.
func TestMobileTodayWithoutSnapshotAnswersAtOnceWithoutBuilding(t *testing.T) {
	bridge := &mobileBridge{}
	var builds atomic.Int32
	release := make(chan struct{})
	defer close(release)
	bridge.today.build = func() (MobileTodayV1, error) {
		builds.Add(1)
		<-release
		return MobileTodayV1{}, nil
	}
	device := mobileDeviceRecord{}
	device.Scopes = []string{mobileScopeBriefRead}
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		bridge.serveToday(recorder, httptest.NewRequest(http.MethodGet, "/v1/today", nil), device)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("GET /today with no snapshot waited instead of answering at once")
	}
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /today status = %d, want 503 while no snapshot is cached", recorder.Code)
	}
	if builds.Load() != 0 {
		t.Fatal("GET /today with no snapshot started a build on the request path")
	}
}

// Withdrawn data must not be served from the cache after invalidation.
func TestMobileTodayInvalidationWithdrawsTheCachedSnapshot(t *testing.T) {
	bridge := &mobileBridge{}
	bridge.today.build = func() (MobileTodayV1, error) {
		return MobileTodayV1{GeneratedAt: "withdrawn-soon"}, nil
	}
	if _, err := bridge.rebuildToday(); err != nil {
		t.Fatal(err)
	}
	bridge.invalidateToday()
	device := mobileDeviceRecord{}
	device.Scopes = []string{mobileScopeBriefRead}
	recorder := httptest.NewRecorder()
	bridge.serveToday(recorder, httptest.NewRequest(http.MethodGet, "/v1/today", nil), device)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /today after invalidation status = %d, want 503", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "withdrawn-soon") {
		t.Fatal("GET /today served a withdrawn snapshot")
	}
}

// The first snapshot is built by the Today loop at start, before any phone asks.
func TestMobileTodayFirstSnapshotIsBuiltAtStart(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	bridge := &mobileBridge{ctx: ctx}
	var builds atomic.Int32
	bridge.today.build = func() (MobileTodayV1, error) {
		builds.Add(1)
		return MobileTodayV1{GeneratedAt: "at-start"}, nil
	}
	bridge.startTodayCache()
	waitForMobileTodaySnapshot(t, bridge)
	if builds.Load() == 0 {
		t.Fatal("the first Today snapshot was served without the builder")
	}
}

// The Today loop builds the first snapshot in the background. A test that
// reads GET /today waits for it instead of relying on a request-path build.
func waitForMobileTodaySnapshot(t *testing.T, bridge *mobileBridge) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		bridge.today.mu.RLock()
		built := bridge.today.value != nil
		bridge.today.mu.RUnlock()
		if built {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the first Today snapshot was not built at start")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
