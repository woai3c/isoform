package identity

import "testing"

func TestIsChatApp(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"wechat", true},
		{"WeChat", true},
		{"  TELEGRAM ", true},
		{"slack", true},
		{"discord", true},
		{"feishu", true},
		{"imessage", true},
		{"messages", true},
		{"line", true},
		{"qq", true},

		// Email-ish / service-ish / agent-ish apps that should NOT
		// auto-bucket. These are the recurring offenders in real
		// captures — Mail.app newsletters, GitHub PR comments,
		// Sentry alerts, ChatGPT/Claude transcript messages.
		{"mail", false},
		{"gmail", false},
		{"chatgpt", false},
		{"claude_web", false},
		{"github", false},
		{"linear", false},
		{"notion", false},
		{"sentry", false},
		{"substack", false},

		// Edge cases
		{"", false},
		{"   ", false},
		{"unknown_app_id", false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := IsChatApp(c.in)
			if got != c.want {
				t.Errorf("IsChatApp(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}
