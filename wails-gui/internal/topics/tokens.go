package topics

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// redactedTag is what the privacy filter leaves where it removed something.
var redactedTag = regexp.MustCompile(`\[REDACTED:[a-z_]+\]`)

// wordChars may appear inside a raw word. Characters that belong to links,
// paths and addresses are included so such a word is seen whole and dropped.
func wordChar(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return true
	}
	switch r {
	case '.', '-', '_', '+', '#', '\'', '’', '/', '\\', '@', ':', '%', '=', '?', '&', '~':
		return true
	default:
		return false
	}
}

// phraseRuns splits text into runs of usable words. A run ends at
// punctuation, at a stop word and at anything dropped (a link, a path, an
// address, a code), so a phrase never spans them.
func phraseRuns(text string) [][]token {
	text = joinSpacedHan(redactedTag.ReplaceAllString(text, " | "))
	var runs [][]token
	var current []token
	flush := func() {
		if len(current) > 0 {
			runs = append(runs, current)
			current = nil
		}
	}
	var raw strings.Builder
	finishWord := func() {
		if raw.Len() == 0 {
			return
		}
		word := raw.String()
		raw.Reset()
		pieces, breakAfter, ok := cleanWord(word)
		if !ok {
			flush()
			return
		}
		for _, piece := range pieces {
			tok, keep := makeToken(piece)
			if !keep {
				flush()
				continue
			}
			current = append(current, tok)
		}
		if breakAfter {
			flush()
		}
	}
	for _, r := range text {
		if wordChar(r) {
			raw.WriteRune(r)
			continue
		}
		finishWord()
		if !unicode.IsSpace(r) {
			flush()
		}
	}
	finishWord()
	flush()
	return runs
}

// joinSpacedHan glues a single Han character written apart to the Han text
// beside it ("张 伟 新闻" reads "张伟 新闻"), so a name spelled with spaces is
// seen whole instead of dropping out as one-character pieces.
func joinSpacedHan(text string) string {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		return text
	}
	single := func(s string) bool {
		r, size := utf8.DecodeRuneInString(s)
		return size == len(s) && unicode.Is(unicode.Han, r)
	}
	var b strings.Builder
	for i, field := range fields {
		if i > 0 {
			prev := fields[i-1]
			last, _ := utf8.DecodeLastRuneInString(prev)
			first, _ := utf8.DecodeRuneInString(field)
			if !(unicode.Is(unicode.Han, last) && unicode.Is(unicode.Han, first) && (single(prev) || single(field))) {
				b.WriteByte(' ')
			}
		}
		b.WriteString(field)
	}
	return b.String()
}

// cleanWord trims a raw word and splits it where the script changes. ok is
// false for a link, a path, an address, a time or a host name.
func cleanWord(word string) (pieces []string, breakAfter bool, ok bool) {
	if strings.Contains(word, "://") || strings.ContainsAny(word, "/\\@%=?&~") {
		return nil, true, false
	}
	trimmed := strings.TrimLeft(word, ".-_'’:")
	end := strings.TrimRight(trimmed, ".,-_'’:")
	if end != trimmed {
		tail := trimmed[len(end):]
		breakAfter = strings.ContainsAny(tail, ".:")
	}
	if end == "" {
		return nil, true, false
	}
	if strings.Contains(end, ":") || hostLike(end) {
		return nil, true, false
	}
	return splitScripts(end), breakAfter, true
}

// splitScripts separates Han characters from everything else: "使用Wails"
// reads as two words.
func splitScripts(word string) []string {
	var out []string
	var b strings.Builder
	prevHan := false
	first := true
	for _, r := range word {
		han := unicode.Is(unicode.Han, r)
		if !first && han != prevHan {
			out = append(out, b.String())
			b.Reset()
		}
		b.WriteRune(r)
		prevHan = han
		first = false
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

// makeToken keys a piece and drops stop words, codes, long numbers, single
// letters and long Han runs (those are sentences, not names).
func makeToken(piece string) (token, bool) {
	// "Jane's budget" is about Jane: the possessive goes, so the name check
	// sees the name.
	for _, suffix := range []string{"'s", "\u2019s", "'", "\u2019"} {
		if trimmed, ok := strings.CutSuffix(piece, suffix); ok && trimmed != "" {
			piece = trimmed
			break
		}
	}
	key := strings.ToLower(piece)
	if stopWords[key] {
		return token{}, false
	}
	runes := utf8.RuneCountInString(key)
	if hanToken(key) {
		if runes < 2 || runes > 6 {
			return token{}, false
		}
		return token{surface: piece, key: key}, true
	}
	if runes < 2 && !unicode.IsDigit([]rune(key)[0]) {
		return token{}, false
	}
	if codeLike(key) {
		return token{}, false
	}
	if runes > 32 || (runes > 8 && unspacedScript(key)) {
		return token{}, false
	}
	return token{surface: piece, key: key}, true
}

// unspacedScript reports a run in a script written without spaces (kana,
// Thai and their neighbours): a long run is a sentence, not a name.
func unspacedScript(key string) bool {
	for _, r := range key {
		if unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Thai, unicode.Lao, unicode.Khmer, unicode.Myanmar) {
			return true
		}
	}
	return false
}

// codeLike is a long number (a year, an order, a phone) or a run mixing
// letters and digits long enough to be an id.
func codeLike(key string) bool {
	digits, letters := 0, 0
	for _, r := range key {
		if unicode.IsDigit(r) {
			digits++
		} else if unicode.IsLetter(r) {
			letters++
		}
	}
	if letters == 0 && digits >= 4 && !strings.Contains(key, ".") {
		return true
	}
	// An order or ticket id ("A7K9Q2", "tx-8f3k2m9"); a version keeps its
	// dots ("go1.25").
	if letters >= 2 && digits >= 2 && letters+digits >= 6 && !strings.Contains(key, ".") {
		return true
	}
	if digits > 0 && letters > 0 && digits+letters >= 10 {
		return true
	}
	return digits >= 6
}

// commonTLDs are the endings that make a dotted word a host name.
var commonTLDs = map[string]bool{
	"com": true, "org": true, "net": true, "io": true, "dev": true, "ai": true, "app": true,
	"co": true, "cn": true, "me": true, "so": true, "xyz": true, "edu": true, "gov": true,
	"info": true, "us": true, "uk": true, "de": true, "jp": true, "fr": true, "ca": true,
	"au": true, "in": true, "tv": true, "ly": true, "gg": true, "sh": true, "site": true,
}

// codeSuffixes end tool names, not hosts ("node.js").
var codeSuffixes = map[string]bool{"js": true, "ts": true, "jsx": true, "tsx": true, "py": true, "rb": true, "go": true, "rs": true, "sh": true}

// hostLike is a dotted word that ends like a host or a file ("mybank.ch",
// "report.pdf"), not a version or a tool name.
func hostLike(word string) bool {
	dot := strings.LastIndexByte(word, '.')
	if dot <= 0 || dot == len(word)-1 {
		return false
	}
	suffix := strings.ToLower(word[dot+1:])
	if commonTLDs[suffix] {
		return true
	}
	if codeSuffixes[suffix] || len(suffix) < 2 || len(suffix) > 6 {
		return false
	}
	for _, r := range suffix {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	for _, r := range word[:dot] {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// stopWords end a phrase. Besides the usual function words they hold the
// words every title carries (apps, sites, "untitled", "inbox"), calendar
// words and generic verbs, so a topic is about something.
var stopWords = func() map[string]bool {
	words := strings.Fields(`
a an the and or but if then else of to in on at by for with from into onto over under about
as is are was were be been being am do does did doing done have has had having can could
will would shall should may might must not no nor so too very just only also than that this
these those there here what which who whom whose when where why how all any both each few
more most other some such own same again further once up down out off my your his her its our
their me him us them i you he she it we they mine yours ours theirs myself yourself
new tab untitled inbox home page window settings preferences dashboard login log sign signin
search results result loading edit view file files folder document documents doc docs draft
drafts notes note chat chats meeting meetings call calls sync weekly daily standup update
updates today yesterday tomorrow week month year am pm re fwd fw via vs etc ok okay
make made get got set use using used need want try new open opened close closed see show
help guide tutorial example examples free best top latest official overview introduction
how-to step steps way ways thing things stuff
monday tuesday wednesday thursday friday saturday sunday mon tue wed thu fri sat sun
january february march april june july august september october november december
jan feb mar apr jun jul aug sep sept oct nov dec
google chrome safari firefox arc edge brave opera slack zoom teams finder terminal iterm iterm2
code vscode cursor xcode notion gmail mail outlook calendar messages wechat whatsapp telegram
figma isoform yansu claude chatgpt codex youtube github gitlab stackoverflow reddit twitter
linkedin wikipedia medium bing baidu duckduckgo kagi docs.google localhost preview
的 了 是 在 和 与 及 或 吗 呢 吧 啊 我 你 他 她 它 我们 你们 他们 这 那 这个 那个 一个
如何 怎么 怎样 什么 为什么 哪里 哪个 可以 能 会 要 用 使用 问题 方法 教程 搜索 首页 设置
新标签页 无标题 文档 文件 会议 笔记 今天 昨天 明天
`)
	out := make(map[string]bool, len(words))
	for _, w := range words {
		out[w] = true
	}
	return out
}()
