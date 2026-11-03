package main

// The speech model that transcribes a call again once it has ended
// (asr_qwen3.go). Setup confirms its background download; Settings shows what
// it is, how large, where it stands and its last error, and can turn it
// off or remove it.

// AfterCallSpeechModelView is the Settings row's view of the model.
type AfterCallSpeechModelView struct {
	Supported     bool   `json:"supported"`
	Name          string `json:"name"`
	DownloadBytes int64  `json:"downloadBytes"`
	DiskBytes     int64  `json:"diskBytes"`
	Enabled       bool   `json:"enabled"`
	// Status is absent, downloading, ready, low_disk or failed.
	Status     string `json:"status"`
	Downloaded int64  `json:"downloaded"`
	Total      int64  `json:"total"`
	LastError  string `json:"lastError"`
	Folder     string `json:"folder"`
	// Removable: model files, a partial download or extraction leftovers
	// are on disk and nothing is downloading, so Remove can clear them.
	Removable bool `json:"removable"`
}

// GetAfterCallSpeechModel describes the after-call speech model.
func (a *App) GetAfterCallSpeechModel() AfterCallSpeechModelView {
	return a.afterCallModelView()
}

// SetAfterCallSpeechModelEnabled turns the after-call model on or off. Off,
// calls are written up with the live engine, a download in progress stops
// and nothing more is downloaded; the files stay until removed.
func (a *App) SetAfterCallSpeechModelEnabled(enabled bool) error {
	manager, err := a.setupModelManager()
	if err != nil {
		return err
	}
	previous := manager.Selected("afterCall")
	if err := manager.SetAfterCallEnabled(enabled); err != nil {
		return err
	}
	if !enabled {
		manager.Cancel(previous)
	}
	return nil
}

// RemoveAfterCallSpeechModel deletes the downloaded model and turns it off,
// so it does not download again until turned back on.
func (a *App) RemoveAfterCallSpeechModel() error {
	if err := a.SetAfterCallSpeechModelEnabled(false); err != nil {
		return err
	}
	return a.removeAfterCallModel()
}

func (a *App) afterCallSpeechModelEnabled() bool {
	manager, err := a.setupModelManager()
	return err == nil && manager.Selected("afterCall") != "none"
}
