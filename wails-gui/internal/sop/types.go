// Package sop is the file-first store for Yansu SOPs — one directory bundle
// per SOP under ~/.yansu-agent/sops/<slug>/. A bundle holds SOP.md (fenced
// frontmatter + the process document) plus an optional scripts/ directory of
// agent-authored helper scripts. Bundles are the unit of future export/share,
// which is why an SOP is a directory rather than a single file. The package is
// pure (stdlib only) so it compiles and tests in isolation, without the
// cgo-heavy `package main`.
package sop

// Status is the lifecycle state of an SOP. Auto-generated SOPs start as
// `draft`; the first user edit promotes them to `confirmed`. SOPs are durable
// process definitions, so unlike handoffs there is no stale/GC state.
type Status string

const (
	StatusDraft     Status = "draft"
	StatusConfirmed Status = "confirmed"
)

// Source records which path produced the SOP.
type Source string

const (
	SourceTriage   Source = "triage"
	SourceManual   Source = "manual"
	SourceImported Source = "imported" // shared bundle brought in via zip import
	// SourceObserved is a playbook the routine detector wrote from a site the
	// user opened at about the same time on most recent days.
	SourceObserved Source = "observed"
)

// Entry is one SOP bundle. The frontmatter of SOP.md carries the machine
// fields; Body is the process document after the fence. Scripts is discovered
// by scanning the bundle's scripts/ directory at load time and is never
// persisted in frontmatter.
type Entry struct {
	ID             string   `json:"id"` // slug, equals the bundle directory name
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Trigger        string   `json:"trigger"` // when-to-use: the situation that starts the process
	Status         Status   `json:"status"`
	Source         Source   `json:"source"`
	Scope          string   `json:"scope"` // mine by default; metadata, never authority to share
	TopicIDs       []string `json:"topicIds,omitempty"`
	SourceIDs      []string `json:"sourceIds"`                // activity group/session ids the SOP was distilled from
	ReadProvenance string   `json:"readProvenance,omitempty"` // code-owned source restrictions; never taken from an authored draft
	Version        int      `json:"version"`                  // bumped on every authored update
	CreatedAt      int64    `json:"createdAt"`                // unix ms
	UpdatedAt      int64    `json:"updatedAt"`                // unix ms
	Scripts        []string `json:"scripts"`                  // relative paths under scripts/, discovered at load time
	// Sites are the web addresses (scheme, host and path prefix) the steps
	// open. They are the playbook's own claim; an unattended run may open
	// only the sites the user approved when scheduling it (Plan.Sites).
	Sites []string `json:"sites"`
	Body  string   `json:"body"` // SOP.md content after the frontmatter

	// UpdateExisting is transport-only: the author agent's dedup verdict on a
	// create pass ("this draft IS the existing SOP <slug>"). Parsed from the
	// draft's frontmatter, acted on by the installer, never persisted.
	UpdateExisting string `json:"-"`
	metadataErr    error
}

// ImportScopePolicy accepts a bundle's requested scope only after the importer
// has confirmed it. A nil policy keeps every import in mine. It must not call
// back into the store, whose lock is held during installation.
type ImportScopePolicy func(requestedScope string) bool
