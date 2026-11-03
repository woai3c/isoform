package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Immutable Self history remains inspectable locally. Evaluation-derived
// statements must still have readable evidence when they enter a model prompt.
func (a *App) selfModelEntryAllowsModelRead(entry SelfModelEntry) bool {
	if a == nil || a.IsClosedEyesModeEnabled() || a.activity == nil || a.activity.store == nil {
		return false
	}
	for _, evidence := range append(append([]SelfModelEvidence(nil), entry.Evidence...), entry.Contradictions...) {
		switch evidence.SourceKind {
		case "shadow_task_evaluation":
			if !a.selfModelEvaluationAllowsModelRead(evidence.SourceID) {
				return false
			}
		case "activity_session", "session", taskContextSourceActivitySnapshotKind,
			"memory", taskContextSourceMemoryKind, "knowledge", taskContextSourceKnowledgeKind:
			if !a.selfModelEvidenceSourceAllowsRead(evidence.SourceKind, evidence.SourceID) {
				return false
			}
		}
	}
	return true
}

func (a *App) selfModelEvidenceSourceAllowsRead(kind, id string) bool {
	if strings.TrimSpace(id) == "" {
		return false
	}
	switch kind {
	case "activity_session", "session":
		return a.activityDependencyAllowsModelRead(id, nil)
	case taskContextSourceActivitySnapshotKind:
		// Snapshot identities embed the session ID. Its receipt survives raw
		// snapshot retention and carries the same current privacy restrictions.
		index := strings.LastIndex(id, ":")
		if index <= 0 {
			return false
		}
		snapshotID, err := strconv.ParseInt(id[index+1:], 10, 64)
		return err == nil && snapshotID > 0 && a.activityDependencyAllowsModelRead(id[:index], nil)
	case "memory", taskContextSourceMemoryKind, "knowledge", taskContextSourceKnowledgeKind:
		if filepath.Base(id) != id || id == "." || id == ".." {
			return false
		}
		dir, err := memoryDir()
		if kind == "knowledge" || kind == taskContextSourceKnowledgeKind {
			dir, err = knowledgeDir()
		}
		if err != nil {
			return false
		}
		content, err := os.ReadFile(filepath.Join(dir, id+".md"))
		return err == nil && a.memorySourceAllowsRead(string(content))
	default:
		return false
	}
}

func (a *App) selfModelEvaluationAllowsModelRead(evaluationID string) bool {
	store := a.activity.store
	var episodeID string
	if store.db.QueryRow(`SELECT run.task_episode_id
		FROM shadow_run_evaluations evaluation
		JOIN shadow_runs run ON run.id = evaluation.shadow_run_id
		WHERE evaluation.id = ?`, evaluationID).Scan(&episodeID) != nil || episodeID == "" {
		return false
	}
	detail, err := store.GetTaskEpisodeDetail(episodeID)
	if err != nil || len(detail.Evidence) == 0 {
		return false
	}
	// An onset activity_event ID is an in-memory ordinal, never a SQLite
	// event ID. Resolve its app/time against a concrete frozen session source.
	var sources []activitySourceProvenance
	for _, source := range detail.Episode.FrozenContext.Manifest {
		if !source.Included {
			continue
		}
		if source.SourceKind == "activity_event" {
			found := false
			for _, evidence := range detail.Evidence {
				if evidence.SourceKind == source.SourceKind && evidence.SourceID == source.SourceID {
					found = true
				}
			}
			if !found {
				return false
			}
			continue
		}
		if !a.selfModelEvidenceSourceAllowsRead(source.SourceKind, source.SourceID) {
			return false
		}
		id := source.SourceID
		switch source.SourceKind {
		case taskContextSourceActivitySnapshotKind:
			id = id[:strings.LastIndex(id, ":")]
		case "activity_session", "session":
		default:
			continue
		}
		provenance, err := store.activityDependencyProvenance(id, nil)
		if err != nil {
			return false
		}
		sources = append(sources, provenance)
	}
	for _, evidence := range detail.Evidence {
		if evidence.SourceKind != "activity_event" {
			if !a.selfModelEvidenceSourceAllowsRead(evidence.SourceKind, evidence.SourceID) {
				return false
			}
			continue
		}
		var onset TaskOnsetEvidence
		if json.Unmarshal([]byte(evidence.PayloadJSON), &onset) != nil ||
			onset.SourceID != evidence.SourceID || onset.SourceKind != evidence.SourceKind ||
			onset.OccurredAt != evidence.ObservedAt || (onset.AppKey == "" && onset.AppName == "") ||
			activitySourceAppIsSensitive(onset.AppKey, onset.AppName) {
			return false
		}
		resolved := false
		for _, source := range sources {
			if source.StartTime <= onset.OccurredAt && source.EndTime >= onset.OccurredAt {
				for _, app := range source.Apps {
					if strings.EqualFold(app, onset.AppName) || strings.EqualFold(app, onset.AppKey) {
						resolved = true
					}
				}
			}
		}
		if !resolved {
			return false
		}
	}
	return true
}
