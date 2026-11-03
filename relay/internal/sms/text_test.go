package sms

import (
	"bytes"
	"strings"
	"testing"
)

func TestClassifyKeyword(t *testing.T) {
	cases := map[string]Keyword{
		"STOP": KeywordStop, "stop": KeywordStop, " Stop ": KeywordStop, "STOP.": KeywordStop, "stop!": KeywordStop,
		"UNSUBSCRIBE": KeywordStop, "unsubscribe": KeywordStop, "Cancel": KeywordStop, "END": KeywordStop,
		"quit": KeywordStop, "STOPALL": KeywordStop, "optout": KeywordStop, "Revoke": KeywordStop,
		"START": KeywordStart, "start": KeywordStart, "Unstop": KeywordStart,
		"HELP": KeywordHelp, "help": KeywordHelp, "Info": KeywordHelp,
		"STOP\x00": KeywordStop, "\u200bstop": KeywordStop, "S\u200bTOP": KeywordStop,
		"Y": KeywordNone, "N": KeywordNone, "1": KeywordNone, "please stop": KeywordNone,
		"stop it": KeywordNone, "": KeywordNone, "STOPP": KeywordNone, "yes": KeywordNone,
	}
	for body, want := range cases {
		if got := ClassifyKeyword(body); got != want {
			t.Fatalf("%q: %v want %v", body, got, want)
		}
	}
}

func TestNormalizeNumber(t *testing.T) {
	good := map[string]string{
		"+14155550123":       "+14155550123",
		" +1 (415) 555-0123": "+14155550123",
		"+44 20 7946 0100":   "+442079460100",
		"+86.000.0013.8000":  "+8600000138000",
	}
	for raw, want := range good {
		if got, ok := NormalizeNumber(raw); !ok || got != want {
			t.Fatalf("%q: %q %t", raw, got, ok)
		}
	}
	for _, raw := range []string{"", "4155550123", "+0123456789", "+1415", "+1415555012345678", "+1415555O123", "tel:+14155550123"} {
		if _, ok := NormalizeNumber(raw); ok {
			t.Fatalf("%q accepted", raw)
		}
	}
}

func TestValidBodyAndCleanReply(t *testing.T) {
	if !ValidBody("Isoform: \"Deck\" is ready.\nReply R.") || !ValidBody(strings.Repeat("a", MaxBodyRunes)) || !ValidBody(strings.Repeat("好", MaxUnicodeBodyRunes)) {
		t.Fatal("a valid body was refused")
	}
	for _, body := range []string{"", "   ", strings.Repeat("a", MaxBodyRunes+1), strings.Repeat("好", MaxUnicodeBodyRunes+1), strings.Repeat("a", MaxUnicodeBodyRunes) + "\u201c", "bell\a", "tab\there", string([]byte{0xff, 0xfe})} {
		if ValidBody(body) {
			t.Fatalf("%q accepted", body)
		}
	}
	if got := cleanReply("  Y\x00\r\n"); got != "Y" {
		t.Fatalf("clean %q", got)
	}
	if got := cleanReply(strings.Repeat("长", MaxReplyRunes+10)); len([]rune(got)) != MaxReplyRunes {
		t.Fatalf("long reply kept %d runes", len([]rune(got)))
	}
}

func TestSegmentsAndTheLengthCaps(t *testing.T) {
	cases := map[string]int{
		strings.Repeat("a", 160): 1, strings.Repeat("a", 161): 2, strings.Repeat("a", MaxBodyRunes): 3,
		strings.Repeat("好", 70): 1, strings.Repeat("好", 71): 2, strings.Repeat("好", MaxUnicodeBodyRunes): 3,
	}
	for body, want := range cases {
		if got := Segments(body); got != want {
			t.Fatalf("%d runes: %d segments, want %d", len([]rune(body)), got, want)
		}
	}
}

func TestRelayTextsAreValidSingleSegments(t *testing.T) {
	texts := []string{stopReply, startText(DefaultDailyCap), helpText(DefaultDailyCap, "help@example.com"), codeText("en", "123456", DefaultDailyCap), codeText("zh-CN", "123456", DefaultDailyCap)}
	for _, text := range texts {
		if !strings.HasPrefix(text, "Isoform") || !ValidBody(text) || Segments(text) > 1 {
			t.Fatalf("relay text %q (%d segments)", text, Segments(text))
		}
	}
	if codeText("fr", "123456", 10) != codeText("en", "123456", 10) {
		t.Fatal("an unknown language does not fall back to English")
	}
}

func TestVault(t *testing.T) {
	key := bytes.Repeat([]byte{7}, KeySize)
	v, err := NewVault(VaultKeys{Seal: key})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := v.Seal("+14155550123", numberLabel("hub-a"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "4155550123") {
		t.Fatal("sealed number is readable")
	}
	if opened, err := v.Open(sealed, numberLabel("hub-a")); err != nil || opened != "+14155550123" {
		t.Fatalf("open %q %v", opened, err)
	}
	if _, err = v.Open(sealed, numberLabel("hub-b")); err == nil {
		t.Fatal("a number opened under another hub's label")
	}
	other, _ := NewVault(VaultKeys{Seal: bytes.Repeat([]byte{8}, KeySize)})
	if _, err = other.Open(sealed, numberLabel("hub-a")); err == nil {
		t.Fatal("a number opened under another key")
	}
	if v.HashNumber("+14155550123") != v.HashNumber("+14155550123") || v.HashNumber("+14155550123") == other.HashNumber("+14155550123") {
		t.Fatal("number hash is not stable and keyed")
	}
	if v.HashCode("hub-a", "n", "123456") == v.HashCode("hub-b", "n", "123456") {
		t.Fatal("code hash is not bound to the hub")
	}
	if _, err = NewVault(VaultKeys{Seal: []byte("short")}); err == nil {
		t.Fatal("short key accepted")
	}
	if _, err = NewVault(VaultKeys{Seal: key, Previous: key}); err == nil {
		t.Fatal("the previous key may not be the current one")
	}
}

func TestVaultKeyIDsAndRotation(t *testing.T) {
	oldKey, newKey := bytes.Repeat([]byte{7}, KeySize), bytes.Repeat([]byte{8}, KeySize)
	before, _ := NewVault(VaultKeys{Seal: oldKey})
	sealed, err := before.Seal("+14155550123", numberLabel("hub-a"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sealed, KeyID(oldKey)+".") || !before.Current(sealed) {
		t.Fatalf("sealed value %q does not name its key", sealed)
	}
	// A value sealed before key ids existed opens under its key.
	legacy := strings.TrimPrefix(sealed, KeyID(oldKey)+".")
	if opened, err := before.Open(legacy, numberLabel("hub-a")); err != nil || opened != "+14155550123" || before.Current(legacy) {
		t.Fatalf("legacy value: %q %v", opened, err)
	}
	during, err := NewVault(VaultKeys{Seal: newKey, Previous: oldKey, Hash: oldKey})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{sealed, legacy} {
		if opened, err := during.Open(value, numberLabel("hub-a")); err != nil || opened != "+14155550123" || during.Current(value) {
			t.Fatalf("old value during the rotation: %q %v", opened, err)
		}
	}
	if during.HashNumber("+14155550123") != before.HashNumber("+14155550123") {
		t.Fatal("the rotation changed the number hash")
	}
	after, _ := NewVault(VaultKeys{Seal: newKey, Hash: oldKey})
	if _, err = after.Open(sealed, numberLabel("hub-a")); err == nil {
		t.Fatal("a value under a dropped key opened")
	}
	if _, err = after.Open("ffffffff."+legacy, numberLabel("hub-a")); err == nil {
		t.Fatal("a value naming an unknown key opened")
	}
}

func TestHubBody(t *testing.T) {
	allowed := []string{
		"Isoform: \"Q4 deck\" is ready to review. Nothing was sent.",
		"Isoform: \"report.pdf\" needs your answer. Reply A to answer in chat, L for later.",
		"Isoform: \"build.sh\" starts in 5 min in Room 4.",
		"Isoform: Standup (9:00-10:30) overlaps Review (10:00-11:00).",
		"Isoform: \"Plan\" is due on Oct 16 with no progress yet. Reply T for tomorrow, N if not yours.",
		"Isoform: notes for \"Sync\" are ready: 3 decisions, 12 follow-ups.",
		"Isoform：“周报”需要你回答。回复 A 在聊天中回答，回复 L 稍后。",
		"Isoform: you did \"Invoice 2026\" 4 times.",
	}
	for _, body := range allowed {
		if !HubBody(body) {
			t.Fatalf("refused %q", body)
		}
	}
	refused := []string{
		"\"Deck\" is ready.",
		"isoform: lower case brand",
		" Isoform: leading space",
		"Isoform: open https://example.com/x",
		"Isoform: open HTTP://EXAMPLE.COM",
		"Isoform: open www.example.org",
		"Isoform: see bit.ly/abc123",
		"Isoform: see example.com",
		"Isoform: see evil.zip/login",
		"Isoform：请看 example.com 的说明",
		"Isoform: call 415-555-0199",
		"Isoform: call (415) 555 0199",
		"Isoform: call +44 20 7946 0100",
		"Isoform: call 4155550199",
		"Isoform: code 1234567",
	}
	for _, body := range refused {
		if HubBody(body) {
			t.Fatalf("accepted %q", body)
		}
	}
}

func TestLoadConfig(t *testing.T) {
	env := map[string]string{
		"ISOFORM_RELAY_SMS_PROVIDER":                 "twilio",
		"ISOFORM_RELAY_TWILIO_ACCOUNT_SID":           testAccount,
		"ISOFORM_RELAY_TWILIO_AUTH_TOKEN":            testToken,
		"ISOFORM_RELAY_TWILIO_MESSAGING_SERVICE_SID": testService,
		"ISOFORM_RELAY_SMS_WEBHOOK_URL":              testWebhook,
		"ISOFORM_RELAY_SMS_KEY":                      "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
		"ISOFORM_RELAY_SMS_HELP_CONTACT":             "help@example.com",
	}
	getenv := func(key string) string { return env[key] }
	cfg, err := LoadConfig(getenv)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled() || cfg.DailyCap != 10 || cfg.MonthlyCap != 200 || len(cfg.Countries) != 1 || cfg.Countries[0] != "+1" || len(cfg.Key) != KeySize {
		t.Fatalf("defaults %+v", cfg)
	}
	if off, err := LoadConfig(func(string) string { return "" }); err != nil || off.Enabled() {
		t.Fatalf("empty settings: %+v %v", off, err)
	}
	env["ISOFORM_RELAY_SMS_COUNTRIES"] = "+1, +44"
	env["ISOFORM_RELAY_SMS_DAILY_CAP"] = "5"
	if cfg, err = LoadConfig(getenv); err != nil || cfg.DailyCap != 5 || len(cfg.Countries) != 2 || !cfg.allowedNumber("+442079460100") || cfg.allowedNumber("+8600000138000") {
		t.Fatalf("overrides %+v %v", cfg, err)
	}
	if cfg.KeywordAnswersPerHour != DefaultKeywordAnswersPerHour || cfg.PreviousKey != nil || cfg.HashKey != nil {
		t.Fatalf("key and budget defaults %+v", cfg)
	}
	env["ISOFORM_RELAY_SMS_KEY_PREVIOUS"] = "ICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj8="
	env["ISOFORM_RELAY_SMS_HASH_KEY"] = "ICEiIyQlJicoKSorLC0uLzAxMjM0NTY3ODk6Ozw9Pj8="
	if cfg, err = LoadConfig(getenv); err != nil || len(cfg.PreviousKey) != KeySize || len(cfg.HashKey) != KeySize {
		t.Fatalf("rotation keys %+v %v", cfg, err)
	}
	if _, err = NewVault(cfg.VaultKeys()); err != nil {
		t.Fatal(err)
	}
	bad := map[string]string{
		"ISOFORM_RELAY_SMS_KEY_PREVIOUS":             "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
		"ISOFORM_RELAY_SMS_HASH_KEY":                 "c2hvcnQ=",
		"ISOFORM_RELAY_SMS_KEYWORD_ANSWERS_PER_HOUR": "0",
		"ISOFORM_RELAY_SMS_PROVIDER":                 "carrier-pigeon",
		"ISOFORM_RELAY_SMS_KEY":                      "c2hvcnQ=",
		"ISOFORM_RELAY_SMS_HELP_CONTACT":             "",
		"ISOFORM_RELAY_SMS_DAILY_CAP":                "0",
		"ISOFORM_RELAY_SMS_MONTHLY_CAP":              "lots",
		"ISOFORM_RELAY_SMS_COUNTRIES":                "1",
		"ISOFORM_RELAY_SMS_WEBHOOK_URL":              "http://relay.example.com/v1/sms/inbound",
	}
	for key, value := range bad {
		saved := env[key]
		env[key] = value
		if _, err := LoadConfig(getenv); err == nil {
			t.Fatalf("%s=%q accepted", key, value)
		}
		env[key] = saved
	}
}
