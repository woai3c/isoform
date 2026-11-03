package main

import (
	"log"
	"os"

	"wails-gui/internal/platform/shellenv"
)

// ── Model discovery (exposed to frontend via Wails) ──

// ModelInfo represents a single model option
type ModelInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Executor string `json:"executor"` // "claude" or "codex"
}

// ExecutorInfo describes an available executor and its models
type ExecutorInfo struct {
	Name      string      `json:"name"`
	Available bool        `json:"available"`
	Models    []ModelInfo `json:"models"`
	Default   string      `json:"default"`
}

// ListAvailableModels returns available model groups.
// Claude models are always returned -- execution goes through the in-process
// native agent loop.
// Codex models are only returned when the codex CLI is installed locally.
func (a *App) ListAvailableModels() []ExecutorInfo {
	var result []ExecutorInfo

	claudeCfg := claudeCodeModelConfig()

	// Claude models are always available -- execution goes through the
	// in-process native agent loop, so no local CLI check needed.
	result = append(result, ExecutorInfo{
		Name:      "claude",
		Available: true,
		Models:    buildClaudeModelOptions(claudeCfg),
		Default:   claudeCfg.ChatModel,
	})

	// Detect Codex CLI
	if modelCLIAvailable("codex") {
		result = append(result, ExecutorInfo{
			Name:      "codex",
			Available: true,
			Models: []ModelInfo{
				{ID: "gpt-5.5", Name: "GPT-5.5", Executor: "codex"},
				{ID: "gpt-5.4", Name: "GPT-5.4", Executor: "codex"},
				{ID: "gpt-5.4-mini", Name: "GPT-5.4 Mini", Executor: "codex"},
				{ID: "gpt-5.3-codex-spark", Name: "GPT-5.3 Codex Spark", Executor: "codex"},
			},
			Default: "gpt-5.5",
		})
	}

	return result
}

func modelCLIAvailable(name string) bool {
	if shellenv.ResolveCommandPath(name) != "" {
		return true
	}

	log.Printf("[models] %q not found (PATH=%q, shell=%q)", name, os.Getenv("PATH"), shellenv.LoginShell())
	return false
}
