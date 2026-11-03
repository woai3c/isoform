package main

import (
	"context"
	"os"

	"yansu-agent/agent"
)

// installCLIProbeFakes keeps the user's installed Claude Code and Codex out
// of a plain go test, for the whole test binary; the package TestMain
// (main_test.go) calls it first. Any test that starts the agent builds the
// real assistant router, and its probe runs `claude` / `codex` for sign-in
// and version checks as soon as the data folder under HOME holds the
// installed Isoform CLI. The research readiness check runs `claude auth
// status` the same way. Those runs use the user's sign-in and let the CLI
// rewrite ~/.claude.json. The real probes run only when asked:
// YANSU_CLAUDE_INTEGRATION=1 or YANSU_CODEX_INTEGRATION=1 for the router,
// YANSU_CLAUDE_INTEGRATION=1 or YANSU_RESEARCH_INTEGRATION=1 for research.
// Tests of the probe itself call probeAssistantRoute with a fake command.
func installCLIProbeFakes() {
	realRouteProbe := assistantRouteProbe
	assistantRouteProbe = func(ctx context.Context, cfg agent.ExternalAgentConfig) agent.AssistantState {
		optIn := "YANSU_CLAUDE_INTEGRATION"
		if cfg.Format == "codex" {
			optIn = "YANSU_CODEX_INTEGRATION"
		}
		if os.Getenv(optIn) == "1" {
			return realRouteProbe(ctx, cfg)
		}
		return agent.AssistantState{Reason: "real CLI probes are off in tests (set " + optIn + "=1)"}
	}
	handoffStopProbe = func(*App) handoffStopProof { return handoffStopProof{} }
	handoffStartAIReady = func(*App) bool { return true }
	chatSendAIReady = func(*App) bool { return true }
	realResearchProbe := claudeResearchProbe
	claudeResearchProbe = func() bool {
		if os.Getenv("YANSU_CLAUDE_INTEGRATION") == "1" || os.Getenv("YANSU_RESEARCH_INTEGRATION") == "1" {
			return realResearchProbe()
		}
		return false
	}
}
