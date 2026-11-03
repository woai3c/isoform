package appparser

import "strings"

// MainLandmarkText extracts the visible text of the AXMain landmark
// (i.e. the page's `<main>` element), skipping common chrome landmarks
// — navigation, banner/header, contentinfo/footer, complementary/aside,
// and search forms — that show up around the actual content on most
// modern web apps.
//
// Web parsers should call this as a last-resort body extractor before
// returning nil, so a tool we recognize by URL still produces useful
// content even when its specific class-based selectors miss.
//
// Returns "" when no AXMain landmark exists; callers can then fall
// through to the generic browser chrome stripper.
func MainLandmarkText(root *AXNode) string {
	if root == nil {
		return ""
	}
	main := FindFirst(root, isMainLandmark)
	if main == nil {
		return ""
	}
	var lines []string
	Traverse(main, func(n *AXNode) bool {
		if n.Role == "AXScrollBar" {
			return false
		}
		if isChromeLandmark(n) {
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

// isMainLandmark reports whether the node is the page's main content
// landmark. WebKit / Blink expose `<main>` as AXLandmark with
// AXLandmarkMain subrole; older builds may use Group with role="main".
func isMainLandmark(n *AXNode) bool {
	if n == nil {
		return false
	}
	if n.Role == "AXLandmarkMain" {
		return true
	}
	if n.Subrole == "AXLandmarkMain" {
		return true
	}
	if n.Identifier == "main" {
		return true
	}
	return false
}

// isChromeLandmark reports whether the node is a chrome landmark we
// want to skip when extracting body content. Matches navigation,
// banner/header, contentinfo/footer, complementary/aside, and the
// search form landmark — all of which sit around but never inside
// the actual page content.
func isChromeLandmark(n *AXNode) bool {
	if n == nil {
		return false
	}
	switch n.Role {
	case "AXLandmarkNavigation",
		"AXLandmarkBanner",
		"AXLandmarkContentInfo",
		"AXLandmarkComplementary",
		"AXLandmarkSearch":
		return true
	}
	switch n.Subrole {
	case "AXLandmarkNavigation",
		"AXLandmarkBanner",
		"AXLandmarkContentInfo",
		"AXLandmarkComplementary",
		"AXLandmarkSearch":
		return true
	}
	return false
}
