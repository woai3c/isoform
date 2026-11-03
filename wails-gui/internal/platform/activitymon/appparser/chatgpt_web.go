package appparser

// chatgptWebSpec captures chatgpt.com / chat.openai.com conversations
// rendered in a browser. The native macOS ChatGPT app (com.openai.chat)
// is handled separately by chatgpt.go because the SwiftUI app exposes a
// completely different AX layout — sharing code between the two would
// trade clarity for a fragile abstraction.
//
// Class anchors (verified against a live chatgpt.com dump):
//
//   - "user-message-bubble-color" lives on the inner bubble div for
//     user input; its parent AXGroup is the message container we want
//     to surface, but matching on the inner is fine because the
//     bubble div is the only node carrying the user's text.
//   - "agent-turn" rides on the outer AXGroup of an assistant turn.
//     The class is short and unlikely to collide with unrelated
//     elements; substring match is safe.
//
// Title comes from AXWebArea.title which is the ChatGPT-generated
// conversation name (e.g. "Universal Control 设置指南"). No suffix
// stripping needed — the title is already clean.
var chatgptWebSpec = webChatSpec{
	AppName:           "chatgpt-web",
	Hosts:             []string{"chatgpt.com", "chat.openai.com"},
	UserMessageClass:  "user-message-bubble-color",
	AgentMessageClass: "agent-turn",
	AgentSender:       "ChatGPT",
}

type chatgptWebParser struct{}

func init() {
	Register(&chatgptWebParser{})
}

// CanParse intentionally returns false: this parser only dispatches via
// URL match. The native ChatGPT desktop app's bundle ID is handled by
// chatgpt.go.
func (p *chatgptWebParser) CanParse(_ string) bool { return false }

func (p *chatgptWebParser) CanParseURL(rawURL string) bool {
	return hostMatches(rawURL, chatgptWebSpec.Hosts...)
}

func (p *chatgptWebParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        "chatgpt_web",
		Label:       "ChatGPT (web)",
		ContentType: "conversation",
		URLHosts:    append([]string(nil), chatgptWebSpec.Hosts...),
	}
}

func (p *chatgptWebParser) ParseTree(tree *AXNode, bundleID, _windowTitle, url string) *ParsedContent {
	return webChatDriver(tree, chatgptWebSpec, bundleID, url)
}
