package main

import (
	"strings"
)

// This older API accepts arbitrary caller prose without a source envelope.
// Keep that prose in the local UI; desktop banners use source-free copy.
// Typed engagement notifications use their separately bound delivery path.
func (a *App) notificationForDelivery(title, body, clickData string) (string, string, bool) {
	if a == nil || a.IsClosedEyesModeEnabled() {
		return "", "", false
	}
	if kind, id, ok := strings.Cut(clickData, ":"); ok {
		switch kind {
		case "memory":
			if _, err := a.memoryEntryForExternalRead(id); err != nil {
				return "", "", false
			}
		case "knowledge":
			entry, err := a.GetKnowledgeEntry(id)
			allow, privacyErr := a.memoryReadFilter()
			if err != nil || privacyErr != nil || !allow(entry.ID, entry.Content) {
				return "", "", false
			}
		}
	}
	body = "Open Isoform for the latest update."
	if a.currentUILanguage() == "zh-CN" {
		body = "打开 Isoform 查看最新更新。"
	}
	return productName(), body, true
}
