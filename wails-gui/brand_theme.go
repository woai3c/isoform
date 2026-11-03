package main

import (
	_ "embed"
	"regexp"
	"strconv"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/options"
)

// Native surfaces consume the same authored tokens as the WebViews. Embedding
// the source avoids another palette that can drift when the brand changes.
//
//go:embed frontend/src/index.css
var brandStylesheet string

var brandDeclaration = regexp.MustCompile(`(?m)^\s*(--(?:color|shadow)-[\w-]+):\s*([^;]+);`)

func brandThemeDeclarations(dark bool) string {
	selector := "@theme {"
	if dark {
		selector = `:root[data-theme="onedark"] {`
	}
	_, block, found := strings.Cut(brandStylesheet, selector)
	if !found {
		panic("missing brand theme: " + selector)
	}
	block, _, _ = strings.Cut(block, "\n}")
	var declarations strings.Builder
	for _, match := range brandDeclaration.FindAllStringSubmatch(block, -1) {
		declarations.WriteString(match[1] + ":" + match[2] + ";")
	}
	return declarations.String()
}

func brandThemeCSS() string {
	return ":root{" + brandThemeDeclarations(false) + "}:root.dark,:root[data-theme=\"onedark\"]{" + brandThemeDeclarations(true) + "}"
}

func brandWindowBackground() *options.RGBA {
	declarations := brandThemeDeclarations(currentThemePreference() == "onedark")
	_, value, found := strings.Cut(declarations, "--color-background:#")
	if !found {
		panic("missing brand background")
	}
	value, _, _ = strings.Cut(value, ";")
	hex, err := strconv.ParseUint(value, 16, 24)
	if err != nil {
		panic("invalid brand background: " + value)
	}
	return &options.RGBA{R: uint8(hex >> 16), G: uint8(hex >> 8), B: uint8(hex), A: 255}
}
