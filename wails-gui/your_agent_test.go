package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// yourAgentFakeServer follows the agents contract (docs/briefs/your-agent-1.0).
// Tests set the handler for each route; the request count shows whether a
// call reached the network at all.
type yourAgentFakeServer struct {
	server   *httptest.Server
	requests atomic.Int32
	check    func(w http.ResponseWriter, r *http.Request)
	reserve  func(w http.ResponseWriter, r *http.Request)
	get      func(w http.ResponseWriter, r *http.Request)
	release  func(w http.ResponseWriter, r *http.Request)
	verify   func(w http.ResponseWriter, r *http.Request)
	resend   func(w http.ResponseWriter, r *http.Request)
	choose   func(w http.ResponseWriter, r *http.Request)
	cancel   func(w http.ResponseWriter, r *http.Request)
}

func newYourAgentFakeServer(t *testing.T) *yourAgentFakeServer {
	t.Helper()
	fake := &yourAgentFakeServer{}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.requests.Add(1)
		switch r.URL.Path {
		case "/check":
			yourAgentFakeRoute(w, r, fake.check)
		case "/reserve":
			yourAgentFakeRoute(w, r, fake.reserve)
		case "/reservation":
			yourAgentFakeRoute(w, r, fake.get)
		case "/release":
			yourAgentFakeRoute(w, r, fake.release)
		case "/verify":
			yourAgentFakeRoute(w, r, fake.verify)
		case "/resend":
			yourAgentFakeRoute(w, r, fake.resend)
		case "/choose":
			yourAgentFakeRoute(w, r, fake.choose)
		case "/cancel":
			yourAgentFakeRoute(w, r, fake.cancel)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func yourAgentFakeRoute(w http.ResponseWriter, r *http.Request, handler func(http.ResponseWriter, *http.Request)) {
	if handler == nil {
		http.Error(w, "no handler", http.StatusInternalServerError)
		return
	}
	handler(w, r)
}

func yourAgentWriteJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// yourAgentTestEnv returns an environment with temp state, a fixed clock and
// fixed entries. The client points at the fake server, never at isoform.ai.
func yourAgentTestEnv(t *testing.T, baseURL, fullName string, entries []KnowledgeEntry) yourAgentEnv {
	t.Helper()
	dir := t.TempDir()
	return yourAgentEnv{
		stateFile:   filepath.Join(dir, yourAgentStateFileName),
		client:      newYourAgentClient(baseURL),
		fullName:    fullName,
		lang:        "en",
		now:         func() time.Time { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) },
		fingerprint: func() (string, error) { return "fp-1", nil },
		entries:     func() ([]KnowledgeEntry, error) { return entries, nil },
		hubFallback: func() string { return "" },
	}
}

// yourAgentTestEntry builds a know-how entry. sessionPrefix makes its session
// ids distinct unless the test shares them on purpose.
func yourAgentTestEntry(id, title, apps string, sessions []string, takeaway string) KnowledgeEntry {
	content := "## Description\n" + takeaway + "\n\n## Key Insights\n- a point\n"
	return KnowledgeEntry{
		ID:         id,
		Title:      title,
		Apps:       apps,
		Date:       "2026-10-20",
		SessionIDs: sessions,
		Content:    content,
	}
}

func yourAgentSessions(prefix string, count int) []string {
	ids := make([]string, 0, count)
	for i := 0; i < count; i++ {
		ids = append(ids, fmt.Sprintf("%s-%d", prefix, i))
	}
	return ids
}

func TestYourAgentNameCandidatesFromFullName(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{"Alex Rivera", []string{"alex.rivera", "alexrivera", "a.rivera"}},
		{"José Núñez", []string{"jose.nunez", "josenunez", "j.nunez"}},
		{"Mary Ann Smith", []string{"mary.ann.smith", "maryannsmith", "m.smith"}},
		{"张伟", nil},
		{"", nil},
		{"Bo", []string{}},
	}
	for _, tc := range cases {
		got := yourAgentNameCandidates(tc.name)
		if len(got) != len(tc.want) {
			t.Fatalf("candidates(%q) = %v, want %v", tc.name, got, tc.want)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatalf("candidates(%q) = %v, want %v", tc.name, got, tc.want)
			}
		}
	}
}

func TestYourAgentNameRulesAndReservedList(t *testing.T) {
	valid := []string{"abc", "alex.rivera", "a.b.c", "x1.2y", strings.Repeat("a", 30)}
	for _, name := range valid {
		if !yourAgentNameValid(name) {
			t.Errorf("%q should be a valid page name", name)
		}
	}
	invalid := []string{"", "ab", ".abc", "abc.", "a..bc", "ab-c", "ab_c", "ab c", "Alex", strings.Repeat("a", 31), "é.abc"}
	for _, name := range invalid {
		if yourAgentNameValid(name) {
			t.Errorf("%q should be refused by the name pattern", name)
		}
	}
	for _, name := range []string{"isoform", "agents", "admin", "support", "www", "bo", "billing"} {
		if !yourAgentNameReserved(name) {
			t.Errorf("%q should be reserved", name)
		}
	}
	if yourAgentNameReserved("alex.rivera") {
		t.Error("a personal name must not be reserved")
	}
}

func TestYourAgentEmailFormOnly(t *testing.T) {
	if !yourAgentEmailValid("alex@example.com") {
		t.Error("a plain address should pass the form check")
	}
	for _, email := range []string{"alex", "alex@", "@example.com", "a b@example.com", "alex@example"} {
		if yourAgentEmailValid(email) {
			t.Errorf("%q should fail the form check", email)
		}
	}
	long := strings.Repeat("a", 250) + "@example.com"
	if yourAgentEmailValid(long) {
		t.Error("an address over 254 characters must fail")
	}
}

func TestYourAgentThingKeyMergesPluralAndKeepsWords(t *testing.T) {
	key, display := yourAgentThingKey("Weekly updates")
	keyBare, _ := yourAgentThingKey("Weekly update")
	if key != keyBare || key != "weekly update" {
		t.Fatalf("plural and singular should share a key: %q vs %q", key, keyBare)
	}
	if display != "Weekly updates" {
		t.Fatalf("display should keep the title's words, got %q", display)
	}
	key, display = yourAgentThingKey("The client SOW for Acme Corp and more")
	if key != "client sow acme" || display != "client SOW Acme" {
		t.Fatalf("stop words and the three word limit: %q / %q", key, display)
	}
	if key, _ := yourAgentThingKey("   "); key != "" {
		t.Fatalf("an empty title has no key, got %q", key)
	}
}

func TestYourAgentRequestCandidatesEndWithTypedName(t *testing.T) {
	got := yourAgentRequestCandidates("Alex Rivera", "alex.rivera")
	want := []string{"alex.rivera", "alexrivera", "a.rivera", "alex.rivera"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	// A name with no Latin letters skips the first three; the typed name still goes last.
	if got := yourAgentRequestCandidates("张伟", "yourname"); strings.Join(got, ",") != "yourname" {
		t.Fatalf("no Latin name: candidates = %v", got)
	}
	// An invalid typed name is not appended.
	if got := yourAgentRequestCandidates("Alex Rivera", "a"); len(got) != 3 {
		t.Fatalf("invalid typed name must not be a candidate: %v", got)
	}
}

func TestYourAgentSpecialtiesCountThresholdAndTopThree(t *testing.T) {
	entries := []KnowledgeEntry{
		yourAgentTestEntry("sow", "Client SOW", "Docs", yourAgentSessions("c", 7), "Put the price in the first paragraph of an SOW."),
		yourAgentTestEntry("weekly-a", "Weekly updates", "Docs", yourAgentSessions("w", 6), "Lead the weekly update with what changed."),
		yourAgentTestEntry("sows-b", "Client SOWs", "Docs", []string{"c-4", "c-5", "c-6", "x-1", "x-2"}, "Name one owner for every open item."),
		yourAgentTestEntry("weekly-b", "Weekly update", "Docs", []string{"w-6", "w-7", "w-8", "w-9"}, "Keep the weekly update under one page."),
		yourAgentTestEntry("hiring", "Hiring plans", "Docs", yourAgentSessions("h", 6), "Write the hiring plan by role."),
		yourAgentTestEntry("offsites", "Team offsites", "Docs", yourAgentSessions("t", 5), "Plan the offsite agenda early."),
		yourAgentTestEntry("four", "Four things", "Docs", yourAgentSessions("f", 4), "Do the four things in order."),
	}
	got := computeYourAgentInsights(entries, "Alex Rivera")
	if got.StillLearning {
		t.Fatal("qualified specialties must not show still learning")
	}
	if len(got.Specialties) != 3 {
		t.Fatalf("want the top three, got %+v", got.Specialties)
	}
	// Weekly updates: 10 distinct sessions (w-0..w-9). Client SOW: c-0..c-6 plus x-1, x-2 = 9.
	// Hiring plans: 6. Team offsites (5) is the fourth and is left out; Four things (4) is under the threshold.
	want := []YourAgentSpecialty{{"Weekly updates", 10}, {"Client SOW", 9}, {"Hiring plans", 6}}
	for i := range want {
		if got.Specialties[i] != want[i] {
			t.Fatalf("specialty %d = %+v, want %+v", i, got.Specialties[i], want[i])
		}
	}
}

func TestYourAgentSpecialtyThresholdIsFive(t *testing.T) {
	four := []KnowledgeEntry{yourAgentTestEntry("a", "Client SOW", "Docs", yourAgentSessions("c", 4), "Put the price first.")}
	if got := computeYourAgentInsights(four, ""); len(got.Specialties) != 0 || !got.StillLearning {
		t.Fatalf("four sightings must stay still learning: %+v", got)
	}
	five := []KnowledgeEntry{yourAgentTestEntry("a", "Client SOW", "Docs", yourAgentSessions("c", 5), "Put the price first.")}
	if got := computeYourAgentInsights(five, ""); len(got.Specialties) != 1 || got.Specialties[0].Seen != 5 {
		t.Fatalf("five sightings must qualify: %+v", got)
	}
}

func TestYourAgentExclusionsPersonalAndMailMessagesAndKeptOutApps(t *testing.T) {
	sessions := yourAgentSessions("s", 9)
	excluded := []KnowledgeEntry{
		yourAgentTestEntry("home", "Home budget review", "Numbers", sessions, "Check the budget monthly."),
		yourAgentTestEntry("family", "Family trip plans", "Notes", sessions, "Book the trip early."),
		yourAgentTestEntry("cjk", "家庭账单整理", "Notes", sessions, "整理每月账单。"),
		yourAgentTestEntry("mail", "Invoice replies", "Mail", sessions, "Reply to invoices in one pass."),
		yourAgentTestEntry("messages", "Client follow-ups", "Messages, Docs", sessions, "Follow up within a day."),
		yourAgentTestEntry("vault", "Vault exports", "1Password", sessions, "Export the vault without secrets."),
	}
	for _, entry := range excluded {
		if !yourAgentEntryExcluded(entry) {
			t.Errorf("entry %q (apps %q) must be excluded", entry.Title, entry.Apps)
		}
	}
	if got := computeYourAgentInsights(excluded, ""); len(got.Specialties) != 0 || !got.StillLearning {
		t.Fatalf("excluded patterns must not appear: %+v", got)
	}
	kept := yourAgentTestEntry("sow", "Client SOW", "Docs", sessions, "Put the price first.")
	if yourAgentEntryExcluded(kept) {
		t.Fatal("a work pattern in an ordinary app must stay")
	}
}

func TestYourAgentTasteRedactsNamesAndNumbers(t *testing.T) {
	got := yourAgentRedactTaste("Email alex@example.com about 2 invoices", "Alex Rivera")
	if strings.Contains(got, "@") || strings.ContainsAny(got, "0123456789") {
		t.Fatalf("email or digits survived: %q", got)
	}
	if !strings.Contains(got, "about invoices") {
		t.Fatalf("the usable words should stay: %q", got)
	}
	name := yourAgentRedactTaste("Send Alex Rivera the 2019 numbers before the weekly call", "Alex Rivera")
	if name != "Send the numbers before the weekly call" {
		t.Fatalf("name and year should be stripped, got %q", name)
	}
	if short := yourAgentRedactTaste("Too short", ""); short != "" {
		t.Fatalf("a line under the minimum length is dropped, got %q", short)
	}
}

func TestYourAgentTasteFromTopSpecialtyThenNextSpecialties(t *testing.T) {
	weeklyA := yourAgentTestEntry("wa", "Weekly updates", "Docs", yourAgentSessions("w", 6), "Lead the weekly update with what changed, not what happened.")
	weeklyB := yourAgentTestEntry("wb", "Weekly update", "Docs", []string{"w-6", "w-7", "w-8", "w-9"}, "Send Alex Rivera the 2019 numbers before the weekly call")
	sow := yourAgentTestEntry("sow", "Client SOW", "Docs", yourAgentSessions("c", 7), "Put the price and the date in the first paragraph of an SOW.")
	sow2 := yourAgentTestEntry("sow2", "Client SOW", "Docs", []string{"c-0"}, "Name one owner for every open item.")
	got := computeYourAgentInsights([]KnowledgeEntry{weeklyA, weeklyB, sow, sow2}, "Alex Rivera")
	want := []string{
		"Lead the weekly update with what changed, not what happened.",
		"Send the numbers before the weekly call",
		"Put the price and the date in the first paragraph of an SOW.",
	}
	if len(got.Taste) != len(want) {
		t.Fatalf("taste = %q, want %q", got.Taste, want)
	}
	for i := range want {
		if got.Taste[i] != want[i] {
			t.Fatalf("taste %d = %q, want %q", i, got.Taste[i], want[i])
		}
	}
}

func TestYourAgentStillLearningHidesTaste(t *testing.T) {
	entries := []KnowledgeEntry{yourAgentTestEntry("a", "Client SOW", "Docs", yourAgentSessions("c", 3), "Put the price first in the SOW.")}
	got := computeYourAgentInsights(entries, "")
	if !got.StillLearning || len(got.Specialties) != 0 || len(got.Taste) != 0 {
		t.Fatalf("know-how without five sightings must be still learning: %+v", got)
	}
}

func TestYourAgentStateFileModeAndStableToken(t *testing.T) {
	hub := uuid.NewString()
	env := yourAgentTestEnv(t, "http://127.0.0.1:1", "", nil)
	env.hubFallback = func() string { return hub }
	first, err := loadYourAgentState(env)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(env.stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode = %o, want 0600", info.Mode().Perm())
	}
	raw, err := base64.RawURLEncoding.DecodeString(first.Token)
	if err != nil || len(raw) != 32 {
		t.Fatalf("token must be base64url of 32 bytes: %v (%d bytes)", err, len(raw))
	}
	if first.Hub != hub {
		t.Fatalf("hub = %q, want the mobile hub id %q", first.Hub, hub)
	}
	second, err := loadYourAgentState(env)
	if err != nil {
		t.Fatal(err)
	}
	if second.Token != first.Token {
		t.Fatal("the token must be created once and kept")
	}
}

func TestYourAgentStateWithoutMobileIdentityGetsOwnHub(t *testing.T) {
	env := yourAgentTestEnv(t, "http://127.0.0.1:1", "", nil)
	state, err := loadYourAgentState(env)
	if err != nil {
		t.Fatal(err)
	}
	if !mobileValidUUID(state.Hub) {
		t.Fatalf("hub %q is not a UUID", state.Hub)
	}
}

func TestYourAgentInsightsCacheRecomputesOnlyWhenNeeded(t *testing.T) {
	entries := []KnowledgeEntry{yourAgentTestEntry("a", "Client SOW", "Docs", yourAgentSessions("c", 5), "Put the price first in the SOW.")}
	env := yourAgentTestEnv(t, "http://127.0.0.1:1", "", nil)
	var reads atomic.Int32
	fingerprint := "fp-1"
	day := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	env.fingerprint = func() (string, error) { return fingerprint, nil }
	env.entries = func() ([]KnowledgeEntry, error) {
		reads.Add(1)
		return entries, nil
	}
	env.now = func() time.Time { return day }

	if err := refreshYourAgentInsights(env); err != nil {
		t.Fatal(err)
	}
	if err := refreshYourAgentInsights(env); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 1 {
		t.Fatalf("same day and store must read once, read %d times", reads.Load())
	}
	fingerprint = "fp-2"
	if err := refreshYourAgentInsights(env); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 2 {
		t.Fatalf("a changed How you do things must recompute, reads = %d", reads.Load())
	}
	day = day.Add(24 * time.Hour)
	if err := refreshYourAgentInsights(env); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 3 {
		t.Fatalf("a new day must recompute, reads = %d", reads.Load())
	}
	state, err := loadYourAgentState(env)
	if err != nil {
		t.Fatal(err)
	}
	if state.Insights == nil || state.Insights.Day != "2026-10-28" || state.Insights.Fingerprint != "fp-2" {
		t.Fatalf("snapshot not saved with its day and fingerprint: %+v", state.Insights)
	}
}

func TestYourAgentCheckAnswersFromServer(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	fake.check = func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		// Full-name form, no-dots form, initial and last, then the typed name (v2.0.0).
		if body["name"] != "alex.rivera" || len(body["candidates"].([]any)) != 4 || body["candidates"].([]any)[3] != "alex.rivera" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if _, hasHub := body["hub"]; hasHub {
			http.Error(w, "hub must not be sent before a reservation", http.StatusBadRequest)
			return
		}
		yourAgentWriteJSON(w, http.StatusOK, `{"name":"alex.rivera","available":false,"reason":"taken","suggestion":"alexrivera"}`)
	}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	result, err := yourAgentCheck(context.Background(), env, "Alex.Rivera ")
	if err != nil {
		t.Fatal(err)
	}
	if result.Reason != "taken" || result.Suggestion != "alexrivera" || result.Available {
		t.Fatalf("check result = %+v", result)
	}
	local, err := yourAgentCheck(context.Background(), env, "isoform")
	if err != nil || local.Reason != "not_allowed" {
		t.Fatalf("a reserved name is decided on the Mac: %+v %v", local, err)
	}
	local, _ = yourAgentCheck(context.Background(), env, "a")
	if local.Reason != "invalid" {
		t.Fatalf("a malformed name is decided on the Mac: %+v", local)
	}
}

func TestYourAgentClientCheckEveryContractAnswer(t *testing.T) {
	answers := map[string]string{
		"available":   `{"name":"alex.rivera","available":true}`,
		"taken":       `{"name":"alex.rivera","available":false,"reason":"taken","suggestion":"alexrivera"}`,
		"not_allowed": `{"name":"support.team","available":false,"reason":"not_allowed"}`,
		"invalid":     `{"name":"ab","available":false,"reason":"invalid"}`,
	}
	fake := newYourAgentFakeServer(t)
	current := ""
	fake.check = func(w http.ResponseWriter, r *http.Request) { yourAgentWriteJSON(w, http.StatusOK, answers[current]) }
	client := newYourAgentClient(fake.server.URL)
	for _, key := range []string{"available", "taken", "not_allowed", "invalid"} {
		current = key
		got, err := client.check(context.Background(), yourAgentCheckRequest{Name: "alex.rivera", Candidates: []string{"alex.rivera"}})
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if key == "available" && !got.Available {
			t.Fatalf("available answer lost: %+v", got)
		}
		if key != "available" && got.Reason == "" {
			t.Fatalf("%s answer lost its reason: %+v", key, got)
		}
	}
	fake.check = func(w http.ResponseWriter, r *http.Request) {
		yourAgentWriteJSON(w, http.StatusForbidden, `{"error":"token"}`)
	}
	if _, err := client.check(context.Background(), yourAgentCheckRequest{Name: "alex.rivera"}); !isYourAgentAPIStatus(err, 403) {
		t.Fatalf("a 403 check must be an API error, got %v", err)
	}
	fake.check = func(w http.ResponseWriter, r *http.Request) {
		yourAgentWriteJSON(w, http.StatusTooManyRequests, `{"error":"rate"}`)
	}
	if _, err := client.check(context.Background(), yourAgentCheckRequest{Name: "alex.rivera"}); !isYourAgentAPIStatus(err, 429) {
		t.Fatalf("a 429 check must be an API error, got %v", err)
	}
}

func isYourAgentAPIStatus(err error, status int) bool {
	var apiErr *yourAgentAPIError
	return errors.As(err, &apiErr) && apiErr.Status == status
}

func TestYourAgentReserveStatusMapping(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantStatus string
		wantSugg   string
	}{
		{"ok", http.StatusOK, `{"name":"alex.rivera","email":"alex@example.com","reservedAt":"2026-10-27T12:00:00Z"}`, "reserved", ""},
		{"taken", http.StatusConflict, `{"error":"taken","suggestion":"alexrivera"}`, "taken", "alexrivera"},
		{"invalid name", http.StatusUnprocessableEntity, `{"error":"invalid_name"}`, "invalid_name", ""},
		{"not allowed", http.StatusUnprocessableEntity, `{"error":"not_allowed"}`, "not_allowed", ""},
		{"invalid email", http.StatusUnprocessableEntity, `{"error":"invalid_email"}`, "invalid_email", ""},
		{"token", http.StatusForbidden, `{"error":"token"}`, "token", ""},
		{"rate", http.StatusTooManyRequests, `{"error":"rate"}`, "rate", ""},
		{"server error", http.StatusInternalServerError, `{}`, "offline", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newYourAgentFakeServer(t)
			fake.reserve = func(w http.ResponseWriter, r *http.Request) { yourAgentWriteJSON(w, tc.status, tc.body) }
			env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
			result, err := yourAgentReserve(context.Background(), env, "alex.rivera", "alex@example.com")
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != tc.wantStatus || result.Suggestion != tc.wantSugg {
				t.Fatalf("result = %+v, want status %q suggestion %q", result, tc.wantStatus, tc.wantSugg)
			}
		})
	}
}

func TestYourAgentReserveNetworkDownIsOffline(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	fake.server.Close()
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	result, err := yourAgentReserve(context.Background(), env, "alex.rivera", "alex@example.com")
	if err != nil || result.Status != "offline" {
		t.Fatalf("a closed server must be offline: %+v %v", result, err)
	}
	if reservation, err := env.client.reservation(context.Background(), "hub", "token"); reservation != nil || !errors.Is(err, errYourAgentOffline) {
		t.Fatalf("reservation read when down: %v %v", reservation, err)
	}
}

func TestYourAgentClientTimeoutIsOffline(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	fake.reserve = func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	started := time.Now()
	result, err := yourAgentReserve(context.Background(), env, "alex.rivera", "alex@example.com")
	if err != nil || result.Status != "offline" {
		t.Fatalf("a slow server must read as offline: %+v %v", result, err)
	}
	if elapsed := time.Since(started); elapsed > 4500*time.Millisecond {
		t.Fatalf("the call must give up at 3 seconds, took %v", elapsed)
	}
}

func TestYourAgentReserveChangeReleaseAndLocalState(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	var lastReserve map[string]any
	fake.reserve = func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&lastReserve)
		yourAgentWriteJSON(w, http.StatusOK, `{"name":"alex.rivera","email":"alex@example.com","reservedAt":"2026-10-27T12:00:00Z"}`)
	}
	fake.get = func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			yourAgentWriteJSON(w, http.StatusForbidden, `{"error":"token"}`)
			return
		}
		yourAgentWriteJSON(w, http.StatusOK, `{"name":"alex.rivera","email":"alex@example.com","reservedAt":"2026-10-27T12:00:00Z"}`)
	}
	fake.release = func(w http.ResponseWriter, r *http.Request) { yourAgentWriteJSON(w, http.StatusOK, `{}`) }
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)

	result, err := yourAgentReserve(context.Background(), env, "Alex.Rivera", " alex@example.com ")
	if err != nil || result.Status != "reserved" || result.Reservation == nil {
		t.Fatalf("reserve: %+v %v", result, err)
	}
	state, err := loadYourAgentState(env)
	if err != nil {
		t.Fatal(err)
	}
	if state.Reservation == nil || state.Reservation.Name != "alex.rivera" {
		t.Fatalf("reservation not kept locally: %+v", state.Reservation)
	}
	if lastReserve["lang"] != "en" || lastReserve["token"] != state.Token || lastReserve["hub"] != state.Hub {
		t.Fatalf("reserve body = %v", lastReserve)
	}
	if lastReserve["email"] != "alex@example.com" || lastReserve["name"] != "alex.rivera" {
		t.Fatalf("typed values must be trimmed and lowercased: %v", lastReserve)
	}
	page, err := yourAgentLoadPage(context.Background(), env)
	if err != nil || page.Reservation == nil || !page.Online {
		t.Fatalf("page after reserve: %+v %v", page, err)
	}
	released, err := yourAgentRelease(context.Background(), env)
	if err != nil || released.Status != "released" {
		t.Fatalf("release: %+v %v", released, err)
	}
	state, _ = loadYourAgentState(env)
	if state.Reservation != nil {
		t.Fatal("a released name must be cleared locally")
	}
}

func TestYourAgentLocalRejectsNeverReachServer(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	cases := map[string][2]string{
		"invalid name":  {"a", "alex@example.com"},
		"reserved name": {"isoform", "alex@example.com"},
		"bad email":     {"alex.rivera", "alex"},
	}
	for label, input := range cases {
		result, err := yourAgentReserve(context.Background(), env, input[0], input[1])
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if result.Status == "reserved" || result.Status == "" {
			t.Fatalf("%s: %+v", label, result)
		}
	}
	if fake.requests.Load() != 0 {
		t.Fatalf("local rejects must not reach the server, requests = %d", fake.requests.Load())
	}
}

func TestYourAgentPageOfflineKeepsCachedReservation(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	if _, err := updateYourAgentState(env, func(state *yourAgentStateFile) {
		state.Reservation = &YourAgentReservation{Name: "alex.rivera", Email: "alex@example.com", ReservedAt: "2026-10-27T12:00:00Z"}
	}); err != nil {
		t.Fatal(err)
	}
	fake.server.Close()
	page, err := yourAgentLoadPage(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if page.Online || page.Reservation == nil || page.Reservation.Name != "alex.rivera" {
		t.Fatalf("offline page must keep the cached reservation: %+v", page)
	}
	if got := page.NameCandidates; len(got) != 3 || got[0] != "alex.rivera" {
		t.Fatalf("name candidates = %v", got)
	}
}

func TestYourAgentRouteScopeAndShape(t *testing.T) {
	bridge := &mobileBridge{}
	path := mobileAPIPrefix + "/your-agent"

	denied := httptest.NewRecorder()
	bridge.handleAuthorized(denied, httptest.NewRequest(http.MethodGet, path, nil), mobileTestDevice())
	if denied.Code != http.StatusForbidden {
		t.Fatalf("a phone without brief:read must get 403, got %d", denied.Code)
	}

	allowed := httptest.NewRecorder()
	bridge.handleAuthorized(allowed, httptest.NewRequest(http.MethodGet, path, nil), mobileTestDevice(mobileScopeBriefRead))
	if allowed.Code == http.StatusForbidden || allowed.Code == http.StatusNotFound {
		t.Fatalf("brief:read must reach the route, got %d", allowed.Code)
	}

	entries := []KnowledgeEntry{yourAgentTestEntry("a", "Client SOW", "Docs", yourAgentSessions("c", 5), "Put the price first in the SOW.")}
	fake := newYourAgentFakeServer(t)
	fake.get = func(w http.ResponseWriter, r *http.Request) {
		yourAgentWriteJSON(w, http.StatusNotFound, `{"error":"none"}`)
	}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", entries)
	if err := refreshYourAgentInsights(env); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	serveYourAgentView(recorder, env)
	if recorder.Code != http.StatusOK {
		t.Fatalf("route status = %d", recorder.Code)
	}
	var envelope struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.OK {
		t.Fatal("route envelope must be ok")
	}
	for _, key := range []string{"specialties", "taste", "stillLearning", "nameCandidates", "hub", "token", "reservation"} {
		if _, ok := envelope.Data[key]; !ok {
			t.Fatalf("route body is missing %q: %v", key, envelope.Data)
		}
	}
	if envelope.Data["reservation"] != nil {
		t.Fatalf("404 from the server means no reservation: %v", envelope.Data["reservation"])
	}
	specialties, _ := envelope.Data["specialties"].([]any)
	if len(specialties) != 1 {
		t.Fatalf("specialties = %v", envelope.Data["specialties"])
	}
	if first, _ := specialties[0].(map[string]any); first["name"] != "Client SOW" || first["seen"] != float64(5) {
		t.Fatalf("specialty shape = %v", first)
	}
	if token, _ := envelope.Data["token"].(string); len(token) != 43 {
		t.Fatalf("token must travel in the sealed body, got %q", token)
	}
}

// The first paint reads only the cache: with the reservation route hanging,
// the cached page must still come back at once.
func TestYourAgentFirstPaintDoesNotWaitForNetwork(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) }) // runs before the server closes
	fake.get = func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}
	entries := []KnowledgeEntry{yourAgentTestEntry("a", "Client SOW", "Docs", yourAgentSessions("c", 5), "Put the price first in the SOW.")}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", entries)
	if err := refreshYourAgentInsights(env); err != nil {
		t.Fatal(err)
	}
	if _, err := updateYourAgentState(env, func(state *yourAgentStateFile) {
		state.Reservation = &YourAgentReservation{Name: "alex.rivera", Email: "alex@example.com", ReservedAt: "2026-10-27T12:00:00Z"}
	}); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	page, err := yourAgentCachedPage(env)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("the first paint waited %v on the network", elapsed)
	}
	if len(page.Specialties) != 1 || page.Reservation == nil || page.Reservation.Name != "alex.rivera" {
		t.Fatalf("first paint must show the cached data: %+v", page)
	}
}

func TestYourAgentReservationReadOfflineKeepsCache(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	if _, err := updateYourAgentState(env, func(state *yourAgentStateFile) {
		state.Reservation = &YourAgentReservation{Name: "alex.rivera", Email: "alex@example.com", ReservedAt: "2026-10-27T12:00:00Z"}
	}); err != nil {
		t.Fatal(err)
	}
	fake.server.Close()
	read, err := yourAgentReadReservation(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if read.Online || read.Reservation == nil || read.Reservation.Name != "alex.rivera" {
		t.Fatalf("offline read must keep the cache and report offline: %+v", read)
	}
}

func TestYourAgentRateLimitNamesTheServersRetryTime(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	fake.reserve = func(w http.ResponseWriter, r *http.Request) {
		yourAgentWriteJSON(w, http.StatusTooManyRequests, `{"error":"rate","retryAt":"2026-10-27T21:40:00Z"}`)
	}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	result, err := yourAgentReserve(context.Background(), env, "alex.rivera", "alex@example.com")
	if err != nil || result.Status != "rate" || result.RetryAt != "2026-10-27T21:40:00Z" {
		t.Fatalf("rate result = %+v %v", result, err)
	}
}

func TestYourAgentRateLimitWithoutRetryAtUsesNextUTCHour(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	fake.reserve = func(w http.ResponseWriter, r *http.Request) {
		yourAgentWriteJSON(w, http.StatusTooManyRequests, `{"error":"rate"}`)
	}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	result, err := yourAgentReserve(context.Background(), env, "alex.rivera", "alex@example.com")
	if err != nil || result.Status != "rate" {
		t.Fatalf("rate result = %+v %v", result, err)
	}
	retryAt, err := time.Parse(time.RFC3339, result.RetryAt)
	if err != nil {
		t.Fatalf("retryAt must be RFC 3339: %v", err)
	}
	if retryAt.Minute() != 0 || retryAt.Second() != 0 || !retryAt.After(time.Now()) || retryAt.Sub(time.Now()) > 61*time.Minute {
		t.Fatalf("retryAt = %v, want the start of the next UTC hour", retryAt)
	}
}

func TestYourAgentNextUTCHourIsTheStartOfTheNextHour(t *testing.T) {
	got := yourAgentNextUTCHour(time.Date(2026, 10, 27, 21, 40, 5, 0, time.UTC))
	want := time.Date(2026, 10, 27, 22, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("next UTC hour = %v, want %v", got, want)
	}
}

// A refused token is re-read once and the request tried again, silently.
func TestYourAgentRefusedTokenRetriesOnceSilently(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	var calls atomic.Int32
	fake.reserve = func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			yourAgentWriteJSON(w, http.StatusForbidden, `{"error":"token"}`)
			return
		}
		yourAgentWriteJSON(w, http.StatusOK, `{"name":"alex.rivera","email":"alex@example.com","reservedAt":"2026-10-27T12:00:00Z"}`)
	}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	result, err := yourAgentReserve(context.Background(), env, "alex.rivera", "alex@example.com")
	if err != nil || result.Status != "reserved" {
		t.Fatalf("after one silent retry the request must succeed: %+v %v", result, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("reserve calls = %d, want 2", calls.Load())
	}
}

func TestYourAgentRefusedTokenTwiceShowsTheTokenStatus(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	var calls atomic.Int32
	fake.reserve = func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		yourAgentWriteJSON(w, http.StatusForbidden, `{"error":"token"}`)
	}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	result, err := yourAgentReserve(context.Background(), env, "alex.rivera", "alex@example.com")
	if err != nil || result.Status != "token" || calls.Load() != 2 {
		t.Fatalf("two refusals must show token after exactly two calls: %+v %v calls=%d", result, err, calls.Load())
	}
}

// Before the first compute the page says nothing about specialties: no
// Still learning claim, and Computed is false so the page shows placeholders.
func TestYourAgentComputedFlagFollowsTheFirstCompute(t *testing.T) {
	entries := []KnowledgeEntry{yourAgentTestEntry("a", "Client SOW", "Docs", yourAgentSessions("c", 5), "Put the price first in the SOW.")}
	env := yourAgentTestEnv(t, "http://127.0.0.1:1", "Alex Rivera", entries)
	before, err := yourAgentCachedPage(env)
	if err != nil {
		t.Fatal(err)
	}
	if before.Computed || before.StillLearning || len(before.Specialties) != 0 {
		t.Fatalf("before the first compute: %+v", before)
	}
	if err := refreshYourAgentInsights(env); err != nil {
		t.Fatal(err)
	}
	after, err := yourAgentCachedPage(env)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Computed || len(after.Specialties) != 1 {
		t.Fatalf("after the first compute: %+v", after)
	}
}

func TestYourAgentReserveFlowRejectsBadToken(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	fake.reserve = func(w http.ResponseWriter, r *http.Request) {
		yourAgentWriteJSON(w, http.StatusForbidden, `{"error":"token"}`)
	}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	result, err := yourAgentReserve(context.Background(), env, "alex.rivera", "alex@example.com")
	if err != nil || result.Status != "token" {
		t.Fatalf("a token mismatch must report token: %+v %v", result, err)
	}
	state, _ := loadYourAgentState(env)
	if state.Reservation != nil {
		t.Fatal("a refused reserve must not store a reservation")
	}
}

// Bodies the fake server answers with for the code flow.
const (
	yourAgentPendingBody  = `{"name":"alex.rivera","email":"alex@example.com","reservedAt":"2026-10-27T12:00:00Z","status":"pending","expiresAt":"2026-10-27T12:10:00Z"}`
	yourAgentVerifiedBody = `{"name":"alex.rivera","email":"alex@example.com","reservedAt":"2026-10-27T12:00:00Z","status":"verified","place":214}`
	yourAgentChoiceBody   = `{"name":"alex.rivera","email":"alex@example.com","reservedAt":"2026-10-27T12:00:00Z","status":"choice","expiresAt":"2026-10-27T12:10:00Z","oldName":"arivera"}`
	yourAgentOriginalBody = `{"name":"arivera","email":"alex@example.com","reservedAt":"2026-10-20T12:00:00Z","status":"verified","place":40}`
)

// yourAgentSeedReservation writes a reservation into the state file, as an
// earlier request on this Mac would have.
func yourAgentSeedReservation(t *testing.T, env yourAgentEnv, reservation *YourAgentReservation) {
	t.Helper()
	if _, err := updateYourAgentState(env, func(state *yourAgentStateFile) {
		state.Reservation = reservation
	}); err != nil {
		t.Fatal(err)
	}
}

func yourAgentStoredReservation(t *testing.T, env yourAgentEnv) *YourAgentReservation {
	t.Helper()
	state, err := loadYourAgentState(env)
	if err != nil {
		t.Fatal(err)
	}
	return state.Reservation
}

func yourAgentSeedPending(t *testing.T, env yourAgentEnv) {
	t.Helper()
	yourAgentSeedReservation(t, env, &YourAgentReservation{
		Name: "alex.rivera", Email: "alex@example.com", ReservedAt: "2026-10-27T12:00:00Z",
		Status: "pending", ExpiresAt: "2026-10-27T12:10:00Z",
	})
}

func TestYourAgentReserveSendsMacAndKeepsPending(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	var sent map[string]any
	fake.reserve = func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
		yourAgentWriteJSON(w, http.StatusOK, yourAgentPendingBody)
	}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	result, err := yourAgentReserve(context.Background(), env, "alex.rivera", "alex@example.com")
	if err != nil || result.Status != "pending" || result.Reservation == nil || result.Reservation.ExpiresAt != "2026-10-27T12:10:00Z" {
		t.Fatalf("pending reserve = %+v %v", result, err)
	}
	if sent["client"] != "mac" {
		t.Fatalf("reserve must say client mac, sent %v", sent["client"])
	}
	if stored := yourAgentStoredReservation(t, env); stored == nil || stored.Status != "pending" {
		t.Fatalf("a pending answer must be kept on the Mac: %+v", stored)
	}
}

// A reservation from before the status field has no status: it reads as verified.
func TestYourAgentMissingStatusMeansVerified(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	fake.reserve = func(w http.ResponseWriter, r *http.Request) {
		yourAgentWriteJSON(w, http.StatusOK, `{"name":"alex.rivera","email":"alex@example.com","reservedAt":"2026-10-27T12:00:00Z"}`)
	}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	result, err := yourAgentReserve(context.Background(), env, "alex.rivera", "alex@example.com")
	if err != nil || result.Status != "reserved" || result.Reservation == nil || result.Reservation.Status != "verified" {
		t.Fatalf("an answer without status must be verified: %+v %v", result, err)
	}
	if err := os.WriteFile(env.stateFile, []byte(`{"version":1,"hub":"hub-1","token":"tok-1","reservation":{"name":"alex.rivera","email":"alex@example.com","reservedAt":"2026-10-27T12:00:00Z"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if stored := yourAgentStoredReservation(t, env); stored == nil || stored.Status != "verified" {
		t.Fatalf("a state file from before the status field must read as verified: %+v", stored)
	}
}

func TestYourAgentVerifyEveryAnswer(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantStatus string
		wantSugg   string
		wantStored string
	}{
		{"verified with place", http.StatusOK, yourAgentVerifiedBody, "reserved", "", "verified"},
		{"choice", http.StatusOK, yourAgentChoiceBody, "choice", "", "choice"},
		{"wrong code", http.StatusBadRequest, `{"error":"wrong_code"}`, "wrong_code", "", "pending"},
		{"expired", http.StatusGone, `{"error":"expired"}`, "expired", "", "pending"},
		{"taken lets the request go", http.StatusConflict, `{"error":"taken","suggestion":"alexrivera"}`, "taken", "alexrivera", ""},
		{"rate", http.StatusTooManyRequests, `{"error":"rate","retryAt":"2026-10-27T21:40:00Z"}`, "rate", "", "pending"},
		{"token refused", http.StatusForbidden, `{"error":"token"}`, "token", "", "pending"},
		{"server error", http.StatusInternalServerError, `{}`, "offline", "", "pending"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newYourAgentFakeServer(t)
			fake.verify = func(w http.ResponseWriter, r *http.Request) { yourAgentWriteJSON(w, tc.status, tc.body) }
			env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
			yourAgentSeedPending(t, env)
			result, err := yourAgentVerify(context.Background(), env, "123456")
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != tc.wantStatus || result.Suggestion != tc.wantSugg {
				t.Fatalf("result = %+v, want status %q suggestion %q", result, tc.wantStatus, tc.wantSugg)
			}
			stored := yourAgentStoredReservation(t, env)
			if tc.wantStored == "" {
				if stored != nil {
					t.Fatalf("a taken request must be let go on the Mac too: %+v", stored)
				}
				return
			}
			if stored == nil || stored.Status != tc.wantStored {
				t.Fatalf("stored = %+v, want status %q", stored, tc.wantStored)
			}
		})
	}
}

func TestYourAgentVerifySendsHubTokenAndCode(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	var sent map[string]any
	fake.verify = func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&sent)
		yourAgentWriteJSON(w, http.StatusOK, yourAgentVerifiedBody)
	}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	yourAgentSeedPending(t, env)
	state, err := loadYourAgentState(env)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := yourAgentVerify(context.Background(), env, "654321"); err != nil {
		t.Fatal(err)
	}
	if sent["code"] != "654321" || sent["hub"] != state.Hub || sent["token"] != state.Token {
		t.Fatalf("verify body = %v", sent)
	}
}

// A malformed code is refused on the Mac: the server never sees it.
func TestYourAgentVerifyRejectsMalformedCodeLocally(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	yourAgentSeedPending(t, env)
	for _, code := range []string{"", "12345", "1234567", "12345a", "12 456", "١٢٣٤٥٦"} {
		result, err := yourAgentVerify(context.Background(), env, code)
		if err != nil || result.Status != "wrong_code" {
			t.Fatalf("code %q: %+v %v", code, result, err)
		}
	}
	if fake.requests.Load() != 0 {
		t.Fatalf("malformed codes must not reach the server, requests = %d", fake.requests.Load())
	}
}

func TestYourAgentVerifyRefusedTokenRetriesOnce(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	var calls atomic.Int32
	fake.verify = func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			yourAgentWriteJSON(w, http.StatusForbidden, `{"error":"token"}`)
			return
		}
		yourAgentWriteJSON(w, http.StatusOK, yourAgentVerifiedBody)
	}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	yourAgentSeedPending(t, env)
	result, err := yourAgentVerify(context.Background(), env, "123456")
	if err != nil || result.Status != "reserved" || calls.Load() != 2 {
		t.Fatalf("one silent retry must reach verified: %+v %v calls=%d", result, err, calls.Load())
	}
}

func TestYourAgentResendEveryAnswer(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantStatus string
		wantSugg   string
		wantStored string
	}{
		{"new code out", http.StatusOK, yourAgentPendingBody, "pending", "", "pending"},
		{"unconfirmed with a code out", http.StatusOK, `{"name":"alex.rivera","email":"alex@example.com","reservedAt":"2026-10-27T12:00:00Z","status":"unconfirmed","expiresAt":"2026-10-27T12:10:00Z"}`, "pending", "", "unconfirmed"},
		{"nothing pending", http.StatusNotFound, `{"error":"none"}`, "none", "", ""},
		{"name let go", http.StatusConflict, `{"error":"taken","suggestion":"alexrivera"}`, "taken", "alexrivera", ""},
		{"nothing to confirm", http.StatusConflict, `{"error":"not_pending"}`, "not_pending", "", "pending"},
		{"rate", http.StatusTooManyRequests, `{"error":"rate"}`, "rate", "", "pending"},
		{"server error", http.StatusInternalServerError, `{}`, "offline", "", "pending"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newYourAgentFakeServer(t)
			fake.resend = func(w http.ResponseWriter, r *http.Request) { yourAgentWriteJSON(w, tc.status, tc.body) }
			env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
			yourAgentSeedPending(t, env)
			result, err := yourAgentResend(context.Background(), env)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != tc.wantStatus || result.Suggestion != tc.wantSugg {
				t.Fatalf("result = %+v, want status %q suggestion %q", result, tc.wantStatus, tc.wantSugg)
			}
			stored := yourAgentStoredReservation(t, env)
			if tc.wantStored == "" {
				if stored != nil {
					t.Fatalf("a request the server no longer holds must be forgotten: %+v", stored)
				}
				return
			}
			if stored == nil || stored.Status != tc.wantStored {
				t.Fatalf("stored = %+v, want status %q", stored, tc.wantStored)
			}
		})
	}
}

func TestYourAgentChooseKeepAndSwitch(t *testing.T) {
	for _, keep := range []bool{true, false} {
		fake := newYourAgentFakeServer(t)
		var sent map[string]any
		fake.choose = func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&sent)
			if keep {
				yourAgentWriteJSON(w, http.StatusOK, `{"name":"arivera","email":"alex@example.com","reservedAt":"2026-10-20T12:00:00Z","status":"verified","place":40}`)
				return
			}
			yourAgentWriteJSON(w, http.StatusOK, yourAgentVerifiedBody)
		}
		env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
		yourAgentSeedReservation(t, env, &YourAgentReservation{
			Name: "alex.rivera", Email: "alex@example.com", ReservedAt: "2026-10-27T12:00:00Z",
			Status: "choice", ExpiresAt: "2026-10-27T12:10:00Z", OldName: "arivera",
		})
		result, err := yourAgentChoose(context.Background(), env, keep)
		if err != nil || result.Status != "reserved" || result.Reservation == nil {
			t.Fatalf("keep=%v: %+v %v", keep, result, err)
		}
		wantName := "alex.rivera"
		if keep {
			wantName = "arivera"
		}
		if result.Reservation.Name != wantName || sent["keep"] != keep {
			t.Fatalf("keep=%v: name %q body %v", keep, result.Reservation.Name, sent)
		}
		if stored := yourAgentStoredReservation(t, env); stored == nil || stored.Name != wantName {
			t.Fatalf("keep=%v: stored %+v", keep, stored)
		}
	}
}

// A choice that ran out reads the server again: the verified original stays,
// and nothing left means the form comes back.
func TestYourAgentChooseExpiredReadsTheServer(t *testing.T) {
	choiceHeld := &YourAgentReservation{
		Name: "alex.rivera", Email: "alex@example.com", ReservedAt: "2026-10-27T12:00:00Z",
		Status: "choice", ExpiresAt: "2026-10-27T12:10:00Z", OldName: "arivera",
	}
	t.Run("original stays", func(t *testing.T) {
		fake := newYourAgentFakeServer(t)
		fake.choose = func(w http.ResponseWriter, r *http.Request) {
			yourAgentWriteJSON(w, http.StatusGone, `{"error":"expired"}`)
		}
		fake.get = func(w http.ResponseWriter, r *http.Request) {
			yourAgentWriteJSON(w, http.StatusOK, yourAgentOriginalBody)
		}
		env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
		yourAgentSeedReservation(t, env, choiceHeld)
		result, err := yourAgentChoose(context.Background(), env, true)
		if err != nil || result.Status != "expired" || result.Reservation == nil || result.Reservation.Name != "arivera" {
			t.Fatalf("expired choice with an original: %+v %v", result, err)
		}
		if stored := yourAgentStoredReservation(t, env); stored == nil || stored.Name != "arivera" {
			t.Fatalf("stored = %+v", stored)
		}
	})
	t.Run("nothing left", func(t *testing.T) {
		fake := newYourAgentFakeServer(t)
		fake.choose = func(w http.ResponseWriter, r *http.Request) {
			yourAgentWriteJSON(w, http.StatusGone, `{"error":"expired"}`)
		}
		fake.get = func(w http.ResponseWriter, r *http.Request) {
			yourAgentWriteJSON(w, http.StatusNotFound, `{"error":"none"}`)
		}
		env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
		yourAgentSeedReservation(t, env, choiceHeld)
		result, err := yourAgentChoose(context.Background(), env, false)
		if err != nil || result.Status != "expired" || result.Reservation != nil {
			t.Fatalf("expired choice with nothing left: %+v %v", result, err)
		}
		if stored := yourAgentStoredReservation(t, env); stored != nil {
			t.Fatalf("stored must be cleared: %+v", stored)
		}
	})
}

func TestYourAgentCancelKeepsTheVerifiedOriginal(t *testing.T) {
	t.Run("hold goes", func(t *testing.T) {
		fake := newYourAgentFakeServer(t)
		fake.cancel = func(w http.ResponseWriter, r *http.Request) {
			yourAgentWriteJSON(w, http.StatusOK, `{"reservation":null}`)
		}
		env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
		yourAgentSeedPending(t, env)
		result, err := yourAgentCancel(context.Background(), env)
		if err != nil || result.Status != "released" || result.Reservation != nil {
			t.Fatalf("cancel without an original: %+v %v", result, err)
		}
		if stored := yourAgentStoredReservation(t, env); stored != nil {
			t.Fatalf("stored = %+v", stored)
		}
	})
	t.Run("original stays", func(t *testing.T) {
		fake := newYourAgentFakeServer(t)
		fake.cancel = func(w http.ResponseWriter, r *http.Request) {
			yourAgentWriteJSON(w, http.StatusOK, `{"reservation":`+yourAgentOriginalBody+`}`)
		}
		env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
		yourAgentSeedPending(t, env)
		result, err := yourAgentCancel(context.Background(), env)
		if err != nil || result.Status != "reserved" || result.Reservation == nil || result.Reservation.Name != "arivera" {
			t.Fatalf("cancel with an original: %+v %v", result, err)
		}
		if stored := yourAgentStoredReservation(t, env); stored == nil || stored.Name != "arivera" {
			t.Fatalf("stored = %+v", stored)
		}
	})
}

// The server says none for a request this Mac held: it ran out, and the read
// says so, so the page can refill the form.
func TestYourAgentRanOutWhenTheServerSaysNone(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	fake.get = func(w http.ResponseWriter, r *http.Request) {
		yourAgentWriteJSON(w, http.StatusNotFound, `{"error":"none"}`)
	}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	yourAgentSeedPending(t, env)
	read, err := yourAgentReadReservation(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if !read.Online || read.Reservation != nil || read.RanOut == nil || read.RanOut.Name != "alex.rivera" || read.RanOut.Email != "alex@example.com" {
		t.Fatalf("ran out read = %+v", read)
	}
	if stored := yourAgentStoredReservation(t, env); stored != nil {
		t.Fatalf("a ran-out request must be let go on the Mac: %+v", stored)
	}
}

// The hold's expiresAt has passed while the server still answers with it.
func TestYourAgentRanOutWhenTheHoldPassed(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	fake.get = func(w http.ResponseWriter, r *http.Request) {
		yourAgentWriteJSON(w, http.StatusOK, `{"name":"alex.rivera","email":"alex@example.com","reservedAt":"2026-10-27T11:50:00Z","status":"pending","expiresAt":"2026-10-27T11:59:00Z"}`)
	}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	yourAgentSeedPending(t, env)
	read, err := yourAgentReadReservation(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if read.Reservation != nil || read.RanOut == nil || read.RanOut.Name != "alex.rivera" {
		t.Fatalf("a passed hold must run out: %+v", read)
	}
}

func TestYourAgentPendingHoldStaysBeforeItsExpiry(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	fake.get = func(w http.ResponseWriter, r *http.Request) {
		yourAgentWriteJSON(w, http.StatusOK, `{"name":"alex.rivera","email":"alex@example.com","reservedAt":"2026-10-27T11:55:00Z","status":"pending","expiresAt":"2026-10-27T12:05:00Z"}`)
	}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	yourAgentSeedPending(t, env)
	read, err := yourAgentReadReservation(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if read.RanOut != nil || read.Reservation == nil || read.Reservation.Status != "pending" {
		t.Fatalf("a live hold must stay: %+v", read)
	}
}

// A verified reservation the server no longer returns is not a ran-out code
// step: the read clears it as it always did, with nothing to refill.
func TestYourAgentVerifiedMissingIsNotRanOut(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	fake.get = func(w http.ResponseWriter, r *http.Request) {
		yourAgentWriteJSON(w, http.StatusNotFound, `{"error":"none"}`)
	}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	yourAgentSeedReservation(t, env, &YourAgentReservation{Name: "alex.rivera", Email: "alex@example.com", ReservedAt: "2026-10-27T12:00:00Z", Status: "verified"})
	read, err := yourAgentReadReservation(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if read.RanOut != nil {
		t.Fatalf("a verified request is not a ran-out one: %+v", read)
	}
}

func TestYourAgentRouteCarriesRanOut(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	fake.get = func(w http.ResponseWriter, r *http.Request) {
		yourAgentWriteJSON(w, http.StatusNotFound, `{"error":"none"}`)
	}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	yourAgentSeedPending(t, env)
	recorder := httptest.NewRecorder()
	serveYourAgentView(recorder, env)
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	ranOut, _ := envelope.Data["ranOut"].(map[string]any)
	if ranOut["name"] != "alex.rivera" || envelope.Data["reservation"] != nil {
		t.Fatalf("route after a run-out: ranOut=%v reservation=%v", envelope.Data["ranOut"], envelope.Data["reservation"])
	}
}

func TestYourAgentExpiredChangeKeepsVerifiedOriginal(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	fake.get = func(w http.ResponseWriter, r *http.Request) {
		yourAgentWriteJSON(w, http.StatusOK, yourAgentOriginalBody)
	}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	yourAgentSeedReservation(t, env, &YourAgentReservation{
		Name: "alex.rivera", Email: "new@example.com", Status: "pending", ExpiresAt: "2026-10-27T11:59:00Z",
	})
	read, err := yourAgentReadReservation(context.Background(), env)
	if err != nil || read.RanOut == nil || read.RanOut.Email != "new@example.com" || read.Reservation == nil || read.Reservation.Name != "arivera" {
		t.Fatalf("expired change should refill while retaining the original: %+v, %v", read, err)
	}
	if stored := yourAgentStoredReservation(t, env); stored == nil || stored.Name != "arivera" {
		t.Fatalf("verified original must remain stored: %+v", stored)
	}
}

func TestYourAgentResendOnAnotherDeviceRenewsExpiredCache(t *testing.T) {
	fake := newYourAgentFakeServer(t)
	fake.get = func(w http.ResponseWriter, r *http.Request) {
		yourAgentWriteJSON(w, http.StatusOK, yourAgentPendingBody)
	}
	env := yourAgentTestEnv(t, fake.server.URL, "Alex Rivera", nil)
	yourAgentSeedReservation(t, env, &YourAgentReservation{
		Name: "alex.rivera", Email: "alex@example.com", Status: "pending", ExpiresAt: "2026-10-27T11:59:00Z",
	})
	read, err := yourAgentReadReservation(context.Background(), env)
	if err != nil || read.RanOut != nil || read.Reservation == nil || read.Reservation.ExpiresAt != "2026-10-27T12:10:00Z" {
		t.Fatalf("server renewal must win over the old cached expiry: %+v, %v", read, err)
	}
}
