package appparser

// Tencent's Windows chat clients — TIM, QQ, WeChat (now "微信" /
// weixin.exe) — share the same problem: they're built on the Duilib
// custom UI framework with no IAccessible / UIA provider
// implementation, so accessibility consumers can see the OS-assigned
// window chrome (close / minimize / title bar) but nothing else.
//
// The Yansu UIA walker on Windows falls back to the foreground window
// title when the tree is empty, so the captured ax_text for these apps
// is typically just the chat group / contact name (e.g.
// "nand2tetris交流群"). That's metadata, not content — the actual
// messages, contact list, and search results never reach UIA.
//
// This parser exists purely to set AXCoverageMetadataOnly on the
// ParsedContent so the activity popup flips its default-open section
// from "UI Automation text" (which would just show the group name)
// to "OCR text" (which at least carries the visible messages, even
// if degraded by font / density). It mirrors what code_editor.go does
// for VSCode / Cursor, where Monaco's canvas-rendered buffer is
// likewise invisible to AX.
//
// Cross-platform: matches lowercase Windows exe basenames AND the
// canonical macOS bundle IDs we've documented in unsupported.go for
// the same Tencent apps. Either world hits the same fallback path.

// tencentChatBundleIDs covers Tencent's desktop chat surfaces. Keep
// the list aligned with the Windows-side bundleID stand-in
// (lowercase exe basename) and the macOS bundle IDs we've audited.
var tencentChatBundleIDs = map[string]bool{
	// Windows exe basenames (see monitor_windows.go FrontmostAppBundleID).
	"tim.exe":    true, // Tencent TIM
	"qq.exe":     true, // Tencent QQ
	"weixin.exe": true, // WeChat (current, renamed to "微信")
	"wechat.exe": true, // WeChat (older Windows builds)
	// macOS bundle IDs — already documented in unsupported.go but
	// included here so the dispatcher tags the snapshot consistently
	// across platforms.
	"com.tencent.xinWeChat": true,
	"com.tencent.qq":        true,
	"com.tencent.qqmacmgr":  true,
	"com.tencent.tim":       true,
}

const tencentChatAppName = "tencent_chat"

type tencentChatParser struct{}

func init() {
	Register(&tencentChatParser{})
}

func (p *tencentChatParser) CanParse(bundleID string) bool {
	return tencentChatBundleIDs[bundleID]
}

func (p *tencentChatParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        tencentChatAppName,
		Label:       "TIM / WeChat / QQ",
		ContentType: "document",
		Bundles: []string{
			"tim.exe",
			"qq.exe",
			"weixin.exe",
			"wechat.exe",
			"com.tencent.xinWeChat",
			"com.tencent.qq",
			"com.tencent.tim",
		},
		Notes: "Tencent's Duilib-based desktop clients (TIM, QQ, WeChat) " +
			"don't expose chat content via UIA / macOS AX. The walker can " +
			"only reach the OS window chrome (title bar, close button) — " +
			"messages, contact list, and search results are invisible. " +
			"We flag AXCoverageMetadataOnly so the OCR signal becomes the " +
			"load-bearing source of text in the activity popup and the " +
			"triager fuses OCR into the parsed body.",
	}
}

// ParseTree produces a metadata-only ParsedContent for any Tencent chat
// surface. The window title (passed in by the dispatcher) usually
// carries the active group / contact name, which is the only piece of
// signal AX can give us; OCR fills in the rest via the standard
// ax_ocr_fusion path.
func (p *tencentChatParser) ParseTree(_ *AXNode, bundleID, windowTitle, _ string) *ParsedContent {
	return p.buildContent(bundleID, windowTitle)
}

// Parse implements the flat-text fallback signature so the parser still
// fires when only ax_text is available (no structured tree). The body
// is the same metadata-only shape either way.
func (p *tencentChatParser) Parse(_, bundleID, windowTitle, _ string) *ParsedContent {
	return p.buildContent(bundleID, windowTitle)
}

func (p *tencentChatParser) buildContent(bundleID, windowTitle string) *ParsedContent {
	meta := map[string]string{
		"app":        tencentChatAppName,
		"bundleId":   bundleID,
		"axCoverage": AXCoverageMetadataOnly,
	}
	body := windowTitle
	title := windowTitle
	if body == "" {
		// Even when the title is blank we still want to register a hit
		// so the popup flips OCR-first. Empty body is fine — consumers
		// already handle that via MergeWithOCR.
		body = "(window title unavailable)"
	}
	return &ParsedContent{
		ContentType: "document",
		Title:       title,
		Body:        body,
		Metadata:    meta,
	}
}
