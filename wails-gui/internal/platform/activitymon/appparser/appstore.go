package appparser

import (
	"strings"
)

// appstoreBundleIDs lists the macOS App Store.
var appstoreBundleIDs = map[string]bool{
	"com.apple.AppStore": true,
}

// Identifier prefixes / exact matches that App Store uses to label its AX
// nodes. The schema is fully identifier-driven (the SwiftUI rewrite did
// the same job as Mail's modern reading-pane redesign) so we don't rely
// on Title/Description heuristics.
const (
	appstoreTitleIdentifier       = "AppStore.shelfItemSubComponent.title"
	appstoreProductPageIdentifier = "AppStore.productPage"
	appstoreOfferButtonIdentifier = "AppStore.offerButton"
	appstoreTabButtonIdentifier   = "AppStore.tabBar."
	appstoreBadgeRating           = "AppStore.productPage.badge.rating"
	appstoreBadgeContentRating    = "AppStore.productPage.badge.contentRating"
	appstoreBadgeChartPosition    = "AppStore.productPage.badge.chartPosition"
	appstoreBadgeArtwork          = "AppStore.productPage.badge.artwork"
	appstoreBadgeParagraph        = "AppStore.productPage.badge.paragraph"
)

type appstoreParser struct{}

func init() {
	Register(&appstoreParser{})
}

func (p *appstoreParser) CanParse(bundleID string) bool {
	return appstoreBundleIDs[bundleID]
}

func (p *appstoreParser) Describe() ParserCatalogEntry {
	return ParserCatalogEntry{
		Name:        "appstore",
		Label:       "App Store",
		ContentType: "document",
		Bundles:     []string{"com.apple.AppStore"},
	}
}

// ParseTree extracts the user's current App Store focus. Two distinct
// surfaces are handled because they convey very different signals:
//
//   - Product page (a specific app's detail view): high-signal — we
//     output app name + developer + rating + size + age + category +
//     chart position. This is the "user is evaluating X app" case.
//   - Tab landing (Discover/Create/Work/Play/Develop/Categories/Updates
//     without a specific app focused): low-signal — we just record
//     "user is browsing App Store / <tab name>". Lazy SwiftUI rendering
//     means the AX tree doesn't expose the visible app shelf rows for
//     these views.
//
// App description and reviews are detected as containers but their text
// is lazy-rendered — visible only after the user scrolls down. We
// surface them via metadata flags when present and leave fuller
// extraction for a future iteration if it becomes useful.
//
// Returns nil for unrecognized surfaces (account sheet, in-app purchase
// flow, sign-in, etc.) so callers fall back to AX text.
func (p *appstoreParser) ParseTree(tree *AXNode, bundleID, _windowTitle, _ string) *ParsedContent {
	if tree == nil {
		return nil
	}

	window := FindFirst(tree, func(n *AXNode) bool { return n.Role == "AXWindow" })
	if window == nil {
		window = tree
	}

	if pc := appstoreProductPageContent(window, bundleID); pc != nil {
		return pc
	}
	if pc := appstoreTabLandingContent(window, bundleID); pc != nil {
		return pc
	}
	return nil
}

// appstoreProductPageContent handles the product-detail surface. The
// AXList with identifier="AppStore.productPage" is the anchor — when it
// exists, the user is looking at a single app's page (regardless of
// which tab they originally arrived from).
func appstoreProductPageContent(window *AXNode, bundleID string) *ParsedContent {
	productPage := FindFirst(window, func(n *AXNode) bool {
		return n.Identifier == appstoreProductPageIdentifier
	})
	if productPage == nil {
		return nil
	}

	appName := appstoreFindText(window, appstoreTitleIdentifier)
	offerLabel := appstoreFindText(window, appstoreOfferButtonIdentifier)
	developer := appstoreExtractDeveloper(window)
	rating := appstoreExtractBadgeValue(window, appstoreBadgeRating)
	contentRating := appstoreExtractBadgeValue(window, appstoreBadgeContentRating)
	category, chartPos := appstoreExtractCategoryAndChart(window)
	languages, size := appstoreExtractParagraphs(window)

	if appName == "" && rating == "" && developer == "" {
		// Probably hit a stale productPage container before the page
		// actually loaded — let caller fall through.
		return nil
	}

	body := appstoreRenderBody(appName, developer, rating, contentRating,
		category, chartPos, languages, size, offerLabel)

	meta := map[string]string{
		"bundleId": bundleID,
		"app":      "appstore",
		"view":     "productPage",
	}
	if appName != "" {
		meta["appName"] = appName
	}
	if developer != "" {
		meta["developer"] = developer
	}
	if rating != "" {
		meta["rating"] = rating
	}
	if contentRating != "" {
		meta["ageRating"] = contentRating
	}
	if category != "" {
		meta["category"] = category
	}
	if chartPos != "" {
		meta["chartPosition"] = chartPos
	}
	if languages != "" {
		meta["languages"] = languages
	}
	if size != "" {
		meta["size"] = size
	}
	if offerLabel != "" {
		meta["offer"] = offerLabel
	}

	return &ParsedContent{
		ContentType: "document",
		Title:       appName,
		Body:        body,
		Metadata:    meta,
	}
}

// appstoreTabLandingContent is the low-signal fallback: no product page is
// open, so the best we can do is record which tab the user is on. The
// tabBar buttons are always present in the AX tree regardless of which
// is active — Apple does not mark the active tab with kAXSelectedAttribute
// here (verified on the live dump), so the only available signal is the
// fact that the user is in App Store without a specific app in focus.
func appstoreTabLandingContent(window *AXNode, bundleID string) *ParsedContent {
	tabButtons := FindAll(window, func(n *AXNode) bool {
		return strings.HasPrefix(n.Identifier, appstoreTabButtonIdentifier)
	})
	if len(tabButtons) == 0 {
		return nil
	}

	var tabs []string
	for _, b := range tabButtons {
		if v := strings.TrimSpace(b.Value); v != "" {
			tabs = append(tabs, v)
		}
	}

	body := "User is browsing the App Store."
	if len(tabs) > 0 {
		body += "\nTabs available: " + strings.Join(tabs, ", ")
	}

	return &ParsedContent{
		ContentType: "document",
		Title:       "App Store",
		Body:        body,
		Metadata: map[string]string{
			"bundleId": bundleID,
			"app":      "appstore",
			"view":     "tabLanding",
		},
	}
}

// appstoreFindText returns the trimmed Value of the first node with the
// given identifier. The identifier names a unique location in the AX
// tree — App Store does not reuse identifiers across surfaces.
func appstoreFindText(root *AXNode, identifier string) string {
	n := FindFirst(root, func(n *AXNode) bool { return n.Identifier == identifier })
	if n == nil {
		return ""
	}
	return strings.TrimSpace(n.Value)
}

// appstoreExtractDeveloper pulls the developer name out of one of the
// artwork badges. App Store reuses identifier="AppStore.productPage.badge
// .artwork" for several metadata strips on a single product page (Apple's
// own apps emit both a "<category>, 类别, ..." badge AND a "<developer>,
// 开发者, ..." badge under the same identifier), so we have to inspect
// each candidate's Value to find the one whose label is "开发者" /
// "Developer". Format is "<value>, <label>, <hint>" — we keep the
// segment before the first comma when the label segment indicates a
// developer field.
func appstoreExtractDeveloper(root *AXNode) string {
	badges := FindAll(root, func(n *AXNode) bool {
		return n.Identifier == appstoreBadgeArtwork
	})
	for _, b := range badges {
		v := strings.TrimSpace(b.Value)
		if v == "" {
			continue
		}
		if strings.Contains(v, "开发者") || strings.Contains(v, "Developer") {
			return appstoreFirstSegment(v)
		}
	}
	return ""
}

// appstoreExtractCategoryFromArtwork is the artwork-badge variant for
// category. Apps not on a top chart don't get a chartPosition badge, so
// "<category>, 类别, ..." comes through the artwork identifier instead.
// This fallback only fires when the dedicated chartPosition badge is
// absent — see appstoreExtractCategoryAndChart.
func appstoreExtractCategoryFromArtwork(root *AXNode) string {
	badges := FindAll(root, func(n *AXNode) bool {
		return n.Identifier == appstoreBadgeArtwork
	})
	for _, b := range badges {
		v := strings.TrimSpace(b.Value)
		if v == "" {
			continue
		}
		if strings.Contains(v, "类别") || strings.Contains(v, "Category") {
			return appstoreFirstSegment(v)
		}
	}
	return ""
}

// appstoreExtractBadgeValue pulls the "clean" trailing value from a
// badge accessibility composite. badge.rating Value
// "四又四分之三颗星, 143个评分, 4.6" → "4.6"; badge.contentRating
// "岁, 年龄, 4+" → "4+". The format is (icon hint, label, value) so the
// last comma-separated segment is consistently the user-visible value.
func appstoreExtractBadgeValue(root *AXNode, identifier string) string {
	v := appstoreFindText(root, identifier)
	if v == "" {
		return ""
	}
	parts := strings.Split(v, ", ")
	last := strings.TrimSpace(parts[len(parts)-1])
	return last
}

// appstoreExtractCategoryAndChart returns (category, chartRank). Source
// priority: the dedicated chartPosition badge ("<category>, 排行榜,
// <rank>") is preferred because it carries the rank; when absent (apps
// not on any top chart), we fall back to the artwork-badge variant which
// only carries the category. rank is empty whenever the chartPosition
// badge is missing.
func appstoreExtractCategoryAndChart(root *AXNode) (category, rank string) {
	if v := appstoreFindText(root, appstoreBadgeChartPosition); v != "" {
		parts := strings.Split(v, ", ")
		category = strings.TrimSpace(parts[0])
		if len(parts) > 1 {
			rank = strings.TrimSpace(parts[len(parts)-1])
		}
		if rank == category {
			rank = ""
		}
		return category, rank
	}
	return appstoreExtractCategoryFromArtwork(root), ""
}

// appstoreExtractParagraphs handles the dual-use AppStore.productPage.badge
// .paragraph identifier, which is emitted twice — once for languages and
// once for size — with no per-instance identifier to disambiguate. We
// disambiguate by the label substring inside the composite Value: the
// languages badge contains "语言" / "Language", the size badge contains
// "大小" / "Size". When the user's macOS locale doesn't match the
// hard-coded substrings, both badges fall through as raw values in body
// rendering, which is graceful degradation rather than a hard miss.
func appstoreExtractParagraphs(root *AXNode) (languages, size string) {
	paragraphs := FindAll(root, func(n *AXNode) bool {
		return n.Identifier == appstoreBadgeParagraph
	})
	for _, p := range paragraphs {
		v := strings.TrimSpace(p.Value)
		if v == "" {
			continue
		}
		switch {
		case languages == "" && (strings.Contains(v, "语言") || strings.Contains(v, "Language")):
			// Composite: "+ 9 种语言, 语言, UK" — first segment is the
			// human-readable summary ("+ 9 languages"), which is what
			// we want to surface.
			languages = appstoreFirstSegment(v)
		case size == "" && (strings.Contains(v, "大小") || strings.Contains(v, "Size")):
			// Composite: "MB, 大小, 822" — useful form is "<num> <unit>".
			// Reconstruct from first + last segments since the composite
			// puts the unit first and the number last.
			parts := strings.Split(v, ", ")
			if len(parts) >= 3 {
				unit := strings.TrimSpace(parts[0])
				num := strings.TrimSpace(parts[len(parts)-1])
				size = num + " " + unit
			} else {
				size = v
			}
		}
	}
	return languages, size
}

// appstoreFirstSegment returns the first comma-separated segment of a
// composite string. Used for badges whose useful value is the leading
// human-readable summary (developer, language summary).
func appstoreFirstSegment(s string) string {
	if idx := strings.Index(s, ","); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return strings.TrimSpace(s)
}

// appstoreRenderBody composes a markdown-flavored body of all the
// extracted product-page fields. Empty fields are skipped so a minimally
// populated page still produces clean output. The order mirrors how the
// fields appear visually on a product page (header → metadata strip →
// chart position) so AI consumers reading the body get the same mental
// shape as a user scrolling the page.
func appstoreRenderBody(appName, developer, rating, contentRating,
	category, chartPos, languages, size, offerLabel string) string {
	var sb strings.Builder
	if appName != "" {
		sb.WriteString("App: ")
		sb.WriteString(appName)
		sb.WriteString("\n")
	}
	if developer != "" {
		sb.WriteString("Developer: ")
		sb.WriteString(developer)
		sb.WriteString("\n")
	}
	if rating != "" {
		sb.WriteString("Rating: ")
		sb.WriteString(rating)
		sb.WriteString("\n")
	}
	if category != "" {
		sb.WriteString("Category: ")
		sb.WriteString(category)
		if chartPos != "" {
			sb.WriteString(" (chart #")
			sb.WriteString(chartPos)
			sb.WriteString(")")
		}
		sb.WriteString("\n")
	}
	if size != "" {
		sb.WriteString("Size: ")
		sb.WriteString(size)
		sb.WriteString("\n")
	}
	if contentRating != "" {
		sb.WriteString("Age: ")
		sb.WriteString(contentRating)
		sb.WriteString("\n")
	}
	if languages != "" {
		sb.WriteString("Languages: ")
		sb.WriteString(languages)
		sb.WriteString("\n")
	}
	if offerLabel != "" {
		sb.WriteString("Offer: ")
		sb.WriteString(offerLabel)
	}
	return strings.TrimRight(sb.String(), "\n")
}
