package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestNativeBrandTokensMatchPublishedPalette(t *testing.T) {
	data, err := os.ReadFile("../docs/design-system/tokens.json")
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Color struct {
			Tokens []struct {
				Name  string            `json:"name"`
				Value map[string]string `json:"value"`
			} `json:"tokens"`
		} `json:"color"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	for _, theme := range []string{"paper", "onedark"} {
		declarations := brandThemeDeclarations(theme == "onedark")
		for _, token := range catalog.Color.Tokens {
			value := token.Value[theme]
			if strings.HasPrefix(value, "{") {
				value = "var(--" + strings.Trim(value, "{}") + ")"
			}
			if !strings.Contains(declarations, "--"+token.Name+":"+value+";") {
				t.Errorf("native %s is missing %s = %s", theme, token.Name, value)
			}
		}
	}
	if !strings.Contains(brandThemeCSS(), `[data-theme="onedark"]{`) {
		t.Fatal("native toast must support the selected dark theme")
	}
}
