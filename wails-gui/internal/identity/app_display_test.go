package identity

import "testing"

func TestAppDisplayName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Known chat apps with brand-correct casing
		{"wechat", "WeChat"},
		{"WeChat", "WeChat"},
		{"  TELEGRAM  ", "Telegram"},
		{"discord", "Discord"},
		{"ringcentral", "RingCentral"},
		{"imessage", "iMessage"},
		{"qq", "QQ"},
		{"line", "LINE"},
		{"whatsapp", "WhatsApp"},
		{"kakaotalk", "KakaoTalk"},
		{"facebook_messenger", "Facebook Messenger"},

		// Non-chat known apps
		{"gmail", "Gmail"},
		{"github", "GitHub"},
		{"chatgpt", "ChatGPT"},

		// Unknown apps fall through to title-case
		{"foo", "Foo"},
		{"some_new_app", "Some New App"},
		{"hyphen-app", "Hyphen App"},

		// Empty / whitespace
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := AppDisplayName(c.in)
			if got != c.want {
				t.Errorf("AppDisplayName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
