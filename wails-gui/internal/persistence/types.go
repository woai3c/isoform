package persistence

// ProjectData is the full project state exchanged with the frontend.
type ProjectData struct {
	ID                string          `json:"id"`
	Name              string          `json:"name"`
	Path              string          `json:"path"`
	Organization      string          `json:"organization,omitempty"`
	Product           string          `json:"product,omitempty"`
	IsYansuProject    bool            `json:"isYansuProject"`
	ActiveSessionID   string          `json:"activeSessionId"`
	ActiveWorkspaceID string          `json:"activeWorkspaceId"`
	SortOrder         int             `json:"sortOrder"`
	Workspaces        []WorkspaceData `json:"workspaces"`
}

// WorkspaceData is the full workspace state exchanged with the frontend.
type WorkspaceData struct {
	ID              string        `json:"id"`
	Label           string        `json:"label"`
	IsDefault       bool          `json:"isDefault"`
	WorktreePath    string        `json:"worktreePath,omitempty"`
	WorktreeBranch  string        `json:"worktreeBranch,omitempty"`
	BaseBranch      string        `json:"baseBranch,omitempty"`
	ActiveSessionID string        `json:"activeSessionId"`
	SortOrder       int           `json:"sortOrder"`
	Sessions        []SessionData `json:"sessions"`
	CreatedAt       int64         `json:"createdAt"`
	UpdatedAt       int64         `json:"updatedAt"`
}

// SessionData is the full session state exchanged with the frontend.
type SessionData struct {
	LocalID          string `json:"localId"`
	WorkspaceID      string `json:"workspaceId"`
	SessionID        string `json:"sessionId"`
	Label            string `json:"label"`
	Model            string `json:"model"`
	CreatedAt        int64  `json:"createdAt"`
	PrdEnabled       bool   `json:"prdEnabled"`
	ThinkingLevel    string `json:"thinkingLevel"`
	PlanMode         bool   `json:"planMode"`
	ThinkingEnabled  bool   `json:"thinkingEnabled"`
	Pinned           bool   `json:"pinned"`
	PinnedAt         int64  `json:"pinnedAt"`
	AutomationResult bool   `json:"automationResult"`
	// Kind is "main" for the main chat, "side" for a side chat, "" otherwise.
	Kind string `json:"kind"`
	// ParentID is the main chat a side chat was split off from.
	ParentID        string           `json:"parentId"`
	UpdatedAt       int64            `json:"updatedAt"`
	StreamItemCount int              `json:"streamItemCount"`
	Items           []StreamItemData `json:"items"`
}

// ForkSessionResult identifies the owner and metadata of a newly forked chat.
// Stream items remain lazy-loaded through LoadStreamItemsPage.
type ForkSessionResult struct {
	ProjectID string      `json:"projectId"`
	Session   SessionData `json:"session"`
}

// StreamItemData is a single conversation item exchanged with the frontend.
type OpenAnswerEvent struct {
	Who   string `json:"who,omitempty"`
	Label string `json:"label"`
}

type OpenAnswerData struct {
	ID           string           `json:"id"`
	SessionID    string           `json:"sessionId"`
	MessageID    string           `json:"messageId"`
	Question     string           `json:"question"`
	Topic        string           `json:"topic,omitempty"`
	Kind         string           `json:"kind"`
	Settles      string           `json:"settles"`
	Source       string           `json:"source,omitempty"`
	WindowStart  string           `json:"windowStart,omitempty"`
	WindowEnd    string           `json:"windowEnd,omitempty"`
	WatchedEvent *OpenAnswerEvent `json:"watchedEvent,omitempty"`
	Delivery     string           `json:"delivery"`
	ExpiresAt    string           `json:"expiresAt"`
	Posts        int              `json:"posts"`
	State        string           `json:"state"`
	SawAt        string           `json:"sawAt,omitempty"`
	CreatedAt    string           `json:"createdAt"`
	Learned      bool             `json:"learned,omitempty"`
}

type StreamItemData struct {
	// Read-time association for legacy week answers; never persisted as origin.
	WeekAnswerBriefID string          `json:"weekAnswerBriefId,omitempty"`
	OpenAnswer        *OpenAnswerData `json:"openAnswer,omitempty"`
	CardType          string          `json:"cardType,omitempty"`
	CreatedAt         int64           `json:"createdAt,omitempty"`
	RowClass          string          `json:"rowClass,omitempty"`
	TopicIDs          []string        `json:"topicIds,omitempty"`
	LocalDay          string          `json:"localDay,omitempty"`

	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Content   string `json:"content,omitempty"`
	ToolName  string `json:"toolName,omitempty"`
	ToolInput string `json:"toolInput,omitempty"` // JSON string
	ToolUseID string `json:"toolUseId,omitempty"` // Anthropic-assigned tool_use_id for pairing tool_use/tool_result
	RunID     string `json:"runId,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	SortOrder int    `json:"sortOrder"`
	// Images is a JSON-encoded array of {mediaType, dataBase64} attachments
	// the user uploaded with this message. Kept as a string across the
	// boundary so the frontend and storage layer agree on one serialization
	// format; the frontend parses it into typed objects for rendering. Empty
	// string means no attachments.
	Images string `json:"images,omitempty"`
	// AutomationContext is a JSON blob ({output, jobId}) attached to the first
	// user turn of a scheduled-automation "continue in chat" session, so the
	// run-output quote card + deliverable chips survive a session reload.
	// Empty string means none; the frontend parses it into typed fields.
	AutomationContext string `json:"automationContext,omitempty"`
	// Files is a JSON-encoded array of {fileName, mediaType, fileSize, category}
	// for non-image file attachments the user uploaded with this message.
	// Empty string means no file attachments.
	Files string `json:"files,omitempty"`
}

// StreamItemPageData is a cursor-paginated result set of stream items.
type StreamItemPageData struct {
	Items      []StreamItemData `json:"items"`
	HasMore    bool             `json:"hasMore"`
	NextCursor int              `json:"nextCursor"` // sort_order of the earliest item; use as cursor for the next page
}

// SidebarGroupData is a custom sidebar group exchanged with the frontend.
type SidebarGroupData struct {
	ID        string                 `json:"id"`
	Name      string                 `json:"name"`
	SortOrder int                    `json:"sortOrder"`
	Collapsed bool                   `json:"collapsed"`
	Items     []SidebarGroupItemData `json:"items"`
	CreatedAt int64                  `json:"createdAt"`
	UpdatedAt int64                  `json:"updatedAt"`
}

// SidebarGroupItemData is an item reference within a sidebar group.
type SidebarGroupItemData struct {
	ID        string `json:"id"`
	GroupID   string `json:"groupId"`
	ItemType  string `json:"itemType"` // "chat"
	RefID     string `json:"refId"`
	SortOrder int    `json:"sortOrder"`
	AddedAt   int64  `json:"addedAt"`
}
