package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Keep the fake published-app home separate from the initially empty override.
// A recursive listing also catches unexpected directories created under HOME.
func isolatedFlavorHome(t *testing.T) (home, dataRoot string) {
	t.Helper()
	home, dataRoot = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(agentHomeEnv, "  "+dataRoot+"  ")
	t.Setenv("YANSU_MOBILE_STATE_DIR", "")
	t.Setenv(topicsStateDirEnvVar, "")
	legacy := filepath.Join(home, ".yansu-agent")
	fixtures := map[string]string{
		tokenFileName:                        "published-app-token\n",
		configFileName:                       `{"sync_interval_minutes":123,"worktree_root":"/published/worktrees"}`,
		aiProviderConfigFileName:             `{"provider":"openai","apiKey":"published-app-key"}`,
		filepath.Join("memory", "legacy.md"): "# Published app memory\n\nKeep this private.\n",
	}
	for name, contents := range fixtures {
		path := filepath.Join(legacy, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home, dataRoot
}

func flavorHomeListing(t *testing.T, home string) map[string]string {
	t.Helper()
	listing := make(map[string]string)
	err := filepath.WalkDir(home, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(home, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			listing[relative+"/"] = ""
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		listing[relative] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return listing
}

func TestFlavorDataRootsHonorOverride(t *testing.T) {
	home, dataRoot := isolatedFlavorHome(t)
	before := flavorHomeListing(t, home)
	for _, test := range []struct {
		name     string
		resolve  func() (string, error)
		relative string
	}{
		{"memory", memoryDir, "memory"},
		{"knowledge", knowledgeDir, "knowledge"},
		{"transcripts", transcriptDir, "transcripts"},
		{"token", yansuTokenPath, tokenFileName},
		{"config", yansuConfigPath, configFileName},
		{"AI provider", aiProviderConfigPath, aiProviderConfigFileName},
		{"Composio", composioByokPath, composioByokFileName},
		{"clones", defaultCloneBaseDir, "repos"},
		{"worktrees", defaultWorktreeBaseDir, "worktrees"},
		{"magpie", func() (string, error) { return defaultMagpieStateDir(), nil }, magpieStateDirName},
		{"text channels", textChannelDir, ""},
		{"contacts consent", contactsConsentPath, contactsConsentFileName},
		{"calendar consent", calendarConsentPath, calendarConsentFileName},
		{"dream settings", dreamSettingsPath, "dream-settings.json"},
		{"dream history", dreamHistoryPath, "dream-runs.json"},
		{"mobile", mobileStateDirectory, "mobile"},
		{"mobile briefs", mobileBriefStateDir, "mobile"},
		{"topics", topicsStateDir, "topics"},
		{"active session", func() (string, error) { return activeSessionFile(), nil }, "active-session"},
		{"soul", soulFilePath, "SOUL.md"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := test.resolve()
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(dataRoot, test.relative); got != want {
				t.Fatalf("data path = %q, want %q", got, want)
			}
		})
	}

	app := &App{}
	for _, platform := range []string{"discord", "slack", "telegram", "feishu"} {
		t.Run(platform, func(t *testing.T) {
			scan, state, err := app.platformDirs(platform)
			if err != nil {
				t.Fatal(err)
			}
			wantScan := ""
			if platform != "telegram" {
				wantScan = filepath.Join(dataRoot, platform+"-scan")
			}
			if scan != wantScan || state != filepath.Join(dataRoot, platform+"-dynamic-scan") {
				t.Fatalf("platform directories = (%q, %q), outside the override %q", scan, state, dataRoot)
			}
		})
	}

	reader := newBrowserHistoryReader(app)
	if reader.home != home {
		t.Fatalf("browser source home = %q, want user home %q", reader.home, home)
	}
	if want := filepath.Join(dataRoot, "tmp", browserHistoryScratch); reader.scratch != want {
		t.Fatalf("browser scratch = %q, want %q", reader.scratch, want)
	}
	if after := flavorHomeListing(t, home); !reflect.DeepEqual(after, before) {
		t.Fatalf("published-app home changed: before=%v after=%v", before, after)
	}
}

func TestFlavorDataReadsAndWritesLeavePublishedDataUntouched(t *testing.T) {
	home, dataRoot := isolatedFlavorHome(t)
	before := flavorHomeListing(t, home)
	app := &App{}

	if token, err := loadYansuToken(); !errors.Is(err, os.ErrNotExist) || token != "" {
		t.Fatalf("empty override read a token: token=%q err=%v", token, err)
	}
	if cfg := loadConfig(); cfg.SyncInterval != 0 || cfg.WorktreeRoot != "" {
		t.Fatalf("empty override read published configuration: %+v", cfg)
	}
	if cfg := loadAIProviderConfig(); cfg.APIKey != "" || cfg.Provider != aiProviderManaged {
		t.Fatalf("empty override read published provider configuration: %+v", cfg)
	}
	if entries, err := app.GetMemoryEntries(); err != nil || len(entries) != 0 {
		t.Fatalf("empty override read published memory: entries=%v err=%v", entries, err)
	}
	if _, err := app.GetMemoryEntry("legacy"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published memory must be absent from override: %v", err)
	}
	if err := clearYansuToken(); err != nil {
		t.Fatal(err)
	}

	if err := saveConfig(yansuConfig{SyncInterval: 456}); err != nil {
		t.Fatal(err)
	}
	if got := loadConfig().SyncInterval; got != 456 {
		t.Fatalf("saved override sync interval = %d, want 456", got)
	}
	if err := saveAIProviderConfig(AIProviderConfig{Provider: "openai", APIKey: "override-key"}); err != nil {
		t.Fatal(err)
	}
	if got := loadAIProviderConfig().APIKey; got != "override-key" {
		t.Fatalf("saved override provider key = %q", got)
	}
	const content = "# Override memory\n\nOnly in the development home.\n"
	saved, err := app.SaveMemoryEntry("legacy", content)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Title != "Override memory" || saved.Source != "user_edit" || saved.Dimension != "custom" || !strings.Contains(saved.Content, "Only in the development home.") {
		t.Fatalf("override memory lost user content or attribution: %+v", saved)
	}
	writeActiveSessionFile("override-session")
	// Saving preserves user ownership and the read envelope in canonical OKF.
	if got, err := os.ReadFile(filepath.Join(dataRoot, "memory", "legacy.md")); err != nil || string(got) != saved.Content {
		t.Fatalf("override memory = %q, err=%v; want %q", got, err, saved.Content)
	}
	doc, ok := parseOKFDocument(saved.Content)
	provenance := memoryReadProvenanceFromContent(saved.Content)
	if !ok || okfString(doc.Frontmatter, "title") != "Override memory" || !strings.Contains(doc.Body, "Only in the development home.\n") ||
		saved.Source != "user_edit" || provenance.Withheld || !reflect.DeepEqual(provenance.Kinds, []string{"manual"}) {
		t.Fatalf("override memory lost body, ownership or provenance: %+v", saved)
	}
	if got, err := os.ReadFile(filepath.Join(dataRoot, "active-session")); err != nil || string(got) != "override-session" {
		t.Fatalf("override active-session = %q, err=%v", got, err)
	}
	for _, name := range []string{configFileName, aiProviderConfigFileName} {
		if _, err := os.Stat(filepath.Join(dataRoot, name)); err != nil {
			t.Fatalf("override file %q: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dataRoot, "memory", memoryHistoryDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("saving new override memory must not snapshot published memory: %v", err)
	}
	if after := flavorHomeListing(t, home); !reflect.DeepEqual(after, before) {
		t.Fatalf("published-app home changed: before=%v after=%v", before, after)
	}
}

func TestIsoformMemoryDefaultHomeIgnoresPublishedData(t *testing.T) {
	home, _ := isolatedFlavorHome(t)
	t.Setenv(agentHomeEnv, "")
	t.Setenv(appFlavorEnv, appFlavorIsoform)
	previousConfigDir := configDirName
	configDirName = configDirForFlavor(appFlavorIsoform)
	t.Cleanup(func() { configDirName = previousConfigDir })
	legacy := filepath.Join(home, ".yansu-agent")
	before := flavorHomeListing(t, legacy)
	app := &App{}

	if entries, err := app.GetMemoryEntries(); err != nil || len(entries) != 0 {
		t.Fatalf("default Isoform home read published memory: entries=%v err=%v", entries, err)
	}
	if _, err := app.GetMemoryEntry("legacy"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("published memory must be absent from default Isoform home: %v", err)
	}
	const content = "# Isoform memory\n"
	saved, err := app.SaveMemoryEntry("legacy", content)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Title != "Isoform memory" || saved.Source != "user_edit" || saved.Dimension != "custom" {
		t.Fatalf("default Isoform memory lost user content or attribution: %+v", saved)
	}
	path := filepath.Join(home, ".isoform-agent", "memory", "legacy.md")
	if got, err := os.ReadFile(path); err != nil || string(got) != saved.Content {
		t.Fatalf("default Isoform memory = %q, err=%v; want %q", got, err, saved.Content)
	}
	doc, ok := parseOKFDocument(saved.Content)
	provenance := memoryReadProvenanceFromContent(saved.Content)
	if !ok || okfString(doc.Frontmatter, "title") != "Isoform memory" || strings.TrimSpace(doc.Body) != "" || saved.Source != "user_edit" ||
		provenance.Withheld || !reflect.DeepEqual(provenance.Kinds, []string{"manual"}) {
		t.Fatalf("default Isoform memory lost body, ownership or provenance: %+v", saved)
	}
	if after := flavorHomeListing(t, legacy); !reflect.DeepEqual(after, before) {
		t.Fatalf("published-app home changed: before=%v after=%v", before, after)
	}
}

func TestFlavorBundledToolsIgnorePublishedInstallAndCopyIntoOverride(t *testing.T) {
	home, dataRoot := isolatedFlavorHome(t)
	t.Setenv("PATH", t.TempDir())
	previousInstallDone := opencliInstallDone
	opencliInstallDone = make(chan struct{})
	close(opencliInstallDone)
	t.Cleanup(func() { opencliInstallDone = previousInstallDone })

	tools := []struct {
		name     string
		resolve  func() string
		relative string
	}{
		{"bun", installedBunPath, filepath.Join("bin", bunExeName)},
		{"git", installedGitPath, filepath.Join("git", "bin", gitExeName)},
		{"FFmpeg", installedFFmpegPath, filepath.Join("bin", ffmpegExeName)},
		{"OpenCLI", installedOpenCLIPath, filepath.Join("opencli", "bin", opencliExeName)},
		{"agent CLI", installedYansuCLIPath, filepath.Join("bin", bundledYansuCLIExecutableName())},
	}
	for _, tool := range tools {
		path := filepath.Join(home, ".yansu-agent", tool.relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("published-app-"+tool.name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	before := flavorHomeListing(t, home)
	for _, tool := range tools {
		t.Run(tool.name, func(t *testing.T) {
			if got := tool.resolve(); got != "" {
				t.Fatalf("empty override found published binary %q", got)
			}
		})
	}

	// Discovery trusts the executable's bundle, never the working directory.
	// Use inert bytes beside this test executable; do not replace a real bundle.
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if name := filepath.Base(executable); !strings.HasSuffix(name, ".test") && !strings.HasSuffix(name, ".test.exe") {
		t.Fatalf("fixture requires a Go test executable: %s", executable)
	}
	bundleRoot := filepath.Join(filepath.Dir(executable), yansuCLIBundleDirName)
	if err := os.Mkdir(bundleRoot, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			t.Skip("test executable already has a CLI bundle; leaving it untouched")
		}
		t.Fatal(err)
	}
	bin := filepath.Join(bundleRoot, "bin")
	source := filepath.Join(bin, bundledYansuCLIExecutableName())
	t.Cleanup(func() {
		for _, path := range []string{source, bin, bundleRoot} {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Errorf("remove CLI test fixture %q: %v", path, err)
			}
		}
	})
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	const bundledContent = "development-agent-cli"
	if err := os.WriteFile(source, []byte(bundledContent), 0o755); err != nil {
		t.Fatal(err)
	}
	installed, err := ensureBundledYansuCLI()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dataRoot, "bin", bundledYansuCLIExecutableName())
	if installed != want || installedYansuCLIPath() != want {
		t.Fatalf("bundled CLI install = %q, lookup = %q, want %q", installed, installedYansuCLIPath(), want)
	}
	if got, err := os.ReadFile(want); err != nil || string(got) != bundledContent {
		t.Fatalf("installed override CLI = %q, err=%v", got, err)
	}
	if after := flavorHomeListing(t, home); !reflect.DeepEqual(after, before) {
		t.Fatalf("published-app home changed: before=%v after=%v", before, after)
	}
}
