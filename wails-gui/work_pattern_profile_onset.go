package main

import (
	"context"
	"errors"
	"sort"
	"strings"
)

const workPatternProfileInfluenceEnabledKey = "work_pattern_profile_influence_enabled"

func (a *App) workPatternProfileInfluenceEnabled() bool {
	if a == nil || a.store == nil {
		return false
	}
	value, err := a.store.GetUIState(
		context.Background(),
		workPatternProfileInfluenceEnabledKey,
	)
	if err != nil || strings.TrimSpace(value) == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(value), "true")
}

func taskOnsetWorkPatternKeys(
	input TaskOnsetInput,
) ([]string, []string) {
	apps := make(map[string]struct{})
	transitions := make(map[string]struct{})
	previous := ""
	for _, evidence := range input.Evidence {
		appKey := strings.ToLower(strings.TrimSpace(evidence.AppKey))
		if appKey == "" {
			continue
		}
		apps[appKey] = struct{}{}
		if previous != "" && previous != appKey {
			transitions[previous+"->"+appKey] = struct{}{}
		}
		previous = appKey
	}
	appKeys := make([]string, 0, len(apps))
	for value := range apps {
		appKeys = append(appKeys, value)
	}
	transitionKeys := make([]string, 0, len(transitions))
	for value := range transitions {
		transitionKeys = append(transitionKeys, value)
	}
	sort.Strings(appKeys)
	sort.Strings(transitionKeys)
	return appKeys, transitionKeys
}

func taskOnsetWorkPatternQuery(input TaskOnsetInput) string {
	parts := make([]string, 0, len(input.Evidence))
	for _, evidence := range input.Evidence {
		if summary := strings.TrimSpace(evidence.Summary); summary != "" {
			parts = append(parts, summary)
		}
	}
	return strings.Join(parts, "\n")
}

func buildTaskOnsetWorkPatternContext(
	store *ActivityStore,
	input TaskOnsetInput,
	influenceEnabled bool,
	createdAt int64,
) (WorkPatternProfileContext, error) {
	if store == nil || store.db == nil {
		return WorkPatternProfileContext{}, errors.New(
			"activity store is unavailable",
		)
	}
	if input.WindowStart <= 1 {
		return WorkPatternProfileContext{}, nil
	}
	profileCutoff := input.WindowStart - 1
	snapshot, _, err := store.EnsureWorkPatternProfileSnapshot(
		profileCutoff,
		createdAt,
	)
	if err != nil {
		return WorkPatternProfileContext{}, err
	}
	query := taskOnsetWorkPatternQuery(input)
	sources, err := GatherTaskContextSources(TaskContextSourceRequest{
		Store:    store,
		CutoffAt: profileCutoff,
		Query:    query,
	})
	if err != nil {
		return WorkPatternProfileContext{}, err
	}
	appKeys, transitionKeys := taskOnsetWorkPatternKeys(input)
	mode := WorkPatternInfluenceNeutral
	if influenceEnabled {
		mode = WorkPatternInfluenceProfile
	}
	return buildWorkPatternProfileContext(
		store,
		snapshot,
		sources,
		query,
		appKeys,
		transitionKeys,
		mode,
	)
}

func workPatternProfileCanInfluence(
	context WorkPatternProfileContext,
) bool {
	return context.InfluenceMode == WorkPatternInfluenceProfile &&
		context.Status == WorkPatternProfileStatusQualified &&
		len(context.SelectedProjections) > 0
}
