package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"wails-gui/internal/platform/activitymon"
	"wails-gui/internal/platform/cmdutil"
)

// ── Types ────────────────────────────────────────────────────────────

// openCLIStrategy represents an OpenCLI authentication strategy.
type openCLIStrategy string

const (
	openCLIStrategyPublic    openCLIStrategy = "public"
	openCLIStrategyCookie    openCLIStrategy = "cookie"
	openCLIStrategyHeader    openCLIStrategy = "header"
	openCLIStrategyIntercept openCLIStrategy = "intercept"
	openCLIStrategyUI        openCLIStrategy = "ui"
)

// openCLICommandArg describes a single argument for an OpenCLI command.
type openCLICommandArg struct {
	Name       string   `json:"name"`
	Type       string   `json:"type"`
	Required   bool     `json:"required"`
	Positional bool     `json:"positional"`
	Choices    []string `json:"choices"`
	Default    any      `json:"default"`
	Help       string   `json:"help"`
}

// openCLICommand describes a single command available for a site.
type openCLICommand struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Strategy    openCLIStrategy     `json:"strategy"`
	Args        []openCLICommandArg `json:"args"`
	Columns     []string            `json:"columns"`
	Domain      string              `json:"domain"`
	Browser     bool                `json:"browser"`
}

// openCLIRawCommand is the flat JSON entry from `opencli list -f json`.
type openCLIRawCommand struct {
	Command     string              `json:"command"`
	Site        string              `json:"site"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Strategy    openCLIStrategy     `json:"strategy"`
	Browser     bool                `json:"browser"`
	Args        []openCLICommandArg `json:"args"`
	Columns     []string            `json:"columns"`
	Domain      string              `json:"domain"`
}

// openCLISite groups all commands for one website/app.
type openCLISite struct {
	Site     string           `json:"site"`
	Commands []openCLICommand `json:"commands"`
	Category string           `json:"category"`
	Domain   string           `json:"domain"`
}

// openCLICapabilities holds the full result of detecting OpenCLI on the system.
type openCLICapabilities struct {
	Version            string        `json:"version"`
	Sites              []openCLISite `json:"sites"`
	Available          bool          `json:"available"`
	ChromeExtInstalled bool          `json:"chromeExtInstalled"` // true if COOKIE/HEADER strategies can work
}

// resolvedDataSource tracks which data source to use for a feature.
type resolvedDataSource struct {
	FeatureName string          // the feature this source serves
	SourceType  string          // "opencli_builtin" | "opencli_generated" | "computer_use" | "unavailable"
	Site        string          // OpenCLI site name or app name for computer use
	Commands    []string        // relevant command names
	Strategy    openCLIStrategy // auth strategy
	Note        string          // stability or provenance note
}

// ── Detection & Caching ─────────────────────────────────────────────

const opencliCacheTTL = 10 * time.Minute

var (
	opencliCache     *openCLICapabilities
	opencliCacheMu   sync.RWMutex
	opencliCacheTime time.Time
)

// detectOpenCLI checks if opencli is installed and caches its capabilities.
// Also probes for Chrome extension availability to determine strategy support.
func detectOpenCLI(ctx context.Context) *openCLICapabilities {
	// Fast path: return cached value if fresh.
	opencliCacheMu.RLock()
	if opencliCache != nil && time.Since(opencliCacheTime) < opencliCacheTTL {
		cached := opencliCache
		opencliCacheMu.RUnlock()
		return cached
	}
	opencliCacheMu.RUnlock()

	// Slow path: acquire write lock and detect.
	opencliCacheMu.Lock()
	defer opencliCacheMu.Unlock()

	// Double-check after acquiring write lock.
	if opencliCache != nil && time.Since(opencliCacheTime) < opencliCacheTTL {
		return opencliCache
	}

	notAvailable := &openCLICapabilities{Available: false}

	path := installedOpenCLIPath()
	if path == "" {
		opencliCache = notAvailable
		opencliCacheTime = time.Now()
		return opencliCache
	}

	// List available sites/commands.
	listCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	cmd := cmdutil.CommandContext(listCtx, path, "list", "-f", "json")
	output, err := cmd.Output()
	if err != nil {
		log.Printf("[opencli] list command failed: %v", err)
		opencliCache = notAvailable
		opencliCacheTime = time.Now()
		return opencliCache
	}

	// opencli list output format varies by version. Try parsing in order:
	// 1. Flat command array (current format) → group by site
	// 2. Already-grouped site array (possible future/alternate format)
	// 3. Give up but still mark as available with empty sites
	sites := parseOpenCLIListOutput(output)

	// Get version.
	versionCtx, vCancel := context.WithTimeout(ctx, 5*time.Second)
	defer vCancel()
	versionCmd := cmdutil.CommandContext(versionCtx, path, "--version")
	versionOut, _ := versionCmd.Output()
	version := strings.TrimSpace(string(versionOut))

	// Detect Chrome extension by running `opencli doctor -f json` and checking
	// for extension connectivity. If it fails or reports no extension, only
	// PUBLIC strategy is reliable.
	chromeExt := detectChromeExtension(ctx, path)

	opencliCache = &openCLICapabilities{
		Version:            version,
		Sites:              sites,
		Available:          true,
		ChromeExtInstalled: chromeExt,
	}
	opencliCacheTime = time.Now()
	log.Printf("[opencli] detected %s with %d sites (chrome extension: %v)", version, len(sites), chromeExt)
	return opencliCache
}

// detectChromeExtension runs `opencli doctor -f json` to check if the Chrome
// extension is installed and connected. Returns true if COOKIE/HEADER
// strategies are available.
func detectChromeExtension(ctx context.Context, opencliPath string) bool {
	doctorCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := cmdutil.CommandContext(doctorCtx, opencliPath, "doctor", "-f", "json")
	output, err := cmd.Output()
	if err != nil {
		log.Printf("[opencli] doctor command failed (assuming no Chrome extension): %v", err)
		return false
	}

	// Parse doctor output — look for extension/browser connectivity fields.
	var doctor map[string]any
	if err := json.Unmarshal(output, &doctor); err != nil {
		log.Printf("[opencli] failed to parse doctor output: %v", err)
		return false
	}

	// Check for extension-related fields. The exact schema depends on OpenCLI
	// version, so we check for common indicators.
	if ext, ok := doctor["extension"].(map[string]any); ok {
		if connected, ok := ext["connected"].(bool); ok {
			return connected
		}
	}
	// Fallback: check if any COOKIE strategy commands exist and succeed.
	if browser, ok := doctor["browser"].(map[string]any); ok {
		if connected, ok := browser["connected"].(bool); ok {
			return connected
		}
	}

	return false
}

// invalidateOpenCLICache forces the next detectOpenCLI call to re-probe.
func invalidateOpenCLICache() {
	opencliCacheMu.Lock()
	opencliCacheTime = time.Time{}
	opencliCacheMu.Unlock()
}

// lookupOpenCLISite finds a site by name in cached capabilities.
func lookupOpenCLISite(caps *openCLICapabilities, siteName string) *openCLISite {
	if caps == nil || !caps.Available {
		return nil
	}
	lower := strings.ToLower(siteName)
	for i := range caps.Sites {
		if strings.ToLower(caps.Sites[i].Site) == lower {
			return &caps.Sites[i]
		}
	}
	return nil
}

// ── Auto-Generation ─────────────────────────────────────────────────

const opencliGenerateTimeout = 2 * time.Minute

// opencliGenerate attempts to auto-create an adapter for an unsupported URL.
func opencliGenerate(ctx context.Context, url string) (*openCLISite, error) {
	path := installedOpenCLIPath()
	if path == "" {
		return nil, fmt.Errorf("opencli not found")
	}

	genCtx, cancel := context.WithTimeout(ctx, opencliGenerateTimeout)
	defer cancel()

	cmd := cmdutil.CommandContext(genCtx, path, "generate", url)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("opencli generate failed for %s: %w\n%s", url, err, output)
	}

	// Invalidate cache so next detectOpenCLI picks up the new adapter.
	invalidateOpenCLICache()

	// Re-detect to find the newly generated site.
	caps := detectOpenCLI(ctx)
	domain := extractDomain(url)
	site := lookupOpenCLISite(caps, domain)
	if site == nil {
		return nil, fmt.Errorf("opencli generate succeeded but site %q not found in list", domain)
	}
	return site, nil
}

// extractDomain extracts the base domain name from a URL for OpenCLI site matching.
func extractDomain(rawURL string) string {
	u := strings.TrimPrefix(rawURL, "https://")
	u = strings.TrimPrefix(u, "http://")
	if idx := strings.Index(u, "/"); idx > 0 {
		u = u[:idx]
	}
	u = strings.TrimPrefix(u, "www.")
	if idx := strings.Index(u, "."); idx > 0 {
		u = u[:idx]
	}
	return u
}

// ── Prompt Building ─────────────────────────────────────────────────

// buildOpenCLIAnalysisSection returns a prompt section for the Analysis phase
// informing the LLM that OpenCLI data sources are available.
func buildOpenCLIAnalysisSection(caps *openCLICapabilities) string {
	if caps == nil || !caps.Available || len(caps.Sites) == 0 {
		return ""
	}

	var b strings.Builder
	hasComputerUse := activitymon.ComputerUseSupported()
	if hasComputerUse {
		b.WriteString("\n\nADDITIONAL DATA SOURCES (OpenCLI + Computer Use API):\n")
	} else {
		b.WriteString("\n\nADDITIONAL DATA SOURCES (OpenCLI):\n")
	}
	b.WriteString("The user's machine has OpenCLI installed, which provides access to websites and Electron apps\n")
	if hasComputerUse {
		b.WriteString("that do NOT have public APIs. The user also has a macOS Computer Use API for controlling native apps.\n")
	} else {
		b.WriteString("that do NOT have public APIs.\n")
	}
	b.WriteString("When designing features, be MORE AMBITIOUS about data requirements.\n\n")

	if caps.ChromeExtInstalled {
		b.WriteString("Chrome extension is INSTALLED — COOKIE/HEADER strategies available (logged-in session access).\n\n")
	} else {
		b.WriteString("Chrome extension is NOT installed — only PUBLIC strategy sites work without setup.\n\n")
	}

	b.WriteString("OpenCLI supports these sites:\n")
	for _, site := range caps.Sites {
		cmds := siteCommandNames(&site)
		strategy := siteDefaultStrategy(&site)
		domain := site.Domain
		if domain == "" {
			domain = site.Site
		}
		fmt.Fprintf(&b, "- %s (%s, %s): %s\n", site.Site, domain, strategy, strings.Join(cmds, ", "))
	}

	if hasComputerUse {
		b.WriteString("\nFor native macOS apps not covered by OpenCLI (Finder, Calendar, Preview, etc.),\n")
		b.WriteString("the Computer Use API can read UI elements and perform actions (click, type, press keys).\n\n")
	}

	b.WriteString("If a feature needs data from one of these sites, KEEP the feature.\n")
	b.WriteString("If a feature needs a site NOT listed above, it may still work — opencli generate can\n")
	b.WriteString("auto-create adapters for new websites at build time.\n")
	if hasComputerUse {
		b.WriteString("CRITICAL: every feature MUST use REAL data fetched at runtime. OpenCLI and Computer Use\n")
		b.WriteString("are tools to GET real data, not an excuse to skip it.\n")
	} else {
		b.WriteString("CRITICAL: every feature MUST use REAL data fetched at runtime. OpenCLI is a tool\n")
		b.WriteString("to GET real data, not an excuse to skip it.\n")
	}

	return b.String()
}

// buildOpenCLIGenerationSection returns a prompt section for the Generation phase
// with concrete usage instructions based on resolved data sources from Phase 2.5.
// caps is used to look up full command signatures (args, columns) for resolved sources.
func buildOpenCLIGenerationSection(sources []resolvedDataSource, opencliPath string, caps *openCLICapabilities) string {
	var opencliSources []resolvedDataSource
	var computerUseSources []resolvedDataSource
	var unavailable []resolvedDataSource

	for _, s := range sources {
		switch s.SourceType {
		case "opencli_builtin", "opencli_generated":
			opencliSources = append(opencliSources, s)
		case "computer_use":
			computerUseSources = append(computerUseSources, s)
		case "unavailable":
			unavailable = append(unavailable, s)
		}
	}

	if len(opencliSources) == 0 && len(computerUseSources) == 0 && len(unavailable) == 0 {
		return ""
	}

	var b strings.Builder

	// OpenCLI section
	if len(opencliSources) > 0 {
		b.WriteString("\n\nOPENCLI DATA SOURCES — REAL DATA FROM WEBSITES/APPS:\n\n")
		b.WriteString("Available data sources for this app:\n")
		for _, s := range opencliSources {
			stability := "built-in, stable"
			if s.SourceType == "opencli_generated" {
				stability = "auto-generated, add extra error handling"
			}
			fmt.Fprintf(&b, "- %s: %s (Strategy: %s, %s)\n",
				s.Site, strings.Join(s.Commands, ", "), s.Strategy, stability)
		}

		// Include full command signatures and adapter docs, deduplicated by site.
		// Multiple features may resolve to the same site — only emit once.
		if caps != nil && caps.Available {
			siteMap := make(map[string]openCLISite, len(caps.Sites))
			for _, site := range caps.Sites {
				siteMap[site.Site] = site
			}

			emittedSites := make(map[string]bool)
			b.WriteString("\nCOMMAND REFERENCE:\n\n")
			for _, s := range opencliSources {
				if emittedSites[s.Site] {
					continue
				}
				emittedSites[s.Site] = true
				if site, ok := siteMap[s.Site]; ok {
					for _, cmd := range site.Commands {
						b.WriteString(formatCommandSignature(site.Site, cmd))
						b.WriteString("\n")
					}
				}
			}
		}

		// Include adapter docs if cached locally (also deduplicated).
		docsSection := buildAdapterDocsSection(deduplicateSources(opencliSources))
		if docsSection != "" {
			b.WriteString(docsSection)
		}

		fmt.Fprintf(&b, "\nHOW TO USE in server.ts:\n\n")
		fmt.Fprintf(&b, "The opencli binary is at: %s\n", opencliPath)
		fmt.Fprintf(&b, "Use this absolute path, NOT bare \"opencli\".\n\n")
		b.WriteString("async function opencli(site: string, command: string, args: Record<string, string> = {}): Promise<any> {\n")
		fmt.Fprintf(&b, "  const proc = Bun.spawn([\"%s\", site, command,\n", opencliPath)
		b.WriteString("    ...Object.entries(args).flatMap(([k, v]) => [`--${k}`, v]), \"-f\", \"json\"],\n")
		b.WriteString("    { stdout: \"pipe\", stderr: \"pipe\" });\n")
		b.WriteString("  const stdout = await new Response(proc.stdout).text();\n")
		b.WriteString("  const exitCode = await proc.exited;\n")
		b.WriteString("  if (exitCode !== 0) {\n")
		b.WriteString("    const stderr = await new Response(proc.stderr).text();\n")
		b.WriteString("    throw new Error(`opencli ${site} ${command} failed (exit ${exitCode}): ${stderr}`);\n")
		b.WriteString("  }\n")
		b.WriteString("  return JSON.parse(stdout);\n")
		b.WriteString("}\n\n")

		b.WriteString("RULES:\n")
		b.WriteString("- Call opencli at RUNTIME inside API route handlers, not at build time.\n")
		b.WriteString("- In-memory TTL cache is allowed (60-300s). File-based caching is forbidden.\n")
		b.WriteString("- Always use -f json for machine-readable output.\n")
		b.WriteString("- If opencli fails, throw an error. NEVER fall back to fake data.\n")
		b.WriteString("- PUBLIC strategy works without Chrome. COOKIE/HEADER requires Chrome running and logged in.\n")
	}

	// Computer use section
	if len(computerUseSources) > 0 {
		b.WriteString("\n\nNATIVE APP COMPUTER USE SOURCES:\n\n")
		b.WriteString("These features use the Host Bridge Computer Use API for native macOS apps:\n")
		for _, s := range computerUseSources {
			fmt.Fprintf(&b, "- %s: use /api/computer-use/* endpoints to interact with %s\n", s.FeatureName, s.Site)
		}
		b.WriteString("\nSee the Host Bridge Computer Use API documentation above for endpoint details.\n")
	}

	// Removed features
	if len(unavailable) > 0 {
		b.WriteString("\n\nREMOVED FEATURES (no real data source found):\n")
		for _, s := range unavailable {
			fmt.Fprintf(&b, "- %s: %s\n", s.FeatureName, s.Note)
		}
		b.WriteString("Do NOT implement these features. Do NOT fake them.\n")
	}

	return b.String()
}

// ── Data Source Resolution (Phase 2.5) ──────────────────────────────

// resolveDataSources attempts to find a data source for each given feature.
// Cascade: OpenCLI builtin → opencli generate → computer use → unavailable.
// caps may be nil if OpenCLI is not installed — computer use fallback still runs.
//
// Generic data-source resolution infrastructure: it takes a plain list of
// feature descriptions so any agent-driven feature (handoff, automation, …)
// can reuse it.
func (a *App) resolveDataSources(ctx context.Context, caps *openCLICapabilities, features []string) []resolvedDataSource {
	var sources []resolvedDataSource
	opencliAvailable := caps != nil && caps.Available

	for _, feature := range features {
		lower := strings.ToLower(feature)

		// 1. Check if any OpenCLI site matches this feature.
		var matched *openCLISite
		if opencliAvailable {
			for i := range caps.Sites {
				siteLower := strings.ToLower(caps.Sites[i].Site)
				if strings.Contains(lower, siteLower) {
					matched = &caps.Sites[i]
					break
				}
				for _, cmd := range caps.Sites[i].Commands {
					if strings.Contains(lower, strings.ToLower(cmd.Name)) {
						matched = &caps.Sites[i]
						break
					}
				}
				if matched != nil {
					break
				}
			}
		}

		var strategy openCLIStrategy
		if matched != nil {
			strategy = siteDefaultStrategy(matched)
			// If strategy requires Chrome extension and it's not installed, check
			// if there's a PUBLIC fallback command.
			if !caps.ChromeExtInstalled && strategy != openCLIStrategyPublic {
				hasPublic := false
				for _, cmd := range matched.Commands {
					if cmd.Strategy == openCLIStrategyPublic {
						hasPublic = true
						break
					}
				}
				if !hasPublic {
					// No public fallback — fall through to computer use instead
					// of marking unavailable. The computer use API can still
					// interact with the site via the browser.
					log.Printf("[opencli] %s: needs Chrome extension (strategy %s), trying computer use", feature, strategy)
					matched = nil // clear match, let computer use handle it
				} else {
					strategy = openCLIStrategyPublic
				}
			}
		}

		if matched != nil {
			log.Printf("[opencli] %s: matched OpenCLI site %q (strategy: %s)", feature, matched.Site, strategy)
			sources = append(sources, resolvedDataSource{
				FeatureName: feature,
				SourceType:  "opencli_builtin",
				Site:        matched.Site,
				Commands:    siteCommandNames(matched),
				Strategy:    strategy,
			})
			continue
		}

		// 2. Try opencli generate for URL-like features (only if OpenCLI is available).
		if opencliAvailable && (strings.Contains(lower, "http") || strings.Contains(lower, ".com") || strings.Contains(lower, ".org")) {
			// Extract URL from feature text.
			words := strings.Fields(feature)
			for _, word := range words {
				if strings.HasPrefix(word, "http") {
					log.Printf("[opencli] %s: attempting opencli generate for %s", feature, word)
					site, err := opencliGenerate(ctx, word)
					if err != nil {
						log.Printf("[opencli] generate failed for %s: %v", word, err)
					} else {
						sources = append(sources, resolvedDataSource{
							FeatureName: feature,
							SourceType:  "opencli_generated",
							Site:        site.Site,
							Commands:    siteCommandNames(site),
							Strategy:    siteDefaultStrategy(site),
							Note:        "auto-generated adapter",
						})
						continue
					}
				}
			}
		}

		// 3. Check if this is a native macOS app or website that can use computer use.
		// Computer use is macOS-only — only emit computer use sources on supported platforms.
		if activitymon.ComputerUseSupported() {
			nativeApps := []string{"finder", "mail", "calendar", "preview", "keynote", "pages", "numbers", "notes", "reminders", "contacts", "maps"}
			isNativeApp := false
			for _, app := range nativeApps {
				if strings.Contains(lower, app) {
					isNativeApp = true
					break
				}
			}
			isWebComputerUse := !isNativeApp && (strings.Contains(lower, "search") ||
				strings.Contains(lower, "browse") ||
				strings.Contains(lower, "website") ||
				strings.Contains(lower, "product") ||
				strings.Contains(lower, "marketplace") ||
				strings.Contains(lower, "amazon") ||
				strings.Contains(lower, "ebay") ||
				strings.Contains(lower, "shop"))

			if isNativeApp {
				log.Printf("[opencli] %s: using Computer Use API (native macOS app)", feature)
				sources = append(sources, resolvedDataSource{
					FeatureName: feature,
					SourceType:  "computer_use",
					Site:        feature,
					Note:        "native macOS app — use Host Bridge Computer Use API",
				})
				continue
			}
			if isWebComputerUse {
				log.Printf("[opencli] %s: using Computer Use API (browser computer use fallback)", feature)
				sources = append(sources, resolvedDataSource{
					FeatureName: feature,
					SourceType:  "computer_use",
					Site:        feature,
					Note:        "website computer use via browser — use Host Bridge Computer Use API to open URL and read accessibility tree",
				})
				continue
			}
		}

		// 4. Check if feature looks like it needs external data.
		// If so, mark as unavailable so the generation prompt removes it.
		externalKeywords := []string{
			"fetch", "track", "monitor", "feed", "stream", "trending",
			"live", "real-time", "api", "scrape", "crawl", "data from",
			"pull from", "sync with", "integrate with", "connect to",
		}
		looksExternal := false
		for _, kw := range externalKeywords {
			if strings.Contains(lower, kw) {
				looksExternal = true
				break
			}
		}
		if looksExternal {
			log.Printf("[opencli] %s: no data source found, marking unavailable", feature)
			sources = append(sources, resolvedDataSource{
				FeatureName: feature,
				SourceType:  "unavailable",
				Note:        "no OpenCLI adapter, no computer use match, and no free API identified",
			})
			continue
		}

		// 5. Feature doesn't clearly need external data — skip resolution
		// (it might be a computation, local tool, or UI-only feature).
	}

	return sources
}

// siteCommandNames returns the command names for a site as a string slice.
func siteCommandNames(site *openCLISite) []string {
	cmds := make([]string, 0, len(site.Commands))
	for _, c := range site.Commands {
		cmds = append(cmds, c.Name)
	}
	return cmds
}

// siteDefaultStrategy returns the first command's strategy as the site default.
func siteDefaultStrategy(site *openCLISite) openCLIStrategy {
	if len(site.Commands) > 0 {
		return site.Commands[0].Strategy
	}
	return openCLIStrategyPublic
}

// maxAdapterDocLength caps each adapter doc to avoid bloating the prompt.
const maxAdapterDocLength = 3000

// buildAdapterDocsSection returns a prompt section with cached adapter documentation
// for the resolved OpenCLI sources. Includes usage examples, output format notes,
// and authentication tips from the official adapter docs.
func buildAdapterDocsSection(sources []resolvedDataSource) string {
	var b strings.Builder
	included := 0

	for _, s := range sources {
		doc := LoadAdapterDoc(s.Site)
		if doc == "" {
			continue
		}
		if included == 0 {
			b.WriteString("\nADAPTER DOCUMENTATION (from OpenCLI docs):\n\n")
		}
		// Truncate very long docs to avoid blowing up the prompt.
		if len(doc) > maxAdapterDocLength {
			doc = doc[:maxAdapterDocLength] + "\n... (truncated)"
		}
		fmt.Fprintf(&b, "--- %s ---\n%s\n\n", s.Site, doc)
		included++
	}

	return b.String()
}

// deduplicateSources returns sources with unique sites only (first occurrence wins).
func deduplicateSources(sources []resolvedDataSource) []resolvedDataSource {
	seen := make(map[string]bool)
	var result []resolvedDataSource
	for _, s := range sources {
		if seen[s.Site] {
			continue
		}
		seen[s.Site] = true
		result = append(result, s)
	}
	return result
}

// parseOpenCLIListOutput tries multiple JSON shapes to be tolerant of
// schema changes across OpenCLI versions. Returns sites (possibly empty)
// but never fails — a partial parse is better than no OpenCLI support.
func parseOpenCLIListOutput(output []byte) []openCLISite {
	// 1. Try flat command array (current format).
	var rawCommands []openCLIRawCommand
	if err := json.Unmarshal(output, &rawCommands); err == nil && len(rawCommands) > 0 {
		return groupCommandsBySite(rawCommands)
	}

	// 2. Try already-grouped site array (possible alternate format).
	var sites []openCLISite
	if err := json.Unmarshal(output, &sites); err == nil && len(sites) > 0 {
		return sites
	}

	// 3. Could not parse — log and return empty (OpenCLI still marked available
	// so other features like `opencli generate` keep working).
	log.Printf("[opencli] could not parse list output (len=%d), continuing with empty site list", len(output))
	return nil
}

// groupCommandsBySite groups flat opencli list output into site-grouped structure.
func groupCommandsBySite(rawCommands []openCLIRawCommand) []openCLISite {
	siteMap := make(map[string]*openCLISite)
	var siteOrder []string

	for _, raw := range rawCommands {
		if raw.Site == "" || raw.Site == "_shared" {
			continue
		}
		site, exists := siteMap[raw.Site]
		if !exists {
			site = &openCLISite{
				Site:   raw.Site,
				Domain: raw.Domain,
			}
			siteMap[raw.Site] = site
			siteOrder = append(siteOrder, raw.Site)
		}
		site.Commands = append(site.Commands, openCLICommand{
			Name:        raw.Name,
			Description: raw.Description,
			Strategy:    raw.Strategy,
			Args:        raw.Args,
			Columns:     raw.Columns,
			Domain:      raw.Domain,
			Browser:     raw.Browser,
		})
	}

	sites := make([]openCLISite, 0, len(siteOrder))
	for _, name := range siteOrder {
		sites = append(sites, *siteMap[name])
	}
	return sites
}

// formatCommandSignature formats a single command's args for the generation prompt.
func formatCommandSignature(site string, cmd openCLICommand) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  opencli %s %s", site, cmd.Name)

	for _, arg := range cmd.Args {
		if arg.Positional {
			if arg.Required {
				fmt.Fprintf(&b, " <%s>", arg.Name)
			} else {
				fmt.Fprintf(&b, " [%s]", arg.Name)
			}
		}
	}
	b.WriteString("\n")

	if cmd.Description != "" {
		fmt.Fprintf(&b, "    %s\n", cmd.Description)
	}

	for _, arg := range cmd.Args {
		if arg.Positional {
			continue
		}
		required := ""
		if arg.Required {
			required = " (required)"
		}
		defVal := ""
		if arg.Default != nil {
			defVal = fmt.Sprintf(", default: %v", arg.Default)
		}
		choices := ""
		if len(arg.Choices) > 0 {
			choices = fmt.Sprintf(", choices: %s", strings.Join(arg.Choices, "|"))
		}
		fmt.Fprintf(&b, "    --%s <%s>%s%s%s", arg.Name, arg.Type, required, defVal, choices)
		if arg.Help != "" {
			fmt.Fprintf(&b, " — %s", arg.Help)
		}
		b.WriteString("\n")
	}

	if len(cmd.Columns) > 0 {
		fmt.Fprintf(&b, "    Output columns: %s\n", strings.Join(cmd.Columns, ", "))
	}

	return b.String()
}
