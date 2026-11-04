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

func TestAppDisplayNameIn(t *testing.T) {
	for _, c := range []struct{ app, lang, want string }{
		{"wechat", "zh-CN", "微信"},
		{"WeChat", "zh-CN", "微信"},
		{"weixin", "zh", "微信"},
		{"feishu", "zh-CN", "飞书"},
		{"slack", "zh-CN", "Slack"},
		{"pages", "zh-CN", "Pages"},
		{"wechat", "en", "WeChat"},
		{"wechat", "", "WeChat"},
	} {
		if got := AppDisplayNameIn(c.app, c.lang); got != c.want {
			t.Errorf("AppDisplayNameIn(%q, %q) = %q, want %q", c.app, c.lang, got, c.want)
		}
	}
}
