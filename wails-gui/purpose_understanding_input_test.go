package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestPurposeExtractionV2FreezesCompleteCutoffSafeCurrentPurpose(t *testing.T) {
	activity := newTestStore(t)
	store := NewPurposeMapStore(activity.db)
	base := time.Now().Add(-2 * time.Minute).UnixMilli()
	const rootCount = 35
	var first PurposeOpenLoopDetail
	for index := 0; index < rootCount; index++ {
		label := "background purpose " + purposeTestIndex(index)
		result, added, err := store.Create(purposeTestCreateInput(
			"purpose-understanding-create-"+purposeTestIndex(index),
			PurposeKindGoal,
			label,
			[]PurposeEvidenceInput{purposeTestEvidence(
				"purpose-understanding-"+purposeTestIndex(index),
				PurposeEvidenceExplicitUser,
				PurposeStanceSupport,
				base+int64(index),
			)},
			base+int64(index),
		))
		if err != nil || !added {
			t.Fatalf("create Purpose root %d: added=%v err=%v", index, added, err)
		}
		if index == 0 {
			first, err = store.GetDetail(result.OpenLoop.ID)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	cutoff := time.Now().UnixMilli()
	input, manifest, err := gatherPurposeExtractionInput(
		activity,
		PurposeExtractionRequest{
			IdempotencyKey: "purpose-understanding-cutoff-one",
			CutoffAt:       cutoff,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if input.Version != PurposeExtractionInputVersion ||
		manifest.Version != PurposeExtractionManifestVersion ||
		len(input.CurrentPurpose) != rootCount ||
		manifest.CurrentPurposeCount != rootCount ||
		manifest.CurrentPurposeHash == "" ||
		manifest.CurrentPurposeUsedChars <= 0 {
		t.Fatalf("unexpected extraction v2 context: input=%+v manifest=%+v", input, manifest)
	}
	if err := validatePurposeExtractionInputManifest(input, manifest); err != nil {
		t.Fatal(err)
	}
	firstRef, err := purposeTaskContextReference(first)
	if err != nil {
		t.Fatal(err)
	}
	matchedFirst := false
	for _, item := range input.CurrentPurpose {
		if item.OpenLoopID != first.OpenLoop.ID {
			continue
		}
		matchedFirst = item.Ref == firstRef &&
			item.CurrentEventID == first.Current.ID &&
			item.Title == first.Current.Title
	}
	if !matchedFirst {
		t.Fatalf("cutoff context did not retain the first immutable projection")
	}
	prompt, err := buildPurposeExtractionPrompt(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "currentPurpose") ||
		!strings.Contains(prompt, "not evidence") {
		t.Fatalf("v2 prompt does not preserve the context/evidence boundary: %s", prompt)
	}
	malicious := purposeExtractionHypothesis(
		"current-purpose-is-not-evidence",
		first.OpenLoop.CanonicalKey,
		PurposeKindGoal,
		PurposeOperationSupport,
		first.Current.Title,
		first.Current.DesiredOutcome,
		firstRef,
	)
	raw := purposeExtractionOutputJSON(t, malicious)
	if _, err := decodePurposeExtractionOutput(raw, input); err == nil ||
		!strings.Contains(err.Error(), "unavailable evidence") {
		t.Fatalf("current Purpose ref was accepted as evidence: %v", err)
	}

	time.Sleep(2 * time.Millisecond)
	revisionInput := purposeTestAppendInput(
		first,
		"purpose-understanding-revise-after-cutoff",
		PurposeEventRevised,
		PurposeStanceUpdate,
		PurposeEvidenceExplicitUser,
		"purpose-understanding-revision",
		cutoff,
	)
	revisionInput.Title = "A later title must not leak into the old cutoff"
	if _, added, err := store.AppendEvent(revisionInput); err != nil || !added {
		t.Fatalf("append post-cutoff revision: added=%v err=%v", added, err)
	}
	oldInput, _, err := gatherPurposeExtractionInput(
		activity,
		PurposeExtractionRequest{
			IdempotencyKey: "purpose-understanding-cutoff-two",
			CutoffAt:       cutoff,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range oldInput.CurrentPurpose {
		if item.OpenLoopID == first.OpenLoop.ID && item.Title != first.Current.Title {
			t.Fatalf("post-cutoff revision leaked into frozen context: %+v", item)
		}
	}
}

func TestPurposeUnderstandingFingerprintIgnoresClockButTracksEvidence(t *testing.T) {
	activity := newTestStore(t)
	store := NewPurposeMapStore(activity.db)
	base := time.Now().Add(-time.Minute).UnixMilli()
	_, added, err := store.Create(purposeTestCreateInput(
		"purpose-understanding-fingerprint-root",
		PurposeKindResponsibility,
		"fingerprint source",
		[]PurposeEvidenceInput{purposeTestEvidence(
			"purpose-understanding-fingerprint",
			PurposeEvidenceExplicitUser,
			PurposeStanceSupport,
			base,
		)},
		base,
	))
	if err != nil || !added {
		t.Fatalf("create Purpose fingerprint root: added=%v err=%v", added, err)
	}
	gather := func(key string, cutoff int64) (PurposeExtractionInput, PurposeExtractionManifest) {
		t.Helper()
		input, manifest, err := gatherPurposeExtractionInput(
			activity,
			PurposeExtractionRequest{IdempotencyKey: key, CutoffAt: cutoff},
		)
		if err != nil {
			t.Fatal(err)
		}
		return input, manifest
	}
	oneInput, oneManifest := gather("purpose-fingerprint-one", time.Now().UnixMilli())
	time.Sleep(2 * time.Millisecond)
	twoInput, twoManifest := gather("purpose-fingerprint-two", time.Now().UnixMilli())
	one, err := purposeUnderstandingEvidenceFingerprint(oneInput, oneManifest, "gpt-5.6-sol")
	if err != nil {
		t.Fatal(err)
	}
	two, err := purposeUnderstandingEvidenceFingerprint(twoInput, twoManifest, "gpt-5.6-sol")
	if err != nil {
		t.Fatal(err)
	}
	if one != two {
		t.Fatalf("wall-clock-only cutoff change altered fingerprint: %s != %s", one, two)
	}
	keyOne, err := purposeUnderstandingIdempotencyKey(one)
	if err != nil {
		t.Fatal(err)
	}
	keyTwo, err := purposeUnderstandingIdempotencyKey(two)
	if err != nil {
		t.Fatal(err)
	}
	if keyOne != keyTwo || !strings.HasPrefix(keyOne, "purpose-understanding:") {
		t.Fatalf("stable fingerprint produced unstable idempotency keys: %q %q", keyOne, keyTwo)
	}
	otherModel, err := purposeUnderstandingEvidenceFingerprint(
		twoInput,
		twoManifest,
		"gpt-5.6-terra",
	)
	if err != nil {
		t.Fatal(err)
	}
	if otherModel == two {
		t.Fatal("model change did not alter Purpose understanding fingerprint")
	}

	detail, err := store.GetDetail(oneInput.CurrentPurpose[0].OpenLoopID)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	revision := purposeTestAppendInput(
		detail,
		"purpose-understanding-fingerprint-revision",
		PurposeEventRevised,
		PurposeStanceUpdate,
		PurposeEvidenceExplicitUser,
		"purpose-understanding-fingerprint-revision",
		time.Now().UnixMilli(),
	)
	revision.DesiredOutcome = "A materially changed outcome is now current."
	if _, added, err := store.AppendEvent(revision); err != nil || !added {
		t.Fatalf("append Purpose fingerprint revision: added=%v err=%v", added, err)
	}
	threeInput, threeManifest := gather("purpose-fingerprint-three", time.Now().UnixMilli())
	three, err := purposeUnderstandingEvidenceFingerprint(
		threeInput,
		threeManifest,
		"gpt-5.6-sol",
	)
	if err != nil {
		t.Fatal(err)
	}
	if three == two {
		t.Fatal("changed current Purpose projection did not alter fingerprint")
	}
}

func TestPurposeExtractionV1HistoricalRunRemainsReadable(t *testing.T) {
	activity := newTestStore(t)
	cutoff := time.Now().Add(-time.Minute).UnixMilli()
	input := PurposeExtractionInput{
		Version: PurposeExtractionInputVersionV1, CutoffAt: cutoff,
		Sources: []PurposeExtractionSource{},
	}
	manifest := PurposeExtractionManifest{
		Version: PurposeExtractionManifestVersionV1, CutoffAt: cutoff,
		BudgetChars: purposeExtractionMaxPromptCharacters,
		Entries:     []PurposeExtractionManifestEntry{},
	}
	prompt, err := buildPurposeExtractionPrompt(input)
	if err != nil {
		t.Fatal(err)
	}
	execution := PurposeExtractionExecution{
		Input: input, Manifest: manifest, Prompt: prompt,
		PromptHash: agencyPayloadHash([]byte(prompt)),
		StartedAt:  cutoff, CompletedAt: cutoff,
	}
	run, hypotheses, err := purposeExtractionRecords(
		PurposeExtractionRequest{
			IdempotencyKey: "purpose-extraction-v1-historical",
			CutoffAt:       cutoff,
		},
		"gpt-5.6-sol",
		execution,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	legacyInput := struct {
		Version  string                    `json:"version"`
		CutoffAt int64                     `json:"cutoffAt"`
		Sources  []PurposeExtractionSource `json:"sources"`
	}{Version: input.Version, CutoffAt: input.CutoffAt, Sources: input.Sources}
	legacyJSON, err := json.Marshal(legacyInput)
	if err != nil {
		t.Fatal(err)
	}
	run.InputJSON = string(legacyJSON)
	run.InputHash = rawPurposeHash(run.InputJSON)
	run.PayloadJSON, run.PayloadHash, err = purposeExtractionRunPayloadJSON(run)
	if err != nil {
		t.Fatal(err)
	}
	detail, added, err := persistPurposeExtractionRun(activity.db, run, hypotheses)
	if err != nil || !added {
		t.Fatalf("persist historical v1 run: added=%v err=%v", added, err)
	}
	if detail.Run.InputVersion != PurposeExtractionInputVersionV1 ||
		detail.Run.PromptVersion != PurposeExtractionPromptVersionV1 ||
		detail.Input.CurrentPurpose != nil ||
		detail.Run.Status != PurposeExtractionStatusNoSources {
		t.Fatalf("historical v1 run did not round-trip: %+v", detail)
	}
	if !strings.Contains(detail.Run.Prompt, "Frozen source input:") ||
		strings.Contains(detail.Run.Prompt, "currentPurpose") {
		t.Fatalf("historical v1 prompt reconstruction changed: %s", detail.Run.Prompt)
	}
}

func purposeTestIndex(index int) string {
	return strconv.Itoa(index)
}
