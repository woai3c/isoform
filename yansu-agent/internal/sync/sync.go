package sync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"yansu-agent/internal/client"
	"yansu-agent/internal/config"
	"yansu-agent/internal/knowledge"
)

// Syncer handles synchronization between local files and cloud
type Syncer struct {
	projectRoot  string
	somethingDir string
	apiClient    *client.Client
}

// SyncResult contains the result of a sync operation
type SyncResult struct {
	Pushed PushResult
	Pulled PullResult
}

// PushResult contains details about pushed changes
type PushResult struct {
	Created []string
	Updated []string
	Deleted []string
	Errors  []string
}

// PullResult contains details about pulled changes
type PullResult struct {
	Created []string
	Updated []string
	Deleted []string
	Errors  []string
}

// HasChanges returns true if there were any changes
func (r *PullResult) HasChanges() bool {
	return len(r.Created) > 0 || len(r.Updated) > 0 || len(r.Deleted) > 0
}

// TotalChanges returns the total number of changes
func (r *PullResult) TotalChanges() int {
	return len(r.Created) + len(r.Updated) + len(r.Deleted)
}

// NewSyncer creates a new syncer
func NewSyncer(projectRoot, somethingDir string, apiClient *client.Client) *Syncer {
	return &Syncer{
		projectRoot:  projectRoot,
		somethingDir: somethingDir,
		apiClient:    apiClient,
	}
}

// SyncAll performs a full bidirectional sync
func (s *Syncer) SyncAll(ctx context.Context) (*SyncResult, error) {
	result := &SyncResult{}

	// Push local changes first
	pushResult, err := s.Push(ctx)
	if err != nil {
		return nil, fmt.Errorf("push failed: %w", err)
	}
	result.Pushed = *pushResult

	// Then pull remote changes
	pullResult, err := s.Pull(ctx)
	if err != nil {
		return nil, fmt.Errorf("pull failed: %w", err)
	}
	result.Pulled = *pullResult

	// Update last synced time
	s.updateLastSyncTime()

	return result, nil
}

// Push pushes local changes to cloud
func (s *Syncer) Push(ctx context.Context) (*PushResult, error) {
	result := &PushResult{}
	newKnowledgeDir := filepath.Join(s.somethingDir, "new-knowledge")

	entries, err := os.ReadDir(newKnowledgeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return nil, fmt.Errorf("failed to read new-knowledge directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}

		filePath := filepath.Join(newKnowledgeDir, entry.Name())
		k, err := knowledge.ParseFile(filePath)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", entry.Name(), err))
			continue
		}

		// Push to cloud
		resp, err := s.pushKnowledge(ctx, k)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", k.Trigger, err))
			continue
		}

		// Move file to knowledge directory
		if err := s.moveToKnowledge(filePath, resp, k); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: failed to move file: %v", k.Trigger, err))
			continue
		}

		switch k.Action {
		case "create", "":
			result.Created = append(result.Created, k.Trigger)
		case "update":
			result.Updated = append(result.Updated, k.Trigger)
		case "delete":
			result.Deleted = append(result.Deleted, k.Trigger)
		}
	}

	// Update index
	if err := knowledge.UpdateIndex(s.somethingDir); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("failed to update index: %v", err))
	}

	return result, nil
}

func (s *Syncer) pushKnowledge(ctx context.Context, k *knowledge.Knowledge) (*client.KnowledgeResponse, error) {
	metadata := map[string]any{
		"tags": k.Tags,
		"path": k.Path,
	}

	switch k.Action {
	case "create", "":
		return s.apiClient.CreateKnowledge(ctx, &client.CreateKnowledgeRequest{
			Trigger:  k.Trigger,
			Content:  k.Content,
			Metadata: metadata,
		})

	case "update":
		if k.UUID == "" {
			return nil, fmt.Errorf("UUID required for update")
		}
		return s.apiClient.UpdateKnowledge(ctx, k.UUID, &client.UpdateKnowledgeRequest{
			Trigger:  k.Trigger,
			Content:  k.Content,
			Metadata: metadata,
		})

	case "delete":
		if k.UUID == "" {
			return nil, fmt.Errorf("UUID required for delete")
		}
		if err := s.apiClient.DeleteKnowledge(ctx, k.UUID); err != nil {
			return nil, err
		}
		return nil, nil

	default:
		return s.apiClient.CreateKnowledge(ctx, &client.CreateKnowledgeRequest{
			Trigger:  k.Trigger,
			Content:  k.Content,
			Metadata: metadata,
		})
	}
}

func (s *Syncer) moveToKnowledge(srcPath string, resp *client.KnowledgeResponse, k *knowledge.Knowledge) error {
	knowledgeDir := filepath.Join(s.somethingDir, "knowledge")

	// For delete action, remove source file and existing knowledge file
	if k.Action == "delete" {
		os.Remove(srcPath)
		knowledge.DeleteKnowledgeFile(knowledgeDir, k.UUID)
		return nil
	}

	// Write to knowledge directory with UUID
	uuid := k.UUID
	if resp != nil {
		uuid = resp.UUID
	}

	if err := knowledge.WriteKnowledgeFile(knowledgeDir, uuid, k.Trigger, k.Content, k.Tags, k.Path); err != nil {
		return err
	}

	// Remove source file
	return os.Remove(srcPath)
}

// Pull pulls remote changes to local
func (s *Syncer) Pull(ctx context.Context) (*PullResult, error) {
	result := &PullResult{}
	knowledgeDir := filepath.Join(s.somethingDir, "knowledge")

	// Get remote knowledge list
	remoteList, err := s.apiClient.ListKnowledge(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list remote knowledge: %w", err)
	}

	// Get local knowledge list
	localList, err := knowledge.LoadAll(knowledgeDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load local knowledge: %w", err)
	}

	// Build local UUID index
	localByUUID := make(map[string]*knowledge.Knowledge)
	for _, k := range localList {
		if k.UUID != "" {
			localByUUID[k.UUID] = k
		}
	}

	// Track remote UUIDs
	remoteUUIDs := make(map[string]bool)

	// Sync remote knowledge
	for _, remote := range remoteList {
		remoteUUIDs[remote.UUID] = true

		local, exists := localByUUID[remote.UUID]
		if !exists {
			// New knowledge, download
			if err := s.downloadKnowledge(remote); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", remote.Trigger, err))
				continue
			}
			result.Created = append(result.Created, remote.Trigger)
		} else if remote.UpdatedAt.After(local.UpdatedAt) {
			// Remote is newer, update local
			if err := s.downloadKnowledge(remote); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", remote.Trigger, err))
				continue
			}
			result.Updated = append(result.Updated, remote.Trigger)
		}
	}

	// Delete local knowledge that doesn't exist remotely
	for uuid, local := range localByUUID {
		if !remoteUUIDs[uuid] {
			if err := os.Remove(local.FilePath); err != nil && !os.IsNotExist(err) {
				result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", local.Trigger, err))
				continue
			}
			result.Deleted = append(result.Deleted, local.Trigger)
		}
	}

	// Update index if there were changes
	if result.HasChanges() {
		if err := knowledge.UpdateIndex(s.somethingDir); err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("failed to update index: %v", err))
		}
	}

	return result, nil
}

func (s *Syncer) downloadKnowledge(remote *client.KnowledgeResponse) error {
	knowledgeDir := filepath.Join(s.somethingDir, "knowledge")

	// Delete existing file with same UUID if exists
	knowledge.DeleteKnowledgeFile(knowledgeDir, remote.UUID)

	// Write new file
	// Extract path from pointer
	var path string
	if remote.Path != nil {
		path = *remote.Path
	}

	return knowledge.WriteKnowledgeFile(knowledgeDir, remote.UUID, remote.Trigger, remote.Content, remote.Tags, path)
}

// PushSingleKnowledge pushes a single knowledge file
func (s *Syncer) PushSingleKnowledge(ctx context.Context, filePath string) error {
	k, err := knowledge.ParseFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to parse file: %w", err)
	}

	resp, err := s.pushKnowledge(ctx, k)
	if err != nil {
		return fmt.Errorf("failed to push: %w", err)
	}

	if err := s.moveToKnowledge(filePath, resp, k); err != nil {
		return fmt.Errorf("failed to move file: %w", err)
	}

	// Update index
	knowledge.UpdateIndex(s.somethingDir)

	return nil
}

// SyncContext syncs the project context from cloud
func (s *Syncer) SyncContext(ctx context.Context) error {
	contextResp, err := s.apiClient.GetProjectContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get project context: %w", err)
	}

	return knowledge.GenerateContextFile(
		s.somethingDir,
		contextResp.PRD,
		contextResp.Summary,
		contextResp.Requirements,
		contextResp.Scenarios,
		contextResp.Decisions,
	)
}

func (s *Syncer) updateLastSyncTime() {
	projectCfg, err := config.LoadProject(s.somethingDir)
	if err != nil {
		return
	}
	projectCfg.LastSyncedAt = time.Now()
	config.SaveProject(s.somethingDir, projectCfg)
}

// GetSyncStatus returns the current sync status
func (s *Syncer) GetSyncStatus() (*SyncStatus, error) {
	status := &SyncStatus{}

	// Load project config for last sync time
	projectCfg, err := config.LoadProject(s.somethingDir)
	if err == nil {
		status.LastSyncedAt = projectCfg.LastSyncedAt
	}

	// Count local knowledge
	knowledgeDir := filepath.Join(s.somethingDir, "knowledge")
	localList, _ := knowledge.LoadAll(knowledgeDir)
	status.LocalKnowledgeCount = len(localList)

	// Count pending new knowledge
	newKnowledgeDir := filepath.Join(s.somethingDir, "new-knowledge")
	entries, _ := os.ReadDir(newKnowledgeDir)
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			status.PendingNewKnowledge++
		}
	}

	return status, nil
}

// SyncStatus represents the current sync status
type SyncStatus struct {
	LastSyncedAt        time.Time
	LocalKnowledgeCount int
	PendingNewKnowledge int
}
