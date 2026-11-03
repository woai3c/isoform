package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiscordScannerRootsDoNotUseWorkingDirectory(t *testing.T) {
	for _, override := range []string{"", "relative-data", "~/.isoform-agent"} {
		t.Run(override, func(t *testing.T) {
			t.Setenv("HOME", "")
			t.Setenv("USERPROFILE", "")
			t.Setenv(agentHomeEnv, override)
			cwd := t.TempDir()
			t.Chdir(cwd)
			for _, dir := range []string{"discord-dynamic-scan", filepath.Join(configDirName, "discord-scan", "conversations")} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join("discord-dynamic-scan", "state.json"), []byte(`{"pending_rescan":true}`), 0o600); err != nil {
				t.Fatal(err)
			}
			before := flavorHomeListing(t, cwd)
			app := &App{}
			scanner := NewDiscordIncrementalScanner(app)
			if scanner.loadState().PendingRescan {
				t.Error("invalid home read state from the working directory")
			}
			scanner.saveState(discordIncrementalScanState{})
			scanner.saveHistory([]DiscordDynamicScanRecord{{ID: "must-not-save"}})
			app.cleanupDiscordScanState()
			if after := flavorHomeListing(t, cwd); !reflect.DeepEqual(after, before) {
				t.Errorf("invalid home changed working-directory files: before=%v after=%v", before, after)
			}
		})
	}
}
