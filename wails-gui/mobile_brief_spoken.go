package main

import (
	"regexp"
	"strings"
	"unicode"
)

// The brief is read aloud, so English brief text says titles plainly: no
// quotation marks and no parentheses ("your 10am 1:1 with Priya and Alex",
// "the Halcyon Arc lamp follow-up"). Chinese text keeps its conventional “”
// around a title. These helpers shape a source title for a sentence; the item's
// own Title and evidence label never change.

var mobileBriefOneOnOne = regexp.MustCompile(`(?i)\b(?:1\s*:\s*1|1-on-1|one[- ]on[- ]one)\b`)

var mobileBriefTitleSeparator = regexp.MustCompile(`\s+(?:-|–|—|\|)\s+`)

var mobileBriefNameSplit = regexp.MustCompile(`\s*(?:/|&|,|\+|\band\b|<>)\s*`)

var mobileBriefArticles = map[string]bool{"the": true, "a": true, "an": true, "my": true, "your": true, "our": true, "his": true, "her": true, "their": true, "this": true, "that": true}

// Verbs that open a task title and are rarely the first word of a name
// ("Pay the Citi card", "Run payroll", "Agree on the pricing plan").
var mobileBriefTaskVerbs = map[string]bool{
	"agree": true, "answer": true, "apply": true, "approve": true, "ask": true, "book": true, "buy": true,
	"cancel": true, "confirm": true, "contact": true, "decide": true, "discuss": true, "draft": true,
	"email": true, "fill": true, "finalize": true, "finish": true, "fix": true, "follow": true, "hire": true,
	"invite": true, "pay": true, "prepare": true, "remind": true, "renew": true, "reply": true,
	"reschedule": true, "respond": true, "run": true, "schedule": true, "send": true, "sign": true,
	"submit": true, "update": true, "upload": true, "write": true,
}

// Verbs that also start names ("Call with investors", "Review of Q3"). They
// make a task only before a determiner, a pronoun, a capitalized name or a
// particle that belongs to the verb ("Call Dana", "Check in with Maya Chen").
var mobileBriefLooseVerbs = map[string]bool{
	"add": true, "build": true, "call": true, "check": true, "clean": true, "close": true, "collect": true,
	"create": true, "deliver": true, "file": true, "find": true, "get": true, "make": true, "meet": true,
	"message": true, "move": true, "note": true, "order": true, "organize": true, "plan": true, "print": true,
	"publish": true, "read": true, "record": true, "register": true, "request": true, "return": true,
	"review": true, "set": true, "share": true, "ship": true, "start": true, "take": true, "test": true,
	"text": true, "transfer": true, "try": true, "visit": true,
}

// A determiner second word marks a verb phrase whatever the first word is
// ("Finalize the deck"); a name rarely reads "X the Y".
var mobileBriefDeterminers = map[string]bool{
	"the": true, "a": true, "an": true, "my": true, "your": true, "our": true, "their": true, "his": true, "her": true,
}

// Particles and prepositions between a verb and its object ("Follow up with
// Dana", "Agree on the pricing plan").
var mobileBriefParticles = map[string]bool{
	"up": true, "out": true, "back": true, "off": true, "on": true, "in": true, "to": true, "for": true,
	"with": true, "about": true, "over": true, "through": true, "into": true, "at": true, "from": true, "by": true,
}

// Particles that stay with a loose verb ("Check in", "Set up"); "with" and
// "for" do not ("Call with investors" is a name).
var mobileBriefVerbParticles = map[string]bool{
	"up": true, "out": true, "back": true, "off": true, "in": true, "over": true, "through": true, "down": true,
}

var mobileBriefPronouns = map[string]bool{"it": true, "them": true, "him": true, "her": true, "me": true, "us": true, "this": true, "that": true}

// Other common first words of a step or a goal that are safe to lowercase
// mid-sentence. A word outside every list, such as a name, keeps its case.
var mobileBriefCommonLeadWords = map[string]bool{
	"accept": true, "attend": true, "choose": true, "clear": true, "compare": true, "complete": true,
	"decline": true, "deploy": true, "estimate": true, "gather": true, "give": true, "go": true, "join": true,
	"keep": true, "launch": true, "learn": true, "list": true, "look": true, "merge": true, "no": true,
	"open": true, "outline": true, "pack": true, "pick": true, "practice": true, "put": true, "reach": true,
	"release": true, "research": true, "resolve": true, "save": true, "see": true, "sell": true, "show": true,
	"study": true, "summarize": true, "sync": true, "talk": true, "tell": true, "use": true, "wait": true,
}

// mobileBriefSpokenTitle reads a title the way a person says it: a one-on-one
// names its people, and separators become words or pauses.
func mobileBriefSpokenTitle(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	if mobileBriefOneOnOne.MatchString(title) {
		rest := strings.Trim(mobileBriefOneOnOne.ReplaceAllString(title, " "), " :-–—|/,")
		// "1:1 with Wei" already says "with".
		if len(rest) > 5 && strings.EqualFold(rest[:5], "with ") {
			rest = strings.TrimSpace(rest[5:])
		}
		var names []string
		for _, name := range mobileBriefNameSplit.Split(rest, -1) {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if len(strings.Fields(name)) > 2 || strings.ContainsAny(name, "0123456789:") {
				names = nil
				break
			}
			names = append(names, name)
		}
		if len(names) > 0 && len(names) <= 3 {
			return "1:1 with " + mobileBriefJoinAnd(names)
		}
	}
	title = mobileBriefTitleSeparator.ReplaceAllString(title, ", ")
	title = strings.ReplaceAll(title, " / ", " and ")
	// A closing initial keeps its period ("Maya C."); other end marks go.
	if fields := strings.Fields(title); len(fields) > 0 {
		last := fields[len(fields)-1]
		if !(len([]rune(last)) == 2 && unicode.IsUpper([]rune(last)[0]) && strings.HasSuffix(last, ".")) {
			title = strings.TrimRight(title, " .!?;:,")
		}
	}
	return title
}

// mobileBriefJoinAnd joins names the way a sentence lists them.
func mobileBriefJoinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// mobileBriefNounSubject is a title as the subject or object of an English
// sentence: "Discovery", "the Halcyon Arc lamp follow-up", "the 1:1 with
// Priya and Alex". Chinese keeps the quoted title.
func mobileBriefNounSubject(title, lang string) string {
	if lang == "zh" {
		return "“" + strings.TrimSpace(title) + "”"
	}
	spoken := mobileBriefSpokenTitle(title)
	words := strings.Fields(spoken)
	if len(words) == 0 {
		return ""
	}
	if mobileBriefArticles[strings.ToLower(words[0])] {
		return mobileBriefLowerFirst(spoken)
	}
	if len(words) == 1 {
		return spoken
	}
	return "the " + spoken
}

// mobileBriefTaskPhrase returns an English task title as a verb phrase ("pay
// the Citi and Amazon card") when its first word is a known verb, or when a
// determiner follows the first word ("Finalize the deck"). Anything else is
// a name, whatever its kind: "Dinner with Maya Chen" and "Trip to Japan"
// stay nouns. kind is kept for callers; grammar decides.
func mobileBriefTaskPhrase(title, kind, lang string) (string, bool) {
	if lang == "zh" {
		return "", false
	}
	spoken := mobileBriefSpokenTitle(title)
	words := strings.Fields(spoken)
	if len(words) < 2 {
		return "", false
	}
	first, second := strings.ToLower(words[0]), strings.ToLower(words[1])
	secondRunes := []rune(words[1])
	named := len(secondRunes) > 0 && unicode.IsUpper(secondRunes[0])
	if mobileBriefTaskVerbs[first] || mobileBriefDeterminers[second] ||
		mobileBriefLooseVerbs[first] && (mobileBriefPronouns[second] || named || mobileBriefVerbParticles[second]) {
		return mobileBriefLowerFirst(spoken), true
	}
	return "", false
}

// mobileBriefItemNoun keeps a commitment's saved name. Other tasks are named
// by an object with a determiner, or by "your task to ...".
func mobileBriefItemNoun(title, kind, lang string) string {
	// A commitment's saved name is its name in every sentence. In particular,
	// do not prepend an article or substitute an object for the name.
	if kind == "commitment" {
		return pick(lang, strings.TrimSpace(title), "“"+strings.TrimSpace(title)+"”")
	}
	task, ok := mobileBriefTaskPhrase(title, kind, lang)
	if !ok {
		return mobileBriefNounSubject(title, lang)
	}
	words := strings.Fields(task)[1:]
	for len(words) > 1 && mobileBriefParticles[strings.ToLower(words[0])] {
		words = words[1:]
	}
	if len(words) > 1 && mobileBriefDeterminers[strings.ToLower(words[0])] {
		return strings.Join(words, " ")
	}
	return "your task to " + task
}

// mobileBriefCapitalize starts a sentence with a capital letter.
func mobileBriefCapitalize(value string) string {
	runes := []rune(value)
	if len(runes) == 0 || !unicode.IsLower(runes[0]) {
		return value
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
