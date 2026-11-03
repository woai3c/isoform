package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPErrorPrefix(t *testing.T) {
	got := httpErrorPrefix(404, "https://example.com/missing")
	if !strings.HasPrefix(got, "[HTTP 404 Not Found] WebFetch failed for https://example.com/missing") {
		t.Errorf("missing status + url in prefix: %q", got)
	}
	if !strings.Contains(got, "Do not retry the same URL") {
		t.Errorf("missing retry-discouraging copy: %q", got)
	}
	if !strings.HasSuffix(got, "\n\n") {
		t.Errorf("expected trailing blank line so the body reads cleanly: %q", got)
	}

	// 5xx should also produce a prefix — we don't want the model to see
	// a 502 page as a successful fetch either.
	got5xx := httpErrorPrefix(502, "https://example.com/down")
	if !strings.Contains(got5xx, "502 Bad Gateway") {
		t.Errorf("5xx status text missing: %q", got5xx)
	}
}

func TestWebFetchTool_HTTPSRequired(t *testing.T) {
	tool := NewWebFetchTool()
	_, err := tool.Execute(context.Background(), ToolCall{
		Input: map[string]any{"url": "http://example.com"},
	})
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("expected https requirement error, got %v", err)
	}
}

func TestWebFetchTool_RefusesLocalhost(t *testing.T) {
	tool := NewWebFetchTool()
	_, err := tool.Execute(context.Background(), ToolCall{
		Input: map[string]any{"url": "https://localhost/x"},
	})
	if err == nil {
		t.Error("expected refusal for localhost")
	}
}

func TestWebFetchTool_RefusesPrivateIP(t *testing.T) {
	tool := NewWebFetchTool()
	_, err := tool.Execute(context.Background(), ToolCall{
		Input: map[string]any{"url": "https://10.0.0.1/x"},
	})
	if err == nil {
		t.Error("expected refusal for private ip")
	}
}

func TestWebFetchTool_RefusesMetadata(t *testing.T) {
	tool := NewWebFetchTool()
	_, err := tool.Execute(context.Background(), ToolCall{
		Input: map[string]any{"url": "https://169.254.169.254/latest/meta-data"},
	})
	if err == nil {
		t.Error("expected refusal for metadata endpoint")
	}
}

func TestExtractTextFallback(t *testing.T) {
	html := `<html><head><style>body{}</style></head><body><h1>Hello</h1><script>x=1;</script><p>world</p></body></html>`
	got := extractTextFallback(html)
	if !strings.Contains(got, "Hello") || !strings.Contains(got, "world") {
		t.Errorf("extractTextFallback = %q", got)
	}
	if strings.Contains(got, "x=1") {
		t.Errorf("extractTextFallback kept script: %q", got)
	}
}

func TestExtractContent_ReadabilityArticle(t *testing.T) {
	// Realistic article HTML with nav, sidebar, footer noise.
	html := `<html><head><title>MRVL Stock Analysis 2026</title></head><body>
		<nav><a href="/">Home</a><a href="/markets">Markets</a><a href="/news">News</a></nav>
		<div class="sidebar">Related: NVDA, AMD, INTC, QCOM trending stocks sidebar widget</div>
		<article>
			<h1>MRVL Stock Analysis 2026</h1>
			<p>By Jane Smith</p>
			<p>Marvell Technology reported strong Q2 results with revenue up 15% year-over-year,
			driven by demand for custom AI chips in data centers. The company's design wins with
			major cloud providers continue to expand, positioning it well for sustained growth.</p>
			<p>Key highlights from the earnings call include expanding margins in the data center
			segment and growing adoption of their custom silicon solutions. Management raised
			full-year guidance citing strong demand visibility.</p>
			<p>Analysts maintain a consensus buy rating with an average price target of $125,
			representing approximately 20% upside from current levels.</p>
		</article>
		<footer>Copyright 2026 Financial News Corp. Terms of Service. Privacy Policy. Contact us.
		Disclaimer: not financial advice. All rights reserved. Cookie preferences.</footer>
		<script>var analytics={track:function(){}}; analytics.track('page_view');</script>
	</body></html>`

	u, _ := url.Parse("https://example.com/stock/mrvl")
	got := extractContent([]byte(html), u, "text/html")

	if !strings.Contains(got, "revenue up 15%") {
		t.Error("readability should preserve article content")
	}
	if !strings.Contains(got, "price target") {
		t.Error("readability should preserve analyst section")
	}
	// Nav, sidebar, footer noise should be stripped.
	if strings.Contains(got, "Cookie preferences") {
		t.Error("readability should strip footer noise")
	}
	if strings.Contains(got, "sidebar widget") {
		t.Error("readability should strip sidebar noise")
	}
	if strings.Contains(got, "analytics.track") {
		t.Error("readability should strip scripts")
	}
	// Title should appear in the header.
	if !strings.Contains(got, "MRVL Stock Analysis") {
		t.Error("readability should include the article title")
	}
	// Result should be smaller than the raw HTML.
	if len(got) >= len(html) {
		t.Errorf("readability output (%d bytes) should be smaller than raw HTML (%d bytes)", len(got), len(html))
	}
}

func TestExtractContent_NonHTMLPassthrough(t *testing.T) {
	json := `{"status": "ok", "data": [1,2,3]}`
	u, _ := url.Parse("https://api.example.com/v1/data")
	got := extractContent([]byte(json), u, "application/json")
	if got != json {
		t.Errorf("non-HTML should pass through unchanged, got %q", got)
	}
}

func TestExtractContent_FallbackForMinimalHTML(t *testing.T) {
	// Page with no article structure — readability should fail,
	// fallback to regex-based extraction.
	html := `<html><body><div>Just a short div</div></body></html>`
	u, _ := url.Parse("https://example.com")
	got := extractContent([]byte(html), u, "text/html")
	if !strings.Contains(got, "Just a short div") {
		t.Errorf("fallback should preserve text content, got %q", got)
	}
}

func TestExtractContent_LargePageSizeReduction(t *testing.T) {
	// Build a realistic large HTML page: small article surrounded by
	// massive boilerplate (nav, sidebar, footer, comments, ads).
	var sb strings.Builder
	sb.WriteString(`<html><head><title>Test Article</title>
		<style>body{margin:0} .ad{display:block}</style>
		<script>window.analytics={}</script></head><body>`)
	// Nav with 50 links
	sb.WriteString(`<nav>`)
	for i := range 50 {
		sb.WriteString(fmt.Sprintf(`<a href="/page/%d">Navigation Link %d with some descriptive text</a>`, i, i))
	}
	sb.WriteString(`</nav>`)
	// Sidebar with recommendations
	sb.WriteString(`<aside class="sidebar">`)
	for i := range 30 {
		sb.WriteString(fmt.Sprintf(`<div class="recommendation"><h3>Recommended Article %d</h3><p>Some teaser text for recommendation %d that adds context</p></div>`, i, i))
	}
	sb.WriteString(`</aside>`)
	// The actual article
	sb.WriteString(`<article><h1>Test Article Title</h1><p>By Test Author</p>`)
	for i := range 5 {
		sb.WriteString(fmt.Sprintf(`<p>This is paragraph %d of the actual article content that contains meaningful information the reader cares about.</p>`, i))
	}
	sb.WriteString(`</article>`)
	// Footer
	sb.WriteString(`<footer>`)
	for i := range 20 {
		sb.WriteString(fmt.Sprintf(`<a href="/legal/%d">Legal Link %d</a> | `, i, i))
	}
	sb.WriteString(`<p>Copyright 2026. All rights reserved. Terms of Service. Privacy Policy. Cookie preferences.</p></footer>`)
	// Comments section
	sb.WriteString(`<div class="comments">`)
	for i := range 40 {
		sb.WriteString(fmt.Sprintf(`<div class="comment"><span class="user">User%d</span><p>This is comment number %d with some opinion about the article that adds noise to the content</p></div>`, i, i))
	}
	sb.WriteString(`</div>`)
	// Ads and tracking scripts
	for range 10 {
		sb.WriteString(`<script>var _gaq=_gaq||[];_gaq.push(['_trackPageview']);</script>`)
	}
	sb.WriteString(`</body></html>`)
	raw := sb.String()

	u, _ := url.Parse("https://example.com/articles/test")
	got := extractContent([]byte(raw), u, "text/html; charset=utf-8")

	if !strings.Contains(got, "actual article content") {
		t.Error("readability should preserve article body")
	}
	// The readability output should be dramatically smaller.
	ratio := float64(len(got)) / float64(len(raw))
	if ratio > 0.3 {
		t.Errorf("readability should significantly reduce size: raw=%d extracted=%d ratio=%.2f", len(raw), len(got), ratio)
	}
	t.Logf("size reduction: %d → %d bytes (%.0f%% reduction)", len(raw), len(got), (1-ratio)*100)
}

func TestExtractContent_MalformedHTML(t *testing.T) {
	// Unclosed tags, nested errors — should not panic.
	html := `<html><body><div><p>Unclosed paragraph<div>Nested wrong</p></div>
		<script>broken<script>double script</script><p>Still readable</p></body>`
	u, _ := url.Parse("https://example.com")
	got := extractContent([]byte(html), u, "text/html")
	// Should not be empty and should not panic.
	if len(got) == 0 {
		t.Error("malformed HTML should still produce output")
	}
}

func TestExtractContent_EmptyBody(t *testing.T) {
	u, _ := url.Parse("https://example.com")
	got := extractContent([]byte{}, u, "text/html")
	if got != "" {
		t.Errorf("empty body should produce empty output, got %q", got)
	}
}

func TestExtractContent_ContentTypeVariants(t *testing.T) {
	article := `<html><body><article><h1>Title</h1><p>Article body text that is long enough to pass the 200-byte threshold for readability extraction to succeed properly in this test case.</p></article></body></html>`
	u, _ := url.Parse("https://example.com")

	cases := []struct {
		ct   string
		name string
	}{
		{"text/html", "plain html"},
		{"text/html; charset=utf-8", "html with charset"},
		{"TEXT/HTML", "uppercase html"},
		{"application/xhtml+xml", "xhtml (has html in body)"},
	}
	for _, tc := range cases {
		got := extractContent([]byte(article), u, tc.ct)
		if !strings.Contains(got, "Article body text") {
			t.Errorf("%s: expected article content, got %q", tc.name, got)
		}
	}
}

func TestExtractContent_PlainTextContentType(t *testing.T) {
	text := "This is plain text, not HTML at all. No tags here."
	u, _ := url.Parse("https://example.com/data.txt")
	got := extractContent([]byte(text), u, "text/plain")
	if got != text {
		t.Errorf("plain text should pass through unchanged, got %q", got)
	}
}

func TestExtractContent_ReadabilityShortContentFallback(t *testing.T) {
	// Page where readability extracts too little text (<200 bytes),
	// should fall back to regex extraction.
	html := `<html><body>
		<div class="app-shell">
			<div class="dashboard">Loading...</div>
			<div class="widget">CPU: 45%</div>
			<div class="widget">Memory: 2.1GB</div>
		</div>
	</body></html>`
	u, _ := url.Parse("https://example.com/dashboard")
	got := extractContent([]byte(html), u, "text/html")
	// Should fall back and include all div text.
	if !strings.Contains(got, "CPU: 45%") || !strings.Contains(got, "Memory: 2.1GB") {
		t.Errorf("short readability result should trigger fallback, got %q", got)
	}
}

func TestWebSearchTool(t *testing.T) {
	tool := NewWebSearchTool()
	_, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{}})
	if err == nil {
		t.Error("expected error for missing query")
	}
	res, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{"query": "foo bar & baz"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "not configured") {
		t.Errorf("expected not-configured message, got %q", res.Content)
	}
	// The stub must mark itself as is_error so the model treats it as a
	// failure rather than as actionable advice — see web.go for the
	// fabricated-URL incident this guards against.
	if !res.IsError {
		t.Errorf("expected IsError=true on the WebSearch stub result")
	}
	if !strings.Contains(res.Content, "Do NOT fabricate article URLs") {
		t.Errorf("expected fabrication warning in stub message, got %q", res.Content)
	}
	// The redirect must point at opencli (the supported search backend in
	// this build) and echo the original query verbatim so the model can
	// reuse it when constructing the opencli command.
	if !strings.Contains(res.Content, "opencli") {
		t.Errorf("expected opencli redirect in stub message, got %q", res.Content)
	}
	if !strings.Contains(res.Content, "foo bar & baz") {
		t.Errorf("expected original query echoed in stub message, got %q", res.Content)
	}
	if backend, _ := res.Metadata["preferred_backend"].(string); backend != "opencli" {
		t.Errorf("expected preferred_backend=opencli in metadata, got %q", backend)
	}
}

func TestNotebookEditTool_Basic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nb.ipynb")

	nb := map[string]any{
		"cells": []any{
			map[string]any{"cell_type": "code", "source": []any{"print('old')\n"}, "outputs": []any{}, "execution_count": nil},
			map[string]any{"cell_type": "markdown", "source": []any{"# Header\n"}},
		},
		"metadata":       map[string]any{"kernelspec": map[string]any{"display_name": "Python 3"}},
		"nbformat":       4,
		"nbformat_minor": 5,
	}
	raw, _ := json.Marshal(nb)
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewNotebookEditTool(nil)
	_, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{
		"path":       p,
		"cell_index": 0.0,
		"new_source": "print('new')\nprint('two')\n",
	}})
	if err != nil {
		t.Fatalf("notebook edit: %v", err)
	}

	updated, _ := os.ReadFile(p)
	var parsed map[string]any
	if err := json.Unmarshal(updated, &parsed); err != nil {
		t.Fatal(err)
	}
	cells := parsed["cells"].([]any)
	cell0 := cells[0].(map[string]any)
	src := cell0["source"].([]any)
	if len(src) != 2 || src[0].(string) != "print('new')\n" {
		t.Errorf("cell source = %+v", src)
	}
	// nbformat preserved.
	if parsed["nbformat"].(float64) != 4 {
		t.Errorf("nbformat lost: %v", parsed["nbformat"])
	}
}

func TestNotebookEditTool_ConfinedToWorkDir(t *testing.T) {
	workDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "nb.ipynb")
	raw, _ := json.Marshal(map[string]any{"cells": []any{map[string]any{"cell_type": "code", "source": []any{"x"}}}})
	if err := os.WriteFile(outside, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewNotebookEditTool(nil)
	_, err := tool.Execute(context.Background(), ToolCall{WorkDir: workDir, Input: map[string]any{
		"path": outside, "cell_index": 0.0, "new_source": "changed",
	}})
	if err == nil || !strings.Contains(err.Error(), "outside of the allowed workspace") {
		t.Fatalf("edit outside the work folder: err = %v, want a workspace refusal", err)
	}
	after, _ := os.ReadFile(outside)
	if string(after) != string(raw) {
		t.Fatal("notebook outside the work folder was changed")
	}

	// A hard link inside the work folder is replaced, not edited in place.
	link := filepath.Join(workDir, "link.ipynb")
	if err := os.Link(outside, link); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	if _, err := tool.Execute(context.Background(), ToolCall{WorkDir: workDir, Input: map[string]any{
		"path": link, "cell_index": 0.0, "new_source": "changed",
	}}); err != nil {
		t.Fatal(err)
	}
	after, _ = os.ReadFile(outside)
	if string(after) != string(raw) {
		t.Fatal("edit through a hard link changed the file outside the work folder")
	}
}

func TestNotebookEditTool_RejectsNonIpynb(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "not.json")
	if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewNotebookEditTool(nil)
	_, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{
		"path": p, "cell_index": 0.0, "new_source": "x",
	}})
	if err == nil {
		t.Error("expected error for non-ipynb path")
	}
}

func TestNotebookEditTool_OutOfRangeIndex(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nb.ipynb")
	nb := map[string]any{"cells": []any{map[string]any{"cell_type": "code", "source": []any{}}}, "nbformat": 4, "nbformat_minor": 5}
	raw, _ := json.Marshal(nb)
	os.WriteFile(p, raw, 0o644)

	tool := NewNotebookEditTool(nil)
	_, err := tool.Execute(context.Background(), ToolCall{Input: map[string]any{
		"path": p, "cell_index": 5.0, "new_source": "x",
	}})
	if err == nil {
		t.Error("expected error for out-of-range index")
	}
}

func TestSplitNotebookSource(t *testing.T) {
	got := splitNotebookSource("a\nb\nc\n")
	want := []any{"a\n", "b\n", "c\n"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, v := range got {
		if v != want[i] {
			t.Errorf("[%d] = %q, want %q", i, v, want[i])
		}
	}
}
