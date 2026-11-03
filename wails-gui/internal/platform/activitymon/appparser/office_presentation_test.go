package appparser

import (
	"strings"
	"testing"
)

func presTextArea(s string) *AXNode {
	return &AXNode{Role: "AXTextArea", Value: s}
}

func presStaticText(s string) *AXNode {
	return &AXNode{Role: "AXStaticText", Value: s}
}

func presWindow(title string, canvas []*AXNode, navigatorTitles ...string) *AXNode {
	var navRows []*AXNode
	for _, n := range navigatorTitles {
		navRows = append(navRows, &AXNode{
			Role: "AXRow",
			Children: []*AXNode{
				{Role: "AXStaticText", Title: n, Value: n},
			},
		})
	}
	return &AXNode{
		Role:  "AXWindow",
		Title: title,
		Children: []*AXNode{
			{Role: "AXToolbar", Children: []*AXNode{
				{Role: "AXButton", Title: "Insert"},
				presStaticText("TOOLBAR_LEAK_SHOULD_NOT_APPEAR"),
			}},
			{Role: "AXOutline", Children: navRows},
			{Role: "AXScrollArea", Children: canvas},
		},
	}
}

func TestOfficePresentationParser_CanParse(t *testing.T) {
	p := &officePresentationParser{}
	for _, b := range []string{"com.microsoft.Powerpoint", "com.apple.Keynote", "com.apple.iWork.Keynote"} {
		if !p.CanParse(b) {
			t.Errorf("CanParse(%q) = false, want true", b)
		}
	}
	if p.CanParse("com.microsoft.Word") {
		t.Errorf("Word must not match the presentation parser")
	}
}

// TestOfficePresentationParser_CollectsTextFrames covers the Keynote
// canvas path. PowerPoint cannot use this test because it exposes no
// AXTextArea for slide body (Microsoft privacy posture); the
// PowerPoint outline path is covered separately below.
func TestOfficePresentationParser_CollectsTextFrames(t *testing.T) {
	canvas := []*AXNode{
		presTextArea("Slide title"),
		presTextArea("Bullet one"),
		presTextArea("Bullet two"),
	}
	tree := presWindow("Q4 Deck.key — Keynote", canvas, "Title", "Agenda", "Q4 Numbers")

	p := &officePresentationParser{}
	got := p.ParseTree(tree, "com.apple.iWork.Keynote", "Q4 Deck.key — Keynote", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	if got.ContentType != "presentation" {
		t.Errorf("contentType: got %q want presentation", got.ContentType)
	}
	if got.Title != "Q4 Deck.key" {
		t.Errorf("title: got %q want %q", got.Title, "Q4 Deck.key")
	}
	if !strings.Contains(got.Body, "Slide title") {
		t.Errorf("body missing slide title: %q", got.Body)
	}
	if !strings.Contains(got.Body, "Bullet one") || !strings.Contains(got.Body, "Bullet two") {
		t.Errorf("body missing bullets: %q", got.Body)
	}
	if strings.Contains(got.Body, "TOOLBAR_LEAK_SHOULD_NOT_APPEAR") {
		t.Errorf("toolbar text leaked into body: %q", got.Body)
	}
	for _, navTitle := range []string{"Agenda", "Q4 Numbers"} {
		if strings.Contains(got.Body, navTitle) {
			t.Errorf("navigator slide title %q leaked into body: %q", navTitle, got.Body)
		}
	}
}

func TestOfficePresentationParser_ActiveSlideMetadata(t *testing.T) {
	canvas := []*AXNode{presTextArea("Body")}
	tree := presWindow("Deck — Keynote", canvas, "Cover", "Intro", "Details")
	// Mark navigator row 2 ("Intro") as the active slide.
	outline := tree.Children[1]
	outline.Children[1].Value = "1"

	p := &officePresentationParser{}
	got := p.ParseTree(tree, "com.apple.iWork.Keynote", "Deck — Keynote", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}
	if got.Metadata["activeSlideIndex"] != "2" {
		t.Errorf("activeSlideIndex: got %q want %q", got.Metadata["activeSlideIndex"], "2")
	}
	if got.Metadata["activeSlideTitle"] != "Intro" {
		t.Errorf("activeSlideTitle: got %q want %q", got.Metadata["activeSlideTitle"], "Intro")
	}
}

func TestOfficePresentationParser_EmptyCanvasReturnsNil(t *testing.T) {
	tree := &AXNode{Role: "AXWindow", Title: "Empty - PowerPoint"}
	p := &officePresentationParser{}
	if got := p.ParseTree(tree, "com.microsoft.Powerpoint", "Empty - PowerPoint", ""); got != nil {
		t.Errorf("expected nil for empty tree, got %+v", got)
	}
}

// TestOfficePresentationParser_PowerpointOutlineZh mirrors the AX
// shape captured from PowerPoint for Mac under the zh-CN UI. The slide
// body is NEVER exposed (Microsoft's privacy posture, mirroring
// Apple's Numbers); only the navigator thumbnails carry per-slide
// metadata via AXCell.description, and the active slide index lives on
// AXLayoutArea.description. The parser must assemble these into an
// outline.
func TestOfficePresentationParser_PowerpointOutlineZh(t *testing.T) {
	window := &AXNode{
		Role:  "AXWindow",
		Title: "2024掘金年度总结",
		Children: []*AXNode{
			{Role: "AXLayoutArea", Description: "幻灯片编辑器窗格、第 2 张幻灯片，共 4 张"},
			{Role: "AXList", Description: "缩略图窗格", Children: []*AXNode{
				{Role: "AXList", Children: []*AXNode{
					{Role: "AXGroup", Children: []*AXNode{
						{Role: "AXCell", Description: "掘金2024年度报告, 第 1 张幻灯片，共 4 张"},
					}},
					{Role: "AXGroup", Children: []*AXNode{
						{Role: "AXCell", Description: "效率与系统健壮性提升, 第 2 张幻灯片，共 4 张, 有备注"},
					}},
					{Role: "AXGroup", Children: []*AXNode{
						{Role: "AXCell", Description: "新模块 & 重点业务支持 & 年度数据, 第 3 张幻灯片，共 4 张, 有备注"},
					}},
					{Role: "AXGroup", Children: []*AXNode{
						{Role: "AXCell", Description: "第 4 张幻灯片，共 4 张"},
					}},
				}},
			}},
			// Body-of-slide AX nodes that PowerPoint NEVER exposes —
			// included here to assert we don't accidentally consume them.
			{Role: "AXStaticText", Value: "幻灯片 2 / 4"},
		},
	}

	p := &officePresentationParser{}
	got := p.ParseTree(window, "com.microsoft.Powerpoint", "2024掘金年度总结", "")
	if got == nil {
		t.Fatalf("expected outline ParsedContent, got nil")
	}
	if got.ContentType != "presentation" {
		t.Errorf("contentType: got %q want presentation", got.ContentType)
	}
	if got.Title != "2024掘金年度总结" {
		t.Errorf("title: got %q", got.Title)
	}

	for _, want := range []string{
		"Slide 2 of 4",
		"1. 掘金2024年度报告",
		"2. 效率与系统健壮性提升",
		"[active]",
		"[has notes]",
		"3. 新模块 & 重点业务支持 & 年度数据",
		"4. (no title)",
	} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("body missing %q: %q", want, got.Body)
		}
	}
	// The "[active]" marker must attach to slide 2, not 1 or 3.
	idxLine := strings.Index(got.Body, "2. 效率与系统健壮性提升")
	idxActive := strings.Index(got.Body, "[active]")
	idxLine3 := strings.Index(got.Body, "3. 新模块")
	if !(idxLine < idxActive && idxActive < idxLine3) {
		t.Errorf("[active] marker not attached to slide 2: body=%q", got.Body)
	}

	if got.Metadata["activeSlideIndex"] != "2" {
		t.Errorf("activeSlideIndex: got %q want 2", got.Metadata["activeSlideIndex"])
	}
	if got.Metadata["activeSlideTitle"] != "效率与系统健壮性提升" {
		t.Errorf("activeSlideTitle: got %q", got.Metadata["activeSlideTitle"])
	}
	if got.Metadata["slidesTotal"] != "4" {
		t.Errorf("slidesTotal: got %q want 4", got.Metadata["slidesTotal"])
	}
	if got.Metadata["axCoverage"] != "outline_only" {
		t.Errorf("axCoverage: got %q want outline_only", got.Metadata["axCoverage"])
	}
}

// TestOfficePresentationParser_PowerpointOutlineEn covers the en UI
// locale: "Slide N of M" / "Has notes" without commas in the active
// layout description.
func TestOfficePresentationParser_PowerpointOutlineEn(t *testing.T) {
	window := &AXNode{
		Role:  "AXWindow",
		Title: "Deck",
		Children: []*AXNode{
			{Role: "AXLayoutArea", Description: "Slide editor pane, slide 1 of 2"},
			{Role: "AXCell", Description: "Cover, Slide 1 of 2"},
			{Role: "AXCell", Description: "Agenda, Slide 2 of 2, Has notes"},
		},
	}
	p := &officePresentationParser{}
	got := p.ParseTree(window, "com.microsoft.Powerpoint", "Deck", "")
	if got == nil {
		t.Fatalf("expected outline ParsedContent, got nil")
	}
	for _, want := range []string{"Slide 1 of 2", "1. Cover  [active]", "2. Agenda", "[has notes]"} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("body missing %q: %q", want, got.Body)
		}
	}
	if got.Metadata["activeSlideIndex"] != "1" {
		t.Errorf("activeSlideIndex: got %q want 1", got.Metadata["activeSlideIndex"])
	}
}

// TestOfficePresentationParser_KeynoteRealShape mirrors the actual AX
// structure captured from Keynote 14 on macOS:
//
//   - the right-hand Format Inspector is an AXGroup with
//     description="格式化工具" carrying AXRadioGroup tabs, AXCheckBox
//     toggles, and AXStaticText labels ("外观", "背景", "当前填充", …).
//   - the title-bar copy of the document name and the missing-font
//     warning are AXStaticText nodes hung directly off AXWindow, not
//     under any canvas container.
//
// None of these chrome strings may leak into Body.
func TestOfficePresentationParser_KeynoteRealShape(t *testing.T) {
	formatInspector := &AXNode{
		Role:        "AXGroup",
		Description: "格式化工具",
		Children: []*AXNode{
			{
				Role: "AXRadioGroup",
				Children: []*AXNode{
					{Role: "AXRadioButton", Description: "幻灯片"},
				},
			},
			{
				Role:  "AXScrollArea",
				Title: "\"幻灯片\"格式化工具",
				Children: []*AXNode{
					{Role: "AXStaticText", Value: "外观"},
					{Role: "AXCheckBox", Title: "标题"},
					{Role: "AXCheckBox", Title: "正文"},
					{Role: "AXStaticText", Value: "背景"},
					{Role: "AXStaticText", Value: "当前填充"},
					{Role: "AXStaticText", Value: "显示"},
					{Role: "AXStaticText", Value: "媒体"},
				},
			},
		},
	}
	canvas := &AXNode{
		Role: "AXSplitGroup",
		Children: []*AXNode{
			{Role: "AXScrollArea", Children: []*AXNode{ /* navigator placeholder */ }},
			{Role: "AXSplitter"},
			{
				Role: "AXScrollArea",
				Children: []*AXNode{
					presTextArea("效率与系统健壮性提升"),
					presTextArea("提升系统效率"),
					presTextArea("测试流程自动化\n音视频码流自动化同步"),
					presTextArea("01"),
				},
			},
		},
	}
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "2024掘金年度总结",
		Children: []*AXNode{
			{Role: "AXToolbar", Children: []*AXNode{{Role: "AXButton", Title: "Play"}}},
			formatInspector,
			canvas,
			// Window-direct stray nodes — must never reach the body.
			{Role: "AXStaticText", Value: "此演示文稿缺少一种字体。"},
			{Role: "AXStaticText", Value: "2024掘金年度总结"},
			{Role: "AXImage"},
			{Role: "AXMenuButton"},
		},
	}

	p := &officePresentationParser{}
	got := p.ParseTree(tree, "com.apple.iWork.Keynote", "", "")
	if got == nil {
		t.Fatalf("expected ParsedContent, got nil")
	}

	wantPresent := []string{"效率与系统健壮性提升", "提升系统效率", "测试流程自动化", "01"}
	for _, s := range wantPresent {
		if !strings.Contains(got.Body, s) {
			t.Errorf("body missing slide text %q: %q", s, got.Body)
		}
	}

	chromeLeaks := []string{
		"外观", "背景", "当前填充", "显示", "媒体",
		"此演示文稿缺少一种字体",
		"幻灯片", // Inspector radio button description
	}
	for _, leak := range chromeLeaks {
		if strings.Contains(got.Body, leak) {
			t.Errorf("chrome leak %q in body: %q", leak, got.Body)
		}
	}
	// The window title is the same string as a stray AXStaticText
	// node. Body must not contain it because the stray was dropped;
	// the title arrives separately via ParsedContent.Title.
	if strings.Contains(got.Body, "2024掘金年度总结") {
		t.Errorf("stray window-title duplicate leaked into body: %q", got.Body)
	}
	if got.Title != "2024掘金年度总结" {
		t.Errorf("title: got %q want %q", got.Title, "2024掘金年度总结")
	}
}
