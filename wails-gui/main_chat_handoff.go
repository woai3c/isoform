package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"wails-gui/internal/engagement"
	"wails-gui/internal/handoff"
	"wails-gui/internal/pii"
	"wails-gui/storage"
)

// The result is a display snapshot of this transition, not a pointer to the
// execution chat's latest turn. Starting the work again must not change what
// Review opens on an earlier message. Proactive payloads stay out of prompts.
type mainChatHandoffWork struct {
	HandoffID     string                       `json:"handoffId"`
	Status        handoff.Status               `json:"status"`
	BlockedReason string                       `json:"blockedReason,omitempty"`
	Verification  *handoff.Verification        `json:"verification,omitempty"`
	Summary       string                       `json:"summary"`
	Result        string                       `json:"result"`
	Deliverables  []mainChatHandoffDeliverable `json:"deliverables"`
}

type mainChatHandoffDeliverable struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

func (a *App) postHandoffResult(entry handoff.Entry, transcript string) {
	if a == nil || a.store == nil || entry.ID == "" {
		return
	}
	kind := engagement.KindWorkFinished
	switch entry.Status {
	case handoff.StatusCompleted, handoff.StatusNeedsReview:
	case handoff.StatusBlocked, handoff.StatusAborted:
		kind = engagement.KindWorkBlocked
	default:
		return
	}

	if svc := a.engagementService(); svc != nil {
		// Decide before preparing the report. Dedicated results never wait on
		// the delivery pass, which can be rendering unrelated model copy.
		if svc.kindQuiet(kind) {
			id := mainChatEngagementMomentID(mainChatHandoffMomentID(entry))
			if existing, err := a.store.GetEngagementMoment(context.Background(), id); err == nil && existing == nil {
				if kind == engagement.KindWorkFinished {
					a.produceWorkFinished(entry)
				} else {
					a.produceWorkBlocked(entry)
				}
			}
			return
		}
	}

	title, _ := pii.RedactText(engagementTitle(entry.Title))
	if pii.IsSensitiveAppName(entry.AppName) {
		title, transcript = "", ""
	}
	clean, refs := parseDeliverableRefs(stripHandoffResultBlock(transcript))
	if entry.Result != nil && !pii.IsSensitiveAppName(entry.AppName) {
		for _, evidence := range entry.Result.Evidence {
			if evidence.Path != "" {
				// Rejected verification evidence must not gain a broader file
				// scope when it becomes a link in the main chat. Only pass the
				// safe canonical path into the legacy deliverable resolver.
				full, ok := resolveEvidencePath(evidence.Path, entry.Workdir)
				if !ok {
					continue
				}
				checked, err := checkedHandoffEvidencePath(entry, full)
				if err != nil {
					continue
				}
				refs = append(refs, deliverableRef{Path: checked})
			} else if evidence.URL != "" {
				// The existing URL checks below reject credentials and keep
				// permitted destinations unchanged.
				refs = append(refs, deliverableRef{Path: evidence.URL})
			}
		}
	}
	clean, _ = parseDeliveryTarget(clean)
	clean, _ = pii.RedactText(strings.TrimSpace(clean))
	work := &mainChatHandoffWork{
		HandoffID: entry.ID, Status: entry.Status, BlockedReason: entry.BlockedReason,
		Summary:      deliverableSummaryLine(clean, 400),
		Result:       tailBytes(clean, handoffVerifyTranscriptMax),
		Deliverables: []mainChatHandoffDeliverable{},
	}
	if entry.Verification != nil && !pii.IsSensitiveAppName(entry.AppName) {
		verification := *entry.Verification
		verification.Note, _ = pii.RedactText(verification.Note)
		work.Verification = &verification
	}
	if work.Summary == "" && entry.Verification != nil && !pii.IsSensitiveAppName(entry.AppName) {
		note, _ := pii.RedactText(entry.Verification.Note)
		work.Summary = deliverableSummaryLine(note, 400)
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if len(work.Deliverables) >= maxDeliverablePaths {
			break
		}
		link := a.mainChatHandoffDeliverable(entry, ref)
		if link.URL == "" || seen[link.URL] {
			continue
		}
		seen[link.URL] = true
		work.Deliverables = append(work.Deliverables, link)
	}
	key := mainChatHandoffMomentID(entry)
	target := mainChatTarget{Kind: mainChatTargetHandoff, ID: entry.ID}
	item := mainChatProactive{
		MomentID: key, Kind: string(kind), Target: target, State: "pending",
		ActionIDs: []string{"review", "stop", "dismiss"}, Work: work,
		Evidence:  []mainChatEvidenceRef{{Kind: mainChatSourceHandoff, ID: entry.ID, Target: target}},
		Localized: map[string]mainChatProactiveCopy{},
	}
	if entry.Status == handoff.StatusCompleted || entry.Status == handoff.StatusAborted {
		item.ActionIDs = []string{"review", "dismiss"}
	}
	if entry.UpdatedAt > 0 {
		item.Evidence[0].At = time.UnixMilli(entry.UpdatedAt).UTC().Format(time.RFC3339)
	}
	for _, lang := range engagement.Languages {
		copy := mainChatHandoffCopy(entry.Status, title, lang)
		if work.Summary != "" {
			copy.Body += "\n" + work.Summary
		}
		item.Localized[lang] = copy
	}
	copy, ok := item.Localized[a.currentUILanguage()]
	if !ok {
		copy = item.Localized["en"]
	}
	item.Headline, item.Body, item.ActionLabels = copy.Headline, copy.Body, copy.ActionLabels
	item.sourceHash = proactiveVoiceHash(entry)
	a.postProactiveItem(item, key)
}

func mainChatHandoffMomentID(entry handoff.Entry) string {
	if entry.AttemptID == "" {
		return mainChatReportKey(mainChatSourceHandoff, entry.ID, string(entry.Status), entry.BlockedReason, fmt.Sprint(entry.UpdatedAt))
	}
	return mainChatReportKey(mainChatSourceHandoff, entry.ID, entry.AttemptID, string(entry.Status), entry.BlockedReason, fmt.Sprint(entry.UpdatedAt))
}

func mainChatHandoffDeliverableLabel(label string) string {
	label = strings.Join(strings.Fields(sanitizeDeliverableDescription(label)), " ")
	if runes := []rune(label); len(runes) > maxDeliverableDescriptionRunes {
		label = string(runes[:maxDeliverableDescriptionRunes-3]) + "..."
	}
	return label
}

// Only declared outputs become links. The brief's KeyFiles can be inputs and
// must not be presented as produced deliverables. Local files use the existing
// chat path checks, resolved before the result moves into the main chat.
func (a *App) mainChatHandoffDeliverable(entry handoff.Entry, ref deliverableRef) mainChatHandoffDeliverable {
	path := strings.TrimSpace(ref.Path)
	if len(path) > 2048 || strings.ContainsAny(path, "\r\n|") {
		return mainChatHandoffDeliverable{}
	}
	if _, redactions := pii.RedactText(path); redactions > 0 {
		return mainChatHandoffDeliverable{}
	}
	label, _ := pii.RedactText(ref.Description)
	if parsed, err := url.Parse(path); err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		if parsed.Hostname() == "" || parsed.User != nil || !mainChatHandoffURLAllowed(parsed) {
			return mainChatHandoffDeliverable{}
		}
		if label == "" {
			label = parsed.Host
		}
		return mainChatHandoffDeliverable{Label: mainChatHandoffDeliverableLabel(label), URL: parsed.String()}
	}
	if !filepath.IsAbs(path) && filepath.IsAbs(entry.Workdir) {
		path = filepath.Join(entry.Workdir, path)
	}
	abs, err := a.resolveDeliverablePath(entry.ChatSessionID, path)
	if err != nil || !isOpenableDeliverableExt(abs) {
		return mainChatHandoffDeliverable{}
	}
	if label == "" {
		label = filepath.Base(abs)
	}
	return mainChatHandoffDeliverable{Label: mainChatHandoffDeliverableLabel(label), URL: abs}
}

// Deliverable URLs are destinations, not observation metadata. Preserve their
// port, query and fragment; refuse credential-bearing links instead of quietly
// changing their destination with the observation URL redactor.
func mainChatHandoffURLAllowed(link *url.URL) bool {
	decoded, err := url.QueryUnescape(link.String())
	if err != nil {
		return false
	}
	if _, redactions := pii.RedactText(decoded); redactions > 0 {
		return false
	}
	parts := []string{link.RawQuery}
	if strings.Contains(link.Fragment, "=") {
		parts = append(parts, link.Fragment)
	}
	for _, part := range parts {
		query, err := url.ParseQuery(part)
		if err != nil {
			return false
		}
		for key := range query {
			key = strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(key))
			switch key {
			case "token", "accesstoken", "refreshtoken", "idtoken", "authtoken", "oauthtoken", "auth", "authorization", "password", "passwd", "apikey", "secret", "signature", "sig", "credential", "session", "sessionid", "code", "xamzsignature", "xamzcredential", "xamzsecuritytoken", "xgoogsignature", "xgoogcredential":
				return false
			}
		}
	}
	return true
}

func mainChatHandoffCopy(status handoff.Status, title, lang string) mainChatProactiveCopy {
	if lang == "zh-CN" {
		if title == "" {
			title = "这项工作"
		}
		copy := mainChatProactiveCopy{Headline: "工作结果已准备好。", Body: title + "的结果可以查看了。", ActionLabels: map[string]string{"review": "查看", "stop": "停止", "dismiss": "忽略"}}
		switch status {
		case handoff.StatusNeedsReview:
			copy.Headline, copy.Body = "工作结果需要你检查。", title+"已结束，但结果仍需你检查。"
		case handoff.StatusBlocked:
			copy.Headline, copy.Body = "工作遇到了阻碍。", title+"暂时无法继续，需要你查看。"
		case handoff.StatusAborted:
			copy.Headline, copy.Body = "工作已停止。", title+"在完成前停止了。"
		}
		return copy
	}
	if title == "" {
		title = "This work"
	}
	copy := mainChatProactiveCopy{Headline: "Work finished.", Body: title + " is ready to review.", ActionLabels: map[string]string{"review": "Review", "stop": "Stop", "dismiss": "Dismiss"}}
	switch status {
	case handoff.StatusNeedsReview:
		copy.Headline, copy.Body = "Work needs your review.", title+" finished, but its result needs your review."
	case handoff.StatusBlocked:
		copy.Headline, copy.Body = "Work is blocked.", title+" is blocked and needs your attention."
	case handoff.StatusAborted:
		copy.Headline, copy.Body = "Work stopped.", title+" stopped before finishing."
	}
	return copy
}

func (a *App) resolveMainChatHandoffAction(card *mainChatProactive, actionID string) (string, error) {
	if card.Work == nil || card.Work.HandoffID == "" || card.Target != (mainChatTarget{Kind: mainChatTargetHandoff, ID: card.Work.HandoffID}) {
		return "", errors.New("work message is invalid")
	}
	switch actionID {
	case "dismiss":
		return storage.EngagementReceiptDismissed, nil
	case "stop":
		if err := a.abortHandoff(card.Work.HandoffID, card.MomentID); err != nil {
			return "", err
		}
		return storage.EngagementReceiptActed, nil
	case "review":
		ctx := context.Background()
		main, err := a.ensureMainChat(ctx)
		if err != nil {
			return "", err
		}
		mainChatPostMu.Lock()
		defer mainChatPostMu.Unlock()
		id := "handoff-review:" + engagement.HashPart(card.MomentID)
		if row, err := a.store.LoadStreamItem(ctx, id); err != nil {
			return "", err
		} else if row != nil {
			return storage.EngagementReceiptOpened, nil
		}
		result := card.Work.Result
		if result == "" {
			result = card.Body
			if copy, ok := card.Localized[a.currentUILanguage()]; ok {
				result = copy.Body
			}
		}
		var paths []string
		for _, link := range card.Work.Deliverables {
			if filepath.IsAbs(link.URL) {
				paths = append(paths, link.URL+" | "+link.Label)
			} else {
				label := strings.NewReplacer("[", "", "]", "", "\n", " ").Replace(link.Label)
				result += "\n\n[" + label + "](<" + link.URL + ">)"
			}
		}
		if len(paths) > 0 {
			result += "\n\n<deliverables>\n" + strings.Join(paths, "\n") + "\n</deliverables>"
		}
		if a.appendMainChatItem(ctx, main.LocalID, storage.StreamItemRow{ID: id, Kind: "assistant", Content: result}, mainChatSourceHandoff) == "" {
			return "", errors.New("work result could not be opened")
		}
		return storage.EngagementReceiptOpened, nil
	default:
		return "", errors.New("action is not available for this work")
	}
}
