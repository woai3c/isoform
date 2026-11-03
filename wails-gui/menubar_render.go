package main

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"
)

// Read the same token declarations as the Wails frontend. In particular, the
// violet migration changes this single source, not a second native palette.
//
//go:embed frontend/src/index.css
var menuBarAppCSS string

//go:embed menubar_window_assets/popover.html
var menuBarHTML string

var menuBarCSSBlock = regexp.MustCompile(`(?s)([^{}]+)\{([^{}]*)\}`)
var menuBarCSSDeclaration = regexp.MustCompile(`(?m)^\s*--(?:color-|shadow-)[\w-]+\s*:[^;\n]+;`)

func menuBarTokenCSS(source string) string {
	var css strings.Builder
	for _, block := range menuBarCSSBlock.FindAllStringSubmatch(source, -1) {
		selector := strings.TrimSpace(block[1])
		switch true {
		case strings.HasSuffix(selector, "@theme"):
			selector = ":root"
		case strings.Contains(selector, `:root[data-theme="onedark"]`):
			selector = `:root[data-theme="onedark"]`
		default:
			continue
		}
		css.WriteString(selector + "{\n")
		for _, declaration := range menuBarCSSDeclaration.FindAllString(block[2], -1) {
			css.WriteString(declaration + "\n")
		}
		css.WriteString("}\n")
	}
	return css.String()
}
func renderMenuBarHTML(view menuBarView) string {
	data, _ := json.Marshal(view) // HTML-safe JSON escapes untrusted source text.
	mark, _ := embeddedMenuBarIcons.ReadFile("assets/menubar-icon@3x.png")
	return strings.NewReplacer("/*__TOKENS__*/", menuBarTokenCSS(menuBarAppCSS), "/*__MODEL__*/null", string(data), "/*__MARK__*/", "data:image/png;base64,"+base64.StdEncoding.EncodeToString(mark)).Replace(menuBarHTML)
}
