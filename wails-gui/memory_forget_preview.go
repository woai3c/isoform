package main

import (
	"fmt"
	"sync"
	"time"
)

const memoryForgetPreviewLifetime = 10 * time.Minute
const memoryForgetPreviewLimit = 64

type memoryForgetPreparedPlan struct {
	dir, id, baseToken  string
	expiresAt           time.Time
	semantics           memoryForgetSemantics
	historyVersionsOnly bool
	scopeIDs            []string
}

var memoryForgetPreparedPlans = struct {
	sync.Mutex
	entries map[string]memoryForgetPreparedPlan
}{entries: make(map[string]memoryForgetPreparedPlan)}

// GetMemoryForgetPreview uses local deterministic matching and any existing
// optional semantic receipts. It never calls a model.
// Confirmation rechecks the entire byte-bound deletion scope under the locks.
func (a *App) GetMemoryForgetPreview(id string) (MemoryForgetPreview, error) {
	return a.getMemoryForgetPreviewScoped(id, false)
}

func (a *App) getMemoryForgetPreviewScoped(id string, historyVersionsOnly bool) (MemoryForgetPreview, error) {
	preview, scope, err := a.readMemoryForgetPreviewScoped(id, historyVersionsOnly)
	if err != nil {
		return MemoryForgetPreview{}, err
	}
	dir, err := memoryDir()
	if err != nil {
		return MemoryForgetPreview{}, err
	}
	semantics, err := a.prepareMemoryForgetSemantics(scope.contents)
	if err != nil {
		return MemoryForgetPreview{}, fmt.Errorf("prepare temporary memory matching: %w", err)
	}
	plan := memoryForgetPreparedPlan{
		dir: dir, id: id, baseToken: preview.ConfirmationToken,
		expiresAt: time.Now().Add(memoryForgetPreviewLifetime), semantics: semantics, historyVersionsOnly: historyVersionsOnly,
		scopeIDs: append([]string(nil), scope.ids...),
	}
	preview.ConfirmationToken = memoryForgetHash(fmt.Sprintf("%s:%#v", plan.baseToken, semantics))
	memoryForgetPreparedPlans.Lock()
	defer memoryForgetPreparedPlans.Unlock()
	for token, old := range memoryForgetPreparedPlans.entries {
		if !time.Now().Before(old.expiresAt) {
			delete(memoryForgetPreparedPlans.entries, token)
		}
	}
	if len(memoryForgetPreparedPlans.entries) >= memoryForgetPreviewLimit {
		oldestToken := ""
		oldest := time.Now().Add(memoryForgetPreviewLifetime)
		for token, old := range memoryForgetPreparedPlans.entries {
			if old.expiresAt.Before(oldest) {
				oldestToken, oldest = token, old.expiresAt
			}
		}
		delete(memoryForgetPreparedPlans.entries, oldestToken)
	}
	memoryForgetPreparedPlans.entries[preview.ConfirmationToken] = plan
	return preview, nil
}

func loadMemoryForgetPreparedPlan(id, token string) (memoryForgetPreparedPlan, error) {
	dir, err := memoryDir()
	if err != nil {
		return memoryForgetPreparedPlan{}, err
	}
	memoryForgetPreparedPlans.Lock()
	defer memoryForgetPreparedPlans.Unlock()
	plan, ok := memoryForgetPreparedPlans.entries[token]
	if !ok || plan.id != id || plan.dir != dir || !time.Now().Before(plan.expiresAt) {
		return memoryForgetPreparedPlan{}, fmt.Errorf("memory forget preview expired or changed; review it again")
	}
	return plan, nil
}
