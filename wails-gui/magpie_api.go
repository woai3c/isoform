package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"yansu-agent/agent"

	wailsRuntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Wails bindings for managed model accounts. The Settings surface reads
// GetModelAccountsStatus and changes state through the other calls. Every
// binding returns JSON-friendly values.

const (
	// magpieBootstrapTimeout bounds one bootstrap run, which is several CLI calls.
	magpieBootstrapTimeout = 30 * time.Second
	// magpieChatGroupID is the group whose members decide AIReady.
	magpieChatGroupID = "yansu-chat"
	// magpieAdvancedDefault is the advanced provider when none was remembered.
	magpieAdvancedDefault = "anthropic"
)

// SidecarStatus describes the magpie sidecar process.
type SidecarStatus struct {
	Available  bool   `json:"available"`
	Running    bool   `json:"running"`
	Healthy    bool   `json:"healthy"`
	Degraded   bool   `json:"degraded"`
	Port       int    `json:"port"`
	Version    string `json:"version"`
	LastError  string `json:"lastError,omitempty"`
	BinaryPath string `json:"binaryPath,omitempty"`
}

// QuotaWindow is one allowance window of a subscription. Used and Remaining
// are the percent of the window's allowance used and left (0-100); magpie
// accepts fractional percentages and computes Remaining as max(0, 100-Used).
// Source: yetone/magpie v0.1.1172, internal/provider/quotas.go (QuotaSpan and
// quotaReport). Unlimited windows have no binding percentage. ResetsAt is RFC 3339.
// https://github.com/yetone/magpie/blob/v0.1.1172/internal/provider/quotas.go
type QuotaWindow struct {
	Unlimited bool    `json:"unlimited,omitempty"`
	Name      string  `json:"name"`
	Used      float64 `json:"used"`
	Remaining float64 `json:"remaining"`
	ResetsAt  string  `json:"resetsAt"`
}

// ModelAccount is one subscription or key magpie knows. Windows is empty when
// magpie reports no numbers; the UI shows such an account as in use.
type ModelAccount struct {
	Provider string        `json:"provider"`
	Name     string        `json:"name"`
	Kind     string        `json:"kind"`
	Plan     string        `json:"plan,omitempty"`
	User     string        `json:"user,omitempty"`
	On       bool          `json:"on"`
	Balance  string        `json:"balance,omitempty"`
	Windows  []QuotaWindow `json:"windows"`
	ReadAt   string        `json:"readAt,omitempty"`
	Error    string        `json:"error,omitempty"`
}

// ModelGroup is one of Yansu's routing groups, as the last bootstrap applied it.
type ModelGroup struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Routing string   `json:"routing"`
	Members []string `json:"members"`
}

// ConsentStatus reports the subscription-use consent. Required is true when
// subscriptions are available and the current version is not accepted.
type ConsentStatus struct {
	Required   bool   `json:"required"`
	Accepted   bool   `json:"accepted"`
	Version    int    `json:"version"`
	AcceptedAt string `json:"acceptedAt,omitempty"`
}

// ModelAccountsStatus is the full managed-models view for Settings.
type ModelAccountsStatus struct {
	// Mode is "managed", "advanced" or "external".
	Mode     string         `json:"mode"`
	Sidecar  SidecarStatus  `json:"sidecar"`
	Accounts []ModelAccount `json:"accounts"`
	Groups   []ModelGroup   `json:"groups"`
	Consent  ConsentStatus  `json:"consent"`
	// AccountOrder is the assistant order the groups admit, primary first
	// (AIProviderConfig.managedOrder): the default when the user never chose.
	AccountOrder []string `json:"accountOrder"`
	// Install says which magpie Isoform uses: the user's own or the bundled copy.
	Install MagpieInstallView `json:"install"`
	Ollama  bool              `json:"ollama"`
	// ByokRegistered is true when the user's own key is registered as yansu-byok.
	ByokRegistered bool `json:"byokRegistered"`
	// Error is the last bootstrap or quota error, when there is one.
	Error string `json:"error,omitempty"`
	// ResearchVia is how Today's searches reach the web: claude_code,
	// opencli (the assistant with opencli in Chrome) or none.
	ResearchVia string `json:"researchVia"`
}

// GetModelAccountsStatus returns the cached managed-models view. It does not
// call the gateway, so it is cheap to poll.
func (a *App) GetModelAccountsStatus() (ModelAccountsStatus, error) {
	return a.modelAccountsStatus(), nil
}

// MagpieInstallView says which magpie Isoform uses. Kind is "user" for a
// magpie the user installed (its gateway and routing group are used as they
// are) or "bundled" for Isoform's own copy (its groups are built from the
// models the user's agents use). AgentModels is the model each agent
// currently uses, by provider id.
type MagpieInstallView struct {
	Kind        string            `json:"kind"`
	Version     string            `json:"version,omitempty"`
	BinaryPath  string            `json:"binaryPath,omitempty"`
	ConfigDir   string            `json:"configDir,omitempty"`
	ServerURL   string            `json:"serverUrl,omitempty"`
	Healthy     bool              `json:"healthy"`
	ActiveGroup string            `json:"activeGroup,omitempty"`
	Groups      []ModelGroup      `json:"groups"`
	AgentModels map[string]string `json:"agentModels"`
}

// magpieInstallView assembles the install view from detection and the
// cached bootstrap state. It never blocks on the gateway.
func (a *App) magpieInstallView() MagpieInstallView {
	view := MagpieInstallView{Kind: "bundled", Groups: []ModelGroup{}, AgentModels: map[string]string{}}
	if in, ok := detectUserMagpieInstall(); ok {
		view.Kind = "user"
		view.BinaryPath = in.BinaryPath
		view.ConfigDir = in.ConfigDir
		view.ServerURL = in.ServerURL()
	}
	if a.magpie == nil {
		return view
	}
	snap := a.magpie.Status()
	view.Version = snap.Version
	view.Healthy = snap.Healthy
	if view.Kind == "bundled" {
		view.BinaryPath = snap.BinaryPath
		if snap.Port > 0 {
			view.ServerURL = fmt.Sprintf("http://127.0.0.1:%d", snap.Port)
		}
	}
	model := a.magpieModels.snapshot()
	view.ActiveGroup = model.ActiveGroup
	if model.External {
		view.Groups = model.Groups
	}
	if model.AgentModels != nil {
		view.AgentModels = model.AgentModels
	}
	return view
}

// magpieRouteTargets are the model ids the native runner sends for chat,
// background and light work. Chat uses Isoform's group when it is available.
type magpieRouteTargets struct {
	Chat       string
	Background string
	Light      string
}

func (a *App) magpieRouteTargets() magpieRouteTargets {
	snap := a.magpieModels.snapshot()
	if !snap.External {
		// Chat uses Isoform's group when present. Background work always
		// keeps the separately paced group, including after bootstrap.
		heavy := magpieChatGroupModel
		background := magpieBackgroundGroupModel
		if snap.ActiveGroup == magpieIsoformGroupID {
			heavy = "group/" + magpieIsoformGroupID
		}
		return magpieRouteTargets{Chat: heavy, Background: background, Light: magpieLightGroupModel}
	}
	// Optional writes may have failed on a read-only external installation.
	// Use only groups present in the observed bootstrap state, and otherwise
	// fall back to the user's working chat route for unattended calls.
	groupAvailable := func(id string) bool {
		for _, group := range snap.Groups {
			if group.ID == id && len(group.Members) > 0 {
				return true
			}
		}
		return false
	}
	target := ""
	for _, group := range snap.Groups {
		if group.ID != snap.ActiveGroup {
			continue
		}
		// An existing selection remains authoritative even when it cannot
		// serve a model. Never recover by using an unselected provider.
		target = "group/" + group.ID
		if len(group.Members) == 0 {
			return magpieRouteTargets{Chat: target, Background: target, Light: target}
		}
		break
	}
	if target == "" {
		for _, provider := range []string{"claude", "codex"} {
			if id, ok := strings.CutPrefix(snap.AgentModels[provider], "group/"); ok && !isIsoformTaskGroup(id) && groupAvailable(id) {
				target = "group/" + id
				break
			}
		}
	}
	if target == "" {
		for _, group := range snap.Groups {
			if !isIsoformTaskGroup(group.ID) && len(group.Members) > 0 {
				target = "group/" + group.ID
				break
			}
		}
	}
	if target == "" {
		target = snap.FallbackModel
	}
	if target == "" {
		for _, provider := range []string{"claude", "codex"} {
			if model := agentCatalogModel(snap.AgentModels, provider); model != "" {
				target = model
				break
			}
		}
	}
	routes := magpieRouteTargets{Chat: target, Background: target, Light: target}
	if groupAvailable("isoform-background") {
		routes.Background = "group/isoform-background"
	}
	if groupAvailable("isoform-light") {
		routes.Light = "group/isoform-light"
	}
	return routes
}

// AcceptManagedModelConsent records consent for subscription use at the
// given version and re-runs bootstrap so subscription members join the groups.
func (a *App) AcceptManagedModelConsent(version int) (ModelAccountsStatus, error) {
	if version != magpieConsentVersion {
		return a.modelAccountsStatus(), fmt.Errorf("consent version %d is not current (%d)", version, magpieConsentVersion)
	}
	stateDir := a.magpieStateDir()
	if stateDir == "" {
		return a.modelAccountsStatus(), errMagpieStateDir
	}
	if err := saveMagpieConsent(stateDir, MagpieConsent{Version: version, AcceptedAt: time.Now()}); err != nil {
		return a.modelAccountsStatus(), err
	}
	if a.magpie.Healthy() {
		a.bootstrapManagedModelsLogged()
	}
	return a.modelAccountsStatus(), nil
}

// SetManagedAccountOrder records which signed-in subscriptions Isoform thinks
// with and in what order, then re-applies the routing groups. In the bundled copy that includes Isoform's
// own group, which chat and background work route through; a magpie the user
// installed is left as it is (Settings hides Change there). Onboarding calls
// it when the user picks an assistant; Settings can change it later. Unknown
// entries are rejected and an empty choice is an error, so the groups never
// silently lose every subscription member.
func (a *App) SetManagedAccountOrder(order []string) (ModelAccountsStatus, error) {
	chosen := make([]string, 0, len(order))
	seen := make(map[string]bool, len(order))
	for _, raw := range order {
		id := strings.ToLower(strings.TrimSpace(raw))
		if !managedAccountProviderKnown(id) {
			return a.modelAccountsStatus(), fmt.Errorf("unknown assistant %q (expected one of %s)", raw, strings.Join(managedAccountProviders, ", "))
		}
		if !seen[id] {
			seen[id] = true
			chosen = append(chosen, id)
		}
	}
	if len(chosen) == 0 {
		return a.modelAccountsStatus(), errors.New("choose at least one assistant")
	}
	catalog, _ := a.agentChoices.catalogSnapshot()
	if a.magpie != nil {
		a.magpie.bootstrapMu.Lock()
	}
	_, err := updateAIProviderConfig(func(cfg *AIProviderConfig) error {
		cfg.ManagedOrder = chosen
		// Onboarding's first assistant becomes the answering pick.
		cfg.AnswerWith = ""
		if len(cfg.MagpieProfile) > 0 {
			var profile []AgentModelChoice
			for _, provider := range chosen {
				found := false
				for _, member := range cfg.MagpieProfile {
					if providerPart(member.Model) == provider {
						profile = append(profile, member)
						found = true
						break
					}
				}
				if !found {
					preferred := map[string]string{"claude": "claude/claude-sonnet-5-5", "codex": "codex/gpt-5.6-sol"}[provider]
					model := preferred
					if !slices.Contains(catalog, model) {
						model = firstWithPrefix(catalog, provider+"/", "")
					}
					if model != "" {
						profile = append(profile, AgentModelChoice{Model: model, Effort: defaultEffort})
					}
				}
			}
			for _, member := range cfg.MagpieProfile {
				if !managedAccountProviderKnown(providerPart(member.Model)) {
					profile = append(profile, member)
				}
			}
			if len(profile) > 0 {
				cfg.MagpieProfile = profile
			}
		}
		return nil
	})
	if a.magpie != nil {
		a.magpie.bootstrapMu.Unlock()
	}
	if err != nil {
		return a.modelAccountsStatus(), err
	}
	a.refreshAssistantRoutingPolicy()
	noteResearchConfigChanged()
	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, "ai:config-changed")
	}
	if a.magpie != nil && a.magpie.Healthy() {
		a.bootstrapManagedModelsLogged()
	}
	return a.modelAccountsStatus(), nil
}

// RevokeManagedModelConsent removes the recorded agreement and re-runs
// bootstrap. Consent is informational (planMagpieGroups ignores it), so this
// only brings the terms note back in Settings; signed-in subscriptions stay in
// the groups. To stop using them, the user turns on their own key or model, or
// signs out of Claude Code or Codex (Settings says so before withdrawing).
func (a *App) RevokeManagedModelConsent() (ModelAccountsStatus, error) {
	stateDir := a.magpieStateDir()
	if stateDir == "" {
		return a.modelAccountsStatus(), errMagpieStateDir
	}
	if err := clearMagpieConsent(stateDir); err != nil {
		return a.modelAccountsStatus(), err
	}
	if a.magpie.Healthy() {
		a.bootstrapManagedModelsLogged()
	}
	return a.modelAccountsStatus(), nil
}

// RefreshModelAccounts re-detects accounts and keys and re-runs bootstrap.
// In managed mode it also starts the sidecar when it is down.
func (a *App) RefreshModelAccounts() (ModelAccountsStatus, error) {
	if a.modelSidecarActive() {
		a.syncMagpieSidecar()
	}
	a.refreshAssistantRoutingPolicy()
	return a.modelAccountsStatus(), nil
}

// SetAIProviderMode switches between "managed" and "advanced". Switching to
// advanced restores the provider used before (default anthropic). Switching to
// managed remembers that provider. Neither touches the saved key or models.
// The agent restarts so the change takes effect.
func (a *App) SetAIProviderMode(mode string) error {
	_, err := updateAIProviderConfig(func(cfg *AIProviderConfig) error {
		next, err := aiProviderConfigForMode(*cfg, mode)
		if err == nil {
			*cfg = next
		}
		return err
	})
	if err != nil {
		return err
	}
	noteResearchConfigChanged()
	if a.ctx != nil {
		wailsRuntime.EventsEmit(a.ctx, "ai:config-changed")
	}
	if _, err := a.RestartAgent(); err != nil {
		return fmt.Errorf("mode saved but the agent failed to start: %v", err)
	}
	return nil
}

// aiProviderConfigForMode returns cfg switched to mode. The saved key, base
// URL and models are kept in every case, so a round trip loses nothing.
func aiProviderConfigForMode(cfg AIProviderConfig, mode string) (AIProviderConfig, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "managed":
		if !cfg.managedActive() {
			cfg.AdvancedProvider = cfg.normalizedProvider()
			cfg.Provider = aiProviderManaged
		}
	case "advanced":
		if cfg.managedActive() {
			next := strings.ToLower(strings.TrimSpace(cfg.AdvancedProvider))
			if next == "" || next == aiProviderManaged {
				next = magpieAdvancedDefault
			}
			cfg.Provider = next
		}
	default:
		return cfg, fmt.Errorf("unknown AI provider mode %q", mode)
	}
	return cfg, nil
}

// managedModelsActive reports whether model calls route through magpie.
func (a *App) managedModelsActive() bool {
	return loadAIProviderConfig().managedActive()
}

// modelSidecarActive reports whether the sidecar should run: managed mode and
// the external CLI modes.
func (a *App) modelSidecarActive() bool {
	return loadAIProviderConfig().sidecarActive()
}

// managedModelsReady reports whether the chat group has a member the sidecar
// can serve. It is the readiness signal in managed mode.
func (a *App) managedModelsReady() bool {
	if a.magpie == nil || !a.magpie.Healthy() {
		return false
	}
	target := a.magpieRouteTargets().Chat
	if target == "" {
		return false
	}
	wanted, isGroup := strings.CutPrefix(target, "group/")
	if !isGroup {
		// External gateways without writable groups can serve their
		// advertised concrete fallback model directly.
		return true
	}
	for _, group := range a.magpieModels.snapshot().Groups {
		if group.ID == wanted && len(group.Members) > 0 {
			return true
		}
	}
	return false
}

// syncMagpieSidecar starts the sidecar in managed and external modes and
// stops it otherwise.
// After a successful start it runs bootstrap before the caller builds the
// runner, so the groups match the current consent. A start that fails leaves
// the runner on the fallback path; the supervisor keeps retrying, and the
// first healthy transition bootstraps.
func (a *App) syncMagpieSidecar() {
	if a.magpie == nil {
		return
	}
	// Also withdraw stale bundled credentials when this provider does not
	// start magpie, or when the user's own magpie is selected.
	if err := a.magpie.reconcileByok(); err != nil {
		a.magpie.Stop()
		log.Printf("[magpie] credential reconciliation: %v", err)
		return
	}
	if !a.modelSidecarActive() {
		a.magpie.Stop()
		return
	}
	ctx := a.contextOrBackground()
	// A magpie the user installed themselves is used as it is: its gateway,
	// its routing group. The bundled copy only runs when there is none.
	if in, ok := detectUserMagpieInstall(); ok && a.magpie.adoptUserInstall(in) {
		log.Printf("[magpie] using the user's magpie at %s (%s)", in.ServerURL(), in.BinaryPath)
	}
	a.startMagpieQuotaLoop(ctx)
	if err := a.magpie.Start(ctx); err != nil {
		log.Printf("[magpie] sidecar not ready: %v", err)
		return
	}
	a.bootstrapManagedModelsLogged()
}

// onMagpieHealthy runs when the sidecar turns healthy, including after a
// restart. It re-applies the groups in the background.
func (a *App) onMagpieHealthy() {
	a.bootstrapManagedModelsLogged()
}

// bootstrapManagedModelsLogged runs bootstrap under a bounded context and logs
// the error. The error also lands in the cached status.
func (a *App) bootstrapManagedModelsLogged() {
	ctx, cancel := context.WithTimeout(a.contextOrBackground(), magpieBootstrapTimeout)
	defer cancel()
	if err := a.bootstrapManagedModels(ctx); err != nil {
		log.Printf("[magpie] bootstrap: %v", err)
	}
}

// bootstrapManagedModels reconciles the magpie instance and refreshes the
// cached status and quotas. It emits model-accounts:updated afterwards.
func (a *App) bootstrapManagedModels(ctx context.Context) error {
	if a.magpie == nil {
		return nil
	}
	opts := magpieBootstrapOptions{
		Provider:        loadAIProviderConfig(),
		ConsentAccepted: magpieConsentAccepted(a.magpie.stateDir),
		ReloadProfile:   true,
	}
	state, err := a.magpie.Bootstrap(ctx, opts)
	a.magpieModels.applyBootstrap(state, err)
	if err == nil {
		err = a.initializeMagpieProfile(ctx)
		if err != nil {
			a.magpieModels.applyBootstrap(magpieBootstrapState{}, err)
		}
	}
	if err == nil {
		a.pollMagpieQuotas(ctx)
		a.refreshAgentChoices(ctx)
	}
	a.emitModelAccountsUpdated()
	return err
}

// emitModelAccountsUpdated sends the current status to the frontend.
func (a *App) emitModelAccountsUpdated() {
	a.emitBrowserEvent(magpieModelsUpdatedEvent, a.modelAccountsStatus())
}

// magpieStateDir returns the sidecar state directory, or "" when unavailable.
func (a *App) magpieStateDir() string {
	if a.magpie == nil {
		return ""
	}
	return a.magpie.stateDir
}

func (a *App) contextOrBackground() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// modelAccountsStatus assembles the status from the sidecar snapshot, the
// cached model state and the consent file. It never blocks on the gateway.
func (a *App) modelAccountsStatus() ModelAccountsStatus {
	return a.modelAccountsStatusFromSnapshot(a.magpieModels.snapshot())
}

func (a *App) modelAccountsStatusFromSnapshot(snap magpieModelSnapshot) ModelAccountsStatus {
	cfg := loadAIProviderConfig()

	mode := "advanced"
	switch {
	case cfg.isExternalAgent():
		mode = "external"
	case cfg.managedActive():
		mode = "managed"
	}

	var sidecar SidecarStatus
	if a.magpie != nil {
		sidecar = sidecarStatusView(a.magpie.Status())
	}

	consent, hasConsent := loadMagpieConsent(a.magpieStateDir())
	accepted := hasConsent && consent.Version == magpieConsentVersion
	status := ModelAccountsStatus{
		Mode:     mode,
		Sidecar:  sidecar,
		Accounts: snap.Accounts,
		Groups:   snap.Groups,
		Consent: ConsentStatus{
			// The note is informational: subscriptions join without it.
			Required: !accepted && snap.Subscriptions && !snap.External,
			Accepted: accepted,
			Version:  magpieConsentVersion,
		},
		AccountOrder:   cfg.managedOrder(),
		Install:        a.magpieInstallView(),
		Ollama:         snap.Ollama,
		ByokRegistered: snap.ByokRegistered,
	}
	if accepted {
		status.Consent.AcceptedAt = consent.AcceptedAt.UTC().Format(time.RFC3339)
	}
	switch {
	case snap.BootstrapErr != "":
		status.Error = snap.BootstrapErr
	case snap.PollErr != "":
		status.Error = snap.PollErr
	}
	status.ResearchVia = a.researchRouteShown()
	return status
}

// magpieLocalAPIKeyPlaceholder is sent as x-api-key. Loopback magpie accepts
// any key, so the value only has to be present.
const magpieLocalAPIKeyPlaceholder = magpieLocalAPIKey

// applyManagedRunnerConfig points the native runner at the sidecar. The
// Anthropic client appends /v1/messages to BaseURL. DisableEnvFallback keeps
// the runner from picking up an ANTHROPIC_BASE_URL from the environment. The
// default model is the chat alias, so an unset model routes to yansu-chat.
func applyManagedRunnerConfig(cfg *agent.NativeRunnerConfig, baseURL string, targets magpieRouteTargets) {
	cfg.ProviderFormat = "anthropic"
	cfg.BaseURL = baseURL
	cfg.APIKey = magpieLocalAPIKeyPlaceholder
	cfg.AuthToken = ""
	cfg.DisableEnvFallback = true
	cfg.DefaultModel = defaultClaudeChatModel
	cfg.ModelAliases = managedModelAliasesFor(targets.Chat, targets.Light)
	cfg.BackgroundModelAliases = managedModelAliasesFor(targets.Background, targets.Light)
}

func sidecarStatusView(snap magpieSidecarSnapshot) SidecarStatus {
	return SidecarStatus{
		Available:  snap.Available,
		Running:    snap.Running,
		Healthy:    snap.Healthy,
		Degraded:   snap.Degraded,
		Port:       snap.Port,
		Version:    snap.Version,
		LastError:  snap.LastError,
		BinaryPath: snap.BinaryPath,
	}
}
