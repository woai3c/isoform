package storage

// PrincipalKind enumerates the kinds of identities the activity pipeline
// distinguishes between. The values are persisted verbatim, so adding a new
// kind requires a migration if downstream queries discriminate on it.
const (
	PrincipalKindSelf           = "self"
	PrincipalKindContact        = "contact"
	PrincipalKindContentSubject = "content_subject"
	PrincipalKindAnonymous      = "anonymous"
)

// PrincipalRow represents a single identity entity persisted in the
// `principals` table. Aliases live in a separate one-to-many table to keep
// the row scannable; the Aliases slice is populated by Get/List helpers.
type PrincipalRow struct {
	ID           string // ulid-shaped opaque id
	Kind         string // see PrincipalKind* constants
	DisplayName  string
	Relationship string // family | friend | colleague | partner | acquaintance | "" (only meaningful for contact)
	Notes        string
	CreatedFrom  string   // onboarding | manual | auto_from_chat | auto_from_voice | system_contacts | vcard | ""
	Source       string   // system_contacts | vcard | ""; provenance of the principal, not merged handles
	ExternalRef  string   // source contact identifier; empty for principals created outside an import
	Confidence   float64  // 0..1
	Aliases      []string // populated by reads; ignored on writes (use Set/AddAlias)
	CreatedAt    int64
	UpdatedAt    int64
}

// AppHandleRow binds a principal to a per-app account identifier so chat
// senders captured in parsed content can be resolved back to a known person.
type AppHandleRow struct {
	ID            int64
	PrincipalID   string
	App           string // canonical app id: "wechat" | "telegram" | "slack" | "discord" | "feishu" | "imessage" | "email" | etc.
	HandleID      string // stable per-app id when available (e.g. telegram user_id, slack U0123); empty when only display handle is known
	DisplayHandle string // human-visible handle ("@yetone", "yetone.li", "John Doe"); always present
	CreatedAt     int64
}

// PrincipalSightingRow records cumulative per-app counters for a principal.
// One row per (principal, app) tuple; BumpPrincipalSighting maintains it
// via UPSERT.
type PrincipalSightingRow struct {
	PrincipalID  string
	App          string
	MessageCount int
	FirstSeenAt  int64
	LastSeenAt   int64
}

// PrincipalRecentMessageRow holds a short, redacted preview of a recently
// captured message. The Who you know page uses these for the "recent messages"
// drawer; the rolling window is enforced by TrimPrincipalRecentMessages
// after each insert so the table stays bounded.
type PrincipalRecentMessageRow struct {
	PrincipalID string
	App         string
	SeenAt      int64
	Preview     string
}

// PrincipalStatsRow is the aggregate view consumed by the Who you know page.
// Populated by GetPrincipalStats which joins the principal row with its
// sightings + recent-messages tables.
type PrincipalStatsRow struct {
	PrincipalID    string
	TotalMessages  int
	FirstSeenAt    int64
	LastSeenAt     int64
	ByApp          []PrincipalSightingRow
	RecentMessages []PrincipalRecentMessageRow
}

// PrincipalResearchStatus enumerates the lifecycle states of a person's
// research dossier. Persisted verbatim in principal_research.status.
const (
	PrincipalResearchStatusIdle        = "idle"
	PrincipalResearchStatusResearching = "researching"
	PrincipalResearchStatusReady       = "ready"
	PrincipalResearchStatusFailed      = "failed"
)

// PrincipalResearchRow holds the AI-generated research dossier for a
// principal — a structured, web-sourced profile (Contact / Who They Are /
// Your Relationship) rendered as Markdown on the Who you know page. One row per
// principal; the dossier is regenerated in place rather than versioned.
// Sources is stored as a JSON array in the DB and decoded on read.
type PrincipalResearchRow struct {
	PrincipalID  string
	Status       string           // see PrincipalResearchStatus* constants
	Summary      string           // one-line description; feeds the list subtitle
	Markdown     string           // dossier body with inline [n] citations
	Sources      []ResearchSource // decoded from the sources JSON column
	Model        string           // model that produced the dossier
	Error        string           // last failure reason when Status == failed
	ResearchedAt int64            // unix millis of the last successful run ("Research updated at")
	CreatedAt    int64
	UpdatedAt    int64
}

// ResearchSource is a single citation backing a dossier. The N matches the
// inline [n] markers in PrincipalResearchRow.Markdown.
type ResearchSource struct {
	N     int    `json:"n"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

// PrincipalSessionSightingRow records that a principal appeared at least
// once in a given activity session. Populated as a side-effect of the
// sender labeler resolving a chat message — see BumpPrincipalSighting.
// Used by the Who you know page to drill from a principal back to the sessions
// they appeared in.
type PrincipalSessionSightingRow struct {
	PrincipalID string
	SessionID   string
	FirstSeenAt int64
	LastSeenAt  int64
}

// SessionPrincipalRow is the inverse view: "who showed up in this
// session". Joined from principal_session_sightings + principals so
// callers don't need a second round-trip to fetch display_name / kind.
// Used by the Activity page to render the "People in this session"
// section.
type SessionPrincipalRow struct {
	PrincipalID string
	DisplayName string
	Kind        string
	FirstSeenAt int64
	LastSeenAt  int64
}
