package recentfiles

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseItemsMapsSpotlightFields(t *testing.T) {
	data := []byte(`[
		{"path":"/Users/sam/Documents/Q3 deck.key","name":"Q3 deck.key","type":"com.apple.iwork.keynote.sffkey",
		 "tree":["com.apple.iwork.keynote.sffkey","com.apple.package","public.presentation"],
		 "lastUsed":1760400000000,"modified":1760403600000,"created":1760000000000,"added":0},
		{"path":"","name":"no path"},
		{"path":"/Users/sam/Desktop/notes.txt","name":"","type":"public.plain-text","tree":null,"lastUsed":0,"modified":0,"created":0}
	]`)
	items, err := ParseItems(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2 (the entry without a path is dropped)", len(items))
	}
	deck := items[0]
	if deck.Name != "Q3 deck.key" || deck.ContentType != "com.apple.iwork.keynote.sffkey" || len(deck.TypeTree) != 3 {
		t.Fatalf("deck = %+v", deck)
	}
	if !deck.LastUsed.Equal(time.UnixMilli(1760400000000)) || !deck.Modified.Equal(time.UnixMilli(1760403600000)) {
		t.Fatalf("dates = %v %v", deck.LastUsed, deck.Modified)
	}
	if !deck.Added.IsZero() {
		t.Fatalf("a zero date must stay zero, got %v", deck.Added)
	}
	if items[1].Name != "notes.txt" {
		t.Fatalf("a missing name falls back to the path's last element, got %q", items[1].Name)
	}
	if !items[1].LastUsed.IsZero() || !items[1].Modified.IsZero() {
		t.Fatalf("missing dates must be zero: %+v", items[1])
	}
	if _, err := ParseItems([]byte(`{`)); err == nil {
		t.Fatal("broken JSON must fail")
	}
}

func TestRecentQueryUsesUTCISODates(t *testing.T) {
	since := time.Date(2026, 10, 1, 9, 30, 0, 0, time.FixedZone("PDT", -7*3600))
	got := RecentQuery(since)
	want := `kMDItemLastUsedDate >= $time.iso(2026-10-01T16:30:00Z) || kMDItemContentModificationDate >= $time.iso(2026-10-01T16:30:00Z)`
	if got != want {
		t.Fatalf("query = %s", got)
	}
}

func TestRootOf(t *testing.T) {
	roots := Roots("/Users/sam")
	cases := []struct {
		path string
		key  string
		rel  string
		ok   bool
	}{
		{"/Users/sam/Documents/Plan.pages", RootDocuments, "Plan.pages", true},
		{"/Users/sam/documents/Work/Plan.pages", RootDocuments, "Work/Plan.pages", true},
		{"/Users/sam/Desktop/a.txt", RootDesktop, "a.txt", true},
		{"/Users/sam/Downloads/x.pdf", RootDownloads, "x.pdf", true},
		{"/Users/sam/Library/Mobile Documents/com~apple~CloudDocs/Taxes/2026.numbers", RootICloud, "Taxes/2026.numbers", true},
		{"/Users/sam/DocumentsOld/a.txt", "", "", false},
		{"/Users/sam/Documents", "", "", false},
		{"/Users/sam/Pictures/a.png", "", "", false},
	}
	for _, c := range cases {
		root, rel, ok := RootOf(roots, c.path)
		if ok != c.ok || root.Key != c.key || rel != c.rel {
			t.Errorf("RootOf(%q) = %q %q %v, want %q %q %v", c.path, root.Key, rel, ok, c.key, c.rel, c.ok)
		}
	}
}

func TestSkipped(t *testing.T) {
	doc := []string{"public.composite-content", "public.content", "public.item"}
	pages := []string{"com.apple.iwork.pages.sffpages", "com.apple.package", "public.directory", "public.item"}
	cases := []struct {
		rel  string
		tree []string
		want bool
	}{
		{"Plan.docx", doc, false},
		{"Work/Plan.docx", doc, false},
		{"Report.pages", pages, false},
		{".hidden.txt", doc, true},
		{"Work/.git/HEAD", doc, true},
		{"app/node_modules/x/readme.md", doc, true},
		{"Library/Prefs/x.plist", doc, true},
		{"Project/Caches/blob.dat", doc, true},
		{"Project/DerivedData/x.o", doc, true},
		{"Clients/Target/proposal.docx", doc, false},
		{"Build/plan.docx", doc, false},
		// Build output rebuilt in place is never "edited" work.
		{"Code/site/dist/app.js", doc, true},
		{"Code/site/build/index.html", doc, true},
		{"Code/tool/target/debug/tool.d", doc, true},
		{"Code/c/main.o", doc, true},
		{"Code/java/Main.class", doc, true},
		{"Code/site/app.js.map", doc, true},
		{"Code/c/main.c", doc, false},
		{"Models/teapot.obj", doc, false},
		{"Photos Library.photoslibrary/originals/a.jpg", doc, true},
		{"Tool.app/Contents/Info.plist", doc, true},
		{"~$Plan.docx", doc, true},
		{"Folder", []string{"public.folder", "public.directory", "public.item"}, true},
		{"Plain dir", []string{"public.directory", "public.item"}, true},
		{"Tool.app", []string{"com.apple.application-bundle", "com.apple.package"}, true},
		{"video.mp4.crdownload", doc, true},
		{"file.download", doc, true},
		{"", doc, true},
		{"../escape.txt", doc, true},
	}
	for _, c := range cases {
		if got := Skipped(c.rel, c.tree); got != c.want {
			t.Errorf("Skipped(%q) = %v, want %v", c.rel, got, c.want)
		}
	}
}

func TestSecretName(t *testing.T) {
	cases := map[string]bool{
		"verification code 482913.txt":    true,
		"verification_code_482913.txt":    true,
		"verification-code-482913.txt":    true,
		"verification__code__482_913.txt": true,
		"verification_report_2026.txt":    false,
		"2410.14224v1.pdf":                false,
		"Report 2026-10-17.pdf":           false,
		"Passwords.csv":                   true,
		"my_passwords_export.csv":         true,
		"bank-PIN.txt":                    true,
		"recovery phrase.txt":             true,
		"seed-words.rtf":                  true,
		"id_rsa":                          true,
		"vault.kdbx":                      true,
		"export.1pux":                     true,
		"login.keychain-db":               true,
		"server.pem":                      true,
		"aws credentials.txt":             true,
		"api-key.txt":                     true,
		"银行密码.txt":                        true,
		"Q3 deck.key":                     false,
		"Quarterly plan.pages":            false,
		"Passage to India.pdf":            false,
		"Spinach recipe.docx":             false,
		"Opinion piece draft.docx":        false,
		"Design tokens.fig":               false,
		"Pinterest pins.txt":              false,
		"github-access-token.txt":         true,
	}
	for name, want := range cases {
		if got := SecretName(name); got != want {
			t.Errorf("SecretName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestKind(t *testing.T) {
	cases := []struct {
		typ  string
		tree []string
		want string
	}{
		{"com.adobe.pdf", []string{"com.adobe.pdf", "public.data"}, KindPDF},
		{"com.apple.iwork.keynote.sffkey", []string{"com.apple.iwork.keynote.sffkey", "public.presentation"}, KindPresentation},
		{"org.openxmlformats.spreadsheetml.sheet", []string{"public.spreadsheet"}, KindSpreadsheet},
		{"public.png", []string{"public.png", "public.image"}, KindImage},
		{"public.swift-source", []string{"public.swift-source", "public.source-code", "public.plain-text"}, KindCode},
		{"org.openxmlformats.wordprocessingml.document", []string{"org.openxmlformats.wordprocessingml.document"}, KindDocument},
		{"com.apple.iwork.pages.sffpages", []string{"com.apple.iwork.pages.sffpages", "com.apple.package"}, KindDocument},
		{"net.daringfireball.markdown", []string{"net.daringfireball.markdown", "public.plain-text", "public.text"}, KindText},
		{"public.zip-archive", []string{"public.zip-archive", "public.data"}, KindOther},
	}
	for _, c := range cases {
		if got := Kind(c.typ, c.tree); got != c.want {
			t.Errorf("Kind(%q) = %q, want %q", c.typ, got, c.want)
		}
	}
}

func TestActivity(t *testing.T) {
	base := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		item   Item
		root   string
		at     time.Time
		action string
		ok     bool
	}{
		{"edited long after it was created", Item{Created: base, Modified: base.Add(2 * time.Hour)}, RootDocuments, base.Add(2 * time.Hour), ActionEdited, true},
		{"opened later than the last edit", Item{Created: base, Modified: base.Add(time.Hour), LastUsed: base.Add(3 * time.Hour)}, RootDocuments, base.Add(3 * time.Hour), ActionOpened, true},
		{"edit right after arriving is not work", Item{Created: base, Modified: base.Add(20 * time.Second)}, RootDesktop, time.Time{}, "", false},
		{"copied in recently, old content date", Item{Created: base.Add(-48 * time.Hour), Added: base, Modified: base.Add(30 * time.Second)}, RootDocuments, time.Time{}, "", false},
		{"download counts only when opened", Item{Created: base, Modified: base.Add(10 * time.Minute)}, RootDownloads, time.Time{}, "", false},
		{"download opened", Item{Created: base, Modified: base.Add(10 * time.Minute), LastUsed: base.Add(time.Hour)}, RootDownloads, base.Add(time.Hour), ActionOpened, true},
		{"nothing happened", Item{}, RootDocuments, time.Time{}, "", false},
		{"no creation date: only an open counts", Item{Modified: base, LastUsed: base.Add(-time.Hour)}, RootDocuments, base.Add(-time.Hour), ActionOpened, true},
	}
	for _, c := range cases {
		at, action, ok := Activity(c.item, c.root)
		if ok != c.ok || action != c.action || !at.Equal(c.at) {
			t.Errorf("%s: Activity = %v %q %v, want %v %q %v", c.name, at, action, ok, c.at, c.action, c.ok)
		}
	}
}

func TestCanonicalMapsICloudDesktopAndDocuments(t *testing.T) {
	home := "/Users/sam"
	cloud := home + "/Library/Mobile Documents/com~apple~CloudDocs"
	cases := map[string]string{
		cloud + "/Documents/Taxes/return.pdf": home + "/Documents/Taxes/return.pdf",
		cloud + "/desktop/a.txt":              home + "/Desktop/a.txt",
		cloud + "/Projects/plan.pages":        cloud + "/Projects/plan.pages",
		home + "/Documents/x.txt":             home + "/Documents/x.txt",
		cloud + "/Documents":                  home + "/Documents",
	}
	for in, want := range cases {
		if got := Canonical(in, home); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHashesFindFilesUnderAPrivateFolder(t *testing.T) {
	path := "/Users/sam/Documents/Taxes/2026/return.pdf"
	hashes := strings.Fields(AncestorHashes(path))
	if len(hashes) != 6 {
		t.Fatalf("hashes = %d, want one per folder above the file (6)", len(hashes))
	}
	want := FolderHash("/Users/Sam/Documents/Taxes")
	found := false
	for _, h := range hashes {
		if h == want {
			found = true
		}
	}
	if !found {
		t.Fatal("a folder above the file must be found by its hash, whatever its case")
	}
	if PathHash(path) != PathHash("/Users/sam/documents/Taxes/2026/return.pdf") {
		t.Fatal("the path hash must ignore case")
	}
	if strings.Contains(AncestorHashes(path), "Taxes") {
		t.Fatal("no folder name may be stored")
	}
	if !UnderFolder(path, "/Users/sam/Documents/taxes") || UnderFolder(path, "/Users/sam/Documents/Tax") {
		t.Fatal("UnderFolder must match whole folders, ignoring case")
	}
}

// TestSpotlightQueryAgainstApplications runs the real bridge against
// /Applications, which no privacy setting guards. Set YANSU_SPOTLIGHT_TEST=1
// to run it; it needs Spotlight indexing on.
func TestSpotlightQueryAgainstApplications(t *testing.T) {
	if os.Getenv("YANSU_SPOTLIGHT_TEST") != "1" || !Supported() {
		t.Skip("set YANSU_SPOTLIGHT_TEST=1 on a Mac to run the Spotlight bridge")
	}
	items, err := Query(ProbeQuery, "/Applications", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 {
		t.Fatal("Spotlight returned nothing for /Applications")
	}
	for _, item := range items {
		if !strings.HasPrefix(item.Path, "/Applications/") || item.Name == "" {
			t.Fatalf("item = %+v", item)
		}
	}
}
