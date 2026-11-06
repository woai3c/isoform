package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"time"
)

// Who you know's saved view: the last directory read, kept beside the briefs
// under the privacy stamp it was made under (savedCopyStamp). The page paints
// it on the first open, before its reads finish. It is served only while that
// stamp still holds, so a withdrawal, a Closed Eyes change or a sensitive-app
// change drops it at once. Message previews are never written.

const (
	peopleSavedViewFileName = "people-view.json"
	peopleSavedViewVersion  = 1
)

var errPeopleSavedStampChanged = errors.New("people saved view: the privacy stamp changed since the read began")

type peopleSavedViewFile struct {
	Version   int                     `json:"version"`
	ReadStamp string                  `json:"readStamp"`
	SavedAt   time.Time               `json:"savedAt"`
	Overview  PeopleOverview          `json:"overview"`
	Entries   []PrincipalWithStatsDTO `json:"entries"`
}

// PeopleSavedView is the saved directory the page paints before its reads.
type PeopleSavedView struct {
	Overview PeopleOverview          `json:"overview"`
	Entries  []PrincipalWithStatsDTO `json:"entries"`
	SavedAt  time.Time               `json:"savedAt"`
}

func peopleSavedViewPath() string {
	dir, err := mobileBriefStateDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, mobileBriefDirName, peopleSavedViewFileName)
}

// GetSavedCopyStamp is the privacy stamp a saved copy is made under. It reads
// memory only. The page takes it before its reads and gives it back on save.
func (a *App) GetSavedCopyStamp() string {
	return a.savedCopyStamp(context.Background())
}

// GetSavedPeopleView returns the saved directory only while the privacy stamp it
// was made under still holds. A copy under another stamp is deleted, never served.
func (a *App) GetSavedPeopleView() (perfValue *PeopleSavedView, perfErr error) {
	defer logPageBinding("GetSavedPeopleView", time.Now(), &perfValue)
	path := peopleSavedViewPath()
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	stamp := a.savedCopyStamp(context.Background())
	var file peopleSavedViewFile
	if json.Unmarshal(raw, &file) != nil || file.Version != peopleSavedViewVersion || file.ReadStamp == "" || file.ReadStamp != stamp {
		_ = os.Remove(path)
		log.Printf("[people-saved] served=none reason=stamp-changed")
		return nil, nil
	}
	log.Printf("[people-saved] served=copy saved_at=%s", file.SavedAt.Format(time.RFC3339))
	return &PeopleSavedView{Overview: file.Overview, Entries: file.Entries, SavedAt: file.SavedAt}, nil
}

// SavePeopleSavedView stores the directory the page just read. The stamp must be
// the one taken before those reads; if it has changed, nothing is written.
func (a *App) SavePeopleSavedView(stamp string, overview PeopleOverview, entries []PrincipalWithStatsDTO) error {
	path := peopleSavedViewPath()
	if path == "" || stamp == "" {
		return nil
	}
	if stamp != a.savedCopyStamp(context.Background()) {
		log.Printf("[people-saved] saved=refused reason=stamp-changed")
		return errPeopleSavedStampChanged
	}
	raw, err := json.Marshal(peopleSavedViewFile{
		Version:   peopleSavedViewVersion,
		ReadStamp: stamp,
		SavedAt:   time.Now(),
		Overview:  overview,
		Entries:   withoutPeopleMessageText(entries),
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	log.Printf("[people-saved] saved=ok entries=%d", len(entries))
	return nil
}

// ForgetSavedPeopleView removes the saved directory after a write the copy would
// show as before the change.
func (a *App) ForgetSavedPeopleView() error {
	path := peopleSavedViewPath()
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// withoutPeopleMessageText copies the directory with every recent-message
// preview removed; the saved copy never holds message text.
func withoutPeopleMessageText(entries []PrincipalWithStatsDTO) []PrincipalWithStatsDTO {
	out := make([]PrincipalWithStatsDTO, len(entries))
	for i, entry := range entries {
		entry.Stats.RecentMessages = []PrincipalRecentMessageDTO{}
		out[i] = entry
	}
	return out
}
