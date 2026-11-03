//go:build !darwin

package main

import (
	"context"
	"errors"

	"wails-gui/storage"
)

func (a *App) sendMainChatMail(ctx context.Context, send storage.OutboundSendRow) error {
	return errors.New("Mail sending is available only on macOS")
}
