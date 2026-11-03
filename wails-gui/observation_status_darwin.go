//go:build darwin

package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func readObservationAppCategory(app SystemApp) string {
	if app.bundlePath == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/libexec/PlistBuddy", "-c", "Print :LSApplicationCategoryType", filepath.Join(app.bundlePath, "Contents", "Info.plist")).Output()
	if err != nil {
		return ""
	}
	switch strings.TrimSpace(string(out)) {
	case "public.app-category.finance":
		return "finance"
	case "public.app-category.medical", "public.app-category.healthcare-fitness":
		return "health"
	default:
		return ""
	}
}
