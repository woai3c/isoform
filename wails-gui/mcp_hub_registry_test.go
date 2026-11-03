package main

import (
	"strings"
	"testing"
	"time"
)

const sampleModelcontextprotocolReadme = `# MCP Servers

## 🌟 Reference Servers

These servers are maintained by the protocol authors.

- **[Filesystem](src/filesystem)** - File operations on the local disk.
- **[Fetch](src/fetch)** - Make HTTP requests from the model.
- **[Git](https://github.com/modelcontextprotocol/servers/tree/main/src/git)** - Read git history.

## 🌎 Third-Party Servers

These servers are maintained by the community.

- **[Linear](https://github.com/jerhadf/linear-mcp-server)** - Linear integration.
- **[Notion](https://github.com/danhilse/notion_mcp)** - Notion integration.

## ⭐ Support

If you have questions, please open a discussion.

- [GitHub Discussions](https://github.com/orgs/modelcontextprotocol/discussions) - Community support forum.
`

const samplePunkpeyeReadme = `# Awesome MCP Servers

* [Server Implementations](#server-implementations)
* [Frameworks](#frameworks)

## What is MCP?

MCP is an open protocol.

## Clients

- [Claude Desktop](https://claude.ai/download) - Anthropic's reference client.

## Server Implementations

### 🔗 <a name="aggregators"></a>Aggregators

Servers for accessing many apps through one MCP server.

- [acme/agent](https://github.com/acme/agent) 📇 ☁️ - A unified MCP aggregator.
- [glama/listing](https://glama.ai/mcp/servers) - Web directory (not a server).
- [bob/multitool](https://github.com/bob/multitool) [![bob/multitool MCP server](https://glama.ai/mcp/servers/bob/multitool/badges/score.svg)](https://glama.ai/mcp/servers/bob/multitool) 🐍 ☁️ - Multi-tool wrapper.

### 📂 <a name="browser-automation"></a>Browser Automation

- [puppet/puppet-mcp](https://github.com/puppet/puppet-mcp) 📇 - Puppeteer-based browser automation.

## Frameworks

- [framework-x](https://github.com/example/framework-x) - SDK, not an installable server.

## Tips and Tricks

- [some-tip](https://github.com/example/tip) - Some advice.

## Star History

- [Star Chart](https://star-history.com/...) - Pretty graphs.
`

func TestParseModelcontextprotocolReadmeOnlyReference(t *testing.T) {
	entries := parseModelcontextprotocolReadme(sampleModelcontextprotocolReadme, "modelcontextprotocol/servers")
	if len(entries) != 3 {
		t.Fatalf("expected 3 reference entries, got %d: %+v", len(entries), entries)
	}
	bySlug := map[string]McpRegistryEntry{}
	for _, e := range entries {
		bySlug[e.Slug] = e
	}
	fs, ok := bySlug["filesystem"]
	if !ok || fs.Kind != mcpRegistryKindReference {
		t.Fatalf("filesystem missing or wrong kind: %+v", fs)
	}
	if fs.NpmPackage != "@modelcontextprotocol/server-filesystem" {
		t.Fatalf("npm package wrong: %q", fs.NpmPackage)
	}
	if !fs.Installable || fs.Command != "npx" || len(fs.Args) != 2 {
		t.Fatalf("install fields wrong: %+v", fs)
	}
	if fs.Section != "Reference Servers" {
		t.Fatalf("section not cleaned: %q", fs.Section)
	}
	// Third-party + Support entries from this README must NOT leak in —
	// the official README is reference-only in our parser.
	for _, e := range entries {
		if strings.Contains(e.Name, "Linear") || strings.Contains(e.Name, "Notion") {
			t.Fatalf("third-party leaked from official README: %+v", e)
		}
		if strings.Contains(e.Name, "GitHub Discussions") {
			t.Fatalf("Support-section entry leaked: %+v", e)
		}
	}
}

func TestParsePunkpeyeReadmeFiltering(t *testing.T) {
	entries := parsePunkpeyeReadme(samplePunkpeyeReadme, "punkpeye/awesome-mcp-servers")

	bySlug := map[string]McpRegistryEntry{}
	for _, e := range entries {
		bySlug[e.Slug] = e
	}

	if _, ok := bySlug["tp-acme-agent"]; !ok {
		t.Fatalf("acme/agent missing from punkpeye entries: %+v", entries)
	}
	if _, ok := bySlug["tp-bob-multitool"]; !ok {
		t.Fatalf("bob/multitool (with glama badge) missing")
	}

	if puppet, ok := bySlug["tp-puppet-puppet-mcp"]; !ok {
		t.Fatalf("puppet/puppet-mcp missing")
	} else if puppet.Section != "Browser Automation" {
		t.Fatalf("expected section=Browser Automation, got %q", puppet.Section)
	}

	if agg, ok := bySlug["tp-acme-agent"]; !ok || agg.Section != "Aggregators" {
		t.Fatalf("aggregators section not cleaned, got %+v", agg)
	}

	for _, e := range entries {
		if strings.Contains(e.SourceURL, "glama.ai") {
			t.Fatalf("non-github entry leaked: %+v", e)
		}
		if strings.Contains(e.Name, "framework-x") {
			t.Fatalf("Frameworks section leaked: %+v", e)
		}
		if strings.Contains(e.Name, "Claude Desktop") {
			t.Fatalf("Clients section leaked: %+v", e)
		}
		if strings.Contains(e.Name, "some-tip") {
			t.Fatalf("Tips section leaked: %+v", e)
		}
		if strings.Contains(e.Name, "Star Chart") {
			t.Fatalf("Star History section leaked: %+v", e)
		}
		if e.Kind != mcpRegistryKindThirdParty {
			t.Fatalf("punkpeye entry should be third-party, got %+v", e)
		}
		if e.Installable {
			t.Fatalf("punkpeye entry must not be auto-installable: %+v", e)
		}
	}
}

func TestRegistryHostAllowedRejectsNonRepoLinks(t *testing.T) {
	cases := []struct {
		link string
		want bool
	}{
		{"https://github.com/acme/repo", true},
		{"https://github.com/acme/repo/tree/main", true},
		{"https://gitlab.com/acme/repo", true},
		{"https://github.com/orgs/modelcontextprotocol/discussions", false},
		{"https://github.com/topics/mcp", false},
		{"https://github.com/acme", false},
		{"https://glama.ai/mcp/servers", false},
		{"https://twitter.com/foo", false},
		{"not a url", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.link, func(t *testing.T) {
			got := registryHostAllowed(tc.link)
			if got != tc.want {
				t.Fatalf("registryHostAllowed(%q) = %v, want %v", tc.link, got, tc.want)
			}
		})
	}
}

func TestCleanupRegistryHeading(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"🔗 <a name=\"aggregators\"></a>Aggregators", "Aggregators"},
		{"🌟 Reference Servers", "Reference Servers"},
		{"  Browser Automation  ", "Browser Automation"},
		{"<a name=\"x\"></a>Memory & Storage", "Memory & Storage"},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := cleanupRegistryHeading(tc.in)
			if got != tc.want {
				t.Fatalf("cleanupRegistryHeading(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestCleanupRegistryDescription(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"📇 ☁️ - A unified MCP aggregator.", "A unified MCP aggregator."},
		{"[![alt](https://glama.ai/badge.svg)](https://x.com) 📇 ☁️ - Multi-tool wrapper.", "Multi-tool wrapper."},
		{"  Make HTTP requests from the model.", "Make HTTP requests from the model."},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got := cleanupRegistryDescription(tc.in)
			if got != tc.want {
				t.Fatalf("cleanupRegistryDescription(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestThirdPartySlugFromGitHubURL(t *testing.T) {
	cases := []struct {
		name string
		link string
		want string
	}{
		{"acme/agent", "https://github.com/acme/agent", "tp-acme-agent"},
		{"Some Display Name", "https://github.com/Foo-Bar/baz_qux", "tp-foo-bar-baz-qux"},
		{"name only", "", "tp-name-only"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := thirdPartySlug(tc.name, tc.link)
			if got != tc.want {
				t.Fatalf("thirdPartySlug(%q,%q) = %q, want %q", tc.name, tc.link, got, tc.want)
			}
		})
	}
}

func TestMcpRegistryCacheRoundTrip(t *testing.T) {
	fakeHomeDirT(t)

	entries := parseModelcontextprotocolReadme(sampleModelcontextprotocolReadme, "modelcontextprotocol/servers")
	cache := mcpRegistryCache{
		FetchedAt: time.Now(),
		Sources:   []string{"modelcontextprotocol/servers"},
		Entries:   entries,
	}
	if err := writeMcpRegistryCache(cache); err != nil {
		t.Fatalf("writeMcpRegistryCache: %v", err)
	}
	got, ok := readMcpRegistryCache()
	if !ok {
		t.Fatalf("readMcpRegistryCache returned ok=false")
	}
	if len(got.Entries) != len(entries) {
		t.Fatalf("entry count mismatch: got %d, want %d", len(got.Entries), len(entries))
	}
	if len(got.Sources) != 1 || got.Sources[0] != "modelcontextprotocol/servers" {
		t.Fatalf("sources mismatch: %+v", got.Sources)
	}
}

func TestSearchMcpRegistryFiltersByQuery(t *testing.T) {
	fakeHomeDirT(t)
	combined := append(
		parseModelcontextprotocolReadme(sampleModelcontextprotocolReadme, "modelcontextprotocol/servers"),
		parsePunkpeyeReadme(samplePunkpeyeReadme, "punkpeye/awesome-mcp-servers")...,
	)
	if err := writeMcpRegistryCache(mcpRegistryCache{
		FetchedAt: time.Now(),
		Sources:   []string{"modelcontextprotocol/servers", "punkpeye/awesome-mcp-servers"},
		Entries:   combined,
	}); err != nil {
		t.Fatalf("writeMcpRegistryCache: %v", err)
	}
	app := &App{}
	got, err := app.SearchMcpRegistry("filesystem")
	if err != nil {
		t.Fatalf("SearchMcpRegistry: %v", err)
	}
	if len(got) != 1 || got[0].Slug != "filesystem" {
		t.Fatalf("unexpected results: %+v", got)
	}

	browserHits, err := app.SearchMcpRegistry("browser")
	if err != nil {
		t.Fatalf("SearchMcpRegistry browser: %v", err)
	}
	if len(browserHits) == 0 {
		t.Fatalf("expected at least one browser entry, got none")
	}

	all, err := app.SearchMcpRegistry("")
	if err != nil {
		t.Fatalf("SearchMcpRegistry empty: %v", err)
	}
	if len(all) != len(combined) {
		t.Fatalf("expected all %d entries, got %d", len(combined), len(all))
	}
}

func TestInstallMcpFromRegistryRefuseThirdParty(t *testing.T) {
	fakeHomeDirT(t)
	entries := parsePunkpeyeReadme(samplePunkpeyeReadme, "punkpeye/awesome-mcp-servers")
	if err := writeMcpRegistryCache(mcpRegistryCache{
		FetchedAt: time.Now(),
		Sources:   []string{"punkpeye/awesome-mcp-servers"},
		Entries:   entries,
	}); err != nil {
		t.Fatalf("writeMcpRegistryCache: %v", err)
	}
	app := &App{}
	var slug string
	for _, e := range entries {
		if e.Kind == mcpRegistryKindThirdParty {
			slug = e.Slug
			break
		}
	}
	if slug == "" {
		t.Fatalf("expected at least one third-party entry in fixture")
	}
	if _, err := app.InstallMcpFromRegistry(slug, false); err == nil {
		t.Fatalf("expected third-party install to be refused")
	}
}

func TestInstallMcpFromRegistryReferenceEntry(t *testing.T) {
	fakeHomeDirT(t)
	entries := parseModelcontextprotocolReadme(sampleModelcontextprotocolReadme, "modelcontextprotocol/servers")
	if err := writeMcpRegistryCache(mcpRegistryCache{
		FetchedAt: time.Now(),
		Sources:   []string{"modelcontextprotocol/servers"},
		Entries:   entries,
	}); err != nil {
		t.Fatalf("writeMcpRegistryCache: %v", err)
	}
	app := &App{}
	view, err := app.InstallMcpFromRegistry("filesystem", false)
	if err != nil {
		t.Fatalf("InstallMcpFromRegistry: %v", err)
	}
	if view.Command != "npx" {
		t.Fatalf("command not set: %+v", view)
	}
	if view.Source != mcpSourceGitHub {
		t.Fatalf("expected source=github (registry stamps it), got %q", view.Source)
	}
	if !strings.Contains(view.SourceURL, "modelcontextprotocol") {
		t.Fatalf("source url not preserved: %q", view.SourceURL)
	}
}
