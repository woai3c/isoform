package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"yansu-agent/internal/config"
)

// ProjectEntry is a registered project in the local registry.
type ProjectEntry struct {
	Ref  string // "org/product/project"
	Path string // absolute local directory path
}

// ProjectRegistry maps project references to local directory paths.
// Stored at ~/.yansu-agent/projects.json.
type ProjectRegistry struct {
	entries map[string]string // ref → path
	path    string
}

// NewProjectRegistry creates a new registry backed by the default file path.
func NewProjectRegistry() *ProjectRegistry {
	configDir, _ := config.ConfigDir()
	return &ProjectRegistry{
		entries: make(map[string]string),
		path:    filepath.Join(configDir, "projects.json"),
	}
}

// Load reads the registry from disk. Returns nil error if the file does not exist.
func (r *ProjectRegistry) Load() error {
	data, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read projects registry: %w", err)
	}
	if err := json.Unmarshal(data, &r.entries); err != nil {
		return fmt.Errorf("failed to parse projects registry: %w", err)
	}
	return nil
}

// Save writes the registry to disk.
func (r *ProjectRegistry) Save() error {
	dir := filepath.Dir(r.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	data, err := json.MarshalIndent(r.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal projects registry: %w", err)
	}
	if err := os.WriteFile(r.path, data, 0600); err != nil {
		return fmt.Errorf("failed to write projects registry: %w", err)
	}
	return nil
}

// Register adds or updates a project entry.
func (r *ProjectRegistry) Register(ref, path string) error {
	r.entries[ref] = path
	return r.Save()
}

// Unregister removes a project entry by ref. Returns an error if the ref is not registered.
func (r *ProjectRegistry) Unregister(ref string) error {
	if _, ok := r.entries[ref]; !ok {
		return fmt.Errorf("project %q is not registered", ref)
	}
	delete(r.entries, ref)
	return r.Save()
}

// List returns all registered projects.
func (r *ProjectRegistry) List() []ProjectEntry {
	entries := make([]ProjectEntry, 0, len(r.entries))
	for ref, path := range r.entries {
		entries = append(entries, ProjectEntry{Ref: ref, Path: path})
	}
	return entries
}

// Resolve finds a project path by reference. Supports exact match and prefix match.
// Returns an error listing candidates if the ref is ambiguous.
func (r *ProjectRegistry) Resolve(ref string) (string, error) {
	// Exact match first
	if path, ok := r.entries[ref]; ok {
		return path, nil
	}

	// Prefix match: "acme/web-app" matches "acme/web-app/feat-x"
	var matches []ProjectEntry
	for k, v := range r.entries {
		if strings.HasPrefix(k, ref+"/") {
			matches = append(matches, ProjectEntry{Ref: k, Path: v})
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("project %q not found in registry (run 'yansu daemon register' in the project directory)", ref)
	case 1:
		return matches[0].Path, nil
	default:
		refs := make([]string, len(matches))
		for i, m := range matches {
			refs[i] = m.Ref
		}
		return "", &AmbiguousProjectError{Ref: ref, Candidates: matches}
	}
}

// AmbiguousProjectError is returned when a ref matches multiple projects.
type AmbiguousProjectError struct {
	Ref        string
	Candidates []ProjectEntry
}

func (e *AmbiguousProjectError) Error() string {
	refs := make([]string, len(e.Candidates))
	for i, c := range e.Candidates {
		refs[i] = c.Ref
	}
	return fmt.Sprintf("ambiguous project ref %q — matches: %s", e.Ref, strings.Join(refs, ", "))
}
