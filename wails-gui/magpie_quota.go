package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"
)

// Quota polling. While the sidecar is healthy, GET /v1/magpie/quotas runs once
// after each bootstrap and then every magpieQuotaPollInterval. The result is
// cached so the bindings never block on the gateway. A change in the account
// list emits model-accounts:updated.
//
// Numbers come only from magpie. A subscription without windows is reported
// as it is (no windows, no balance) and the UI shows it as in use.

const (
	magpieQuotaPollInterval  = 60 * time.Second
	magpieQuotasPath         = "/v1/magpie/quotas"
	magpieModelsUpdatedEvent = "model-accounts:updated"
)

type magpieQuotaWindowRow struct {
	Unlimited bool    `json:"unlimited,omitempty"`
	Name      string  `json:"name"`
	Used      float64 `json:"used"`
	Remaining float64 `json:"remaining"`
	ResetsAt  string  `json:"resetsAt"`
}

type magpieQuotaRow struct {
	Provider string                 `json:"provider"`
	Name     string                 `json:"name"`
	Kind     string                 `json:"kind"`
	Plan     string                 `json:"plan"`
	User     string                 `json:"user"`
	Balance  string                 `json:"balance"`
	Error    string                 `json:"error"`
	ReadAt   string                 `json:"readAt"`
	Windows  []magpieQuotaWindowRow `json:"windows"`
}

// fetchQuotas reads GET /v1/magpie/quotas from the running gateway.
func (s *magpieSidecar) fetchQuotas(ctx context.Context) ([]magpieQuotaRow, error) {
	s.mu.Lock()
	port := s.port
	s.mu.Unlock()
	if port == 0 {
		return nil, errMagpieNotHealthy
	}
	status, body, err := s.probe(ctx, port, magpieQuotasPath)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("magpie quotas returned %d", status)
	}
	var payload struct {
		Data []magpieQuotaRow `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode magpie quotas: %w", err)
	}
	return payload.Data, nil
}

// buildModelAccounts turns quota rows into the bound account shape. On comes
// from `accounts --json` when that snapshot has the provider, otherwise from
// whether the row reports an error.
func buildModelAccounts(rows []magpieQuotaRow, info map[string]magpieAccountInfo) []ModelAccount {
	accounts := make([]ModelAccount, 0, len(rows))
	for _, row := range rows {
		account := ModelAccount{
			Provider: row.Provider,
			Name:     row.Name,
			Kind:     row.Kind,
			Plan:     row.Plan,
			User:     row.User,
			On:       row.Error == "",
			Balance:  row.Balance,
			Windows:  make([]QuotaWindow, 0, len(row.Windows)),
			ReadAt:   row.ReadAt,
			Error:    row.Error,
		}
		if detail, ok := info[row.Provider]; ok {
			account.On = detail.On
			if account.Plan == "" {
				account.Plan = detail.Plan
			}
			if account.User == "" {
				account.User = detail.User
			}
		}
		for _, w := range row.Windows {
			account.Windows = append(account.Windows, QuotaWindow{
				Name:      w.Name,
				Unlimited: w.Unlimited,
				Used:      w.Used,
				Remaining: w.Remaining,
				ResetsAt:  w.ResetsAt,
			})
		}
		accounts = append(accounts, account)
	}
	return accounts
}

// magpieModelState is the cached view the bindings read. It has its own
// mutex, separate from the sidecar's, so reads never wait on a CLI call.
type magpieModelState struct {
	mu sync.Mutex

	accounts      []ModelAccount
	accountsJSON  []byte
	groups        []ModelGroup
	ollama        bool
	byok          bool
	subscriptions bool
	// info is the accounts snapshot from the last successful bootstrap.
	info map[string]magpieAccountInfo
	// external, activeGroup and agentModels describe the user's own magpie
	// when the sidecar fronts one (see bootstrapExternal).
	external      bool
	activeGroup   string
	fallbackModel string
	agentModels   map[string]string

	bootstrapErr string
	pollErr      string
	pollAt       time.Time
}

// magpieModelSnapshot is a copy of magpieModelState for the bindings.
type magpieModelSnapshot struct {
	Accounts       []ModelAccount
	Groups         []ModelGroup
	Ollama         bool
	ByokRegistered bool
	Subscriptions  bool
	External       bool
	ActiveGroup    string
	FallbackModel  string
	AgentModels    map[string]string
	BootstrapErr   string
	PollErr        string
	PollAt         time.Time
}

func (m *magpieModelState) setOwnedProfile(groups []magpieGroupInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, group := range groups {
		if group.ID != magpieIsoformGroupID {
			continue
		}
		m.groups = slices.DeleteFunc(m.groups, func(item ModelGroup) bool { return item.ID == magpieIsoformGroupID })
		m.groups = append(m.groups, ModelGroup{ID: group.ID, Name: group.Name, Routing: group.Routing, Members: slices.Clone(group.Members)})
		m.activeGroup = group.ID
		return
	}
}

// applyBootstrap records the groups and detection results of a bootstrap run.
func (m *magpieModelState) applyBootstrap(state magpieBootstrapState, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.bootstrapErr = err.Error()
		return
	}
	m.bootstrapErr = ""
	m.groups = make([]ModelGroup, 0, len(state.Groups))
	for _, plan := range state.Groups {
		members := append([]string{}, plan.Members...)
		m.groups = append(m.groups, ModelGroup{
			ID:      plan.ID,
			Name:    plan.Name,
			Routing: plan.Routing,
			Members: members,
		})
	}
	m.ollama = state.Ollama
	m.byok = state.ByokRegistered
	m.subscriptions = state.Subscriptions
	m.info = state.Accounts
	m.external = state.External
	m.activeGroup = state.ActiveGroup
	m.fallbackModel = state.FallbackModel
	m.agentModels = state.AgentModels
}

// applyQuotas stores a poll result. It reports whether the account list
// changed, so the caller emits the update event only then.
func (m *magpieModelState) applyQuotas(rows []magpieQuotaRow, err error, now time.Time) (changed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pollAt = now
	if err != nil {
		m.pollErr = err.Error()
		return false
	}
	m.pollErr = ""
	accounts := buildModelAccounts(rows, m.info)
	encoded, encErr := json.Marshal(accounts)
	if encErr != nil {
		m.pollErr = encErr.Error()
		return false
	}
	if string(encoded) == string(m.accountsJSON) {
		return false
	}
	m.accounts = accounts
	m.accountsJSON = encoded
	return true
}

func (m *magpieModelState) snapshot() magpieModelSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	agentModels := make(map[string]string, len(m.agentModels))
	for provider, model := range m.agentModels {
		agentModels[provider] = model
	}
	return magpieModelSnapshot{
		Accounts:       append([]ModelAccount{}, m.accounts...),
		Groups:         append([]ModelGroup{}, m.groups...),
		Ollama:         m.ollama,
		ByokRegistered: m.byok,
		Subscriptions:  m.subscriptions,
		External:       m.external,
		ActiveGroup:    m.activeGroup,
		FallbackModel:  m.fallbackModel,
		AgentModels:    agentModels,
		BootstrapErr:   m.bootstrapErr,
		PollErr:        m.pollErr,
		PollAt:         m.pollAt,
	}
}

// pollMagpieQuotas fetches quotas once and emits model-accounts:updated when
// the account list changed. It reports whether the list changed.
func (a *App) pollMagpieQuotas(ctx context.Context) bool {
	if a.magpie == nil || !a.magpie.Healthy() {
		return false
	}
	rows, err := a.magpie.fetchQuotas(ctx)
	changed := a.magpieModels.applyQuotas(rows, err, time.Now())
	if changed {
		a.emitModelAccountsUpdated()
	}
	return changed
}

// startMagpieQuotaLoop polls on the poll interval for the life of ctx. It
// starts once per App.
func (a *App) startMagpieQuotaLoop(ctx context.Context) {
	a.magpieQuotaOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(magpieQuotaPollInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					a.pollMagpieQuotas(ctx)
				}
			}
		}()
	})
}
