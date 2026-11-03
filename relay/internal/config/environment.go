package config

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Environment resolves current relay settings and their deprecated legacy names.
type Environment struct {
	getenv func(string) string
	logf   func(string, ...any)

	mu        sync.Mutex
	warned    map[string]struct{}
	conflicts map[string]struct{}
	errs      []error
}

// NewEnvironment creates an environment reader for one process startup.
func NewEnvironment(getenv func(string) string, logf func(string, ...any)) *Environment {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Environment{
		getenv:    getenv,
		logf:      logf,
		warned:    make(map[string]struct{}),
		conflicts: make(map[string]struct{}),
	}
}

// Get reads an ISOFORM_RELAY_* name and falls back to its YANSU_RELAY_* alias.
func (e *Environment) Get(name string) string {
	const prefix = "ISOFORM_RELAY_"
	if !strings.HasPrefix(name, prefix) {
		return e.getenv(name)
	}

	legacyName := "YANSU_RELAY_" + strings.TrimPrefix(name, prefix)
	current, legacy := e.getenv(name), e.getenv(legacyName)
	if current != "" && legacy != "" && current != legacy {
		e.mu.Lock()
		if _, exists := e.conflicts[name]; !exists {
			e.conflicts[name] = struct{}{}
			e.errs = append(e.errs, fmt.Errorf("%s and %s are both set with different values", name, legacyName))
		}
		e.mu.Unlock()
		return current
	}
	if current != "" {
		return current
	}
	if legacy == "" {
		return ""
	}

	e.mu.Lock()
	_, warned := e.warned[legacyName]
	if !warned {
		e.warned[legacyName] = struct{}{}
	}
	e.mu.Unlock()
	if !warned {
		e.logf("deprecated environment variable %s; use %s", legacyName, name)
	}
	return legacy
}

// Err reports conflicts between current and legacy environment names.
func (e *Environment) Err() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return errors.Join(e.errs...)
}
