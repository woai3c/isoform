package main

import (
	"errors"
	"strings"
	"time"
)

// The answers a belief takes on About you, the portrait and New this week
// (answer feedback v1.0.0 section 11), and the Undo each keeps until the
// person leaves the page. That's me is ConfirmMemoryEntry and Not quite with
// words is the Edit wording save; these are the rest.

// UndoConfirmMemoryEntry is Undo after That's me on a belief.
func (a *App) UndoConfirmMemoryEntry(id string) error {
	return a.unconfirmMemoryEntry(id)
}

// NotQuiteMemoryEntry is Not quite left blank: the belief stops being used
// and keeps its place in About you. It returns the confirmation it took back,
// for Undo.
func (a *App) NotQuiteMemoryEntry(id string) (int64, error) {
	entry, err := a.GetMemoryEntry(id)
	if err != nil {
		return 0, err
	}
	confirmedAt := memoryConfirmedAt(entry.Content)
	if err := a.markMemoryNotQuite(id); err != nil {
		return 0, err
	}
	return confirmedAt, nil
}

// UndoNotQuiteMemoryEntry takes back Not quite and restores the confirmation
// it had (0 for none).
func (a *App) UndoNotQuiteMemoryEntry(id string, confirmedAt int64) error {
	memoryMutationMu.RLock()
	defer memoryMutationMu.RUnlock()
	unlock := memoryFileLocks.Acquire(id)
	defer unlock()
	entry, err := a.GetMemoryEntry(id)
	if err != nil {
		return err
	}
	if memoryNotQuiteAt(entry.Content) == 0 {
		return nil
	}
	dir, err := memoryDir()
	if err != nil {
		return err
	}
	if err := checkMemoryWriteAllowed(dir, id, entry.Content, false); err != nil {
		return err
	}
	content := clearMemoryNotQuite(entry.Content)
	if confirmedAt > 0 {
		content, err = setMemoryConfirmedAt(content, time.UnixMilli(confirmedAt))
	} else {
		// An envelope that held only the answer goes with it.
		content, err = clearMemoryConfirmation(content)
	}
	if err != nil {
		return err
	}
	if err := writeMemoryFileAtomically(dir, id, content); err != nil {
		return err
	}
	a.markMemoryDirty("not_quite_undo")
	return nil
}

var errMemoryWordingChanged = errors.New("memory_wording_changed")

// UndoMemoryWording is Undo after Not quite with words: it puts back the
// version the save kept in history, while the belief still reads as the
// person worded it.
func (a *App) UndoMemoryWording(id, wording string) (MemoryEntry, error) {
	memoryMutationMu.RLock()
	defer memoryMutationMu.RUnlock()
	unlock := memoryFileLocks.Acquire(id)
	defer unlock()
	entry, err := a.GetMemoryEntry(id)
	if err != nil {
		return MemoryEntry{}, err
	}
	points := parseMemoryPoints(entry.Content)
	if len(points) == 0 || strings.TrimSpace(points[0]) != strings.TrimSpace(wording) {
		return MemoryEntry{}, errMemoryWordingChanged
	}
	versions, err := a.ListMemoryVersions(id)
	if err != nil {
		return MemoryEntry{}, err
	}
	if len(versions) == 0 {
		return MemoryEntry{}, errMemoryWordingChanged
	}
	before, err := a.GetMemoryVersion(id, versions[0].TimestampMs)
	if err != nil {
		return MemoryEntry{}, err
	}
	dir, err := memoryDir()
	if err != nil {
		return MemoryEntry{}, err
	}
	if err := checkMemoryWriteAllowed(dir, id, before, false); err != nil {
		return MemoryEntry{}, err
	}
	if err := snapshotMemoryFile(dir, id); err != nil {
		return MemoryEntry{}, err
	}
	if err := writeMemoryFileAtomically(dir, id, before); err != nil {
		return MemoryEntry{}, err
	}
	a.markMemoryDirty("wording_undo")
	return a.GetMemoryEntry(id)
}
