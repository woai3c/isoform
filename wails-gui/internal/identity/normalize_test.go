package identity

import "testing"

func TestNormalizeSenderName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// trailing timestamps
		{"bare_hhmm", "Maya Chen 10:25", "Maya Chen"},
		{"bare_hhmm_ampm", "Alice 8:01 AM", "Alice"},
		{"today_at_ampm", "Alice Today at 11:51 PM", "Alice"},
		{"yesterday_at_ampm", "Bob Yesterday at 8:01 AM", "Bob"},
		{"chinese_today_no_space", "alex_halcyon 今天11:51", "alex_halcyon"},
		{"chinese_today_with_space", "alex_halcyon 今天 11:51", "alex_halcyon"},
		{"chinese_yesterday", "alex_halcyon 昨天 10:34", "alex_halcyon"},
		{"chinese_qiantian", "alex_halcyon 前天 08:12", "alex_halcyon"},
		{"absolute_date", "alex_halcyon 2031/7/1 08:15", "alex_halcyon"},
		{"single_digit_hour", "Priya 8:01", "Priya"},

		// role badges (preceded by space; bare badge alone is preserved)
		{"discord_op_original_poster", "maya_chen OP Original Poster", "maya_chen"},
		{"discord_original_poster", "maya_chen Original Poster", "maya_chen"},
		{"discord_server_owner", "Boss Server Owner", "Boss"},
		{"discord_bot_suffix", "Yansu BOT", "Yansu"},
		{"slack_app_suffix", "Github App", "Github"},

		// stacked: badge + timestamp
		{"badge_then_timestamp_en", "maya_chen OP Original Poster 10:12", "maya_chen"},
		{"badge_then_timestamp_zh", "Priya Server Owner 今天 09:30", "Priya"},
		{"two_badges_then_timestamp", "Boss Server Owner Original Poster 10:00", "Boss"},

		// message body bleed — single-token sender stays after sentence strip
		{"body_bleed_then_timestamp", "maya_chen Sample welcome message! 10:12", "maya_chen"},
		{"body_bleed_then_badge_and_ts", "maya_chen Sample welcome message! OP Original Poster 10:12", "maya_chen"},

		// preservation: legitimate names that LOOK like badges/timestamps
		// must NOT be stripped to empty.
		{"name_is_literal_bot", "Bot", "Bot"},
		{"name_is_literal_app", "App", "App"},
		{"name_is_literal_server_owner", "Server Owner", "Server Owner"},
		// timestamp without surrounding context — entire input is a
		// timestamp, can't strip without going empty, so we keep raw.
		{"only_timestamp_kept_as_is", "10:25", "10:25"},

		// preservation: multi-word display names should NOT get sentence-bled
		// down to a single token even when one trailing word looks like a
		// sentence start (heuristic explicitly refuses to truncate to
		// multi-token results, but it would never accept a remaining
		// "Mary Jane" → "Mary" split either).
		{"multiword_display_name", "Mary Jane Doe", "Mary Jane Doe"},
		{"multiword_with_timestamp", "Mary Jane 10:25", "Mary Jane"},

		// CJK pure names
		{"chinese_name_clean", "张三", "张三"},
		{"chinese_name_with_timestamp", "张三 08:01", "张三"},

		// Trailing bare date (Discord shows date-only for older messages)
		{"date_no_comma", "halcyon 2031/6/29", "halcyon"},
		{"date_with_comma", "halcyon 2031/6/30,", "halcyon"},
		{"date_with_two_digit_md", "halcyon 2031/06/30", "halcyon"},
		{"date_then_timestamp", "Alice 2031/6/30 10:25", "Alice"}, // existing timestamp regex handles this first
		// Stacked: server tag THEN bare date
		{"server_tag_then_date_no_comma", "alex_halcyon Server Tag: HLC 2031/6/30", "alex_halcyon"},
		{"server_tag_then_date_with_comma", "alex_halcyon Server Tag: HLC 2031/6/30,", "alex_halcyon"},
		{"server_tag_then_date_cjk", "张三 Server Tag: ABCD 2031/6/29,", "张三"},
		// Preservation: a digit-containing username (no slash-date pattern) stays intact.
		{"username_with_digits_no_date", "user1234", "user1234"},
		// A bare year-looking suffix that doesn't match the regex stays.
		{"not_a_date_suffix", "Alice 2031", "Alice 2031"},

		// Discord server tag (clan badge)
		{"server_tag_simple", "alex_halcyon Server Tag: HLC", "alex_halcyon"},
		{"server_tag_with_backslash", "maya_chen Server Tag: X\\", "maya_chen"},
		{"server_tag_with_digits", "Alice Server Tag: ABCD1234", "Alice"},
		{"server_tag_after_timestamp", "alex_halcyon Server Tag: HLC 10:25", "alex_halcyon"},
		{"server_tag_then_badge_then_ts", "alex_halcyon Server Tag: HLC OP Original Poster 10:25", "alex_halcyon"},
		{"server_tag_cjk_name", "张三 Server Tag: ABCD", "张三"},
		// preservation: a user literally named "Server" with no clan tag
		// after it must not be stripped (regex requires the trailing
		// value to be non-empty + non-whitespace).
		{"name_with_server_word_kept", "Server Admin", "Server Admin"},
		// no clan value → pattern doesn't match (regex needs at least one
		// non-whitespace char after "Tag:") → name kept intact, only the
		// outer TrimSpace fires.
		{"empty_tag_value_kept", "Bob Server Tag: ", "Bob Server Tag:"},

		// edge cases
		{"empty_returns_raw", "", ""},
		{"whitespace_returns_raw", "   ", "   "},
		{"already_clean", "Alex", "Alex"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizeSenderName(c.in)
			if got != c.want {
				t.Errorf("NormalizeSenderName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestPlausibleAutoContactName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		// fictional names / handles — accepted
		{"simple", "priya", true},
		{"two_word_name", "Maya Chen", true},
		{"cjk", "张三", true},
		{"handle_with_at", "@zhangsan", true},
		{"with_digits", "Michael_6888", true},
		{"single_token_bang", "Winner!", true}, // single token: not prose
		{"single_token_dotted", "a.b.c", true}, // single token: not prose
		// junk that bled in — rejected
		{"discord_welcome_card", "welcome Server Tag: X\\ This is a sample welcome!", false},
		{"server_tag_midstring", "maya_chen Server Tag: X\\ noise", false},
		{"multi_word_exclaim", "Hi everyone, this is a sample welcome!", false},
		{"multi_word_question", "anyone around to help?", false},
		{"prose_midsentence", "Sure. Let me check that for you", false},
		{"newline", "line one\nline two", false},
		{"too_long", "this is a very long string that is clearly a message body not a name at all", false},
		{"empty", "", false},
		{"whitespace", "   ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := PlausibleAutoContactName(c.in); got != c.want {
				t.Errorf("PlausibleAutoContactName(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
