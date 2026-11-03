package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"wails-gui/internal/engagement"
)

// A fallback is delivery-only copy, derived from the receipt's source after
// the caller verifies its presentation and current provenance. Evidence IDs
// are never used as source authority. Stored cards and receipts stay intact.
func (a *App) projectProactiveCommitmentSentence(ctx context.Context, item *mainChatProactive, lang string) bool {
	if item.Kind != string(engagement.KindCommitmentSlipping) {
		return true
	}
	var moment engagement.Moment
	generated := item.VoiceReceipt != ""
	if generated {
		raw, err := a.store.GetUIState(ctx, proactiveVoiceReceiptPrefix+item.VoiceReceipt)
		var receipt proactiveVoiceReceipt
		if err != nil || json.Unmarshal([]byte(raw), &receipt) != nil {
			return false
		}
		moment = receipt.Moment
	} else {
		raw, err := a.store.GetUIState(ctx, engagementCardReceiptPrefix+item.ReadReceipt)
		var receipt engagementCardReadReceipt
		if err != nil || json.Unmarshal([]byte(raw), &receipt) != nil {
			return false
		}
		row, err := a.store.GetEngagementMoment(ctx, receipt.MomentID)
		if err != nil || row == nil {
			return false
		}
		moment = momentFromRow(*row)
	}
	if moment.Kind != engagement.KindCommitmentSlipping || moment.SubjectID == "" {
		return false
	}
	now := time.Now()
	if a.engagement != nil {
		now = a.engagement.now()
	}
	detail, allowed := a.proactiveVoicePurpose(ctx, moment.SubjectID, now)
	if !allowed || a.IsClosedEyesModeEnabled() {
		return false
	}
	current, eligible := engagementCommitmentSlippingMoment(detail, now)
	if !eligible {
		return false
	}
	// Authorization alone does not make source prose safe to display. Use the
	// generated-copy projection for its restricted-content rejection, PII/code
	// redaction and link stripping before adding a delivery-only sentence.
	source, reason := a.loadProactiveVoiceSource(ctx, moment, now, lang)
	if reason != "" {
		return false
	}
	job, _, reason := assembleProactiveVoiceJob(item.Kind, lang, true, now, source)
	if reason != "" || len(job.Items) != 1 || job.Items[0].Title == nil || strings.TrimSpace(*job.Items[0].Title) == "" {
		return false
	}
	originalTitle := *job.Items[0].Title
	title := commitmentShortName(originalTitle)
	// Recheck the original receipt after resolving display fields: a changed
	// source must not lend a new title or due date to an older presentation.
	hash := proactiveVoicePresentationHash(*item)
	if generated {
		allowed = a.proactiveVoiceReceiptAllowsRead(ctx, item.VoiceReceipt, item.Kind, hash, false)
	} else {
		allowed = a.engagementCardAllowsRead(ctx, item.ReadReceipt, hash)
	}
	if !allowed {
		return false
	}
	// Reword only the exact, verified source title after receipt validation.
	// This preserves the historical receipt and never substitutes guessed facts.
	if title != originalTitle {
		shorten := func(text string) string {
			text = strings.ReplaceAll(text, originalTitle, title)
			return strings.ReplaceAll(text, "The "+title, title)
		}
		item.Headline, item.Body = shorten(item.Headline), shorten(item.Body)
		for i := range item.Sentences {
			item.Sentences[i] = shorten(item.Sentences[i])
		}
		for language, copy := range item.Localized {
			copy.Headline, copy.Body = shorten(copy.Headline), shorten(copy.Body)
			item.Localized[language] = copy
		}
	}
	for _, language := range engagement.Languages {
		copy := item.Localized[language]
		if strings.TrimSpace(copy.Body) != "" && strings.TrimSpace(copy.Body) != strings.TrimSpace(copy.Headline) {
			continue
		}
		if item.Localized == nil {
			item.Localized = map[string]mainChatProactiveCopy{}
		}
		if copy.Headline == "" {
			catalog, _ := engagement.Copy(moment.Kind, language)
			copy.Headline, copy.ActionLabels = catalog.PushBody, catalog.Actions
		}
		copy.Body = proactiveCommitmentSentence(title, current.DueAt, now, language)
		item.Localized[language] = copy
	}
	if strings.TrimSpace(item.Body) == "" || strings.TrimSpace(item.Body) == strings.TrimSpace(item.Headline) {
		item.Body = item.Localized[lang].Body
	}
	return true
}

func proactiveCommitmentSentence(title string, due, now time.Time, lang string) string {
	due = due.In(now.Location())
	clock := observationMenuClock(due, lang) // Same display clock as lib/brief-time.ts.
	chinese := normalizeUILanguage(lang) == "zh-CN"
	if due.Format("2006-01-02") == now.Format("2006-01-02") {
		if chinese {
			return title + " · 今天" + clock + "到期"
		}
		return title + " · due today " + clock
	}
	if chinese {
		return fmt.Sprintf("%s · %d月%d日 %s到期", title, due.Month(), due.Day(), clock)
	}
	return title + " · due " + due.Format("Jan 2") + " at " + clock
}
