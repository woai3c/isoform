package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/handoff"
	"wails-gui/storage"

	"github.com/google/uuid"
)

// mobileTestBriefSource is an in-memory brief service for route tests.
type mobileTestBriefSource struct {
	brief     *MobileBriefV1
	refreshes int
}

func (s *mobileTestBriefSource) Start(ctx context.Context) {}
func (s *mobileTestBriefSource) Stop()                     {}

func (s *mobileTestBriefSource) LatestMobileBrief() (MobileBriefV1, bool) {
	if s.brief == nil {
		return MobileBriefV1{}, false
	}
	return *s.brief, true
}

func (s *mobileTestBriefSource) ListMobileBriefs(limit int) []MobileBriefSummary {
	if s.brief == nil {
		return nil
	}
	return []MobileBriefSummary{{BriefID: s.brief.BriefID, Slot: s.brief.Slot, LocalDate: s.brief.LocalDate,
		AsOf: s.brief.AsOf, NarrativeStatus: s.brief.NarrativeStatus}}
}

func (s *mobileTestBriefSource) GetMobileBrief(briefID string) (MobileBriefV1, bool) {
	if s.brief == nil || s.brief.BriefID != briefID {
		return MobileBriefV1{}, false
	}
	return *s.brief, true
}

func (s *mobileTestBriefSource) RequestMobileBrief(reason string) (MobileBriefRequestResult, error) {
	s.refreshes++
	return MobileBriefRequestResult{Status: "started", NextAllowedAt: "2026-10-09T08:00:00Z"}, nil
}

func (s *mobileTestBriefSource) Schedule() (MobileBriefSchedule, error) {
	return MobileBriefSchedule{Enabled: true, Morning: "07:00", Evening: "18:00", NarrativeAvailable: true}, nil
}

func (s *mobileTestBriefSource) SetSchedule(schedule MobileBriefSchedule) (MobileBriefSchedule, error) {
	return schedule, nil
}

func (s *mobileTestBriefSource) Health() MobileBriefHealth {
	return MobileBriefHealth{Enabled: true}
}

func (s *mobileTestBriefSource) Audio(string) (MobileBriefAudioData, bool) {
	return MobileBriefAudioData{}, false
}

func mobileTestBrief() MobileBriefV1 {
	return MobileBriefV1{Version: 1, BriefID: "2026-10-09-morning", Slot: "morning", LocalDate: "2026-10-09",
		AsOf: "2026-10-09T07:00:12Z", HubName: "Mac", NarrativeStatus: "ok",
		Narrative: []MobileBriefPara{}, Sections: MobileBriefSections{}, Counts: MobileBriefCounts{OpenLoopsByKind: map[string]int{}},
		Evidence: []MobileBriefRef{}, Limits: MobileBriefLimits{TruncatedSections: []string{}}}
}

func mobileTestDevice(scopes ...string) mobileDeviceRecord {
	now := time.Now()
	if scopes == nil {
		scopes = []string{}
	}
	return mobileDeviceRecord{MobileDeviceView: MobileDeviceView{DeviceID: mobileTestDeviceID,
		CreatedAt: mobileTimestamp(now), ExpiresAt: mobileTimestamp(now.Add(mobileDeviceLifetime)), Scopes: scopes}}
}

func TestMobileRouteScopesAreEnforced(t *testing.T) {
	brief := mobileTestBrief()
	source := &mobileTestBriefSource{brief: &brief}
	bridge, err := newMobileBridge(&App{mobileBriefSvc: source}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	read, refresh := mobileScopeBriefRead, mobileScopeBriefRefresh
	cases := []struct {
		name, method, path string
		scopes             []string
		want               int
	}{
		{"status needs no scope", http.MethodGet, "/mobile/v1/status", nil, http.StatusOK},
		{"device list needs no scope", http.MethodGet, "/mobile/v1/devices", nil, http.StatusOK},
		{"latest brief with brief:read", http.MethodGet, "/mobile/v1/brief/latest", []string{read}, http.StatusOK},
		{"latest brief without brief:read", http.MethodGet, "/mobile/v1/brief/latest", []string{mobileScopePresenceRead}, http.StatusForbidden},
		{"brief list with brief:read", http.MethodGet, "/mobile/v1/briefs?limit=3", []string{read}, http.StatusOK},
		{"brief list without brief:read", http.MethodGet, "/mobile/v1/briefs?limit=3", []string{mobileScopePush}, http.StatusForbidden},
		{"one brief with brief:read", http.MethodGet, "/mobile/v1/briefs/" + brief.BriefID, []string{read}, http.StatusOK},
		{"one brief without brief:read", http.MethodGet, "/mobile/v1/briefs/" + brief.BriefID, nil, http.StatusForbidden},
		{"refresh with brief:refresh", http.MethodPost, "/mobile/v1/brief/refresh", []string{refresh}, http.StatusOK},
		{"refresh with only brief:read", http.MethodPost, "/mobile/v1/brief/refresh", []string{read}, http.StatusForbidden},
		{"missing brief", http.MethodGet, "/mobile/v1/briefs/2000-01-01-morning", []string{read}, http.StatusNotFound},
		{"brief id outside the template", http.MethodGet, "/mobile/v1/briefs/bad%20id", []string{read}, http.StatusBadRequest},
		{"brief list limit below range", http.MethodGet, "/mobile/v1/briefs?limit=0", []string{read}, http.StatusBadRequest},
		{"brief list limit above range", http.MethodGet, "/mobile/v1/briefs?limit=61", []string{read}, http.StatusBadRequest},
		{"release 2 snapshot route is gone", http.MethodGet, "/mobile/v1/snapshot", []string{read, refresh}, http.StatusNotFound},
		{"release 3 chat route is gone", http.MethodPost, "/mobile/v1/chat", []string{read, refresh}, http.StatusNotFound},
		{"unknown route", http.MethodGet, "/mobile/v1/nothing", nil, http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			bridge.handleAuthorized(recorder, httptest.NewRequest(tc.method, tc.path, nil), mobileTestDevice(tc.scopes...))
			if recorder.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", recorder.Code, tc.want, recorder.Body.String())
			}
			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("route response may be cached")
			}
		})
	}
	if source.refreshes != 1 {
		t.Fatalf("refresh reached the brief service %d times", source.refreshes)
	}
}

func TestMobileBriefRoutesReportNotReady(t *testing.T) {
	bridge, err := newMobileBridge(&App{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	device := mobileTestDevice(mobileScopeBriefRead, mobileScopeBriefRefresh)
	for _, path := range []string{"/mobile/v1/brief/latest", "/mobile/v1/briefs", "/mobile/v1/briefs/2026-10-09-morning"} {
		recorder := httptest.NewRecorder()
		bridge.handleAuthorized(recorder, httptest.NewRequest(http.MethodGet, path, nil), device)
		if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "the brief service is not ready") {
			t.Fatalf("%s returned %d", path, recorder.Code)
		}
	}
	recorder := httptest.NewRecorder()
	bridge.handleAuthorized(recorder, httptest.NewRequest(http.MethodGet, "/mobile/v1/status", nil), device)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"protocolVersion":1`) {
		t.Fatalf("status is not available while the brief service starts: %d", recorder.Code)
	}
}

func TestMobileDeviceRevocationIsSelfOnly(t *testing.T) {
	dir := t.TempDir()
	bridge, err := newMobileBridge(&App{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	first, second := newMobileTestPhone(t), newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, first, []string{mobileScopeBriefRead})
	mobileTestAddDevice(t, bridge, second, []string{mobileScopeBriefRead})
	firstRecord := mobileDeviceRecord{MobileDeviceView: bridge.devices[first.deviceID].MobileDeviceView}

	recorder := httptest.NewRecorder()
	bridge.handleAuthorized(recorder, httptest.NewRequest(http.MethodDelete, "/mobile/v1/devices/"+second.deviceID, nil), firstRecord)
	if recorder.Code != http.StatusForbidden || bridge.devices[second.deviceID] == nil {
		t.Fatal("a device revoked another device")
	}
	recorder = httptest.NewRecorder()
	bridge.handleAuthorized(recorder, httptest.NewRequest(http.MethodDelete, "/mobile/v1/devices/"+first.deviceID, nil), firstRecord)
	if recorder.Code != http.StatusOK || bridge.devices[first.deviceID] != nil {
		t.Fatalf("self revocation failed: %d", recorder.Code)
	}
	stored, err := os.ReadFile(filepath.Join(dir, "devices.json"))
	if err != nil || strings.Contains(string(stored), first.deviceID) || !strings.Contains(string(stored), second.deviceID) {
		t.Fatal("the revocation did not persist exactly one removal")
	}
}

func TestMobileDeviceStoreVersionOneIsDiscardedAndVersionTwoRoundTrips(t *testing.T) {
	dir := t.TempDir()
	legacy := map[string]any{"version": 1, "hubId": uuid.NewString(), "devices": []any{
		map[string]any{"deviceId": uuid.NewString(), "tokenHash": strings.Repeat("a", 64)},
	}}
	if err := writeMobilePrivateJSON(filepath.Join(dir, "devices.json"), legacy); err != nil {
		t.Fatal(err)
	}
	bridge, err := newMobileBridge(&App{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	if len(bridge.devices) != 0 {
		t.Fatal("a version 1 device survived the migration")
	}
	stored, err := os.ReadFile(filepath.Join(dir, "devices.json"))
	if err != nil || !strings.Contains(string(stored), `"version": 2`) || strings.Contains(string(stored), "tokenHash") {
		t.Fatal("the discarded store was not replaced by a version 2 store")
	}

	phone := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead, mobileScopePush})
	bridge.mu.Lock()
	bridge.devices[phone.deviceID].SendSeq = 5
	bridge.devices[phone.deviceID].LastAcceptedSeq = 9
	err = bridge.persistDevicesLocked()
	bridge.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := newMobileBridge(&App{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reloaded.Stop)
	record := reloaded.devices[phone.deviceID]
	if record == nil || record.DevicePub != phone.publicKeyText() || record.LastAcceptedSeq != 9 {
		t.Fatal("a version 2 device record did not round trip")
	}
	if record.SendSeq != 5+mobileSendSeqSkip {
		t.Fatalf("send counter %d did not skip ahead on load", record.SendSeq)
	}
	if len(record.Scopes) != 2 || record.Scopes[0] != mobileScopeBriefRead {
		t.Fatalf("scopes did not round trip: %v", record.Scopes)
	}
}

func TestMobileExpiredDevicesAreRemovedOnLoad(t *testing.T) {
	dir := t.TempDir()
	bridge, err := newMobileBridge(&App{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	expired := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, expired, []string{mobileScopeBriefRead})
	bridge.mu.Lock()
	bridge.devices[expired.deviceID].ExpiresAt = mobileTimestamp(time.Now().Add(-time.Minute))
	err = bridge.persistDevicesLocked()
	bridge.mu.Unlock()
	bridge.Stop()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := newMobileBridge(&App{}, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reloaded.Stop)
	if reloaded.devices[expired.deviceID] != nil {
		t.Fatal("an expired device was loaded")
	}
	// A device whose authorization lapsed while the hub was running is refused too.
	lapsed := mobileTestDevice(mobileScopeBriefRead)
	lapsed.ExpiresAt = mobileTimestamp(time.Now().Add(-time.Second))
	if status, _ := reloaded.serveRequest(lapsed, mobileRequestPlaintext{Method: http.MethodGet, Path: "/status"}); status != http.StatusUnauthorized {
		t.Fatalf("an expired device was served: %d", status)
	}
}

func TestMobileRequestValidation(t *testing.T) {
	valid := mobileRequestPlaintext{ID: mobileTestOtherID, Method: http.MethodGet, Path: "/briefs"}
	raw, _ := json.Marshal(valid)
	if _, err := decodeMobileRequest(raw); err != nil {
		t.Fatalf("a valid request was refused: %v", err)
	}
	for name, mutate := range map[string]func(*mobileRequestPlaintext){
		"method outside the release":  func(r *mobileRequestPlaintext) { r.Method = http.MethodPut },
		"path with a query character": func(r *mobileRequestPlaintext) { r.Path = "/briefs?limit=1" },
		"path with traversal":         func(r *mobileRequestPlaintext) { r.Path = "/../secret" },
		"id that is not a uuid":       func(r *mobileRequestPlaintext) { r.ID = "request-1" },
		"oversized query":             func(r *mobileRequestPlaintext) { r.Query = strings.Repeat("q", 2049) },
	} {
		request := valid
		mutate(&request)
		raw, _ := json.Marshal(request)
		if _, err := decodeMobileRequest(raw); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
	if _, err := decodeMobileRequest([]byte(`{"id":"` + mobileTestOtherID + `","method":"GET","path":"/status","extra":1}`)); err == nil {
		t.Fatal("an unknown field in a request was accepted")
	}
}

func TestMobileRequestsAreServedOnceAndAudited(t *testing.T) {
	bridge, relay := mobileTestHub(t)
	phone := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead})
	conn := relay.acceptRegistration(t, mobileTestListed(phone))
	request := []byte(`{"id":"` + mobileTestOtherID + `","method":"GET","path":"/devices","query":"","body":""}`)
	env := phone.sealRequest(t, bridge.identity.Public, bridge.identity.HubID, 1, request)
	conn.send(t, map[string]any{"t": "in", "env": env})
	relay.nextFrame(t, "out")
	relay.nextFrame(t, "ack")

	// The worker acknowledges before it appends the audit line, so wait for the line.
	auditPath := filepath.Join(bridge.stateDir, "mobile_audit.jsonl")
	mobileTestWaitFor(t, "the audit line", func() bool {
		raw, err := os.ReadFile(auditPath)
		return err == nil && strings.HasSuffix(string(raw), "\n")
	})
	info, err := os.Stat(auditPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("audit log is not owner-only: %v", err)
	}
	lines := readMobileTestLines(t, auditPath)
	if len(lines) != 1 {
		t.Fatalf("audit has %d lines after one request", len(lines))
	}
	var entry mobileAuditEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatal(err)
	}
	if entry.DeviceID != phone.deviceID || entry.Method != http.MethodGet || entry.Path != "/devices" || entry.Status != http.StatusOK || entry.TS == "" {
		t.Fatalf("unexpected audit entry: %+v", entry)
	}
	if strings.Contains(lines[0], "query") || strings.Contains(lines[0], "body") {
		t.Fatal("the audit line carries request content")
	}

	// A redelivered envelope is acknowledged, not served again, and not audited again.
	// One worker serves a device in order, so this ack follows every earlier audit write.
	conn.send(t, map[string]any{"t": "in", "env": env})
	relay.nextFrame(t, "ack")
	relay.expectNoFrame(t, "out", 200*time.Millisecond)
	if lines := readMobileTestLines(t, auditPath); len(lines) != 1 {
		t.Fatalf("a replayed request was audited: %d lines", len(lines))
	}
}

func TestMobileOversizedResponsesBecomeAnExplicitError(t *testing.T) {
	bridge, relay := mobileTestHub(t)
	state, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.Close() })
	bridge.app.store = state
	large := mobileTestBrief()
	// The fixture must pass source and mail privacy before exercising the
	// transport's independent response-size guard.
	large.Evidence = []MobileBriefRef{{Ref: "e1", SourceKind: "history", SourceID: "completed-work", Label: "Completed work"}}
	large.Narrative = []MobileBriefPara{{Text: strings.Repeat("\"", 40*1024), Refs: []string{"e1"}}, {Text: strings.Repeat("\"", 40*1024), Refs: []string{"e1"}}}
	work, err := handoff.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bridge.app.handoffStore = work
	if _, err := work.Save(handoff.Entry{ID: "completed-work", Title: "Completed work", Source: "manual", Status: handoff.StatusCompleted}); err != nil {
		t.Fatal(err)
	}
	if filtered := bridge.app.mobileBriefForDelivery(large); len(filtered.Narrative) != len(large.Narrative) {
		t.Fatal("size-guard fixture lost its readable source")
	}
	bridge.app.mobileBriefSvc = &mobileTestBriefSource{brief: &large}
	phone := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead})
	conn := relay.acceptRegistration(t, mobileTestListed(phone))
	request := []byte(`{"id":"` + mobileTestOtherID + `","method":"GET","path":"/brief/latest","query":"","body":""}`)
	conn.send(t, map[string]any{"t": "in", "env": phone.sealRequest(t, bridge.identity.Public, bridge.identity.HubID, 1, request)})
	next := func() mobileResponsePlaintext {
		for {
			out := relay.nextFrame(t, "out")
			var frame struct {
				Env mobileEnvelope `json:"env"`
			}
			if err := json.Unmarshal(out.raw, &frame); err != nil {
				t.Fatal(err)
			}
			var candidate mobileResponsePlaintext
			if err := json.Unmarshal(phone.openFromHub(t, bridge.identity.Public, bridge.identity.HubID, frame.Env), &candidate); err != nil {
				t.Fatal(err)
			}
			// A today_changed event is unsolicited and carries no request id.
			if candidate.ID != "" {
				return candidate
			}
		}
	}
	// A brief past the inline size is answered with its parts, never cut.
	if response := next(); response.Status != http.StatusOK || !strings.Contains(response.Body, `"chunked":true`) {
		t.Fatalf("a large brief was not offered in parts: %d %.200s", response.Status, response.Body)
	}
	// Any other response past the connection's size is an explicit error.
	if err := bridge.sendMobileResponse(phone.deviceID, mobileTestOtherID, http.StatusOK, strings.Repeat("x", mobileMaxMessagePlaintext)); err != nil {
		t.Fatal(err)
	}
	if response := next(); response.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("an oversized response was sent with status %d", response.Status)
	}
}

func TestMobileEnvelopeHeaderFieldsRejectWrongHub(t *testing.T) {
	bridge, relay := mobileTestHub(t)
	phone := newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, phone, []string{mobileScopeBriefRead})
	conn := relay.acceptRegistration(t, mobileTestListed(phone))
	request := []byte(`{"id":"` + mobileTestOtherID + `","method":"GET","path":"/status","query":"","body":""}`)
	env := phone.sealRequest(t, bridge.identity.Public, bridge.identity.HubID, 1, request)
	env.Hub = mobileTestOtherID
	conn.send(t, map[string]any{"t": "in", "env": env})
	// Nothing is served or acknowledged, so the relay keeps the envelope until it expires.
	relay.expectNoFrame(t, "out", 300*time.Millisecond)
	relay.expectNoFrame(t, "ack", 0)
	if bridge.devices[phone.deviceID].LastAcceptedSeq != 0 {
		t.Fatal("an envelope for another hub advanced the replay counter")
	}
}

func TestMobileReconciliationKeepsApprovalsInFlight(t *testing.T) {
	bridge, err := newMobileBridge(&App{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bridge.Stop)
	inFlight, stale := newMobileTestPhone(t), newMobileTestPhone(t)
	mobileTestAddDevice(t, bridge, inFlight, []string{mobileScopeBriefRead})
	mobileTestAddDevice(t, bridge, stale, []string{mobileScopeBriefRead})
	bridge.mu.Lock()
	bridge.devices[inFlight.deviceID].approving = true
	bridge.reconcileLocked(nil)
	kept := bridge.devices[inFlight.deviceID] != nil
	dropped := bridge.devices[stale.deviceID] == nil
	bridge.mu.Unlock()
	if !kept || !dropped {
		t.Fatalf("reconciliation kept=%v dropped=%v; an approval in flight must survive and an unlisted device must not", kept, dropped)
	}
}

func readMobileTestLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
