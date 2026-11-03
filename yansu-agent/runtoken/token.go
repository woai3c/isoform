// Package runtoken carries desktop-issued, memory-only credentials for one run.
// Possession authenticates a run, never a new authority grant.
package runtoken

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"yansu-agent/agent/authority"
)

const (
	TokenEnv  = "YANSU_RUN_TOKEN"
	PortEnv   = "YANSU_RUN_PORT"
	ScopedEnv = "YANSU_RUN_SCOPED"
)

var ErrInvalid = errors.New("invalid or expired run token")

type Endpoint struct {
	Method string
	// A trailing slash permits descendants of this route, never sibling routes.
	Path string
}

type Scope struct {
	RunID      string
	SessionID  string
	Workspace  string
	Endpoints  []Endpoint
	Grants     []authority.Grant
	Unattended bool
	ExpiresAt  time.Time
}

func (s Scope) Allows(method, path string) bool {
	for _, endpoint := range s.Endpoints {
		if endpoint.Method == method && (endpoint.Path == path ||
			strings.HasSuffix(endpoint.Path, "/") && strings.HasPrefix(path, endpoint.Path)) {
			return true
		}
	}
	return false
}

// Workspace resolves aliases before comparing job ownership. The directory
// must already exist; an unresolved path can never broaden a credential.
func Workspace(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("run workspace must be absolute")
	}
	return filepath.EvalSymlinks(filepath.Clean(path))
}

type record struct {
	scope Scope
	ctx   context.Context
}

type Registry struct {
	mu     sync.Mutex
	tokens map[[32]byte]record
	closed bool
}

func clone(scope Scope) Scope {
	scope.Endpoints = append([]Endpoint(nil), scope.Endpoints...)
	scope.Grants = append([]authority.Grant(nil), scope.Grants...)
	return scope
}

// Mint requires a bounded run context. Cancellation is checked synchronously
// on every lookup, so access ends before asynchronous cleanup runs.
func (r *Registry) Mint(ctx context.Context, scope Scope) (string, func(), error) {
	deadline, ok := ctx.Deadline()
	if !ok || ctx.Err() != nil || !time.Now().Before(deadline) || scope.RunID == "" || scope.SessionID == "" {
		return "", nil, ErrInvalid
	}
	workspace, err := Workspace(scope.Workspace)
	if err != nil {
		return "", nil, err
	}
	scope = clone(scope)
	scope.Workspace, scope.ExpiresAt = workspace, deadline
	for i := range scope.Grants {
		// Authority compares filesystem resources lexically. Freeze their
		// resolved paths just like the workspace; leave URL, opencli and
		// other non-filesystem grant prefixes with their existing semantics.
		prefix := strings.TrimSpace(scope.Grants[i].ResourcePrefix)
		if filepath.IsAbs(prefix) {
			resolved, err := Workspace(prefix)
			if err != nil {
				return "", nil, err
			}
			scope.Grants[i].ResourcePrefix = resolved
		}
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", nil, err
	}
	token := "run_" + hex.EncodeToString(random)
	key := sha256.Sum256([]byte(token))
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return "", nil, ErrInvalid
	}
	if r.tokens == nil {
		r.tokens = make(map[[32]byte]record)
	}
	r.tokens[key] = record{scope: scope, ctx: ctx}
	r.mu.Unlock()
	revoke := func() { r.mu.Lock(); delete(r.tokens, key); r.mu.Unlock() }
	stop := context.AfterFunc(ctx, revoke)
	return token, func() { stop(); revoke() }, nil
}

func (r *Registry) Verify(token string) (Scope, error) {
	key := sha256.Sum256([]byte(token))
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.tokens[key]
	if !ok || entry.ctx.Err() != nil || !time.Now().Before(entry.scope.ExpiresAt) {
		delete(r.tokens, key)
		return Scope{}, ErrInvalid
	}
	return clone(entry.scope), nil
}

func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	r.tokens = nil
}

// RevokeAll drops outstanding tokens when the HTTP listener stops. The desktop
// may still run text-only startup probes or bring up a replacement listener.
// Close additionally prevents issuance during application shutdown.
func (r *Registry) RevokeAll() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokens = nil
}

type Credential struct {
	Token string
	Port  int
}

type credentialKey struct{}

func WithCredential(ctx context.Context, credential Credential) context.Context {
	return context.WithValue(ctx, credentialKey{}, credential)
}

func FromContext(ctx context.Context) (Credential, bool) {
	credential, ok := ctx.Value(credentialKey{}).(Credential)
	return credential, ok
}

// ChildEnv never takes a run credential from ambient or caller-supplied env.
// Only the desktop's typed context can introduce it after normal env scrubbing.
func ChildEnv(ctx context.Context, env []string) []string {
	result := make([]string, 0, len(env)+3)
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(key) {
		case "YANSU_CRON_SECRET", "YANSU_CRON_PORT", TokenEnv, PortEnv, ScopedEnv:
			continue
		}
		result = append(result, entry)
	}
	if credential, ok := FromContext(ctx); ok {
		result = append(result, ScopedEnv+"=1", TokenEnv+"="+credential.Token, PortEnv+"="+strconv.Itoa(credential.Port))
	}
	return result
}
