package appparser

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Native macOS presentation apps. PowerPoint for Mac and Keynote both
// render the active slide inside an AXScrollArea (the editor canvas),
// the slide navigator on the left as an AXOutline, and the toolbar
// strip at the top as an AXToolbar — uniformly across recent versions.
var officePresentationBundleIDs = map[string]string{
	"com.microsoft.Powerpoint": "powerpoint",
	"com.apple.Keynote":        "keynote",
	"com.apple.iWork.Keynote":  "keynote",
}

type officePresentationParser struct{}

func init() { Register(&officePresentationParser{}) }

func (p *officePresentationParser) CanParse(bundleID string) bool {
	_, ok := officePresentationBundleIDs[bundleID]
	return ok
}

func (p *officePresentationParser) DescribeAll() []ParserCatalogEntry {
	// Both Microsoft and Apple expose only the slide-navigator
	// thumbnails through AX — the slide canvas (text frames, shapes,
	// images on the current slide) is never reachable. The parser
	// always flags axCoverage=outline_only so the pipeline fuses OCR
	// to recover the visible slide body.
	canvasNote := "Slide canvas isn't exposed via macOS AX — only the navigator outline (per-slide titles + active marker). OCR fills in the visible slide body."
	return []ParserCatalogEntry{
		{
			Name:        "office_presentation_powerpoint",
			Label:       "Microsoft PowerPoint",
			ContentType: "document",
			Bundles:     []string{"com.microsoft.Powerpoint"},
			Notes:       canvasNote,
		},
		{
			Name:        "office_presentation_keynote",
			Label:       "Apple Keynote",
			ContentType: "document",
			Bundles: []string{
				"com.apple.Keynote",
				"com.apple.iWork.Keynote",
			},
			Notes: canvasNote,
		},
	}
}

// ParseTree extracts the active slide's text content. Strategy:
//
//  1. Skip well-known chrome subtrees (AXToolbar, AXTabGroup) and the
//     slide navigator (AXOutline) so left-rail thumbnails and ribbon
//     labels don't bleed into the body.
//  2. Inside the remaining tree, collect every AXTextArea / AXTextField
//     value plus visible AXStaticText. These cover speaker-notes panes,
//     the active slide canvas, and floating annotation text alike.
//  3. Render as one text frame per line. The frames are visually
//     positioned on a slide, but we preserve only document order — most
//     downstream consumers (the AI context build, search, dedup) want
//     readable text, not a layout.
//
// Slide index is best-effort: when the navigator outline carries a row
// whose Value reads "1" (AppKit's accessible-selected proxy), we record
// its position and Title in Metadata. Otherwise the metadata is omitted
// rather than guessed.
func (p *officePresentationParser) ParseTree(tree *AXNode, bundleID, windowTitle, _ string) *ParsedContent {
	if tree == nil {
		return nil
	}
	appName, ok := officePresentationBundleIDs[bundleID]
	if !ok {
		return nil
	}
	window := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWindow" })
	if window == nil {
		window = tree
	}

	title := officeStripAppSuffix(windowTitle, appName)
	if title == "" && window != nil {
		title = officeStripAppSuffix(window.Title, appName)
	}

	// PowerPoint for Mac exposes zero AXTextArea / AXStaticText for
	// slide body content (Microsoft's privacy posture, mirroring
	// Apple's Numbers). Only the navigator thumbnails carry the slide
	// titles, via AXCell.description. Render an outline from those
	// instead of returning nil; OCR remains the path for body text.
	if appName == "powerpoint" {
		return officePowerpointOutline(window, bundleID, appName, title)
	}

	frames := officePresentationCollectFrames(window)
	body := strings.TrimSpace(strings.Join(frames, "\n"))
	if body == "" {
		return nil
	}

	meta := map[string]string{
		"bundleId": bundleID,
		"app":      appName,
		"frames":   strconv.Itoa(len(frames)),
	}
	if idx, slideTitle := officePresentationActiveSlide(window); idx > 0 {
		meta["activeSlideIndex"] = strconv.Itoa(idx)
		if slideTitle != "" {
			meta["activeSlideTitle"] = slideTitle
		}
	}

	return &ParsedContent{
		ContentType: "presentation",
		Title:       title,
		Body:        body,
		Metadata:    meta,
	}
}

// officePresentationCollectFrames walks the window depth-first,
// skipping chrome subtrees + the slide navigator + the format
// inspector, and returns one entry per text frame in traversal order.
//
// Skip rules (verified against a real Keynote 14 ax-tree-dump):
//
//  1. Role-based skip — chrome controls that never carry slide text:
//     AXToolbar, AXOutline (slide navigator), AXTabGroup, AXMenuBar,
//     AXScrollBar, AXSplitter, AXRadioGroup / AXRadioButton (Inspector
//     tab selector), AXCheckBox (Inspector toggles), AXMenuButton,
//     AXPopUpButton, AXSlider, AXIncrementor, AXDisclosureTriangle.
//
//  2. Description-based skip — Keynote's Format Inspector identifies
//     itself with an AXGroup whose description is "格式化工具" (zh-CN)
//     or "Format" / "Inspector" (en).
//
//  3. Window-direct stray skip — Keynote hangs the title-bar copy of
//     the document name and the missing-font warning directly under
//     AXWindow, not inside any canvas container. Real slide text
//     always lives inside AXSplitGroup or AXScrollArea, so we drop
//     AXStaticText / AXImage / AXTextField that sit at the window's
//     top level. AXTextArea is exempt because hypothetical apps may
//     hang an editable canvas directly off the window.
//
// AXTextArea is the primary text source: Keynote uses it for every
// editable text box on the canvas (title, body, page numbers).
// AXStaticText is kept too because cover slides and PowerPoint
// sometimes route text through it.
func officePresentationCollectFrames(window *AXNode) []string {
	var frames []string
	var walk func(n, parent *AXNode)
	walk = func(n, parent *AXNode) {
		if n == nil {
			return
		}
		switch n.Role {
		case "AXToolbar", "AXOutline", "AXTabGroup", "AXMenuBar",
			"AXScrollBar", "AXSplitter",
			"AXRadioGroup", "AXRadioButton", "AXCheckBox",
			"AXMenuButton", "AXPopUpButton", "AXSlider",
			"AXIncrementor", "AXDisclosureTriangle":
			return
		}
		if n.Role == "AXGroup" {
			switch strings.TrimSpace(n.Description) {
			case "格式化工具", "格式", "Format", "Inspector":
				return
			}
		}
		if parent != nil && parent.Role == "AXWindow" {
			switch n.Role {
			case "AXStaticText", "AXImage", "AXTextField":
				return
			}
		}
		switch n.Role {
		case "AXTextArea", "AXTextField":
			s := strings.TrimSpace(n.Value)
			if s == "" {
				s = strings.TrimSpace(n.Description)
			}
			if s != "" {
				frames = append(frames, s)
			}
			// Don't descend into a text input — its children are
			// AXStaticText mirroring the value we just captured.
			return
		case "AXStaticText":
			s := strings.TrimSpace(n.Value)
			if s == "" {
				s = strings.TrimSpace(n.Description)
			}
			if s != "" {
				frames = append(frames, s)
			}
			return
		}
		for _, c := range n.Children {
			walk(c, n)
		}
	}
	walk(window, nil)
	return frames
}

// officePresentationActiveSlide tries to recover the position + title
// of the currently selected slide from the navigator outline.
// Returns (0, "") when no candidate row exposes a usable signal.
func officePresentationActiveSlide(window *AXNode) (int, string) {
	outline := FindFirst(window, func(n *AXNode) bool { return n.Role == "AXOutline" })
	if outline == nil {
		return 0, ""
	}
	rows := FindAll(outline, func(n *AXNode) bool { return n.Role == "AXRow" })
	for i, row := range rows {
		if row == nil {
			continue
		}
		if row.Value != "1" {
			continue
		}
		title := ""
		if t := FindFirst(row, func(n *AXNode) bool {
			return n.Role == "AXStaticText" && (n.Value != "" || n.Title != "")
		}); t != nil {
			title = strings.TrimSpace(t.Value)
			if title == "" {
				title = strings.TrimSpace(t.Title)
			}
		}
		return i + 1, title
	}
	return 0, ""
}

// PowerPoint for Mac handles slide content very differently from
// Keynote: it does not expose any AXTextArea or AXStaticText for the
// slide body, so the canvas-based path used for Keynote returns
// nothing useful. The one signal it does expose is the navigator
// thumbnails — each is an AXCell whose description carries the slide
// title, position, total count, and a "has notes" flag. The currently
// edited slide is identified separately via an AXLayoutArea
// description. We assemble these into an outline so the snapshot
// carries structured slide metadata even when OCR is needed for the
// pixel content of the active slide.

// powerpointSlideDescZhRe matches AXCell.description in the zh-CN UI,
// e.g. "效率与系统健壮性提升, 第 2 张幻灯片，共 4 张, 有备注"
// or untitled "第 4 张幻灯片，共 4 张".
var powerpointSlideDescZhRe = regexp.MustCompile(
	`^(?:(.+?),\s*)?第\s*(\d+)\s*张幻灯片[,，]\s*共\s*(\d+)\s*张(?:[,，]\s*(有备注))?\s*$`,
)

// powerpointSlideDescEnRe matches AXCell.description in the en UI,
// e.g. "Quarterly review, Slide 2 of 4, Has notes" or "Slide 4 of 4".
var powerpointSlideDescEnRe = regexp.MustCompile(
	`^(?:(.+?),\s*)?Slide\s+(\d+)\s+of\s+(\d+)(?:,\s*(Has notes))?\s*$`,
)

// powerpointActiveLayoutZhRe / powerpointActiveLayoutEnRe pick the
// active slide index out of the AXLayoutArea.description string. The
// editor pane carries this in both locales:
//
//	zh-CN: "幻灯片编辑器窗格、第 2 张幻灯片，共 4 张"
//	en:    "Slide editor pane, slide 2 of 4"
var (
	powerpointActiveLayoutZhRe = regexp.MustCompile(`第\s*(\d+)\s*张幻灯片`)
	powerpointActiveLayoutEnRe = regexp.MustCompile(`(?i)slide\s+(\d+)\s+of`)
)

func officePowerpointOutline(window *AXNode, bundleID, appName, title string) *ParsedContent {
	type slide struct {
		idx      int
		title    string
		hasNotes bool
	}
	var slides []slide
	total := 0
	seenIdx := map[int]bool{}

	Traverse(window, func(n *AXNode) bool {
		if n == nil || n.Role != "AXCell" {
			return true
		}
		desc := strings.TrimSpace(n.Description)
		if desc == "" {
			return true
		}
		var (
			rawTitle string
			idx, t   int
			hasNotes bool
		)
		if m := powerpointSlideDescZhRe.FindStringSubmatch(desc); m != nil {
			rawTitle = m[1]
			idx, _ = strconv.Atoi(m[2])
			t, _ = strconv.Atoi(m[3])
			hasNotes = m[4] != ""
		} else if m := powerpointSlideDescEnRe.FindStringSubmatch(desc); m != nil {
			rawTitle = m[1]
			idx, _ = strconv.Atoi(m[2])
			t, _ = strconv.Atoi(m[3])
			hasNotes = m[4] != ""
		} else {
			return true
		}
		if idx <= 0 || seenIdx[idx] {
			return true
		}
		seenIdx[idx] = true
		if t > total {
			total = t
		}
		slides = append(slides, slide{
			idx:      idx,
			title:    strings.TrimSpace(rawTitle),
			hasNotes: hasNotes,
		})
		return true
	})

	if len(slides) == 0 {
		return nil
	}

	activeIdx := 0
	Traverse(window, func(n *AXNode) bool {
		if n == nil || n.Role != "AXLayoutArea" {
			return true
		}
		desc := strings.TrimSpace(n.Description)
		if m := powerpointActiveLayoutZhRe.FindStringSubmatch(desc); m != nil {
			activeIdx, _ = strconv.Atoi(m[1])
			return false
		}
		if m := powerpointActiveLayoutEnRe.FindStringSubmatch(desc); m != nil {
			activeIdx, _ = strconv.Atoi(m[1])
			return false
		}
		return true
	})

	sort.Slice(slides, func(i, j int) bool { return slides[i].idx < slides[j].idx })

	var b strings.Builder
	if activeIdx > 0 && total > 0 {
		fmt.Fprintf(&b, "Slide %d of %d", activeIdx, total)
	} else if total > 0 {
		fmt.Fprintf(&b, "%d slides", total)
	}
	b.WriteString("\n")
	for _, s := range slides {
		t := s.title
		if t == "" {
			t = "(no title)"
		}
		fmt.Fprintf(&b, "\n%d. %s", s.idx, t)
		if s.idx == activeIdx {
			b.WriteString("  [active]")
		}
		if s.hasNotes {
			b.WriteString("  [has notes]")
		}
	}

	meta := map[string]string{
		"bundleId":    bundleID,
		"app":         appName,
		"slidesTotal": strconv.Itoa(total),
		"axCoverage":  "outline_only",
	}
	if activeIdx > 0 {
		meta["activeSlideIndex"] = strconv.Itoa(activeIdx)
		for _, s := range slides {
			if s.idx == activeIdx && s.title != "" {
				meta["activeSlideTitle"] = s.title
				break
			}
		}
	}

	return &ParsedContent{
		ContentType: "presentation",
		Title:       title,
		Body:        strings.TrimSpace(b.String()),
		Metadata:    meta,
	}
}
