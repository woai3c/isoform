package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/storage"
)

// The first-read decision cannot observe a saved brief before its chat
// notification has been submitted to engagement.
var firstReadBriefMu sync.Mutex

const firstReadWaitingForBriefKey = "first_read.waiting_for_brief"

// firstReadWaitsForBrief keeps the introduction behind the first brief while
// its engagement item is still waiting to be written to chat. A suppressed or
// disabled notice does not block the introduction.
func (a *App) firstReadWaitsForBrief(ctx context.Context, brief MobileBriefV1) bool {
	installed := a.firstWeekInstall(ctx)
	if installed.IsZero() || a.engagement == nil {
		return false
	}
	day := engagement.FirstWeekDay(installed, a.engagement.now())
	if day < 0 || day > 1 {
		return false
	}
	keys := []string{engagement.DedupeKey(engagement.KindBriefReady, brief.BriefID), engagement.DedupeKey(engagement.KindFirstWeek, installed.Format(time.RFC3339Nano), "brief")}
	for _, key := range keys {
		pending, err := a.store.GetOpenEngagementMomentByKey(ctx, key)
		if err != nil {
			return true
		}
		if pending == nil {
			continue
		}
		main, err := a.store.LoadMainSession(ctx)
		if err != nil || main == nil {
			return true
		}
		row, err := a.store.FindProactiveStreamItem(ctx, main.LocalID, pending.ID)
		if err != nil || row == nil {
			return true
		}
	}
	return false
}

// firstReadFacts must be collected from currently readable sources. Empty
// values mean that source did not establish the corresponding fact.
type firstReadFacts struct {
	Person    string
	BriefTime string
	Tool      string
}

type firstReadContent struct {
	Sentences  []string
	Help       []string
	OfferCount int
}

// buildFirstRead accepts the portrait's read projection, rather than a draft:
// that projection has already removed sentences whose evidence is no longer
// readable. The caller must obtain it again at delivery time.
func buildFirstRead(portrait AboutYouPortrait, facts firstReadFacts, offerCount int, language string) *firstReadContent {
	content := &firstReadContent{}
	for _, sentence := range portrait.Sentences {
		var text strings.Builder
		for _, part := range sentence.Parts {
			text.WriteString(part.Text)
		}
		if line := strings.TrimSpace(text.String()); line != "" {
			content.Sentences = append(content.Sentences, line)
		}
		if len(content.Sentences) == 2 {
			break
		}
	}
	if len(content.Sentences) == 0 {
		return nil
	}
	zh := normalizeUILanguage(language) == "zh-CN"
	if person := strings.TrimSpace(facts.Person); person != "" {
		if zh {
			content.Help = append(content.Help, fmt.Sprintf("帮你准备给 %s 的回复。", person))
		} else {
			content.Help = append(content.Help, fmt.Sprintf("Help you prepare a reply to %s.", person))
		}
	}
	if at := strings.TrimSpace(facts.BriefTime); at != "" {
		if zh {
			content.Help = append(content.Help, fmt.Sprintf("在 %s 给你简报。", at))
		} else {
			content.Help = append(content.Help, fmt.Sprintf("Brief you at %s.", at))
		}
	}
	if tool := strings.TrimSpace(facts.Tool); tool != "" {
		if zh {
			content.Help = append(content.Help, fmt.Sprintf("%s 的工作需要你时告诉你。", tool))
		} else {
			content.Help = append(content.Help, fmt.Sprintf("Tell you when %s work needs you.", tool))
		}
	}
	if len(content.Help) < 2 {
		content.Help = nil
	}
	content.OfferCount = min(max(offerCount, 0), 3)
	return content
}

// A pairing includes its creation time, so pairing the same device again gets
// a new identity without reopening an old pairing's marker.
func firstReadIdentity(device MobileDeviceView) string {
	return engagement.HashPart(device.DeviceID + "\x00" + device.CreatedAt)
}

func firstReadMoment(device MobileDeviceView) string {
	return "first-read:" + firstReadIdentity(device)
}

func firstReadSentenceHash(sentences []string) string {
	raw, _ := json.Marshal(sentences)
	return agencyPayloadHash(raw)
}

// postFirstRead persists only the portrait version, never a copy of the
// portrait. The stable row id covers a crash between append and marker write.
func (a *App) postFirstRead(ctx context.Context, device MobileDeviceView, portrait AboutYouPortrait) string {
	if a == nil || a.store == nil || device.DeviceID == "" || device.CreatedAt == "" || portrait.GeneratedAt <= 0 {
		return ""
	}
	content := buildFirstRead(portrait, firstReadFacts{}, 0, "en")
	if content == nil {
		return ""
	}
	main, err := a.ensureMainChat(ctx)
	if err != nil {
		return ""
	}
	mainChatPostMu.Lock()
	defer mainChatPostMu.Unlock()
	moment := firstReadMoment(device)
	marker := "first_read.pairing." + firstReadIdentity(device)
	if value, err := a.store.GetUIState(ctx, marker); err != nil || value != "" {
		return ""
	}
	rowID := "proactive:" + engagement.HashPart(moment)
	row, err := a.store.LoadStreamItem(ctx, rowID)
	if err != nil {
		return ""
	}
	if row == nil {
		item := mainChatProactive{MomentID: moment, Kind: "first_read", Headline: "What I see so far",
			FirstReadVersion: portrait.GeneratedAt, FirstReadHash: firstReadSentenceHash(content.Sentences), State: "pending", Target: mainChatTarget{Kind: mainChatTargetNone},
			ActionIDs: []string{"confirm_portrait", "correct_portrait"}, ActionLabels: map[string]string{}, Evidence: []mainChatEvidenceRef{}}
		payload, err := json.Marshal(item)
		if err != nil {
			return ""
		}
		if a.appendMainChatItem(ctx, main.LocalID, storage.StreamItemRow{ID: rowID, Kind: "system",
			ToolName: mainChatProactiveTool, ToolInput: string(payload)}, mainChatSourceEngagement) == "" {
			return ""
		}
	}
	if err := a.store.SetUIState(ctx, marker, rowID); err != nil {
		return ""
	}
	return rowID
}

// firstReadFactsNow uses only currently enabled sources. Adaptive schedules do
// not promise a fixed time, even when a default clock is configured.
func (a *App) firstReadFactsNow(ctx context.Context, history AssistantHistoryView) firstReadFacts {
	var facts firstReadFacts
	if a.store != nil {
		people, err := a.store.ListPrincipals(ctx)
		if err == nil {
			for _, person := range people {
				if person.Kind != storage.PrincipalKindContact || !a.proactiveVoicePersonAllowed(ctx, person) {
					continue
				}
				last, err := a.store.PrincipalLastSeenAt(ctx, person.ID)
				if err == nil && last > 0 && last <= time.Now().UnixMilli() {
					facts.Person = mobileOfferText(person.DisplayName, 80)
					if facts.Person != "" {
						break
					}
				}
			}
		}
	}
	if history.Enabled && history.Pass2Done && len(history.Projects) > 0 {
		switch true {
		case containsString(history.Assistants, "claude"):
			facts.Tool = "Claude Code"
		case containsString(history.Assistants, "codex"):
			facts.Tool = "Codex"
		}
	}
	if schedule, err := a.GetMobileBriefSchedule(); err == nil && schedule.Enabled &&
		schedule.AdaptiveTiming != nil && !*schedule.AdaptiveTiming && mobileBriefClockPattern.MatchString(schedule.Morning) {
		facts.BriefTime = schedule.Morning
	}
	return facts
}

// maybePostFirstReads is called after the history scan, portrait publication,
// and pairing. Each can finish first; a later event retries the same marker.
func (a *App) maybePostFirstReads(ctx context.Context) {
	if a == nil || a.store == nil || a.IsClosedEyesModeEnabled() || ctx.Err() != nil {
		return
	}
	history := a.GetAssistantHistory()
	if !history.Enabled || !history.Pass2Done || history.State == "reading" {
		return
	}
	// On a first install the requested brief precedes this introduction. A
	// later pairing on an existing install can use its already stored brief.
	firstReadBriefMu.Lock()
	brief, ok := a.latestMobileBrief()
	if ok {
		// Set before checking the moment: a delivery pass can finish while
		// the read below is in flight and must see that a retry is needed.
		_ = a.store.SetUIState(ctx, firstReadWaitingForBriefKey, "true")
	}
	waiting := ok && a.firstReadWaitsForBrief(ctx, brief)
	if ok && !waiting {
		_ = a.store.SetUIState(ctx, firstReadWaitingForBriefKey, "")
	}
	firstReadBriefMu.Unlock()
	if !ok {
		return
	}
	if waiting {
		return
	}
	portrait, err := a.GetAboutYouPortrait()
	if err != nil || buildFirstRead(portrait, firstReadFacts{}, 0, "en") == nil {
		return
	}
	bridge := a.mobileBridgeForUse()
	if bridge == nil {
		return
	}
	bridge.mu.Lock()
	var devices []MobileDeviceView
	for _, record := range bridge.devices {
		if !record.approving && mobileHasScope(record.Scopes, mobileScopeChatRead) {
			devices = append(devices, record.MobileDeviceView)
		}
	}
	bridge.mu.Unlock()
	for _, device := range devices {
		if a.postFirstRead(ctx, device, portrait) != "" {
			if main, err := a.store.LoadMainSession(ctx); err == nil && main != nil {
				// The existing chat event wakes an open transcript. An empty
				// turn id cannot acknowledge the person's outgoing message.
				bridge.publishChatTurn(main.LocalID, "", mobileChatEventDone)
			}
		}
	}
}

// projectFirstRead rechecks portrait support and offer visibility on every
// desktop or phone read. A changed version cannot be confirmed as the one
// that was originally presented.
func (a *App) projectFirstRead(ctx context.Context, item *mainChatProactive, language string) {
	if item.State == "corrected" {
		*item = mainChatProactive{MomentID: item.MomentID, Kind: "thread_update_unavailable", Target: mainChatTarget{Kind: mainChatTargetNone}}
		return
	}
	portrait, err := a.GetAboutYouPortrait()
	if err != nil || portrait.GeneratedAt != item.FirstReadVersion || a.IsClosedEyesModeEnabled() {
		*item = mainChatProactive{MomentID: item.MomentID, Kind: "thread_update_unavailable", Target: mainChatTarget{Kind: mainChatTargetNone}}
		return
	}
	count := 0
	if offers, err := a.mobileOffers(ctx); err == nil {
		count = len(offers.Offers)
	}
	facts := a.firstReadFactsNow(ctx, a.GetAssistantHistory())
	content := buildFirstRead(portrait, facts, count, language)
	if content == nil || firstReadSentenceHash(content.Sentences) != item.FirstReadHash {
		*item = mainChatProactive{MomentID: item.MomentID, Kind: "thread_update_unavailable", Target: mainChatTarget{Kind: mainChatTargetNone}}
		return
	}
	item.Sentences, item.Helps, item.OfferCount = content.Sentences, content.Help, content.OfferCount
	item.Body = strings.Join(content.Sentences, " ")
	item.Localized = map[string]mainChatProactiveCopy{}
	for _, lang := range []string{"en", "zh-CN"} {
		localized := buildFirstRead(portrait, facts, count, lang)
		item.Localized[lang] = mainChatProactiveCopy{Headline: item.Headline, Body: item.Body,
			ActionLabels: map[string]string{}, Helps: localized.Help}
	}
	item.FirstReadHash = ""
}
