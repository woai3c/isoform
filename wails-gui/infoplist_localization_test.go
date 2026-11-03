package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The macOS permission prompts follow the system language (fresh-install
// rehearsal, 2026-10-27: English prompts under zh-CN). Every usage
// description in Info.plist has an en and a zh-Hans InfoPlist.strings entry,
// the English one word for word, and the build installs the .lproj folders.
func TestPermissionPromptsAreLocalized(t *testing.T) {
	plist, err := os.ReadFile("build/darwin/Info.plist")
	if err != nil {
		t.Fatal(err)
	}
	usage := map[string]string{}
	for _, m := range regexp.MustCompile(`<key>(NS\w+UsageDescription)</key>\s*<string>([^<]*)</string>`).FindAllStringSubmatch(string(plist), -1) {
		usage[m[1]] = m[2]
	}
	if len(usage) == 0 {
		t.Fatal("no usage descriptions found")
	}
	devPlist, err := os.ReadFile("build/darwin/Info.dev.plist")
	if err != nil {
		t.Fatal(err)
	}
	devUsage := map[string]string{}
	for _, m := range regexp.MustCompile(`<key>(NS\w+UsageDescription)</key>\s*<string>([^<]*)</string>`).FindAllStringSubmatch(string(devPlist), -1) {
		devUsage[m[1]] = m[2]
	}
	if len(devUsage) != len(usage) {
		t.Errorf("Info.dev.plist has %d usage descriptions, Info.plist has %d", len(devUsage), len(usage))
	}
	for key, value := range usage {
		if devUsage[key] != value {
			t.Errorf("Info.dev.plist %s differs from Info.plist", key)
		}
	}
	entry := regexp.MustCompile(`(?m)^"(\w+)" = "(.*)";$`)
	for _, lang := range []string{"en", "zh-Hans"} {
		raw, err := os.ReadFile("build/darwin/" + lang + ".lproj/InfoPlist.strings")
		if err != nil {
			t.Fatal(err)
		}
		strings_ := map[string]string{}
		for _, m := range entry.FindAllStringSubmatch(string(raw), -1) {
			strings_[m[1]] = m[2]
		}
		for key, english := range usage {
			got, ok := strings_[key]
			if !ok || got == "" {
				t.Errorf("%s: missing %s", lang, key)
				continue
			}
			if lang == "en" && got != english {
				t.Errorf("en %s differs from Info.plist", key)
			}
			if lang == "zh-Hans" && got == english {
				t.Errorf("zh-Hans %s is not translated", key)
			}
		}
		if len(strings_) != len(usage) {
			t.Errorf("%s has %d entries, Info.plist has %d", lang, len(strings_), len(usage))
		}
	}
	script, err := os.ReadFile("scripts/build-wails.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), `"${WAILS_DIR}/build/darwin/"*.lproj`) {
		t.Fatal("build-wails.sh no longer installs the localized permission prompts")
	}
}
