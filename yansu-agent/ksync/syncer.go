package ksync

import (
	"context"
	"fmt"
	"net/url"

	"yansu-agent/internal/client"
	"yansu-agent/internal/config"
	"yansu-agent/internal/knowledge"
	internalsync "yansu-agent/internal/sync"
)

// KSyncer provides a public API for knowledge synchronization.
// It wraps the internal sync, client, config, and knowledge packages
// so that external modules (e.g. wails-gui) can use sync functionality
// without importing internal packages directly.
type KSyncer struct {
	syncer       *internalsync.Syncer
	somethingDir string
}

// NewKSyncer creates a syncer for a project directory with explicit credentials.
func NewKSyncer(baseURL, token, org, product, projectName, projectRoot string) *KSyncer {
	somethingDir := config.GetSomethingDir(projectRoot)
	apiClient := client.New(baseURL, token)
	apiClient.SetProject(org, product, projectName)
	syncer := internalsync.NewSyncer(projectRoot, somethingDir, apiClient)
	return &KSyncer{
		syncer:       syncer,
		somethingDir: somethingDir,
	}
}

// NewKSyncerFromProject creates a syncer by auto-loading config from
// ~/.yansu-agent/ and .something/project.json in the given project root.
func NewKSyncerFromProject(projectRoot string) (*KSyncer, error) {
	somethingDir := config.GetSomethingDir(projectRoot)

	projectCfg, err := config.LoadProject(somethingDir)
	if err != nil {
		return nil, fmt.Errorf("load project config: %w", err)
	}

	globalCfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load global config: %w", err)
	}

	// Determine base URL: prefer project-specific YansuURL, fall back to global config
	baseURL := globalCfg.BaseURL
	if projectCfg.YansuURL != "" {
		if parsedURL, err := url.Parse(projectCfg.YansuURL); err == nil && parsedURL.Host != "" {
			baseURL = fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)
		}
	}

	token := globalCfg.Token

	apiClient := client.New(baseURL, token)
	apiClient.SetProject(projectCfg.Organization, projectCfg.Product, projectCfg.Name)
	if projectCfg.ProductUUID != "" {
		apiClient.SetProductUUID(projectCfg.ProductUUID)
	}

	syncer := internalsync.NewSyncer(projectRoot, somethingDir, apiClient)
	return &KSyncer{
		syncer:       syncer,
		somethingDir: somethingDir,
	}, nil
}

// Pull pulls remote knowledge changes to local.
func (k *KSyncer) Pull(ctx context.Context) (*PullResult, error) {
	r, err := k.syncer.Pull(ctx)
	if err != nil {
		return nil, err
	}
	return &PullResult{
		Created: r.Created,
		Updated: r.Updated,
		Deleted: r.Deleted,
		Errors:  r.Errors,
	}, nil
}

// Push pushes local new-knowledge to cloud.
func (k *KSyncer) Push(ctx context.Context) (*PushResult, error) {
	r, err := k.syncer.Push(ctx)
	if err != nil {
		return nil, err
	}
	return &PushResult{
		Created: r.Created,
		Updated: r.Updated,
		Deleted: r.Deleted,
		Errors:  r.Errors,
	}, nil
}

// SyncContext syncs the project context (PRD, scenarios, etc.) from cloud.
func (k *KSyncer) SyncContext(ctx context.Context) error {
	return k.syncer.SyncContext(ctx)
}

// SyncAll performs a full bidirectional sync (push + pull).
func (k *KSyncer) SyncAll(ctx context.Context) (*SyncResult, error) {
	r, err := k.syncer.SyncAll(ctx)
	if err != nil {
		return nil, err
	}
	return &SyncResult{
		Pushed: PushResult{
			Created: r.Pushed.Created,
			Updated: r.Pushed.Updated,
			Deleted: r.Pushed.Deleted,
			Errors:  r.Pushed.Errors,
		},
		Pulled: PullResult{
			Created: r.Pulled.Created,
			Updated: r.Pulled.Updated,
			Deleted: r.Pulled.Deleted,
			Errors:  r.Pulled.Errors,
		},
	}, nil
}

// GetSyncStatus returns the current sync status for the project.
func (k *KSyncer) GetSyncStatus() (*SyncStatus, error) {
	s, err := k.syncer.GetSyncStatus()
	if err != nil {
		return nil, err
	}
	return &SyncStatus{
		LastSyncedAt:        s.LastSyncedAt,
		LocalKnowledgeCount: s.LocalKnowledgeCount,
		PendingNewKnowledge: s.PendingNewKnowledge,
	}, nil
}

// EnsureDirectories creates the .something subdirectories if they don't exist.
func (k *KSyncer) EnsureDirectories() error {
	return knowledge.EnsureDirectories(k.somethingDir)
}

// UpdateIndex regenerates the knowledge index file.
func (k *KSyncer) UpdateIndex() error {
	return knowledge.UpdateIndex(k.somethingDir)
}
