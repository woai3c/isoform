package tools

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Keep these expressions shared with Seatbelt: no directory inventory is
// needed, and new files receive the same policy as existing files.
var dataPrivateBasenames = []string{
	`[^/]*\.(db|sqlite3?)(-journal)?`, `[^/]*-wal`, `[^/]*-shm`, `cron-secret`,
	`(ai-provider|external-mcp|daemon|composio|memory|auth|relay|devices)\.json`,
	`([^/]*[-_.])?(token|tokens|secret|secrets|credential|credentials|identity|identities|key|keys)(\.(json|yaml|yml|toml)|[-_.](backup|bak|old|archived)(\.json)?)?`,
	`[^/]*\.(pem|key|p12|pfx)`,
	`(yansu-gui(\.stderr)?|triage)\.log(\.[^/]*)?`,
}

const dataPrivatePath = `(mcp-hub/state\.json(\.tmp\.[^/]*)?$|(slack-scan/conversations|mobile/briefs)(/|$))`

const dataMediaPath = `(activity|screenshots|audio|transcripts)(/|$)`
const dataMemoryPath = `(memory|knowledge)(/|$)|((SOUL|MEMORY|USER|IDENTITY)\.md)$`

var bundledToolFolders = []string{"bin", "opencli", "bun", "git"}

// Resolve this fixed set individually so a relocated content directory stays
// private without enumerating the data folder.
var dataProtectedFolders = []string{"activity", "screenshots", "audio", "transcripts", "slack-scan/conversations", "mobile/briefs"}

var dataPrivateFileRE = regexp.MustCompile(`(?i)^(` + strings.Join(dataPrivateBasenames, "|") + `)$`)
var dataPrivatePathRE = regexp.MustCompile(`(?i)^` + dataPrivatePath)
var dataMediaRE = regexp.MustCompile(`(?i)^` + dataMediaPath)
var dataMemoryRE = regexp.MustCompile(`(?i)^(` + dataMemoryPath + `)`)

func dataRelativeSensitive(relative string, bash bool) bool {
	relative = filepath.ToSlash(relative)
	if dataPrivateFileRE.MatchString(filepath.Base(relative)) || dataPrivatePathRE.MatchString(relative) || dataMediaRE.MatchString(relative) {
		return true
	}
	return bash && dataMemoryRE.MatchString(strings.TrimPrefix(relative, "./"))
}
