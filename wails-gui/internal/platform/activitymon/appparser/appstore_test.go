package appparser

import (
	"strings"
	"testing"
)

// appstoreProductPageTree builds an AXWindow shaped like a real App
// Store product page. The relevant identifiers are exactly what the
// live AX tree emits (verified by ax-tree-dump against Spark Mail and
// Numbers). Callers can vary the badge set to model third-party apps
// (with chartPosition) vs first-party apps (without it).
type appstoreBadgeSpec struct {
	Identifier string
	Value      string
}

func appstoreProductPageTree(appName string, badges []appstoreBadgeSpec) *AXNode {
	productPageChildren := []*AXNode{
		{
			Role:       "AXStaticText",
			Identifier: appstoreTitleIdentifier,
			Value:      appName,
		},
	}
	for _, b := range badges {
		productPageChildren = append(productPageChildren, &AXNode{
			Role:       "AXStaticText",
			Identifier: b.Identifier,
			Value:      b.Value,
		})
	}
	return &AXNode{
		Role:  "AXWindow",
		Title: "App Store",
		Children: []*AXNode{
			{
				Role:       "AXList",
				Identifier: appstoreProductPageIdentifier,
				Children:   productPageChildren,
			},
		},
	}
}

// TestAppStoreParser_ThirdPartyProductPage uses the Spark Mail layout:
// developer is in artwork (single badge), category + chart come from
// the dedicated chartPosition badge.
func TestAppStoreParser_ThirdPartyProductPage(t *testing.T) {
	tree := appstoreProductPageTree("Spark Mail – 智能邮箱和日历", []appstoreBadgeSpec{
		{appstoreBadgeRating, "四又四分之三颗星, 143个评分, 4.6"},
		{appstoreBadgeContentRating, "岁, 年龄, 4+"},
		{appstoreBadgeChartPosition, "效率, 排行榜, 98"},
		{appstoreBadgeArtwork, "Readdle Technologies Limited, 开发者, 自拍"},
		{appstoreBadgeParagraph, "+ 9 种语言, 语言, UK"},
		{appstoreBadgeParagraph, "MB, 大小, 822"},
	})

	parsed := (&appstoreParser{}).ParseTree(tree, "com.apple.AppStore", "App Store", "")
	if parsed == nil {
		t.Fatal("expected non-nil parsed content for product page")
	}
	if parsed.Metadata["view"] != "productPage" {
		t.Errorf("metadata.view = %q, want %q", parsed.Metadata["view"], "productPage")
	}
	checks := map[string]string{
		"appName":       "Spark Mail – 智能邮箱和日历",
		"developer":     "Readdle Technologies Limited",
		"rating":        "4.6",
		"ageRating":     "4+",
		"category":      "效率",
		"chartPosition": "98",
		"size":          "822 MB",
		"languages":     "+ 9 种语言",
	}
	for k, want := range checks {
		if got := parsed.Metadata[k]; got != want {
			t.Errorf("metadata.%s = %q, want %q", k, got, want)
		}
	}
	if parsed.Title != "Spark Mail – 智能邮箱和日历" {
		t.Errorf("title = %q, want app name", parsed.Title)
	}
}

// TestAppStoreParser_FirstPartyProductPage uses the Numbers layout: no
// chartPosition badge, two artwork badges (one for category, one for
// developer). The previous parser misread "效率" as the developer because
// FindFirst grabbed the first artwork badge — this regression test pins
// the label-disambiguated lookup.
func TestAppStoreParser_FirstPartyProductPage(t *testing.T) {
	tree := appstoreProductPageTree("Numbers 表格: 制作电子表格", []appstoreBadgeSpec{
		{appstoreBadgeRating, "五颗星, 4406个评分, 4.8"},
		{appstoreBadgeContentRating, "岁, 年龄, 4+"},
		{appstoreBadgeArtwork, "效率, 类别, 插图"},     // category, not developer
		{appstoreBadgeArtwork, "Apple, 开发者, 自拍"}, // developer
		{appstoreBadgeParagraph, "+ 32 种语言, 语言, DA"},
		{appstoreBadgeParagraph, "MB, 大小, 730.1"},
	})

	parsed := (&appstoreParser{}).ParseTree(tree, "com.apple.AppStore", "App Store", "")
	if parsed == nil {
		t.Fatal("expected non-nil parsed content")
	}
	if parsed.Metadata["developer"] != "Apple" {
		t.Errorf("developer = %q, want %q — artwork badge disambiguation regressed",
			parsed.Metadata["developer"], "Apple")
	}
	if parsed.Metadata["category"] != "效率" {
		t.Errorf("category = %q, want %q — artwork fallback path didn't fire",
			parsed.Metadata["category"], "效率")
	}
	if parsed.Metadata["chartPosition"] != "" {
		t.Errorf("first-party apps with no chart presence should not emit chartPosition, got %q",
			parsed.Metadata["chartPosition"])
	}
	if parsed.Metadata["size"] != "730.1 MB" {
		t.Errorf("size = %q, want %q", parsed.Metadata["size"], "730.1 MB")
	}
}

// TestAppStoreParser_TabLandingFallback covers the case where no
// product page is open — App Store is just sitting on a tab landing
// view. Body is minimal but the metadata tag lets consumers know the
// user is in App Store without misclassifying as "no content".
func TestAppStoreParser_TabLandingFallback(t *testing.T) {
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "App Store",
		Children: []*AXNode{
			{
				Role:       "AXButton",
				Identifier: "AppStore.tabBar.discover",
				Value:      "探索",
			},
			{
				Role:       "AXButton",
				Identifier: "AppStore.tabBar.work",
				Value:      "工作",
			},
			{
				Role:       "AXButton",
				Identifier: "AppStore.tabBar.updates",
				Value:      "更新",
			},
		},
	}
	parsed := (&appstoreParser{}).ParseTree(tree, "com.apple.AppStore", "App Store", "")
	if parsed == nil {
		t.Fatal("expected non-nil parsed content for tab landing")
	}
	if parsed.Metadata["view"] != "tabLanding" {
		t.Errorf("view = %q, want %q", parsed.Metadata["view"], "tabLanding")
	}
	if !strings.Contains(parsed.Body, "browsing the App Store") {
		t.Errorf("tab-landing body should mention App Store browsing:\n%s", parsed.Body)
	}
	for _, want := range []string{"探索", "工作", "更新"} {
		if !strings.Contains(parsed.Body, want) {
			t.Errorf("tab name %q missing from body:\n%s", want, parsed.Body)
		}
	}
}

// TestAppStoreParser_ReturnsNilForUnknownSurface guards against the
// parser leaking output when the user is in a sheet/dialog we don't
// recognize. The dispatcher relies on nil here to fall back to AX text.
func TestAppStoreParser_ReturnsNilForUnknownSurface(t *testing.T) {
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "App Store",
		Children: []*AXNode{
			{Role: "AXStaticText", Value: "loading"},
		},
	}
	parsed := (&appstoreParser{}).ParseTree(tree, "com.apple.AppStore", "App Store", "")
	if parsed != nil {
		t.Errorf("expected nil for unrecognized surface, got %+v", parsed)
	}
}

// TestAppStoreExtractParagraphs locks the paragraph badge disambiguation
// rule in place. Both badges share the same identifier so locale-keyword
// matching is the only signal we have.
func TestAppStoreExtractParagraphs(t *testing.T) {
	root := &AXNode{
		Role: "AXGroup",
		Children: []*AXNode{
			{
				Role:       "AXStaticText",
				Identifier: appstoreBadgeParagraph,
				Value:      "MB, 大小, 514.2",
			},
			{
				Role:       "AXStaticText",
				Identifier: appstoreBadgeParagraph,
				Value:      "+ 5 种语言, 语言, ZH",
			},
		},
	}
	languages, size := appstoreExtractParagraphs(root)
	if languages != "+ 5 种语言" {
		t.Errorf("languages = %q, want %q", languages, "+ 5 种语言")
	}
	if size != "514.2 MB" {
		t.Errorf("size = %q, want %q (unit + number reorder)", size, "514.2 MB")
	}
}

// TestAppStoreExtractBadgeValue locks the "last comma-separated segment"
// rule for cleanest user-visible value extraction.
func TestAppStoreExtractBadgeValue(t *testing.T) {
	root := &AXNode{
		Role: "AXGroup",
		Children: []*AXNode{
			{
				Role:       "AXStaticText",
				Identifier: appstoreBadgeRating,
				Value:      "五颗星, 4406个评分, 4.8",
			},
		},
	}
	if got := appstoreExtractBadgeValue(root, appstoreBadgeRating); got != "4.8" {
		t.Errorf("rating = %q, want %q", got, "4.8")
	}
}
