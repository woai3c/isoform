package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

// isoformChoiceFileName was Isoform's model and effort pick inside the magpie
// state dir. The pick lives in Isoform's own config now; the migration reads
// the file once and removes it.
const isoformChoiceFileName = "isoform-choice.json"

// runAgentCLI runs a magpie agent show command entirely inside Isoform's agent
// home: its HOME, its own magpie config and cache, and no gateway address, so
// it works before the gateway starts and never reads the user's ~/.claude or
// ~/.codex. Only the one-time choice migration below runs it.
func (s *magpieSidecar) runAgentCLI(ctx context.Context, args ...string) ([]byte, error) {
	home, err := s.agentHome()
	if err != nil {
		return nil, err
	}
	return s.runCLIEnv(ctx, append(agentSettingsEnv(home),
		"XDG_CONFIG_HOME="+filepath.Join(home, "config"),
		"XDG_CACHE_HOME="+filepath.Join(home, "cache"),
		"MAGPIE_ADDR=127.0.0.1:1",
	), args...)
}

// migrateAgentChoices moves the Claude Code, Codex and Isoform model and
// effort picks into Isoform's own config (AIProviderConfig.AgentChoices),
// once. Before this they lived in magpie's per-agent settings (written by the
// magpie CLI into Isoform's sandboxed agent home, never the user's folders)
// and in isoform-choice.json inside the magpie state dir. Runs already got
// them as flags; only the storage moves. A failed read or save retries on the
// next start: the marker is written only once every source was read.
func (a *App) migrateAgentChoices() {
	cfg := loadAIProviderConfig()
	if cfg.AgentChoicesMigrated {
		return
	}
	if a.magpie == nil || a.magpie.stateDir == "" {
		// Without a magpie state dir there is nowhere old picks could live.
		_, err := updateAIProviderConfig(func(cfg *AIProviderConfig) error {
			cfg.AgentChoicesMigrated = true
			return nil
		})
		if err != nil {
			log.Printf("[assistant-choice] choice migration not saved (retried on next start): %v", err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(a.contextOrBackground(), assistantChoiceTimeout)
	defer cancel()
	legacy := make(map[string]AgentModelChoice, len(choiceAgents))
	// The user's own magpie never held Isoform's picks; skip the show calls.
	if !a.magpie.External() {
		for _, ca := range choiceAgents {
			if ca.magpie == "" {
				continue
			}
			out, err := a.magpie.runAgentCLI(ctx, ca.magpie)
			if err != nil {
				log.Printf("[assistant-choice] choice migration not finished (retried on next start): %v", err)
				return
			}
			model, effort := parseMagpieAgentShow(string(out), magpieAgentDisplayName(ca.magpie))
			legacy[ca.id] = AgentModelChoice{Model: model, Effort: effort}
		}
	}
	legacy[answeringIsoform] = readLegacyIsoformChoice(a.magpie.stateDir)
	moved := false
	_, err := updateAIProviderConfig(func(cfg *AIProviderConfig) error {
		for id, choice := range legacy {
			// A pick saved while the old stores were being read wins too.
			if choice != (AgentModelChoice{}) && cfg.agentChoice(id) == (AgentModelChoice{}) {
				cfg.setAgentChoice(id, choice)
				moved = true
			}
		}
		cfg.AgentChoicesMigrated = true
		return nil
	})
	if err != nil {
		log.Printf("[assistant-choice] choice migration not saved (retried on next start): %v", err)
		return
	}
	// Nothing reads the old file anymore. The sandboxed agent-home settings
	// stay: they are inert and deleting them is the bundled magpie's business.
	if err := os.Remove(filepath.Join(a.magpie.stateDir, isoformChoiceFileName)); err != nil && !os.IsNotExist(err) {
		log.Printf("[assistant-choice] old %s not removed: %v", isoformChoiceFileName, err)
	}
	if moved {
		log.Printf("[assistant-choice] model and effort picks moved into Isoform's own config")
	}
}

// readLegacyIsoformChoice reads the pre-migration isoform-choice.json. A
// missing or unreadable file yields the zero choice.
func readLegacyIsoformChoice(stateDir string) AgentModelChoice {
	var choice AgentModelChoice
	if stateDir == "" {
		return choice
	}
	data, err := os.ReadFile(filepath.Join(stateDir, isoformChoiceFileName))
	if err == nil {
		_ = json.Unmarshal(data, &choice)
	}
	return choice
}
