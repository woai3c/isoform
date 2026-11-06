package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wails-gui/internal/calendar"
	"wails-gui/internal/platform/activitymon"
	"wails-gui/storage"
)

// fakePeekDriver is a browser that shows the calendar as soon as the URL is
// opened and remembers what the peek did to it.
type fakePeekDriver struct {
	front       string
	browser     string
	opened      []string
	closed      []string
	activated   []string
	tree        *activitymon.AXNode
	openFails   bool
	neverShows  bool
	currentURL  string
	tabRef      string
	idleTime    time.Duration
	sleeps      time.Duration
	readBundles []string
	// onSleep lets a test change the world while the peek waits.
	onSleep func(d time.Duration)
}

func (d *fakePeekDriver) openURL(url string) bool {
	d.opened = append(d.opened, url)
	if d.openFails {
		return false
	}
	d.front = d.browser
	if !d.neverShows {
		d.currentURL = url
	}
	d.tabRef = "chromium:7:42"
	return true
}
func (d *fakePeekDriver) frontmostBundle() string      { return d.front }
func (d *fakePeekDriver) isBrowser(bundle string) bool { return bundle == d.browser }
func (d *fakePeekDriver) browserURL(bundle string) string {
	if bundle == d.browser {
		return d.currentURL
	}
	return ""
}
func (d *fakePeekDriver) activeTabRef(bundle string) string {
	if bundle == d.browser {
		return d.tabRef
	}
	return ""
}
func (d *fakePeekDriver) closeTabRef(bundle, ref, fragment string) bool {
	d.closed = append(d.closed, bundle+" "+ref+" "+fragment)
	d.currentURL = ""
	return true
}
func (d *fakePeekDriver) readTree(bundle string) *activitymon.AXNode {
	d.readBundles = append(d.readBundles, bundle)
	return d.tree
}
func (d *fakePeekDriver) activate(bundle string) bool {
	d.activated = append(d.activated, bundle)
	d.front = bundle
	return true
}
func (d *fakePeekDriver) idle() time.Duration    { return d.idleTime }
func (d *fakePeekDriver) defaultBrowser() string { return d.browser }
func (d *fakePeekDriver) sleep(delay time.Duration) {
	d.sleeps += delay
	if d.onSleep != nil {
		d.onSleep(delay)
	}
}

func googleWeekTree() *activitymon.AXNode {
	chip := func(label string) *activitymon.AXNode {
		return &activitymon.AXNode{Role: "AXButton", Description: label}
	}
	return &activitymon.AXNode{
		Role: "AXWindow",
		Children: []*activitymon.AXNode{{
			Role: "AXWebArea",
			Children: []*activitymon.AXNode{
				chip("10am to 10:30am, Standup, Work, Accepted, https://meet.google.com/abc-defg-hij, October 14, 2026"),
				chip("2pm to 3pm, Design review, Work, October 15, 2026"),
				chip("Create"),
				chip("2pm to 3pm, Design review, Work, October 15, 2026"),
			},
		}},
	}
}

// peekClock advances a fake clock by the driver's sleeps, so idle checks
// compare like they do in production.
type peekClock struct {
	now time.Time
}

func newPeekTestService(t *testing.T, driver *fakePeekDriver, start time.Time) (*calendarPeekService, *storage.Store, *peekClock) {
	t.Helper()
	st, err := storage.New(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	clock := &peekClock{now: start}
	driver.idleTime = time.Hour
	prev := driver.onSleep
	driver.onSleep = func(d time.Duration) {
		clock.now = clock.now.Add(d)
		// An idle user keeps getting more idle while the peek waits.
		driver.idleTime += d
		if prev != nil {
			prev(d)
		}
	}
	svc := &calendarPeekService{
		store:     st,
		driver:    driver,
		now:       func() time.Time { return clock.now },
		loc:       time.UTC,
		lease:     &computerUseLease{},
		notice:    func() bool { return true },
		failures:  map[string]int{},
		holdUntil: map[string]time.Time{},
	}
	return svc, st, clock
}

func TestCalendarPeekReadsClosesAndRestores(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	driver := &fakePeekDriver{front: "com.apple.dt.Xcode", browser: "com.google.Chrome", tree: googleWeekTree()}
	svc, st, _ := newPeekTestService(t, driver, now)
	var reads []string
	var readCounts []int
	suppressed := []bool{}
	svc.suppress = func(on bool) { suppressed = append(suppressed, on) }
	notices := 0
	svc.notice = func() bool { notices++; return true }
	svc.onRead = func(provider string, events int, at time.Time) {
		reads = append(reads, provider)
		readCounts = append(readCounts, events)
		if at.Before(now) {
			t.Errorf("read %s at %v, before the start %v", provider, at, now)
		}
	}

	res, err := svc.run(context.Background(), []string{"google"}, "idle")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Events != 2 || len(res.Providers) != 1 {
		t.Fatalf("result %+v", res)
	}
	if len(driver.opened) != 1 || driver.opened[0] != "https://calendar.google.com/calendar/u/0/r/week" {
		t.Fatalf("opened %v", driver.opened)
	}
	// The tab is closed by the identity captured right after it opened.
	if len(driver.closed) != 1 || driver.closed[0] != "com.google.Chrome chromium:7:42 calendar.google.com" {
		t.Fatalf("the opened tab must be closed by identity: %v", driver.closed)
	}
	if len(driver.activated) != 1 || driver.activated[0] != "com.apple.dt.Xcode" {
		t.Fatalf("the previous app must come back: %v", driver.activated)
	}
	if len(reads) != 1 || reads[0] != "google" || readCounts[0] != 2 {
		t.Fatalf("reads %v %v", reads, readCounts)
	}
	if len(suppressed) != 2 || !suppressed[0] || suppressed[1] {
		t.Fatalf("capture is paused for the run and resumed after: %v", suppressed)
	}
	if notices != 1 {
		t.Fatalf("the one-time notice shows before the first peek: %d", notices)
	}
	if svc.lease.Holder() != "" {
		t.Fatal("the lease is released after the run")
	}
	events, err := st.ListCalendarEvents(context.Background(), 0, now.Add(7*24*time.Hour).UnixMilli(), false)
	if err != nil || len(events) != 2 {
		t.Fatalf("stored %+v %v", events, err)
	}
	standup := events[0]
	if standup.Source != "web" || standup.Provider != "google" || standup.Title != "Standup" || standup.ConferenceHost != "Google Meet" {
		t.Fatalf("standup %+v", standup)
	}
	if standup.StartAt != time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("standup start %d", standup.StartAt)
	}
	// A peek records a read, never a sighting.
	if sources, _ := st.ListCalendarSources(context.Background()); len(sources) != 0 {
		t.Fatalf("a peek must not create a source: %+v", sources)
	}
	_ = st.UpsertCalendarSource(context.Background(), storage.CalendarSourceRow{ID: "web:google", Kind: "web", Provider: "google", DetectedBy: calendar.DetectedByHistory, FirstSeenAt: 1, LastSeenAt: 1})
	windows := loadCalendarPeekWindows(context.Background(), st)
	if len(windows) != 1 || !windows.contains(now) || windows.contains(now.Add(-time.Hour)) {
		t.Fatalf("peek window recorded: %+v", windows)
	}
	last, _ := st.GetUIState(context.Background(), calendarPeekLastKey)
	if last != now.UTC().Format(time.RFC3339) || !svc.lastPeek.Equal(now) {
		t.Fatalf("last peek %q %v", last, svc.lastPeek)
	}

	// Second read: the review is gone, the standup stays, the notice does
	// not repeat, and last_read_at moves while last_seen_at does not.
	driver.tree.Children[0].Children = driver.tree.Children[0].Children[:1]
	driver.front = "com.apple.dt.Xcode"
	if _, err := svc.run(context.Background(), []string{"google"}, "brief time"); err != nil {
		t.Fatalf("second run: %v", err)
	}
	live, _ := st.ListCalendarEvents(context.Background(), 0, now.Add(7*24*time.Hour).UnixMilli(), false)
	if len(live) != 1 || live[0].Title != "Standup" {
		t.Fatalf("after second read %+v", live)
	}
	if notices != 1 {
		t.Fatalf("notice shown once: %d", notices)
	}
	sources, _ := st.ListCalendarSources(context.Background())
	if len(sources) != 1 || sources[0].LastSeenAt != 1 || sources[0].LastReadAt == 0 {
		t.Fatalf("a read moves last_read_at only: %+v", sources)
	}

	// An unreadable page is an error and never deletes the week.
	driver.tree = nil
	driver.front = "com.apple.dt.Xcode"
	if _, err := svc.run(context.Background(), []string{"google"}, "idle"); err == nil {
		t.Fatal("an unreadable page is an error")
	}
	live, _ = st.ListCalendarEvents(context.Background(), 0, now.Add(7*24*time.Hour).UnixMilli(), false)
	if len(live) != 1 {
		t.Fatalf("an empty read must keep the stored week: %+v", live)
	}
	driver.tree = &activitymon.AXNode{Role: "AXWindow"}
	if _, err := svc.run(context.Background(), []string{"google"}, "idle"); err != nil {
		t.Fatalf("a page without chips is not an error: %v", err)
	}
	if live, _ = st.ListCalendarEvents(context.Background(), 0, now.Add(7*24*time.Hour).UnixMilli(), false); len(live) != 1 {
		t.Fatalf("a read with no chips must keep the stored week: %+v", live)
	}
}

func TestCalendarPeekStopsWhenTheUserTypes(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	driver := &fakePeekDriver{front: "com.tinyspeck.slackmacgap", browser: "com.google.Chrome", tree: googleWeekTree()}
	// The user starts typing during the settle wait: idle drops to zero.
	driver.onSleep = func(d time.Duration) {
		if d == calendarPeekSettle {
			driver.idleTime = 0
		}
	}
	svc, st, _ := newPeekTestService(t, driver, now)
	_, err := svc.run(context.Background(), []string{"google"}, "brief time")
	if err == nil || !strings.Contains(err.Error(), "user is active") {
		t.Fatalf("expected an interrupted run, got %v", err)
	}
	if len(driver.readBundles) != 0 {
		t.Fatal("nothing is read once the user is active")
	}
	if len(driver.closed) != 1 {
		t.Fatalf("the tab is closed on the way out: %v", driver.closed)
	}
	if events, _ := st.ListCalendarEvents(context.Background(), 0, now.Add(7*24*time.Hour).UnixMilli(), true); len(events) != 0 {
		t.Fatalf("nothing stored: %+v", events)
	}
	// An interrupted run is not a peek: it retries once the user is idle
	// again, after a short pause, instead of waiting two hours.
	if !svc.lastPeek.IsZero() {
		t.Fatal("an interrupted run must not count as a peek")
	}
	if svc.notBefore.IsZero() {
		t.Fatal("an interrupted run sets a short retry pause")
	}
	if last, _ := st.GetUIState(context.Background(), calendarPeekLastKey); last != "" {
		t.Fatalf("no last peek recorded: %q", last)
	}

	// Input before the browser is even opened stops the run first.
	driver2 := &fakePeekDriver{front: "com.tinyspeck.slackmacgap", browser: "com.google.Chrome", tree: googleWeekTree()}
	svc2, _, clock := newPeekTestService(t, driver2, now)
	driver2.idleTime = 0
	clock.now = now.Add(time.Second)
	if _, err := svc2.run(context.Background(), []string{"google"}, "idle"); err == nil || len(driver2.opened) != 0 {
		t.Fatalf("the browser must not open after input: %v %v", err, driver2.opened)
	}
}

func TestCalendarPeekGuardStopsARunMidway(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	driver := &fakePeekDriver{front: "com.apple.finder", browser: "com.google.Chrome", tree: googleWeekTree()}
	reason := ""
	driver.onSleep = func(d time.Duration) {
		if d == calendarPeekSettle {
			reason = "screen locked"
		}
	}
	svc, _, _ := newPeekTestService(t, driver, now)
	svc.guard = func() string { return reason }
	if _, err := svc.run(context.Background(), []string{"google"}, "idle"); err == nil || !strings.Contains(err.Error(), "screen locked") {
		t.Fatalf("a guard stops the run: %v", err)
	}
	if len(driver.readBundles) != 0 || len(driver.closed) != 1 {
		t.Fatalf("no read, tab closed: %v %v", driver.readBundles, driver.closed)
	}
	// cancelRun ends a run the same way.
	driver2 := &fakePeekDriver{front: "com.apple.finder", browser: "com.google.Chrome", tree: googleWeekTree()}
	svc2, _, _ := newPeekTestService(t, driver2, now)
	base := driver2.onSleep
	driver2.onSleep = func(d time.Duration) {
		base(d)
		svc2.cancelRun()
	}
	if _, err := svc2.run(context.Background(), []string{"google"}, "idle"); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("cancelRun stops the run: %v", err)
	}
}

func TestCalendarPeekGivesUpAndBacksOff(t *testing.T) {
	start := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	driver := &fakePeekDriver{front: "com.apple.Safari", browser: "com.apple.Safari", neverShows: true}
	svc, st, _ := newPeekTestService(t, driver, start)
	for i := 0; i < calendarPeekFailureLimit; i++ {
		driver.front = "com.apple.Safari"
		if _, err := svc.run(context.Background(), []string{"google"}, "idle"); err == nil {
			t.Fatal("expected an error when the calendar never shows")
		}
	}
	// Each failed attempt closed the tab it opened and read nothing.
	if len(driver.readBundles) != 0 || len(driver.closed) != calendarPeekFailureLimit {
		t.Fatalf("closed %v read %v", driver.closed, driver.readBundles)
	}
	if events, _ := st.ListCalendarEvents(context.Background(), 0, start.Add(7*24*time.Hour).UnixMilli(), true); len(events) != 0 {
		t.Fatalf("no events stored: %+v", events)
	}
	// A failed attempt still counts as the last peek (two-hour gap).
	if svc.lastPeek.IsZero() {
		t.Fatal("a failed peek must still set lastPeek")
	}
	// After three failures the provider is on hold for a day.
	if got := svc.providersNotOnHold([]string{"google", "outlook"}, svc.now()); len(got) != 1 || got[0] != "outlook" {
		t.Fatalf("google should be on hold: %v", got)
	}
	if got := svc.providersNotOnHold([]string{"google"}, svc.now().Add(calendarPeekFailureHold+time.Minute)); len(got) != 1 {
		t.Fatalf("the hold ends after a day: %v", got)
	}
}

func TestCalendarPeekRespectsTheLeaseAndTheUsersSwitch(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	driver := &fakePeekDriver{front: "com.apple.finder", browser: "com.google.Chrome", tree: googleWeekTree()}
	svc, _, _ := newPeekTestService(t, driver, now)
	ok, _ := svc.lease.TryAcquire("gmail-reader")
	if !ok {
		t.Fatal("test setup")
	}
	if _, err := svc.run(context.Background(), []string{"google"}, "idle"); err == nil || len(driver.opened) != 0 {
		t.Fatalf("another reader holding the screen blocks the peek: %v %v", err, driver.opened)
	}
	svc.lease.Release("gmail-reader")

	// The user switched to another app while the peek read (without
	// touching the keyboard, say with a trackpad gesture the idle probe
	// missed): it is not pulled back to the old one.
	base := driver.onSleep
	driver.onSleep = func(d time.Duration) {
		base(d)
		driver.idleTime = time.Hour + 24*time.Hour
		if d == calendarPeekSettle {
			driver.front = "com.apple.mail"
		}
	}
	if _, err := svc.run(context.Background(), []string{"google"}, "idle"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(driver.activated) != 0 {
		t.Fatalf("a deliberate switch is left alone: %v", driver.activated)
	}
}

func TestCalendarPeekCheckAppliesTheRules(t *testing.T) {
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	driver := &fakePeekDriver{front: "com.apple.finder", browser: "com.google.Chrome", tree: googleWeekTree()}
	svc, _, _ := newPeekTestService(t, driver, now)
	state := calendar.PeekState{Enabled: true, Granted: true, Providers: []string{"google"}, ComputerUseReady: true, BrowserScriptable: true, Idle: calendar.PeekIdleAfter}
	svc.state = func(time.Time) calendar.PeekState { return state }

	decision, err := svc.check(context.Background())
	if err != nil || !decision.Run || decision.Reason != "idle" {
		t.Fatalf("decision %+v %v", decision, err)
	}
	if len(driver.opened) != 1 {
		t.Fatalf("one open: %v", driver.opened)
	}
	// Straight after, the two-hour gap holds even though the user is idle.
	decision, _ = svc.check(context.Background())
	if decision.Run || decision.Reason != "too soon" {
		t.Fatalf("second decision %+v", decision)
	}
	state.ScreenLocked = true
	svc.lastPeek = time.Time{}
	decision, _ = svc.check(context.Background())
	if decision.Run || decision.Reason != "screen locked" {
		t.Fatalf("locked decision %+v", decision)
	}
	if len(driver.opened) != 1 {
		t.Fatalf("no more opens: %v", driver.opened)
	}
	// A retry pause after user input holds the rules too.
	state.ScreenLocked = false
	svc.notBefore = svc.now().Add(time.Minute)
	decision, _ = svc.check(context.Background())
	if decision.Run || decision.Reason != "user was active" {
		t.Fatalf("retry pause %+v", decision)
	}
}

func TestCalendarPeekWaitsWhenItsNoticeCannotBeSent(t *testing.T) {
	driver := &fakePeekDriver{front: "com.example.editor", browser: "com.example.browser", tree: googleWeekTree()}
	svc, _, _ := newPeekTestService(t, driver, time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC))
	svc.notice = func() bool { return false }
	if _, err := svc.run(context.Background(), []string{"google"}, "idle"); err == nil {
		t.Fatal("peek ran without its notice")
	}
	if len(driver.opened) != 0 {
		t.Fatal("browser opened before its notice")
	}
}
