package main

import (
	"log"
	"sync"
	"time"
)

var launchStartedAt = time.Now()
var launchStages sync.Map

// Stage names are code-owned. Never log binding arguments or returned content.
func recordLaunchStage(stage string) {
	if _, loaded := launchStages.LoadOrStore(stage, true); !loaded {
		log.Printf("[perf] launch stage=%s ms_since_start=%d", stage, time.Since(launchStartedAt).Milliseconds())
	}
}

// pageFirstPaintLimit drops values no real paint takes (a stalled tab).
const pageFirstPaintLimit = 10 * time.Minute

// ReportPageFirstPaint logs one page's first paint after a switch, measured
// in the WebView: "[perf] first_paint page=today ms=42 source=cache". Page
// and source names are code-owned and checked here; nothing else is logged.
func (a *App) ReportPageFirstPaint(page string, ms int, source string) {
	switch page {
	case "today", "chat", "activity", "goals", "tasks", "automations", "about-you", "know-how", "people", "settings", "search":
	default:
		return
	}
	switch source {
	case "cache", "network":
	default:
		return
	}
	if ms < 0 || time.Duration(ms)*time.Millisecond > pageFirstPaintLimit {
		return
	}
	log.Printf("[perf] first_paint page=%s ms=%d source=%s", page, ms, source)
}

// ReportLaunchStage connects WebView paint milestones to the process clock.
func (a *App) ReportLaunchStage(stage string) {
	switch stage {
	case "first_screen_rendered":
		recordLaunchStage(stage)
		a.launch.firstScreenPainted()
	case "frontend_boot", "first_paint", "first_data", "brief_rendered", "chat_rendered", "counts_rendered", "overview_rendered":
		recordLaunchStage(stage)
	}
}
