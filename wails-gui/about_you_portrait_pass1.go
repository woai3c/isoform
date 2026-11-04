package main

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Pass 1 of the portrait: everything that needs no model. It groups the
// beliefs, computes the four sides (and so the name and the shape), picks the
// facts sentence 2 may use and the one Lately line, and says whether there is
// enough to write at all. It is deterministic so the name stays stable.

// The four sides, in the shape's order: top, right, bottom, left. A value is
// how far the person leans toward the second pole: 0 is fully the first.
const (
	portraitSideFocus     = iota // deep focus .. many threads
	portraitSideReasoning        // first principles .. precedent
	portraitSideWords            // brief .. detailed
	portraitSidePlanning         // plans ahead .. goes with the flow
)

// A side flips only once its value is this far past the middle.
const portraitFlipMargin = 0.10

// The bar for writing the first portrait (handoff section 8).
const (
	portraitNeedBeliefs    = 20
	portraitNeedDays       = 5
	portraitNeedGroups     = 3
	portraitGroupMinimum   = 3
	portraitRewriteChanges = 10
)

// The support bar (portrait v2.0.0 section 1): a claim passes on a belief
// the person confirmed, or on cited beliefs that together rest on 3 or more
// observations on 2 or more local days.
const (
	portraitNeedObservations = 3
	portraitNeedObservedDays = 2
	portraitSideMinimum      = 5
)

// Kinds of observation a belief rests on.
const (
	portraitObservedMoment    = "moment"    // a session Isoform saw on this computer
	portraitObservedSession   = "session"   // an imported Claude Code or Codex session
	portraitObservedRule      = "rule"      // a CLAUDE.md or AGENTS.md rule, by the file's last change
	portraitObservedStatement = "statement" // a statement brought in, by the day it arrived
)

// portraitObservation is one dated thing a belief rests on. The id makes the
// same session cited by two beliefs count once.
type portraitObservation struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Day  string `json:"day"`
}

func portraitLocalDay(at int64) string {
	return time.UnixMilli(at).In(time.Local).Format("2006-01-02")
}

// portraitSource is one readable belief as pass 1 sees it.
type portraitSource struct {
	ID               string
	Title            string
	Points           []string
	Area             string
	Source           string
	Confirmed        bool
	NotQuite         bool
	FirstLearnedAt   int64
	SessionTimes     []int64
	Observations     []portraitObservation
	SentMessageCount int
	Digest           string
}

func (s portraitSource) text() string {
	return strings.ToLower(s.Title + "\n" + strings.Join(s.Points, "\n"))
}

// observations are what the belief rests on: its sessions on this computer
// and its imported sessions by their own dates. A belief with neither counts
// once, on the day it arrived.
func (s portraitSource) observations() []portraitObservation {
	if len(s.Observations) > 0 {
		return s.Observations
	}
	var out []portraitObservation
	for i, at := range s.SessionTimes {
		out = append(out, portraitObservation{ID: fmt.Sprintf("%s:%d", s.ID, i), Kind: portraitObservedMoment, Day: portraitLocalDay(at)})
	}
	if len(out) == 0 && s.FirstLearnedAt > 0 {
		out = append(out, portraitObservation{ID: "statement:" + s.ID, Kind: portraitObservedStatement, Day: portraitLocalDay(s.FirstLearnedAt)})
	}
	return out
}

// days are the local dates of the belief's observations.
func (s portraitSource) days() []string {
	seen := map[string]bool{}
	for _, observation := range s.observations() {
		seen[observation.Day] = true
	}
	days := make([]string, 0, len(seen))
	for day := range seen {
		days = append(days, day)
	}
	sort.Strings(days)
	return days
}

// weight follows the belief's confidence: what the person confirmed counts
// most, then what was seen on several days.
func (s portraitSource) weight() float64 {
	switch true {
	case s.Confirmed:
		return 3
	case len(s.observations()) >= 4 || len(s.days()) >= 2:
		return 2
	default:
		return 1
	}
}

// Words that pull a side toward each pole, in English and Chinese. Matching
// is on the belief's own title and points.
var portraitSideLexicon = [4][2][]string{
	portraitSideFocus: {
		{"deep work", "deep focus", "focus", "uninterrupted", "long block", "blocks of", "one thing at a time", "single task", "heads-down", "heads down", "do not disturb", "notifications off", "专注", "深度", "整块时间", "不被打扰", "一次只做"},
		{"switch", "multitask", "multiple projects", "several projects", "many projects", "in parallel", "parallel", "juggle", "many threads", "context switch", "back and forth", "many tabs", "同时", "切换", "多个项目", "并行", "多线程"},
	},
	portraitSideReasoning: {
		{"first principles", "from scratch", "prototype", "builds the first", "experiment", "tests", "data", "number", "measure", "benchmark", "asks why", "root cause", "own version", "第一性", "从零", "原型", "实验", "数据", "数字", "根本原因"},
		{"template", "reuse", "existing", "convention", "checklist", "playbook", "best practice", "same way", "as before", "precedent", "standard", "copies", "follows the", "模板", "复用", "沿用", "惯例", "清单", "照着", "先例"},
	},
	portraitSideWords: {
		{"short", "brief", "concise", "one line", "one-line", "two sentences", "terse", "bullet", "to the point", "first line", "no greeting", "简短", "一句", "两句", "简洁", "要点"},
		{"detailed", "long message", "long email", "thorough", "explains", "documents", "writes up", "context", "paragraph", "narrative", "详细", "长文", "解释", "文档", "背景"},
	},
	portraitSidePlanning: {
		{"plans", "plan ahead", "in advance", "ahead of time", "outline", "schedule", "calendar", "agenda", "routine", "every morning", "every week", "months ahead", "books", "prepares", "before starting", "计划", "提前", "日程", "规划", "大纲", "每天", "每周"},
		{"ad hoc", "ad-hoc", "spontaneous", "last minute", "last-minute", "improvis", "on the fly", "as it comes", "reactive", "whatever", "flexible", "临时", "随性", "即兴", "灵活", "随手"},
	},
}

// portraitSides returns the four side values and, for each side, the groups
// whose beliefs moved it most.
func portraitSides(sources []portraitSource) ([4]float64, [4][]string) {
	sides, areas, _ := portraitSideCounts(sources)
	return sides, areas
}

// portraitSideCounts also counts the beliefs each side rests on. The name and
// the shape show once every side rests on portraitSideMinimum or more.
func portraitSideCounts(sources []portraitSource) ([4]float64, [4][]string, [4]int) {
	var sides [4]float64
	var areas [4][]string
	var counts [4]int
	for side := range sides {
		var toward [2]float64
		byArea := map[string]float64{}
		for _, source := range sources {
			if source.NotQuite {
				continue
			}
			text := source.text()
			moved := false
			for pole := 0; pole < 2; pole++ {
				for _, word := range portraitSideLexicon[side][pole] {
					if strings.Contains(text, word) {
						toward[pole] += source.weight()
						byArea[memoryAreaOrOther(source.Area)] += source.weight()
						moved = true
						break
					}
				}
			}
			if moved {
				counts[side]++
			}
		}
		// Two beliefs' worth of smoothing keeps one belief from deciding a side.
		sides[side] = math.Round((0.5+0.5*(toward[1]-toward[0])/(toward[0]+toward[1]+2))*100) / 100
		areas[side] = portraitTopAreas(byArea, 2)
	}
	return sides, areas, counts
}

func memoryAreaOrOther(area string) string {
	switch area {
	case "communication", "work", "people", "tools", "routines", "interests":
		return area
	default:
		return "other"
	}
}

func portraitTopAreas(weights map[string]float64, limit int) []string {
	areas := make([]string, 0, len(weights))
	for area := range weights {
		areas = append(areas, area)
	}
	sort.Slice(areas, func(i, j int) bool {
		if weights[areas[i]] != weights[areas[j]] {
			return weights[areas[i]] > weights[areas[j]]
		}
		return areas[i] < areas[j]
	})
	if len(areas) > limit {
		areas = areas[:limit]
	}
	return areas
}

// portraitPoles turns side values into poles (0 the first, 1 the second). A
// side keeps its previous pole until it moves portraitFlipMargin past the
// middle, so the name does not flicker around 0.5.
func portraitPoles(sides [4]float64, previous []int) []int {
	poles := make([]int, 4)
	for side, value := range sides {
		if len(previous) == 4 {
			poles[side] = previous[side]
			if previous[side] == 0 && value > 0.5+portraitFlipMargin {
				poles[side] = 1
			}
			if previous[side] == 1 && value < 0.5-portraitFlipMargin {
				poles[side] = 0
			}
			continue
		}
		if value > 0.5 {
			poles[side] = 1
		}
	}
	return poles
}

// portraitNameWords returns the two keys of the name from the fixed set of 16.
// The first word comes from planning and reasoning, the second from focus and
// words.
func portraitNameWords(poles []int) (string, string) {
	first := [2][2]string{{"blueprint", "steady"}, {"curious", "practical"}}[poles[portraitSidePlanning]][poles[portraitSideReasoning]]
	second := [2][2]string{{"builder", "author"}, {"conductor", "connector"}}[poles[portraitSideFocus]][poles[portraitSideWords]]
	return first, second
}

// portraitNameAreas names the groups behind each word of the name.
func portraitNameAreas(areas [4][]string) ([]string, []string) {
	merge := func(a, b []string) []string {
		weights := map[string]float64{}
		for i, area := range a {
			weights[area] += float64(2 - i)
		}
		for i, area := range b {
			weights[area] += float64(2 - i)
		}
		return portraitTopAreas(weights, 2)
	}
	return merge(areas[portraitSidePlanning], areas[portraitSideReasoning]), merge(areas[portraitSideFocus], areas[portraitSideWords])
}

// AboutYouPortraitProgress is the Still learning bar.
type AboutYouPortraitProgress struct {
	Beliefs       int `json:"beliefs"`
	BeliefsNeeded int `json:"beliefsNeeded"`
	Days          int `json:"days"`
	DaysNeeded    int `json:"daysNeeded"`
	Groups        int `json:"groups"`
	GroupsNeeded  int `json:"groupsNeeded"`
	MoreDays      int `json:"moreDays"`
	// SidesReady says every side of the shape rests on five or more
	// beliefs (v2.0.0 section 2). It is reported, not required: the word
	// lists behind the sides find too few beliefs on real data (a 60-belief
	// fixture has 2 on focus), and requiring it would keep Bo's card in
	// Still learning. Open question for Isoform design.
	SidesReady bool `json:"sidesReady"`
	Met        bool `json:"met"`
}

// portraitReadiness checks the bar: 20 beliefs, observations on 5 days, and
// three groups with three or more beliefs each. The days left come from the
// current rate.
func portraitReadiness(sources []portraitSource) AboutYouPortraitProgress {
	days := map[string]bool{}
	groups := map[string]int{}
	for _, source := range sources {
		for _, day := range source.days() {
			days[day] = true
		}
		groups[memoryAreaOrOther(source.Area)]++
	}
	full := 0
	for _, count := range groups {
		if count >= portraitGroupMinimum {
			full++
		}
	}
	progress := AboutYouPortraitProgress{Beliefs: len(sources), BeliefsNeeded: portraitNeedBeliefs, Days: len(days), DaysNeeded: portraitNeedDays, Groups: full, GroupsNeeded: portraitNeedGroups}
	_, _, counts := portraitSideCounts(sources)
	progress.SidesReady = true
	for _, count := range counts {
		progress.SidesReady = progress.SidesReady && count >= portraitSideMinimum
	}
	progress.Met = progress.Beliefs >= portraitNeedBeliefs && progress.Days >= portraitNeedDays && full >= portraitNeedGroups
	if progress.Met {
		return progress
	}
	more := max(0, portraitNeedDays-progress.Days)
	if progress.Beliefs < portraitNeedBeliefs {
		rate := float64(progress.Beliefs) / float64(max(1, progress.Days))
		if rate > 0 {
			more = max(more, int(math.Ceil(float64(portraitNeedBeliefs-progress.Beliefs)/rate)))
		} else {
			more = max(more, portraitNeedDays)
		}
	}
	progress.MoreDays = max(1, more)
	return progress
}

var portraitNumber = regexp.MustCompile(`\d+(?:[.,:]\d+)?`)

// Numbers in the prose match their facts within rounding (v2.0.0 section 5,
// item 2): clock times to 5 minutes (30 after "about"), shares to 5 points,
// durations within a fifth in any unit ("about two days" for 38 to 58 hours),
// and counts exactly.
var (
	portraitClock       = regexp.MustCompile(`(?i)\b(\d{1,2})(?::(\d{2}))?\s*(am|pm|a\.m\.|p\.m\.)|\b(\d{1,2}):(\d{2})\b`)
	portraitShare       = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(?:%|percent)`)
	portraitDuration    = regexp.MustCompile(`(?i)\b(\d+(?:\.\d+)?|an?|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|half an?)\s*(minutes?|mins?|hours?|hrs?|days?|weeks?|months?)\b|(\d+(?:\.\d+)?)\s*(分钟|小时|天|周|个月)`)
	portraitNumberWords = map[string]float64{"a": 1, "an": 1, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12, "half a": 0.5, "half an": 0.5}
	portraitUnitMinutes = map[string]float64{"minute": 1, "min": 1, "hour": 60, "hr": 60, "day": 1440, "week": 10080, "month": 43200, "分钟": 1, "小时": 60, "天": 1440, "周": 10080, "个月": 43200}
	portraitAbout       = regexp.MustCompile(`(?i)(about|around|roughly|nearly|almost|大约|左右|差不多)\s*$`)
)

func portraitClockMinutes(match []string) float64 {
	hour, minute := 0.0, 0.0
	if match[1] != "" {
		hour, _ = strconv.ParseFloat(match[1], 64)
		minute, _ = strconv.ParseFloat(match[2], 64)
		if strings.HasPrefix(strings.ToLower(match[3]), "p") && hour < 12 {
			hour += 12
		}
		if strings.HasPrefix(strings.ToLower(match[3]), "a") && hour == 12 {
			hour = 0
		}
	} else {
		hour, _ = strconv.ParseFloat(match[4], 64)
		minute, _ = strconv.ParseFloat(match[5], 64)
	}
	return hour*60 + minute
}

func portraitDurationMinutes(match []string) float64 {
	amount, unit := strings.ToLower(match[1]), strings.ToLower(match[2])
	if match[3] != "" {
		amount, unit = match[3], match[4]
	}
	value, ok := portraitNumberWords[amount]
	if !ok {
		value, _ = strconv.ParseFloat(amount, 64)
	}
	unit = strings.TrimSuffix(unit, "s")
	return value * portraitUnitMinutes[unit]
}

// portraitQuantities pulls clock times, shares and durations out of a text
// and returns them with the text that is left, for the exact count check.
func portraitQuantities(text string) (clocks, shares, durations []float64, rest string) {
	rest = portraitClock.ReplaceAllStringFunc(text, func(found string) string {
		clocks = append(clocks, portraitClockMinutes(portraitClock.FindStringSubmatch(found)))
		return " "
	})
	rest = portraitShare.ReplaceAllStringFunc(rest, func(found string) string {
		value, _ := strconv.ParseFloat(portraitShare.FindStringSubmatch(found)[1], 64)
		shares = append(shares, value)
		return " "
	})
	rest = portraitDuration.ReplaceAllStringFunc(rest, func(found string) string {
		durations = append(durations, portraitDurationMinutes(portraitDuration.FindStringSubmatch(found)))
		return " "
	})
	return clocks, shares, durations, rest
}

func portraitNumbersWithin(text, source string) bool {
	srcClocks, srcShares, srcDurations, srcRest := portraitQuantities(source)
	allowed := map[string]bool{}
	for _, number := range portraitNumber.FindAllString(source, -1) {
		allowed[number] = true
	}
	for _, number := range portraitNumber.FindAllString(srcRest, -1) {
		allowed[number] = true
	}
	near := func(value float64, among []float64, within func(float64) float64) bool {
		for _, candidate := range among {
			if math.Abs(value-candidate) <= within(candidate) {
				return true
			}
		}
		return false
	}
	// "about" before a clock time widens it to half an hour.
	for _, index := range portraitClock.FindAllStringSubmatchIndex(text, -1) {
		match := make([]string, 6)
		for group := 0; group < 6; group++ {
			if index[2*group] >= 0 {
				match[group] = text[index[2*group]:index[2*group+1]]
			}
		}
		within := 5.0
		if portraitAbout.MatchString(text[:index[0]]) {
			within = 30
		}
		if !near(portraitClockMinutes(match), srcClocks, func(float64) float64 { return within }) {
			return false
		}
	}
	_, shares, durations, rest := portraitQuantities(text)
	for _, share := range shares {
		if !near(share, srcShares, func(float64) float64 { return 5 }) {
			return false
		}
	}
	for _, duration := range durations {
		if !near(duration, srcDurations, func(candidate float64) float64 { return candidate * 0.21 }) {
			return false
		}
	}
	for _, number := range portraitNumber.FindAllString(rest, -1) {
		if !allowed[number] {
			return false
		}
	}
	return true
}

// portraitFact is a belief sentence 2 may rest on: its own words carry a
// number or a time, or it was counted from sent messages, so the contrast is
// the person's data, never invented.
type portraitFact struct {
	ID    string   `json:"id"`
	Facts []string `json:"facts,omitempty"`
}

// portraitFacts picks up to eight candidates, the most certain first.
func portraitFacts(sources []portraitSource) []portraitFact {
	sorted := append([]portraitSource(nil), sources...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].weight() > sorted[j].weight() })
	var facts []portraitFact
	for _, source := range sorted {
		if portraitSensitiveTopic(source.text()) {
			continue
		}
		numbered := portraitNumber.MatchString(source.Title + " " + strings.Join(source.Points, " "))
		if !numbered && source.SentMessageCount == 0 {
			continue
		}
		var lines []string
		if days := len(source.days()); days >= 2 {
			lines = append(lines, "seen "+strconv.Itoa(len(source.observations()))+" times on "+strconv.Itoa(days)+" days")
		}
		if source.SentMessageCount > 0 {
			lines = append(lines, "counted from "+strconv.Itoa(source.SentMessageCount)+" messages the person sent")
		}
		facts = append(facts, portraitFact{ID: source.ID, Facts: lines})
		if len(facts) == 8 {
			break
		}
	}
	return facts
}

// Lately never speaks about health, money or relationships; sentence 2
// facts skip them too.
var portraitSensitiveWords = []string{"health", "doctor", "medic", "therap", "sleep", "weight", "diet", "pregnan", "money", "salary", "bank", "debt", "invest", "loan", "budget", "dating", "partner", "divorce", "relationship", "boyfriend", "girlfriend", "wife", "husband", "健康", "医生", "治疗", "睡眠", "体重", "怀孕", "工资", "银行", "债", "投资", "贷款", "恋爱", "伴侣", "离婚", "感情"}

func portraitSensitiveTopic(text string) bool {
	text = strings.ToLower(text)
	for _, word := range portraitSensitiveWords {
		if strings.Contains(text, word) {
			return true
		}
	}
	return false
}

// portraitLatelyFact is a routine whose count per week this month is half or
// less, or double or more, of its median over the previous 8 weeks.
type portraitLatelyFact struct {
	MemoryID  string `json:"memoryId"`
	ThisMonth int    `json:"thisMonth"`
	Usual     int    `json:"usual"`
}

// portraitLately picks the clearest such routine, with at least two weeks of
// this month seen. It returns nil when nothing differs.
func portraitLately(sources []portraitSource, now time.Time) *portraitLatelyFact {
	now = now.In(time.Local)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	weeksThisMonth := now.Sub(monthStart).Hours() / 24 / 7
	if weeksThisMonth < 2 {
		return nil
	}
	var best *portraitLatelyFact
	bestScore := 0.0
	for _, source := range sources {
		if memoryAreaOrOther(source.Area) != "routines" || portraitSensitiveTopic(source.text()) {
			continue
		}
		thisMonth := 0
		weekly := make([]int, 8)
		for _, at := range source.SessionTimes {
			seen := time.UnixMilli(at).In(time.Local)
			if !seen.Before(monthStart) && !seen.After(now) {
				thisMonth++
				continue
			}
			week := int(monthStart.Sub(seen).Hours() / 24 / 7)
			if seen.Before(monthStart) && week < 8 {
				weekly[week]++
			}
		}
		sort.Ints(weekly)
		usual := float64(weekly[3]+weekly[4]) / 2
		if usual < 1 {
			continue
		}
		perWeek := float64(thisMonth) / weeksThisMonth
		ratio := perWeek / usual
		if ratio > 0.5 && ratio < 2 {
			continue
		}
		fact := &portraitLatelyFact{MemoryID: source.ID, ThisMonth: int(math.Round(perWeek)), Usual: int(math.Round(usual))}
		if fact.ThisMonth == fact.Usual {
			continue
		}
		score := math.Abs(math.Log((perWeek + 0.1) / usual))
		if best == nil || score > bestScore {
			best, bestScore = fact, score
		}
	}
	return best
}

// portraitBelieved is the bar for a sentence, clause or line (section 1): one
// confirmed belief, or cited beliefs whose observations together number 3 or
// more on 2 or more local days. A belief marked Not quite never supports.
// Forgotten and protected beliefs are never live, so they never count.
func portraitBelieved(ids []string, live map[string]portraitInput) bool {
	observed := map[string]string{}
	for _, id := range ids {
		input, ok := live[id]
		if !ok || input.NotQuite {
			continue
		}
		if input.Confirmed {
			return true
		}
		for _, observation := range input.observations() {
			observed[observation.ID] = observation.Day
		}
	}
	days := map[string]bool{}
	for _, day := range observed {
		days[day] = true
	}
	return len(observed) >= portraitNeedObservations && len(days) >= portraitNeedObservedDays
}

// portraitObservationCount counts the distinct observations under the ids,
// for ranking confirm rows.
func portraitObservationCount(input portraitInput) int {
	return len(input.observations())
}

// portraitChanges counts beliefs changed, confirmed or forgotten among those
// the portrait was written from, and beliefs learned or updated since.
func portraitChanges(previous []portraitInput, sources []portraitSource, since int64) int {
	changes := 0
	current := make(map[string]portraitSource, len(sources))
	for _, source := range sources {
		current[source.ID] = source
	}
	written := map[string]bool{}
	for _, input := range previous {
		written[input.MemoryID] = true
		now, ok := current[input.MemoryID]
		switch true {
		case !ok, now.Digest != input.Digest, now.Confirmed != input.Confirmed:
			changes++
		}
	}
	for _, source := range sources {
		if !written[source.ID] && source.FirstLearnedAt > since {
			changes++
		}
	}
	return changes
}
