package main

import (
	"context"
	"strings"
	"time"

	"wails-gui/internal/pii"
)

const uiLanguageStateKey = "ui.language"

func normalizeUILanguage(lang string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "zh") {
		return "zh-CN"
	}
	return "en"
}

func uiLanguageDisplayName(lang string) string {
	switch normalizeUILanguage(lang) {
	case "zh-CN":
		return "Simplified Chinese"
	default:
		return "English"
	}
}

func (a *App) currentUILanguage() string {
	return a.currentUILanguageContext(context.Background())
}

// Display readers with a latency budget must not add an unbounded settings
// query after their source/privacy read has exhausted that budget.
func (a *App) currentUILanguageContext(ctx context.Context) string {
	if a == nil || a.store == nil {
		return "en"
	}
	value, err := a.store.GetUIState(ctx, uiLanguageStateKey)
	if err != nil {
		return "en"
	}
	return normalizeUILanguage(value)
}

// Presentation must not add an unbounded settings read to a source read.
// Resolve this before publication fences; timed-out reads use the normal
// English fallback and do not cache it.
func (a *App) modelDisplayLanguage() string {
	return a.modelDisplayLanguageContext(context.Background())
}

func (a *App) modelDisplayLanguageContext(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	return a.currentUILanguageContext(ctx)
}

func (a *App) currentUILanguageName() string {
	return uiLanguageDisplayName(a.currentUILanguage())
}

func responseLanguageInstruction(languageName string) string {
	languageName = strings.TrimSpace(languageName)
	if languageName == "" {
		languageName = "English"
	}
	return pii.ModelOutputInstructions + "The user's UI language is " + languageName + ". Unless the user explicitly asks for another language, write user-facing responses and generated titles/descriptions in " + languageName + "."
}
