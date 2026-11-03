package main

import (
	"encoding/json"
	"reflect"
	"strings"

	"wails-gui/internal/pii"
)

// displayModelCopy projects human-readable fields after source admission. It
// copies containers so a display read cannot rewrite stored masks or cached
// evidence. IDs, URLs, timestamps, digests and private provenance stay exact.
// This is a read boundary, never a model-input or permission transformation.
func displayModelCopy[T any](value T, language string) T {
	return displayModelValue(reflect.ValueOf(value), language, false).Interface().(T)
}

func displayModelValue(value reflect.Value, language string, text bool) reflect.Value {
	switch value.Kind() {
	case reflect.String:
		if text {
			out := reflect.New(value.Type()).Elem()
			out.SetString(pii.DisplayModelText(value.String(), language))
			return out
		}
	case reflect.Struct:
		out := reflect.New(value.Type()).Elem()
		out.Set(value)
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			if field.PkgPath != "" {
				continue
			}
			readable := false
			switch field.Name {
			case "Text", "Title", "Name", "Names", "TopicName", "Detail", "Summary", "NextStep", "Question", "UserActed", "AttentionReason", "Label", "Prompt", "Message", "Preview", "SourceLabel", "BlockedReason", "Reason", "InterestWhy", "Headline", "Body", "Description", "Content", "SuggestedFollowup", "ActionLabels", "Params", "Why", "Original", "Sentences", "Helps":
				readable = true
			}
			out.Field(i).Set(displayModelValue(value.Field(i), language, readable))
		}
		return out
	case reflect.Pointer:
		if !value.IsNil() {
			out := reflect.New(value.Type().Elem())
			out.Elem().Set(displayModelValue(value.Elem(), language, text))
			return out
		}
	case reflect.Map:
		if !value.IsNil() {
			out := reflect.MakeMapWithSize(value.Type(), value.Len())
			iter := value.MapRange()
			for iter.Next() {
				out.SetMapIndex(iter.Key(), displayModelValue(iter.Value(), language, text))
			}
			return out
		}
	case reflect.Slice:
		if !value.IsNil() {
			out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
			for i := 0; i < value.Len(); i++ {
				out.Index(i).Set(displayModelValue(value.Index(i), language, text))
			}
			return out
		}
	}
	return value
}

// speechDisplayText is shared by chat read-aloud and the brief's TTS input.
func speechDisplayText(text, language string) string {
	return pii.DisplayModelText(text, language)
}

func displayModelToolInput(toolName, raw, language string) string {
	if !strings.Contains(strings.ToUpper(raw), "[REDACTED") {
		return raw
	}
	var payload any
	switch toolName {
	case mainChatProactiveTool:
		payload = &mainChatProactive{}
	case mainChatReportTool:
		payload = &mainChatReport{}
	case mainChatStatusTool:
		payload = &mainChatStatus{}
	default:
		return raw
	}
	if json.Unmarshal([]byte(raw), payload) != nil {
		return raw
	}
	shown := displayModelValue(reflect.ValueOf(payload), language, false)
	encoded, err := json.Marshal(shown.Interface())
	if err != nil {
		return raw
	}
	return string(encoded)
}

// Inspect legacy audio before hygiene can discard a mask-bearing line. Bound
// audio is checked against the canonical sanitized text by AudioForRead.
func mobileBriefDisplay(brief MobileBriefV1, language string) MobileBriefV1 {
	display := displayModelCopy(brief, language)
	if !reflect.DeepEqual(brief, display) && display.Audio != nil && display.Audio.binding == "" {
		display.Audio = nil
	}
	if display.Cards != nil {
		cards := make([]MobileBriefCard, 0, len(display.Cards))
		for i, card := range display.Cards {
			// Admission must inspect the original subject before display copy
			// turns a privacy marker into ordinary words.
			source := brief.Cards[i]
			if !mobileBriefPrivacyMask.MatchString(source.Label) && !mobileBriefPrivacyMask.MatchString(source.Message) {
				cards = append(cards, card)
			}
		}
		display.Cards = cards
	}
	return mobileBriefHygiene(display)
}
