package appparser

import "testing"

func TestGitHubParserConversationCarriesBundleID(t *testing.T) {
	const bundleID = "com.google.Chrome"
	const url = "https://github.com/Isoform/something/pull/1664"
	const windowTitle = "[codex] improve handoff approval preview by alexrowan-dev · Pull Request #1664 · Isoform/something - Google Chrome - Alex"

	tree := &AXNode{
		Role: "AXWindow",
		Children: []*AXNode{
			{
				Role: "AXWebArea",
				Children: []*AXNode{
					{Role: "AXHeading", Value: "[codex] improve handoff approval preview"},
					{
						Role:         "AXGroup",
						DOMClassList: []string{"timeline-comment"},
						Children: []*AXNode{
							{Role: "AXLink", Value: "alexrowan-dev", DOMClassList: []string{"author"}},
							{
								Role:         "AXGroup",
								DOMClassList: []string{"comment-body"},
								Children: []*AXNode{
									{Role: "AXStaticText", Value: "Summary"},
									{Role: "AXStaticText", Value: "Improve the handoff approval preview."},
								},
							},
						},
					},
				},
			},
		},
	}

	parsed := (&githubParser{}).ParseTree(tree, bundleID, windowTitle, url)
	if parsed == nil {
		t.Fatal("expected GitHub PR conversation to parse")
	}
	if parsed.ContentType != "conversation" {
		t.Fatalf("ContentType = %q, want conversation", parsed.ContentType)
	}
	if got := parsed.Metadata["bundleId"]; got != bundleID {
		t.Fatalf("metadata bundleId = %q, want %q", got, bundleID)
	}
	if got := parsed.Metadata["page"]; got != "pull" {
		t.Fatalf("metadata page = %q, want pull", got)
	}
}
