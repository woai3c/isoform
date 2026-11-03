//go:build !darwin

package main

import (
	"context"
	"errors"
)

// systemMailSupported reports whether this platform has Mail.app. Only
// macOS does.
func systemMailSupported() bool { return false }

func mailAutomationStatus(askUser bool) mailAutomationState { return mailAutomationUnknown }

func mailRunning() bool { return false }

func mailProcessID() int { return 0 }

func requestMailAutomation() bool { return false }

func runMailScript(ctx context.Context, script string) ([]byte, error) {
	return nil, errors.New("Mail is read only on macOS")
}
