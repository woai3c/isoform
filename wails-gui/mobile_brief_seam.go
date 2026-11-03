package main

import (
	"context"
	"errors"
	"strings"
	"time"
)

// The brief service (mobile_brief_service.go) and the relay transport
// (mobile_relay_client.go) are developed independently. This file is the only
// place where they meet, so each side compiles without the other.

// mobileBriefSource is implemented by the brief service. The transport reads
// briefs through it when a phone asks over an envelope.
type mobileBriefSource interface {
	Start(ctx context.Context)
	Stop()
	LatestMobileBrief() (MobileBriefV1, bool)
	ListMobileBriefs(limit int) []MobileBriefSummary
	GetMobileBrief(briefID string) (MobileBriefV1, bool)
	// RequestMobileBrief starts an on-demand generation. reason is "phone" or
	// "settings". Generation is asynchronous; the finished brief reaches
	// phones through (*App).publishMobileBrief.
	RequestMobileBrief(reason string) (MobileBriefRequestResult, error)
	Schedule() (MobileBriefSchedule, error)
	SetSchedule(schedule MobileBriefSchedule) (MobileBriefSchedule, error)
	// Health reports last run, next run and the missed runs of the last week.
	Health() MobileBriefHealth
	// Audio returns the spoken version of a stored brief, when one exists.
	Audio(briefID string) (MobileBriefAudioData, bool)
}

// mobileBriefPatcher is the optional part of a brief source that changes the
// newest stored brief in place (new finds join its watching section).
type mobileBriefPatcher interface {
	PatchLatestMobileBrief(fn func(MobileBriefV1) (MobileBriefV1, bool), after func(MobileBriefV1)) (MobileBriefV1, bool)
}

// mobileBriefBuilding is the optional part of a brief source that says a
// brief is being built.
type mobileBriefBuilding interface {
	Building() bool
}

// mobileBriefServiceFactory is set by an init() in mobile_brief_service.go.
// Startup calls it after the stores are ready; a nil factory means the brief
// feature is absent from this build.
var mobileBriefServiceFactory func(app *App) mobileBriefSource

var (
	errMobileBriefUnavailable = errors.New("the brief service is not ready")
	errMobileBriefNoAudio     = errors.New("this brief has no spoken version")
)

// mobileBriefSource returns the running brief service or nil.
func (a *App) mobileBriefSource() mobileBriefSource {
	a.mobileBridgeMu.Lock()
	defer a.mobileBridgeMu.Unlock()
	return a.mobileBriefSvc
}

// ensureMobileBriefService gives launch reads and startup the same owner and
// patch lock. Construction starts no workers; startup calls Start after stores
// are ready. Shutdown prevents a late UI read from recreating the service.
func (a *App) ensureMobileBriefService() mobileBriefSource {
	a.mobileBridgeMu.Lock()
	defer a.mobileBridgeMu.Unlock()
	if a.mobileBriefSvc == nil && !a.mobileBridgeStopping && mobileBriefServiceFactory != nil {
		a.mobileBriefSvc = mobileBriefServiceFactory(a)
	}
	return a.mobileBriefSvc
}

// publishMobileBrief delivers a finished brief to every paired phone through
// the relay (an h2d envelope of kind "brief", with a push when notify is set).
// While the relay is disconnected, the newest brief per device waits for the
// next registration, and delivery never fails generation.
func (a *App) publishMobileBrief(brief MobileBriefV1, notify bool) error {
	if brief.NarrativeStatus == "quiet" {
		return nil
	}
	defer a.maybePostFirstReads(context.Background())
	bridge := a.mobileBridgeForUse()
	if bridge == nil {
		return nil
	}
	return bridge.publishBrief(brief, notify)
}

// Wails bindings for the brief schedule. They delegate to the service so the
// frontend contract (PROTOCOL.md section 12) does not depend on wiring order.

func (a *App) GetMobileBriefSchedule() (perfValue MobileBriefSchedule, perfErr error) {
	defer logPageBinding("GetMobileBriefSchedule", time.Now(), &perfValue)
	svc := a.mobileBriefSource()
	if svc == nil {
		return MobileBriefSchedule{}, errMobileBriefUnavailable
	}
	return svc.Schedule()
}

func (a *App) SetMobileBriefSchedule(schedule MobileBriefSchedule) (MobileBriefSchedule, error) {
	svc := a.mobileBriefSource()
	if svc == nil {
		return MobileBriefSchedule{}, errMobileBriefUnavailable
	}
	result, err := svc.SetSchedule(schedule)
	if err == nil {
		a.emitBrowserEvent("mobile:updated")
	}
	return result, err
}

// GetMobileBriefHealth returns last run, next run, missed runs and a failing
// slot for Today and Settings. Before the service exists it reports an empty,
// disabled view instead of failing.
func (a *App) GetMobileBriefHealth() (perfValue MobileBriefHealth) {
	defer logPageBinding("GetMobileBriefHealth", time.Now(), &perfValue)
	svc := a.mobileBriefSource()
	if svc == nil {
		return MobileBriefHealth{}
	}
	health := svc.Health()
	// The current brief is the one play-ahead prepares. Today offers to play
	// only a recording whose words are on the same brief.
	health.Audio, health.CurrentBriefID = nil, ""
	if current, ok := currentMobileBrief(svc); ok {
		health.CurrentBriefID = current.BriefID
		// The words are the delivered narrative: a template's spoken text counts.
		if current.Audio != nil && current.Audio.Bytes > 0 && len(current.Narrative) > 0 {
			health.Audio = current.Audio
		}
	}
	return health
}

// GetMobileBriefAudio returns the spoken brief for Today's play control. An
// empty id means the newest brief.
func (a *App) GetMobileBriefAudio(briefID string) (MobileBriefAudioData, error) {
	svc := a.mobileBriefSource()
	if svc == nil {
		return MobileBriefAudioData{}, errMobileBriefUnavailable
	}
	if strings.TrimSpace(briefID) == "" {
		latest, ok := svc.LatestMobileBrief()
		if !ok {
			return MobileBriefAudioData{}, errMobileBriefNoAudio
		}
		briefID = latest.BriefID
	}
	data, ok := svc.Audio(briefID)
	if !ok {
		return MobileBriefAudioData{}, errMobileBriefNoAudio
	}
	return data, nil
}

func (a *App) GenerateMobileBriefNow() (MobileBriefRequestResult, error) {
	svc := a.mobileBriefSource()
	if svc == nil {
		return MobileBriefRequestResult{}, errMobileBriefUnavailable
	}
	return svc.RequestMobileBrief("settings")
}

// GenerateTodayMissedBrief starts or joins a generation and associates the
// opaque token with its terminal result, without putting brief content in it.
func (a *App) GenerateTodayMissedBrief(token string) (MobileBriefRequestResult, error) {
	svc, ok := a.mobileBriefSource().(interface {
		RequestTodayMobileBrief(string) (MobileBriefRequestResult, error)
	})
	if !ok {
		return MobileBriefRequestResult{}, errMobileBriefUnavailable
	}
	return svc.RequestTodayMobileBrief(token)
}

// Today owns quiet slots. A phone keeps the last posted brief until the next
// substantial brief is ready, including after polling or reconnecting.
func latestPostedMobileBrief(source mobileBriefSource) (MobileBriefV1, bool) {
	latest, ok := source.LatestMobileBrief()
	if !ok || latest.NarrativeStatus != "quiet" {
		return latest, ok
	}
	for _, summary := range source.ListMobileBriefs(mobileBriefStoreListMax) {
		if brief, found := source.GetMobileBrief(summary.BriefID); found && brief.NarrativeStatus != "quiet" {
			return brief, true
		}
	}
	return MobileBriefV1{}, false
}
