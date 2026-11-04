package main

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var commitmentDictationLead = regexp.MustCompile(`(?i)^(?:(?:ok(?:ay)?|so|um|uh|please)[,.]?\s+)*(?:remind me to |make sure to |make sure (?:that )?i (?:do )?|don'?t (?:let me )?forget to |i (?:really )?(?:need|want|have|ought) to |i(?:'ve| have)? got to |i should |i(?:'ll| will| am going to|'m going to|'m gonna) )`)
var commitmentDictationHedge = regexp.MustCompile(`(?i)\b(?:some sort of|some kind of|sort of|kind of) `)
var commitmentDictationFiller = regexp.MustCompile(`(?i)\s+like\s+(every|each|on|by|before|after)\b`)
var commitmentDictationTail = regexp.MustCompile(`(?i)(?:,?\s+(?:or something(?: like that)?|and stuff|and so on|or whatever))?[.!\s]*$`)

// commitmentShortNameLimit is the longest name a commitment shows on Today,
// in Chat and on the phone (Thursday polish C1). The full words stay in the
// commitment's detail.
const commitmentShortNameLimit = 40

// Words dictation commonly mishears in commitments.
var commitmentDictationMisheard = strings.NewReplacer("calf race", "calf raises", "calf races", "calf raises")

var (
	commitmentVagueAlternative = regexp.MustCompile(`(?i)\s+or (?:an? )?(?:exercises?|something|stuff|whatever)\b`)
	commitmentForAcronym       = regexp.MustCompile(`^(.+?) for ([A-Z][A-Z0-9]{1,})\b(.*)$`)
	commitmentTrailingCadence  = regexp.MustCompile(`(?i),?\s+(every (?:day|morning|evening|night|week|weekday)|each (?:day|morning|evening|week)|daily|weekly|twice a day)$`)
)

// commitmentPlainName rewords a dictated commitment into a plain name: a
// misheard word fixed, a vague alternative dropped, what it is for put
// first, and how often set off at the end. "calf race or exercise for AFO
// every day" reads "AFO calf raises, every day" (chat-review v1.0.10 H4).
func commitmentPlainName(name string) string {
	name = commitmentDictationMisheard.Replace(name)
	name = commitmentVagueAlternative.ReplaceAllString(name, "")
	if m := commitmentForAcronym.FindStringSubmatch(name); m != nil {
		subject := []rune(m[1])
		if len(subject) > 1 && unicode.IsUpper(subject[0]) && !unicode.IsUpper(subject[1]) {
			subject[0] = unicode.ToLower(subject[0])
		}
		name = m[2] + " " + string(subject) + m[3]
	}
	return commitmentTrailingCadence.ReplaceAllString(name, ", $1")
}

// A cut never leaves the name ending on a word that needs what follows it.
var commitmentShortNameDangling = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "but": true, "so": true,
	"to": true, "for": true, "of": true, "with": true, "by": true, "on": true, "at": true,
	"in": true, "into": true, "from": true, "about": true, "if": true, "unless": true,
	"before": true, "after": true, "until": true, "when": true, "every": true, "each": true,
	"my": true, "your": true, "our": true, "their": true, "his": true, "her": true, "its": true,
	"this": true, "that": true, "some": true, "any": true, "is": true, "are": true, "be": true,
}

// Words that open a condition or a time for the task before them.
var commitmentShortNameClause = map[string]bool{
	"if": true, "unless": true, "when": true, "before": true, "after": true, "until": true, "while": true, "because": true,
}

// commitmentShortName removes common spoken scaffolding without guessing at
// meaning, then keeps the name to commitmentShortNameLimit characters, cut at
// a word. Short names keep the task, its object and its conditions word for
// word.
func commitmentShortName(source string) string {
	original := strings.Join(strings.Fields(source), " ")
	name := strings.TrimSpace(commitmentDictationLead.ReplaceAllString(original, ""))
	name = commitmentDictationHedge.ReplaceAllString(name, "")
	name = commitmentDictationFiller.ReplaceAllString(name, " $1")
	name = strings.TrimSpace(commitmentDictationTail.ReplaceAllString(name, ""))
	name = commitmentPlainName(name)
	if name == "" {
		name = original
	}
	if name != strings.TrimRight(original, ".! ") {
		runes := []rune(name)
		runes[0] = unicode.ToUpper(runes[0])
		name = string(runes)
	} else if utf8.RuneCountInString(original) <= commitmentShortNameLimit {
		return original
	}
	return commitmentShortNameCut(name)
}

// commitmentShortNameCut cuts at the last word (or, in text written without
// spaces, the last clause mark) that fits, and drops a dangling connective.
func commitmentShortNameCut(name string) string {
	runes := []rune(name)
	if len(runes) <= commitmentShortNameLimit {
		return name
	}
	head := runes[:commitmentShortNameLimit]
	cut := -1
	if unicode.IsSpace(runes[commitmentShortNameLimit]) || unicode.IsPunct(runes[commitmentShortNameLimit]) {
		cut = commitmentShortNameLimit
	} else {
		for i := len(head) - 1; i > 0; i-- {
			if unicode.IsSpace(head[i]) || strings.ContainsRune("，、；：,;:", head[i]) {
				cut = i
				break
			}
		}
	}
	if cut <= 0 {
		// One long word, or text with no clause marks: a hard cut.
		cut = commitmentShortNameLimit
	}
	words := strings.Fields(string(head[:cut]))
	// The cut always ends the last clause early, so a condition or a time it
	// starts goes whole rather than in part ("only if the trip").
	for i := len(words) - 1; i >= 2; i-- {
		if commitmentShortNameClause[strings.ToLower(words[i])] {
			words = words[:i]
			if strings.EqualFold(words[i-1], "only") {
				words = words[:i-1]
			}
			break
		}
	}
	for len(words) > 1 && commitmentShortNameDangling[strings.ToLower(strings.Trim(words[len(words)-1], ",;:-"))] {
		words = words[:len(words)-1]
	}
	return strings.TrimRight(strings.Join(words, " "), ",;:-–—，、；： ")
}
