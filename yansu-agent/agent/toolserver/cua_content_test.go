package toolserver

import (
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"yansu-agent/agent/tools"
)

func TestCuaDaemonEnvelopePreservesImageContent(t *testing.T) {
	const envelope = `{"kind":"call","payload":{"content":[{"type":"text","text":"window state"},{"type":"image","data":"aW1hZ2U=","mimeType":"image/png"}],"window_id":42}}`
	result := resultToMCP("cua_driver", tools.ToolResult{Content: envelope})
	if result.IsError || len(result.Content) != 2 {
		t.Fatalf("image envelope lost: %+v", result)
	}
	img, ok := result.Content[0].(mcp.ImageContent)
	if !ok || img.Data != "aW1hZ2U=" || img.MIMEType != "image/png" {
		t.Fatalf("image not delivered as image content: %#v", result.Content[0])
	}
	text, ok := result.Content[1].(mcp.TextContent)
	if !ok || !strings.Contains(text.Text, `"window_id":42`) || !strings.Contains(text.Text, "window state") || strings.Contains(text.Text, "aW1hZ2U=") {
		t.Fatalf("metadata lost or image duplicated: %#v", result.Content[1])
	}
	for _, tc := range []struct {
		name, content string
		failed        bool
	}{
		{"cua_driver", `{"app":"browser","elements":[]}`, false},
		{"cua_driver", "Not returned: a run on its own reads windows with get_window_state, never as a plain screenshot.", true},
		{"Read", envelope, false},
	} {
		got := resultToMCP(tc.name, tools.ToolResult{Content: tc.content, IsError: tc.failed})
		if len(got.Content) != 1 || got.Content[0].(mcp.TextContent).Text != tc.content || got.IsError != tc.failed {
			t.Fatalf("transport changed gated or non-cua result: %+v", got)
		}
	}
}
