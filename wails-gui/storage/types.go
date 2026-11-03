package storage

// ProjectRow represents a project as stored in the database.
type ProjectRow struct {
	ID                string
	Name              string
	Path              string
	Organization      string
	Product           string
	IsYansuProject    bool
	ActiveSessionID   string
	ActiveWorkspaceID string
	SortOrder         int
	SetupCommand      string
	Hidden            bool
	CreatedAt         int64
	UpdatedAt         int64
}

// SessionRow represents a session as stored in the database.
type SessionRow struct {
	LocalID          string
	ProjectID        string
	WorkspaceID      string
	SessionID        string
	Label            string
	Model            string
	PrdEnabled       bool
	ThinkingLevel    string
	PlanMode         bool
	ThinkingEnabled  bool
	Pinned           bool
	PinnedAt         int64
	AutomationResult bool
	Hidden           bool
	// MeetingID groups every chat session produced by a single meeting
	// (transcript slices + handoffs). Empty for regular chats. Sidebar
	// Recent collapses sessions that share a non-empty MeetingID under one
	// parent row.
	MeetingID string
	// Kind is the session's place in the chat model: SessionKindMain for the
	// one Isoform-owned main chat, SessionKindSide for a side chat split off
	// from it, and "" for every other session. Legacy kinds (transcripts,
	// handoffs, scheduled runs) stay inferred from the label prefix,
	// MeetingID and AutomationResult.
	Kind string
	// ParentID is the local id of the session a side chat was split off from
	// (the main chat). Empty for every other kind.
	ParentID  string
	CreatedAt int64
	UpdatedAt int64
}

const (
	// SessionKindMain marks the one main chat the user keeps talking in.
	SessionKindMain = "main"
	// SessionKindSide marks a side chat split off from the main chat.
	SessionKindSide = "side"
)

// WorkspaceRow represents a workspace as stored in the database.
type WorkspaceRow struct {
	ID              string
	ProjectID       string
	Label           string
	IsDefault       bool
	WorktreePath    string
	WorktreeBranch  string
	BaseBranch      string
	ActiveSessionID string
	SortOrder       int
	Hidden          bool
	CreatedAt       int64
	UpdatedAt       int64
}

// StreamItemRow represents a stream item as stored in the database.
type StreamItemRow struct {
	// TopicID is write-only admission metadata, persisted in stream_item_topics.
	TopicID        string
	ID             string
	SessionLocalID string
	Kind           string
	Content        string
	ToolName       string
	ToolInput      string // JSON string
	ToolUseID      string // Anthropic-assigned tool_use_id (e.g. "toolu_...") for pairing tool_use/tool_result
	RunID          string
	SessionID      string
	SortOrder      int
	CreatedAt      int64
	// Images is a JSON-encoded array of {mediaType, dataBase64} attachments
	// the user pasted / uploaded with this message. Stored as a string so
	// the schema stays flat; empty string means no attachments. Populated
	// only for user-kind items today, but the column is generic.
	Images string
	// AutomationContext is a JSON blob ({output, jobId}) attached to the first
	// user turn of a scheduled-automation "continue in chat" session. It drives
	// the collapsible "Run output" quote card + deliverable chips on reload.
	// Phone Ideas admissions also retain {offerReferences} here, without an
	// output or jobId; automation renderers ignore those source references.
	// Topic turns and their response rows retain {topicContext} source IDs and
	// digests for dispatch/history read gates, without contextual prose.
	// Empty string means none; flat-string storage mirrors Images.
	AutomationContext string
	// Files is a JSON-encoded array of {fileName, mediaType, fileSize, category}
	// for non-image file attachments the user uploaded with this message.
	// Empty string means no file attachments. Mirrors the Images pattern.
	Files string
}

// StreamItemPage is a cursor-paginated result set of stream items.
type StreamItemPage struct {
	Items   []StreamItemRow
	HasMore bool
}

// GitSnapshotRow represents a git worktree snapshot taken before an AI turn.
// The snapshot is a full orphan commit (created via git write-tree + commit-tree)
// that captures the complete worktree state, including untracked files.
type GitSnapshotRow struct {
	ID             int64
	SessionLocalID string
	SortOrder      int
	StashSHA       string // checkpoint commit SHA (full worktree tree commit)
	HeadSHA        string // HEAD SHA at snapshot time (for diagnostics + HEAD restore)
	UntrackedFiles string // newline-separated list of untracked file paths at snapshot time
	WorkDir        string
	CreatedAt      int64
}

// CheckpointFileRow represents a backed-up file associated with a conversation turn.
type CheckpointFileRow struct {
	ID             int64
	SessionLocalID string
	SortOrder      int
	FilePath       string
	BackupPath     string
	Existed        bool
	CreatedAt      int64
}

// SidebarGroupRow represents a custom sidebar group as stored in the database.
type SidebarGroupRow struct {
	ID        string
	Name      string
	SortOrder int
	Collapsed bool
	CreatedAt int64
	UpdatedAt int64
}

// SidebarGroupItemRow represents an item reference within a sidebar group.
type SidebarGroupItemRow struct {
	ID        string
	GroupID   string
	ItemType  string // "chat" or "automation"
	RefID     string
	SortOrder int
	AddedAt   int64
}

// CronJobRow represents a cron job as stored in the database.
type CronJobRow struct {
	ID                string
	Name              string
	Description       string
	Enabled           bool
	ScheduleKind      string // "at", "every", "cron", "manual"
	ScheduleConfig    string // JSON
	ScheduleEvidence  string // verbatim quote justifying LLM-inferred schedule; cleared on user edit
	TargetKind        string // "agent"
	TargetConfig      string // JSON
	DeleteAfterRun    bool
	RetryPolicy       string // JSON
	NextRunAt         int64
	LastRunAt         int64
	LastRunStatus     string // "ok", "error", "skipped"
	LastError         string
	ConsecutiveErrors int
	RetryAttempt      int
	RetryRunAt        int64
	SourceSessionID   string // chat session that created this job
	SourcePlatform    string // "slack", "feishu", "chat" ("" for legacy/unspecified GUI chat)
	Fingerprint       string // sha256 dedup key; only persisted for auto-created automations
	NotifyDM          bool   // whether to send integration DM on completion (default true)
	NotifyPlatform    string // preferred DM platform: "slack","discord","telegram","feishu","wechat","none","" (empty = legacy auto-pick)
	SurfaceKind       string // sidebar routing: "" or "scheduled" (legacy default), "handoff" (one-shot)
	SortOrder         int
	CreatedAt         int64
	UpdatedAt         int64
}

// CronRunRow represents a single execution record for a cron job.
type CronRunRow struct {
	ID                string
	JobID             string
	StartedAt         int64
	FinishedAt        int64
	Status            string // "running", "ok", "error", "blocked", "skipped", "missed"
	TriggerKind       string // "scheduled", "manual", "catchup", "retry"
	Error             string
	DurationMs        int64
	ResultSummary     string
	SessionTag        string // session local_id holding stream items for this run
	ItemSortStart     int    // first stream_item sort_order in this run (-1 = unknown)
	NotifyResult      string // JSON-encoded DM notification outcome (empty = not yet notified)
	CandidateBackends string // JSON array of the ordered route at run start
	Backend           string // agent loop that last attempted the run
	FallbackFrom      string // JSON array of failed or unavailable assistants
	BackendReason     string // why an earlier assistant could not finish the run
	Model             string // actual model reported by the chosen backend
	ActionsStarted    int    // durable tool ledger entries; never inferred from model text
}

// ComposerSuggestionsRow represents a generated batch of composer suggestion chips.
type ComposerSuggestionsRow struct {
	ID          string
	GeneratedAt int64  // unix ms
	Suggestions string // JSON array of strings
	SourceHash  string // hash of suggestion inputs used (dedup / staleness check)
	ExpiresAt   int64  // unix ms; used by cleanup pass
}

// ChatDeliverableRow represents files an interactive chat agent declared via
// its <deliverables> block, persisted so they surface in the unified
// Deliverables hub. WorkDir/SessionTempDir are captured at record time so the
// file stays openable (path validation) across app restarts.
type ChatDeliverableRow struct {
	ID             string
	SessionID      string // chat session local_id (tag)
	Title          string
	Summary        string
	Files          string // JSON array of absolute paths
	WorkDir        string
	SessionTempDir string
	CreatedAt      int64 // unix ms
	UpdatedAt      int64 // unix ms
}
