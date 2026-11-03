package proactivevoice

const Contract = "isoform.proactive.job/1"

// Display is a localized fact computed by the caller, never by the model.
type Display struct {
	Display string `json:"display"`
}

type User struct {
	Name *string `json:"name"`
}

type Person struct {
	Ref       string  `json:"ref"`
	Name      *string `json:"name"`
	RoleLabel string  `json:"role_label"`
}

type Limit struct {
	Unit string `json:"unit"`
	Max  int    `json:"max"`
}

type Limits struct {
	Headline Limit  `json:"headline"`
	Body     Limit  `json:"body"`
	Draft    *Limit `json:"draft,omitempty"`
}

// Item is the closed union of evidence fields in the reviewed job schema.
// Pointer fields distinguish withheld data from an empty value.
type Item struct {
	Ref            string     `json:"ref"`
	Type           string     `json:"type"`
	FromName       *string    `json:"from_name,omitempty"`
	WaitingSince   *Display   `json:"waiting_since,omitempty"`
	Subject        *string    `json:"subject,omitempty"`
	BodyExcerpt    *string    `json:"body_excerpt,omitempty"`
	Title          *string    `json:"title,omitempty"`
	Start          *Display   `json:"start,omitempty"`
	StartsIn       *Display   `json:"starts_in,omitempty"`
	Place          *string    `json:"place,omitempty"`
	PeopleRefs     []string   `json:"people_refs,omitempty"`
	AttendeeTotal  *Display   `json:"attendee_total,omitempty"`
	Due            *Display   `json:"due,omitempty"`
	Progress       *string    `json:"progress,omitempty"`
	NextStep       *string    `json:"next_step,omitempty"`
	FollowupsCount *Display   `json:"followups_count,omitempty"`
	Followups      []Followup `json:"followups,omitempty"`
	Owner          string     `json:"owner,omitempty"`
	With           string     `json:"with,omitempty"`
	LastTouch      *Display   `json:"last_touch,omitempty"`
	Gap            *Display   `json:"gap,omitempty"`
	Slot           *Display   `json:"slot,omitempty"`
	TopItems       []TopItem  `json:"top_items,omitempty"`
	What           *Display   `json:"what,omitempty"`
	OffFor         *Display   `json:"off_for,omitempty"`
	Label          string     `json:"label,omitempty"`
	Redacted       bool       `json:"redacted,omitempty"`
	Text           string     `json:"text,omitempty"`
}

type Followup struct {
	Ref   string `json:"ref"`
	Text  string `json:"text"`
	Owner string `json:"owner"`
}

type TopItem struct {
	Ref  string `json:"ref"`
	Text string `json:"text"`
}

type PromisedDay struct {
	Display string `json:"display"`
	Status  string `json:"status"`
}

type UserWords struct {
	Ref         string       `json:"ref"`
	Source      string       `json:"source"`
	Attribution string       `json:"attribution"`
	When        Display      `json:"when"`
	Text        string       `json:"text"`
	About       []string     `json:"about"`
	PromisedDay *PromisedDay `json:"promised_day,omitempty"`
}

type WritingSample struct {
	Ref  string `json:"ref"`
	Text string `json:"text"`
}

type CommunicationBelief struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type Job struct {
	Contract             string                `json:"contract"`
	Kind                 string                `json:"kind"`
	MomentID             string                `json:"moment_id"`
	Locale               string                `json:"locale"`
	OutputLanguage       string                `json:"output_language"`
	DraftLanguage        *string               `json:"draft_language"`
	Surface              string                `json:"surface"`
	NamesAllowed         bool                  `json:"names_allowed"`
	MaxNames             int                   `json:"max_names"`
	Now                  Display               `json:"now"`
	User                 User                  `json:"user"`
	DraftAllowed         bool                  `json:"draft_allowed"`
	DraftTo              string                `json:"draft_to,omitempty"`
	DraftSubject         string                `json:"draft_subject,omitempty"`
	ClosingLine          string                `json:"closing_line,omitempty"`
	HeadlineSubject      *string               `json:"headline_subject"`
	MailFlag             string                `json:"mail_flag"`
	Limits               Limits                `json:"limits"`
	Coverage             map[string]string     `json:"coverage"`
	Actions              []string              `json:"actions"`
	KindGuidance         string                `json:"kind_guidance"`
	People               []Person              `json:"people"`
	Items                []Item                `json:"items"`
	UserWords            []UserWords           `json:"user_words"`
	WritingSamples       []WritingSample       `json:"writing_samples,omitempty"`
	CommunicationBeliefs []CommunicationBelief `json:"communication_beliefs,omitempty"`
}

type Draft struct {
	To      string `json:"to"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
}

type Output struct {
	Headline string   `json:"headline"`
	Body     string   `json:"body"`
	Draft    *Draft   `json:"draft"`
	Actions  []string `json:"actions"`
	Evidence []string `json:"evidence"`
	Abstain  bool     `json:"abstain"`
	Reason   *string  `json:"reason"`
}

// Text is useful for nullable display text in an assembled job.
func Text(value string) *string { return &value }

// Supported keeps silent and unimplemented kinds off the model route.
func Supported(kind string) bool {
	switch kind {
	case "waiting_on_reply", "commitment_slipping", "meeting_prep", "brief_ready", "capture_paused":
		return true
	default:
		return false
	}
}

// Actions is the code-owned table. A fresh slice prevents job mutation from
// changing the validator's authority table.
func Actions(kind string, draft bool) []string {
	switch kind {
	case "waiting_on_reply":
		if draft {
			return []string{"copy_draft", "open", "dismiss"}
		}
		return []string{"open", "dismiss"}
	case "meeting_followup":
		return []string{"open_notes", "later"}
	case "quiet_thread", "commitment_slipping", "brief_ready":
		return []string{"open"}
	case "meeting_prep":
		return []string{"open", "dismiss"}
	case "capture_paused":
		return []string{"open_settings"}
	default:
		return nil
	}
}

func DefaultLimits(language string, draft bool) Limits {
	limits := Limits{Headline: Limit{"characters", 90}, Body: Limit{"words", 60}}
	draftLimit := Limit{"words", 120}
	if language == "zh-CN" {
		limits = Limits{Headline: Limit{"characters", 40}, Body: Limit{"characters", 120}}
		draftLimit = Limit{"characters", 240}
	}
	if draft {
		limits.Draft = &draftLimit
	}
	return limits
}

func ClosingLine(language string) string {
	if language == "zh-CN" {
		return "未经你的确认，不会发出任何内容。"
	}
	return "Nothing goes out without your word."
}

func CautionLine(language string) string {
	if language == "zh-CN" {
		return "这封邮件在要求某类信息或付款，请你先亲自阅读再行动。"
	}
	return "This mail asks for something of that kind. Read it yourself before you act."
}
