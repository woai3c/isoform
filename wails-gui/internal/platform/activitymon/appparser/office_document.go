package appparser

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Native macOS document apps. Word and Pages each expose their document
// body through AXTextArea nodes, but the shapes differ:
//
//   - Word for Mac splits the document into one AXTextArea per page, each
//     tagged with description="页面 N 内容" (zh-CN) / "Page N content"
//     (en). Visiting only the longest one would drop every other page.
//   - Pages exposes a single editable AXTextArea covering the whole
//     document, with no page-tagged description.
//
// We try the page-tagged collection first and fall back to the longest
// AXTextArea so both shapes work.
var officeDocumentBundleIDs = map[string]string{
	"com.microsoft.Word":    "word",
	"com.apple.Pages":       "pages",
	"com.apple.iWork.Pages": "pages",
}

type officeDocumentParser struct{}

func init() { Register(&officeDocumentParser{}) }

func (p *officeDocumentParser) CanParse(bundleID string) bool {
	_, ok := officeDocumentBundleIDs[bundleID]
	return ok
}

func (p *officeDocumentParser) DescribeAll() []ParserCatalogEntry {
	return []ParserCatalogEntry{
		{
			Name:        "office_document_word",
			Label:       "Microsoft Word",
			ContentType: "document",
			Bundles:     []string{"com.microsoft.Word"},
		},
		{
			Name:        "office_document_pages",
			Label:       "Apple Pages",
			ContentType: "document",
			Bundles: []string{
				"com.apple.Pages",
				"com.apple.iWork.Pages",
			},
		},
	}
}

// ParseTree extracts the active document body.
func (p *officeDocumentParser) ParseTree(tree *AXNode, bundleID, windowTitle, _ string) *ParsedContent {
	if tree == nil {
		return nil
	}
	appName, ok := officeDocumentBundleIDs[bundleID]
	if !ok {
		return nil
	}

	window := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWindow" })
	if window == nil {
		window = tree
	}

	body, pages := officeDocCollectPages(window)
	if body == "" {
		body = officeDocPickLongestTextArea(window)
	}
	if strings.TrimSpace(body) == "" {
		// Last-resort fallback: review pane / comments-only views route
		// through AXStaticText instead of AXTextArea.
		body = strings.Join(CollectStaticTexts(window), "\n")
	}
	if strings.TrimSpace(body) == "" {
		return nil
	}

	title := officeStripAppSuffix(windowTitle, appName)
	if title == "" && window != nil {
		title = officeStripAppSuffix(window.Title, appName)
	}

	meta := map[string]string{
		"bundleId": bundleID,
		"app":      appName,
	}
	if pages > 0 {
		meta["pages"] = strconv.Itoa(pages)
	}

	return &ParsedContent{
		ContentType: "document",
		Title:       title,
		Body:        strings.TrimSpace(body),
		Metadata:    meta,
	}
}

// officeDocPageDescRe matches Word's per-page AXTextArea description in
// the locales we have actually seen on macOS. Word renders this as
// "页面 N 内容" (zh-CN) or "Page N content" (en); we capture the page
// number so the output is page-ordered even if the AX tree is not.
var officeDocPageDescRe = regexp.MustCompile(`^\s*(?:页面|Page)\s+(\d+)\s*(?:内容|content)\s*$`)

// officeDocCollectPages returns the document body assembled from
// page-tagged AXTextArea nodes and the number of pages found. When no
// node carries a page-tag description, returns ("", 0) so the caller
// can fall back to the single-canvas Pages shape.
func officeDocCollectPages(root *AXNode) (string, int) {
	type page struct {
		idx int
		val string
	}
	var pages []page
	Traverse(root, func(n *AXNode) bool {
		if n.Role != "AXTextArea" {
			return true
		}
		m := officeDocPageDescRe.FindStringSubmatch(strings.TrimSpace(n.Description))
		if m == nil {
			return true
		}
		idx, err := strconv.Atoi(m[1])
		if err != nil {
			return true
		}
		val := n.Value
		if strings.TrimSpace(val) == "" {
			return true
		}
		pages = append(pages, page{idx: idx, val: val})
		return true
	})
	if len(pages) == 0 {
		return "", 0
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].idx < pages[j].idx })
	var b strings.Builder
	for i, p := range pages {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(strings.TrimRight(p.val, "\n"))
	}
	return b.String(), len(pages)
}

// officeDocPickLongestTextArea returns the longest Value across every
// AXTextArea in the subtree. Used as the Pages fallback when no
// page-tagged AXTextArea is found.
func officeDocPickLongestTextArea(root *AXNode) string {
	var best string
	Traverse(root, func(n *AXNode) bool {
		if n.Role != "AXTextArea" {
			return true
		}
		s := n.Value
		if s == "" {
			s = n.Description
		}
		if len(s) > len(best) {
			best = s
		}
		return true
	})
	return best
}
