package main

import (
	"strings"
	"testing"
)

func TestGroupCommandsBySite(t *testing.T) {
	raw := []openCLIRawCommand{
		{Command: "hackernews/top", Site: "hackernews", Name: "top", Description: "Top stories", Strategy: "public", Domain: "news.ycombinator.com", Args: []openCLICommandArg{{Name: "limit", Type: "int", Default: 20, Help: "Number of stories"}}},
		{Command: "hackernews/new", Site: "hackernews", Name: "new", Description: "Newest stories", Strategy: "public", Domain: "news.ycombinator.com"},
		{Command: "twitter/search", Site: "twitter", Name: "search", Description: "Search tweets", Strategy: "cookie", Domain: "twitter.com", Browser: true, Args: []openCLICommandArg{{Name: "query", Type: "str", Required: true, Positional: true, Help: "Search query"}}},
		{Command: "twitter/user", Site: "twitter", Name: "user", Description: "User profile", Strategy: "cookie", Domain: "twitter.com", Browser: true},
		{Command: "_shared/desktop-commands", Site: "_shared", Name: "desktop-commands", Description: "Shared", Strategy: "ui"},
	}

	sites := groupCommandsBySite(raw)

	if len(sites) != 2 {
		t.Fatalf("expected 2 sites (hackernews, twitter), got %d", len(sites))
	}

	// Verify order preserved.
	if sites[0].Site != "hackernews" {
		t.Errorf("expected first site hackernews, got %s", sites[0].Site)
	}
	if sites[1].Site != "twitter" {
		t.Errorf("expected second site twitter, got %s", sites[1].Site)
	}

	// _shared should be filtered out.
	for _, s := range sites {
		if s.Site == "_shared" {
			t.Error("_shared should be excluded")
		}
	}

	// Verify commands grouped correctly.
	if len(sites[0].Commands) != 2 {
		t.Errorf("hackernews should have 2 commands, got %d", len(sites[0].Commands))
	}
	if len(sites[1].Commands) != 2 {
		t.Errorf("twitter should have 2 commands, got %d", len(sites[1].Commands))
	}

	// Verify domain propagated.
	if sites[0].Domain != "news.ycombinator.com" {
		t.Errorf("hackernews domain should be news.ycombinator.com, got %s", sites[0].Domain)
	}

	// Verify args carried through.
	topCmd := sites[0].Commands[0]
	if len(topCmd.Args) != 1 {
		t.Fatalf("hackernews/top should have 1 arg, got %d", len(topCmd.Args))
	}
	if topCmd.Args[0].Name != "limit" {
		t.Errorf("expected arg name 'limit', got %s", topCmd.Args[0].Name)
	}
}

func TestGroupCommandsBySiteEmpty(t *testing.T) {
	sites := groupCommandsBySite(nil)
	if len(sites) != 0 {
		t.Errorf("expected 0 sites for nil input, got %d", len(sites))
	}

	sites = groupCommandsBySite([]openCLIRawCommand{})
	if len(sites) != 0 {
		t.Errorf("expected 0 sites for empty input, got %d", len(sites))
	}
}

func TestFormatCommandSignature(t *testing.T) {
	cmd := openCLICommand{
		Name:        "search",
		Description: "Search Hacker News stories",
		Args: []openCLICommandArg{
			{Name: "query", Type: "str", Required: true, Positional: true, Help: "Search query"},
			{Name: "limit", Type: "int", Required: false, Default: 20, Help: "Number of results"},
			{Name: "sort", Type: "str", Required: false, Choices: []string{"relevance", "date"}, Default: "relevance", Help: "Sort order"},
		},
		Columns: []string{"rank", "title", "score", "author"},
	}

	result := formatCommandSignature("hackernews", cmd)

	// Should contain the command line.
	if !strings.Contains(result, "opencli hackernews search <query>") {
		t.Errorf("expected command line with positional arg, got:\n%s", result)
	}

	// Should contain description.
	if !strings.Contains(result, "Search Hacker News stories") {
		t.Errorf("expected description in output")
	}

	// Should contain named args with details.
	if !strings.Contains(result, "--limit <int>") {
		t.Errorf("expected --limit arg")
	}
	if !strings.Contains(result, "default: 20") {
		t.Errorf("expected default value for limit")
	}
	if !strings.Contains(result, "choices: relevance|date") {
		t.Errorf("expected choices for sort")
	}

	// Should contain output columns.
	if !strings.Contains(result, "Output columns: rank, title, score, author") {
		t.Errorf("expected output columns")
	}

	// Positional args should NOT appear as --flags.
	if strings.Contains(result, "--query") {
		t.Errorf("positional arg 'query' should not appear as --flag")
	}
}

func TestFormatCommandSignatureNoArgs(t *testing.T) {
	cmd := openCLICommand{
		Name:        "top",
		Description: "Top stories",
		Columns:     []string{"title", "score"},
	}

	result := formatCommandSignature("hackernews", cmd)
	if !strings.Contains(result, "opencli hackernews top\n") {
		t.Errorf("expected clean command line with no args, got:\n%s", result)
	}
}

func TestSiteCommandNames(t *testing.T) {
	site := &openCLISite{
		Site: "hackernews",
		Commands: []openCLICommand{
			{Name: "top"},
			{Name: "new"},
			{Name: "search"},
		},
	}
	names := siteCommandNames(site)
	if len(names) != 3 {
		t.Fatalf("expected 3 command names, got %d", len(names))
	}
	if names[0] != "top" || names[1] != "new" || names[2] != "search" {
		t.Errorf("unexpected names: %v", names)
	}
}

func TestSiteDefaultStrategy(t *testing.T) {
	site := &openCLISite{
		Commands: []openCLICommand{
			{Strategy: "cookie"},
			{Strategy: "public"},
		},
	}
	if s := siteDefaultStrategy(site); s != "cookie" {
		t.Errorf("expected cookie (first command's strategy), got %s", s)
	}

	empty := &openCLISite{}
	if s := siteDefaultStrategy(empty); s != openCLIStrategyPublic {
		t.Errorf("expected public for empty site, got %s", s)
	}
}

func TestBuildAdapterDocsSection(t *testing.T) {
	// With no cached docs, should return empty.
	sources := []resolvedDataSource{
		{Site: "nonexistent-site-xyz", SourceType: "opencli_builtin"},
	}
	result := buildAdapterDocsSection(sources)
	if result != "" {
		t.Errorf("expected empty for uncached site, got: %s", result)
	}
}

func TestParseOpenCLIListOutputFlat(t *testing.T) {
	// Current format: flat array of commands.
	input := `[{"command":"hn/top","site":"hn","name":"top","description":"Top","strategy":"public","browser":false,"args":[],"columns":["title"],"domain":"hn.com"},{"command":"hn/new","site":"hn","name":"new","description":"New","strategy":"public","browser":false,"args":[],"columns":[],"domain":"hn.com"}]`
	sites := parseOpenCLIListOutput([]byte(input))
	if len(sites) != 1 {
		t.Fatalf("expected 1 site, got %d", len(sites))
	}
	if sites[0].Site != "hn" {
		t.Errorf("expected site hn, got %s", sites[0].Site)
	}
	if len(sites[0].Commands) != 2 {
		t.Errorf("expected 2 commands, got %d", len(sites[0].Commands))
	}
}

func TestParseOpenCLIListOutputGrouped(t *testing.T) {
	// Alternate format: already grouped by site.
	input := `[{"site":"hn","commands":[{"name":"top","description":"Top","strategy":"public"}],"domain":"hn.com"}]`
	sites := parseOpenCLIListOutput([]byte(input))
	if len(sites) != 1 {
		t.Fatalf("expected 1 site, got %d", len(sites))
	}
	if len(sites[0].Commands) != 1 {
		t.Errorf("expected 1 command, got %d", len(sites[0].Commands))
	}
}

func TestParseOpenCLIListOutputInvalid(t *testing.T) {
	// Completely invalid JSON — should return nil without panic.
	sites := parseOpenCLIListOutput([]byte(`not json`))
	if sites != nil {
		t.Errorf("expected nil for invalid JSON, got %v", sites)
	}
}

func TestDeduplicateSources(t *testing.T) {
	sources := []resolvedDataSource{
		{Site: "hackernews", FeatureName: "trending stories"},
		{Site: "hackernews", FeatureName: "search stories"},
		{Site: "twitter", FeatureName: "tweets"},
		{Site: "twitter", FeatureName: "user profiles"},
		{Site: "reddit", FeatureName: "posts"},
	}
	deduped := deduplicateSources(sources)
	if len(deduped) != 3 {
		t.Fatalf("expected 3 unique sites, got %d", len(deduped))
	}
	// First occurrence should win.
	if deduped[0].FeatureName != "trending stories" {
		t.Errorf("expected first hackernews feature preserved, got %s", deduped[0].FeatureName)
	}
}

func TestStrategyConstants(t *testing.T) {
	// Verify constants match what opencli list -f json returns (lowercase).
	strategies := []openCLIStrategy{
		openCLIStrategyPublic,
		openCLIStrategyCookie,
		openCLIStrategyHeader,
		openCLIStrategyIntercept,
		openCLIStrategyUI,
	}
	for _, s := range strategies {
		if string(s) != strings.ToLower(string(s)) {
			t.Errorf("strategy %q should be lowercase to match opencli JSON output", s)
		}
	}
}
