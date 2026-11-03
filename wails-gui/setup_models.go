package main

import (
	"fmt"
	"os"
	"path/filepath"

	"wails-gui/models"
)

func (a *App) setupModelManager() (*models.Manager, error) {
	a.setupModelsOnce.Do(func() {
		dir := configDirOrFallback()
		folder := filepath.Join(dir, "models")
		a.setupModels, a.setupModelsErr = models.New(folder, dir, models.Catalog(), func(path string) (int64, error) {
			// Stat the nearest existing parent; planning does not create folders.
			for {
				if _, err := os.Stat(path); err == nil {
					break
				}
				parent := filepath.Dir(path)
				if parent == path {
					return 0, fmt.Errorf("no disk for %s", path)
				}
				path = parent
			}
			n, err := freeDiskBytes(path)
			return int64(n), err
		}, func(progress []models.Download) { a.emitBrowserEvent("models:downloads", progress) })
		if a.setupModelsErr == nil && !a.setupModels.HasSavedChoices() && loadConfig().AfterCallSpeechModelOff {
			a.setupModelsErr = a.setupModels.SetAfterCallEnabled(false)
		}
	})
	return a.setupModels, a.setupModelsErr
}
func (a *App) GetModelChoices() (models.Choices, error) {
	m, err := a.setupModelManager()
	if err != nil {
		return models.Choices{}, err
	}
	return m.GetChoices()
}
func (a *App) SetModelChoice(job, id string) error {
	m, err := a.setupModelManager()
	if err != nil {
		return err
	}
	return m.SetChoice(job, id)
}
func (a *App) PlanModelDownload(choices map[string]string, sourceOverrides map[string]string) (models.Plan, error) {
	m, err := a.setupModelManager()
	if err != nil {
		return models.Plan{}, err
	}
	return m.BuildPlan(choices, sourceOverrides)
}
func (a *App) StartModelDownloads(plan models.Plan) error {
	m, err := a.setupModelManager()
	if err != nil {
		return err
	}
	m.MonitorNetwork()
	return m.Start(plan)
}
func (a *App) GetModelDownloads() ([]models.Download, error) {
	m, err := a.setupModelManager()
	if err != nil {
		return nil, err
	}
	return m.Downloads(), nil
}
func (a *App) RetryModelDownload(id string) error {
	m, err := a.setupModelManager()
	if err != nil {
		return err
	}
	m.MonitorNetwork()
	return m.Retry(id)
}
func (a *App) RemoveModel(id string) error {
	m, err := a.setupModelManager()
	if err != nil {
		return err
	}
	return m.Remove(id)
}
func (a *App) GetModelEvaluation() string { return models.Evaluation() }

// ResumeModelDownload rechecks the current path and shared disk reservation;
// it never overrides a metered connection or grants a new model download.
func (a *App) ResumeModelDownload(id string) error {
	m, err := a.setupModelManager()
	if err != nil {
		return err
	}
	m.MonitorNetwork()
	return m.ResumeDownload(id)
}

func (a *App) GetModelNetworkState() (models.NetworkState, error) {
	m, err := a.setupModelManager()
	if err != nil {
		return models.NetworkState{}, err
	}
	return m.NetworkSnapshot(), nil
}
