import {
    act,
    fireEvent,
    render,
    screen,
    waitFor,
    within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type {
    ShadowAgentDebugSnapshot,
    ShadowRunDebugDetail,
    TaskShadowDebugDetail,
    TaskShadowDebugSnapshot,
} from "@/lib/wails-shim-app";
import { ShadowAgentDebugSettings } from "./shadow-agent-debug-settings";

const mockGetSnapshot = vi.fn();
const mockGetRun = vi.fn();
const mockGetTaskSnapshot = vi.fn();
const mockGetTaskDetail = vi.fn();
const mockGetObserverSnapshot = vi.fn();
const mockGetObserverDetail = vi.fn();
const mockGetObserverCoverage = vi.fn();
const mockGetSelfDreamSnapshot = vi.fn();
const mockGetSelfDreamRun = vi.fn();
const mockReplaySelfDream = vi.fn();
const mockEventsOn = vi.fn((
    _eventName: string,
    _callback: (...args: unknown[]) => void,
) => vi.fn());
const translatedLabels: Record<string, string> = {
    "shadowDebug.title": "Shadow learning",
    "shadowDebug.groups.shadowWork": "Task timeline",
    "shadowDebug.groups.learning": "Learning quality",
    "shadowDebug.groups.sources": "Learning sources",
    "shadowDebug.tabs.taskEpisodes": "Observed tasks",
    "shadowDebug.tabs.observer": "Task detection",
    "shadowDebug.tabs.runs": "Private practice",
    "shadowDebug.tabs.attempts": "Comparisons",
    "shadowDebug.tabs.workProfile": "Work patterns",
    "shadowDebug.tabs.profiles": "Prediction accuracy",
    "shadowDebug.tabs.qualifications": "Suggestion readiness",
    "shadowDebug.tabs.selfModel": "Supported insights",
    "shadowDebug.tabs.selfDream": "Profile suggestions",
    "shadowDebug.tabs.background": "Background learning",
    "shadowDebug.tabs.appAccess": "App access",
};
const mockTranslate = (key: string) => translatedLabels[key] ?? key;

function selectShadowView(group: "Task timeline" | "Learning quality" | "Learning sources", view: string) {
    fireEvent.click(screen.getByRole("tab", { name: group }));
    fireEvent.click(screen.getByRole("tab", { name: view }));
}

vi.mock("@/lib/wails-shim-app", async (importOriginal) => {
    const original = await importOriginal<typeof import("@/lib/wails-shim-app")>();
    return {
        ...original,
        GetShadowAgentDebugSnapshot: (...args: unknown[]) => mockGetSnapshot(...args),
        GetShadowAgentDebugRun: (...args: unknown[]) => mockGetRun(...args),
        GetTaskShadowDebugSnapshot: (...args: unknown[]) =>
            mockGetTaskSnapshot(...args),
        GetTaskShadowDebugDetail: (...args: unknown[]) =>
            mockGetTaskDetail(...args),
        GetTaskShadowObserverDebugSnapshot: (...args: unknown[]) =>
            mockGetObserverSnapshot(...args),
        GetTaskShadowObserverDebugDetail: (...args: unknown[]) =>
            mockGetObserverDetail(...args),
        GetTaskShadowObserverCoverageSnapshot: (...args: unknown[]) =>
            mockGetObserverCoverage(...args),
        GetSelfDreamDebugSnapshot: (...args: unknown[]) =>
            mockGetSelfDreamSnapshot(...args),
        GetSelfDreamDebugRun: (...args: unknown[]) =>
            mockGetSelfDreamRun(...args),
        ReplaySelfDreamDebug: (...args: unknown[]) =>
            mockReplaySelfDream(...args),
    };
});

vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: (
        eventName: string,
        callback: (...args: unknown[]) => void,
    ) => mockEventsOn(eventName, callback),
}));

vi.mock("react-i18next", () => ({
    useTranslation: () => ({
        t: mockTranslate,
    }),
}));

const snapshot = {
    generatedAt: 1_721_700_000_000,
    runs: [{
        id: "shadow-run-1",
        capabilityKey: "customer.follow_up.draft",
        status: "evaluated",
        sourceKind: "activity",
        sourceId: "activity-1",
        predictedAction: "Draft the customer follow-up.",
        expectedResult: "A draft exists.",
        actionConfidence: 0.84,
        observationCount: 1,
        evaluationCount: 1,
        observationHorizonEnd: 1_721_786_400_000,
        createdAt: 1_721_700_000_000,
    }],
    profiles: [{
        id: "profile-1",
        capabilityKey: "customer.follow_up.draft",
        policyVersion: "shadow-capability-profile-v1",
        evidenceHash: "profile-evidence-hash",
        evaluationCount: 5,
        qualifiedEvaluationCount: 5,
        excludedEvaluationCount: 0,
        runCount: 5,
        predictionSourceCount: 5,
        observationSourceCount: 5,
        sourcePairCount: 5,
        sourceKindCount: 1,
        contextCount: 5,
        action: {
            totalCount: 5,
            knownCount: 5,
            qualifiedCount: 5,
            unknownCount: 0,
            lowConfidenceCount: 0,
            missingScoreCount: 0,
            assessmentCounts: { matched: 5 },
            qualifiedAssessmentCounts: { matched: 5 },
            reliability: 1,
            averageConfidence: 0.9,
        },
        outcome: {
            totalCount: 5,
            knownCount: 5,
            qualifiedCount: 5,
            unknownCount: 0,
            lowConfidenceCount: 0,
            missingScoreCount: 0,
            assessmentCounts: { matched: 5 },
            qualifiedAssessmentCounts: { matched: 5 },
            reliability: 1,
            averageConfidence: 0.85,
        },
        timing: {
            totalCount: 5,
            knownCount: 5,
            qualifiedCount: 5,
            unknownCount: 0,
            lowConfidenceCount: 0,
            missingScoreCount: 0,
            assessmentCounts: { partial: 5 },
            qualifiedAssessmentCounts: { partial: 5 },
            reliability: 0.5,
            averageConfidence: 0.75,
        },
        reasoning: {
            totalCount: 5,
            knownCount: 0,
            qualifiedCount: 0,
            unknownCount: 5,
            lowConfidenceCount: 0,
            missingScoreCount: 0,
            assessmentCounts: { unknown: 5 },
            qualifiedAssessmentCounts: {},
        },
        safety: {
            totalCount: 5,
            knownCount: 5,
            qualifiedCount: 5,
            unknownCount: 0,
            lowConfidenceCount: 0,
            missingScoreCount: 0,
            assessmentCounts: { safe: 5 },
            qualifiedAssessmentCounts: { safe: 5 },
            reliability: 1,
            averageConfidence: 0.9,
        },
        readiness: {
            status: "ready",
            ready: true,
            reasons: [],
            minQualifiedEvaluations: 5,
            minPredictionSources: 3,
            minObservationSources: 5,
            minContexts: 3,
            minQualifiedSafetyAssessments: 3,
            actionReliabilityThreshold: 0.75,
            dimensionConfidenceThreshold: 0.7,
        },
        evidence: [{
            evaluationId: "evaluation-1",
            shadowRunId: "shadow-run-1",
            observationId: "observation-1",
            predictionSourceKind: "activity",
            predictionSourceId: "activity-1",
            observationSourceKind: "activity",
            observationSourceId: "activity-2",
            contextHash: "context-hash",
            actionAssessment: "matched",
            outcomeAssessment: "matched",
            timingAssessment: "partial",
            reasoningAssessment: "unknown",
            safetyAssessment: "safe",
            qualified: true,
            evaluationCreatedAt: 1_721_700_012_000,
        }],
        createdAt: 1_721_700_013_000,
    }],
    qualifications: [{
        id: "qualification-1",
        qualificationKey: "qualification-key",
        policyVersion: "shadow-qualification-v1",
        profileId: "profile-1",
        profileEvidenceHash: "profile-evidence-hash",
        capabilityKey: "customer.follow_up.draft",
        representativeShadowRunId: "shadow-run-1",
        candidatePlanHash: "plan-hash",
        runtimeEvidenceHash: "runtime-hash",
        runtimeAttemptIds: ["shadow-runtime-attempt-1"],
        riskTier: "local_write",
        status: "qualified",
        reasons: [],
        thresholds: {
            minQualifiedEvaluations: 7,
            minPredictionSources: 4,
            minObservationSources: 7,
            minContexts: 4,
            minQualifiedSafetyAssessments: 5,
            minActionReliability: 0.8,
            minCompletedRuntimeAttempts: 2,
        },
        actual: {
            qualifiedEvaluations: 7,
            predictionSources: 7,
            observationSources: 7,
            contexts: 7,
            qualifiedSafetyAssessments: 7,
            unsafeAssessments: 0,
            actionReliability: 1,
            completedRuntimeAttempts: 7,
        },
        effects: [{
            capability: "filesystem.write",
            effect: "local_write",
            resource: "/tmp/customer-follow-up.md",
            reversible: true,
        }],
        proposalInitiativeId: "",
        createdAt: 1_721_700_014_000,
    }],
    selfModel: [{
        id: "self-model-1",
        entryKey: "self-model-key",
        revision: 1,
        kind: "preference",
        scope: "customer.follow_up.draft",
        statement: "Draft the customer follow-up after review.",
        status: "provisional",
        confidence: 0.84,
        reasoningStatus: "behavior_derived",
        evidence: [{
            sourceKind: "shadow_qualification",
            sourceId: "qualification-1",
            statement: "Draft the customer follow-up after review.",
            confidence: 0.84,
            sourceRefs: ["shadow-runtime-attempt-1"],
            occurredAt: 1_721_700_014_000,
        }],
        contradictions: [],
        sourceHash: "self-model-source-hash",
        createdAt: 1_721_700_014_000,
    }],
    attempts: [{
        id: "attempt-1",
        sourceKind: "activity",
        sourceId: "activity-2",
        observedAt: 1_721_700_010_000,
        observedSummary: "The follow-up draft was saved.",
        status: "completed",
        reason: "comparison_completed",
        candidateCount: 1,
        selectedCount: 1,
        reportedMatches: 1,
        persistedRunIds: ["shadow-run-1"],
        candidates: [{
            shadowRunId: "shadow-run-1",
            capabilityKey: "customer.follow_up.draft",
            relevance: 4,
            payloadBytes: 1200,
            selected: true,
            reason: "selected",
        }],
        comparatorPrompt: "exact comparator prompt",
        comparatorHash: "hash",
        comparatorVersion: "shadow-comparator-v1",
        evaluatorModel: "gpt-5.6-terra",
        evaluatorRaw: `{"matches":[{"shadowRunId":"shadow-run-1"}]}`,
        createdAt: 1_721_700_011_000,
    }],
} satisfies ShadowAgentDebugSnapshot;

const detail = {
    detail: {
        run: {
            id: "shadow-run-1",
            decisionKey: "decision-1",
            capabilityKey: "customer.follow_up.draft",
            sourceKind: "activity",
            sourceId: "activity-1",
            contextHash: "context-hash",
            context: {
                capturedAt: 1_721_700_000_000,
                promptContext: "Frozen production prompt",
                references: [],
            },
            predictionHash: "prediction-hash",
            prediction: {
                goal: "Move the commitment forward",
                predictedAction: "Draft the customer follow-up.",
                expectedResult: "A draft exists.",
                reasoningHypotheses: [],
                goalConfidence: 0.8,
                actionConfidence: 0.84,
                resultConfidence: 0.76,
                timingConfidence: 0.5,
            },
            model: "gpt-5.6-sol",
            promptVersion: "activity-shadow-v2",
            runnerVersion: "activity-triage",
            observationHorizonStart: 1_721_700_000_000,
            observationHorizonEnd: 1_721_786_400_000,
            createdAt: 1_721_700_000_000,
        },
        status: "evaluated",
        evidence: [],
        observations: [{
            id: "observation-1",
            shadowRunId: "shadow-run-1",
            sourceKind: "activity",
            sourceId: "activity-2",
            observedAction: "Drafted the customer follow-up.",
            observedResult: "The draft was saved.",
            reasoningEvidence: [],
            evidence: [],
            observedAt: 1_721_700_010_000,
            createdAt: 1_721_700_011_000,
        }],
        evaluations: [{
            id: "evaluation-1",
            shadowRunId: "shadow-run-1",
            observationId: "observation-1",
            idempotencyKey: "evaluation-key",
            comparisonVersion: "shadow-comparator-v1",
            evaluatorModel: "gpt-5.6-terra",
            comparatorPrompt: "exact comparator prompt",
            comparatorHash: "hash",
            evaluatorRaw: `{"matches":[]}`,
            action: { assessment: "matched", score: 1, confidence: 0.9 },
            outcome: { assessment: "matched", score: 1, confidence: 0.8 },
            timing: { assessment: "unknown", confidence: 0.2 },
            reasoning: { assessment: "unknown", confidence: 0.2 },
            safety: { assessment: "unknown", confidence: 0.2 },
            createdAt: 1_721_700_012_000,
        }],
    },
    executionAttempts: [{
        id: "shadow-runtime-attempt-1",
        shadowRunId: "shadow-run-1",
        idempotencyKey: "shadow-run-1:shadow-runtime-v1",
        runtimeVersion: "shadow-runtime-v1",
        status: "completed",
        policyVersion: "shadow-runtime-v1",
        agentRunId: "agent-shadow-run-1",
        model: "gpt-5.6-sol",
        planHash: "plan-hash",
        prompt: "Draft the customer follow-up and save it.",
        plan: {
            kind: "automation",
            name: "draft-customer-follow-up",
            prompt: "Draft the customer follow-up and save it.",
            scheduleKind: "manual",
            scheduleConfig: {},
            confidence: 0.84,
        },
        decisions: [{
            policy_version: "shadow-runtime-v1",
            decision: "deny",
            reason: "shadow runtime blocks every non-read or unclassified side effect",
            context: {
                run_id: "agent-shadow-run-1",
                shadow_run_id: "shadow-run-1",
                execution_mode: "shadow",
                unattended: true,
            },
            action: {
                capability: "filesystem.write",
                tool_name: "Write",
                operation: "write",
                effect: "local_write",
                resource: "/tmp/customer-follow-up.md",
                reversible: true,
            },
        }],
        assistantOutput: "The write was blocked; production state was unchanged.",
        startedAt: 1_721_700_001_000,
        completedAt: 1_721_700_002_000,
        createdAt: 1_721_700_002_000,
    }],
    memoryProjection: {
        path: "/tmp/shadow-run-learning-shadow-run-1.md",
        exists: true,
        content: "# Shadow mode\n\nObserved action: Drafted the customer follow-up.",
    },
} satisfies ShadowRunDebugDetail;

const taskSnapshot = {
    generatedAt: 1_721_700_020_000,
    limit: 200,
    enabledWorkflows: ["compose.text", "analyze.text"],
    releaseGate: {
        version: "task-shadow-release-gate-v7",
        stage: "evaluation_pending",
        complete: false,
        canObserveNaturalTask: true,
        monitoring: true,
        foregroundApp: "Mail",
        foregroundBundleId: "com.apple.mail",
        latestTaskEpisodeId: "task-episode-1",
        latestShadowRunId: "task-shadow-run-1",
        latestPairState: "pair_ready",
        selectedWorkflow: "compose.text",
        requiredShadowPrompt: "shadow-task-v7",
        selectedShadowPrompt: "shadow-task-v7",
        currentGeneration: true,
        historicalShadowRunIds: [],
        observerProvenance: {
            version: "task-shadow-release-observer-provenance-v2",
            state: "origin_verified",
            reason: "one exact production observer source created this Task Episode and Shadow run",
            sourceComplete: true,
            originComplete: true,
            pipelineRunId: 91,
            traceId: "observer-trace-1",
            traceContractVersion: "task-shadow-observer-trace-v2",
            coverageState: "covered",
            admission: "eligible",
            flow: "full_onset",
            terminalStatus: "completed",
            productionOnsetAttemptId: "attempt-production",
            taskEpisodeId: "task-episode-1",
            workflow: "compose.text",
            workflowDecisionState: "run_created",
            frozenContextHash: "context-hash",
            shadowRunId: "task-shadow-run-1",
            claimTraceCount: 1,
            candidateTraceCount: 1,
            candidateDecisionCount: 1,
            integrityValid: true,
        },
        requiredAction:
            "Wait for or inspect the deterministic pair readiness and evaluator attempt.",
        reasons: [],
        steps: [
            {
                key: "observer_source",
                label: "Real activity source found",
                complete: true,
                evidenceId: "observer-trace-1",
            },
            {
                key: "observer_origin",
                label: "Task start verified",
                complete: true,
                evidenceId: "attempt-production",
            },
            {
                key: "task_episode",
                label: "Your task completed",
                complete: true,
                evidenceId: "task-episode-1",
            },
            {
                key: "shadow_attempt",
                label: "Isoform's shadow attempt completed",
                complete: true,
                evidenceId: "shadow-attempt-1",
            },
            {
                key: "user_result",
                label: "Your result captured",
                complete: true,
                evidenceId: "user-result-digest",
            },
            {
                key: "result_destination",
                label: "Your result source verified",
                complete: true,
                evidenceId: "destination-digest",
            },
            {
                key: "shadow_result",
                label: "Isoform's result captured",
                complete: true,
                evidenceId: "shadow-result-digest",
            },
            {
                key: "evaluation",
                label: "Results compared across 8 dimensions",
                complete: false,
            },
            {
                key: "learning",
                label: "Safe learning update recorded",
                complete: false,
            },
        ],
    },
    continuation: {
        version: "task-shadow-continuation-v2",
        generatedAt: 1_721_700_020_000,
        scannedRunCount: 2,
        currentRunCount: 1,
        executionPendingCount: 0,
        evaluationPendingCount: 1,
        learningPendingCount: 0,
        materializationPendingCount: 0,
        awaitingTerminalCount: 0,
        awaitingResultCount: 0,
        completeCount: 0,
        blockedCount: 0,
        historicalCount: 1,
        firstRunExcludedCount: 0,
        items: [{
            taskEpisodeId: "task-episode-1",
            shadowRunId: "task-shadow-run-1",
            workflow: "compose.text",
            state: "evaluation_pending",
            action: "evaluate_pair",
            reason: "current terminal pair is ready for comparison",
            episodeStatus: "completed",
            attemptId: "shadow-attempt-1",
            attemptStatus: "completed",
            pairState: "pair_ready",
            currentEvaluationIds: [],
            createdAt: 1_721_700_002_000,
        }],
    },
    episodes: [{
        id: "task-episode-1",
        status: "mirroring",
        taskHypothesis: "Prepare the customer follow-up",
        desiredResultHypothesis: "A verified local draft exists.",
        taskConfidence: 0.86,
        boundaryConfidence: 0.78,
        desiredResultConfidence: 0.81,
        inferredStartAt: 1_721_700_000_000,
        detectedAt: 1_721_700_002_000,
        contextSnapshotId: "task-context-1",
        contextHash: "task-context-hash",
        contextIncludedCount: 1,
        contextExcludedCount: 1,
        linkedShadowRunIds: [],
        linkedShadowRunCount: 1,
        taskAgentAttemptCount: 1,
        persistedEvaluationCount: 0,
        latestPairWorkflow: "compose.text",
        latestPairState: "pair_ready",
        latestPairReasons: [],
        createdAt: 1_721_700_002_000,
    }],
} satisfies TaskShadowDebugSnapshot;

const taskDetail = {
    generatedAt: 1_721_700_020_000,
    limit: 200,
    productionRollout: {
        workflow: "compose.text",
        enabledWorkflows: ["compose.text", "analyze.text"],
        state: "eligible",
        eligible: true,
        reason: "workflow is enabled for the current production shadow rollout",
    },
    episode: {
        episode: {
            id: "task-episode-1",
            inferredStartAt: 1_721_700_000_000,
            detectedAt: 1_721_700_002_000,
            currentStatus: "mirroring",
            observationLease: {
                policyVersion: "task-lifecycle-state-bound-lease-v2",
                status: "mirroring",
                open: false,
                anchorKind: "state_transition",
                anchorTransitionId: "task-transition-1",
                anchorAt: 1_721_700_002_000,
                horizonMs: 0,
                deadlineAt: 0,
                due: false,
                evidenceRenews: false,
            },
            createPayloadHash: "episode-hash",
            frozenContext: {
                id: "task-context-1",
                taskEpisodeId: "task-episode-1",
                cutoffAt: 1_721_700_000_000,
                sha256: "task-context-hash",
                manifest: [
                    {
                        ref: "activity:before-onset",
                        section: "activity",
                        sourceKind: "activity",
                        sourceId: "activity-1",
                        observedAt: 1_721_699_999_000,
                        evidenceClass: "observed",
                        privacyDecision: "allowed",
                        included: true,
                        contentHash: "included-hash",
                        includedChars: 420,
                    },
                    {
                        ref: "activity:after-onset",
                        section: "activity",
                        sourceKind: "activity",
                        sourceId: "activity-2",
                        observedAt: 1_721_700_001_000,
                        evidenceClass: "observed",
                        privacyDecision: "allowed",
                        included: false,
                        reason: "after_onset_cutoff",
                    },
                ],
                manifestEntryCount: 7,
                manifestIncludedCount: 3,
                manifestExcludedCount: 4,
                manifestTruncated: true,
                canonicalJsonRedacted: true,
                createdAt: 1_721_700_002_000,
            },
            latestHypothesis: {
                taskHypothesis: "Prepare the customer follow-up",
                desiredResultHypothesis: "A verified local draft exists.",
                taskConfidence: 0.86,
                boundaryConfidence: 0.78,
                desiredResultConfidence: 0.81,
                revision: 1,
            },
            createdAt: 1_721_700_002_000,
        },
        hypotheses: [],
        evidence: [],
        transitions: [],
        links: [],
    },
    runs: [{
        run: {
            id: "task-shadow-run-1",
            decisionKey: "task-shadow-decision-1",
            capabilityKey: "compose.text",
            sourceKind: "task_episode",
            sourceId: "task-episode-1",
            contextHash: "task-context-hash",
            context: {
                capturedAt: 1_721_700_000_000,
                productionEventAt: 1_721_700_000_000,
                promptContext: "",
                references: [],
            },
            predictionHash: "task-shadow-prediction-hash",
            prediction: {
                goal: "Prepare a customer follow-up",
                predictedAction: "Compose the follow-up independently.",
                expectedResult: "A complete local draft.",
                reasoningHypotheses: [],
                goalConfidence: 0.86,
                actionConfidence: 0.82,
                resultConfidence: 0.81,
                timingConfidence: 0.3,
            },
            model: "gpt-5.6-sol",
            promptVersion: "shadow-task-v4",
            runnerVersion: "task-shadow-v1",
            observationHorizonStart: 1_721_700_000_000,
            observationHorizonEnd: 1_721_700_180_000,
            createdAt: 1_721_700_002_000,
            mode: "task_mirror",
            taskEpisodeId: "task-episode-1",
            taskContextHash: "task-context-hash",
        },
        status: "pair_ready",
        pairReadiness: {
            workflow: "compose.text",
            state: "pair_ready",
            ready: true,
            taskEpisodeStatus: "completed",
            shadowAttemptStatus: "completed",
            userResultMaterialized: true,
            userResultSha256: "user-result-digest",
            userResultDestinationSha256: "destination-digest",
            userResultTruncated: false,
            userResultEvidenceIds: ["user-result-evidence-1"],
            shadowResultMaterialized: true,
            shadowResultSha256: "shadow-result-digest",
            shadowArtifactDigests: ["shadow-result-digest"],
            evaluationIds: [],
            reasons: [],
        },
        evidence: [],
        observations: [],
        executionAttempts: [],
        evaluations: [],
        contextPromptRedacted: true,
        executionPlanRedacted: true,
    }],
    taskAgentAttempts: [{
        id: "task-agent-attempt-1",
        phase: "mirror_plan",
        taskEpisodeId: "task-episode-1",
        modelName: "gpt-5.6-sol",
        modelVersion: "gpt-5.6-sol",
        promptVersion: "task-shadow-mirror-v1",
        promptText: "Exact task mirror prompt",
        promptHash: "prompt-hash",
        normalizedInputJson: `{"taskEpisodeId":"task-episode-1"}`,
        inputHash: "input-hash",
        status: "completed",
        rawOutput: `{"action":"draft"}`,
        parsedOutputJson: `{"action":"draft"}`,
        startedAt: 1_721_700_003_000,
        completedAt: 1_721_700_004_000,
        createdAt: 1_721_700_004_000,
    }],
    learning: {
        runs: [],
        revisions: [],
        materializations: [],
    },
    correlations: {
        taskEpisodeId: "task-episode-1",
    },
    exclusions: [{
        stage: "context",
        correlationId: "activity:after-onset",
        reason: "after_onset_cutoff",
    }],
} satisfies TaskShadowDebugDetail;

beforeEach(() => {
    vi.clearAllMocks();
    mockGetSnapshot.mockResolvedValue(snapshot);
    mockGetRun.mockResolvedValue(detail);
    mockGetTaskSnapshot.mockResolvedValue(taskSnapshot);
    mockGetTaskDetail.mockResolvedValue(taskDetail);
    mockGetObserverSnapshot.mockResolvedValue({
        version: "task-shadow-observer-debug-v1",
        generatedAt: 1_800_000_000_000,
        limit: 200,
        counts: {
            total: 0,
            running: 0,
            interrupted: 0,
            completed: 0,
            completedWithErrors: 0,
            skipped: 0,
            cancelled: 0,
            failed: 0,
            corrupt: 0,
        },
        traces: [],
    });
    mockGetObserverDetail.mockResolvedValue(null);
    mockGetObserverCoverage.mockResolvedValue({
        version: "task-shadow-observer-coverage-v2",
        generatedAt: 0,
        limit: 200,
        health: "awaiting_instrumented_activity",
        healthReason: "No instrumented activity group has reached observer dispatch yet.",
        counts: {
            instrumentedSources: 0,
            legacySources: 0,
            currentTraces: 0,
            legacyTraces: 0,
            covered: 0,
            coveredRootOnly: 0,
            pendingDispatch: 0,
            dispatchFailed: 0,
            traceMissing: 0,
            linkMismatch: 0,
            traceCorrupt: 0,
            duplicateTrace: 0,
            sourcePruned: 0,
            unlinkedTrace: 0,
            scanTruncated: 0,
        },
        items: [],
        loadedItems: 0,
        scannedTraces: 0,
    });
    mockGetSelfDreamSnapshot.mockResolvedValue({
        generatedAt: 1_800_000_000_000,
        runs: [],
        rollout: {
            enabled: false,
            ready: false,
            completedRealRuns: 0,
            scoredCandidates: 0,
            meanImprovement: 0,
            coverageLossCount: 0,
            unsupportedReasoningCount: 0,
            correctionOrRejectRate: 0,
            staleCompilationErrors: 0,
            reasons: ["Need real paired evidence."],
        },
    });
});

describe("ShadowAgentDebugSettings", () => {
    it("loads only detected-task diagnostics until a summary view is opened", async () => {
        render(<ShadowAgentDebugSettings />);

        expect(
            await screen.findByRole("region", { name: "Observed tasks" }),
        ).toBeInTheDocument();
        await waitFor(() =>
            expect(mockGetTaskSnapshot).toHaveBeenCalledWith(200),
        );
        expect(mockGetSnapshot).not.toHaveBeenCalled();

        act(() => {
            for (const [eventName, callback] of mockEventsOn.mock.calls) {
                if (eventName === "activity_pipeline:run") callback();
            }
        });
        await waitFor(() =>
            expect(mockGetTaskSnapshot.mock.calls.length).toBeGreaterThan(1),
        );
        expect(mockGetSnapshot).not.toHaveBeenCalled();

        selectShadowView("Task timeline", "Private practice");
        await waitFor(() =>
            expect(mockGetSnapshot).toHaveBeenCalledWith(200),
        );
        expect(
            await screen.findAllByText("customer.follow_up.draft"),
        ).not.toHaveLength(0);
    });

    it("opens Background learning from a locked Shadow gate", async () => {
        mockGetTaskSnapshot.mockResolvedValueOnce({
            ...taskSnapshot,
            releaseGate: {
                ...taskSnapshot.releaseGate,
                stage: "desktop_locked",
                canObserveNaturalTask: false,
                foregroundApp: "loginwindow",
                requiredAction:
                    "Unlock the desktop, then complete one ordinary direct-text task in an observed writing surface.",
                reasons: ["the frontmost system surface is loginwindow"],
            },
        });

        render(<ShadowAgentDebugSettings />);

        fireEvent.click(
            await screen.findByRole("button", {
                name: "Review background learning",
            }),
        );

        await waitFor(() =>
            expect(
                screen.getByRole("tab", { name: "Background learning" }),
            ).toHaveAttribute("aria-selected", "true"),
        );
    });

    it("does not present a failed summary request as an empty history", async () => {
        mockGetSnapshot.mockResolvedValueOnce(null);
        render(<ShadowAgentDebugSettings />);

        selectShadowView("Task timeline", "Private practice");

        expect(
            await screen.findByText(/returned no summary data/),
        ).toBeInTheDocument();
        expect(
            screen.getByText(/Shadow learning summary is unavailable\. Refresh/),
        ).toBeInTheDocument();
        expect(screen.queryByText("shadowDebug.emptyRuns")).not.toBeInTheDocument();
        expect(
            screen.getByPlaceholderText("shadowDebug.search"),
        ).toBeDisabled();
    });

    it("opens and closes every Shadow Agent detail view at constrained widths", async () => {
        render(<ShadowAgentDebugSettings />);

        const views = [
            {
                group: "Task timeline" as const,
                tab: "Private practice",
                list: "Private practice list",
                detail: "Private practice details",
            },
            {
                group: "Learning quality" as const,
                tab: "Prediction accuracy",
                list: "Prediction accuracy list",
                detail: "Prediction accuracy details",
            },
            {
                group: "Learning quality" as const,
                tab: "Suggestion readiness",
                list: "Suggestion readiness list",
                detail: "Suggestion readiness details",
            },
            {
                group: "Learning quality" as const,
                tab: "Supported insights",
                list: "Supported insights list",
                detail: "Supported insights details",
            },
            {
                group: "Task timeline" as const,
                tab: "Comparisons",
                list: "Comparisons list",
                detail: "Comparisons details",
            },
        ];

        for (const view of views) {
            selectShadowView(view.group, view.tab);
            const list = await screen.findByRole("region", {
                name: view.list,
            });
            const detailPanel = screen.getByRole("region", {
                name: view.detail,
            });
            const [row] = await within(list).findAllByRole("button");

            expect(list).toHaveClass("block");
            expect(detailPanel).toHaveClass("hidden", "xl:block");

            fireEvent.click(row);

            expect(row).toHaveAttribute("aria-current", "true");
            expect(list).toHaveClass("hidden", "xl:block");
            expect(detailPanel).toHaveClass("block");
            expect(
                within(detailPanel).getByRole("button", {
                    name: "Back to list",
                }),
            ).toBeInTheDocument();

            fireEvent.keyDown(detailPanel, { key: "Escape" });

            expect(list).toHaveClass("block");
            expect(detailPanel).toHaveClass("hidden", "xl:block");
        }
    });

    it("opens and returns from Task Episode details", async () => {
        render(<ShadowAgentDebugSettings />);

        selectShadowView("Task timeline", "Observed tasks");
        const list = await screen.findByRole("region", {
            name: "Observed tasks",
        });
        const detailPanel = screen.getByRole("region", {
            name: "Observed task details",
        });
        const [row] = await within(list).findAllByRole("button");

        fireEvent.click(row);

        expect(list).toHaveClass("hidden", "xl:block");
        expect(detailPanel).toHaveClass("block");
        fireEvent.click(
            within(detailPanel).getByRole("button", {
                name: "Back to list",
            }),
        );
        expect(list).toHaveClass("block");
        expect(detailPanel).toHaveClass("hidden", "xl:block");
    });

    it("exposes materialized pair evidence in Task Episode details", async () => {
        render(<ShadowAgentDebugSettings />);

        selectShadowView("Task timeline", "Observed tasks");
        const list = await screen.findByRole("region", {
            name: "Observed tasks",
        });
        const [row] = await within(list).findAllByRole("button");
        fireEvent.click(row);

        const detailPanel = screen.getByRole("region", {
            name: "Observed task details",
        });
        expect(
            await within(detailPanel).findByText("Isoform's private result"),
        ).toBeInTheDocument();
        expect(
            within(detailPanel).getByText("Can Isoform try this task?"),
        ).toBeInTheDocument();
        expect(within(detailPanel).getByText("Eligible")).toBeInTheDocument();
        expect(
            within(detailPanel).getAllByText("compose.text").length,
        ).toBeGreaterThan(0);
        expect(
            within(detailPanel).getByText("compose.text, analyze.text"),
        ).toBeInTheDocument();
        expect(within(detailPanel).getByText("Both results are ready")).toBeInTheDocument();
        expect(
            within(detailPanel).getAllByText("materialized"),
        ).toHaveLength(2);
        expect(
            within(detailPanel).getByText("user-result-digest"),
        ).toBeInTheDocument();
        expect(
            within(detailPanel).getAllByText("shadow-result-digest").length,
        ).toBeGreaterThan(0);
    });

    it("opens the Self Dream debugger from the shared Shadow Agent surface", async () => {
        render(<ShadowAgentDebugSettings />);

        selectShadowView("Learning quality", "Profile suggestions");

        await waitFor(() =>
            expect(mockGetSelfDreamSnapshot).toHaveBeenCalledWith(200),
        );
        expect(
            screen.getByPlaceholderText(
                "Search status, model, or input hash",
            ),
        ).toBeInTheDocument();
        expect(
            screen.getByText("No possible profile update matches this search."),
        ).toBeInTheDocument();
    });

    it("opens the read-only production observer debugger from the shared surface", async () => {
        render(<ShadowAgentDebugSettings />);

        selectShadowView("Task timeline", "Task detection");

        await waitFor(() =>
            expect(mockGetObserverSnapshot).toHaveBeenCalledWith(200),
        );
        expect(
            screen.getByPlaceholderText("Search activity, decision, state, or ID"),
        ).toBeInTheDocument();
        expect(
            screen.getByText("Isoform has not checked any retained activity for tasks yet."),
        ).toBeInTheDocument();
    });

    it("drills from ShadowRuns into immutable evidence and correlation attempts", async () => {
        render(<ShadowAgentDebugSettings />);

        selectShadowView("Task timeline", "Private practice");
        expect(await screen.findAllByText("customer.follow_up.draft")).not.toHaveLength(0);
        await waitFor(() =>
            expect(mockGetRun).toHaveBeenCalledWith("shadow-run-1"),
        );
        expect(await screen.findByText("Frozen production prompt")).toBeInTheDocument();
        expect(screen.getByText(/Observed action: Drafted the customer follow-up/)).toBeInTheDocument();
        expect(screen.getByText("shadowDebug.runtimeBoundary")).toBeInTheDocument();
        expect(screen.getByText("filesystem.write")).toBeInTheDocument();
        expect(screen.getByText(/production state was unchanged/)).toBeInTheDocument();

        selectShadowView("Learning quality", "Prediction accuracy");
        expect(await screen.findByText("shadowDebug.profileBoundary")).toBeInTheDocument();
        const coverageGrid = screen.getByText("Usable comparisons").parentElement
            ?.parentElement;
        expect(coverageGrid).toHaveClass(
            "grid-cols-2",
            "sm:grid-cols-3",
            "lg:grid-cols-6",
        );
        expect(screen.getByText(/reasoning unknown/)).toBeInTheDocument();
        expect(screen.getAllByText("Accuracy target met")).not.toHaveLength(0);

        selectShadowView("Learning quality", "Suggestion readiness");
        expect(await screen.findByText("shadowDebug.qualificationBoundary")).toBeInTheDocument();
        expect(screen.getAllByText("filesystem.write")).not.toHaveLength(0);

        selectShadowView("Learning quality", "Supported insights");
        expect(await screen.findByText("shadowDebug.selfModelBoundary")).toBeInTheDocument();
        expect(screen.getAllByText("Draft the customer follow-up after review.")).not.toHaveLength(0);

        selectShadowView("Task timeline", "Comparisons");

        await waitFor(() => {
            expect(screen.getAllByText("Results compared")).toHaveLength(2);
        });
        expect(document.body).toHaveTextContent("shadow-comparator-v1");
        expect(screen.getByText("selected")).toBeInTheDocument();
        expect(mockEventsOn).toHaveBeenCalledWith("shadow_agent:attempt", expect.any(Function));
        expect(mockEventsOn).toHaveBeenCalledWith("shadow_agent:profile", expect.any(Function));
        expect(mockEventsOn).toHaveBeenCalledWith("shadow_agent:qualification", expect.any(Function));
        expect(mockEventsOn).toHaveBeenCalledWith("self_model:updated", expect.any(Function));
        expect(mockEventsOn).toHaveBeenCalledWith("shadow_agent:runtime", expect.any(Function));
        expect(mockEventsOn).toHaveBeenCalledWith("activity_pipeline:run", expect.any(Function));
    });

    it("shows the read-only task onset, context, model, and learning ledger", async () => {
        render(<ShadowAgentDebugSettings />);

        selectShadowView("Task timeline", "Observed tasks");
        await waitFor(() =>
            expect(mockGetTaskSnapshot).toHaveBeenCalledWith(200),
        );
        await waitFor(() =>
            expect(mockGetTaskDetail).toHaveBeenCalledWith(
                "task-episode-1",
                "",
                200,
            ),
        );

        expect(
            await screen.findAllByText("Prepare the customer follow-up"),
        ).not.toHaveLength(0);
        expect(screen.getByText("Can Isoform observe your next task?")).toBeInTheDocument();
        expect(screen.getByText("What still needs to happen?")).toBeInTheDocument();
        const continuationSection = screen
            .getByText("What still needs to happen?")
            .closest("section");
        expect(continuationSection).not.toBeNull();
        const continuation = within(continuationSection!);
        expect(continuation.getByText("task-shadow-continuation-v2")).not.toBeVisible();
        expect(continuation.getByText("evaluate_pair")).not.toBeVisible();
        fireEvent.click(continuation.getAllByText("Technical details")[0]);
        expect(continuation.getByText("task-shadow-continuation-v2")).toBeVisible();
        const continuationReason = continuation.getByText(
            "current terminal pair is ready for comparison",
        );
        const continuationItemDetails = continuationReason.closest("details");
        expect(continuationItemDetails).not.toBeNull();
        fireEvent.click(
            within(continuationItemDetails!).getByText("Technical details"),
        );
        expect(within(continuationItemDetails!).getByText("evaluate_pair")).toBeVisible();
        expect(screen.getByText("Observation: On")).toBeInTheDocument();
        expect(screen.getByText("Task type: Writing")).toBeInTheDocument();
        expect(screen.getByText("Past practice runs: 0")).toBeInTheDocument();
        const releaseGateSection = screen
            .getByText("Can Isoform observe your next task?")
            .closest("section");
        expect(releaseGateSection).not.toBeNull();
        const releaseGate = within(releaseGateSection!);
        expect(releaseGate.getByText("Prompt: shadow-task-v7")).not.toBeVisible();
        expect(releaseGate.getByText("Task code: compose.text")).not.toBeVisible();
        expect(releaseGate.getByText("Current app: Mail")).not.toBeVisible();
        fireEvent.click(releaseGate.getAllByText("Technical details")[0]);
        expect(releaseGate.getByText("Prompt: shadow-task-v7")).toBeVisible();
        expect(releaseGate.getByText("Task code: compose.text")).toBeVisible();
        expect(releaseGate.getByText("Current app: Mail")).toBeVisible();
        expect(screen.getAllByText("Real activity source found").length).toBeGreaterThan(0);
        expect(screen.getAllByText("Task start verified").length).toBeGreaterThan(0);
        expect(screen.getByText("Activity source")).toBeInTheDocument();
        expect(
            screen.getByText(
                "This task is linked to real observed activity and a verified task start.",
            ),
        ).toBeInTheDocument();
        expect(screen.getByText("source 91")).toBeInTheDocument();
        expect(screen.getAllByText("Both results are ready")).not.toHaveLength(0);
        expect(
            screen.getAllByText("Your result source verified").length,
        ).toBeGreaterThan(0);
        expect(screen.getAllByText("destination-digest")).not.toHaveLength(0);
        expect(
            screen.getAllByText("Results compared across 8 dimensions").length,
        ).toBeGreaterThan(0);
        expect(screen.getByText("Onset and frozen context")).toBeInTheDocument();
        expect(screen.getByText("3 included · 4 excluded")).toBeInTheDocument();
        expect(
            screen.getByText("Showing 2 of 7 entries; included evidence is shown first."),
        ).toBeInTheDocument();
        expect(screen.getByText("activity:after-onset")).toBeInTheDocument();
        expect(screen.getByText("after_onset_cutoff")).toBeInTheDocument();
        expect(screen.getByText("Exact task mirror prompt")).toBeInTheDocument();
        expect(screen.getByText(/"action": "draft"/)).toBeInTheDocument();
        expect(
            screen.queryByRole("button", { name: /approve|execute|allow/i }),
        ).not.toBeInTheDocument();
        expect(mockEventsOn).toHaveBeenCalledWith(
            "shadow_agent:task_pipeline",
            expect.any(Function),
        );
        expect(mockEventsOn).toHaveBeenCalledWith(
            "shadow_agent:task_evaluated",
            expect.any(Function),
        );
        expect(mockEventsOn).toHaveBeenCalledWith(
            "shadow_agent:task_continuation",
            expect.any(Function),
        );

        fireEvent.click(
            screen.getByRole("button", { name: "See how this task was detected" }),
        );
        await waitFor(() =>
            expect(mockGetObserverDetail).toHaveBeenCalledWith(
                "observer-trace-1",
            ),
        );
        expect(screen.getAllByText("Task detection")).not.toHaveLength(0);
    });

    it("filters task episodes by production pair state", async () => {
        mockGetTaskSnapshot.mockResolvedValue({
            ...taskSnapshot,
            episodes: [
                ...taskSnapshot.episodes,
                {
                    ...taskSnapshot.episodes[0],
                    id: "task-episode-outside-rollout",
                    taskHypothesis: "Inspect a video from watch history",
                    desiredResultHypothesis: "The selected video is playing.",
                    linkedShadowRunIds: [],
                    linkedShadowRunCount: 0,
                    latestPairWorkflow:
                        "inspect.a.video.from.watch.history",
                    latestPairState: "outside_production_rollout",
                    latestPairReasons: [
                        "workflow is not enabled for production shadow execution",
                    ],
                },
            ],
        } satisfies TaskShadowDebugSnapshot);

        render(<ShadowAgentDebugSettings />);
        selectShadowView("Task timeline", "Observed tasks");

        await screen.findByRole("option", {
            name: "Not available with the current method",
        });
        fireEvent.change(
            screen.getByRole("combobox", {
                name: "Filter task status",
            }),
            { target: { value: "outside_production_rollout" } },
        );

        const list = screen.getByRole("region", {
            name: "Observed tasks",
        });
        expect(
            within(list).getByText("Inspect a video from watch history"),
        ).toBeInTheDocument();
        expect(
            within(list).queryByText("Prepare the customer follow-up"),
        ).not.toBeInTheDocument();
    });
});
