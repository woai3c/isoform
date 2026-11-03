package main

import (
	"strings"
	"time"
)

// heavyAXWalkMinInterval is the minimum time between accessibility-tree walks
// for a heavy AX app (see isHeavyAXWalkApp). 30s is long enough to stop the
// repeated live-a11y-tree rebuilds that make these apps sluggish while still
// refreshing the structured tree periodically. The screenshot/OCR path still
// runs on its normal cadence, so per-frame content is unaffected.
const heavyAXWalkMinInterval = 30 * time.Second

// heavyAXWalkBundlePrefixes lists apps whose full accessibility tree is
// expensive to walk AND low-value to us: heavy Chromium/CEF apps that we do not
// prime (see app_needs_prime in axtree_tree_darwin.go) and have no structured
// parser for. Repeatedly querying kAXChildren on their web area forces the app
// to build/maintain a live accessibility tree on its UI thread, which starves
// the foreground app and makes typing sluggish (observed with DingTalk). For
// these apps we throttle the AX walk hard and rely on the screenshot/OCR path.
//
// Matching is case-insensitive and prefix-based, mirroring pii.IsSensitiveApp.
// Do NOT add apps we prime AND parse here (Slack, Discord, Lark/Feishu, Linear,
// Notion, …): their AX tree is valuable and priming already amortizes the cost.
var heavyAXWalkBundlePrefixes = []string{
	"com.alibaba.dingtalk", // DingTalk / 钉钉 — CEF, unprimed, no parser
}

// isHeavyAXWalkApp reports whether the given bundle ID belongs to an app whose
// AX-tree walk should be throttled to protect its UI responsiveness.
func isHeavyAXWalkApp(bundleID string) bool {
	if bundleID == "" {
		return false
	}
	lower := strings.ToLower(bundleID)
	for _, prefix := range heavyAXWalkBundlePrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}
