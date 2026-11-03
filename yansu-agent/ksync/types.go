package ksync

import "time"

// PushResult contains details about pushed changes.
type PushResult struct {
	Created []string
	Updated []string
	Deleted []string
	Errors  []string
}

// PullResult contains details about pulled changes.
type PullResult struct {
	Created []string
	Updated []string
	Deleted []string
	Errors  []string
}

// SyncResult contains the result of a full bidirectional sync.
type SyncResult struct {
	Pushed PushResult
	Pulled PullResult
}

// SyncStatus represents the current sync status for a project.
type SyncStatus struct {
	LastSyncedAt        time.Time
	LocalKnowledgeCount int
	PendingNewKnowledge int
}
