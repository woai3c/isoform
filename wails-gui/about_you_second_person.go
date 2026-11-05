package main

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// beliefSelfVerbs are the verbs a subjectless belief title starts with
// ("Uses design reviews…"). Only these become "You …", so a title that
// starts with a noun ("Tools for…", "News about…") stays as it is.
var beliefSelfVerbs = map[string]bool{
	"accept": true, "block": true, "decline": true, "get": true, "give": true, "guard": true, "make": true, "protect": true, "put": true, "say": true, "set": true, "take": true, "think": true, "try": true, "wake": true,
	"answer": true, "ask": true, "attend": true, "avoid": true, "batch": true, "build": true,
	"call": true, "care": true, "check": true, "choose": true, "code": true, "commit": true, "compare": true,
	"debug": true, "delegate": true, "deploy": true, "design": true, "dictate": true, "draft": true,
	"drink": true, "edit": true, "email": true, "enjoy": true, "exercise": true, "expect": true,
	"favor": true, "favour": true, "find": true, "fix": true, "follow": true, "gather": true,
	"group": true, "hand": true, "coordinate": true, "handle": true, "keep": true, "lead": true, "learn": true, "like": true,
	"let": true, "listen": true, "live": true, "log": true, "love": true, "manage": true, "meet": true,
	"merge": true, "monitor": true, "need": true, "open": true, "organize": true, "plan": true,
	"prefer": true, "prioritize": true, "push": true, "queue": true, "read": true, "record": true, "refactor": true,
	"rely": true, "reply": true, "report": true, "review": true, "run": true, "schedule": true,
	"send": true, "share": true, "ship": true, "sign": true, "skip": true, "sleep": true,
	"speak": true, "split": true, "start": true, "study": true, "talk": true, "teach": true,
	"tend": true, "test": true, "track": true, "travel": true, "treat": true, "trust": true,
	"type": true, "use": true, "value": true, "walk": true, "want": true, "watch": true,
	"work": true, "write": true, "verify": true, "turn": true,
}

var beliefIrregularVerbs = map[string]string{"is": "are", "was": "were", "has": "have", "does": "do", "goes": "go"}

// beliefInSecondPerson writes a belief the way Isoform talks everywhere else
// (§26, Activity AR01): "Uses design reviews…" and "I use design reviews…"
// both read "You use design reviews…". Quotation marks around it go.
// Anything else (Chinese, a noun phrase, already "You …") is unchanged.
func beliefInSecondPerson(text string) string {
	return beliefAgreeWithYou(beliefFirstPersonToYou(text))
}

// beliefAgreeWithYou makes every verb agree with "you" (v1.0.2 Y28):
// "…, and declines meetings" reads "…, and decline meetings". A conjunction
// right after a plural noun ("notes and plans") is a list, so it stays.
func beliefAgreeWithYou(sentence string) string {
	if !strings.HasPrefix(sentence, "You ") {
		return sentence
	}
	words := strings.Split(sentence, " ")
	for i := 1; i+1 < len(words); i++ {
		switch words[i] {
		case "and", "or", "but", "then":
		default:
			continue
		}
		next := words[i+1]
		end := strings.IndexFunc(next, func(r rune) bool { return !unicode.IsLetter(r) && r != '-' })
		if end < 0 {
			end = len(next)
		}
		r, _ := utf8.DecodeRuneInString(next)
		if end == 0 || !unicode.IsLower(r) {
			continue
		}
		before := words[i-1]
		_, irregular := beliefIrregularVerbs[next[:end]]
		hasObject := i+2 < len(words) && (words[i+2] == "them" || words[i+2] == "it" || words[i+2] == "him" || words[i+2] == "her")
		if !irregular && !hasObject && !strings.HasSuffix(before, ",") && (strings.HasSuffix(before, "s") || strings.HasSuffix(before, "S")) {
			continue
		}
		if base, ok := beliefBaseVerbOf(next[:end]); ok {
			words[i+1] = base + next[end:]
		}
	}
	return strings.Join(words, " ")
}

func beliefFirstPersonToYou(text string) string {
	trimmed := strings.TrimSpace(text)
	if unquoted := strings.Trim(trimmed, "\"“”'‘’"); unquoted != trimmed && unquoted != "" {
		trimmed = strings.TrimSpace(unquoted)
	}
	first, rest, _ := strings.Cut(trimmed, " ")
	if first == "" || rest == "" {
		return beliefKeepOrTrim(text, trimmed)
	}
	switch first {
	case "I":
		verb, after, _ := strings.Cut(rest, " ")
		switch verb {
		case "am":
			verb = "are"
		case "was":
			verb = "were"
		}
		return strings.TrimSpace("You " + verb + " " + after)
	case "I'm", "I’m":
		return "You're " + rest
	case "My":
		return "Your " + rest
	}
	if strings.EqualFold(first, "never") || strings.EqualFold(first, "always") || strings.EqualFold(first, "often") || strings.EqualFold(first, "usually") {
		verb, after, _ := strings.Cut(rest, " ")
		if base, ok := beliefBaseVerbOf(verb); ok {
			return strings.TrimSpace("You " + strings.ToLower(first) + " " + base + " " + after)
		}
	}
	if base, ok := beliefBaseVerb(first); ok {
		return "You " + base + " " + rest
	}
	return beliefKeepOrTrim(text, trimmed)
}

func beliefKeepOrTrim(original, trimmed string) string {
	if trimmed != strings.TrimSpace(original) {
		return trimmed
	}
	return original
}

// beliefBaseVerb turns a capitalised third-person verb ("Uses", "Relies",
// "Fast-merges") into the form after "You" ("use", "rely", "fast-merge").
func beliefBaseVerb(word string) (string, bool) {
	r, _ := utf8.DecodeRuneInString(word)
	if !unicode.IsUpper(r) {
		return "", false
	}
	return beliefBaseVerbOf(word)
}

// beliefBaseVerbOf is beliefBaseVerb without the capital letter check.
func beliefBaseVerbOf(word string) (string, bool) {
	lower := strings.ToLower(word)
	if base, ok := beliefIrregularVerbs[lower]; ok {
		return base, true
	}
	prefix, verb := "", lower
	if i := strings.LastIndex(lower, "-"); i >= 0 {
		prefix, verb = lower[:i+1], lower[i+1:]
	}
	for _, base := range beliefBaseCandidates(verb) {
		if beliefSelfVerbs[base] {
			return prefix + base, true
		}
	}
	return "", false
}

func beliefBaseCandidates(verb string) []string {
	var out []string
	if strings.HasSuffix(verb, "ies") {
		out = append(out, strings.TrimSuffix(verb, "ies")+"y")
	}
	if strings.HasSuffix(verb, "es") {
		out = append(out, strings.TrimSuffix(verb, "es"))
	}
	if strings.HasSuffix(verb, "s") && !strings.HasSuffix(verb, "ss") {
		out = append(out, strings.TrimSuffix(verb, "s"))
	}
	return out
}
