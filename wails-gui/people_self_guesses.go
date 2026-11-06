package main

import (
	"context"
	"sort"
	"strings"

	"wails-gui/internal/identity"
	"wails-gui/storage"
)

type SelfSenderGuess struct {
	SelfID   string `json:"selfId"`
	PersonID string `json:"personId"`
	Name     string `json:"name"`
	App      string `json:"app"`
}

// Only offer an observed sender whose name or handle exactly matches the
// user's confirmed name, alias, or handle. A guess is never a binding.
func (a *App) peopleSelfSenderGuesses(ctx context.Context, rows []PrincipalWithStatsDTO, cache *peopleReadGateCache) []SelfSenderGuess {
	out := []SelfSenderGuess{}
	if a.IsClosedEyesModeEnabled() {
		return out
	}
	var self *PrincipalDTO
	for i := range rows {
		if rows[i].Principal.Kind == storage.PrincipalKindSelf {
			self = &rows[i].Principal
			break
		}
	}
	if self == nil {
		return out
	}
	selfRow, err := a.store.GetPrincipal(ctx, self.ID)
	if err != nil || !a.peopleProfileAllowsModelRead(ctx, selfRow, cache) {
		return out
	}
	names := personNames(self.DisplayName, self.Aliases)
	boundApps := map[string]bool{}
	for _, handle := range self.AppHandles {
		if !a.peopleAppReadableCached(ctx, handle.App, cache) {
			continue
		}
		boundApps[strings.ToLower(strings.TrimSpace(handle.App))] = true
		for _, value := range []string{handle.DisplayHandle, handle.HandleID} {
			if key := threadNorm(value); key != "" {
				names[key] = true
			}
		}
	}
	notSame, err := a.store.PeopleNotSame(ctx)
	if err != nil {
		return out
	}
	for _, row := range rows {
		p := row.Principal
		if p.Kind != storage.PrincipalKindAnonymous || notSame([]string{self.ID, p.ID}) {
			continue
		}
		for _, handle := range p.AppHandles {
			app := strings.ToLower(strings.TrimSpace(handle.App))
			if boundApps[app] || !identity.IsChatApp(app) || !a.peopleAppReadableCached(ctx, app, cache) {
				continue
			}
			if !names[threadNorm(p.DisplayName)] && !names[threadNorm(handle.DisplayHandle)] && !names[threadNorm(handle.HandleID)] {
				continue
			}
			principal, err := a.store.GetPrincipal(ctx, p.ID)
			if err != nil || !a.peopleProfileAllowsModelRead(ctx, principal, cache) || !a.peopleMetadataReadable(ctx, p.ID, cache) {
				continue
			}
			name := strings.TrimSpace(handle.DisplayHandle)
			if name == "" {
				name = strings.TrimSpace(p.DisplayName)
			}
			if name != "" {
				out = append(out, SelfSenderGuess{SelfID: self.ID, PersonID: p.ID, Name: name, App: app})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].App != out[j].App {
			return out[i].App < out[j].App
		}
		return out[i].PersonID < out[j].PersonID
	})
	return out
}
