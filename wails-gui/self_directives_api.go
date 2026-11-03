package main

import (
	"errors"
	"time"
)

type RetractSelfDirectiveRequest struct {
	DirectiveID         string `json:"directiveId"`
	ExpectedSelfEntryID string `json:"expectedSelfEntryId"`
	IdempotencyKey      string `json:"idempotencyKey"`
	Reason              string `json:"reason"`
}

func (a *App) selfDirectiveStore() (*SelfDirectiveStore, error) {
	if a == nil || a.activity == nil || a.activity.store == nil ||
		a.activity.store.db == nil {
		return nil, errors.New("Self directive storage is unavailable")
	}
	return NewSelfDirectiveStore(a.activity.store.db), nil
}

func (a *App) GetSelfDirectiveDetail(
	directiveID string,
) (SelfDirectiveDetail, error) {
	store, err := a.selfDirectiveStore()
	if err != nil {
		return SelfDirectiveDetail{}, err
	}
	return store.GetDetail(directiveID)
}

func (a *App) RetractSelfDirective(
	input RetractSelfDirectiveRequest,
) (SelfDirectiveMutationResult, error) {
	store, err := a.selfDirectiveStore()
	if err != nil {
		return SelfDirectiveMutationResult{}, err
	}
	result, added, err := store.Retract(RetractSelfDirectiveInput{
		DirectiveID:         input.DirectiveID,
		ExpectedSelfEntryID: input.ExpectedSelfEntryID,
		IdempotencyKey:      input.IdempotencyKey,
		Reason:              input.Reason,
		OccurredAt:          time.Now().UnixMilli(),
	})
	if err != nil {
		return SelfDirectiveMutationResult{}, err
	}
	if added {
		a.emitBrowserEvent("self_directive:updated", result.Directive.ID)
		a.emitBrowserEvent("self_model:updated", result.SelfEntry.ID)
		a.emitBrowserEvent("agency:updated")
	}
	return result, nil
}
