//go:build darwin

package main

/*
#include <stdlib.h>

// Declaration only — the implementation lives in the preamble of
// quickChat_window_darwin.go. Splitting the //export functions out
// into their own file (with a preamble that contains no Objective-C
// class definitions) avoids CGO emitting the @implementation twice
// and producing duplicate-symbol linker errors.
extern void goCloseQuickChatWindow(void);
*/
import "C"

import (
	"log"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//export goQuickChatSubmit
func goQuickChatSubmit(cPrompt *C.char) {
	// Copy out of the C buffer immediately, then move all Wails IPC work
	// off this cgo callback thread. Calling wailsruntime.* inline from a
	// //export function invoked by a WKWebView message handler runs on the
	// main thread and reenters Cocoa from a state Wails wasn't expecting,
	// crashing the process when a suggestion is clicked.
	prompt := C.GoString(cPrompt)
	quickChatWindowMu.Lock()
	app := quickChatWindowApp
	bundleID := lastQuickChatBundleID
	quickChatWindowMu.Unlock()
	if app == nil || app.ctx == nil {
		return
	}

	C.goCloseQuickChatWindow()
	quickChatWindowMu.Lock()
	quickChatWindowOpen = false
	quickChatWindowMu.Unlock()

	go func() {
		// Bring Yansu forward and emit an event that App.tsx already
		// knows how to handle (opens a new chat, seeds the composer
		// with focused-app context, and auto-submits).
		wailsruntime.WindowShow(app.ctx)
		wailsruntime.Show(app.ctx)
		appName := ""
		if bundleID != "" {
			appName = bundleIDPrettyName(bundleID)
		}
		wailsruntime.EventsEmit(app.ctx, "quickChat:submit", QuickChatSubmitEvent{
			Prompt:            prompt,
			FrontmostBundleID: bundleID,
			FrontmostAppName:  appName,
		})
	}()
}

//export goQuickChatClosed
func goQuickChatClosed() {
	quickChatWindowMu.Lock()
	open := quickChatWindowOpen
	quickChatWindowOpen = false
	quickChatWindowMu.Unlock()
	if !open {
		return
	}
	C.goCloseQuickChatWindow()
}

//export goQuickChatRequestSuggestions
func goQuickChatRequestSuggestions(cBundleID *C.char) {
	bundleID := C.GoString(cBundleID)
	quickChatWindowMu.Lock()
	app := quickChatWindowApp
	quickChatWindowMu.Unlock()
	if app == nil {
		return
	}

	// Update the panel label to reflect what we're loading. This runs
	// before the (potentially slow) per-app pipeline below so the user
	// sees "Reading Discord…" rather than the default header.
	if bundleID != "" {
		app.pushQuickChatLoading("Reading " + bundleIDPrettyName(bundleID) + "…")
	} else {
		app.pushQuickChatLoading("Looking around…")
	}

	go func() {
		// Per-app path: capture context for the requested app, then ask
		// Haiku for chips grounded in ranked context: current app, same-app
		// history, then broad recent-work context. If capture/model work
		// produces nothing, use deterministic app-aware fallbacks instead of
		// stale cached composer chips.
		const totalBudget = 4 * time.Second
		done := make(chan []RecommendedQuestion, 1)
		go func() {
			questions, err := app.GetQuickChatSuggestionsForApp(bundleID, "")
			if err != nil {
				log.Printf("[quickChat] per-app suggestions failed: %v", err)
				done <- nil
				return
			}
			done <- questions
		}()

		var suggestions []RecommendedQuestion
		select {
		case s := <-done:
			suggestions = s
		case <-time.After(totalBudget):
			log.Printf("[quickChat] per-app suggestions timed out after %s", totalBudget)
		}
		if len(suggestions) == 0 {
			suggestions = defaultQuickChatQuestionsForApp(bundleID, "")
		}

		label := "Ask about your recent work"
		if bundleID != "" {
			label = "About " + bundleIDPrettyName(bundleID)
		}
		app.pushQuickChatSuggestions(suggestions, label)
	}()
}
