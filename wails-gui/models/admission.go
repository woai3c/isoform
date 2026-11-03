package models

import (
	"errors"
	"os"
	"path/filepath"
)

var ErrNoSpace = errors.New("not enough free disk space")

type NetworkState struct {
	Known       bool `json:"known"`
	Offline     bool `json:"offline"`
	Expensive   bool `json:"expensive"`
	Constrained bool `json:"constrained"`
}

func (n NetworkState) pauseReason() string {
	if !n.Known {
		return "Waiting for network status"
	}
	if n.Offline {
		return "Paused while offline"
	}
	if n.Expensive || n.Constrained {
		return "Paused on a metered connection"
	}
	return ""
}

// remainingSpace includes staging: the existing installation is retained until
// its replacement is verified. Only a resumable prefix reduces transfer space.
func (m *Manager) remainingSpace(e Entry) int64 {
	if presentAt(m.folder, m.legacy, e) {
		return 0
	}
	var needed int64
	for _, f := range e.Files {
		dest := filepath.Join(m.folder, f.Path)
		if !recognized(dest, f.Bytes, f.SHA256) {
			needed += *f.Bytes - resumablePrefix(dest, *f.Bytes, *f.SHA256)
		}
		if f.Archive != nil && !archivePresent(m.folder, f) && !archivePresent(m.legacy, f) {
			for _, member := range f.Archive.Files {
				needed += member.Bytes
			}
		}
	}
	return needed
}
func resumablePrefix(dest string, total int64, sha string) int64 {
	st, err := os.Stat(dest + ".partial")
	if err == nil && st.Size() == total && recognized(dest+".partial", &total, &sha) {
		return total
	}
	validator, _ := os.ReadFile(dest + ".partial.validator")
	if err == nil && st.Mode().IsRegular() && st.Size() < total && len(validator) > 0 {
		return st.Size()
	}
	return 0
}
func (m *Manager) reservedExcept(ids map[string]bool) int64 {
	var n int64
	for id, reserved := range m.reserved {
		if !ids[id] {
			n += reserved
		}
	}
	return n
}
func (m *Manager) spaceNeeded(ids []string) int64 {
	selected := map[string]bool{}
	var n int64
	for _, id := range ids {
		if selected[id] {
			continue
		}
		selected[id] = true
		e, _ := m.entry(id)
		n += m.remainingSpace(e)
	}
	return n + m.reservedExcept(selected)
}
func (m *Manager) admitLocked(id string) error {
	job := m.jobs[id]
	if m.networkProbe != nil {
		m.network = m.networkProbe()
	}
	if reason := m.network.pauseReason(); reason != "" {
		job.State, job.PauseReason = "paused", reason
		return nil
	}
	e, _ := m.entry(id)
	needed := m.remainingSpace(e)
	free, err := m.free(m.folder)
	if err == nil && free < needed+m.reservedExcept(map[string]bool{id: true}) {
		err = ErrNoSpace
	}
	if err != nil {
		job.State, job.PauseReason = "paused", err.Error()
		return err
	}
	m.reserved[id] = needed
	job.PauseReason = ""
	job.State = "queued"
	return nil
}
func (m *Manager) consumeReservation(id string, bytes int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reserved[id] -= bytes
	if m.reserved[id] < 0 {
		m.reserved[id] = 0
	}
}
func (m *Manager) restoreReservation(id string, bytes int64) error {
	if bytes == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	free, err := m.free(m.folder)
	if err != nil {
		return err
	}
	if free < m.reservedExcept(nil)+bytes {
		return ErrNoSpace
	}
	m.reserved[id] += bytes
	return nil
}

// SetNetworkState never adds authority. It can only pause active confirmed jobs
// or resume those same jobs after the path becomes suitable again.
func (m *Manager) SetNetworkState(state NetworkState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return
	}
	m.network = state
	reason := state.pauseReason()
	for _, id := range m.order {
		j := m.jobs[id]
		if reason != "" && (m.running[id] || j.State == "queued" || j.State == "paused") {
			j.State, j.PauseReason = "paused", reason
			if m.running[id] {
				m.jobCancels[id]()
			}
		} else if reason == "" && !m.running[id] && (j.State == "queued" || j.State == "paused") {
			_ = m.launchLocked(id)
		}
	}
	_ = m.saveLocked()
	m.changedLocked()
}
func (m *Manager) ResumeDownload(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok || (j.State != "paused" && j.State != "queued") {
		return errors.New("no paused confirmed download")
	}
	// Resuming a job confirmed with Later starts it now.
	j.Deferred = false
	m.batch[id] = true
	err := m.launchLocked(id)
	if saveErr := m.saveLocked(); saveErr != nil {
		return saveErr
	}
	m.changedLocked()
	return err
}

// NetworkSnapshot reads the same native path used for download admission.
// Reading status does not confirm, start or resume a download.
func (m *Manager) NetworkSnapshot() NetworkState {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.networkProbe != nil {
		return m.networkProbe()
	}
	// Before the launch worker starts the native monitor there is no path
	// observation yet. A display read must not assume the path is online.
	return NetworkState{}
}
