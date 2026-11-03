package appparser

import (
	"testing"
)

func TestTencentChat_CanParse(t *testing.T) {
	p := &tencentChatParser{}
	cases := []struct {
		bundleID string
		want     bool
	}{
		// Windows exe basenames
		{"tim.exe", true},
		{"qq.exe", true},
		{"weixin.exe", true},
		{"wechat.exe", true},
		// macOS bundle IDs
		{"com.tencent.xinWeChat", true},
		{"com.tencent.qq", true},
		{"com.tencent.tim", true},
		// Negatives
		{"slack.exe", false},
		{"chrome.exe", false},
		{"com.tinyspeck.slackmacgap", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := p.CanParse(tc.bundleID); got != tc.want {
			t.Errorf("CanParse(%q) = %v, want %v", tc.bundleID, got, tc.want)
		}
	}
}

func TestTencentChat_ParseSetsMetadataOnly(t *testing.T) {
	p := &tencentChatParser{}
	got := p.ParseTree(nil, "tim.exe", "nand2tetris交流群", "")
	if got == nil {
		t.Fatal("expected ParsedContent, got nil")
	}
	if got.Metadata["axCoverage"] != AXCoverageMetadataOnly {
		t.Errorf("axCoverage: got %q, want %q", got.Metadata["axCoverage"], AXCoverageMetadataOnly)
	}
	if got.Metadata["app"] != tencentChatAppName {
		t.Errorf("app: got %q, want %q", got.Metadata["app"], tencentChatAppName)
	}
	if got.Title != "nand2tetris交流群" || got.Body != "nand2tetris交流群" {
		t.Errorf("title/body should carry the window title; got title=%q body=%q", got.Title, got.Body)
	}
}

func TestTencentChat_ParseFlatTextPathMatchesTreePath(t *testing.T) {
	// Both ParseTree and Parse should produce the same metadata-only
	// shape — neither has any actual content to extract from.
	p := &tencentChatParser{}
	fromTree := p.ParseTree(nil, "weixin.exe", "Pd (1)", "")
	fromText := p.Parse("", "weixin.exe", "Pd (1)", "")
	if fromTree.Body != fromText.Body ||
		fromTree.Title != fromText.Title ||
		fromTree.Metadata["axCoverage"] != fromText.Metadata["axCoverage"] {
		t.Errorf("tree vs text paths diverged:\n  tree: %+v\n  text: %+v", fromTree, fromText)
	}
}

func TestTencentChat_HandlesEmptyTitle(t *testing.T) {
	p := &tencentChatParser{}
	got := p.ParseTree(nil, "wechat.exe", "", "")
	if got == nil {
		t.Fatal("expected ParsedContent even with empty title")
	}
	if got.Metadata["axCoverage"] != AXCoverageMetadataOnly {
		t.Errorf("axCoverage should still be metadata_only; got %q", got.Metadata["axCoverage"])
	}
	if got.Body == "" {
		t.Errorf("body should fall back to a placeholder; got empty")
	}
}

func TestTencentChat_IsPartialAX(t *testing.T) {
	p := &tencentChatParser{}
	got := p.ParseTree(nil, "tim.exe", "群名", "")
	if !IsPartialAX(got) {
		t.Errorf("Tencent chat output should be flagged as partial AX so OCR fusion kicks in")
	}
}
