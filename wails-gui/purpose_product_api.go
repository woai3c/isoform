package main

import (
	"errors"
	"sort"
	"strings"
)

type AgencyPurposeCountsView struct {
	Total        int `json:"total"`
	Current      int `json:"current"`
	Active       int `json:"active"`
	Waiting      int `json:"waiting"`
	Candidate    int `json:"candidate"`
	Resolved     int `json:"resolved"`
	Contradicted int `json:"contradicted"`
	Dismissed    int `json:"dismissed"`
	Superseded   int `json:"superseded"`
}

type AgencyPurposeView struct {
	ID                     string   `json:"id"`
	CanonicalKey           string   `json:"canonicalKey"`
	Kind                   string   `json:"kind"`
	Title                  string   `json:"title"`
	DesiredOutcome         string   `json:"desiredOutcome"`
	Owner                  string   `json:"owner"`
	Counterparty           string   `json:"counterparty,omitempty"`
	Scope                  string   `json:"scope,omitempty"`
	DueKind                string   `json:"dueKind"`
	DueValue               string   `json:"dueValue,omitempty"`
	WaitingCondition       string   `json:"waitingCondition,omitempty"`
	LifecycleState         string   `json:"lifecycleState"`
	SupportState           string   `json:"supportState"`
	ReasoningStatus        string   `json:"reasoningStatus"`
	Rationale              string   `json:"rationale,omitempty"`
	Confidence             float64  `json:"confidence"`
	EvidenceCount          int      `json:"evidenceCount"`
	RelationCount          int      `json:"relationCount"`
	Eligible               bool     `json:"eligible"`
	CurrentEventID         string   `json:"currentEventId"`
	CurrentEventType       string   `json:"currentEventType"`
	AdvancingInitiativeIDs []string `json:"advancingInitiativeIds"`
	CreatedAt              string   `json:"createdAt"`
	UpdatedAt              string   `json:"updatedAt"`
}

type AgencyPurposeSnapshotView struct {
	Current   []AgencyPurposeView     `json:"current"`
	Counts    AgencyPurposeCountsView `json:"counts"`
	UpdatedAt string                  `json:"updatedAt,omitempty"`
}

type AgencyPurposeEvidenceView struct {
	ID               string `json:"id"`
	EventID          string `json:"eventId"`
	SourceKind       string `json:"sourceKind"`
	SourceID         string `json:"sourceId"`
	SourceVersion    string `json:"sourceVersion"`
	SourceObservedAt string `json:"sourceObservedAt"`
	SourceHash       string `json:"sourceHash"`
	EvidenceClass    string `json:"evidenceClass"`
	Stance           string `json:"stance"`
	IndependenceKey  string `json:"independenceKey"`
	ExcerptHash      string `json:"excerptHash,omitempty"`
	PrivacyDecision  string `json:"privacyDecision"`
	CreatedAt        string `json:"createdAt"`
	PayloadHash      string `json:"payloadHash"`
}

type AgencyPurposeEventView struct {
	ID                 string   `json:"id"`
	Sequence           int      `json:"sequence"`
	EventType          string   `json:"eventType"`
	PredecessorEventID string   `json:"predecessorEventId,omitempty"`
	Title              string   `json:"title"`
	DesiredOutcome     string   `json:"desiredOutcome"`
	Owner              string   `json:"owner"`
	Counterparty       string   `json:"counterparty,omitempty"`
	Scope              string   `json:"scope,omitempty"`
	DueKind            string   `json:"dueKind"`
	DueValue           string   `json:"dueValue,omitempty"`
	WaitingCondition   string   `json:"waitingCondition,omitempty"`
	LifecycleState     string   `json:"lifecycleState"`
	SupportState       string   `json:"supportState"`
	ReasoningStatus    string   `json:"reasoningStatus"`
	Rationale          string   `json:"rationale,omitempty"`
	Confidence         float64  `json:"confidence"`
	EvidenceIDs        []string `json:"evidenceIds"`
	EvidenceSetHash    string   `json:"evidenceSetHash"`
	OccurredAt         string   `json:"occurredAt"`
	CreatedAt          string   `json:"createdAt"`
	PayloadHash        string   `json:"payloadHash"`
}

type AgencyPurposeRelationEventView struct {
	ID              string `json:"id"`
	Sequence        int    `json:"sequence"`
	EventType       string `json:"eventType"`
	EvidenceEventID string `json:"evidenceEventId"`
	Reason          string `json:"reason,omitempty"`
	OccurredAt      string `json:"occurredAt"`
	CreatedAt       string `json:"createdAt"`
	PayloadHash     string `json:"payloadHash"`
}

type AgencyPurposeRelationView struct {
	ID               string                           `json:"id"`
	RelationType     string                           `json:"relationType"`
	Direction        string                           `json:"direction"`
	RelatedPurposeID string                           `json:"relatedPurposeId"`
	RelatedTitle     string                           `json:"relatedTitle"`
	RelatedState     string                           `json:"relatedState"`
	Active           bool                             `json:"active"`
	CurrentEventID   string                           `json:"currentEventId"`
	CreatedAt        string                           `json:"createdAt"`
	PayloadHash      string                           `json:"payloadHash"`
	Events           []AgencyPurposeRelationEventView `json:"events"`
}

type AgencyPurposeDetailView struct {
	Purpose   AgencyPurposeView           `json:"purpose"`
	Events    []AgencyPurposeEventView    `json:"events"`
	Evidence  []AgencyPurposeEvidenceView `json:"evidence"`
	Relations []AgencyPurposeRelationView `json:"relations"`
}

func (a *App) purposeProductStore() (*PurposeMapStore, error) {
	if a == nil || a.activity == nil || a.activity.store == nil ||
		a.activity.store.db == nil {
		return nil, errors.New("Purpose storage is unavailable")
	}
	return NewPurposeMapStore(a.activity.store.db), nil
}

func emptyAgencyPurposeSnapshot() AgencyPurposeSnapshotView {
	return AgencyPurposeSnapshotView{Current: []AgencyPurposeView{}}
}

func agencyPurposeView(detail PurposeOpenLoopDetail) AgencyPurposeView {
	current := detail.Current
	return AgencyPurposeView{
		ID: detail.OpenLoop.ID, CanonicalKey: detail.OpenLoop.CanonicalKey,
		Kind: detail.OpenLoop.Kind, Title: current.Title,
		DesiredOutcome: current.DesiredOutcome, Owner: current.Owner,
		Counterparty: current.Counterparty, Scope: current.Scope,
		DueKind: current.DueKind, DueValue: current.DueValue,
		WaitingCondition: current.WaitingCondition,
		LifecycleState:   current.LifecycleState, SupportState: current.SupportState,
		ReasoningStatus: current.ReasoningStatus, Rationale: current.Rationale,
		Confidence: current.Confidence, EvidenceCount: len(detail.Evidence),
		RelationCount: len(detail.Relations), Eligible: detail.Eligible,
		CurrentEventID: current.ID, CurrentEventType: current.EventType,
		AdvancingInitiativeIDs: []string{},
		CreatedAt:              agencyTimestamp(detail.OpenLoop.CreatedAt),
		UpdatedAt:              agencyTimestamp(current.OccurredAt),
	}
}

func agencyPurposeSnapshot(details []PurposeOpenLoopDetail) AgencyPurposeSnapshotView {
	view := emptyAgencyPurposeSnapshot()
	for _, detail := range details {
		view.Counts.Total++
		switch detail.Current.LifecycleState {
		case PurposeStateActive:
			view.Counts.Active++
		case PurposeStateWaiting:
			view.Counts.Waiting++
		case PurposeStateCandidate:
			view.Counts.Candidate++
		case PurposeStateResolved:
			view.Counts.Resolved++
		case PurposeStateContradicted:
			view.Counts.Contradicted++
		case PurposeStateDismissed:
			view.Counts.Dismissed++
		case PurposeStateSuperseded:
			view.Counts.Superseded++
		}
		if !detail.Eligible {
			continue
		}
		item := agencyPurposeView(detail)
		view.Current = append(view.Current, item)
		if item.UpdatedAt > view.UpdatedAt {
			view.UpdatedAt = item.UpdatedAt
		}
	}
	view.Counts.Current = len(view.Current)
	sort.SliceStable(view.Current, func(i, j int) bool {
		if view.Current[i].LifecycleState != view.Current[j].LifecycleState {
			return view.Current[i].LifecycleState == PurposeStateActive
		}
		if view.Current[i].UpdatedAt != view.Current[j].UpdatedAt {
			return view.Current[i].UpdatedAt > view.Current[j].UpdatedAt
		}
		return view.Current[i].ID < view.Current[j].ID
	})
	return view
}

func agencyPurposeDetailView(
	detail PurposeOpenLoopDetail,
	all []PurposeOpenLoopDetail,
) AgencyPurposeDetailView {
	view := AgencyPurposeDetailView{
		Purpose: agencyPurposeView(detail), Events: []AgencyPurposeEventView{},
		Evidence: []AgencyPurposeEvidenceView{}, Relations: []AgencyPurposeRelationView{},
	}
	byID := make(map[string]PurposeOpenLoopDetail, len(all))
	for _, item := range all {
		byID[item.OpenLoop.ID] = item
	}
	for _, event := range detail.Events {
		view.Events = append(view.Events, AgencyPurposeEventView{
			ID: event.ID, Sequence: event.Sequence, EventType: event.EventType,
			PredecessorEventID: event.PredecessorEventID, Title: event.Title,
			DesiredOutcome: event.DesiredOutcome, Owner: event.Owner,
			Counterparty: event.Counterparty, Scope: event.Scope,
			DueKind: event.DueKind, DueValue: event.DueValue,
			WaitingCondition: event.WaitingCondition,
			LifecycleState:   event.LifecycleState, SupportState: event.SupportState,
			ReasoningStatus: event.ReasoningStatus, Rationale: event.Rationale,
			Confidence:      event.Confidence,
			EvidenceIDs:     append([]string(nil), event.EvidenceIDs...),
			EvidenceSetHash: event.EvidenceSetHash,
			OccurredAt:      agencyTimestamp(event.OccurredAt),
			CreatedAt:       agencyTimestamp(event.CreatedAt), PayloadHash: event.PayloadHash,
		})
	}
	for _, evidence := range detail.Evidence {
		view.Evidence = append(view.Evidence, AgencyPurposeEvidenceView{
			ID: evidence.ID, EventID: evidence.EventID,
			SourceKind: evidence.SourceKind, SourceID: evidence.SourceID,
			SourceVersion:    evidence.SourceVersion,
			SourceObservedAt: agencyTimestamp(evidence.SourceObservedAt),
			SourceHash:       evidence.SourceHash, EvidenceClass: evidence.EvidenceClass,
			Stance: evidence.Stance, IndependenceKey: evidence.IndependenceKey,
			ExcerptHash: evidence.ExcerptHash, PrivacyDecision: evidence.PrivacyDecision,
			CreatedAt: agencyTimestamp(evidence.CreatedAt), PayloadHash: evidence.PayloadHash,
		})
	}
	for _, relation := range detail.Relations {
		relatedID := relation.Relation.ObjectID
		direction := "outgoing"
		if relation.Relation.ObjectID == detail.OpenLoop.ID {
			relatedID = relation.Relation.SubjectID
			direction = "incoming"
		}
		related := byID[relatedID]
		item := AgencyPurposeRelationView{
			ID: relation.Relation.ID, RelationType: relation.Relation.RelationType,
			Direction: direction, RelatedPurposeID: relatedID,
			Active:         relation.Current.EventType == PurposeRelationEventAsserted,
			CurrentEventID: relation.Current.ID,
			CreatedAt:      agencyTimestamp(relation.Relation.CreatedAt),
			PayloadHash:    relation.Relation.PayloadHash,
			Events:         []AgencyPurposeRelationEventView{},
		}
		if related.OpenLoop.ID != "" {
			item.RelatedTitle = related.Current.Title
			item.RelatedState = related.Current.LifecycleState
		}
		for _, event := range relation.Events {
			item.Events = append(item.Events, AgencyPurposeRelationEventView{
				ID: event.ID, Sequence: event.Sequence, EventType: event.EventType,
				EvidenceEventID: event.EvidenceEventID, Reason: event.Reason,
				OccurredAt: agencyTimestamp(event.OccurredAt),
				CreatedAt:  agencyTimestamp(event.CreatedAt), PayloadHash: event.PayloadHash,
			})
		}
		view.Relations = append(view.Relations, item)
	}
	sort.SliceStable(view.Relations, func(i, j int) bool {
		if view.Relations[i].Active != view.Relations[j].Active {
			return view.Relations[i].Active
		}
		if view.Relations[i].RelationType != view.Relations[j].RelationType {
			return view.Relations[i].RelationType < view.Relations[j].RelationType
		}
		return view.Relations[i].ID < view.Relations[j].ID
	})
	return view
}

func (a *App) GetPurposeOpenLoopProductDetail(
	openLoopID string,
) (AgencyPurposeDetailView, error) {
	store, err := a.purposeProductStore()
	if err != nil {
		return AgencyPurposeDetailView{}, err
	}
	all, err := store.ListDetailsAll()
	if err != nil {
		return AgencyPurposeDetailView{}, err
	}
	detail, err := findPurposeDetail(all, strings.TrimSpace(openLoopID))
	if err != nil {
		return AgencyPurposeDetailView{}, err
	}
	return agencyPurposeDetailView(detail, all), nil
}
