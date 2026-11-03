package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/skratchdot/open-golang/open"
)

// OpenScanStageFolder opens the OS file manager at the directory most relevant
// to the given scan stage for the specified platform.
func (a *App) OpenScanStageFolder(platform, stage string) error {
	target, err := a.resolveScanStageDir(platform, stage)
	if err != nil {
		return err
	}
	if _, err := os.Stat(target); os.IsNotExist(err) {
		return fmt.Errorf("directory does not exist: %s", target)
	}
	return open.Start(target)
}

func (a *App) resolveScanStageDir(platform, stage string) (string, error) {
	scanDir, stateDir, err := a.platformDirs(platform)
	if err != nil {
		return "", err
	}

	switch stage {
	case "fetching", "segmenting":
		// Conversation JSON files written during fetch/segment.
		if scanDir != "" {
			return filepath.Join(scanDir, "conversations"), nil
		}
		return filepath.Join(stateDir, "conversations"), nil

	case "triaging":
		// Triage output is ephemeral (workDir); open the conversations dir
		// so users can inspect the source data that was triaged.
		if scanDir != "" {
			return filepath.Join(scanDir, "conversations"), nil
		}
		return filepath.Join(stateDir, "conversations"), nil

	case "extracting":
		// memory_retry.json and extraction state live in stateDir.
		return stateDir, nil

	case "analyzing":
		// Analysis specs are ephemeral; open the scan base dir which
		// contains the persistent conversation + triage data.
		if scanDir != "" {
			return scanDir, nil
		}
		return stateDir, nil

	case "generating":
		// known_processed.json location varies by platform.
		// Discord/Slack: scanDir, Telegram/Feishu: stateDir.
		switch platform {
		case "discord", "slack":
			return scanDir, nil
		default:
			return stateDir, nil
		}

	default:
		// Fallback: open whichever base dir exists.
		if scanDir != "" {
			return scanDir, nil
		}
		return stateDir, nil
	}
}

// platformDirs returns (scanDir, stateDir) for the given platform.
// scanDir may be empty when the platform stores everything under stateDir.
func (a *App) platformDirs(platform string) (scanDir, stateDir string, err error) {
	base, err := flavorDataDir()
	if err != nil {
		return "", "", fmt.Errorf("cannot determine home directory: %w", err)
	}

	switch platform {
	case "discord":
		return filepath.Join(base, "discord-scan"), filepath.Join(base, "discord-dynamic-scan"), nil
	case "slack":
		return filepath.Join(base, "slack-scan"), filepath.Join(base, "slack-dynamic-scan"), nil
	case "telegram":
		return "", filepath.Join(base, "telegram-dynamic-scan"), nil
	case "feishu":
		return filepath.Join(base, "feishu-scan"), filepath.Join(base, "feishu-dynamic-scan"), nil
	default:
		return "", "", fmt.Errorf("unknown platform: %s", platform)
	}
}
