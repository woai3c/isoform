package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"yansu-agent/agent"
)

// MagpieProfileView is the ordered group and the gateway's model catalog.
type MagpieProfileView struct {
	Members              []AgentModelChoice `json:"members"`
	Models               []string           `json:"models"`
	BackgroundUsesChoice bool               `json:"backgroundUsesChoice"`
}

func validProfileModel(model string) bool {
	return magpieModelIDPattern.MatchString(model) && !strings.HasPrefix(model, "group/") && !strings.Contains(model, ",") && memberBase(model) == model
}

func profileMembers(groups []magpieGroupInfo) []AgentModelChoice {
	for _, group := range groups {
		if group.ID == magpieIsoformGroupID {
			out := make([]AgentModelChoice, 0, len(group.Members))
			seen := map[string]bool{}
			for _, member := range group.Members {
				base := memberBase(member)
				if !magpieModelIDPattern.MatchString(base) || strings.HasPrefix(base, "group/") || seen[providerPart(base)] {
					continue
				}
				seen[providerPart(base)] = true
				effort := strings.TrimPrefix(member[len(base):], ":")
				if !slices.Contains(effortLevels, effort) {
					effort = defaultEffort
				}
				out = append(out, AgentModelChoice{Model: base, Effort: effort})
			}
			return out
		}
	}
	return []AgentModelChoice{}
}

// migrateProfileChoices keeps prior Isoform-owned choices where their models
// still exist in the gateway. The existing group supplies the initial order.
func migrateProfileChoices(members []AgentModelChoice, cfg AIProviderConfig, catalog []string) []AgentModelChoice {
	out := slices.Clone(members)
	for i := range out {
		agentID := providerPart(out[i].Model)
		if agentID == "claude" {
			agentID = string(agent.BackendClaudeCode)
		}
		choice := cfg.agentChoice(agentID)
		if validProfileModel(choice.Model) && providerPart(choice.Model) == providerPart(out[i].Model) && slices.Contains(catalog, choice.Model) {
			out[i].Model = choice.Model
		}
		if slices.Contains(effortLevels, choice.Effort) {
			out[i].Effort = choice.Effort
		}
	}
	isoform := cfg.agentChoice(answeringIsoform)
	if cfg.AnswerWith == answeringIsoform && validProfileModel(isoform.Model) && slices.Contains(catalog, isoform.Model) {
		index := slices.IndexFunc(out, func(member AgentModelChoice) bool { return providerPart(member.Model) == providerPart(isoform.Model) })
		selected := AgentModelChoice{Model: isoform.Model, Effort: defaultEffort}
		if index >= 0 {
			selected.Effort = out[index].Effort
			out = slices.Delete(out, index, index+1)
		}
		if slices.Contains(effortLevels, isoform.Effort) {
			selected.Effort = isoform.Effort
		}
		out = append([]AgentModelChoice{selected}, out...)
	}
	return out
}

// An external gateway may not have a separate background group. Select a
// lightweight advertised model from the profile's providers without writing
// another group or falling back to an unrelated provider.
func (a *App) externalProfileBackgroundModel() string {
	profile := loadAIProviderConfig().MagpieProfile
	if len(profile) == 0 {
		return ""
	}
	catalog, _ := a.agentChoices.catalogSnapshot()
	for _, member := range profile {
		for _, model := range catalog {
			if providerPart(model) == providerPart(member.Model) && (strings.Contains(strings.ToLower(modelPart(model)), "haiku") || strings.Contains(strings.ToLower(modelPart(model)), "luna")) {
				return model
			}
		}
	}
	return profile[0].Model
}

// initializeMagpieProfile adopts the initial Isoform group once. Subsequent
// bootstraps always project the saved configuration back into that group.
func (a *App) initializeMagpieProfile(ctx context.Context) error {
	cfg := loadAIProviderConfig()
	if a.magpie == nil || len(cfg.MagpieProfile) > 0 {
		return nil
	}
	// The older migration can run concurrently at startup. Complete it
	// before adopting the group so its picks cannot arrive too late.
	if !cfg.AgentChoicesMigrated {
		a.migrateAgentChoices()
		if !loadAIProviderConfig().AgentChoicesMigrated {
			return errors.New("assistant choices are not ready to migrate")
		}
	}
	a.magpie.bootstrapMu.Lock()
	defer a.magpie.bootstrapMu.Unlock()
	groups := a.magpie.readGroups(ctx)
	members := profileMembers(groups)
	if len(members) == 0 {
		return nil
	}
	catalog, err := a.magpie.listModels(ctx)
	if err != nil {
		return err
	}
	cfg, err = updateAIProviderConfig(func(cfg *AIProviderConfig) error {
		if len(cfg.MagpieProfile) == 0 {
			cfg.MagpieProfile = migrateProfileChoices(members, *cfg, catalog)
		}
		return nil
	})
	if err != nil {
		return err
	}
	groups, err = a.magpie.applyOwnedProfile(ctx, groups, cfg.MagpieProfile)
	if err == nil {
		a.magpieModels.setOwnedProfile(groups)
	}
	return err
}

// applyOwnedProfile changes only Isoform's group, verifying the resulting
// members before reporting success. The caller holds bootstrapMu.
func (s *magpieSidecar) applyOwnedProfile(ctx context.Context, groups []magpieGroupInfo, profile []AgentModelChoice) ([]magpieGroupInfo, error) {
	want := make([]string, 0, len(profile))
	for _, member := range profile {
		want = append(want, member.Model+":"+member.Effort)
	}
	name := ""
	for _, group := range groups {
		if group.ID == magpieIsoformGroupID && slices.Equal(group.Members, want) && group.Routing == "order" {
			return groups, nil
		}
		if group.ID == magpieIsoformGroupID {
			name = group.Name
		}
	}
	args := []string{"group", "add", magpieIsoformGroupName, "id=" + magpieIsoformGroupID}
	if name != "" {
		collision := false
		for _, group := range groups {
			if group.ID != magpieIsoformGroupID && strings.EqualFold(group.Name, name) {
				collision = true
				break
			}
		}
		if collision {
			// magpie replaces by name on `group add`. Give our reserved ID
			// a distinct name first, without changing the colliding group.
			for n := 1; ; n++ {
				candidate := "Isoform profile"
				if n > 1 {
					candidate = fmt.Sprintf("Isoform profile %d", n)
				}
				if !slices.ContainsFunc(groups, func(group magpieGroupInfo) bool { return strings.EqualFold(group.Name, candidate) }) {
					if _, err := s.runCLI(ctx, "group", "set", magpieIsoformGroupID, "name="+candidate); err != nil {
						return groups, err
					}
					name = candidate
					break
				}
			}
		}
		args = []string{"group", "add", name}
	}
	args = append(args, "models="+strings.Join(want, ","), "routing=order", "stays=session")
	_, err := s.runCLI(ctx, args...)
	if err != nil {
		return groups, err
	}
	groups = s.readGroups(ctx)
	for _, group := range groups {
		if group.ID == magpieIsoformGroupID && slices.Equal(group.Members, want) && group.Routing == "order" {
			return groups, nil
		}
	}
	return groups, errors.New("magpie did not apply Isoform's profile")
}

func (a *App) GetMagpieProfile() (MagpieProfileView, error) {
	cfg := loadAIProviderConfig()
	view := MagpieProfileView{Members: slices.Clone(cfg.MagpieProfile), Models: []string{}, BackgroundUsesChoice: cfg.BackgroundUsesChoice}
	if a.magpie != nil && a.magpie.Healthy() {
		ctx, cancel := context.WithTimeout(a.contextOrBackground(), assistantChoiceTimeout)
		defer cancel()
		models, err := a.magpie.listModels(ctx)
		if err != nil {
			return view, err
		}
		for _, model := range models {
			if validProfileModel(model) {
				view.Models = append(view.Models, model)
			}
		}
		if len(view.Members) == 0 {
			view.Members = profileMembers(a.magpie.readGroups(ctx))
		}
	}
	if view.Members == nil {
		view.Members = []AgentModelChoice{}
	}
	return view, nil
}

// SetMagpieProfile persists the source of truth before reconciling the group.
// Failed reconciliation is returned to the UI and retried on bootstrap.
func (a *App) SetMagpieProfile(members []AgentModelChoice) (MagpieProfileView, error) {
	view, err := a.GetMagpieProfile()
	if err != nil {
		return view, err
	}
	if len(members) == 0 {
		return view, errors.New("choose at least one model")
	}
	seen := map[string]bool{}
	for _, member := range members {
		provider := providerPart(member.Model)
		known := slices.Contains(view.Models, member.Model)
		for _, old := range view.Members {
			known = known || old.Model == member.Model
		}
		if !known || !validProfileModel(member.Model) || !slices.Contains(effortLevels, member.Effort) || seen[provider] {
			return view, fmt.Errorf("invalid profile member %q", member.Model)
		}
		seen[provider] = true
	}
	if a.magpie != nil {
		a.magpie.bootstrapMu.Lock()
		defer a.magpie.bootstrapMu.Unlock()
	}
	if _, err := updateAIProviderConfig(func(cfg *AIProviderConfig) error {
		cfg.MagpieProfile = slices.Clone(members)
		return nil
	}); err != nil {
		return view, err
	}
	if a.magpie != nil && a.magpie.Healthy() {
		ctx, cancel := context.WithTimeout(a.contextOrBackground(), assistantChoiceTimeout)
		defer cancel()
		groups, err := a.magpie.applyOwnedProfile(ctx, a.magpie.readGroups(ctx), members)
		if err != nil {
			return view, err
		}
		a.magpieModels.setOwnedProfile(groups)
		a.emitModelAccountsUpdated()
	}
	a.assistantChoiceChanged()
	return a.GetMagpieProfile()
}

// configureMagpieRunChoices resolves profile choices when each run starts.
func (a *App) configureMagpieRunChoices(cfg *agent.NativeRunnerConfig) {
	// "Use my choice for background work too": background work
	// thinks through the chat route (Isoform's pick) instead of the
	// paced background group, from the next run on. The route is read
	// per run, so a group Bootstrap makes later is used at once.
	cfg.InteractiveModel = func(model string) string {
		if len(loadAIProviderConfig().MagpieProfile) > 0 && (model == "" || managedModelAliasesFor("chat", "light")[model] == "chat") {
			return a.magpieRouteTargets().Chat
		}
		if a.magpie.External() && (model == "" || managedModelAliasesFor("chat", "light")[model] == "chat") {
			return a.externalIsoformModel()
		}
		return ""
	}
	cfg.ModelEffort = func(model, priority string) string {
		if len(loadAIProviderConfig().MagpieProfile) > 0 {
			if a.magpie.External() && priority == agent.PriorityBackground && !a.backgroundUsesChoice.Load() {
				return "medium"
			}
			return ""
		}
		if !a.magpie.External() || (priority == agent.PriorityBackground && !a.backgroundUsesChoice.Load()) {
			return ""
		}
		if priority != agent.PriorityBackground && model != "" && managedModelAliasesFor("chat", "light")[model] != "chat" {
			return ""
		}
		effort := loadAIProviderConfig().agentChoice(answeringIsoform).Effort
		if effort == "" {
			return defaultEffort
		}
		return effort
	}
	cfg.BackgroundModel = func() string {
		if a.backgroundUsesChoice.Load() {
			if len(loadAIProviderConfig().MagpieProfile) > 0 {
				return a.magpieRouteTargets().Chat
			}
			if a.magpie.External() {
				if model := a.externalIsoformModel(); model != "" {
					return model
				}
			}
			return a.magpieRouteTargets().Chat
		}
		if a.magpie.External() {
			return a.externalProfileBackgroundModel()
		}
		return ""
	}
}
