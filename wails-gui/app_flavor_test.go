package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAppFlavorFromEnvironmentAndLdflag(t *testing.T) {
	previous := buildFlavor
	t.Cleanup(func() { buildFlavor = previous })

	t.Setenv(appFlavorEnv, "")
	buildFlavor = ""
	if appFlavor() != appFlavorYansu || productName() != "Yansu" || configDirForFlavor(appFlavor()) != ".yansu-agent" {
		t.Fatalf("the default flavor is Yansu: %s %s", appFlavor(), productName())
	}
	buildFlavor = "isoform"
	if appFlavor() != appFlavorIsoform || productName() != "Isoform" || configDirForFlavor(appFlavor()) != ".isoform-agent" {
		t.Fatalf("the ldflag selects Isoform: %s %s", appFlavor(), productName())
	}
	t.Setenv(appFlavorEnv, " Yansu ")
	if appFlavor() != appFlavorIsoform {
		t.Fatal("the compiled flavor must ignore an inherited release flavor")
	}
	t.Setenv(appFlavorEnv, "isoform")
	buildFlavor = ""
	if appFlavor() != appFlavorIsoform {
		t.Fatal("the environment selects Isoform")
	}
	t.Setenv(appFlavorEnv, "mystery")
	if appFlavor() != appFlavorYansu {
		t.Fatal("an unknown flavor falls back to Yansu")
	}
}

func TestExportAgentHomeFollowsTheFlavorUnlessSet(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	t.Setenv(agentHomeEnv, "")
	exportAgentHome()
	if got := os.Getenv(agentHomeEnv); got != filepath.Join(home, configDirName) {
		t.Fatalf("YANSU_AGENT_HOME = %q, want the flavor's folder", got)
	}
	t.Setenv(agentHomeEnv, "/tmp/elsewhere")
	exportAgentHome()
	if got := os.Getenv(agentHomeEnv); got != "/tmp/elsewhere" {
		t.Fatalf("an explicit YANSU_AGENT_HOME must be kept: %q", got)
	}
}

func TestCompiledIsoformRefusesInheritedPublishedHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv(appFlavorEnv, appFlavorYansu)
	previousBuild, previousDir := buildFlavor, configDirName
	t.Cleanup(func() { buildFlavor, configDirName = previousBuild, previousDir })
	buildFlavor = appFlavorIsoform
	configDirName = configDirForFlavor(appFlavor())
	t.Setenv(agentHomeEnv, filepath.Join(home, ".yansu-agent"))
	if err := exportAgentHome(); err == nil {
		t.Fatal("compiled Isoform accepted an inherited published-app home")
	}
	t.Setenv(agentHomeEnv, "~/.isoform-agent")
	if err := exportAgentHome(); err == nil {
		t.Fatal("unexpanded home accepted")
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Fatalf("invalid roots created data: %v, %v", entries, err)
	}
}
