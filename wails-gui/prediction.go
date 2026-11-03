package main

import (
	"context"
	"fmt"
	"log"

	"github.com/google/uuid"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"wails-gui/storage"
)

// This file contains the sidebar-group helpers shared by transcripts, handoffs,
// and legacy automation sessions. New proactive work surfaces as initiatives on
// Today instead of creating notification chat sessions.

const (
	automationsGroupName = "Automations"
	handoffsGroupName    = "Handoff"
	transcriptsGroupName = "Transcripts"
	// legacyScheduledCrystalsGroupName is renamed to automationsGroupName on
	// startup. Leftover from when the group was briefly labelled "Scheduled Crystals".
	legacyScheduledCrystalsGroupName = "Scheduled Crystals"
)

// ensureSidebarGroupByName creates a sidebar group with the given name if it
// does not already exist. Returns the group ID (new or existing).
func (a *App) ensureSidebarGroupByName(ctx context.Context, name string) string {
	if a.store == nil {
		return ""
	}
	groups, _, err := a.store.LoadAllSidebarGroups(ctx)
	if err != nil {
		log.Printf("[proactive] failed to load sidebar groups: %v", err)
		return ""
	}
	for _, g := range groups {
		if g.Name == name {
			return g.ID
		}
	}
	row, err := a.store.CreateSidebarGroup(ctx, name)
	if err != nil {
		log.Printf("[proactive] failed to create sidebar group %q: %v", name, err)
		return ""
	}
	log.Printf("[proactive] created sidebar group %q (id=%s)", name, row.ID)
	return row.ID
}

// addSessionToSidebarGroup adds a chat session to the named sidebar group,
// creating the group if it does not exist.
func (a *App) addSessionToSidebarGroup(ctx context.Context, groupName, sessionLocalID string) {
	if a.store == nil {
		return
	}
	groupID := a.ensureSidebarGroupByName(ctx, groupName)
	if groupID == "" {
		return
	}
	if _, err := a.store.AddItemToSidebarGroup(ctx, groupID, "chat", sessionLocalID); err != nil {
		log.Printf("[proactive] failed to add session %s to group %s: %v", sessionLocalID, groupID, err)
	}
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "sidebar:groups-changed", nil)
	}
}

// automationsRenameMigrationKey marks that the one-time rename from the
// leftover "Scheduled Crystals" sidebar group to automationsGroupName has
// already run. Once set, a user-created group named "Scheduled Crystals" will
// not be touched.
const automationsRenameMigrationKey = "migration.scheduled-crystals-group-renamed"

// ensureAutomationsGroup creates the Automations sidebar group if it does not
// already exist. On first run after the rename, it also migrates any leftover
// "Scheduled Crystals" group to the current name. The migration is gated by a
// UI-state flag so it runs at most once per install — a user may later create
// their own group named "Scheduled Crystals" and it will be left alone.
func (a *App) ensureAutomationsGroup() {
	if a.store == nil {
		return
	}
	ctx := context.Background()

	if done, err := a.LoadUIState(automationsRenameMigrationKey); err == nil && done != "true" {
		groups, _, lErr := a.store.LoadAllSidebarGroups(ctx)
		if lErr == nil {
			var legacyID, currentID string
			for _, g := range groups {
				switch g.Name {
				case legacyScheduledCrystalsGroupName:
					legacyID = g.ID
				case automationsGroupName:
					currentID = g.ID
				}
			}
			if legacyID != "" && currentID == "" {
				if rErr := a.store.RenameSidebarGroup(ctx, legacyID, automationsGroupName); rErr != nil {
					log.Printf("[proactive] failed to rename legacy %q group: %v", legacyScheduledCrystalsGroupName, rErr)
				} else {
					log.Printf("[proactive] renamed legacy %q sidebar group to %q", legacyScheduledCrystalsGroupName, automationsGroupName)
				}
			}
		}
		if sErr := a.SaveUIState(automationsRenameMigrationKey, "true"); sErr != nil {
			log.Printf("[proactive] failed to persist %s flag: %v", automationsRenameMigrationKey, sErr)
		}
	}

	a.ensureSidebarGroupByName(ctx, automationsGroupName)
}

// addSessionToAutomationsGroup adds a chat session to the Automations group.
func (a *App) addSessionToAutomationsGroup(ctx context.Context, sessionLocalID string) {
	a.addSessionToSidebarGroup(ctx, automationsGroupName, sessionLocalID)
}

// ensureTranscriptsGroup creates the "Transcripts" sidebar group if it does
// not already exist. Called once during app startup.
func (a *App) ensureTranscriptsGroup() {
	if a.store == nil {
		return
	}
	a.ensureSidebarGroupByName(context.Background(), transcriptsGroupName)
}

// addSessionToTranscriptsGroup adds a chat session to the Transcripts group.
func (a *App) addSessionToTranscriptsGroup(ctx context.Context, sessionLocalID string) {
	a.addSessionToSidebarGroup(ctx, transcriptsGroupName, sessionLocalID)
}

// activeProjectAndWorkspace returns the user's currently active project and
// its default workspace. System-generated sessions (automations,
// transcripts) are placed here and organized by sidebar groups.
//
// Fallback chain: active project -> first available project -> new default project.
func (a *App) activeProjectAndWorkspace(ctx context.Context) (projectID, workspaceID string, err error) {
	// 1. Try the persisted active project.
	activeID, _ := a.LoadActiveProjectID()
	if activeID != "" {
		if wss, wErr := a.store.LoadWorkspaces(ctx, activeID); wErr == nil && len(wss) > 0 {
			return activeID, wss[0].ID, nil
		}
	}

	// 2. Fall back to the first available project with workspaces.
	allProjects, _ := a.store.LoadProjects(ctx)
	for _, p := range allProjects {
		if wss, wErr := a.store.LoadWorkspaces(ctx, p.ID); wErr == nil && len(wss) > 0 {
			return p.ID, wss[0].ID, nil
		}
	}

	// 3. Nothing else worked — route through the standalone chat sentinel.
	// First check whether path="" is already occupied (by the sentinel itself
	// or by a stray UUID row left over from earlier builds that minted random
	// ids here). Reusing that row keeps a single owner of the path="" slot
	// and avoids the projects.path UNIQUE conflict that previously surfaced
	// as a chat-side error chip.
	if existing, lpErr := a.store.LoadProjectByPath(ctx, ""); lpErr == nil && existing.ID != "" {
		projectID = existing.ID
		if wss, wErr := a.store.LoadWorkspacesByProject(ctx, projectID); wErr == nil && len(wss) > 0 {
			return projectID, wss[0].ID, nil
		}
		// Project row exists but its workspace is missing — add a default one
		// so downstream session/FK inserts succeed.
		workspaceID = uuid.New().String()
		if err := a.store.SaveWorkspace(ctx, storage.WorkspaceRow{
			ID:        workspaceID,
			ProjectID: projectID,
			Label:     "General Chat",
			IsDefault: true,
		}); err != nil {
			return "", "", fmt.Errorf("create default workspace for project %s: %w", projectID, err)
		}
		return projectID, workspaceID, nil
	}

	// No row owns path="" yet — materialize the canonical sentinel.
	projectID = standaloneChatProjectID
	workspaceID = standaloneChatWorkspaceID
	if err := a.store.EnsureProjectStructure(ctx,
		storage.ProjectRow{ID: projectID, Name: "General Chat", Path: ""},
		[]storage.WorkspaceRow{{ID: workspaceID, ProjectID: projectID, Label: "General Chat", IsDefault: true}},
		nil,
	); err != nil {
		return "", "", fmt.Errorf("create default project: %w", err)
	}
	return projectID, workspaceID, nil
}

// standaloneChatProjectAndWorkspace always returns the standalone chat
// sentinel project + a usable workspace inside it, materializing the rows
// if they don't exist yet. Callers that surface chat sessions in the
// sidebar's Recent / standalone list (transcripts, handoffs) MUST use this
// instead of activeProjectAndWorkspace, otherwise the chat row lands in
// whichever git project happens to be active and the frontend's
// effectiveSessions (== standaloneWorkspace.sessions) can't find it on
// click.
func (a *App) standaloneChatProjectAndWorkspace(ctx context.Context) (projectID, workspaceID string, err error) {
	if a.store == nil {
		return "", "", fmt.Errorf("store not initialized")
	}
	// path == "" is the canonical key for the standalone chat sentinel;
	// reuse an existing row (regardless of its UUID — older builds minted
	// random ids here) to avoid the projects.path UNIQUE conflict.
	if existing, lpErr := a.store.LoadProjectByPath(ctx, ""); lpErr == nil && existing.ID != "" {
		projectID = existing.ID
		if wss, wErr := a.store.LoadWorkspacesByProject(ctx, projectID); wErr == nil && len(wss) > 0 {
			return projectID, wss[0].ID, nil
		}
		workspaceID = uuid.New().String()
		if err := a.store.SaveWorkspace(ctx, storage.WorkspaceRow{
			ID:        workspaceID,
			ProjectID: projectID,
			Label:     "General Chat",
			IsDefault: true,
		}); err != nil {
			return "", "", fmt.Errorf("create default workspace for standalone project %s: %w", projectID, err)
		}
		return projectID, workspaceID, nil
	}
	projectID = standaloneChatProjectID
	workspaceID = standaloneChatWorkspaceID
	if err := a.store.EnsureProjectStructure(ctx,
		storage.ProjectRow{ID: projectID, Name: "General Chat", Path: ""},
		[]storage.WorkspaceRow{{ID: workspaceID, ProjectID: projectID, Label: "General Chat", IsDefault: true}},
		nil,
	); err != nil {
		return "", "", fmt.Errorf("create standalone chat project: %w", err)
	}
	return projectID, workspaceID, nil
}

// ensureHandoffsGroup creates the "Handoff" sidebar group on startup. No legacy
// migration needed — this is a brand-new group from day one of the feature.
func (a *App) ensureHandoffsGroup() {
	if a.store == nil {
		return
	}
	a.ensureSidebarGroupByName(context.Background(), handoffsGroupName)
}

// addSessionToHandoffsGroup adds a chat session to the Handoff sidebar group.
func (a *App) addSessionToHandoffsGroup(ctx context.Context, sessionLocalID string) {
	a.addSessionToSidebarGroup(ctx, handoffsGroupName, sessionLocalID)
}

// appendHandoffProposalNotification (the proposal chat-card with
// [Pick this up]/[Not now] action chips) was removed with the proposal-based
// handoff path. File-first handoffs surface via the composer chip + Handoffs
// page, and execution creates its session in createHandoffExecutionSession.
