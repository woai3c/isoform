package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// "Your week" is the evening look back for the person (docs/your-week/
// handoff.md): where the week went, what got done for them, what is still
// open, what Isoform learned, and one recommendation for the next workday. It
// is the last evening brief of the configured work week. Every part hides
// when empty, and no system count, internal name, caveat or zero is ever
// shown: the only numbers are hours by topic and counts of things the person
// can open.

const (
	yourWeekTimeTopics    = 3
	yourWeekDoneMax       = 3
	yourWeekOpenMax       = 3
	yourWeekLearnedMax    = 2
	yourWeekQuietSeenDays = 2
	yourWeekChipMaxRunes  = 40

	yourWeekDoneOutcome     = "outcome"
	yourWeekDoneDeliverable = "deliverable"
	yourWeekDoneMeetings    = "meetings"

	yourWeekLearnedMemory = "memory"
	yourWeekLearnedMute   = "mute"
)

// MobileBriefWeek is the brief's "Your week" card. Older phones ignore it and
// read the same week as paragraphs in WeeklyRecap.
type MobileBriefWeek struct {
	// Start and End are the work week's first and last local dates.
	Start string `json:"start"`
	End   string `json:"end"`
	// Headline is the lead sentence; named so the display pass
	// (displayModelCopy) reads it as text.
	Headline string                `json:"lead"`
	Time     []MobileBriefWeekTime `json:"time,omitempty"`
	Done     []MobileBriefWeekDone `json:"done,omitempty"`
	// DoneMore counts the other things done for the person, shown as
	// "N more in Activity"; never zero on the wire.
	DoneMore int                      `json:"doneMore,omitempty"`
	Open     []MobileBriefWeekOpen    `json:"open,omitempty"`
	Learned  []MobileBriefWeekLearned `json:"learned,omitempty"`
	Ask      *MobileBriefWeekAsk      `json:"ask,omitempty"`
}

// MobileBriefWeekTime is one bar of "Where your time went": a topic, or the
// rest of the time on this computer (Other). Hours are whole and at least 1.
type MobileBriefWeekTime struct {
	TopicID string `json:"topicId,omitempty"`
	Name    string `json:"name,omitempty"`
	Other   bool   `json:"other,omitempty"`
	Hours   int    `json:"hours"`
}

// MobileBriefWeekDone is one thing done for the person that they used. A
// deliverable opens its file; a source-bound task opens its thread.
type MobileBriefWeekDone struct {
	TaskID string `json:"taskId,omitempty"`
	ID     string `json:"id"`
	Kind   string `json:"kind"` // outcome | deliverable | meetings
	Title  string `json:"title,omitempty"`
	At     string `json:"at,omitempty"`
	// Count is the number of calls with notes (Kind meetings), at least 1.
	Count    int    `json:"count,omitempty"`
	Source   string `json:"source,omitempty"`
	SourceID string `json:"sourceId,omitempty"`
	Path     string `json:"path,omitempty"`
}

// MobileBriefWeekOpen is one still-open item. Its chip is the item's own card
// (CardID, the row's "item:" card) with a label by the brief-chips rule
// (docs/brief-chips/handoff.md); no label means no chip.
type MobileBriefWeekOpen struct {
	ItemKey string `json:"itemKey"`
	CardID  string `json:"cardId,omitempty"`
	Title   string `json:"title"`
	Since   string `json:"since,omitempty"`
	Label   string `json:"label,omitempty"`
}

// MobileBriefWeekLearned is one thing Isoform learned that changed what it
// does, with its fix: a memory opens on Memory ("Not right"), a mute is
// undone ("Undo").
type MobileBriefWeekLearned struct {
	ID             string `json:"id"`
	Kind           string `json:"kind"` // memory | mute
	Text           string `json:"text,omitempty"`
	MemoryID       string `json:"memoryId,omitempty"`
	EngagementKind string `json:"engagementKind,omitempty"`
}

// MobileBriefWeekAsk is the one next-workday recommendation. Accepting it puts
// its Message in the chat box, unsent: through its card (CardID) when the
// subject is a brief row, else in the topic's own box (TopicID).
type MobileBriefWeekAsk struct {
	Text    string             `json:"text"`
	Subject string             `json:"-"`
	Message string             `json:"message"`
	Day     string             `json:"day,omitempty"`
	CardID  string             `json:"cardId,omitempty"`
	TopicID string             `json:"topicId,omitempty"`
	Hold    *TodayDayShapeHold `json:"hold,omitempty"`
	// Refs are the subject row's refs, for the ask's card.
	Refs []string `json:"-"`
}

type yourWeekParagraphRole string

const (
	yourWeekParagraphLead    yourWeekParagraphRole = "lead"
	yourWeekParagraphTime    yourWeekParagraphRole = "time"
	yourWeekParagraphDone    yourWeekParagraphRole = "done"
	yourWeekParagraphOpen    yourWeekParagraphRole = "open"
	yourWeekParagraphLearned yourWeekParagraphRole = "learned"
	yourWeekParagraphAsk     yourWeekParagraphRole = "ask"
)

type yourWeekParagraph struct {
	Role yourWeekParagraphRole
	Text string
}

// yourWeekTopic is one readable topic and its minutes this week.
type yourWeekTopic struct {
	ID, Name string
	Minutes  int64
}

// yourWeekOpenCandidate is one row that needs the person, in attention order,
// with the section its subject sits in.
type yourWeekOpenCandidate struct {
	Item    MobileBriefItem
	Section string
}

// yourWeekInput is everything "Your week" is selected from, already read
// through each source's gates.
type yourWeekInput struct {
	Lang               string // en | zh
	Start, End         time.Time
	NextWorkDay        time.Time
	SeenDays, WorkDays int
	Topics             []yourWeekTopic
	TotalMinutes       int64
	Done               []MobileBriefWeekDone // outcomes and deliverables, any order
	Meetings           int                   // calls with readable notes
	MeetingsAt         time.Time
	Open               []yourWeekOpenCandidate
	Memories           []MobileBriefWeekLearned
	Muted              []string
}

// selectYourWeek picks the parts of the card, capped and in order. It returns
// nil when there is nothing to say at all.
func selectYourWeek(in yourWeekInput) *MobileBriefWeek {
	lang := in.Lang
	week := &MobileBriefWeek{Start: in.Start.Format("2006-01-02"), End: in.End.Format("2006-01-02")}
	week.Time = yourWeekTimeRows(in.Topics, in.TotalMinutes, lang)

	done := make([]MobileBriefWeekDone, 0, len(in.Done)+1)
	for _, item := range in.Done {
		if item.Title = strings.TrimSpace(item.Title); item.Title != "" && yourWeekCopyAllowed(item.Title) {
			done = append(done, item)
		}
	}
	// What the person sent or opened first, newest first within each kind.
	sort.SliceStable(done, func(i, j int) bool {
		if done[i].Kind != done[j].Kind {
			return done[i].Kind == yourWeekDoneOutcome
		}
		return done[i].At > done[j].At
	})
	if in.Meetings > 0 {
		meetings := MobileBriefWeekDone{ID: "meetings:" + week.Start, Kind: yourWeekDoneMeetings, Count: in.Meetings}
		if !in.MeetingsAt.IsZero() {
			meetings.At = in.MeetingsAt.Format(time.RFC3339)
		}
		// One row for all the calls, after the replies and before files.
		at := len(done)
		for i, item := range done {
			if item.Kind != yourWeekDoneOutcome {
				at = i
				break
			}
		}
		done = append(done[:at], append([]MobileBriefWeekDone{meetings}, done[at:]...)...)
	}
	if len(done) > yourWeekDoneMax {
		for _, item := range done[yourWeekDoneMax:] {
			if item.Kind == yourWeekDoneMeetings {
				week.DoneMore += item.Count
			} else {
				week.DoneMore++
			}
		}
		done = done[:yourWeekDoneMax]
	}
	if len(done) > 0 {
		week.Done = done
	}

	for _, candidate := range in.Open {
		if len(week.Open) == yourWeekOpenMax {
			break
		}
		title := strings.TrimSpace(mobileBriefCardTitle(candidate.Item))
		if title == "" || !yourWeekCopyAllowed(title) {
			continue
		}
		key := mobileBriefCardItemKey(candidate.Item)
		open := MobileBriefWeekOpen{ItemKey: key, Title: title, Since: candidate.Item.Since}
		if label := yourWeekChipLabel(candidate, title, lang); label != "" && key != "" {
			open.Label, open.CardID = label, "item:"+key
		}
		week.Open = append(week.Open, open)
	}

	for _, kind := range in.Muted {
		if len(week.Learned) == yourWeekLearnedMax {
			break
		}
		if text := yourWeekMuteText(kind, lang); text != "" {
			week.Learned = append(week.Learned, MobileBriefWeekLearned{ID: "mute:" + kind, Kind: yourWeekLearnedMute, Text: text, EngagementKind: kind})
		}
	}
	for _, memory := range in.Memories {
		if len(week.Learned) == yourWeekLearnedMax {
			break
		}
		if memory.Text = strings.TrimSpace(memory.Text); memory.Text != "" && memory.MemoryID != "" && yourWeekCopyAllowed(memory.Text) {
			memory.ID, memory.Kind = "memory:"+memory.MemoryID, yourWeekLearnedMemory
			week.Learned = append(week.Learned, memory)
		}
	}

	week.Ask = yourWeekAsk(in, week, lang)
	quiet := in.SeenDays >= 0 && in.SeenDays <= yourWeekQuietSeenDays && in.SeenDays < in.WorkDays
	if !quiet && len(week.Time) == 0 && len(week.Done) == 0 && len(week.Open) == 0 && len(week.Learned) == 0 {
		return nil
	}
	week.Headline = yourWeekLead(in, week, quiet, lang)
	return week
}

// yourWeekTimeRows is the top three topics by whole hours, plus the rest of
// the time on this computer. A topic under an hour is part of Other, and a
// bar that would read 0 h is never drawn.
func yourWeekTimeRows(topics []yourWeekTopic, totalMinutes int64, lang string) []MobileBriefWeekTime {
	ranked := append([]yourWeekTopic(nil), topics...)
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Minutes > ranked[j].Minutes })
	var rows []MobileBriefWeekTime
	var shown int64
	for _, topic := range ranked {
		name := strings.TrimSpace(topic.Name)
		hours := yourWeekHours(topic.Minutes)
		if len(rows) == yourWeekTimeTopics || hours < 1 || name == "" || !topicDisplayNameSafe(name) || !yourWeekCopyAllowed(name) {
			continue
		}
		rows = append(rows, MobileBriefWeekTime{TopicID: topic.ID, Name: name, Hours: hours})
		shown += topic.Minutes
	}
	if len(rows) == 0 {
		return nil
	}
	if other := yourWeekHours(totalMinutes - shown); other >= 1 {
		rows = append(rows, MobileBriefWeekTime{Other: true, Hours: other})
	}
	return rows
}

func yourWeekHours(minutes int64) int {
	if minutes <= 0 {
		return 0
	}
	return int((minutes + 30) / 60)
}

// yourWeekChipLabel is the still-open row's one chip, by what its subject is
// (docs/brief-chips/handoff.md). Subjects with no clear next step, or no
// plain name within the label budget, get none.
func yourWeekChipLabel(candidate yourWeekOpenCandidate, title, lang string) string {
	item := candidate.Item
	var label string
	switch {
	case item.Kind == "waiting_on_reply" || candidate.Section == mobileBriefSectionWaitingOnYou:
		label = pick(lang, "Draft the reply to "+title, "起草给"+title+"的回复")
	case candidate.Section == mobileBriefSectionCommitments:
		label = pick(lang, "Find time for "+title, "为“"+title+"”留出时间")
	case candidate.Section == mobileBriefSectionWorkingToward:
		label = pick(lang, "What's left on "+title+"?", "“"+title+"”还差什么？")
	case candidate.Section == mobileBriefSectionDeliverables || item.Kind == "one_time_work" && item.Status == "needs_review":
		label = pick(lang, "Open "+title, "打开"+title)
	default:
		return ""
	}
	if utf8.RuneCountInString(label) > yourWeekChipMaxRunes || !yourWeekCopyAllowed(label) {
		return ""
	}
	return label
}

// yourWeekAsk is one recommendation for the next first workday: start on the
// first open commitment or goal, else on the topic that took the most time.
func yourWeekAsk(in yourWeekInput, week *MobileBriefWeek, lang string) *MobileBriefWeekAsk {
	ask := func(subject string) *MobileBriefWeekAsk {
		nextDay := in.NextWorkDay
		if nextDay.IsZero() {
			nextDay = in.End.AddDate(0, 0, 1)
			for nextDay.Weekday() == time.Saturday || nextDay.Weekday() == time.Sunday {
				nextDay = nextDay.AddDate(0, 0, 1)
			}
		}
		weekday := yourWeekWeekdayName(nextDay.Weekday(), lang)
		return &MobileBriefWeekAsk{
			Subject: subject,
			Day:     nextDay.Format("2006-01-02"),
			Text:    pick(lang, "Start next week on "+subject+"?", "下周从"+subject+"开始？"),
			Message: pick(lang, "Help me plan how to start "+weekday+" on "+subject+". Don't write to my calendar.",
				"帮我规划"+weekday+"如何从"+subject+"开始，不要写入日历。"),
		}
	}
	for _, candidate := range in.Open {
		if candidate.Section != mobileBriefSectionCommitments && candidate.Section != mobileBriefSectionWorkingToward {
			continue
		}
		title := strings.TrimSpace(mobileBriefCardTitle(candidate.Item))
		key := mobileBriefCardItemKey(candidate.Item)
		if title == "" || key == "" || len(candidate.Item.Refs) == 0 || !yourWeekCopyAllowed(title) {
			continue
		}
		out := ask(title)
		out.CardID, out.Refs = "week:ask:"+key, append([]string{}, candidate.Item.Refs...)
		return out
	}
	for _, row := range week.Time {
		if !row.Other && row.TopicID != "" {
			out := ask(row.Name)
			out.TopicID = row.TopicID
			return out
		}
	}
	return nil
}

// yourWeekLead is the one headline sentence. A quiet week says so, with the
// days seen only when there was at least one.
func yourWeekLead(in yourWeekInput, week *MobileBriefWeek, quiet bool, lang string) string {
	if quiet {
		if in.SeenDays > 0 {
			return pick(lang, fmt.Sprintf("A quiet week. I saw you on this computer %d of %d days.", in.SeenDays, in.WorkDays),
				fmt.Sprintf("这周比较安静，我只在 %d 天里的 %d 天看到你用这台电脑。", in.WorkDays, in.SeenDays))
		}
		return pick(lang, "A quiet week.", "这周比较安静。")
	}
	var topics []string
	for _, row := range week.Time {
		if !row.Other {
			topics = append(topics, row.Name)
		}
	}
	lead := pick(lang, "Here's your week: what got done, and what's still open.", "这是你的一周：做完了什么，还有什么没收尾。")
	switch {
	case len(topics) >= 2:
		lead = pick(lang, "A "+topics[0]+" week: most of your time went there, then to "+topics[1]+".", "这周的重心是 "+topics[0]+"，其次是 "+topics[1]+"。")
	case len(topics) == 1:
		lead = pick(lang, "A "+topics[0]+" week: most of your time went there.", "这周的重心是 "+topics[0]+"。")
	}
	if !yourWeekCopyAllowed(lead) {
		return pick(lang, "Here's your week: what got done, and what's still open.", "这是你的一周：做完了什么，还有什么没收尾。")
	}
	return lead
}

// yourWeekParagraphs is the same week as sentences, for readers that show
// paragraphs (older phones) and for the recording.
func yourWeekParagraphs(week *MobileBriefWeek, lang string) []yourWeekParagraph {
	if week == nil {
		return nil
	}
	join := func(parts []string) string { return strings.Join(parts, pick(lang, "; ", "；")) }
	out := []yourWeekParagraph{{Role: yourWeekParagraphLead, Text: week.Headline}}
	if len(week.Time) > 0 {
		var parts []string
		for _, row := range week.Time {
			name := row.Name
			if row.Other {
				name = pick(lang, "other work", "其他")
			}
			parts = append(parts, pick(lang, fmt.Sprintf("%s %d %s", name, row.Hours, yourWeekHourWord(row.Hours)), fmt.Sprintf("%s %d 小时", name, row.Hours)))
		}
		out = append(out, yourWeekParagraph{Role: yourWeekParagraphTime, Text: pick(lang, "Where your time went: ", "时间花在哪了：") + join(parts) + pick(lang, ".", "。")})
	}
	if len(week.Done) > 0 {
		var parts []string
		for _, item := range week.Done {
			if item.Kind == yourWeekDoneMeetings {
				parts = append(parts, yourWeekMeetingsText(item.Count, lang))
				continue
			}
			parts = append(parts, item.Title)
		}
		out = append(out, yourWeekParagraph{Role: yourWeekParagraphDone, Text: pick(lang, "Done for you: ", "替你做完的：") + join(parts) + pick(lang, ".", "。")})
	}
	if len(week.Open) > 0 {
		var parts []string
		for _, item := range week.Open {
			parts = append(parts, item.Title)
		}
		out = append(out, yourWeekParagraph{Role: yourWeekParagraphOpen, Text: pick(lang, "Still open: ", "还没收尾的：") + join(parts) + pick(lang, ".", "。")})
	}
	for _, learned := range week.Learned {
		out = append(out, yourWeekParagraph{Role: yourWeekParagraphLearned, Text: learned.Text})
	}
	if week.Ask != nil {
		out = append(out, yourWeekParagraph{Role: yourWeekParagraphAsk, Text: week.Ask.Text})
	}
	return out
}

func yourWeekWeekdayName(day time.Weekday, lang string) string {
	en := [...]string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}
	zh := [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	if lang == "zh" {
		return zh[day]
	}
	return en[day]
}

func yourWeekHourWord(hours int) string {
	if hours == 1 {
		return "hour"
	}
	return "hours"
}

func yourWeekMeetingsText(count int, lang string) string {
	if count == 1 {
		return pick(lang, "notes from a call", "1 个通话的笔记")
	}
	return pick(lang, fmt.Sprintf("notes from %d calls", count), fmt.Sprintf("%d 个通话的笔记", count))
}

// The words the old recap used for the system's own bookkeeping. None of
// them, and no Isoform-internal name, may appear in "Your week".
var yourWeekNeverShown = []string{
	"purpose map", "possible reason", "receipt", "delivery confirmed", "delivered", "opens recorded",
	"completion rate", "pacing", "calibration", "reasons to speak up",
	"回执", "送达", "打开记录", "提醒机会", "待发送", "提醒节奏", "可能需要提醒",
}

var yourWeekZero = regexp.MustCompile(`(^|[^0-9.,:/])0+($|[^0-9.,:/])`)

// yourWeekCopyAllowed is the "Never shown" guard: no system words, internal
// names or zeros.
func yourWeekCopyAllowed(text string) bool {
	lower := strings.ToLower(text)
	for _, word := range yourWeekNeverShown {
		if strings.Contains(lower, word) {
			return false
		}
	}
	return !yourWeekZero.MatchString(text) && mobileBriefCopySafe(text)
}

// yourWeekHygiene drops any row whose words carry a privacy mask, as the
// brief's own lists do.
func yourWeekHygiene(week *MobileBriefWeek) *MobileBriefWeek {
	if week == nil {
		return nil
	}
	out := *week
	if !mobileBriefCopySafe(out.Headline) {
		return nil
	}
	out.Time, out.Done, out.Open, out.Learned = nil, nil, nil, nil
	for _, row := range week.Time {
		if row.Other || mobileBriefCopySafe(row.Name) {
			out.Time = append(out.Time, row)
		}
	}
	for _, item := range week.Done {
		if mobileBriefCopySafe(item.Title) {
			out.Done = append(out.Done, item)
		}
	}
	for _, item := range week.Open {
		if !mobileBriefCopySafe(item.Title) {
			continue
		}
		if !mobileBriefCopySafe(item.Label) {
			item.Label, item.CardID = "", ""
		}
		out.Open = append(out.Open, item)
	}
	for _, item := range week.Learned {
		if mobileBriefCopySafe(item.Text) {
			out.Learned = append(out.Learned, item)
		}
	}
	if out.Ask != nil && (!mobileBriefCopySafe(out.Ask.Text) || !mobileBriefCopySafe(out.Ask.Message)) {
		out.Ask = nil
	}
	return &out
}

// yourWeekMuteLabels are the notification kinds' names as Settings shows them
// (settings.json proactiveNotifications.kinds.*.label; a test keeps them in
// step), for the learned line a mute becomes.
var yourWeekMuteLabels = map[string][2]string{
	"decision_due":        {"Permission requests", "权限请求"},
	"work_blocked":        {"Needs you", "需要你"},
	"work_finished":       {"Work finished", "工作完成"},
	"answer_ready":        {"Answers you asked for", "你问过的事"},
	"commitment_slipping": {"Commitments slipping", "承诺进度滞后"},
	"capture_paused":      {"Observing paused", "观察已暂停"},
	"meeting_ended":       {"Meeting notes ready", "会议纪要就绪"},
	"brief_ready":         {"Brief ready", "简报就绪"},
	"person_reappeared":   {"People who came back", "再次出现的人"},
	"goal_check_in":       {"Goal check-ins", "目标回顾"},
	"learned_to_confirm":  {"Things Isoform learned", "Isoform 学到的事"},
	"meeting_prep":        {"Meeting coming up", "会议即将开始"},
	"calendar_conflict":   {"Calendar clashes", "日历冲突"},
	"waiting_on_reply":    {"Waiting on your reply", "等你回复"},
	"meeting_followup":    {"Meeting follow-ups", "会议待跟进"},
	"brief_missed":        {"Missed brief", "错过的简报"},
	"schedule_missed":     {"Missed schedule", "错过的定时任务"},
	"suggestion":          {"Suggestions", "建议"},
	"playbook_result":     {"Playbook results", "操作手册结果"},
	"reminder":            {"Reminders you set", "你设置的提醒"},
	"playbook_suggested":  {"Routines to take on", "可接手的日常事务"},
	"topic_finds":         {"New on your topics", "你关注话题的新内容"},
	"first_week":          {"First week", "第一周"},
}

func yourWeekMuteText(kind, lang string) string {
	label, ok := yourWeekMuteLabels[kind]
	if !ok {
		return ""
	}
	return pick(lang, "You muted “"+label[0]+"”, so I'll stay quiet about them.", "你静音了“"+label[1]+"”，我之后不再提。")
}
