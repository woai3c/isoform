package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCredentialRootsRejectUnsafeHome(t *testing.T) {
	for _, test := range []struct {
		name     string
		override string
	}{
		{"missing home", ""},
		{"relative override", "relative-data"},
		{"unexpanded override", "~/.isoform-agent"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cwd := t.TempDir()
			t.Chdir(cwd)
			t.Setenv("HOME", "")
			t.Setenv("USERPROFILE", "")
			t.Setenv(agentHomeEnv, test.override)

			for _, connector := range []struct {
				name string
				save func() error
			}{
				{"slack", func() error {
					return saveSlackLocalCredentials(&slackLocalCredentials{BotToken: "bot", AppToken: "app"})
				}},
				{"discord", func() error { return saveDiscordLocalCredentials(&discordLocalCredentials{BotToken: "bot"}) }},
				{"telegram", func() error { return saveTelegramLocalCredentials(&telegramLocalCredentials{BotToken: "bot"}) }},
				{"feishu", func() error {
					return saveFeishuLocalCredentials(&feishuLocalCredentials{AppID: "id", AppSecret: "secret"})
				}},
				{"lark", func() error {
					return saveFeishuLocalCredentials(&feishuLocalCredentials{Brand: "lark", AppID: "id", AppSecret: "secret"})
				}},
				{"wechat", func() error { return saveWeChatCredentials(&wechatCredentials{BotToken: "bot"}) }},
			} {
				t.Run(connector.name, func(t *testing.T) {
					if err := connector.save(); err == nil {
						t.Error("credential save accepted an invalid data root")
					}
				})
			}
			saveTelegramOffset(42)
			(&wechatMonitor{}).saveCursor("cursor")
			entries, err := os.ReadDir(cwd)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("invalid data root created working-directory data: %v", entries)
			}
		})
	}
}

func TestCredentialRootsDoNotReadOrRemoveWorkingDirectoryData(t *testing.T) {
	for _, override := range []string{"", "relative-data", "~/.isoform-agent"} {
		t.Run("override="+override, func(t *testing.T) {
			cwd := t.TempDir()
			t.Chdir(cwd)
			t.Setenv("HOME", "")
			t.Setenv("USERPROFILE", "")
			t.Setenv(agentHomeEnv, override)
			for _, connector := range []string{"slack", "discord", "telegram", "feishu", "lark", "wechat"} {
				dir := filepath.Join(cwd, override, connector)
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				for _, name := range []string{"credentials.json", "credentials.archived.json", "cursor.json", "offset"} {
					data := `{"bot_token":"bot","app_token":"app","app_id":"id","app_secret":"secret","cursor":"cursor"}`
					if name == "offset" {
						data = "42"
					}
					if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := flavorHomeListing(t, cwd)
			app := &App{}
			for _, connector := range []struct {
				name string
				load func() bool
			}{
				{"slack", func() bool {
					return loadSlackLocalCredentials() != nil || archivedSlackLocalCredentials() != nil || app.GetSlackArchived().Has
				}},
				{"discord", func() bool {
					return loadDiscordLocalCredentials() != nil || archivedDiscordLocalCredentials() != nil || app.GetDiscordArchived().Has
				}},
				{"telegram", func() bool {
					return loadTelegramLocalCredentials() != nil || archivedTelegramLocalCredentials() != nil || app.GetTelegramArchived().Has || loadTelegramOffset() != 0
				}},
				{"feishu", func() bool {
					return loadFeishuLocalCredentials("feishu") != nil || loadArchivedFeishuLocalCredentials("feishu") != nil
				}},
				{"lark", func() bool {
					return loadFeishuLocalCredentials("lark") != nil || loadArchivedFeishuLocalCredentials("lark") != nil
				}},
				{"wechat", func() bool { return loadWeChatCredentials() != nil || (&wechatMonitor{}).loadCursor() != "" }},
			} {
				t.Run(connector.name, func(t *testing.T) {
					if connector.load() {
						t.Error("read working-directory data using an invalid root")
					}
				})
			}
			removeSlackLocalCredentials()
			removeDiscordLocalCredentials()
			removeTelegramLocalCredentials()
			app.DiscardSlackArchived()
			app.DiscardDiscordArchived()
			app.DiscardTelegramArchived()
			for _, disconnect := range []func() error{app.disconnectSlackLocal, app.disconnectDiscordLocal, app.disconnectTelegramLocal, app.DisconnectWeChat} {
				if err := disconnect(); err == nil {
					t.Error("disconnect accepted an invalid data root")
				}
			}
			for _, brand := range []string{"feishu", "lark"} {
				archiveFeishuLocalCredentials(brand)
				removeFeishuLocalCredentials(brand)
				removeArchivedFeishuLocalCredentials(brand)
			}
			saveTelegramOffset(99)
			(&wechatMonitor{}).saveCursor("changed")
			if after := flavorHomeListing(t, cwd); !reflect.DeepEqual(before, after) {
				t.Error("invalid data root modified working-directory data")
			}
		})
	}
}

func TestCredentialRootsHonorAbsoluteHome(t *testing.T) {
	home, dataRoot := isolatedFlavorHome(t)
	before := flavorHomeListing(t, home)
	for _, connector := range []struct {
		name string
		save func() error
		load func() bool
	}{
		{"slack", func() error {
			return saveSlackLocalCredentials(&slackLocalCredentials{BotToken: "bot", AppToken: "app"})
		}, func() bool { return loadSlackLocalCredentials() != nil }},
		{"discord", func() error { return saveDiscordLocalCredentials(&discordLocalCredentials{BotToken: "bot"}) }, func() bool { return loadDiscordLocalCredentials() != nil }},
		{"telegram", func() error { return saveTelegramLocalCredentials(&telegramLocalCredentials{BotToken: "bot"}) }, func() bool { return loadTelegramLocalCredentials() != nil }},
		{"feishu", func() error {
			return saveFeishuLocalCredentials(&feishuLocalCredentials{AppID: "id", AppSecret: "secret"})
		}, func() bool { return loadFeishuLocalCredentials("feishu") != nil }},
		{"lark", func() error {
			return saveFeishuLocalCredentials(&feishuLocalCredentials{Brand: "lark", AppID: "id", AppSecret: "secret"})
		}, func() bool { return loadFeishuLocalCredentials("lark") != nil }},
		{"wechat", func() error { return saveWeChatCredentials(&wechatCredentials{BotToken: "bot"}) }, func() bool { return loadWeChatCredentials() != nil }},
	} {
		t.Run(connector.name, func(t *testing.T) {
			if err := connector.save(); err != nil {
				t.Fatal(err)
			}
			if !connector.load() {
				t.Error("saved credentials were not loaded from the override")
			}
			info, err := os.Stat(filepath.Join(dataRoot, connector.name, "credentials.json"))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0600 {
				t.Errorf("credential mode = %v, want 0600", info.Mode().Perm())
			}
		})
	}
	if after := flavorHomeListing(t, home); !reflect.DeepEqual(before, after) {
		t.Error("credential saves changed the published-app home")
	}
}
