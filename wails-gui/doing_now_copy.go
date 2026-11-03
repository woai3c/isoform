package main

import "strings"

// doingNowCopy is the menu-bar wording of each doing-now kind, in each UI
// language; "en" is the source of truth. The sidebar tooltip says the same
// through the frontend locales (app.json observingStatus.doingNow), and
// TestDoingNowCopyMatchesTheFrontend keeps the two in step. {app} is filled
// from DoingNow; a meeting without an app uses "meetingUnnamed". Running
// work is always "workingUnnamed" here: the menu bar never names the task
// (see doing_now.go), and only the tooltip's "working" phrase does.
var doingNowCopy = map[string]map[string]string{
	"en": {
		"paused":         "Paused",
		"meeting":        "Listening to your {app} call",
		"meetingUnnamed": "Listening to your call",
		"calendar":       "Reading your calendar",
		"workingUnnamed": "Working on a task",
		"watching":       "Observing in background",
		"listening":      "Listening quietly",
		"idle":           "Not observing",
	},
	"zh-CN": {
		"paused":         "已暂停",
		"meeting":        "正在听你的 {app} 通话",
		"meetingUnnamed": "正在听你的通话",
		"calendar":       "正在读取你的日历",
		"workingUnnamed": "正在处理一项任务",
		"watching":       "后台观察中",
		"listening":      "正在安静地聆听",
		"idle":           "未在观察",
	},
}

// doingNowCopyKey is the catalog key for a view: the kind, or its
// "...Unnamed" wording when the app it names is empty or, for running work,
// always.
func doingNowCopyKey(view DoingNow) string {
	switch view.Kind {
	case doingNowMeeting:
		if view.App == "" {
			return "meetingUnnamed"
		}
	case doingNowWorking:
		return "workingUnnamed"
	}
	return string(view.Kind)
}

// doingNowText is the menu-bar line for a view in a UI language. Unknown
// languages fall back to English and unknown kinds to the idle wording. The
// task title is never part of it.
func doingNowText(view DoingNow, lang string) string {
	table, ok := doingNowCopy[normalizeUILanguage(lang)]
	if !ok {
		table = doingNowCopy["en"]
	}
	template, ok := table[doingNowCopyKey(view)]
	if !ok {
		template = table[string(doingNowIdle)]
	}
	return strings.ReplaceAll(template, "{app}", view.App)
}
