package appparser

import (
	"strings"
)

var githubWebHosts = []string{
	"github.com",
}

type githubParser struct{}

func init() {
	Register(&githubParser{})
}

// CanParse always returns false — GitHub has no native macOS app the
// general user runs (GitHub Desktop ships separately and isn't worth
// parsing with this design). The parser only activates via URL match.
func (p *githubParser) CanParse(string) bool { return false }

func (p *githubParser) CanParseURL(rawURL string) bool {
	return hostMatches(rawURL, githubWebHosts...)
}

func (p *githubParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        "github",
		Label:       "GitHub (web)",
		ContentType: "document",
		URLHosts:    append([]string(nil), githubWebHosts...),
	}
}

// ParseTree extracts the focused GitHub page. The shape of the page is
// inferred from the URL path so we can pick the right content type:
//
//	/owner/repo/issues/123      → conversation (issue + comments)
//	/owner/repo/pull/123        → conversation (PR + review comments)
//	/owner/repo/pull/123/files  → document (diff)
//	/owner/repo                 → document (repo README)
//	everything else             → document (generic page text)
//
// We don't try to be exhaustive — when a page doesn't match a known
// shape we fall through to the generic body extractor. The browser
// chrome stripper still wins as a final fallback if we return nil.
func (p *githubParser) ParseTree(tree *AXNode, bundleID, windowTitle, url string) *ParsedContent {
	if tree == nil {
		return nil
	}
	root := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWebArea" })
	if root == nil {
		return nil
	}

	kind := githubPageKind(url)
	switch kind {
	case "issue", "pull":
		if out := githubParseConversation(root, windowTitle, url, kind, bundleID); out != nil {
			return out
		}
	}
	body := githubGenericBody(root)
	if strings.TrimSpace(body) == "" {
		body = MainLandmarkText(root)
		if strings.TrimSpace(body) == "" {
			return nil
		}
	}
	return &ParsedContent{
		ContentType: "document",
		Title:       githubPageTitle(root, windowTitle),
		Body:        body,
		URL:         url,
		Metadata: map[string]string{
			"bundleId": bundleID,
			"app":      "github",
			"page":     kind,
		},
	}
}

// githubPageKind classifies a github.com URL into a coarse page type.
func githubPageKind(rawURL string) string {
	lower := strings.ToLower(rawURL)
	switch {
	case strings.Contains(lower, "/pull/") && strings.HasSuffix(lower, "/files"):
		return "pull_files"
	case strings.Contains(lower, "/pull/"):
		return "pull"
	case strings.Contains(lower, "/issues/") && !strings.HasSuffix(lower, "/issues"):
		return "issue"
	case strings.Contains(lower, "/issues"):
		return "issue_list"
	case strings.Contains(lower, "/discussions/"):
		return "discussion"
	case strings.Contains(lower, "/blob/"), strings.Contains(lower, "/tree/"):
		return "code"
	default:
		return "page"
	}
}

// githubParseConversation extracts the title + comment-thread style body
// for issue and PR pages. Comments are rendered as `<author>: <body>`
// blocks so the model sees who said what.
func githubParseConversation(root *AXNode, windowTitle, url, kind, bundleID string) *ParsedContent {
	title := githubIssueTitle(root, windowTitle)

	comments := githubCollectComments(root)
	if len(comments) == 0 {
		return nil
	}

	return &ParsedContent{
		ContentType: "conversation",
		Title:       title,
		Channel:     githubRepoChannel(url),
		Messages:    comments,
		Body:        renderConversationBody(comments),
		URL:         url,
		Metadata: map[string]string{
			"bundleId": bundleID,
			"app":      "github",
			"page":     kind,
		},
	}
}

// githubIssueTitle pulls the H1 issue/PR title (rendered as an AXHeading)
// or falls back to the leading segment of the window title which has the
// form "Title · Issue #N · owner/repo".
func githubIssueTitle(root *AXNode, windowTitle string) string {
	heading := FindFirst(root, func(n *AXNode) bool {
		return n.Role == "AXHeading"
	})
	if heading != nil {
		t := strings.TrimSpace(Text(heading))
		if t != "" {
			return t
		}
	}
	if windowTitle != "" {
		left := strings.Split(windowTitle, " · ")[0]
		return strings.TrimSpace(left)
	}
	return ""
}

// githubCollectComments walks the page for nodes tagged with the
// `js-comment` / `timeline-comment` classes that GitHub uses to wrap each
// comment block. Inside each block the author is in a `.author` link and
// the body is plain prose under `.comment-body`.
func githubCollectComments(root *AXNode) []ConversationMessage {
	commentBlocks := FindAll(root, func(n *AXNode) bool {
		return HasClassContaining(n, "js-comment") || HasClassContaining(n, "timeline-comment")
	})
	if len(commentBlocks) == 0 {
		return nil
	}
	var msgs []ConversationMessage
	for _, block := range commentBlocks {
		author := githubCommentAuthor(block)
		body := githubCommentBody(block)
		body = strings.TrimSpace(body)
		if body == "" {
			continue
		}
		if author == "" {
			author = "comment"
		}
		msgs = append(msgs, ConversationMessage{
			Sender:  author,
			Content: body,
		})
	}
	return msgs
}

func githubCommentAuthor(block *AXNode) string {
	link := FindFirst(block, func(n *AXNode) bool {
		return n.Role == "AXLink" && HasClassContaining(n, "author")
	})
	if link == nil {
		return ""
	}
	return strings.TrimSpace(Text(link))
}

func githubCommentBody(block *AXNode) string {
	body := FindFirst(block, func(n *AXNode) bool {
		return HasClassContaining(n, "comment-body") || HasClassContaining(n, "markdown-body")
	})
	if body == nil {
		// Fallback: collect all static text in the block.
		return strings.Join(CollectStaticTexts(block), "\n")
	}
	return strings.Join(CollectStaticTexts(body), "\n")
}

// githubGenericBody collects all visible text from the web area, skipping
// the global header/footer and the repo navigation. Used for code pages,
// repo READMEs, and any URL that doesn't match a known shape.
func githubGenericBody(root *AXNode) string {
	var lines []string
	Traverse(root, func(n *AXNode) bool {
		if n.Role == "AXScrollBar" {
			return false
		}
		// Skip site-wide navigation chrome.
		if HasClassContaining(n, "AppHeader") ||
			HasClassContaining(n, "footer") ||
			HasClassContaining(n, "UnderlineNav") {
			return false
		}
		if n.Role == "AXStaticText" {
			t := n.Value
			if t == "" {
				t = n.Description
			}
			t = strings.TrimSpace(t)
			if t != "" {
				lines = append(lines, t)
			}
		}
		return true
	})
	return strings.Join(lines, "\n")
}

func githubPageTitle(root *AXNode, windowTitle string) string {
	heading := FindFirst(root, func(n *AXNode) bool { return n.Role == "AXHeading" })
	if heading != nil {
		t := strings.TrimSpace(Text(heading))
		if t != "" {
			return t
		}
	}
	return strings.TrimSpace(windowTitle)
}

// githubRepoChannel pulls "owner/repo" out of the URL so it can ride along
// as the conversation channel — handy for downstream filtering.
func githubRepoChannel(rawURL string) string {
	lower := strings.ToLower(rawURL)
	idx := strings.Index(lower, "github.com/")
	if idx < 0 {
		return ""
	}
	rest := rawURL[idx+len("github.com/"):]
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "/" + parts[1]
}
