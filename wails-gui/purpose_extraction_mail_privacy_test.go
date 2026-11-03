package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"wails-gui/storage"
	"yansu-agent/agent"
)

func purposeMailTestSource(ref, origin, words string) PurposeExtractionSource {
	class := PurposeEvidenceEvaluatedProjection
	if taskContextMarkdownEvidenceClass(origin, "topic_highlight") == TaskContextEvidenceExplicitUser {
		class = PurposeEvidenceExplicitUser
	}
	return PurposeExtractionSource{
		Ref: ref, SourceKind: taskContextSourceMemoryKind, SourceID: ref, EvidenceClass: class,
		Content: buildOKFMemoryMarkdown(ref, "Generated metadata title", time.Now(), memorySource(origin), "topic_highlight", []string{words}),
	}
}

func TestPurposeExtractionMailProvenance(t *testing.T) {
	const subject = "Contract renewal for Acme"
	const excerpt = "Please transfer the outstanding balance by Friday"
	projection := purposeMailTestSource("activity", "activity", "Read message "+subject+". "+excerpt+".")
	own := purposeMailTestSource("own", "manual", "I want to track "+subject+". "+excerpt+".")
	unrelated := purposeMailTestSource("unrelated", "manual", "I will prepare the launch decision.")
	knowledge := projection
	knowledge.Ref, knowledge.SourceKind = "knowledge", taskContextSourceKnowledgeKind
	edited := purposeMailTestSource("edited", "user_edit", "I want to track "+subject+".")
	self := PurposeExtractionSource{Ref: "self", SourceKind: taskContextSourceSelfKind, EvidenceClass: PurposeEvidenceExplicitUser, Content: subject}
	for _, test := range []struct {
		name, title, outcome string
		refs                 []string
		sources              []PurposeExtractionSource
		subjects             []string
		blocked              bool
	}{
		{"subject from memory", subject, "The agreement is reviewed.", []string{"activity"}, []PurposeExtractionSource{projection}, []string{"Re: " + subject}, true},
		{"wrapped subject", "Review " + subject + " today", "The agreement is reviewed.", []string{"activity"}, []PurposeExtractionSource{projection}, []string{subject}, true},
		{"outcome subject", "Review agreement", "Finish " + subject, []string{"activity"}, []PurposeExtractionSource{projection}, []string{subject}, true},
		{"unrelated explicit source", subject, "The agreement is reviewed.", []string{"activity", "unrelated"}, []PurposeExtractionSource{projection, unrelated}, []string{subject}, true},
		{"uncited user words", subject, "The agreement is reviewed.", []string{"activity"}, []PurposeExtractionSource{projection, own}, []string{subject}, true},
		{"confirmed Self is not authorship", subject, "The agreement is reviewed.", []string{"self"}, []PurposeExtractionSource{self}, []string{subject}, true},
		{"knowledge projection", subject, "The agreement is reviewed.", []string{"knowledge"}, []PurposeExtractionSource{knowledge}, []string{subject}, true},
		{"user edited subject", subject, "The agreement is reviewed.", []string{"edited"}, []PurposeExtractionSource{edited}, []string{subject}, false},
		{"user typed subject", subject, "The agreement is reviewed.", []string{"own"}, []PurposeExtractionSource{projection, own}, []string{subject}, false},
		{"short body", "Wire $900", "The agreement is reviewed.", []string{"activity"}, []PurposeExtractionSource{purposeMailTestSource("activity", "activity", "The message says: Wire $900.")}, nil, true},
		{"short Chinese body", "转账九百元", "The agreement is reviewed.", []string{"activity"}, []PurposeExtractionSource{purposeMailTestSource("activity", "activity", "邮件正文：转账九百元。")}, nil, true},
		{"body title without headers", excerpt, "The agreement is reviewed.", []string{"activity"}, []PurposeExtractionSource{projection}, nil, true},
		{"body outcome without headers", "Review agreement", excerpt, []string{"activity"}, []PurposeExtractionSource{projection}, nil, true},
		{"wrapped body excerpt", "Remember to transfer the outstanding balance by Friday now", "The agreement is reviewed.", []string{"activity"}, []PurposeExtractionSource{projection}, nil, true},
		{"user typed body", excerpt, "The agreement is reviewed.", []string{"own"}, []PurposeExtractionSource{projection, own}, nil, false},
		{"embedded Chinese body", "请记住尽快确认这份合同的最终付款条款后继续", "The agreement is reviewed.", []string{"activity"}, []PurposeExtractionSource{purposeMailTestSource("activity", "activity", "邮件正文：尽快确认这份合同的最终付款条款。")}, nil, true},
		{"neutral paraphrase", "Review agreement", "The renewal terms are assessed.", []string{"activity"}, []PurposeExtractionSource{projection}, []string{subject}, false},
		{"short subject", "HR", "The agreement is reviewed.", []string{"activity"}, []PurposeExtractionSource{projection}, []string{"HR"}, true},
		{"short subject word boundary", "Ship the release", "The release is available.", []string{"activity"}, []PurposeExtractionSource{projection}, []string{"Hi"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := PurposeExtractionInput{Sources: test.sources, MailPrivacy: newPurposeExtractionMailPrivacy(test.subjects)}
			hypothesis := PurposeExtractionHypothesis{Title: test.title, DesiredOutcome: test.outcome, EvidenceRefs: test.refs}
			reason := purposeExtractionMailTextReason(hypothesis, input, test.subjects)
			if (reason != "") != test.blocked {
				t.Fatalf("mail privacy reason = %q, blocked = %v", reason, test.blocked)
			}
		})
	}
}

func TestPurposeExtractionRejectsMailCopiesWithoutDiscardingNeutralHypotheses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	activity := newTestStore(t)
	at := time.Now().Add(-time.Minute)
	const subject = "Contract renewal for Acme"
	purposeWriteMemory(t, home, "mail-observation", at, memorySource("activity"), "Reviewed mail about "+subject+".")
	request := PurposeExtractionRequest{IdempotencyKey: "mail-privacy-mixed-output", CutoffAt: time.Now().UnixMilli()}
	ref := purposeExtractionSourceRef(t, activity, request, "mail-observation")
	runner := &purposeExtractionFakeRunner{responses: []string{purposeExtractionOutputJSON(t,
		purposeExtractionHypothesis("copied", "copied agreement", PurposeKindGoal, PurposeOperationSupport, subject, "The agreement is assessed.", ref),
		purposeExtractionHypothesis("neutral", "neutral agreement", PurposeKindGoal, PurposeOperationSupport, "Review agreement", "The renewal terms are assessed.", ref),
	)}}
	runtime := purposeNewRuntime(t, activity, runner)
	runtime.mailSubjects = []string{subject}
	result, err := runtime.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Detail.Hypotheses) != 2 || len(result.Detail.Receipts) != 2 {
		t.Fatalf("expected both audit hypotheses and receipts: %+v", result.Detail)
	}
	rejected := 0
	for _, receipt := range result.Detail.Receipts {
		if receipt.Disposition == PurposeDispositionRejected && strings.HasPrefix(receipt.Reason, "mail privacy:") {
			rejected++
		}
	}
	details, err := runtime.purpose.ListDetailsAll()
	if err != nil || rejected != 1 || len(details) != 1 || details[0].Current.Title != "Review agreement" {
		t.Fatalf("mail copy reached Purpose or neutral result lost: roots=%+v rejected=%d err=%v", details, rejected, err)
	}
	if result.Detail.Input.MailPrivacy.SubjectCount != 1 || strings.Contains(result.Detail.Run.Prompt, `"mailPrivacy"`) {
		t.Fatal("local mail policy was not frozen independently of model evidence")
	}
	if !strings.Contains(result.Detail.Run.Prompt, "Mail subjects and mail body excerpts are not eligible") {
		t.Fatal("current extraction prompt lost the mail privacy rule")
	}
}

func TestPurposeUnderstandingMailContextChangesFingerprint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bridge, _, now := mobileTestGoalBridge(t)
	service, err := NewPurposeUnderstandingService(bridge.app)
	if err != nil {
		t.Fatal(err)
	}
	purposeWriteMemory(t, home, "privacy-cache", now.Add(-time.Minute), memorySource("manual"), "I will review the agreement.")
	runner := &purposeExtractionFakeRunner{responses: []string{purposeExtractionOutputJSON(t), purposeExtractionOutputJSON(t)}}
	service.now = func() time.Time { return now }
	service.runner = func() (agent.AgentRunner, string, error) { return runner, "gpt-5.6-sol", nil }
	service.runAndReport(context.Background(), []string{PurposeUnderstandingTriggerStartup})
	first := service.Snapshot().Last
	if first == nil || first.State != PurposeUnderstandingStateNoChange {
		t.Fatalf("first cycle = %+v", first)
	}
	if _, err := bridge.app.store.UpsertMailMessages(context.Background(), []storage.MailMessageRow{{
		ID: "privacy-new-mail", Mailbox: storage.MailboxInbox, Subject: "Contract renewal for Acme", At: now.UnixMilli(),
	}}, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	service.runAndReport(context.Background(), []string{PurposeUnderstandingTriggerPeriodic})
	second := service.Snapshot().Last
	if second == nil || second.State != PurposeUnderstandingStateNoChange || second.Cached || first.Fingerprint == second.Fingerprint || len(runner.inputs) != 2 {
		t.Fatalf("new mail context reused old extraction: first=%+v second=%+v calls=%d", first, second, len(runner.inputs))
	}
	if strings.Contains(runner.inputs[1].Prompt, "Contract renewal for Acme") {
		t.Fatal("unrelated mail subject was sent to the model")
	}
	stored, err := loadPurposeExtractionRun(bridge.app.activity.store.db, second.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(stored.Run.InputJSON), "contract renewal for acme") || stored.Input.MailPrivacy.SubjectCount != 1 {
		t.Fatal("unrelated mail subject was persisted instead of a content-free digest")
	}
}

func TestPurposeExtractionV2ReplayRejectsPendingMailCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	activity := newTestStore(t)
	at := time.Now().Add(-time.Minute)
	const subject = "Contract renewal for Acme"
	purposeWriteMemory(t, home, "legacy-mail", at, memorySource("activity"), "Read "+subject+".")
	request := PurposeExtractionRequest{IdempotencyKey: "legacy-mail-pending", CutoffAt: time.Now().UnixMilli()}
	input, manifest, err := gatherPurposeExtractionInput(activity, request)
	if err != nil {
		t.Fatal(err)
	}
	input.Version, input.MailPrivacy = PurposeExtractionInputVersionV2, nil
	manifest.Version = PurposeExtractionManifestVersionV2
	runner := &purposeExtractionFakeRunner{responses: []string{purposeExtractionOutputJSON(t,
		purposeExtractionHypothesis("legacy", "legacy agreement", PurposeKindGoal, PurposeOperationSupport, subject, "The agreement is assessed.", input.Sources[0].Ref),
	)}}
	execution, err := runPurposeExtractionModel(context.Background(), runner, "gpt-5.6-sol", input, manifest)
	if err != nil {
		t.Fatal(err)
	}
	run, hypotheses, err := purposeExtractionRecords(request, "gpt-5.6-sol", execution, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := persistPurposeExtractionRun(activity.db, run, hypotheses); err != nil {
		t.Fatal(err)
	}
	runtime := purposeNewRuntime(t, activity, nil)
	runtime.mailSubjects = []string{subject}
	result, err := runtime.Reconcile(run.ID)
	if err != nil || len(result.Receipts) != 1 || result.Receipts[0].Disposition != PurposeDispositionRejected {
		t.Fatalf("legacy pending mail hypothesis applied: detail=%+v err=%v", result, err)
	}
	if purposeCountRows(t, activity, "purpose_open_loops_v1") != 0 {
		t.Fatal("legacy mail hypothesis created a Purpose")
	}
	if result.Run.PromptVersion != PurposeExtractionPromptVersionV2 || strings.Contains(result.Run.Prompt, "mailPrivacy") {
		t.Fatal("historical v2 audit prompt changed")
	}
}

func TestPurposeUnderstandingMailContextFailsClosed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	activity := newTestStore(t)
	service, app := purposeUnderstandingTestService(t, activity)
	app.store = nil
	runner := &purposeExtractionFakeRunner{}
	service.runner = func() (agent.AgentRunner, string, error) { return runner, "gpt-5.6-sol", nil }
	service.runAndReport(context.Background(), []string{PurposeUnderstandingTriggerStartup})
	last := service.Snapshot().Last
	if last == nil || last.State != PurposeUnderstandingStateFailed || len(runner.inputs) != 0 || !strings.Contains(last.Error, "mail privacy") {
		t.Fatalf("missing mail context allowed extraction: last=%+v calls=%d", last, len(runner.inputs))
	}
}

func TestPurposeExtractionMailRecoveryPreservesCommittedEvent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	activity := newTestStore(t)
	at := time.Now().Add(-time.Minute)
	const subject = "Contract renewal for Acme"
	purposeWriteMemory(t, home, "committed-mail", at, memorySource("activity"), "Read "+subject+".")
	request := PurposeExtractionRequest{IdempotencyKey: "legacy-mail-committed", CutoffAt: time.Now().UnixMilli()}
	input, manifest, err := gatherPurposeExtractionInput(activity, request)
	if err != nil {
		t.Fatal(err)
	}
	input.Version, input.MailPrivacy = PurposeExtractionInputVersionV2, nil
	manifest.Version = PurposeExtractionManifestVersionV2
	runner := &purposeExtractionFakeRunner{responses: []string{purposeExtractionOutputJSON(t,
		purposeExtractionHypothesis("committed", "committed agreement", PurposeKindGoal, PurposeOperationSupport, subject, "The agreement is assessed.", input.Sources[0].Ref),
	)}}
	execution, err := runPurposeExtractionModel(context.Background(), runner, "gpt-5.6-sol", input, manifest)
	if err != nil {
		t.Fatal(err)
	}
	run, hypotheses, err := purposeExtractionRecords(request, "gpt-5.6-sol", execution, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := persistPurposeExtractionRun(activity.db, run, hypotheses); err != nil {
		t.Fatal(err)
	}
	runtime := purposeNewRuntime(t, activity, nil)
	createInput, err := purposeCreateInputForHypothesis(hypotheses[0], purposeSourcesByRef(input))
	if err != nil {
		t.Fatal(err)
	}
	if _, added, err := runtime.purpose.Create(createInput); err != nil || !added {
		t.Fatalf("simulate committed event without receipt: added=%v err=%v", added, err)
	}
	runtime.mailSubjects = []string{subject}
	for attempt := 0; attempt < 2; attempt++ {
		result, err := runtime.Reconcile(run.ID)
		if err != nil || len(result.Receipts) != 1 || result.Receipts[0].Disposition != PurposeDispositionCreated || result.Receipts[0].EventID == "" {
			t.Fatalf("committed effect lost its receipt: detail=%+v err=%v", result, err)
		}
	}
	if purposeCountRows(t, activity, "purpose_open_loop_events_v1") != 1 || purposeCountRows(t, activity, "purpose_reconciliation_receipts_v1") != 1 {
		t.Fatal("privacy-aware recovery duplicated an event or receipt")
	}
}

func TestPurposeExtractionMailGeneratedHeadlinesRemainNonUserText(t *testing.T) {
	const excerpt = "Wire $900"
	headline := purposeMailTestSource("headline", "activity", "The user reviewed a payment request.")
	headline.Content = buildOKFMemoryMarkdown("headline", excerpt, time.Now(), memorySource("activity"), "topic_highlight", []string{"The user reviewed a payment request."})
	own := purposeMailTestSource("own", "manual", "I want to track: "+excerpt+".")
	metadataOnly := purposeMailTestSource("metadata", "manual", "I will review the agreement.")
	metadataOnly.Content = buildOKFMemoryMarkdown("metadata", excerpt, time.Now(), memorySource("manual"), "topic_highlight", []string{"I will review the agreement."})
	for _, kind := range []string{taskContextSourceMemoryKind, taskContextSourceKnowledgeKind} {
		t.Run(kind, func(t *testing.T) {
			headline.SourceKind = kind
			for _, test := range []struct {
				name, title, outcome string
				refs                 []string
				sources              []PurposeExtractionSource
				blocked              bool
			}{
				{"title copy", excerpt, "The payment request is assessed.", []string{"headline"}, []PurposeExtractionSource{headline}, true},
				{"outcome copy", "Review payment request", excerpt, []string{"headline"}, []PurposeExtractionSource{headline}, true},
				{"neutral paraphrase", "Assess requested payment", "The requested amount is checked.", []string{"headline"}, []PurposeExtractionSource{headline}, false},
				{"cited user words", excerpt, "The requested amount is checked.", []string{"headline", "own"}, []PurposeExtractionSource{headline, own}, false},
				{"uncited user words", excerpt, "The requested amount is checked.", []string{"headline"}, []PurposeExtractionSource{headline, own}, true},
				{"metadata is not user words", excerpt, "The requested amount is checked.", []string{"metadata"}, []PurposeExtractionSource{metadataOnly}, true},
			} {
				t.Run(test.name, func(t *testing.T) {
					hypothesis := PurposeExtractionHypothesis{Title: test.title, DesiredOutcome: test.outcome, EvidenceRefs: test.refs}
					reason := purposeExtractionMailTextReason(hypothesis, PurposeExtractionInput{Sources: test.sources}, nil)
					if (reason != "") != test.blocked {
						t.Fatalf("headline privacy reason=%q blocked=%v", reason, test.blocked)
					}
				})
			}
		})
	}
}

func TestPurposeExtractionAppendsRawTitleAfterPresentationRepair(t *testing.T) {
	at := time.Now().Add(-time.Minute).UnixMilli()
	raw := PurposeOpenLoopEventRecord{
		ID: "original-event", Title: "Original private title", DesiredOutcome: "The request is assessed.",
		Owner: PurposeOwnerUser, DueKind: PurposeDueNone, ReasoningStatus: PurposeReasoningUnknown,
		LifecycleState: PurposeStateActive, SupportState: PurposeSupportSupported, OccurredAt: at,
	}
	target := PurposeOpenLoopDetail{OpenLoop: PurposeOpenLoopRecord{ID: "repaired-purpose"}, Current: raw, Events: []PurposeOpenLoopEventRecord{raw}}
	target.Current.Title = "Goal to review"
	source := PurposeExtractionSource{Ref: "user", SourceID: "user", EvidenceClass: PurposeEvidenceExplicitUser, SourceObservedAt: at}
	for _, operation := range []string{PurposeOperationSupport, PurposeOperationWait, PurposeOperationResolve, PurposeOperationDismiss, PurposeOperationUpdate} {
		t.Run(operation, func(t *testing.T) {
			hypothesis := PurposeExtractionHypothesisRecord{
				ID: "repair-followup", Operation: operation, Title: "A newly stated title", DesiredOutcome: "A newly stated outcome.",
				Owner: PurposeOwnerUser, DueKind: PurposeDueNone, ReasoningStatus: PurposeReasoningUnknown,
				WaitingCondition: "Approval arrives", EvidenceRefs: []string{"user"},
			}
			input, _, err := purposeAppendInputForHypothesis(hypothesis, target, map[string]PurposeExtractionSource{"user": source})
			if err != nil {
				t.Fatal(err)
			}
			wantTitle, wantOutcome := raw.Title, raw.DesiredOutcome
			if operation == PurposeOperationUpdate {
				wantTitle, wantOutcome = hypothesis.Title, hypothesis.DesiredOutcome
			}
			if input.Title != wantTitle || input.DesiredOutcome != wantOutcome || input.ExpectedEventID != raw.ID {
				t.Fatalf("presentation repair became inferred ledger data: %+v", input)
			}
		})
	}
}
