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
	hour := when.Hour()
	period := "晚上"
	if hour < 6 {
		period = "凌晨"
	} else if hour < 12 {
		period = "上午"
	} else if hour == 12 {
		period = "中午"
	} else if hour <= 18 {
		period = "下午"
	}
	return period + when.Format("3:04")
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
