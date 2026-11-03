package sop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// plansFileName holds every Plan beside the bundles. The dot keeps List from
// reading it as a bundle, and it is outside every bundle, so an exported
// playbook carries none of the user's decisions.
const plansFileName = ".plans.json"

// Answers to a proposal.
const (
	AnswerAccepted = "accepted"
	AnswerDeclined = "declined"
)

// Proposal is what the routine detector saw and asked about: a site the user
// opened at about the same time on most recent days.
type Proposal struct {
	Host     string `json:"host"`
	SiteName string `json:"siteName"`
	// Time is the typical first visit, local "15:04".
	Time string `json:"time"`
	// Days is on how many of the last seven days the site was opened near Time.
	Days int `json:"days"`
	// Weekdays is true when every one of those days was Monday to Friday.
	Weekdays   bool     `json:"weekdays"`
	Titles     []string `json:"titles"`
	ProposedAt int64    `json:"proposedAt"`
	Answer     string   `json:"answer"`
	AnsweredAt int64    `json:"answeredAt"`
	// SnoozedUntil hides an unanswered offer until then (Unix ms): Not now in
	// Tasks › Up next sets it two weeks out, and the offer comes back after.
	SnoozedUntil int64 `json:"snoozedUntil,omitempty"`
}

// Schedule is when a playbook runs by itself.
type Schedule struct {
	JobID string `json:"jobId"`
	// Time is the local run time, "15:04".
	Time     string `json:"time"`
	Weekdays bool   `json:"weekdays"`
	Enabled  bool   `json:"enabled"`
}

// RunRecord is the outcome of the latest run.
type RunRecord struct {
	RunID  string `json:"runId"`
	At     int64  `json:"at"`
	OK     bool   `json:"ok"`
	Result string `json:"result"`
}

// Plan is what the user decided about one playbook on this Mac: the answer to
// the detector's proposal, the schedule, where results go, and the sites an
// unattended run may open. Sites are copied from the playbook only when the
// user approves a schedule, so nothing written into SOP.md later (an author
// update, an edit by a run) widens what a run may do.
type Plan struct {
	ID       string     `json:"id"`
	Proposal *Proposal  `json:"proposal,omitempty"`
	Schedule *Schedule  `json:"schedule,omitempty"`
	Channels []string   `json:"channels"`
	Sites    []string   `json:"sites"`
	LastRun  *RunRecord `json:"lastRun,omitempty"`
	// BodyHash pins the steps the user approved (BodyHash of the entry when
	// the schedule was saved or the playbook run by hand). A run whose
	// playbook no longer matches it is refused until the user looks again.
	BodyHash string `json:"bodyHash,omitempty"`
	// Removed marks a plan whose playbook was deleted; it is kept so the
	// detector never proposes the same site again.
	Removed   bool  `json:"removed"`
	UpdatedAt int64 `json:"updatedAt"`
}

// PlanStore reads and writes the plans file.
type PlanStore struct {
	path string
	mu   sync.Mutex

	// Now is overridable in tests; nil falls back to time.Now.
	Now func() time.Time
}

// NewPlanStore opens the plans file in dir (the bundles' directory).
func NewPlanStore(dir string) (*PlanStore, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("sop: empty plans dir")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("sop: mkdir %s: %w", dir, err)
	}
	return &PlanStore{path: filepath.Join(dir, plansFileName)}, nil
}

func (p *PlanStore) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *PlanStore) readLocked() (map[string]Plan, error) {
	data, err := os.ReadFile(p.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Plan{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sop: read plans: %w", err)
	}
	plans := map[string]Plan{}
	if len(strings.TrimSpace(string(data))) == 0 {
		return plans, nil
	}
	if err := json.Unmarshal(data, &plans); err != nil {
		return nil, fmt.Errorf("sop: parse plans: %w", err)
	}
	return plans, nil
}

func (p *PlanStore) writeLocked(plans map[string]Plan) error {
	data, err := json.MarshalIndent(plans, "", "  ")
	if err != nil {
		return err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("sop: write plans: %w", err)
	}
	if err := os.Rename(tmp, p.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("sop: commit plans: %w", err)
	}
	return nil
}

// Get returns the plan of one playbook and whether it exists.
func (p *PlanStore) Get(id string) (Plan, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	plans, err := p.readLocked()
	if err != nil {
		return Plan{}, false, err
	}
	plan, ok := plans[id]
	return plan, ok, nil
}

// List returns every plan, sorted by id.
func (p *PlanStore) List() ([]Plan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	plans, err := p.readLocked()
	if err != nil {
		return nil, err
	}
	out := make([]Plan, 0, len(plans))
	for _, plan := range plans {
		out = append(out, plan)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Update applies change to the plan of id (a new one when there is none) and
// saves it.
func (p *PlanStore) Update(id string, change func(*Plan)) (Plan, error) {
	if strings.TrimSpace(id) == "" {
		return Plan{}, errors.New("sop: plan with empty id")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	plans, err := p.readLocked()
	if err != nil {
		return Plan{}, err
	}
	plan := plans[id]
	plan.ID = id
	change(&plan)
	plan.UpdatedAt = p.now().UnixMilli()
	plans[id] = plan
	if err := p.writeLocked(plans); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// ProposedHost reports whether any plan, removed ones included, already holds
// a proposal for host.
func (p *PlanStore) ProposedHost(host string) (bool, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	plans, err := p.List()
	if err != nil {
		return false, err
	}
	for _, plan := range plans {
		if plan.Proposal != nil && strings.EqualFold(plan.Proposal.Host, host) {
			return true, nil
		}
	}
	return false, nil
}

// BodyHash is the fingerprint of what a run of the playbook executes: its
// title and its steps.
func BodyHash(e Entry) string {
	sum := sha256.Sum256([]byte(e.Title + "\x00" + e.Body))
	return hex.EncodeToString(sum[:])
}
