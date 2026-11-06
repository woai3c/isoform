package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"
)

// About you's saved view: the last beliefs and their projection, kept beside the
// briefs under the privacy stamp it was made under (savedCopyStamp). The page
// paints it on the first open while its reads run. It is served only while the
// stamp still holds, so a forget, a withdrawal or a privacy change drops it at once.

const (
	aboutYouSavedFileName = "about-you-view.json"
	aboutYouSavedVersion  = 1
)

type aboutYouSavedFile struct {
	Version   int           `json:"version"`
	ReadStamp string        `json:"readStamp"`
	SavedAt   time.Time     `json:"savedAt"`
	About     AboutYouView  `json:"about"`
	Entries   []MemoryEntry `json:"entries"`
}

// AboutYouSavedView is the saved beliefs the page paints before its reads.
type AboutYouSavedView struct {
	About   AboutYouView  `json:"about"`
	Entries []MemoryEntry `json:"entries"`
	SavedAt time.Time     `json:"savedAt"`
}

func aboutYouSavedViewPath() string {
	dir, err := mobileBriefStateDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, mobileBriefDirName, aboutYouSavedFileName)
}

// GetSavedAboutYou returns the saved beliefs only while the privacy stamp they
// were made under still holds. A copy under another stamp is deleted, never served.
func (a *App) GetSavedAboutYou() (perfValue *AboutYouSavedView, perfErr error) {
	defer logPageBinding("GetSavedAboutYou", time.Now(), &perfValue)
	path := aboutYouSavedViewPath()
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	stamp := a.savedCopyStamp(context.Background())
	var file aboutYouSavedFile
	if json.Unmarshal(raw, &file) != nil || file.Version != aboutYouSavedVersion || file.ReadStamp == "" || file.ReadStamp != stamp {
		_ = os.Remove(path)
		log.Printf("[about-saved] served=none reason=stamp-changed")
		return nil, nil
	}
	log.Printf("[about-saved] served=copy saved_at=%s", file.SavedAt.Format(time.RFC3339))
	return &AboutYouSavedView{About: file.About, Entries: file.Entries, SavedAt: file.SavedAt}, nil
}

// SaveAboutYouSavedView stores the beliefs the page just read. The stamp must be
// the one taken before those reads; if it has changed, nothing is written.
func (a *App) SaveAboutYouSavedView(stamp string, about AboutYouView, entries []MemoryEntry) error {
	path := aboutYouSavedViewPath()
	if path == "" || stamp == "" {
		return nil
	}
	if stamp != a.savedCopyStamp(context.Background()) {
		log.Printf("[about-saved] saved=refused reason=stamp-changed")
		return errPeopleSavedStampChanged
	}
	raw, err := json.Marshal(aboutYouSavedFile{
		Version:   aboutYouSavedVersion,
		ReadStamp: stamp,
		SavedAt:   time.Now(),
		About:     about,
		Entries:   entries,
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
	log.Printf("[about-saved] saved=ok entries=%d", len(entries))
	return nil
}

// ForgetSavedAboutYou removes the saved beliefs after a write the copy would show as before.
func (a *App) ForgetSavedAboutYou() error {
	path := aboutYouSavedViewPath()
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
