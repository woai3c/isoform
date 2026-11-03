package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"wails-gui/internal/engagement"
	"wails-gui/internal/handoff"
)

// desktopTasks has the same turn lifecycle as the phone bridge, without
// enabling or pairing the phone connection.
func (a *App) desktopTasks() (*mobileBridge, error) {
	a.mobileBridgeMu.Lock()
	defer a.mobileBridgeMu.Unlock()
	if a.mobileBridgeStopping {
		return nil, errors.New("the app is closing")
	}
	if a.desktopTaskBridge == nil {
		ctx, cancel := context.WithCancel(context.Background())
		bridge := &mobileBridge{app: a, ctx: ctx, cancel: cancel, devices: make(map[string]*mobileDeviceRecord), workers: make(map[string]chan mobileEnvelope)}
		bridge.registerChatListener()
		a.desktopTaskBridge = bridge
	}
	return a.desktopTaskBridge, nil
}

func desktopTaskResponse[T any](run func(http.ResponseWriter)) (T, error) {
	var value T
	response := &mobileCapturedResponse{header: make(http.Header)}
	run(response)
	var envelope struct {
		OK    bool            `json:"ok"`
		Data  json.RawMessage `json:"data"`
		Error string          `json:"error"`
	}
	if err := json.Unmarshal(response.body.Bytes(), &envelope); err != nil {
		return value, err
	}
	if !envelope.OK {
		return value, errors.New(envelope.Error)
	}
	if err := json.Unmarshal(envelope.Data, &value); err != nil {
		return value, err
	}
	return value, nil
}

func (a *App) GetDesktopTaskThread(id string) (DesktopTaskDetail, error) {
	bridge, err := a.desktopTasks()
	if err != nil {
		return DesktopTaskDetail{}, err
	}
	detail, err := desktopTaskResponse[MobileTaskDetail](func(w http.ResponseWriter) { bridge.serveTaskDetail(w, id) })
	return a.desktopTaskDetail(id, detail, err)
}

func (a *App) ListDesktopTaskThreads() (MobileTaskPage, error) {
	bridge, err := a.desktopTasks()
	if err != nil {
		return MobileTaskPage{}, err
	}
	return desktopTaskResponse[MobileTaskPage](bridge.serveTaskList)
}

type DesktopTaskActionInput struct {
	Action   string `json:"action"`
	Title    string `json:"title"`
	EventID  string `json:"eventId"`
	ChoiceID string `json:"choiceId"`
	Text     string `json:"text"`
}

func (a *App) ActOnDesktopTask(id string, input DesktopTaskActionInput) (DesktopTaskDetail, error) {
	if !mobileValidID(id) {
		return DesktopTaskDetail{}, errors.New("invalid task id")
	}
	bridge, err := a.desktopTasks()
	if err != nil {
		return DesktopTaskDetail{}, err
	}
	device := mobileDeviceRecord{MobileDeviceView: MobileDeviceView{Scopes: []string{mobileScopeChatWrite}}}
	action := taskActionInput{Action: input.Action, Title: input.Title, EventID: input.EventID, ChoiceID: input.ChoiceID, Text: input.Text}
	detail, err := desktopTaskResponse[MobileTaskDetail](func(w http.ResponseWriter) {
		bridge.performTaskAction(w, device, id, action, engagement.ChannelDesktop)
	})
	return a.desktopTaskDetail(id, detail, err)
}

func (a *App) ReplyToDesktopTask(id, text string) (DesktopTaskDetail, error) {
	text = strings.TrimSpace(text)
	if !mobileValidID(id) || text == "" || len(text) > mobileTaskReplyMax {
		return DesktopTaskDetail{}, errors.New("invalid task reply")
	}
	bridge, err := a.desktopTasks()
	if err != nil {
		return DesktopTaskDetail{}, err
	}
	detail, err := desktopTaskResponse[MobileTaskDetail](func(w http.ResponseWriter) { bridge.performTaskMessage(w, id, text) })
	return a.desktopTaskDetail(id, detail, err)
}

// DesktopDraftTaskID maps the card's moment to the same task ID as the phone.
func (a *App) DesktopDraftTaskID(momentID string) (string, error) {
	if !mobileValidID(momentID) {
		return "", errors.New("invalid moment id")
	}
	id := mobileDraftTaskID(momentID)
	_, found, err := a.mobileDraftTask(context.Background(), id)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("draft unavailable")
	}
	return id, nil
}

// StartDesktopTaskOffer rechecks the same offer projection and starts work
// through the phone's handoff start operation, preserving source admission.
func (a *App) StartDesktopTaskOffer(id, version string) (HandoffAcceptResult, error) {
	if !mobileOfferReferencesValid([]mobileOfferReference{{ID: id, Version: version}}) {
		return HandoffAcceptResult{}, errTaskOfferUnavailable
	}
	a.desktopOfferStartMu.Lock()
	defer a.desktopOfferStartMu.Unlock()
	key := id + "\x00" + version
	if previous, ok := a.desktopStartedOffers[key]; ok {
		return previous, nil
	}
	bridge, err := a.desktopTasks()
	if err != nil {
		return HandoffAcceptResult{}, err
	}
	view, err := a.mobileOffers(bridge.ctx)
	if err != nil {
		return HandoffAcceptResult{}, err
	}
	for _, offer := range view.Offers {
		if offer.ID != id || offer.Version != version {
			continue
		}
		refs := []mobileOfferReference{{ID: id, Version: version}}
		prompt, _, err := a.mobileOfferChatAdmission(bridge.ctx, offer.Prompt, refs)
		if err != nil {
			return HandoffAcceptResult{}, err
		}
		input, err := validateCreateTask(CreateTaskInput{Title: offer.Title, Brief: prompt})
		if err != nil {
			return HandoffAcceptResult{}, err
		}
		saved, err := a.createTaskEntryFrom(input, handoff.SourceDesktop, &handoff.Origin{Kind: handoff.OriginUpNext})
		if err != nil {
			return HandoffAcceptResult{}, err
		}
		result, err := bridge.startHandoffWithOffers(saved.ID, refs)
		if err != nil {
			removed, cleanupErr := a.handoffStore.DeleteIfUnchanged(saved)
			if cleanupErr != nil {
				return HandoffAcceptResult{}, fmt.Errorf("%w; could not remove unstarted task: %v", err, cleanupErr)
			}
			if removed {
				a.emitBrowserEvent("handoff:updated", map[string]any{"id": saved.ID})
			}
			return HandoffAcceptResult{}, err
		}
		if a.desktopStartedOffers == nil {
			a.desktopStartedOffers = make(map[string]HandoffAcceptResult)
		}
		// Do not retain or return the source-derived prompt in the replay map.
		result = HandoffAcceptResult{HandoffID: result.HandoffID, SessionID: result.SessionID}
		a.desktopStartedOffers[key] = result
		return result, nil
	}
	return HandoffAcceptResult{}, errTaskOfferUnavailable
}

// StartHandoffAgain runs a stopped, blocked or review task fresh in its own
// thread, with its brief, on the same start path as the phone. Wails binding.
func (a *App) StartHandoffAgain(id string) (HandoffAcceptResult, error) {
	bridge, err := a.desktopTasks()
	if err != nil {
		return HandoffAcceptResult{}, err
	}
	result, err := bridge.startHandoff(id)
	if err != nil {
		return HandoffAcceptResult{}, err
	}
	return HandoffAcceptResult{HandoffID: result.HandoffID, SessionID: result.SessionID}, nil
}
