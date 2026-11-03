package main

import (
	"context"
	"fmt"
	"strings"

	"wails-gui/storage"
)

func (a *App) bindIntegrationSelfIdentity(ctx context.Context, app, handleID, displayHandle string) error {
	app = strings.TrimSpace(strings.ToLower(app))
	handleID = strings.TrimSpace(handleID)
	displayHandle = strings.TrimSpace(displayHandle)
	if app == "" || handleID == "" {
		return nil
	}
	store, err := a.requireStore()
	if err != nil {
		return err
	}

	selfName, err := integrationSelfDisplayName(ctx, store, displayHandle)
	if err != nil {
		return err
	}
	self, err := store.EnsureSelf(ctx, selfName, nil)
	if err != nil {
		return fmt.Errorf("ensure self: %w", err)
	}
	if strings.TrimSpace(self.DisplayName) == "" && selfName != "" {
		self.DisplayName = selfName
		self, err = store.UpdatePrincipal(ctx, self)
		if err != nil {
			return fmt.Errorf("name self: %w", err)
		}
	}
	if err := removeStaleIntegrationHandle(ctx, store, self.ID, app, handleID, displayHandle); err != nil {
		return err
	}
	if _, err := store.BindAppHandle(ctx, self.ID, app, handleID, displayHandle); err != nil {
		return fmt.Errorf("bind %s handle: %w", app, err)
	}

	a.mu.Lock()
	a.cachedDisplayName = ""
	a.mu.Unlock()
	return nil
}

// integrationSelfDisplayName is the name a bound integration gives the self
// principal: the stored name, else the integration's display handle, else
// "". It never makes one up: an empty name keeps the main chat's "What should
// I call you?" step open (main_chat_setup.go).
func integrationSelfDisplayName(ctx context.Context, store *storage.Store, integrationName string) (string, error) {
	rows, err := store.ListPrincipalsByKind(ctx, storage.PrincipalKindSelf)
	if err != nil {
		return "", fmt.Errorf("list self principals: %w", err)
	}
	if len(rows) > 0 && strings.TrimSpace(rows[0].DisplayName) != "" {
		return strings.TrimSpace(rows[0].DisplayName), nil
	}
	return strings.TrimSpace(integrationName), nil
}

func removeStaleIntegrationHandle(
	ctx context.Context,
	store *storage.Store,
	selfID string,
	app string,
	handleID string,
	displayHandle string,
) error {
	principal, err := store.ResolveByAppHandle(ctx, app, handleID)
	if err != nil || principal == nil {
		return err
	}
	handles, err := store.ListAppHandlesByPrincipal(ctx, principal.ID)
	if err != nil {
		return fmt.Errorf("list existing %s handles: %w", app, err)
	}
	for _, handle := range handles {
		if handle.App != app || handle.HandleID != handleID {
			continue
		}
		if principal.ID == selfID && handle.DisplayHandle == displayHandle {
			continue
		}
		if err := store.UnbindAppHandle(ctx, handle.ID); err != nil {
			return fmt.Errorf("replace stale %s handle: %w", app, err)
		}
	}
	return nil
}
