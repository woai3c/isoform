package daemon

// Event is a platform-agnostic incoming message from any supported chat platform.
type Event struct {
	UserID    string
	ChannelID string
	ThreadTS  string // thread continuation identifier; empty for top-level messages
	Text      string
	EventTS   string
	TeamID    string
	Platform  string      // "slack"
	EventType string      // "message" | "app_mention"; empty for older backends
	ChatType  string      // "p2p" for DMs, "group" for channels; empty for older backends
	RelayID   string      // backend-generated UUID for relay correlation; empty in direct mode
	Files     []EventFile // attached files (images, documents, etc.)
}

// IsPassiveChannelEvent reports whether the event is a passively-monitored
// Slack channel message: a non-DM message that does NOT @mention the bound
// user. The backend fans these out to every daemon in the workspace so that
// optional local pipelines (the desktop Extractor) can evaluate them. The CLI
// daemon has no such pipeline, and it must NEVER spawn an agent session for
// them — only DMs and explicit @mentions are commands addressed to the agent.
// Empty EventType (older backends that only relayed interactive events) is
// treated as interactive so behaviour is unchanged there.
func IsPassiveChannelEvent(event Event) bool {
	return event.Platform == "slack" && event.EventType == "message" && event.ChatType != "p2p"
}

// EventFile represents an attached file in a message.
type EventFile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mimetype"`
	URL      string `json:"url_private"`         // private download URL
	Size     int    `json:"size"`                // file size in bytes
	Thumb    string `json:"thumb_360,omitempty"` // thumbnail URL for images
	Content  []byte `json:"-"`                   // downloaded file content (not serialized)
}
