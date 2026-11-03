package main

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

const purposeUnderstandingFingerprintVersion = "purpose-understanding-fingerprint-v1"

func purposeExtractionPromptVersionForInput(inputVersion string) (string, bool) {
	switch inputVersion {
	case PurposeExtractionInputVersionV1:
		return PurposeExtractionPromptVersionV1, true
	case PurposeExtractionInputVersionV2:
		return PurposeExtractionPromptVersionV2, true
	case PurposeExtractionInputVersion:
		return PurposeExtractionPromptVersion, true
	default:
		return "", false
	}
}

func purposeExtractionVersionsValid(
	inputVersion string,
	manifestVersion string,
	promptVersion string,
) bool {
	expectedPrompt, ok := purposeExtractionPromptVersionForInput(inputVersion)
	if !ok || promptVersion != expectedPrompt {
		return false
	}
	switch inputVersion {
	case PurposeExtractionInputVersionV1:
		return manifestVersion == PurposeExtractionManifestVersionV1
	case PurposeExtractionInputVersionV2:
		return manifestVersion == PurposeExtractionManifestVersionV2
	case PurposeExtractionInputVersion:
		return manifestVersion == PurposeExtractionManifestVersion
	default:
		return false
	}
}

func purposeExtractionCurrentPurposeAt(
	store *PurposeMapStore,
	cutoffAt int64,
) ([]PurposeExtractionCurrentPurpose, error) {
	if store == nil || store.db == nil {
		return nil, errors.New("Purpose extraction current context requires Purpose storage")
	}
	details, err := store.ListDetailsAt(cutoffAt)
	if err != nil {
		return nil, err
	}
	items := make([]PurposeExtractionCurrentPurpose, 0, len(details))
	for _, detail := range details {
		ref, err := purposeTaskContextReference(detail)
		if err != nil {
			return nil, err
		}
		parsed, ok := parsePurposeTaskContextReference(ref)
		if !ok {
			return nil, fmt.Errorf(
				"Purpose root %q produced a malformed context reference",
				detail.OpenLoop.ID,
			)
		}
		duplicateOfIDs := []string{}
		duplicateOfID, err := purposeActiveDuplicateTarget(detail)
		if err != nil {
			return nil, err
		}
		if duplicateOfID != "" {
			duplicateOfIDs = append(duplicateOfIDs, duplicateOfID)
		}
		items = append(items, PurposeExtractionCurrentPurpose{
			Ref: ref, OpenLoopID: detail.OpenLoop.ID,
			CurrentEventID:   parsed.CurrentEventID,
			ProjectionHash:   parsed.ProjectionHash,
			CanonicalKey:     detail.OpenLoop.CanonicalKey,
			Kind:             detail.OpenLoop.Kind,
			Title:            detail.Current.Title,
			DesiredOutcome:   detail.Current.DesiredOutcome,
			Owner:            detail.Current.Owner,
			Counterparty:     detail.Current.Counterparty,
			Scope:            detail.Current.Scope,
			DueKind:          detail.Current.DueKind,
			DueValue:         detail.Current.DueValue,
			WaitingCondition: detail.Current.WaitingCondition,
			ReasoningStatus:  detail.Current.ReasoningStatus,
			LifecycleState:   detail.Current.LifecycleState,
			SupportState:     detail.Current.SupportState,
			Eligible:         detail.Eligible,
			DuplicateOfIDs:   duplicateOfIDs,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].CanonicalKey != items[j].CanonicalKey {
			return items[i].CanonicalKey < items[j].CanonicalKey
		}
		return items[i].OpenLoopID < items[j].OpenLoopID
	})
	return items, nil
}

func attachPurposeExtractionCurrentPurpose(
	store *PurposeMapStore,
	input PurposeExtractionInput,
	manifest PurposeExtractionManifest,
) (PurposeExtractionInput, PurposeExtractionManifest, error) {
	if input.Version != PurposeExtractionInputVersion ||
		manifest.Version != PurposeExtractionManifestVersion {
		return PurposeExtractionInput{}, PurposeExtractionManifest{}, errors.New(
			"current Purpose context requires the current extraction version",
		)
	}
	items, err := purposeExtractionCurrentPurposeAt(store, input.CutoffAt)
	if err != nil {
		return PurposeExtractionInput{}, PurposeExtractionManifest{}, err
	}
	encoded, hash, err := agencyStructPayload(items)
	if err != nil {
		return PurposeExtractionInput{}, PurposeExtractionManifest{}, err
	}
	if len(encoded) > purposeExtractionMaxPurposeCharacters {
		return PurposeExtractionInput{}, PurposeExtractionManifest{}, fmt.Errorf(
			"current Purpose context uses %d characters, exceeding the %d-character complete-context budget",
			len(encoded),
			purposeExtractionMaxPurposeCharacters,
		)
	}
	input.CurrentPurpose = items
	manifest.CurrentPurposeBudgetChars = purposeExtractionMaxPurposeCharacters
	manifest.CurrentPurposeUsedChars = len(encoded)
	manifest.CurrentPurposeCount = len(items)
	manifest.CurrentPurposeHash = hash
	return input, manifest, nil
}

func validPurposeExtractionCurrentKind(value string) bool {
	switch value {
	case PurposeKindCommitment, PurposeKindResponsibility, PurposeKindGoal,
		PurposeKindConcern, PurposeKindOpportunity:
		return true
	default:
		return false
	}
}

func validPurposeExtractionCurrentLifecycle(value string) bool {
	switch value {
	case PurposeStateCandidate, PurposeStateActive, PurposeStateWaiting,
		PurposeStateResolved, PurposeStateContradicted, PurposeStateDismissed,
		PurposeStateSuperseded:
		return true
	default:
		return false
	}
}

func validPurposeExtractionCurrentSupport(value string) bool {
	switch value {
	case PurposeSupportHypothesis, PurposeSupportSupported,
		PurposeSupportContradicted, PurposeSupportUnknown:
		return true
	default:
		return false
	}
}

func validatePurposeExtractionCurrentPurposeProjection(
	items []PurposeExtractionCurrentPurpose,
	manifest PurposeExtractionManifest,
) error {
	if items == nil ||
		manifest.CurrentPurposeBudgetChars != purposeExtractionMaxPurposeCharacters ||
		manifest.CurrentPurposeCount != len(items) {
		return errors.New("Purpose extraction current-Purpose summary is inconsistent")
	}
	encoded, hash, err := agencyStructPayload(items)
	if err != nil {
		return err
	}
	if len(encoded) > manifest.CurrentPurposeBudgetChars ||
		manifest.CurrentPurposeUsedChars != len(encoded) ||
		manifest.CurrentPurposeHash != hash {
		return errors.New("Purpose extraction current-Purpose hash or budget is inconsistent")
	}
	seen := make(map[string]struct{}, len(items))
	for index, item := range items {
		parsed, ok := parsePurposeTaskContextReference(item.Ref)
		if !ok || parsed.OpenLoopID != item.OpenLoopID ||
			parsed.CurrentEventID != item.CurrentEventID ||
			parsed.ProjectionHash != item.ProjectionHash {
			return fmt.Errorf("Purpose extraction current-Purpose item %d has an invalid reference", index)
		}
		if _, duplicate := seen[item.OpenLoopID]; duplicate {
			return fmt.Errorf("Purpose extraction current-Purpose root %q is duplicated", item.OpenLoopID)
		}
		seen[item.OpenLoopID] = struct{}{}
		canonical, err := normalizePurposeCanonicalKey(item.CanonicalKey)
		if err != nil || canonical != item.CanonicalKey ||
			!validPurposeExtractionCurrentKind(item.Kind) ||
			!validPurposeExtractionCurrentLifecycle(item.LifecycleState) ||
			!validPurposeExtractionCurrentSupport(item.SupportState) {
			return fmt.Errorf("Purpose extraction current-Purpose item %d has invalid canonical state", index)
		}
		if _, err := normalizePurposeText(item.Title, "current Purpose title", purposeMaxTitleBytes, true); err != nil {
			return err
		}
		if _, err := normalizePurposeText(item.DesiredOutcome, "current Purpose desired outcome", purposeMaxOutcomeBytes, true); err != nil {
			return err
		}
		owner, err := normalizePurposeOwner(item.Owner)
		if err != nil || owner != item.Owner {
			return fmt.Errorf("Purpose extraction current-Purpose item %d has an invalid owner", index)
		}
		dueKind, dueValue, err := normalizePurposeDue(item.DueKind, item.DueValue)
		if err != nil || dueKind != item.DueKind || dueValue != item.DueValue {
			return fmt.Errorf("Purpose extraction current-Purpose item %d has an invalid due signal", index)
		}
		for _, optional := range []struct {
			field string
			value string
		}{
			{field: "counterparty", value: item.Counterparty},
			{field: "scope", value: item.Scope},
			{field: "waiting condition", value: item.WaitingCondition},
		} {
			normalized, err := normalizePurposeText(
				optional.value, "current Purpose "+optional.field, purposeMaxFieldBytes, false,
			)
			if err != nil || normalized != optional.value {
				return fmt.Errorf(
					"Purpose extraction current-Purpose item %d has an invalid %s",
					index, optional.field,
				)
			}
		}
		if item.ReasoningStatus != PurposeReasoningExplicit &&
			item.ReasoningStatus != PurposeReasoningUnknown {
			return fmt.Errorf("Purpose extraction current-Purpose item %d has invalid reasoning status", index)
		}
		if len(item.DuplicateOfIDs) > 1 ||
			(len(item.DuplicateOfIDs) == 1 &&
				(strings.TrimSpace(item.DuplicateOfIDs[0]) == "" ||
					item.DuplicateOfIDs[0] == item.OpenLoopID || item.Eligible)) {
			return fmt.Errorf("Purpose extraction current-Purpose item %d has invalid duplicate state", index)
		}
		if index > 0 {
			previous := items[index-1]
			if previous.CanonicalKey > item.CanonicalKey ||
				(previous.CanonicalKey == item.CanonicalKey && previous.OpenLoopID >= item.OpenLoopID) {
				return errors.New("Purpose extraction current-Purpose items are not canonically ordered")
			}
		}
	}
	return nil
}

type purposeUnderstandingFingerprintSource struct {
	Ref               string `json:"ref"`
	SourceKind        string `json:"sourceKind"`
	SourceID          string `json:"sourceId"`
	SourceVersion     string `json:"sourceVersion"`
	SourceObservedAt  int64  `json:"sourceObservedAt"`
	SourceHash        string `json:"sourceHash"`
	PromptContentHash string `json:"promptContentHash"`
	EvidenceClass     string `json:"evidenceClass"`
	PrivacyDecision   string `json:"privacyDecision"`
	IndependenceKey   string `json:"independenceKey"`
	Truncated         bool   `json:"truncated"`
}

type purposeUnderstandingFingerprintPayload struct {
	Version            string                                  `json:"version"`
	InputVersion       string                                  `json:"inputVersion"`
	ManifestVersion    string                                  `json:"manifestVersion"`
	PromptVersion      string                                  `json:"promptVersion"`
	OutputVersion      string                                  `json:"outputVersion"`
	Model              string                                  `json:"model"`
	Query              string                                  `json:"query,omitempty"`
	Sources            []purposeUnderstandingFingerprintSource `json:"sources"`
	ManifestEntries    []PurposeExtractionManifestEntry        `json:"manifestEntries"`
	CurrentPurposeHash string                                  `json:"currentPurposeHash"`
	CurrentPurposeRefs []string                                `json:"currentPurposeRefs"`
	MailPrivacy        *PurposeExtractionMailPrivacy           `json:"mailPrivacy,omitempty"`
}

func purposeUnderstandingEvidenceFingerprint(
	input PurposeExtractionInput,
	manifest PurposeExtractionManifest,
	model string,
) (string, error) {
	if err := validatePurposeExtractionInputManifest(input, manifest); err != nil {
		return "", err
	}
	promptVersion, ok := purposeExtractionPromptVersionForInput(input.Version)
	if !ok {
		return "", fmt.Errorf("unsupported Purpose extraction input version %q", input.Version)
	}
	payload := purposeUnderstandingFingerprintPayload{
		Version:      purposeUnderstandingFingerprintVersion,
		InputVersion: input.Version, ManifestVersion: manifest.Version,
		PromptVersion: promptVersion, OutputVersion: PurposeExtractionOutputVersion,
		Model: strings.TrimSpace(model), Query: input.Query,
		MailPrivacy:        input.MailPrivacy,
		Sources:            []purposeUnderstandingFingerprintSource{},
		ManifestEntries:    append([]PurposeExtractionManifestEntry(nil), manifest.Entries...),
		CurrentPurposeHash: manifest.CurrentPurposeHash,
		CurrentPurposeRefs: []string{},
	}
	for _, source := range input.Sources {
		payload.Sources = append(payload.Sources, purposeUnderstandingFingerprintSource{
			Ref: source.Ref, SourceKind: source.SourceKind, SourceID: source.SourceID,
			SourceVersion:    source.SourceVersion,
			SourceObservedAt: source.SourceObservedAt, SourceHash: source.SourceHash,
			PromptContentHash: source.PromptContentHash,
			EvidenceClass:     source.EvidenceClass, PrivacyDecision: source.PrivacyDecision,
			IndependenceKey: source.IndependenceKey, Truncated: source.Truncated,
		})
	}
	for _, item := range input.CurrentPurpose {
		payload.CurrentPurposeRefs = append(payload.CurrentPurposeRefs, item.Ref)
	}
	_, hash, err := agencyStructPayload(payload)
	return hash, err
}

func purposeUnderstandingIdempotencyKey(fingerprint string) (string, error) {
	if _, err := normalizePurposeHash(fingerprint, "Purpose understanding fingerprint", true); err != nil {
		return "", err
	}
	return normalizePurposeID(
		"purpose-understanding:"+fingerprint,
		"Purpose understanding idempotency key",
	)
}
