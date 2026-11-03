package appparser

// ParsedContent represents structured content extracted from an app's
// accessibility tree by an app-specific parser.
type ParsedContent struct {
	// ContentType classifies the content: "conversation", "document",
	// "terminal", "task_list", "web_page", or empty for unparsed.
	ContentType string `json:"contentType,omitempty"`

	// Title is the document/conversation title if available.
	Title string `json:"title,omitempty"`

	// Body is the flat-text rendering of the parsed content, suitable for
	// direct inclusion in a prompt or stored alongside a snapshot.
	Body string `json:"body,omitempty"`

	// URL is the active URL for browser-based content.
	URL string `json:"url,omitempty"`

	// Channel names the conversation target for chat apps (e.g., "#general",
	// "Alice, Bob", "Claude"). Empty for non-chat content.
	Channel string `json:"channel,omitempty"`

	// Messages holds structured conversation data when ContentType is
	// "conversation". Each entry has a sender, content, optional timestamp.
	Messages []ConversationMessage `json:"messages,omitempty"`

	// Metadata holds parser-specific key-value pairs.
	Metadata map[string]string `json:"metadata,omitempty"`

	// DedupOfHash is set when the activity monitor elided this
	// snapshot's payload because the previous snapshot from the same
	// bundle had identical structured content. The hash points at the
	// sibling snapshot in the same session burst whose
	// metadata.contentHash matches — consumers should resolve that
	// payload (or fall back to AXText) instead of treating the parsed
	// content as empty. See activity_monitor.go's dedup path.
	DedupOfHash string `json:"dedupOfHash,omitempty"`
}

// ConversationMessage is a single structured message extracted from a chat app.
type ConversationMessage struct {
	Sender    string `json:"sender,omitempty"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp,omitempty"`
	// ThreadParent links this message to the top-level message it replies
	// to, when known. Format: "<parentSender>@<parentTimestamp>" — same
	// shape downstream code uses elsewhere for parent-id linkage.
	// Empty for top-level (non-thread) messages and for the thread parent
	// itself. Currently populated only by the slack parser; other chat
	// parsers that don't expose a thread sidebar leave this empty.
	ThreadParent string `json:"threadParent,omitempty"`
}

// Parser extracts structured content from an app's accessibility data.
//
// Parsers MUST implement CanParse (bundle ID match, used for native apps).
// Parsers MAY implement URLMatcher when they also handle a web version of
// the tool inside a browser — the dispatcher prefers URL matches over
// bundle-ID matches so tool-specific web parsers pre-empt the generic
// browser chrome stripper.
//
// Parsers SHOULD implement ParseTree when they can benefit from the full
// structured AX tree (role/frame/children). Parse (flat-text) is optional
// and serves as a fallback when only flat AX text is available.
type Parser interface {
	// CanParse returns true if this parser handles the given bundle ID.
	CanParse(bundleID string) bool
}

// URLMatcher is an optional capability for parsers that also recognize a
// web version of their tool inside a browser (e.g. Notion at notion.so,
// Linear at linear.app). The registry routes captures with a non-empty
// URL to URL matchers first, so tool-specific parsers win over browser.go.
type URLMatcher interface {
	CanParseURL(url string) bool
}

// TextParser is an optional parser capability for flat AX text. Kept for
// backward compatibility with parsers that don't need the full tree.
type TextParser interface {
	Parser
	Parse(axText, bundleID, windowTitle, url string) *ParsedContent
}

// TreeParser is an optional parser capability for structured AX trees.
// When available, the registry prefers ParseTree over flat-text Parse.
type TreeParser interface {
	Parser
	ParseTree(tree *AXNode, bundleID, windowTitle, url string) *ParsedContent
}

// Describer is an optional capability for parsers that want to advertise
// their supported bundle IDs and URL hosts to UI surfaces (DevTools panel,
// docs page). Without this, parsers remain functional but invisible in the
// catalog — the registry uses CanParse/CanParseURL for actual dispatch,
// not Describe.
type Describer interface {
	Parser
	Describe() ParserCatalogEntry
}

// MultiDescriber is an extension of Describer for parsers whose single
// dispatch implementation handles multiple distinct apps that should
// appear as separate rows in the UI catalog (e.g. Microsoft Word and
// Apple Pages share the office_document parser but operators want to
// see them as independent entries with their own caveats). When both
// Describe and DescribeAll are implemented the catalog prefers
// DescribeAll.
type MultiDescriber interface {
	Parser
	DescribeAll() []ParserCatalogEntry
}

// ParserCatalogEntry is the declarative metadata for a parser exposed to
// the UI. Keep it free of behavioral fields — anything dispatch-relevant
// lives on the CanParse/CanParseURL methods.
type ParserCatalogEntry struct {
	// Name is the stable code identifier (e.g. "slack", "chatgpt_web").
	Name string `json:"name"`
	// Label is the human-readable name shown in UI (e.g. "Slack",
	// "ChatGPT (web)").
	Label string `json:"label"`
	// ContentType is the ParsedContent.ContentType this parser produces
	// when it matches (e.g. "conversation", "document").
	ContentType string `json:"contentType,omitempty"`
	// Bundles lists the macOS bundle IDs this parser claims via CanParse.
	// Empty for parsers that only handle a web version (chatgpt_web,
	// claude_web, github, gmail, google_calendar).
	Bundles []string `json:"bundles,omitempty"`
	// URLHosts lists the host patterns this parser claims via
	// CanParseURL. Empty for native-only parsers.
	URLHosts []string `json:"urlHosts,omitempty"`
	// Notes is a short caveat shown in the DevTools catalog for
	// parsers whose behavior depends on app configuration (VSCode's
	// editor.accessibilitySupport, Slack's AXEnhancedUserInterface
	// priming, etc.). Keep it to a single short sentence — anything
	// longer belongs in source comments.
	Notes string `json:"notes,omitempty"`
}
