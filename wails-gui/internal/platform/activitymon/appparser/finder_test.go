package appparser

import (
	"strings"
	"testing"
)

// finderListViewTree builds an AXWindow containing a Finder List view with
// the provided files. Each entry marks whether the row is selected. The
// shape mirrors the real Finder AX layout: AXOutline[identifier=ListView]
// → AXRow[subrole=AXOutlineRow] → AXCell → AXTextField.
func finderListViewTree(windowTitle string, files []struct {
	Name     string
	Selected bool
}) *AXNode {
	var rows []*AXNode
	for _, f := range files {
		rows = append(rows, &AXNode{
			Role:     "AXRow",
			Subrole:  "AXOutlineRow",
			Selected: f.Selected,
			Children: []*AXNode{
				{
					Role: "AXCell",
					Children: []*AXNode{
						{Role: "AXTextField", Value: f.Name},
					},
				},
			},
		})
	}
	return &AXNode{
		Role:  "AXWindow",
		Title: windowTitle,
		Children: []*AXNode{
			{
				Role:       "AXOutline",
				Identifier: "ListView",
				Children:   rows,
			},
		},
	}
}

func TestFinderParser_ListViewExtractsSelectedFiles(t *testing.T) {
	tree := finderListViewTree("下载", []struct {
		Name     string
		Selected bool
	}{
		{"pd-resume.md", true},
		{"sample.csv", false},
		{"installer.dmg", true},
		{"readme.txt", false},
	})

	parsed := (&finderParser{}).ParseTree(tree, "com.apple.finder", "下载", "")
	if parsed == nil {
		t.Fatal("expected non-nil parsed content for List view")
	}
	if parsed.Title != "下载" {
		t.Errorf("title = %q, want %q", parsed.Title, "下载")
	}
	if parsed.Metadata["view"] != "list" {
		t.Errorf("metadata.view = %q, want %q", parsed.Metadata["view"], "list")
	}
	if parsed.Metadata["selectedCount"] != "2" {
		t.Errorf("metadata.selectedCount = %q, want %q", parsed.Metadata["selectedCount"], "2")
	}
	if parsed.Metadata["visibleFileCount"] != "4" {
		t.Errorf("metadata.visibleFileCount = %q, want %q",
			parsed.Metadata["visibleFileCount"], "4")
	}
	for _, want := range []string{"pd-resume.md", "installer.dmg"} {
		if !strings.Contains(parsed.Body, want) {
			t.Errorf("body missing selected file %q\n--- body ---\n%s", want, parsed.Body)
		}
	}
	// When something is selected the non-selected entries should NOT show
	// up — the selection is the load-bearing signal and the rest is noise.
	if strings.Contains(parsed.Body, "sample.csv") || strings.Contains(parsed.Body, "readme.txt") {
		t.Errorf("non-selected files leaked into body while selection was present:\n%s",
			parsed.Body)
	}
}

func TestFinderParser_ListViewBrowsingShowsAllFiles(t *testing.T) {
	// Nothing selected: parser should fall back to listing the directory
	// so the AI knows what's in the folder.
	tree := finderListViewTree("文稿", []struct {
		Name     string
		Selected bool
	}{
		{"report.pdf", false},
		{"notes.md", false},
		{"draft.txt", false},
	})

	parsed := (&finderParser{}).ParseTree(tree, "com.apple.finder", "文稿", "")
	if parsed == nil {
		t.Fatal("expected non-nil parsed content")
	}
	if parsed.Metadata["selectedCount"] != "" {
		t.Errorf("selectedCount should be empty, got %q", parsed.Metadata["selectedCount"])
	}
	for _, want := range []string{"report.pdf", "notes.md", "draft.txt"} {
		if !strings.Contains(parsed.Body, want) {
			t.Errorf("browse-mode body missing %q:\n%s", want, parsed.Body)
		}
	}
}

func TestFinderParser_SidebarLocationDoesNotMasqueradeAsFile(t *testing.T) {
	// The Finder sidebar marks the current location's row Selected (so
	// VoiceOver can announce "you're in Downloads"). That same role
	// (AXRow[AXOutlineRow][selected]) would also describe a selected
	// file row in the main list, so naive walks pick up "下载" as a file.
	// Anchoring under AXOutline[identifier=ListView] is the guardrail —
	// the sidebar uses a different identifier (usually "_NS:8").
	sidebar := &AXNode{
		Role:       "AXOutline",
		Identifier: "_NS:8",
		Children: []*AXNode{
			{
				Role:     "AXRow",
				Subrole:  "AXOutlineRow",
				Selected: true,
				Children: []*AXNode{
					{
						Role: "AXCell",
						Children: []*AXNode{
							{Role: "AXStaticText", Value: "下载"},
						},
					},
				},
			},
		},
	}
	fileList := &AXNode{
		Role:       "AXOutline",
		Identifier: "ListView",
		Children: []*AXNode{
			{
				Role:     "AXRow",
				Subrole:  "AXOutlineRow",
				Selected: true,
				Children: []*AXNode{
					{
						Role: "AXCell",
						Children: []*AXNode{
							{Role: "AXTextField", Value: "actual-file.pdf"},
						},
					},
				},
			},
		},
	}
	tree := &AXNode{
		Role:     "AXWindow",
		Title:    "下载",
		Children: []*AXNode{sidebar, fileList},
	}

	parsed := (&finderParser{}).ParseTree(tree, "com.apple.finder", "下载", "")
	if parsed == nil {
		t.Fatal("expected non-nil parsed content")
	}
	if !strings.Contains(parsed.Body, "actual-file.pdf") {
		t.Errorf("expected real file in body:\n%s", parsed.Body)
	}
	// The sidebar's "下载" must NOT appear as a "Selected" entry — only
	// as the directory header.
	if strings.Contains(parsed.Body, "Selected (") &&
		strings.Contains(parsed.Body, "- 下载\n") {
		t.Errorf("sidebar location leaked into selected files:\n%s", parsed.Body)
	}
}

func TestFinderParser_IconViewExtractsSelectedGroups(t *testing.T) {
	// Icon view uses AXList[identifier=IconView] containing AXGroups
	// whose Identifier is the filename — a different shape from List
	// view's AXOutline / AXTextField path.
	makeGroup := func(name string, selected bool) *AXNode {
		return &AXNode{
			Role:       "AXGroup",
			Identifier: name,
			Selected:   selected,
			Children: []*AXNode{
				{Role: "AXImage", Title: name},
			},
		}
	}
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "图片",
		Children: []*AXNode{
			{
				Role:       "AXList",
				Identifier: "IconView",
				Subrole:    "AXCollectionList",
				Children: []*AXNode{
					makeGroup("photo1.jpg", false),
					makeGroup("photo2.jpg", true),
					makeGroup("photo3.jpg", false),
				},
			},
		},
	}
	parsed := (&finderParser{}).ParseTree(tree, "com.apple.finder", "图片", "")
	if parsed == nil {
		t.Fatal("expected non-nil parsed content for Icon view")
	}
	if parsed.Metadata["view"] != "icon" {
		t.Errorf("metadata.view = %q, want %q", parsed.Metadata["view"], "icon")
	}
	if !strings.Contains(parsed.Body, "photo2.jpg") {
		t.Errorf("selected icon-view file missing:\n%s", parsed.Body)
	}
	if strings.Contains(parsed.Body, "photo1.jpg") || strings.Contains(parsed.Body, "photo3.jpg") {
		t.Errorf("non-selected icons leaked into body:\n%s", parsed.Body)
	}
}

func TestFinderParser_ColumnViewDegradesToStatusBar(t *testing.T) {
	// Column view exposes no per-file AX nodes (macOS 26 SwiftUI rewrite)
	// — only the status bar tells us anything about the selection. The
	// parser should still return a useful ParsedContent rather than nil,
	// so consumers can record "user is in Finder Column view".
	tree := &AXNode{
		Role:  "AXWindow",
		Title: "下载",
		Children: []*AXNode{
			{
				Role:       "AXBrowser",
				Identifier: "ColumnView",
				Children: []*AXNode{
					{Role: "AXScrollArea"},
				},
			},
			// Status bar lives several levels below the window in the
			// real tree — verify the parser still finds it via the
			// full-tree scan.
			{
				Role: "AXGroup",
				Children: []*AXNode{
					{
						Role: "AXGroup",
						Children: []*AXNode{
							{Role: "AXStaticText", Value: "选择了2项（共290项），158.22 GB可用"},
						},
					},
				},
			},
		},
	}

	parsed := (&finderParser{}).ParseTree(tree, "com.apple.finder", "下载", "")
	if parsed == nil {
		t.Fatal("expected non-nil parsed content for Column view")
	}
	if parsed.Metadata["view"] != "column" {
		t.Errorf("metadata.view = %q, want %q", parsed.Metadata["view"], "column")
	}
	if !strings.Contains(parsed.Body, "选择了2项") {
		t.Errorf("status bar text missing from column-view body:\n%s", parsed.Body)
	}
	if !strings.Contains(parsed.Body, "Column view") {
		t.Errorf("column-view caveat missing — AI consumer should know files weren't extractable:\n%s",
			parsed.Body)
	}
}

func TestFinderParser_BrowseListIsCapped(t *testing.T) {
	// Long directories must not flood the body with hundreds of filenames;
	// the cap keeps prompt size bounded. Selected files (load-bearing
	// signal) ignore the cap, but that's a separate path.
	var files []struct {
		Name     string
		Selected bool
	}
	for i := range finderMaxBrowseListed + 20 {
		files = append(files, struct {
			Name     string
			Selected bool
		}{Name: "f" + string(rune('A'+i%26)) + "_" + itoa(i) + ".txt"})
	}
	tree := finderListViewTree("Downloads", files)
	parsed := (&finderParser{}).ParseTree(tree, "com.apple.finder", "Downloads", "")
	if parsed == nil {
		t.Fatal("expected non-nil parsed content")
	}
	// Count actual file lines: "\n- " followed by a non-ellipsis character.
	// The truncation indicator "\n- …\n" also starts with "\n- " but its
	// next byte is the multi-byte UTF-8 prefix of "…", so a check for any
	// of [a-zA-Z0-9] right after "\n- " excludes the indicator without
	// being locale-fragile.
	fileLines := 0
	for i := 0; i+3 < len(parsed.Body); i++ {
		if parsed.Body[i] == '\n' && parsed.Body[i+1] == '-' && parsed.Body[i+2] == ' ' {
			c := parsed.Body[i+3]
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
				(c >= '0' && c <= '9') {
				fileLines++
			}
		}
	}
	if fileLines > finderMaxBrowseListed {
		t.Errorf("body listed %d files, expected <= %d", fileLines, finderMaxBrowseListed)
	}
	if !strings.Contains(parsed.Body, "shown of") {
		t.Errorf("missing truncation indicator in browse body:\n%s", parsed.Body)
	}
}

// itoa is a local helper since strconv.Itoa would force an import only used
// by one test case; kept hyper-local so the file stays self-contained.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
