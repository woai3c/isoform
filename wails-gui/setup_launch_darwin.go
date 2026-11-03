//go:build darwin

package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"
)

// readNativeSetupLaunchState reads the onboarding state and the watching flag from
// sessions.db before Wails creates the window. It is one indexed read of ui_state:
// the storage itself opens later, after the window is on screen.
//
// A missing database is a first run. A database that cannot be read keeps the main
// window and the menu bar mark, the behaviour before setup v2.
func readNativeSetupLaunchState() setupLaunchState {
	normal := setupLaunchState{MenuBarVisible: true}
	dir, err := flavorDataDir()
	if err != nil {
		return normal
	}
	dbPath := filepath.Join(dir, "sessions.db")
	if _, err := os.Stat(dbPath); errors.Is(err, os.ErrNotExist) {
		return setupLaunchStateFor(false, false)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Printf("[setup-window] launch state: %v", err)
		return normal
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, "SELECT key, value FROM ui_state WHERE key IN (?, ?)", onboardingStateKey, setupWatchingStateKey)
	if err != nil {
		log.Printf("[setup-window] launch state: %v", err)
		return normal
	}
	defer rows.Close()
	var onboardingRaw, watchingRaw string
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			log.Printf("[setup-window] launch state: %v", err)
			return normal
		}
		switch key {
		case onboardingStateKey:
			onboardingRaw = value
		case setupWatchingStateKey:
			watchingRaw = value
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("[setup-window] launch state: %v", err)
		return normal
	}
	return setupLaunchStateFor(onboardingStateDone(onboardingRaw), watchingRaw == "1")
}
