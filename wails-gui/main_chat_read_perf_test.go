package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sort"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
	"wails-gui/internal/engagement"
	"wails-gui/internal/handoff"
	"wails-gui/storage"
)

const (
	mainChatReadPerfFixtureEnv = "ISOFORM_PERF_FIXTURE_DIR"
	mainChatReadPerfPhaseEnv   = "ISOFORM_PERF_PHASE"
	mainChatReadPerfMarker     = "main-chat-fixture.json"
	mainChatReadPerfRows       = 246
	mainChatReadPerfWaiting    = 22
	mainChatReadPerfSessions   = 4540
)

type mainChatReadPerfFixture struct {
	Root      string         `json:"root"`
	Counts    map[string]int `json:"counts"`
	ActivityB int64          `json:"activityBytes"`
	SessionsB int64          `json:"sessionsBytes"`
}

type mainChatReadPerfSource struct {
	topics map[string]ChatTopic
	keys   ChatTopicKeys
}

func (s *mainChatReadPerfSource) ListWorkTopicIDs() []string {
	ids := make([]string, 0, len(s.topics))
	for id := range s.topics {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (s *mainChatReadPerfSource) TopicByID(ctx context.Context, id string) (ChatTopic, error) {
	return s.topics[id], ctx.Err()
}

func (s *mainChatReadPerfSource) TopicKeys(ctx context.Context, id string) (ChatTopicKeys, error) {
	return s.keys, ctx.Err()
}

func mainChatReadPerfDir(tb testing.TB) string {
	tb.Helper()
	root := strings.TrimSpace(os.Getenv(mainChatReadPerfFixtureEnv))
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			tb.Fatal(err)
		}
		root = filepath.Join(home, "work", "perf-fixture")
	}
	return root
}

func mainChatReadPerfMarkerPath(root string) string {
	return filepath.Join(root, mainChatReadPerfMarker)
}

func ensureMainChatReadPerfFixture(t *testing.T) mainChatReadPerfFixture {
	t.Helper()
	root := mainChatReadPerfDir(t)
	if data, err := os.ReadFile(mainChatReadPerfMarkerPath(root)); err == nil {
		var fixture mainChatReadPerfFixture
		if err := json.Unmarshal(data, &fixture); err != nil {
			t.Fatal(err)
		}
		return fixture
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "activity.db")); err == nil {
		t.Fatalf("partial fixture exists without %s; refusing to overwrite", mainChatReadPerfMarker)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("YANSU_MOBILE_STATE_DIR", filepath.Join(root, "mobile"))
	t.Setenv(topicsStateDirEnvVar, filepath.Join(root, "topics"))
	if err := os.MkdirAll(filepath.Join(root, "home"), 0700); err != nil {
		t.Fatal(err)
	}
	state, err := storage.New(filepath.Join(root, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	activity, err := NewActivityStore(root)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	defer state.Close()
	defer activity.Close()

	a := &App{store: state, activity: &activityMonitorState{store: activity}}
	a.engagement = newEngagementService(state, nil, engagementDelivery{})
	a.handoffStore, err = handoff.NewStore(filepath.Join(root, "handoffs"))
	if err != nil {
		t.Fatal(err)
	}
	main, err := a.ensureMainChat(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(time.Local)

	// Seed real outcome sources before bulk-filling to the observed table sizes.
	outcomeFixtures := threadOutcomeFixtures(t)
	var outcomeSources []string
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("perf-thread-%02d", i)
		row := seedThreadFixture(t, a, id, outcomeFixtures[i%len(outcomeFixtures)], now)
		row = saveFixtureOutcome(t, a, row, outcomeFixtures[i%len(outcomeFixtures)])
		outcomeSources = append(outcomeSources, threadOutcomeSourceID(row.ID, row.ThreadOutcome))
	}
	if _, err := activity.db.Exec(`UPDATE activities SET entities='[{"type":"project","name":"Atlas"}]' WHERE id=?`, "perf-thread-00"); err != nil {
		t.Fatal(err)
	}

	// A single matched topic exercises the row-topic enrichment read path.
	ref := TodayChatContextRef{SourceKind: "activity", SourceID: "perf-thread-00"}
	fingerprint, err := a.chatTopicSourceFingerprint(t.Context(), []TodayChatContextRef{ref})
	if err != nil {
		t.Fatal(err)
	}
	a.topicSource = &mainChatReadPerfSource{
		topics: map[string]ChatTopic{"topic-atlas": {ID: "topic-atlas", Name: "Atlas", Refs: []TodayChatContextRef{ref}, SourceFingerprint: fingerprint}},
		keys:   ChatTopicKeys{ThreadKeys: []string{"obj:atlas"}},
	}

	// Five hundred ordinary handoffs let each blocked card traverse both List calls.
	handoffs := make([]handoff.Entry, 0, 500)
	for i := 0; i < 500; i++ {
		status, blockedReason := handoff.StatusBlocked, handoff.BlockedInterrupted
		if i >= 10 && i%10 == 0 {
			status, blockedReason = handoff.StatusCompleted, ""
		}
		entry, err := a.handoffStore.Save(handoff.Entry{
			ID: fmt.Sprintf("perf-handoff-%03d", i), Title: fmt.Sprintf("Review work %03d", i),
			Source: handoff.SourceDesktop, Status: status, BlockedReason: blockedReason,
			Body: "Review the captured work and confirm the result.", CreatedAt: now.Add(-time.Duration(i) * time.Hour).UnixMilli(),
		})
		if err != nil {
			t.Fatal(err)
		}
		handoffs = append(handoffs, entry)
	}

	// The same real read/receipt path used by generated engagement cards.
	makeMoment := func(id string) (engagement.Moment, error) {
		m := engagement.Moment{ID: id, Kind: engagement.KindFirstWeek, SubjectID: id, DedupeKey: "perf:" + id, CreatedAt: now, Params: map[string]string{"stage": "welcome"}}
		m, allowed := a.prepareEngagementRead(t.Context(), m)
		if !allowed {
			return m, fmt.Errorf("could not prepare engagement read receipt for %s", id)
		}
		row := storage.EngagementMomentRow{
			ID: m.ID, Kind: string(m.Kind), SubjectID: m.SubjectID, DedupeKey: m.DedupeKey,
			State: storage.EngagementStateSent, Params: momentParamsJSON(m), CreatedAt: now.UnixMilli(), SentAt: now.UnixMilli(),
		}
		_, err := state.CreateEngagementMoment(t.Context(), row)
		return m, err
	}
	makeCard := func(id, kind string, work *mainChatHandoffWork, outcome string) mainChatProactive {
		card := mainChatProactive{
			MomentID: "perf-moment-" + id, Kind: kind, Headline: "Review work", Body: "A source-backed update is ready.",
			State: "pending", Target: mainChatTarget{Kind: mainChatTargetToday},
			ActionIDs: []string{}, ActionLabels: map[string]string{}, Evidence: []mainChatEvidenceRef{},
			ActivityOutcomeSource: outcome, Work: work,
		}
		if work != nil {
			card.Target = mainChatTarget{Kind: mainChatTargetHandoff, ID: work.HandoffID}
			card.Evidence = []mainChatEvidenceRef{{Kind: "handoff", ID: work.HandoffID}}
			return card
		}
		return card
	}
	encode := func(v any) string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	appendRow := func(id, tool, input string, at time.Time) {
		_, err := state.AppendStreamItem(t.Context(), main.LocalID, storage.StreamItemRow{
			ID: id, Kind: "system", ToolName: tool, ToolInput: input, CreatedAt: at.UnixMilli(),
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	waitingIDs := make([]string, 0, mainChatReadPerfWaiting)
	for i := 0; i < 10; i++ {
		entry := handoffs[i]
		card := makeCard(fmt.Sprintf("blocked-%02d", i), string(engagement.KindWorkBlocked), &mainChatHandoffWork{
			HandoffID: entry.ID, Status: entry.Status, Summary: entry.Title, Deliverables: []mainChatHandoffDeliverable{},
		}, "")
		if i == 0 {
			card.Evidence = append(card.Evidence, mainChatEvidenceRef{Kind: "activity", ID: "perf-thread-00"})
		}
		card.ReadReceipt = a.saveEngagementCardRead(t.Context(), proactiveVoicePresentationHash(card), card.MomentID, entry.ID, proactiveVoiceHash(entry))
		if card.ReadReceipt == "" {
			t.Fatalf("handoff card %s failed to save receipt", entry.ID)
		}
		id := fmt.Sprintf("perf-waiting-%02d", len(waitingIDs))
		appendRow(id, mainChatProactiveTool, encode(card), now.Add(-time.Duration(i+1)*time.Second))
		if i == 0 {
			if err := a.tagMainChatRowTopics(t.Context(), id, []mainChatEvidenceRef{{Kind: "activity", ID: "perf-thread-00"}}); err != nil {
				t.Fatal(err)
			}
		}
		waitingIDs = append(waitingIDs, id)
	}
	for i, source := range outcomeSources {
		card := makeCard(fmt.Sprintf("outcome-%02d", i), string(engagement.KindCommitmentSlipping), nil, source)
		id := fmt.Sprintf("perf-waiting-%02d", len(waitingIDs))
		appendRow(id, mainChatProactiveTool, encode(card), now.Add(-time.Duration(20+i)*time.Second))
		waitingIDs = append(waitingIDs, id)
	}
	for i := 0; i < 4; i++ {
		m, err := makeMoment(fmt.Sprintf("perf-first-week-%02d", i))
		if err != nil {
			t.Fatal(err)
		}
		card := makeCard(fmt.Sprintf("first-week-%02d", i), string(m.Kind), nil, "")
		card.MomentID = m.ID
		if !a.saveEngagementJourneyReceipt(t.Context(), m, &card) {
			t.Fatalf("engagement card %s failed to save journey receipt", m.ID)
		}
		id := fmt.Sprintf("perf-waiting-%02d", len(waitingIDs))
		appendRow(id, mainChatProactiveTool, encode(card), now.Add(-time.Duration(30+i)*time.Second))
		waitingIDs = append(waitingIDs, id)
	}
	for i := 0; i < 2; i++ {
		m, err := makeMoment(fmt.Sprintf("perf-digest-child-%02d", i))
		if err != nil {
			t.Fatal(err)
		}
		child := makeCard(fmt.Sprintf("digest-child-%02d", i), string(m.Kind), nil, "")
		child.MomentID = m.ID
		if !a.saveEngagementJourneyReceipt(t.Context(), m, &child) {
			t.Fatalf("digest child %s failed to save journey receipt", m.ID)
		}
		digest := mainChatProactive{
			MomentID: fmt.Sprintf("perf-digest-%02d", i), Kind: "digest", State: "pending",
			Headline: "Work updates", Body: "Two source-backed items.", Items: []mainChatProactive{child},
			ActionIDs: []string{}, ActionLabels: map[string]string{}, Evidence: []mainChatEvidenceRef{},
		}
		id := fmt.Sprintf("perf-waiting-%02d", len(waitingIDs))
		appendRow(id, mainChatProactiveTool, encode(digest), now.Add(-time.Duration(40+i)*time.Second))
		waitingIDs = append(waitingIDs, id)
	}
	if len(waitingIDs) != mainChatReadPerfWaiting {
		t.Fatalf("created %d waiting cards", len(waitingIDs))
	}
	// A completed handoff card is also projected, but is not a waiting candidate.
	finished := makeCard("finished-00", string(engagement.KindWorkFinished), &mainChatHandoffWork{
		HandoffID: handoffs[10].ID, Status: handoff.StatusCompleted, Summary: handoffs[10].Title, Deliverables: []mainChatHandoffDeliverable{},
	}, "")
	finished.ReadReceipt = a.saveEngagementCardRead(t.Context(), proactiveVoicePresentationHash(finished), finished.MomentID, handoffs[10].ID, proactiveVoiceHash(handoffs[10]))
	appendRow("perf-finished", mainChatProactiveTool, encode(finished), now.Add(-time.Minute))

	statusMoment, err := makeMoment("perf-status-moment")
	if err != nil {
		t.Fatal(err)
	}
	entry := mainChatStatusEntry{ID: "perf-status-entry", MomentID: statusMoment.ID, Kind: string(statusMoment.Kind), Text: "A welcome update is ready.", Target: mainChatTarget{Kind: mainChatTargetToday}}
	entry.ReadReceipt = a.saveEngagementCardRead(t.Context(), proactiveVoiceStatusHash(entry), entry.MomentID, "", "")
	appendRow("perf-status", mainChatStatusTool, encode(mainChatStatus{Period: "morning", Entries: []mainChatStatusEntry{entry}}), now.Add(-time.Minute))
	for i := 0; i < mainChatReadPerfRows-len(waitingIDs)-2; i++ {
		row := storage.StreamItemRow{ID: fmt.Sprintf("perf-chat-%03d", i), Kind: "user", Content: "Review the current project changes.", CreatedAt: now.Add(-time.Duration(i+2) * time.Second).UnixMilli()}
		if _, err := state.AppendStreamItem(t.Context(), main.LocalID, row); err != nil {
			t.Fatal(err)
		}
	}

	if err := fillMainChatReadPerfActivity(t, activity, now); err != nil {
		t.Fatal(err)
	}
	if _, err := state.UpsertMailMessages(t.Context(), []storage.MailMessageRow{{
		ID: "perf-mail-envelope", Mailbox: storage.MailboxInbox, SenderEmail: "review@example.test",
		SenderName: "Review Bot", To: "owner@example.test", ToMe: true, Subject: "Project review notes",
		ThreadKey: "perf-review", At: now.UnixMilli(), FirstSeenAt: now.UnixMilli(), UpdatedAt: now.UnixMilli(),
	}}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	mailRows, err := mobileGoalMailRows(a)
	if err != nil {
		t.Fatalf("read fixture-backed mail: %v", err)
	}
	foundMail := false
	for _, mail := range mailRows {
		if mail.ID == "perf-mail-envelope" {
			foundMail = true
			break
		}
	}
	if !foundMail {
		t.Fatal("fixture-backed mail header was not returned by mobileGoalMailRows")
	}

	counts, err := mainChatReadPerfCounts(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) == 0 {
		t.Fatal("fixture has no tables")
	}
	for table, want := range map[string]int{
		"activity.activity_sessions":                 mainChatReadPerfSessions,
		"activity.activity_events":                   58619,
		"activity.activity_snapshots":                10161,
		"activity.activity_ocr_frames":               6454,
		"activity.activity_timeline_groups":          4540,
		"activity.activity_timeline_group_summaries": 3985,
		"activity.activity_run_links":                3984,
		"activity.work_pattern_profile_evidence_v1":  595884,
		"activity.task_episode_evidence":             15458,
		"sessions.stream_items":                      mainChatReadPerfRows,
	} {
		if counts[table] != want {
			t.Fatalf("%s rows=%d, want %d", table, counts[table], want)
		}
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	if err := activity.Close(); err != nil {
		t.Fatal(err)
	}
	activityInfo, err := os.Stat(filepath.Join(root, "activity.db"))
	if err != nil {
		t.Fatal(err)
	}
	if activityInfo.Size() < 1<<30 {
		t.Fatalf("activity.db is %d bytes; want at least 1 GiB", activityInfo.Size())
	}
	sessionInfo, err := os.Stat(filepath.Join(root, "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	fixture := mainChatReadPerfFixture{Root: root, Counts: counts, ActivityB: activityInfo.Size(), SessionsB: sessionInfo.Size()}
	raw, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainChatReadPerfMarkerPath(root), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func fillMainChatReadPerfActivity(t *testing.T, activity *ActivityStore, now time.Time) error {
	t.Helper()
	const (
		sessionsTarget     = 4540
		eventsTarget       = 58619
		snapshotsTarget    = 10161
		ocrTarget          = 6454
		runsTarget         = 4540
		summariesTarget    = 3985
		linksTarget        = 3984
		profileTarget      = 595884
		taskEvidenceTarget = 15458
	)
	var sessions int
	if err := activity.db.QueryRow(`SELECT COUNT(*) FROM activity_sessions`).Scan(&sessions); err != nil {
		return err
	}
	if sessions < sessionsTarget {
		tx, err := activity.db.Begin()
		if err != nil {
			return err
		}
		stmt, err := tx.Prepare(`INSERT INTO activity_sessions(id,start_time,end_time,trigger_kind,app_names,event_count,segment_count,created_at)
			VALUES(?,?,?,'manual','["Code"]',0,0,?)`)
		if err != nil {
			tx.Rollback()
			return err
		}
		for i := 0; i < sessionsTarget-sessions; i++ {
			days := i % 30
			if i%10 < 7 {
				days = i % 4
			}
			start := now.Add(-time.Duration(days)*24*time.Hour - 45*time.Minute)
			if _, err := stmt.Exec(fmt.Sprintf("perf-session-%05d", i), start.UnixMilli(), start.Add(45*time.Minute).UnixMilli(), now.UnixMilli()); err != nil {
				stmt.Close()
				tx.Rollback()
				return err
			}
		}
		stmt.Close()
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	apps := make([]string, 40)
	appData := make([]string, len(apps))
	for i := range apps {
		apps[i] = fmt.Sprintf("Review App %02d", i)
		payload := map[string]string{"appName": apps[i], "appBundleId": fmt.Sprintf("com.example.review%02d", i), "windowTitle": "Reviewing project changes", "typedText": strings.Repeat("Review implementation and verify the source projection. ", 24)}
		raw, _ := json.Marshal(payload)
		appData[i] = string(raw)
	}
	kinds := []string{"focus", "keyboard", "click", "app_switch", "window", "scroll"}
	var eventCount int
	if err := activity.db.QueryRow(`SELECT COUNT(*) FROM activity_events`).Scan(&eventCount); err != nil {
		return err
	}
	tx, err := activity.db.Begin()
	if err != nil {
		return err
	}
	eventStmt, err := tx.Prepare(`INSERT INTO activity_events(session_id,timestamp,kind,data,sort_order) VALUES(?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	for i := 0; i < eventsTarget-eventCount; i++ {
		sessionIndex := i % (sessionsTarget - 6)
		sessionID := fmt.Sprintf("perf-session-%05d", sessionIndex)
		days := sessionIndex % 30
		if sessionIndex%10 < 7 {
			days = sessionIndex % 4
		}
		sessionEvent := i / (sessionsTarget - 6)
		at := now.Add(-time.Duration(days)*24*time.Hour - 45*time.Minute + time.Duration(sessionEvent)*2*time.Minute)
		if _, err := eventStmt.Exec(sessionID, at.UnixMilli(), kinds[i%len(kinds)], appData[i%len(appData)], sessionEvent); err != nil {
			eventStmt.Close()
			tx.Rollback()
			return err
		}
	}
	eventStmt.Close()
	if err := tx.Commit(); err != nil {
		return err
	}
	snapshotText := strings.Repeat("Project review — implementation notes, source references, tests, and follow-up decisions. ", 1000)
	var snapshotCount int
	if err := activity.db.QueryRow(`SELECT COUNT(*) FROM activity_snapshots`).Scan(&snapshotCount); err != nil {
		return err
	}
	tx, err = activity.db.Begin()
	if err != nil {
		return err
	}
	snapshotStmt, err := tx.Prepare(`INSERT INTO activity_snapshots(session_id,timestamp,file_path,width,height,size_bytes,trigger_kind,storage_tier,ax_text,app,parsed_json)
		VALUES(?,?,?,1440,900,420000,'focus','hot',?,'Code','{}')`)
	if err != nil {
		tx.Rollback()
		return err
	}
	for i := 0; i < snapshotsTarget-snapshotCount; i++ {
		sessionIndex := i % (sessionsTarget - 6)
		sessionID := fmt.Sprintf("perf-session-%05d", sessionIndex)
		days := sessionIndex % 30
		if sessionIndex%10 < 7 {
			days = sessionIndex % 4
		}
		if _, err := snapshotStmt.Exec(sessionID, now.Add(-time.Duration(days)*24*time.Hour).UnixMilli(), fmt.Sprintf("snapshots/%05d.jpg", i), snapshotText); err != nil {
			snapshotStmt.Close()
			tx.Rollback()
			return err
		}
	}
	snapshotStmt.Close()
	if err := tx.Commit(); err != nil {
		return err
	}
	ocrText := strings.Repeat("Project review: verify implementation, compare source changes, and record the test result. ", 170)
	var ocrCount, snapshotTotal int
	if err := activity.db.QueryRow(`SELECT COUNT(*) FROM activity_ocr_frames`).Scan(&ocrCount); err != nil {
		return err
	}
	if err := activity.db.QueryRow(`SELECT COUNT(*) FROM activity_snapshots`).Scan(&snapshotTotal); err != nil {
		return err
	}
	snapshotIDs := make([]int64, 0, snapshotTotal)
	rows, err := activity.db.Query(`SELECT id FROM activity_snapshots ORDER BY id`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		snapshotIDs = append(snapshotIDs, id)
	}
	rows.Close()
	tx, err = activity.db.Begin()
	if err != nil {
		return err
	}
	ocrStmt, err := tx.Prepare(`INSERT INTO activity_ocr_frames(session_id,segment_id,frame_time,text,confidence) VALUES(?,?,?, ?,0.98)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	for i := 0; i < ocrTarget-ocrCount; i++ {
		sessionID := fmt.Sprintf("perf-session-%05d", i%(sessionsTarget-6))
		if _, err := ocrStmt.Exec(sessionID, snapshotIDs[i%len(snapshotIDs)], float64(i), ocrText); err != nil {
			ocrStmt.Close()
			tx.Rollback()
			return err
		}
	}
	ocrStmt.Close()
	if err := tx.Commit(); err != nil {
		return err
	}
	if _, err := activity.db.Exec(`INSERT INTO work_pattern_profile_snapshots_v1(
		id,policy_version,source_window_start,source_cutoff_at,source_max_event_id,source_hash,status,sample_count,session_count,confidence,canonical_json,sha256,created_at
	) VALUES('perf-profile-snapshot','v1',?,?,0,'perf-source','qualified',1,1,0.9,'{}','perf-hash',?)`,
		now.Add(-30*24*time.Hour).UnixMilli(), now.UnixMilli(), now.UnixMilli()); err != nil {
		return err
	}
	tx, err = activity.db.Begin()
	if err != nil {
		return err
	}
	profileStmt, err := tx.Prepare(`INSERT INTO work_pattern_profile_evidence_v1(
			id,profile_snapshot_id,source_kind,source_id,observed_at,content_hash,included,reason,created_at
		) VALUES(?,?,?,?,?,'perf-hash',1,'included',?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	for i := 0; i < profileTarget; i++ {
		if _, err := profileStmt.Exec(
			fmt.Sprintf("perf-profile-%06d", i), "perf-profile-snapshot", "activity_event", fmt.Sprintf("event-%06d", i),
			now.Add(-time.Duration(i%30)*24*time.Hour).UnixMilli(), now.UnixMilli()); err != nil {
			profileStmt.Close()
			tx.Rollback()
			return err
		}
	}
	profileStmt.Close()
	if err := tx.Commit(); err != nil {
		return err
	}
	for i := 0; i < 1546; i++ {
		start := now.Add(-time.Duration(i%30) * 24 * time.Hour).UnixMilli()
		if _, err := activity.db.Exec(`INSERT INTO task_episodes(id,inferred_start_at,detected_at,create_payload_hash,create_payload_json,created_at)
			VALUES(?,?,?,?,?,?)`, fmt.Sprintf("perf-episode-%04d", i), start, start+1000, "perf-hash", `{"task":"project review"}`, now.UnixMilli()); err != nil {
			return err
		}
	}
	tx, err = activity.db.Begin()
	if err != nil {
		return err
	}
	taskStmt, err := tx.Prepare(`INSERT INTO task_episode_evidence(
			id,task_episode_id,role,source_kind,source_id,summary,payload_json,content_hash,observed_at,created_at
		) VALUES(?,?,'context','activity_event',?,'Project review evidence','{}',?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	for i := 0; i < taskEvidenceTarget; i++ {
		start := now.Add(-time.Duration(i%30) * 24 * time.Hour).UnixMilli()
		if _, err := taskStmt.Exec(
			fmt.Sprintf("perf-task-evidence-%05d", i), fmt.Sprintf("perf-episode-%04d", i%1546),
			fmt.Sprintf("event-%06d", i), fmt.Sprintf("hash-%05d", i), start, now.UnixMilli()); err != nil {
			taskStmt.Close()
			tx.Rollback()
			return err
		}
	}
	taskStmt.Close()
	if err := tx.Commit(); err != nil {
		return err
	}
	var currentRuns int
	if err := activity.db.QueryRow(`SELECT COUNT(*) FROM activity_timeline_groups`).Scan(&currentRuns); err != nil {
		return err
	}
	if currentRuns < runsTarget {
		existingSessions := map[string]bool{}
		rows, err := activity.db.Query(`SELECT session_ids FROM activity_timeline_groups`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			var ids []string
			_ = json.Unmarshal([]byte(raw), &ids)
			for _, id := range ids {
				existingSessions[id] = true
			}
		}
		rows.Close()
		rows, err = activity.db.Query(`SELECT s.id,MIN(e.id),MAX(e.id),MIN(e.timestamp),MAX(e.timestamp),COUNT(*)
			FROM activity_sessions s JOIN activity_events e ON e.session_id=s.id
			GROUP BY s.id ORDER BY MIN(e.id)`)
		if err != nil {
			return err
		}
		var runs []ActivityTimelineRunRow
		for rows.Next() {
			var session string
			var first, last, start, end int64
			var count int
			if err := rows.Scan(&session, &first, &last, &start, &end, &count); err != nil {
				rows.Close()
				return err
			}
			if existingSessions[session] {
				continue
			}
			runs = append(runs, ActivityTimelineRunRow{
				ID: timelineRunID(first), FirstEventRowID: first, LastEventRowID: last, AppKey: "com.example.code", AppName: "Code",
				StartMs: start, EndMs: end, ActiveMs: end - start, EventCount: count, SessionIDs: []string{session},
				TriggerKinds: []string{"manual"}, Status: timelineRunStatusClosed,
			})
			if currentRuns+len(runs) == runsTarget {
				break
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		tx, err := activity.db.Begin()
		if err != nil {
			return err
		}
		for _, run := range runs {
			if err := insertTimelineRun(tx, run, now.UnixMilli()); err != nil {
				tx.Rollback()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		currentRuns += len(runs)
		if currentRuns != runsTarget {
			return fmt.Errorf("timeline groups=%d, want %d", currentRuns, runsTarget)
		}
	}
	summaryCount := countRows(t, activity.db, "activity_timeline_group_summaries")
	if summaryCount > summariesTarget {
		return fmt.Errorf("timeline summaries already have %d rows", summaryCount)
	}
	type timelineSummary struct {
		id, appKey, appName string
		start, end          int64
	}
	var summaries []timelineSummary
	rows, err = activity.db.Query(`SELECT g.id,g.app_key,g.app_name,g.start_ms,g.end_ms
		FROM activity_timeline_groups g LEFT JOIN activity_timeline_group_summaries s ON s.group_id=g.id
		WHERE s.group_id IS NULL ORDER BY g.start_ms,g.id LIMIT ?`, summariesTarget-summaryCount)
	if err != nil {
		return err
	}
	for rows.Next() {
		var item timelineSummary
		if err := rows.Scan(&item.id, &item.appKey, &item.appName, &item.start, &item.end); err != nil {
			rows.Close()
			return err
		}
		summaries = append(summaries, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	tx, err = activity.db.Begin()
	if err != nil {
		return err
	}
	insertSummary, err := tx.Prepare(`INSERT INTO activity_timeline_group_summaries(
		group_id,app_key,app_name,start_ms,end_ms,title,description,activity_type,entities,timeline_json,created_at,updated_at
	) VALUES(?,?,?,?,?,'Project review','Implementation and verification work.','work','[]','[]',?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	for _, item := range summaries {
		if _, err := insertSummary.Exec(item.id, item.appKey, item.appName, item.start, item.end, now.UnixMilli(), now.UnixMilli()); err != nil {
			insertSummary.Close()
			tx.Rollback()
			return err
		}
	}
	insertSummary.Close()
	if err := tx.Commit(); err != nil {
		return err
	}
	if got := countRows(t, activity.db, "activity_timeline_group_summaries"); got != summariesTarget {
		return fmt.Errorf("timeline summaries=%d, want %d", got, summariesTarget)
	}
	linkCount := countRows(t, activity.db, "activity_run_links")
	if linkCount > linksTarget {
		return fmt.Errorf("activity run links already have %d rows", linkCount)
	}
	rows, err = activity.db.Query(`SELECT g.id FROM activity_timeline_groups g
		LEFT JOIN activity_run_links l ON l.run_id=g.id WHERE l.run_id IS NULL ORDER BY g.start_ms,g.id LIMIT ?`,
		linksTarget-linkCount)
	if err != nil {
		return err
	}
	var runIDs []string
	for rows.Next() {
		var runID string
		if err := rows.Scan(&runID); err != nil {
			rows.Close()
			return err
		}
		runIDs = append(runIDs, runID)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	tx, err = activity.db.Begin()
	if err != nil {
		return err
	}
	insertLink, err := tx.Prepare(`INSERT INTO activity_run_links(run_id,activity_id,composed_event_count,created_at) VALUES(?,?,12,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	for i, runID := range runIDs {
		if _, err := insertLink.Exec(runID, fmt.Sprintf("perf-activity-%05d", i), now.UnixMilli()); err != nil {
			insertLink.Close()
			tx.Rollback()
			return err
		}
	}
	insertLink.Close()
	if err := tx.Commit(); err != nil {
		return err
	}
	if got := countRows(t, activity.db, "activity_run_links"); got != linksTarget {
		return fmt.Errorf("activity run links=%d, want %d", got, linksTarget)
	}
	if _, err := activity.db.Exec(`UPDATE activity_sessions
		SET app_names=COALESCE((
				SELECT json_group_array(DISTINCT json_extract(data,'$.appName'))
				FROM activity_events WHERE session_id=activity_sessions.id
			),'[]'),
			event_count=(SELECT COUNT(*) FROM activity_events WHERE session_id=activity_sessions.id),
			snapshot_count=(SELECT COUNT(*) FROM activity_snapshots WHERE session_id=activity_sessions.id),
			segment_count=(SELECT COUNT(*) FROM activity_segments WHERE session_id=activity_sessions.id),
			total_size_bytes=COALESCE((SELECT SUM(length(data)) FROM activity_events WHERE session_id=activity_sessions.id),0)
				+COALESCE((SELECT SUM(size_bytes) FROM activity_snapshots WHERE session_id=activity_sessions.id),0)
				+COALESCE((SELECT SUM(length(text)) FROM activity_ocr_frames WHERE session_id=activity_sessions.id),0)`); err != nil {
		return err
	}
	return nil
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM "` + table + `"`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func mainChatReadPerfCounts(root string) (map[string]int, error) {
	counts := map[string]int{}
	for _, database := range []struct {
		name string
		path string
	}{{"activity", filepath.Join(root, "activity.db")}, {"sessions", filepath.Join(root, "sessions.db")}} {
		db, err := sql.Open("sqlite", database.path)
		if err != nil {
			return nil, err
		}
		rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
		if err != nil {
			db.Close()
			return nil, err
		}
		var tables []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				db.Close()
				return nil, err
			}
			tables = append(tables, name)
		}
		rows.Close()
		for _, table := range tables {
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM "` + strings.ReplaceAll(table, `"`, `""`) + `"`).Scan(&count); err != nil {
				db.Close()
				return nil, err
			}
			counts[database.name+"."+table] = count
		}
		if err := db.Close(); err != nil {
			return nil, err
		}
	}
	return counts, nil
}

func openMainChatReadPerfApp(tb testing.TB) (*App, string, func()) {
	tb.Helper()
	root := mainChatReadPerfDir(tb)
	if _, err := os.Stat(mainChatReadPerfMarkerPath(root)); err != nil {
		tb.Fatalf("fixture not generated: %v (run TestMainChatReadPerfFixture first)", err)
	}
	tb.Setenv("HOME", filepath.Join(root, "home"))
	tb.Setenv("YANSU_MOBILE_STATE_DIR", filepath.Join(root, "mobile"))
	tb.Setenv(topicsStateDirEnvVar, filepath.Join(root, "topics"))
	state, err := storage.New(filepath.Join(root, "sessions.db"))
	if err != nil {
		tb.Fatal(err)
	}
	activity, err := openActivityStore(root, false)
	if err != nil {
		state.Close()
		tb.Fatal(err)
	}
	a := &App{store: state, activity: &activityMonitorState{store: activity}}
	a.engagement = newEngagementService(state, nil, engagementDelivery{})
	a.handoffStore, err = handoff.NewStore(filepath.Join(root, "handoffs"))
	if err != nil {
		tb.Fatal(err)
	}
	ref := TodayChatContextRef{SourceKind: "activity", SourceID: "perf-thread-00"}
	fingerprint, err := a.chatTopicSourceFingerprint(tb.Context(), []TodayChatContextRef{ref})
	if err != nil {
		tb.Fatal(err)
	}
	a.topicSource = &mainChatReadPerfSource{
		topics: map[string]ChatTopic{"topic-atlas": {ID: "topic-atlas", Name: "Atlas", Refs: []TodayChatContextRef{ref}, SourceFingerprint: fingerprint}},
		keys:   ChatTopicKeys{ThreadKeys: []string{"obj:atlas"}},
	}
	return a, "", func() {
		_ = state.Close()
		_ = activity.Close()
	}
}

func TestMainChatReadPerfFixture(t *testing.T) {
	if strings.TrimSpace(os.Getenv(mainChatReadPerfFixtureEnv)) == "" {
		t.Skipf("set %s to build the main-chat read fixture", mainChatReadPerfFixtureEnv)
	}
	fixture := ensureMainChatReadPerfFixture(t)
	t.Logf("fixture=%+v", fixture)
	a, _, closeApp := openMainChatReadPerfApp(t)
	defer closeApp()
	overview, err := a.GetMainChatOverview()
	if err != nil {
		t.Fatal(err)
	}
	if overview.Waiting != mainChatReadPerfWaiting {
		t.Fatalf("overview waiting=%d, want %d", overview.Waiting, mainChatReadPerfWaiting)
	}
	if overview.Finished != 1 {
		t.Fatalf("overview finished=%d, want 1", overview.Finished)
	}
	if overview.Routine < 1 {
		t.Fatalf("overview routine=%d, want a projected status entry", overview.Routine)
	}
	page, err := a.ListMainChatWaitingItems("", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != mainChatReadPerfWaiting {
		t.Fatalf("waiting items=%d, want %d", len(page.Items), mainChatReadPerfWaiting)
	}
	for _, item := range page.Items {
		var card mainChatProactive
		if json.Unmarshal([]byte(item.ToolInput), &card) != nil ||
			card.Kind == "thread_update_unavailable" ||
			((card.Kind == string(engagement.KindWorkBlocked) || card.Kind == string(engagement.KindWorkFinished)) && card.Work == nil) {
			t.Fatalf("waiting item %s was withdrawn", item.ID)
		}
		if card.Kind == string(engagement.KindFirstWeek) && card.Headline != "Review work" {
			t.Fatalf("waiting item %s failed its journey receipt", item.ID)
		}
		if card.Kind == "digest" && len(card.Items) != 1 {
			t.Fatalf("waiting item %s lost its digest child", item.ID)
		}
		for _, child := range card.Items {
			if child.Kind == string(engagement.KindFirstWeek) && child.Headline != "Review work" {
				t.Fatalf("waiting item %s has a withdrawn digest child", item.ID)
			}
		}
	}
}

type mainChatReadPerfTiming struct {
	Binding string        `json:"binding"`
	P50     time.Duration `json:"p50"`
	P90     time.Duration `json:"p90"`
	Max     time.Duration `json:"max"`
}

func mainChatReadPerfArtifactDir(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(dir)
}

func mainChatReadPerfArtifactName(ext string) string {
	phase := "before"
	if strings.TrimSpace(os.Getenv(mainChatReadPerfPhaseEnv)) == "after" {
		phase = "after"
	}
	return "main-chat-" + phase + ext
}

func TestMainChatReadPerfTimings(t *testing.T) {
	if strings.TrimSpace(os.Getenv(mainChatReadPerfFixtureEnv)) == "" {
		t.Skipf("set %s to time the main-chat read fixture", mainChatReadPerfFixtureEnv)
	}
	ensureMainChatReadPerfFixture(t)
	a, _, closeApp := openMainChatReadPerfApp(t)
	defer closeApp()
	cases := []struct {
		name string
		read func() error
	}{
		{"GetMainChatOverview", func() error { _, err := a.GetMainChatOverview(); return err }},
		{"ListMainChatWaitingItems", func() error { _, err := a.ListMainChatWaitingItems("", 50); return err }},
	}
	results := make([]mainChatReadPerfTiming, 0, len(cases))
	for _, c := range cases {
		durations := make([]time.Duration, 30)
		for i := range durations {
			start := time.Now()
			if err := c.read(); err != nil {
				t.Fatal(err)
			}
			durations[i] = time.Since(start)
		}
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		results = append(results, mainChatReadPerfTiming{Binding: c.name, P50: durations[14], P90: durations[26], Max: durations[len(durations)-1]})
	}
	raw, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := mainChatReadPerfArtifactDir(t)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, mainChatReadPerfArtifactName(".json")), raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Log(string(raw))
}

func BenchmarkMainChatOverview(b *testing.B) {
	if strings.TrimSpace(os.Getenv(mainChatReadPerfFixtureEnv)) == "" {
		b.Skip("set ISOFORM_PERF_FIXTURE_DIR to use the main-chat fixture")
	}
	a, _, closeApp := openMainChatReadPerfApp(b)
	defer closeApp()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := a.GetMainChatOverview(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListMainChatWaitingItems(b *testing.B) {
	if strings.TrimSpace(os.Getenv(mainChatReadPerfFixtureEnv)) == "" {
		b.Skip("set ISOFORM_PERF_FIXTURE_DIR to use the main-chat fixture")
	}
	a, _, closeApp := openMainChatReadPerfApp(b)
	defer closeApp()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := a.ListMainChatWaitingItems("", 50); err != nil {
			b.Fatal(err)
		}
	}
}

func TestMainChatReadPerfCPUProfile(t *testing.T) {
	if strings.TrimSpace(os.Getenv(mainChatReadPerfFixtureEnv)) == "" {
		t.Skip("set ISOFORM_PERF_FIXTURE_DIR to use the main-chat fixture")
	}
	ensureMainChatReadPerfFixture(t)
	a, _, closeApp := openMainChatReadPerfApp(t)
	defer closeApp()
	dir := mainChatReadPerfArtifactDir(t)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(dir, mainChatReadPerfArtifactName(".cpu")))
	if err != nil {
		t.Fatal(err)
	}
	if err := pprof.StartCPUProfile(file); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if _, err := a.GetMainChatOverview(); err != nil {
		pprof.StopCPUProfile()
		file.Close()
		t.Fatal(err)
	}
	if _, err := a.ListMainChatWaitingItems("", 50); err != nil {
		pprof.StopCPUProfile()
		file.Close()
		t.Fatal(err)
	}
	pprof.StopCPUProfile()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
