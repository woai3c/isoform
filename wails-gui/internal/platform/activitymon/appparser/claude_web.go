package appparser

// claudeWebSpec captures claude.ai conversations rendered in a browser.
// Anthropic uses semantic Tailwind classes that line up cleanly with
// our driver:
//
//   - User messages are wrapped in an AXGroup whose class list contains
//     "font-user-message" (Anthropic adds Tailwind's "!" importance
//     prefix in some cases, e.g. "!font-user-message"; substring match
//     finds either form).
//   - Assistant replies use "font-claude-response" on the outer group
//     and "font-claude-response-body" on the prose subtree. Matching
//     the outer is the right granularity for our message-per-turn
//     output.
//
// Claude appends " - Claude" to the AXWebArea title — that suffix is
// not useful in ParsedContent.Title (it'd repeat the app name we
// already publish in metadata.app), so the driver strips it.
var claudeWebSpec = webChatSpec{
	AppName:           "claude-web",
	Hosts:             []string{"claude.ai"},
	UserMessageClass:  "font-user-message",
	AgentMessageClass: "font-claude-response",
	AgentSender:       "Claude",
	TitleSuffix:       " - Claude",
}

type claudeWebParser struct{}

func init() {
	Register(&claudeWebParser{})
}

// CanParse intentionally returns false: claude.ai has no native macOS
// counterpart with its own bundle ID, so URL match is the only dispatch
// path.
func (p *claudeWebParser) CanParse(_ string) bool { return false }

func (p *claudeWebParser) CanParseURL(rawURL string) bool {
	return hostMatches(rawURL, claudeWebSpec.Hosts...)
}

func (p *claudeWebParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        "claude_web",
		Label:       "Claude (web)",
		ContentType: "conversation",
		URLHosts:    append([]string(nil), claudeWebSpec.Hosts...),
	}
}

func (p *claudeWebParser) ParseTree(tree *AXNode, bundleID, _windowTitle, url string) *ParsedContent {
	return webChatDriver(tree, claudeWebSpec, bundleID, url)
}
