package main

import (
	"fmt"
	"log"
	"time"
)

// Native chrome uses the same three observation states as the sidebar.
func observingMenuText(view DoingNow, observing bool, lang string) string {
	chinese := normalizeUILanguage(lang) == "zh-CN"
	if view.Kind == doingNowPaused {
		if view.PausedUntil > 0 {
			when := time.UnixMilli(view.PausedUntil).Local()
			clock := observationMenuClock(when, lang)
			if chinese {
				return fmt.Sprintf("观察已暂停至 %s", clock)
			}
			return fmt.Sprintf("Observing paused until %s", clock)
		}
		if chinese {
			return "观察已暂停"
		}
		return "Observing paused"
	}
	if observing {
		if chinese {
			return "后台观察中"
		}
		return "Observing in background"
	}
	if chinese {
		return "未在观察"
	}
	return "Not observing"
}

// Mirrors lib/brief-time.ts without changing the prose and spoken clock helper.
func observationMenuClock(when time.Time, lang string) string {
	if normalizeUILanguage(lang) != "zh-CN" {
		return when.Format("3:04 PM")
	}
	// zh-CN is 24-hour with no leading zero and no 上午/下午 (shell review v1.0.2 B8).
	return fmt.Sprintf("%d:%02d", when.Hour(), when.Minute())
}

func menuBarLanguage(app *App) string {
	if app == nil {
		return "en"
	}
	return app.currentUILanguage()
}

func menuBarObservationTitle(paused bool, lang string) string {
	chinese := normalizeUILanguage(lang) == "zh-CN"
	if paused {
		if chinese {
			return "恢复观察"
		}
		return "Resume observing"
	}
	if chinese {
		return "暂停观察 1 小时"
	}
	return "Pause observing for 1 hour"
}

// The menu and macOS shortcut share the timed pause and resume behavior.
func (a *App) toggleMenuBarObservation() {
	var err error
	if a.IsClosedEyesModeEnabled() {
		err = a.ResumeObservation()
	} else {
		err = a.PauseObservationFor(60)
	}
	if err != nil {
		log.Printf("[menubar] change observing: %v", err)
	}
}
