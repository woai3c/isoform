package main

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// ReportPageFirstPaint logs only code-owned page and source names, and a
// plausible duration; anything else from the WebView is dropped.
func TestReportPageFirstPaintLogsOnlyKnownValues(t *testing.T) {
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(previous) })
	app := &App{}
	app.ReportPageFirstPaint("today", 42, "cache")
	app.ReportPageFirstPaint("tasks", 180, "network")
	app.ReportPageFirstPaint("secret page", 10, "cache")
	app.ReportPageFirstPaint("today", 10, "disk")
	app.ReportPageFirstPaint("chat", -1, "cache")
	app.ReportPageFirstPaint("chat", 11*60*1000, "cache")
	out := buf.String()
	if !strings.Contains(out, "[perf] first_paint page=today ms=42 source=cache") || !strings.Contains(out, "[perf] first_paint page=tasks ms=180 source=network") {
		t.Fatalf("known first paints missing: %q", out)
	}
	if strings.Count(out, "first_paint") != 2 {
		t.Fatalf("unknown pages, sources or durations were logged: %q", out)
	}
}
