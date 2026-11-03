// Package plugins describes compiled-in features without taking ownership of
// their persisted settings or the host's privacy and authority gates.
package plugins

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// Manifest describes a plugin and the services needed before it can be applied.
// An ID also provides a service of the same name. Apply must register cleanup
// with its Scope immediately after each successful contribution.
type Manifest struct {
	ID             string                              `json:"id"`
	NameKey        string                              `json:"nameKey"`
	DescriptionKey string                              `json:"descriptionKey"`
	Version        string                              `json:"version"`
	Source         string                              `json:"source"`
	Kinds          []string                            `json:"kinds"`
	Essential      bool                                `json:"essential"`
	Requires       []string                            `json:"requires"`
	Provides       []string                            `json:"provides"`
	Apply          func(context.Context, *Scope) error `json:"-"`
}

// Scope owns the contributions made during a single Apply. Call OnDispose
// synchronously during Apply; cleanup runs in reverse registration order both
// when Apply fails and when the plugin is disabled.
type Scope struct {
	cleanup []func()
}

func (s *Scope) OnDispose(cleanup func()) {
	if cleanup != nil {
		s.cleanup = append(s.cleanup, cleanup)
	}
}

func (s *Scope) dispose() {
	for i := len(s.cleanup) - 1; i >= 0; i-- {
		s.cleanup[i]()
	}
	s.cleanup = nil
}

// Registry serializes lifecycle operations. Read access remains available from
// Apply and cleanup callbacks; those callbacks must not mutate this registry.
type Registry struct {
	opMu      sync.Mutex
	mu        sync.RWMutex
	manifests []Manifest
	active    map[string]*Scope
}

func NewRegistry() *Registry {
	return &Registry{active: make(map[string]*Scope)}
}

// Register panics on duplicate or empty IDs, which are programming mistakes in
// the compiled-in catalog. Dependency validation happens after registration.
func (r *Registry) Register(manifest Manifest) {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if manifest.ID == "" {
		panic("plugin ID is empty")
	}
	for _, existing := range r.manifests {
		if existing.ID == manifest.ID {
			panic("duplicate plugin ID: " + manifest.ID)
		}
	}
	r.manifests = append(r.manifests, cloneManifest(manifest))
}

func cloneManifest(manifest Manifest) Manifest {
	manifest.Kinds = slices.Clone(manifest.Kinds)
	manifest.Requires = slices.Clone(manifest.Requires)
	manifest.Provides = slices.Clone(manifest.Provides)
	return manifest
}

// Manifests returns a copy in registration order, without applying any plugin.
func (r *Registry) Manifests() []Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Manifest, len(r.manifests))
	for i, manifest := range r.manifests {
		result[i] = cloneManifest(manifest)
	}
	return result
}

func (r *Registry) ValidateSet(id string, on bool) error {
	for _, manifest := range r.Manifests() {
		if manifest.ID != id {
			continue
		}
		if manifest.Essential && !on {
			return fmt.Errorf("essential plugin %q cannot be turned off", id)
		}
		return nil
	}
	return fmt.Errorf("unknown plugin %q", id)
}

// Change is one setting change. The App executes it through the existing setter.
type Change struct {
	ID string
	On bool
}

// SetPlan computes only the changes needed for an explicit toggle. Enabling
// starts providers first; disabling stops dependents first. It neither writes
// settings nor applies plugins, and it never changes the supplied snapshot.
func (r *Registry) SetPlan(id string, on bool, current map[string]bool) ([]Change, error) {
	if err := r.ValidateSet(id, on); err != nil {
		return nil, err
	}
	ordered, dependencies, err := dependencyOrder(r.Manifests())
	if err != nil {
		return nil, err
	}
	selected := map[string]bool{id: true}
	if on {
		var include func(string)
		include = func(pluginID string) {
			for _, provider := range dependencies[pluginID] {
				if !selected[provider] {
					selected[provider] = true
					include(provider)
				}
			}
		}
		include(id)
	} else {
		for _, manifest := range ordered {
			for _, provider := range dependencies[manifest.ID] {
				if selected[provider] {
					selected[manifest.ID] = true
				}
			}
			if selected[manifest.ID] && manifest.Essential {
				return nil, fmt.Errorf("plugin %q is required by essential plugin %q", id, manifest.ID)
			}
		}
		slices.Reverse(ordered)
	}
	changes := make([]Change, 0)
	for _, manifest := range ordered {
		if selected[manifest.ID] && current[manifest.ID] != on {
			changes = append(changes, Change{ID: manifest.ID, On: on})
		}
	}
	return changes, nil
}

// Reconcile applies the enabled snapshot without modifying it. A disabled or
// failed provider leaves its dependents inactive. A failed Apply rolls back its
// own contributions; unrelated plugins continue and their errors are joined.
// Essentials stay active even when absent from the snapshot.
func (r *Registry) Reconcile(ctx context.Context, enabled map[string]bool) error {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	ordered, dependencies, err := dependencyOrder(r.Manifests())
	if err != nil {
		return err
	}
	wanted := make(map[string]bool, len(ordered))
	for _, manifest := range ordered {
		on := manifest.Essential || enabled[manifest.ID]
		for _, provider := range dependencies[manifest.ID] {
			on = on && wanted[provider]
		}
		if manifest.Essential && !on {
			return fmt.Errorf("essential plugin %q requires a disabled provider", manifest.ID)
		}
		wanted[manifest.ID] = on
	}
	for i := len(ordered) - 1; i >= 0; i-- {
		id := ordered[i].ID
		if !wanted[id] {
			r.remove(id)
		}
	}
	var applyErrors []error
	for _, manifest := range ordered {
		if !wanted[manifest.ID] || r.isActive(manifest.ID) {
			continue
		}
		ready := true
		for _, provider := range dependencies[manifest.ID] {
			ready = ready && r.isActive(provider)
		}
		if !ready {
			continue
		}
		scope, err := apply(ctx, manifest)
		if err != nil {
			applyErrors = append(applyErrors, fmt.Errorf("apply plugin %q: %w", manifest.ID, err))
			continue
		}
		r.mu.Lock()
		if r.active == nil {
			r.active = make(map[string]*Scope)
		}
		r.active[manifest.ID] = scope
		r.mu.Unlock()
	}
	return errors.Join(applyErrors...)
}

func apply(ctx context.Context, manifest Manifest) (scope *Scope, err error) {
	scope = &Scope{}
	completed := false
	defer func() {
		if !completed {
			scope.dispose()
		}
	}()
	if err = ctx.Err(); err != nil {
		return scope, err
	}
	if manifest.Apply != nil {
		if err = manifest.Apply(ctx, scope); err != nil {
			return scope, err
		}
	}
	completed = true
	return scope, nil
}

func (r *Registry) isActive(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, active := r.active[id]
	return active
}

func (r *Registry) remove(id string) {
	r.mu.Lock()
	scope := r.active[id]
	delete(r.active, id)
	r.mu.Unlock()
	if scope != nil {
		scope.dispose()
	}
}

// ActiveIDs returns a stable snapshot of successfully applied plugin IDs.
func (r *Registry) ActiveIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.active))
	for id := range r.active {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func dependencyOrder(manifests []Manifest) ([]Manifest, map[string][]string, error) {
	providers := make(map[string]string)
	byID := make(map[string]Manifest, len(manifests))
	for _, manifest := range manifests {
		byID[manifest.ID] = manifest
		for _, service := range append([]string{manifest.ID}, manifest.Provides...) {
			if existing, ok := providers[service]; ok && existing != manifest.ID {
				return nil, nil, fmt.Errorf("service %q has multiple providers: %q and %q", service, existing, manifest.ID)
			}
			providers[service] = manifest.ID
		}
	}
	dependencies := make(map[string][]string, len(manifests))
	for _, manifest := range manifests {
		for _, service := range manifest.Requires {
			provider, ok := providers[service]
			if !ok {
				return nil, nil, fmt.Errorf("plugin %q requires missing service %q", manifest.ID, service)
			}
			dependencies[manifest.ID] = append(dependencies[manifest.ID], provider)
		}
	}
	ordered := make([]Manifest, 0, len(manifests))
	state := make(map[string]int, len(manifests))
	var visit func(string) error
	visit = func(id string) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("plugin dependency cycle at %q", id)
		case 2:
			return nil
		}
		state[id] = 1
		for _, provider := range dependencies[id] {
			if err := visit(provider); err != nil {
				return err
			}
		}
		state[id] = 2
		ordered = append(ordered, byID[id])
		return nil
	}
	for _, manifest := range manifests {
		if err := visit(manifest.ID); err != nil {
			return nil, nil, err
		}
	}
	return ordered, dependencies, nil
}
