package main

import "testing"

func TestMenuBarMadeAheadMetaNamesTheMadeAtTime(t *testing.T) {
	w := func(lang string) map[string]string { return menuBarWords(lang) }
	// 07:20 UTC on November 8 is 11:20 PM on November 7 in Los Angeles.
	night := MobileBriefV1{
		LocalDate: "2026-11-08", Timezone: "America/Los_Angeles", AsOf: "2026-11-08T07:20:00Z",
		Generation: &MobileBriefGeneration{Reason: mobileBriefGenerationMadeAhead},
	}
	if got := menuBarMadeAheadMeta(w("en"), "en", night); got != "Made last night at 11:20 PM" {
		t.Fatalf("en night line %q", got)
	}
	if got := menuBarMadeAheadMeta(w("zh-CN"), "zh-CN", night); got != "昨晚 23:20 生成" {
		t.Fatalf("zh night line %q", got)
	}
	day := MobileBriefV1{
		LocalDate: "2026-11-08", Timezone: "America/Los_Angeles", AsOf: "2026-11-08T15:00:00Z",
		Generation: &MobileBriefGeneration{Reason: mobileBriefGenerationMadeAhead},
	}
	yesterday := MobileBriefV1{
		LocalDate: "2026-11-08", Timezone: "America/Los_Angeles", AsOf: "2026-11-08T01:00:00Z",
		Generation: &MobileBriefGeneration{Reason: mobileBriefGenerationMadeAhead},
	}
	if got := menuBarMadeAheadMeta(w("en"), "en", yesterday); got != "Made yesterday at 5:00 PM" {
		t.Fatalf("en yesterday line %q", got)
	}
	if got := menuBarMadeAheadMeta(w("en"), "en", day); got != "Made at 7:00 AM" {
		t.Fatalf("en daytime line %q", got)
	}
}

// v1.0.2: an older day takes the date form; a brief with no time shows no phrase.
func TestMenuBarMadeAheadOlderDayAndNoTime(t *testing.T) {
	older := MobileBriefV1{
		LocalDate: "2026-11-08", Timezone: "America/Los_Angeles", AsOf: "2026-11-06T23:10:00Z",
		Generation: &MobileBriefGeneration{Reason: mobileBriefGenerationMadeAhead},
	}
	if got := menuBarMadeAheadMeta(menuBarWords("en"), "en", older); got != "Made Nov 6 at 3:10 PM" {
		t.Fatalf("en older-day line %q", got)
	}
	if got := menuBarMadeAheadMeta(menuBarWords("zh-CN"), "zh-CN", older); got != "11月6日 15:10 生成" {
		t.Fatalf("zh older-day line %q", got)
	}
	noTime := MobileBriefV1{LocalDate: "2026-11-08", Timezone: "America/Los_Angeles", Generation: &MobileBriefGeneration{Reason: mobileBriefGenerationMadeAhead}}
	if got := menuBarMadeAheadMeta(menuBarWords("en"), "en", noTime); got != "" {
		t.Fatalf("a brief with no time gave a phrase %q", got)
	}
}
