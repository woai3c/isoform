package appparser

import "regexp"

// registry holds all registered app-specific parsers.
var registry []Parser

// parsedBlankRunRE matches 3+ line breaks, including blank lines padded
// with horizontal whitespace. ParsedContent is prompt-facing text, so the
// registry keeps excessive visual spacing out of every parser result.
var parsedBlankRunRE = regexp.MustCompile(`\n[ \t]*(?:\n[ \t]*){2,}`)

// Register adds a parser to the global registry. Called from each parser's
// init() so registration is automatic on package import.
func Register(p Parser) {
	registry = append(registry, p)
}

// ParseTree finds the best parser for a capture and runs it against the
// structured AX tree (preferred) or flat text (fallback).
//
// Dispatch order:
//  1. URL-matching parsers (when url is non-empty) — lets Notion/Linear/…
//     web parsers pre-empt the generic browser chrome stripper when the
//     frontmost app is a browser on a known tool URL.
//  2. Bundle-ID-matching parsers — native apps and the fallback browser
//     chrome stripper for non-matched browser pages.
//
// Within each dispatch bucket, structured TreeParser wins over flat-text
// TextParser. Returns nil when no parser can produce useful content so
// callers can fall back to raw AX text / OCR.
func ParseTree(tree *AXNode, flatText, bundleID, windowTitle, url string) *ParsedContent {
	if out := runParsers(tree, flatText, bundleID, windowTitle, url, urlMatch); out != nil {
		return normalizeParsedContent(out)
	}
	return normalizeParsedContent(runParsers(tree, flatText, bundleID, windowTitle, url, bundleMatch))
}

// Parse runs flat-text parsers only. Kept for backward compatibility with
// call sites that have not yet adopted the structured tree path.
func Parse(axText, bundleID, windowTitle, url string) *ParsedContent {
	return ParseTree(nil, axText, bundleID, windowTitle, url)
}

// Catalog returns the declarative metadata for every registered parser
// that implements Describer. Parsers without a Describe() method are
// omitted from the listing but still participate in dispatch.
//
// The catalog is built fresh on every call (no caching) — registration
// happens at process startup via init() so the registry is stable by the
// time any UI calls this.
func Catalog() []ParserCatalogEntry {
	out := make([]ParserCatalogEntry, 0, len(registry))
	for _, p := range registry {
		if md, ok := p.(MultiDescriber); ok {
			out = append(out, md.DescribeAll()...)
			continue
		}
		if d, ok := p.(Describer); ok {
			out = append(out, d.Describe())
		}
	}
	return out
}

// matchFn reports whether a parser is eligible for a given capture.
type matchFn func(p Parser, bundleID, url string) bool

func urlMatch(p Parser, _, url string) bool {
	if url == "" {
		return false
	}
	um, ok := p.(URLMatcher)
	return ok && um.CanParseURL(url)
}

func bundleMatch(p Parser, bundleID, _ string) bool {
	return p.CanParse(bundleID)
}

func normalizeParsedContent(out *ParsedContent) *ParsedContent {
	if out == nil {
		return nil
	}
	out.Body = collapseParsedBlankRuns(out.Body)
	for i := range out.Messages {
		out.Messages[i].Content = collapseParsedBlankRuns(out.Messages[i].Content)
	}
	return out
}

func collapseParsedBlankRuns(s string) string {
	return parsedBlankRunRE.ReplaceAllString(s, "\n\n")
}

// runParsers walks the registry in declaration order, trying TreeParser
// first when a tree is available, then TextParser. Returns the first
// non-nil ParsedContent or nil when no parser matched or produced output.
func runParsers(tree *AXNode, flatText, bundleID, windowTitle, url string, match matchFn) *ParsedContent {
	if tree != nil {
		for _, p := range registry {
			if !match(p, bundleID, url) {
				continue
			}
			tp, ok := p.(TreeParser)
			if !ok {
				continue
			}
			if out := tp.ParseTree(tree, bundleID, windowTitle, url); out != nil {
				return out
			}
		}
	}
	for _, p := range registry {
		if !match(p, bundleID, url) {
			continue
		}
		tp, ok := p.(TextParser)
		if !ok {
			continue
		}
		if out := tp.Parse(flatText, bundleID, windowTitle, url); out != nil {
			return out
		}
	}
	return nil
}
