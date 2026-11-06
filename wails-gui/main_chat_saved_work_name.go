package main

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

const savedChatWorkNameRepairKey = "saved_chat_work_names_v2"
const savedWorkNameField = "savedWorkNameV1"

var savedWorkCommitClause = regexp.MustCompile(`(?i)\bon\s+[0-9a-f]{7,40}$`)

// savedThreadWorkName accepts a complete title or a complete first clause. It
// does not clip a word or invent a name when the source has no clear boundary.
func savedThreadWorkName(source, body string) string {
	source = strings.TrimSpace(source)
	if strings.ContainsAny(source, "\r\n") {
		return ""
	}
	if strings.Contains(source, ". ") || strings.ContainsAny(source, "!?！？") {
		return ""
	}
	if utf8.RuneCountInString(source) > threadOutcomeTitleLimit {
		at := strings.Index(strings.ToLower(source), " and ")
		if at <= 0 {
			return ""
		}
		source = strings.TrimSpace(source[:at])
		// A bare conjunction can sit inside a name (Research and
		// Development). Only a complete commit reference is unambiguous.
		if !savedWorkCommitClause.MatchString(source) {
			return ""
		}
	}
	source = strings.TrimRight(source, " .。")
	words := strings.Fields(source)
	if len(words) > 0 {
		switch strings.ToLower(words[len(words)-1]) {
		case "a", "an", "and", "at", "for", "from", "in", "of", "on", "or", "the", "to", "with":
			return ""
		}
	}
	if source == "" || utf8.RuneCountInString(source) > threadOutcomeTitleLimit || strings.EqualFold(source, strings.TrimRight(strings.TrimSpace(body), ".。")) {
		return ""
	}
	return source
}

// savedThreadFallbackName removes a dangling fragment from a saved title only
// when it is a literal prefix of the readable source. It never adds words.
func savedThreadFallbackName(source, headline, body string) string {
	source = strings.TrimSpace(source)
	headline = strings.TrimSpace(headline)
	clean := func(value string) string { return strings.TrimRight(strings.TrimSpace(value), " .。") }
	if source != "" && !strings.ContainsAny(source, "\r\n!?！？") && !strings.Contains(source, ". ") && utf8.RuneCountInString(clean(source)) <= threadOutcomeTitleLimit && strings.EqualFold(clean(source), clean(headline)) && strings.EqualFold(clean(source), clean(body)) {
		return clean(source)
	}
	if headline == "" || strings.ContainsAny(headline, "\r\n") || !strings.HasPrefix(source, headline) || len(source) <= len(headline) || utf8.RuneCountInString(headline) > threadOutcomeTitleLimit {
		return ""
	}
	// Only the known dangling prepositions signal a clipped
	// phrase; a word boundary alone does not prove a phrase is complete.
	words := strings.Fields(headline)
	if len(words) < 2 {
		return ""
	}
	switch strings.ToLower(words[len(words)-1]) {
	case "at", "for", "from", "in", "of", "on", "to", "with":
		candidate := strings.TrimRight(strings.TrimSpace(headline[:strings.LastIndex(headline, words[len(words)-1])]), " .。")
		remaining := strings.Fields(candidate)
		if len(remaining) < 2 {
			return ""
		}
		switch strings.ToLower(remaining[len(remaining)-1]) {
		case "a", "an", "and", "at", "for", "from", "in", "of", "on", "or", "the", "to", "with":
			return ""
		}
		return candidate
	default:
		return ""
	}
}

// rewriteSavedWorkNames preserves fields unknown to this binary and visits
// children recursively. resolve returns an empty name when evidence is held
// or there is no safe complete name.
func rewriteSavedWorkNames(input string, resolve func(target, source, body, headline string) string) (string, bool) {
	var root map[string]json.RawMessage
	if json.Unmarshal([]byte(input), &root) != nil || root == nil {
		return input, false
	}
	var rewrite func(map[string]json.RawMessage) bool
	rewrite = func(card map[string]json.RawMessage) bool {
		changed := false
		var kind, source, body, headline, saved string
		var target struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
		}
		_ = json.Unmarshal(card["kind"], &kind)
		_ = json.Unmarshal(card["target"], &target)
		_ = json.Unmarshal(card["activityOutcomeSource"], &source)
		_ = json.Unmarshal(card["body"], &body)
		_ = json.Unmarshal(card["headline"], &headline)
		_ = json.Unmarshal(card[savedWorkNameField], &saved)
		if kind == "decision_due" && target.Kind == mainChatTargetThread && target.ID != "" && source != "" {
			if name := resolve(target.ID, source, body, headline); saved == "" && name != "" {
				// This additive presentation field is ignored by the receipt's
				// typed hash, preserving the original source-bound card proof.
				card[savedWorkNameField], _ = json.Marshal(name)
				changed = true
			}
		}
		for _, field := range []string{"items", "entries"} {
			var children []json.RawMessage
			if json.Unmarshal(card[field], &children) != nil {
				continue
			}
			childChanged := false
			for i, raw := range children {
				var child map[string]json.RawMessage
				if json.Unmarshal(raw, &child) == nil && child != nil && rewrite(child) {
					children[i], _ = json.Marshal(child)
					childChanged = true
				}
			}
			if childChanged {
				card[field], _ = json.Marshal(children)
				changed = true
			}
		}
		return changed
	}
	if !rewrite(root) {
		return input, false
	}
	raw, err := json.Marshal(root)
	if err != nil {
		return input, false
	}
	return string(raw), true
}

// The saved name is applied only after the card's original receipt and
// source have passed the normal delivery gates. Match by moment and source,
// and recheck the name against the current readable source.
func (a *App) applySavedWorkNames(ctx context.Context, input string, item *mainChatProactive) bool {
	var root map[string]json.RawMessage
	if json.Unmarshal([]byte(input), &root) != nil {
		return false
	}
	names := map[string]string{}
	var collect func(map[string]json.RawMessage)
	collect = func(card map[string]json.RawMessage) {
		var id, source, name string
		_ = json.Unmarshal(card["momentId"], &id)
		_ = json.Unmarshal(card["activityOutcomeSource"], &source)
		_ = json.Unmarshal(card[savedWorkNameField], &name)
		if id != "" && source != "" && name != "" {
			names[id+"\x00"+source] = name
		}
		for _, field := range []string{"items", "entries"} {
			var children []json.RawMessage
			if json.Unmarshal(card[field], &children) == nil {
				for _, raw := range children {
					var child map[string]json.RawMessage
					if json.Unmarshal(raw, &child) == nil && child != nil {
						collect(child)
					}
				}
			}
		}
	}
	collect(root)
	if len(names) == 0 {
		return false
	}
	var apply func(*mainChatProactive) bool
	apply = func(card *mainChatProactive) bool {
		changed := false
		name := names[card.MomentID+"\x00"+card.ActivityOutcomeSource]
		if name != "" && card.Kind == "decision_due" && card.Target.Kind == mainChatTargetThread && (name == a.savedThreadWorkNameFromSource(ctx, card.Target.ID, card.ActivityOutcomeSource, card.Body, card.Headline) || name == a.savedThreadWorkNameFromSource(ctx, card.Target.ID, card.ActivityOutcomeSource, card.Body, "")) {
			card.Headline = name
			for lang, copy := range card.Localized {
				copy.Headline = name
				card.Localized[lang] = copy
			}
			changed = true
		}
		for i := range card.Items {
			changed = apply(&card.Items[i]) || changed
		}
		return changed
	}
	return apply(item)
}

func (a *App) savedThreadWorkNameFromSource(ctx context.Context, target, source, body, headline string) string {
	if a == nil || a.activity == nil || a.activity.store == nil {
		return ""
	}
	if !strings.HasPrefix(source, threadOutcomeMomentPrefix+target+":") || !a.threadOutcomeSourceReadable(ctx, source) {
		return ""
	}
	row, err := a.activity.store.GetActivityByIDContext(ctx, target)
	if err != nil || row == nil {
		return ""
	}
	name := savedThreadFallbackName(row.Title, headline, body)
	if name == "" {
		name = savedThreadWorkName(row.Title, body)
	}
	if name == "" || proactiveVoiceRestrictedText(name) || redactThreadOutcomeText(name) != name || activityPrivacyMarkerStrip(name) != name {
		return ""
	}
	return name
}

// Run after Activity opens, away from first paint. The marker is written only
// after every compare-and-swap succeeds, so interruptions and races retry.
func (a *App) repairSavedChatWorkNames(ctx context.Context) error {
	if a == nil || a.store == nil || a.activity == nil || a.activity.store == nil || a.IsClosedEyesModeEnabled() {
		return nil
	}
	if _, err := a.store.LoadMainSession(ctx); err != nil {
		return err
	}
	done, err := a.store.GetUIState(ctx, savedChatWorkNameRepairKey)
	if err != nil || done == "done" {
		return err
	}
	rows, err := a.store.SavedProactiveInputs(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		if a.IsClosedEyesModeEnabled() {
			return errors.New("observation paused during title repair")
		}
		after, changed := rewriteSavedWorkNames(row.ToolInput, func(target, source, body, headline string) string {
			return a.savedThreadWorkNameFromSource(ctx, target, source, body, headline)
		})
		if !changed {
			continue
		}
		updated, err := a.store.ReplaceProactiveInputIfUnchanged(ctx, row.ID, row.ToolInput, after)
		if err != nil {
			return err
		}
		if !updated {
			return errors.New("saved card changed during title repair")
		}
	}
	if a.IsClosedEyesModeEnabled() {
		return errors.New("observation paused during title repair")
	}
	return a.store.SetUIState(ctx, savedChatWorkNameRepairKey, "done")
}
