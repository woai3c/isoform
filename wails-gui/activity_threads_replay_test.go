package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yansu-agent/agent"
)

// TestActivityThreadsReplay replays real days through the thread pipeline. It
// runs only when YANSU_THREAD_REPLAY_SOURCE names an activity database, which
// is attached read-only; the rows of each day are copied into a fresh store in
// a temporary folder, never back. YANSU_THREAD_REPLAY_DIR receives each day's
// prompt; a reply saved next to it as <day>.reply.json is fed back as the
// model's answer on the next run, and the resulting threads are written to
// <day>.threads.txt. YANSU_THREAD_REPLAY_DAYS lists days as
// 2006-01-02 or 2006-01-02@15:04-15:04, comma separated.
func TestActivityThreadsReplay(t *testing.T) {
	source := os.Getenv("YANSU_THREAD_REPLAY_SOURCE")
	outDir := os.Getenv("YANSU_THREAD_REPLAY_DIR")
	if source == "" || outDir == "" {
		t.Skip("set YANSU_THREAD_REPLAY_SOURCE and YANSU_THREAD_REPLAY_DIR to replay real days")
	}
	for _, spec := range strings.Split(os.Getenv("YANSU_THREAD_REPLAY_DAYS"), ",") {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		t.Run(spec, func(t *testing.T) { replayThreadDay(t, source, outDir, spec) })
	}
}

type replayRunner struct {
	agent.AgentRunner
	promptPath, replyPath string
}

func (r *replayRunner) ExecuteTask(ctx context.Context, input *agent.TaskInput, callback agent.StreamCallback) (string, error) {
	if err := os.WriteFile(r.promptPath, []byte(input.Prompt), 0o600); err != nil {
		return "", err
	}
	reply, err := os.ReadFile(r.replyPath)
	if err != nil {
		return "", errors.New("no saved reply yet")
	}
	return "replay", callback(&agent.StreamEvent{Type: "assistant", Content: string(reply)})
}

func replayThreadDay(t *testing.T, source, outDir, spec string) {
	date, window, _ := strings.Cut(spec, "@")
	day, err := time.ParseInLocation("2006-01-02", date, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	from, to := day, day.AddDate(0, 0, 1)
	if window != "" {
		start, end, _ := strings.Cut(window, "-")
		s, err1 := time.ParseInLocation("2006-01-02 15:04", date+" "+start, time.Local)
		e, err2 := time.ParseInLocation("2006-01-02 15:04", date+" "+end, time.Local)
		if err1 != nil || err2 != nil {
			t.Fatalf("bad window %q", window)
		}
		from, to = s, e
	}
	app := newThreadTestApp(t)
	if self := os.Getenv("YANSU_THREAD_REPLAY_SELF"); self != "" {
		app.cachedDisplayName = self
	}
	store := app.activity.store
	copyReplayDay(t, store, source, day, from, to)
	name := strings.NewReplacer(":", "", "@", "_").Replace(spec)
	runner := &replayRunner{promptPath: filepath.Join(outDir, name+".prompt.txt"), replyPath: filepath.Join(outDir, name+".reply.json")}
	app.runner = runner
	dayMs := day.UnixMilli()
	if _, err := app.runActivityThreadPass(context.Background(), dayMs, true, to); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(runner.replyPath); err != nil {
		t.Logf("prompt written to %s; save the reply as %s and run again", runner.promptPath, runner.replyPath)
		return
	}
	threads, err := app.dayThreadsCached(context.Background(), dayMs)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := store.dayActivityRows(context.Background(), dayMs)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]ActivityRow{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	spent, total, err := app.activityMomentTimeMs(context.Background(), dayMs, dayMs+activityThreadDayMs)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	shown := map[string]bool{}
	clock := func(ms int64) string { return time.UnixMilli(ms).Format("15:04") }
	fmt.Fprintf(&b, "%s: %d moments, %d threads, day line %s at the computer\n", spec, len(rows), len(threads.Threads), formatDurationMs(total))
	for _, thread := range threads.Threads {
		fmt.Fprintf(&b, "\n%s\n  %s to %s, about %s, %d moments\n", thread.Title, clock(thread.StartMs), clock(thread.EndMs), formatDurationMs(thread.TimeSpentMs), len(thread.ActivityIDs))
		for _, id := range thread.ActivityIDs {
			shown[id] = true
			fmt.Fprintf(&b, "  - %s  %s\n", clock(byID[id].StartMs), byID[id].Title)
		}
	}
	var loose []string
	for _, row := range rows {
		if !shown[row.ID] {
			loose = append(loose, fmt.Sprintf("  - %s  %s (%s)", clock(row.StartMs), row.Title, formatDurationMs(spent[row.ID])))
		}
	}
	if len(loose) > 0 {
		fmt.Fprintf(&b, "\nNot in a thread (private, or not composed for the model):\n%s\n", strings.Join(loose, "\n"))
	}
	if err := os.WriteFile(filepath.Join(outDir, name+".threads.txt"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// copyReplayDay copies one day's rows from the source database, attached
// read-only, into the test store.
func copyReplayDay(t *testing.T, store *ActivityStore, source string, day, from, to time.Time) {
	t.Helper()
	ctx := context.Background()
	conn, err := store.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS src`, "file:"+source+"?mode=ro"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(ctx, `DETACH DATABASE src`)
	columns := func(table string) string {
		rows, err := conn.QueryContext(ctx, `SELECT name FROM pragma_table_info(?) WHERE name IN (SELECT name FROM src.pragma_table_info(?))`, table, table)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			names = append(names, name)
		}
		return strings.Join(names, ", ")
	}
	dayStart, dayEnd := day.UnixMilli(), day.AddDate(0, 0, 1).UnixMilli()
	copies := []struct{ table, where string }{
		{"activities", fmt.Sprintf(`day_start_ms = %d AND start_ms >= %d AND start_ms < %d`, dayStart, from.UnixMilli(), to.UnixMilli())},
		{"activity_sessions", fmt.Sprintf(`end_time >= %d AND start_time < %d`, dayStart, dayEnd)},
		{"activity_timeline_groups", fmt.Sprintf(`end_ms >= %d AND start_ms < %d`, dayStart-activityThreadDayMs, dayEnd)},
		{"activity_timeline_group_summaries", fmt.Sprintf(`group_id IN (SELECT id FROM src.activity_timeline_groups WHERE end_ms >= %d AND start_ms < %d)`, dayStart-activityThreadDayMs, dayEnd)},
		{"activity_run_links", fmt.Sprintf(`run_id IN (SELECT id FROM src.activity_timeline_groups WHERE end_ms >= %d AND start_ms < %d)`, dayStart-activityThreadDayMs, dayEnd)},
		{"activity_events", fmt.Sprintf(`session_id IN (SELECT id FROM src.activity_sessions WHERE end_time >= %d AND start_time < %d)`, dayStart, dayEnd)},
		{"activity_evidence_source_revisions", fmt.Sprintf(`session_id IN (SELECT id FROM src.activity_sessions WHERE end_time >= %d AND start_time < %d)`, dayStart, dayEnd)},
	}
	for _, c := range copies {
		cols := columns(c.table)
		if cols == "" {
			continue
		}
		if _, err := conn.ExecContext(ctx, fmt.Sprintf(`INSERT OR REPLACE INTO main.%s (%s) SELECT %s FROM src.%s WHERE %s`, c.table, cols, cols, c.table, c.where)); err != nil {
			t.Fatalf("copy %s: %v", c.table, err)
		}
	}
}
