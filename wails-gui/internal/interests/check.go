package interests

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"wails-gui/internal/pii"
	"wails-gui/internal/topics"
)

// Rules are what an answer is checked against on the Mac.
type Rules struct {
	// Names are the people the user knows and the people the summaries
	// named; no word of theirs is ever in a name or a query.
	Names []string
	// OwnNames are the app's own names.
	OwnNames []string
	// Removed are the names the user took off.
	Removed []string
}

// Refusal is one interest or query the checks left out, for the log. It
// holds the reason only, never the text.
type Refusal struct {
	What   string // "interest" or "query"
	Reason string
}

// Reasons a name or a query is refused, besides the topics.Label* ones.
const (
	RefusedOwnName   = "own_name"
	RefusedPrivate   = "private"   // an address, a phone number, a code
	RefusedSensitive = "sensitive" // health, money, legal, religion, sexuality
	RefusedLength    = "length"
	RefusedRemoved   = "removed"
	RefusedEvidence  = "no_evidence"
)

// sensitiveTerms back the prompt's rule on sensitive subjects, one list per
// category. A term ending in "*" matches the start of a word; any other term
// matches whole words. A name or a query with one is never searched.
var sensitiveTerms = [][]string{
	// health
	{"health", "healthcare", "medical*", "medicine", "medicat*", "hospital*", "clinic", "clinics", "doctor*",
		"physician*", "nurse*", "patient*", "mri", "ct scan", "x-ray", "diagnosis", "diagnosed", "symptom*",
		"therap*", "psychiatr*", "psycholog*", "mental health", "mental illness", "depress*", "anxiety", "adhd", "autism", "cancer*",
		"tumor*", "hiv", "diabet*", "disease*", "illness*", "injur*", "pregnan*", "fertil*", "ivf", "rehab*",
		"addict*", "abortion*", "prescript*", "pharmac*", "dental", "dentist*", "surger*", "surgical",
		"vaccin*", "covid"},
	// body and appearance
	{"weight loss", "diet", "diets", "dieting", "fitness", "workout*", "bodybuild*", "body image",
		"skincare", "skin care", "acne", "hair loss", "dermatolog*", "cosmetic*", "botox", "plastic surgery"},
	// money and accounts
	{"bank", "banks", "banking", "loan*", "mortgage*", "debt", "debts", "credit card*", "credit score",
		"salary", "salaries", "payroll", "taxes", "tax return*", "tax refund*", "tax filing", "investing", "investment*", "investor*", "stocks",
		"stock market", "stock price*", "crypto", "cryptocurrenc*", "bitcoin*", "ethereum", "insurance",
		"retirement", "pension*"},
	// legal matters
	{"lawyer*", "attorney*", "lawsuit*", "court", "courts", "legal", "litigation", "arrest*", "criminal",
		"custody", "divorce*", "immigration", "visa", "visas", "deport*"},
	// politics
	{"politic*", "election*", "vote", "votes", "voting", "voter*", "democrat*", "republican*",
		"parliament*", "senate", "protest*", "activism", "activist*"},
	// religion
	{"religio*", "church*", "mosque*", "temple", "synagogue*", "bible", "quran", "koran", "buddhis*",
		"christian*", "islam*", "muslim*", "jewish", "judaism", "hindu*", "pray*", "faith"},
	// sexuality
	{"sex", "sexual*", "porn*", "gay", "lesbian*", "lgbt*", "queer", "transgender", "dating", "hookup*",
		"onlyfans"},
}

// sensitiveHan are the same categories in Han text, matched anywhere.
var sensitiveHan = []string{
	"健康", "医疗", "医院", "医生", "看病", "疾病", "病情", "症状", "诊断", "治疗", "手术", "药物", "处方", "心理",
	"抑郁", "焦虑", "癌", "怀孕", "体检", "牙科", "核磁", "疫苗",
	"减肥", "健身", "整容", "护肤", "美容", "脱发", "痘",
	"银行", "贷款", "债务", "欠款", "工资", "薪水", "报税", "个税", "税务", "投资", "理财", "股票", "基金", "炒股",
	"加密货币", "比特币", "保险", "信用卡",
	"律师", "法院", "诉讼", "官司", "起诉", "离婚", "移民", "签证", "逮捕", "犯罪",
	"政治", "选举", "投票", "政党", "党员", "抗议",
	"宗教", "信仰", "教会", "寺庙", "佛教", "基督", "伊斯兰", "圣经",
	"色情", "同性恋", "性取向", "约会", "性生活",
}

// sensitiveExceptions are everyday phrases that hold a sensitive word.
var sensitiveExceptions = []string{"technical debt", "tech debt", "health check", "health checks", "legal entity"}

// checkText reports why a name or a query may not be searched, or "". A
// name must hold more than one plain word; a query may be all names of
// things ("Claude Code").
func checkText(text string, maxWords, maxRunes int, query bool, rules Rules) string {
	if text == "" {
		return topics.LabelEmpty
	}
	if len(strings.Fields(text)) > maxWords || utf8.RuneCountInString(text) > maxRunes {
		return RefusedLength
	}
	lower := strings.ToLower(text)
	for _, own := range rules.OwnNames {
		if own = strings.ToLower(strings.TrimSpace(own)); own != "" && strings.Contains(lower, own) {
			return RefusedOwnName
		}
	}
	if redacted, n := pii.RedactText(text); n > 0 || pii.RedactCodes(redacted) != text {
		return RefusedPrivate
	}
	for _, word := range strings.Fields(text) {
		if codeWord(strings.Trim(word, ".,;:!?()[]\"'")) {
			return RefusedPrivate
		}
	}
	if streetLike.MatchString(text) {
		return RefusedPrivate
	}
	if sensitive(lower) {
		return RefusedSensitive
	}
	switch reason := topics.CheckLabel(text, rules.Names); reason {
	case topics.LabelGeneric, topics.LabelEmpty:
		if query {
			return ""
		}
		return reason
	default:
		return reason
	}
}

// streetLike is a house number, a capitalized name and a street suffix, or
// a German street and its number: a search never carries one, even without
// the ZIP that screen masking asks for ("12 Oak Street Brooklyn lease",
// "Hauptstrasse 12"). Stricter than masking on purpose: a query that only
// looks like an address ("2 Hour Drive to Napa") is lost, nothing more.
var streetLike = regexp.MustCompile(`\b\d{1,6}[A-Za-z]?\s+(?:[A-Z][a-zA-Z]+\s+){1,3}(?:St|Street|Ave|Avenue|Rd|Road|Blvd|Boulevard|Dr|Drive|Ln|Lane|Ct|Court|Way|Pl|Place|Cir|Circle|Pkwy|Parkway|Ter|Terrace|Loop|Trail|Hwy|Highway)\b|\b[A-Z]\p{L}*(?:strasse|straße|str\.)\s*\d{1,4}[a-z]?\b|\b[A-Z]\p{L}+\s+(?:Strasse|Straße)\s+\d{1,4}[a-z]?\b`)

// codeWord reports a word that reads as an order number or an id rather
// than the name of a thing: letters with three digits or more and no hyphen
// or dot ("A7K9Q2X4"). pii.RedactCodes already takes runs of four digits
// (years too) and long mixed runs; product names keep their shape
// ("ESP32-S3", "GPT-5", "M4", "iOS 18").
func codeWord(word string) bool {
	digits, letters := 0, 0
	for _, r := range word {
		switch {
		case unicode.IsDigit(r):
			digits++
		case unicode.IsLetter(r):
			letters++
		}
	}
	return digits >= 3 && letters >= 2 && !strings.ContainsAny(word, "-.")
}

// Sensitive reports text on a sensitive subject (health, body, money, legal
// matters, politics, religion, sexuality), by the same rule interests use.
func Sensitive(text string) bool {
	return sensitive(strings.ToLower(text))
}

func sensitive(lower string) bool {
	joined := " " + strings.Join(strings.Fields(KeyFor(lower)), " ") + " "
	for _, phrase := range sensitiveExceptions {
		joined = strings.ReplaceAll(joined, " "+phrase+" ", " ")
	}
	for _, category := range sensitiveTerms {
		for _, term := range category {
			if prefix, ok := strings.CutSuffix(term, "*"); ok {
				if strings.Contains(joined, " "+prefix) {
					return true
				}
			} else if strings.Contains(joined, " "+term+" ") {
				return true
			}
		}
	}
	for _, han := range sensitiveHan {
		if strings.Contains(lower, han) {
			return true
		}
	}
	return false
}

// removedClose reports a name close to one the user removed.
func removedClose(name string, removed []string) bool {
	for _, r := range removed {
		if Similarity(name, r) >= sameInterest {
			return true
		}
	}
	return false
}

// Accept checks an answer against the rules and the evidence the prompt
// held (ids from Prompt) and returns the interests that pass, near-duplicates
// in the answer merged into the first. A name that fails any check drops the
// interest; a query that fails is dropped alone, and an interest left with
// no query searches for its name.
func Accept(ans Answer, ids map[string]int, items []Item, rules Rules) ([]Interest, []Refusal) {
	var out []Interest
	var refused []Refusal
	// The people the model saw join the names, before any check.
	rules.Names = append(append([]string(nil), rules.Names...), ans.People...)
	for _, c := range ans.Interests {
		name := cleanName(c.Name)
		reason := checkText(name, NameMaxWords, NameMaxRunes, false, rules)
		if reason == "" && removedClose(name, rules.Removed) {
			reason = RefusedRemoved
		}
		var refs []Ref
		seen := map[string]bool{}
		for _, id := range c.Evidence {
			i, ok := ids[strings.TrimSpace(id)]
			if !ok || i < 0 || i >= len(items) || seen[items[i].Ref] {
				continue
			}
			seen[items[i].Ref] = true
			item := items[i]
			ref := Ref{Key: item.Ref, Source: item.Source, Minutes: item.Minutes}
			if !item.At.IsZero() {
				ref.At = item.At.UnixMilli()
			}
			refs = append(refs, ref)
		}
		if reason == "" && len(refs) == 0 {
			reason = RefusedEvidence
		}
		if reason != "" {
			refused = append(refused, Refusal{What: "interest", Reason: reason})
			continue
		}
		var queries []string
		for _, q := range c.Queries {
			q = cleanName(q)
			if q == "" || contains(queries, q) {
				continue
			}
			if why := checkText(q, QueryMaxWords, QueryMaxRunes, true, rules); why != "" {
				refused = append(refused, Refusal{What: "query", Reason: why})
				continue
			}
			if removedClose(q, rules.Removed) {
				refused = append(refused, Refusal{What: "query", Reason: RefusedRemoved})
				continue
			}
			if len(queries) < MaxQueries {
				queries = append(queries, q)
			}
		}
		if len(queries) == 0 {
			queries = []string{name}
		}
		why := oneLine(c.Why, WhyMaxRunes)
		if topics.MentionsPerson(why, rules.Names) {
			// The why line stays on the Mac, but it never keeps a name: the
			// name words go, the rest of the sentence stays.
			why = topics.StripPerson(why, rules.Names)
		}
		it := Interest{
			Key:        KeyFor(name),
			Name:       name,
			Why:        why,
			Kind:       normalizeKind(c.Kind),
			Confidence: clampConfidence(c.Confidence),
			Queries:    queries,
			Evidence:   refs,
		}
		if dup := duplicateOf(out, name); dup >= 0 {
			out[dup].Evidence = joinRefs(out[dup].Evidence, it.Evidence)
			if it.Confidence > out[dup].Confidence {
				out[dup].Confidence = it.Confidence
			}
			continue
		}
		out = append(out, it)
	}
	return out, refused
}

// cleanName folds whitespace and drops the quotes and end punctuation a
// model adds.
func cleanName(raw string) string {
	name := strings.Join(strings.Fields(raw), " ")
	return strings.Trim(name, "\"'`“”‘’.,;:!?。，；：！？")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

func duplicateOf(list []Interest, name string) int {
	for i, it := range list {
		if Similarity(it.Name, name) >= sameInterest {
			return i
		}
	}
	return -1
}

func normalizeKind(raw string) Kind {
	switch Kind(strings.ToLower(strings.TrimSpace(raw))) {
	case KindProject:
		return KindProject
	case KindResearch:
		return KindResearch
	case KindRoutine:
		return KindRoutine
	default:
		return KindTheme
	}
}

func clampConfidence(c *float64) float64 {
	if c == nil {
		return 0.5
	}
	switch {
	case *c < 0:
		return 0
	case *c > 1:
		return 1
	default:
		return *c
	}
}

// Recheck applies the current rules to stored interests: names and people
// learned since, own names and removals. An interest whose name now fails
// goes, unless keep holds its id (the user pinned it): then it stays with
// no query, since its name may not be searched either. A query that fails
// goes alone, and an interest left with none searches its name only when
// the name passes.
func Recheck(list []Interest, rules Rules, keep map[string]bool) []Interest {
	var out []Interest
	for _, it := range list {
		nameOK := checkText(it.Name, NameMaxWords, NameMaxRunes, false, rules) == ""
		if !nameOK && !keep[it.ID] {
			continue
		}
		var queries []string
		for _, q := range it.Queries {
			if checkText(q, QueryMaxWords, QueryMaxRunes, true, rules) == "" && !removedClose(q, rules.Removed) {
				queries = append(queries, q)
			}
		}
		if !nameOK {
			queries = nil
		} else if len(queries) == 0 {
			queries = []string{it.Name}
		}
		it.Queries = queries
		if topics.MentionsPerson(it.Why, rules.Names) {
			it.Why = topics.StripPerson(it.Why, rules.Names)
		}
		out = append(out, it)
	}
	return out
}

// SafeToSend reports whether a label may go to a search under the rules:
// no person, own name, address, code or sensitive subject. Unlike a model's
// name, it may be one word (a phrase topic).
func SafeToSend(label string, rules Rules) bool {
	return checkText(label, QueryMaxWords, QueryMaxRunes, true, rules) == ""
}
