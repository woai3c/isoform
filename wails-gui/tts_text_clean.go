package main

import (
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// dominantSpeechLang routes a chunk to a TTS engine: "en" (Kokoro) only when
// the chunk has NO Chinese characters at all; otherwise "zh" (MeloTTS). This
// keeps Chinese-user replies entirely on MeloTTS (good Chinese) and English-user
// replies entirely on Kokoro (good English); a Chinese sentence with a stray
// English word/acronym still goes to MeloTTS instead of being mangled by the
// English voice.
func dominantSpeechLang(s string) string {
	hasHan := false
	hasLatin := false
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			hasHan = true
			break
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			hasLatin = true
		}
	}
	if !hasHan && hasLatin {
		return "en"
	}
	return "zh"
}

var (
	reFencedCode   = regexp.MustCompile("(?s)```.*?```")
	reInlineCode   = regexp.MustCompile("`([^`]*)`")
	reImage        = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	reLink         = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	reHeadingMark  = regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s+`)
	reBlockquote   = regexp.MustCompile(`(?m)^\s{0,3}>\s?`)
	reListMarker   = regexp.MustCompile(`(?m)^\s*([-*+]|\d+[.)])\s+`)
	reTableDivider = regexp.MustCompile(`(?m)^\s*\|?[\s:|-]+\|[\s:|-]*$`)
	reEmphasis     = regexp.MustCompile(`(\*\*|__|\*|_|~~)`)
	reHTMLTag      = regexp.MustCompile(`<[^>]+>`)
	reBareURL      = regexp.MustCompile(`https?://\S+`)
	reMultiNewline = regexp.MustCompile(`\n{3,}`)
	reMultiSpace   = regexp.MustCompile(`[ \t]{2,}`)
	// An ASCII space/tab sitting between two Chinese characters: Chinese has no
	// inter-word spaces, and MeloTTS emits a glitch (and can drop the following
	// word) when it hits one — e.g. a "《Title》 Author" line cleaned to
	// "Title Author" read 静夜思 with noise and skipped 李白. Collapsed away.
	// Han↔Latin spaces (e.g. "使用 Python") are meaningful and left untouched.
	reHanSpaceHan = regexp.MustCompile(`(\p{Han})[ \t]+(\p{Han})`)
	// Emoji, pictographs and decorative symbols: the TTS engine can't pronounce
	// them and, when they lead a sentence, can derail synthesis of the whole
	// clip. Strip them so surrounding text still reads. Ranges cover emoji
	// (incl. flags/skin-tone/ZWJ sequences), dingbats, arrows, enclosed
	// alphanumerics (①Ⓜ), box-drawing/block/geometric shapes (╰╯▏■●▪◦•) — these
	// show up as kaomoji decoration — and stray symbols (© ® ™). Deliberately
	// avoids CJK punctuation (。、「」) and useful marks like ° % which sit
	// outside these ranges (a lone ° inside a kaomoji is handled by
	// stripDecorativeBrackets instead).
	reEmoji = regexp.MustCompile(`[\x{1F000}-\x{1FFFF}\x{2190}-\x{21FF}\x{2300}-\x{23FF}\x{2460}-\x{24FF}\x{2500}-\x{25FF}\x{2600}-\x{27BF}\x{2900}-\x{297F}\x{2B00}-\x{2BFF}\x{FE00}-\x{FE0F}\x{200D}\x{20E3}\x{2022}\x{2023}\x{2043}\x{2122}\x{2139}\x{3030}\x{303D}\x{3297}\x{3299}\x{00A9}\x{00AE}]`)
	// A bracketed group with no speakable content — a kaomoji/emoticon like
	// (°▽°), （▏▏） or (^_^). The engine voices these as clicks/noise, so drop
	// the whole group (brackets included). A group with any Han/letter/digit —
	// "(参见下文)", "(25°C)" — is real content and kept by stripDecorativeBrackets.
	reBracketGroup = regexp.MustCompile(`[（(][^（()）]*[)）]`)
	// Quotation marks aren't pronounced and some engines emit a click/noise for
	// a bare double quote. Strip double quotes (ASCII and curly) and CJK quote
	// brackets, but NOT apostrophes/single quotes — those appear in
	// contractions (don't, it's) and must stay.
	reQuotes = regexp.MustCompile(`["“”„「」『』《》〈〉]`)
	// Runs of 2+ uppercase letters are almost always acronyms (PPT, QA, PM, UI)
	// that aren't in MeloTTS's word lexicon and would be dropped as OOV. We spell
	// them out letter-by-letter ("PPT" -> "p p t") so each letter hits the
	// single-letter lexicon entries (p->"pee", t->"tee") and gets spoken.
	reAcronym = regexp.MustCompile(`[A-Z]{2,}`)
	// Em/horizontal dashes (—, ―, the —— pair) carry no pronunciation. Left in,
	// MeloTTS reads them as noise, and the clause splitter shears a "——" into a
	// stray "— —" fragment that becomes a dead, noisy clip. Convert a run to a
	// single comma so it becomes a natural pause and a clean clause-split point.
	reDashRun = regexp.MustCompile(`[—―]+`)
	// Interpunct used as a separator in names/titles (宋·陆游, ・). It has no
	// sound; convert to a comma so the two parts get a pause instead of being
	// run together (deleting it would read "宋陆游").
	reInterpunct = regexp.MustCompile(`[·・]`)
	// Dashes between digits express a range, not decoration. Preserve that
	// meaning before the generic dash cleanup below ("9～18" -> "9到18").
	reNumericRange = regexp.MustCompile(`(\d)\s*[—―～〜]\s*(\d)`)
	// Decorative wave dash (～ / 〜): a tone-lengthening flourish with no
	// pronunciation. Drop it. (ASCII ~ is left alone — it appears in paths and
	// the ~~strikethrough~~ markdown handled by reEmphasis.)
	reWaveDash = regexp.MustCompile(`[～〜]`)
	// A space sitting right after CJK punctuation — reintroduced when the clause
	// splitter re-joins pieces with a separator. MeloTTS glitches on it the same
	// way it does on a bare inter-Han space (collapseHanSpaces can't catch this
	// one because the char before the space is punctuation, not Han). Collapse.
	reSpaceAfterCJKPunct = regexp.MustCompile(`([，、；：。！？])[ \t]+`)
)

// stripDecorativeBrackets removes a bracketed group that has nothing speakable
// inside (kaomoji/emoticons). Groups containing any Han/letter/digit are real
// parentheticals and kept verbatim.
func stripDecorativeBrackets(s string) string {
	return reBracketGroup.ReplaceAllStringFunc(s, func(m string) string {
		if hasSpeechContent(m) {
			return m
		}
		return ""
	})
}

// collapseHanSpaces drops ASCII spaces/tabs between two Chinese characters so
// MeloTTS doesn't glitch on (or skip a word after) a stray inter-Han space.
// Looped because regexp matches are non-overlapping, so a run like "甲 乙 丙"
// needs more than one pass.
func collapseHanSpaces(s string) string {
	for {
		next := reHanSpaceHan.ReplaceAllString(s, "$1$2")
		if next == s {
			return s
		}
		s = next
	}
}

// expandAcronyms spells out uppercase-letter runs as spaced lowercase letters
// so the TTS engine pronounces them letter-by-letter instead of skipping them.
func expandAcronyms(s string) string {
	return reAcronym.ReplaceAllStringFunc(s, func(m string) string {
		letters := strings.Split(strings.ToLower(m), "")
		return " " + strings.Join(letters, " ") + " "
	})
}

func normalizeSpeechSeparators(s string) string {
	rangeWord := "到"
	pause := "，"
	if dominantSpeechLang(s) == "en" {
		rangeWord = " to "
		pause = ", "
	}

	s = reNumericRange.ReplaceAllString(s, "${1}"+rangeWord+"${2}")
	s = reDashRun.ReplaceAllString(s, pause)
	s = reInterpunct.ReplaceAllString(s, pause)
	return reWaveDash.ReplaceAllString(s, "")
}

// splitIntoSentences breaks text on sentence-final punctuation AND newlines,
// keeping the delimiter attached to the preceding sentence. CJK 。！？ and ASCII
// ! ? always end a sentence; an ASCII period ends one only when it looks like
// real punctuation — followed by a space or end of text and not inside a
// decimal (e.g. 3.5) — so abbreviations and numbers aren't split. A newline
// also ends a sentence so paragraphs and list items get a natural pause rather
// than being read as one run-on; short fragments are merged back by the caller.
func splitIntoSentences(s string) []string {
	runes := []rune(s)
	out := make([]string, 0, 8)
	start := 0
	isCloser := func(r rune) bool {
		switch r {
		case '"', '\'', '”', '’', '」', '』', ')', '）', '】':
			return true
		}
		return false
	}
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		end := false
		switch r {
		case '。', '！', '？', '!', '?', '\n':
			end = true
		case '.':
			next := i + 1
			atBoundary := next >= len(runes) || runes[next] == ' ' || isCloser(runes[next])
			prevDigit := i > 0 && runes[i-1] >= '0' && runes[i-1] <= '9'
			nextDigit := next < len(runes) && runes[next] >= '0' && runes[next] <= '9'
			end = atBoundary && !prevDigit && !nextDigit
		}
		if end {
			// Pull any trailing closing quotes/brackets into this sentence so
			// the break lands after them, not before (e.g. ... sailor.").
			j := i + 1
			for j < len(runes) && isCloser(runes[j]) {
				j++
			}
			out = append(out, string(runes[start:j]))
			start = j
			i = j - 1
		}
	}
	if start < len(runes) {
		out = append(out, string(runes[start:]))
	}
	return out
}

// secondarySplitRe splits an over-long sentence on clause punctuation so each
// synthesized chunk stays short and breaks at a natural pause: comma,
// enumeration comma, semicolon, colon, dash and ellipsis.
var secondarySplitRe = regexp.MustCompile(`[^，、；：,;—…]*[，、；：,;—…]|[^，、；：,;—…]+$`)

// Chunk length caps, per engine. MeloTTS (Chinese) prosody drifts on long
// utterances, so Chinese is kept short. Kokoro (English) handles long sentences
// well, so English uses a much larger cap — a normal English sentence stays
// whole instead of being cut mid-phrase (which makes the join sound like a
// pause). Long sentences past the cap still split on clause punctuation.
const (
	ttsMaxChunkRunesZh = 30
	ttsMaxChunkRunesEn = 80
)

// hasSpeechContent reports whether a chunk has anything worth speaking — a
// Chinese character, a letter, or a digit. Punctuation/emoji-only fragments
// (e.g. a lone kaomoji) return false and get merged into the previous chunk
// instead of becoming a dead clip; short-but-meaningful chunks (a 2-character
// list item, "好。") are kept so paragraphs/list items still get their pause.
func hasSpeechContent(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') {
			return true
		}
	}
	return false
}

// splitSpeechSentences cleans markdown then splits into short chunks for
// streaming synthesis (speak the first chunk while later ones generate).
// Sentences over ttsMaxChunkRunes are further split on clause punctuation (and
// hard-split as a last resort) so synthesis stays stable; tiny fragments merge
// into the previous chunk. Returns nil if nothing is speakable.
func splitSpeechSentences(md string) []string {
	clean := stripMarkdownForSpeech(md)
	if clean == "" {
		return nil
	}

	out := make([]string, 0, 8)
	for _, sentence := range splitIntoSentences(clean) {
		sentence = strings.TrimSpace(sentence)
		if sentence == "" {
			continue
		}
		// English routes to Kokoro (good at long sentences), so let English
		// sentences stay whole; Chinese routes to MeloTTS and stays short.
		max := ttsMaxChunkRunesZh
		if dominantSpeechLang(sentence) == "en" {
			max = ttsMaxChunkRunesEn
		}
		for _, chunk := range splitLongChunk(sentence, max) {
			chunk = strings.TrimSpace(chunk)
			if chunk == "" {
				continue
			}
			// splitLongChunk re-joins clauses with a separating space; strip any
			// that landed right after CJK punctuation or between two Han chars,
			// where MeloTTS would glitch on it.
			chunk = collapseHanSpaces(reSpaceAfterCJKPunct.ReplaceAllString(chunk, "$1"))
			if !hasSpeechContent(chunk) && len(out) > 0 {
				out[len(out)-1] = out[len(out)-1] + " " + chunk
				continue
			}
			out = append(out, chunk)
		}
	}
	return out
}

// splitLongChunk breaks a sentence longer than max runes into clause-sized
// pieces on secondary punctuation; a punctuation-free clause that is still too
// long is hard-split by rune count so no single utterance exceeds max.
func splitLongChunk(s string, max int) []string {
	if utf8.RuneCountInString(s) <= max {
		return []string{s}
	}
	out := make([]string, 0, 4)
	var buf strings.Builder
	bufLen := 0
	flush := func() {
		if bufLen > 0 {
			out = append(out, strings.TrimSpace(buf.String()))
			buf.Reset()
			bufLen = 0
		}
	}
	for _, part := range secondarySplitRe.FindAllString(s, -1) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		partLen := utf8.RuneCountInString(part)
		if partLen > max {
			flush()
			out = append(out, hardSplitAtWord(part, max)...)
			continue
		}
		if bufLen+partLen > max {
			flush()
		}
		if buf.Len() > 0 {
			buf.WriteByte(' ')
		}
		buf.WriteString(part)
		bufLen += partLen
	}
	flush()
	return out
}

// hardSplitAtWord splits a too-long, punctuation-free clause into <=max pieces,
// preferring a space (word boundary) so English isn't cut mid-word. Falls back
// to a rune boundary only when there's no space in range (e.g. dense CJK with
// no clause punctuation, which is rare).
func hardSplitAtWord(s string, max int) []string {
	runes := []rune(s)
	out := make([]string, 0, (len(runes)/max)+1)
	for len(runes) > max {
		cut := max
		// Backtrack to the last space within [max/2, max] for a word boundary.
		for i := max; i > max/2; i-- {
			if runes[i] == ' ' {
				cut = i
				break
			}
		}
		piece := strings.TrimSpace(string(runes[:cut]))
		if piece != "" {
			out = append(out, piece)
		}
		runes = runes[cut:]
	}
	if rest := strings.TrimSpace(string(runes)); rest != "" {
		out = append(out, rest)
	}
	return out
}

// stripMarkdownForSpeech reduces markdown to plain, speakable text so the TTS
// engine doesn't read symbols (backticks, pipes, asterisks, URLs) aloud. Code
// blocks are dropped entirely — reading code character-by-character is useless
// and slow. Returns "" if nothing speakable remains.
func stripMarkdownForSpeech(md string) string {
	s := md
	s = reFencedCode.ReplaceAllString(s, " ")
	s = reImage.ReplaceAllString(s, " ")
	s = reLink.ReplaceAllString(s, "$1")
	s = reInlineCode.ReplaceAllString(s, "$1")
	// Any backtick still left is an unpaired stray (reInlineCode only unwraps
	// pairs). Like bare quotes, the engine voices it as a click/noise — drop it.
	s = strings.ReplaceAll(s, "`", "")
	s = reTableDivider.ReplaceAllString(s, "")
	s = reHeadingMark.ReplaceAllString(s, "")
	s = reBlockquote.ReplaceAllString(s, "")
	s = reListMarker.ReplaceAllString(s, "")
	s = reEmphasis.ReplaceAllString(s, "")
	s = reHTMLTag.ReplaceAllString(s, " ")
	s = reBareURL.ReplaceAllString(s, " ")
	// Do this before acronym expansion and sentence splitting: AM/PM must
	// remain part of a natural clock rather than separate spoken letters.
	s = normalizeBriefText(s, dominantSpeechLang(s), time.Time{}, true)
	s = reEmoji.ReplaceAllString(s, "")
	s = stripDecorativeBrackets(s)
	s = reQuotes.ReplaceAllString(s, "")
	// Normalize unpronounceable separators/flourishes to a pause (or nothing)
	// before sentence splitting, so dashes become clause-split points and don't
	// survive as noise.
	s = normalizeSpeechSeparators(s)
	s = expandAcronyms(s)
	// Table cell separators: turn "| a | b |" into "a b".
	s = strings.ReplaceAll(s, "|", " ")

	s = reMultiSpace.ReplaceAllString(s, " ")
	s = collapseHanSpaces(s)
	s = reMultiNewline.ReplaceAllString(s, "\n\n")

	// Trim trailing spaces on each line, then overall.
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}
