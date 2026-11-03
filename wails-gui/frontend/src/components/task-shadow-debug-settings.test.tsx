import {
    fireEvent,
    render,
    screen,
    waitFor,
    within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type {
    TaskShadowDebugDetail,
    TaskShadowDebugSnapshot,
    TaskShadowQualityCampaign,
} from "@/lib/wails-shim-app";
import { TaskShadowDebugSettings } from "./task-shadow-debug-settings";

const mockGetSnapshot = vi.fn();
const mockGetDetail = vi.fn();
const mockRunCampaign = vi.fn();

vi.mock("@/lib/wails-shim-app", async (importOriginal) => {
    const original = await importOriginal<typeof import("@/lib/wails-shim-app")>();
    return {
        ...original,
        GetTaskShadowDebugSnapshot: (...args: unknown[]) =>
            mockGetSnapshot(...args),
        GetTaskShadowDebugDetail: (...args: unknown[]) =>
            mockGetDetail(...args),
        RunTaskShadowQualityCampaign: (...args: unknown[]) =>
            mockRunCampaign(...args),
    };
});

vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: () => vi.fn(),
}));

vi.mock("sonner", () => ({
    toast: {
        success: vi.fn(),
        error: vi.fn(),
    },
}));

const qualityCampaign: TaskShadowQualityCampaign = {
    version: "task-shadow-quality-campaign-v1",
    capabilityKey: "compose.text",
    promptVersion: "shadow-task-v5",
    stage: "ready_to_process",
    complete: false,
    existingEvidenceExhausted: false,
    requiredAction: "Process one durable pair.",
    counts: {
        detectedEpisodes: 2,
        eligibleEpisodes: 2,
        ineligibleEpisodes: 0,
        shadowRuns: 2,
        currentPromptRuns: 2,
        historicalRuns: 0,
        completedShadowAttempts: 2,
        independentResults: 2,
        observedResults: 1,
        awaitingNaturalOutcomes: 1,
        pairReady: 1,
        evaluated: 0,
        currentPromptEvaluations: 0,
        historicalEvaluations: 1,
        safeEvaluations: 0,
        safetyConcerns: 0,
        shadowFailures: 0,
        terminalWithoutShadowRun: 0,
        processablePairs: 1,
    },
    qualification: {
        decision: "collecting",
        reasons: ["need_qualified_evaluations"],
        observedEvaluationCount: 0,
        qualifiedEvaluationCount: 0,
        observedTaskEpisodeCount: 0,
        observedContextCount: 0,
        requiredEvaluationCount: 5,
        requiredTaskEpisodeCount: 5,
        requiredContextCount: 3,
        requiredSafetyCount: 3,
    },
    pendingExecutionRunIds: [],
    readyPairRunIds: ["shadow-ready"],
    blockers: [{
        taskEpisodeId: "episode-waiting",
        shadowRunId: "shadow-waiting",
        state: "awaiting_user_result",
        reasons: ["task_episode_not_terminal"],
        occurredAt: 1_000,
    }],
    release: {
        version: "task-shadow-capability-release-v1",
        capabilityKey: "compose.text",
        promptVersion: "shadow-task-v5",
        state: "unqualified",
        active: false,
        authorityEffect: "none",
        currentPromptEventCount: 0,
        historicalEventCount: 0,
        events: [],
    },
    generatedAt: 2_000,
};

const snapshot: TaskShadowDebugSnapshot = {
    generatedAt: 2_000,
    limit: 200,
    enabledWorkflows: ["compose.text", "analyze.text"],
    lifecycleHealth: {
        policyVersion: "task-lifecycle-state-bound-lease-v2",
        promptVersion: "task-lifecycle-v7",
        shadowPromptVersion: "shadow-task-v7",
        candidateHorizonMs: 90 * 60 * 1_000,
        activeHorizonMs: 24 * 60 * 60 * 1_000,
        pausedHorizonMs: 72 * 60 * 60 * 1_000,
        evidenceRenews: false,
        activitySourcePolicy: "task-lifecycle-activity-source-v1",
        episodeCount: 68,
        openCount: 4,
        overdueOpenCount: 1,
        materializedResultEpisodeCount: 2,
        statusCounts: {
            candidate: 2,
            active: 2,
            completed: 2,
            expired: 62,
        },
        pairStateCounts: {
            awaiting_user_result: 1,
            observation_expired: 3,
            evaluated: 1,
            repeated_task_start: 2,
        },
        duplicateOnsetClusterCount: 1,
        duplicateOnsetEpisodeCount: 2,
        duplicateOnsetClusters: [
            {
                sourceKind: "event",
                sourceId: "shared-keyboard-event",
                evidenceRef: "event:shared-task-start",
                observedAt: 1_000,
                inferredStartAt: 1_000,
                episodeIds: [
                    "duplicate-episode-alpha",
                    "duplicate-episode-beta",
                ],
            },
        ],
        duplicateOnsetClustersTruncated: false,
        latestAttempt: {
            id: "lifecycle-attempt",
            status: "succeeded",
            model: "gpt-5.6-terra",
            promptVersion: "task-lifecycle-v7",
            startedAt: 1_900,
            completedAt: 2_000,
        },
        generatedAt: 2_000,
    },
    continuation: {
        version: "task-shadow-continuation-v2",
        generatedAt: 2_000,
        scannedRunCount: 3,
        currentRunCount: 2,
        executionPendingCount: 0,
        evaluationPendingCount: 1,
        learningPendingCount: 0,
        materializationPendingCount: 0,
        awaitingTerminalCount: 0,
        awaitingResultCount: 0,
        completeCount: 1,
        blockedCount: 0,
        historicalCount: 1,
        firstRunExcludedCount: 0,
        items: [
            {
                taskEpisodeId: "episode-evaluation-pending",
                shadowRunId: "run-evaluation-pending",
                workflow: "compose.text",
                state: "evaluation_pending",
                action: "evaluate_pair",
                reason: "current terminal pair is ready for comparison",
                episodeStatus: "completed",
                attemptId: "attempt-evaluation-pending",
                attemptStatus: "completed",
                pairState: "pair_ready",
                currentEvaluationIds: [],
                createdAt: 1_900,
            },
            {
                taskEpisodeId: "episode-complete",
                shadowRunId: "run-complete",
                workflow: "analyze.text",
                state: "complete",
                action: "none",
                reason: "current comparison and learning are complete",
                episodeStatus: "completed",
                attemptId: "attempt-complete",
                attemptStatus: "completed",
                pairState: "evaluated",
                currentEvaluationIds: ["evaluation-complete"],
                learningRunId: "learning-complete",
                learningMaterialization: {
                    version: "shadow-task-learning-materialization-v1",
                    runId: "learning-complete",
                    complete: true,
                    candidateCount: 1,
                    terminalCount: 1,
                    materializedCount: 1,
                    skippedCount: 0,
                    deniedCount: 0,
                    errorCount: 0,
                    invalidCount: 0,
                    missingCount: 0,
                    pendingRevisionIds: [],
                    errorRevisionIds: [],
                    terminalRevisionIds: ["learning-revision-complete"],
                },
                createdAt: 1_800,
            },
        ],
    },
    qualityCampaign,
    episodes: [],
};

function reasoningEpisode(id: string, taskHypothesis: string) {
    return {
        id,
        status: "completed",
        taskHypothesis,
        desiredResultHypothesis: "A source-backed reasoning comparison",
        taskConfidence: 0.88,
        boundaryConfidence: 0.74,
        desiredResultConfidence: 0.83,
        inferredStartAt: 1_000,
        detectedAt: 1_010,
        contextSnapshotId: `${id}:context`,
        contextHash: "a".repeat(64),
        contextIncludedCount: 1,
        contextExcludedCount: 0,
        linkedShadowRunIds: [`${id}:run`],
        linkedShadowRunCount: 1,
        taskAgentAttemptCount: 3,
        persistedEvaluationCount: 1,
        latestPairWorkflow: "compose.text",
        latestPairState: "evaluated",
        latestPairReasons: [],
        createdAt: 1_010,
    };
}

const reasoningDetail: TaskShadowDebugDetail = {
    generatedAt: 2_000,
    limit: 200,
    productionRollout: {
        workflow: "compose.text",
        enabledWorkflows: ["compose.text", "analyze.text"],
        state: "eligible",
        eligible: true,
        reason: "The task is eligible for isolated text composition.",
    },
    episode: {
        episode: {
            id: "reasoning-episode",
            inferredStartAt: 1_000,
            detectedAt: 1_010,
            currentStatus: "completed",
            observationLease: {
                policyVersion: "task-lifecycle-state-bound-lease-v2",
                status: "completed",
                open: false,
                anchorKind: "state_transition",
                anchorTransitionId: "reasoning-completed-transition",
                anchorAt: 1_700,
                horizonMs: 0,
                deadlineAt: 0,
                due: false,
                evidenceRenews: false,
            },
            createPayloadHash: "b".repeat(64),
            frozenContext: {
                id: "reasoning-context",
                taskEpisodeId: "reasoning-episode",
                cutoffAt: 1_000,
                sha256: "a".repeat(64),
                manifest: [],
                manifestEntryCount: 0,
                manifestIncludedCount: 0,
                manifestExcludedCount: 0,
                manifestTruncated: false,
                canonicalJsonRedacted: true,
                createdAt: 1_000,
            },
            latestHypothesis: {
                taskHypothesis: "Trace reasoning parity",
                desiredResultHypothesis: "A source-backed reasoning comparison",
                taskConfidence: 0.88,
                boundaryConfidence: 0.74,
                desiredResultConfidence: 0.83,
                revision: 1,
            },
            createdAt: 1_010,
        },
        hypotheses: [],
        evidence: [],
        resultDestinations: [{
            status: "current_valid",
            validationReason: "content, workflow, destination, and digests verified",
            current: true,
            valid: true,
            workflow: "compose.text",
            resultSha256: "c".repeat(64),
            destinationSha256: "d".repeat(64),
            version: "task-result-destination-v1",
            appBundleId: "com.apple.mail",
            appName: "Mail",
            windowTitle: "Customer update",
            semanticRole: "user_result_candidate",
            captureMethod: "ax_field_diff",
            evidenceIds: ["result-a", "result-b"],
            sourceEvidenceRefs: ["suffix:text"],
            latestObservedAt: 1_600,
        }],
        transitions: [],
        links: [],
    },
    runs: [],
    reasoning: {
        version: "task-reasoning-debug-v2",
        versions: {
            current: {
                onsetPrompt: "task-onset-v5",
                lifecyclePrompt: "task-lifecycle-v7",
                shadowPrompt: "shadow-task-v7",
                resultDestination: "task-result-destination-v1",
                evaluationInput: "shadow-task-evaluation-input-v4",
                evaluationOutput: "shadow-task-evaluation-output-v4",
                comparator: "shadow-task-comparator-v4",
                evaluator: "shadow-task-evaluator-v4",
            },
            onsetPrompts: [{ version: "task-onset-v5", count: 1, current: true }],
            lifecyclePrompts: [{ version: "task-lifecycle-v7", count: 2, current: true }],
            shadowPrompts: [{ version: "shadow-task-v7", count: 1, current: true }],
            evaluationInputs: [{ version: "shadow-task-evaluation-input-v4", count: 1, current: true }],
            evaluationOutputs: [{ version: "shadow-task-evaluation-output-v4", count: 1, current: true }],
            comparators: [{ version: "shadow-task-comparator-v4", count: 1, current: true }],
            evaluators: [{ version: "shadow-task-evaluator-v4", count: 1, current: true }],
        },
        onsetHypotheses: [{
            statement: "The user may prioritize customer certainty.",
            confidence: 0.49,
            basis: "contextual_inference",
            evidenceRefs: ["onset-evidence"],
            profileRefs: [],
            contradictionRefs: [],
            quoteValidation: "not_applicable",
            provenanceResolved: true,
            validationReason: "frozen provenance resolves",
        }],
        predictions: [{
            shadowRunId: "reasoning-run",
            promptVersion: "shadow-task-v7",
            current: true,
            hypotheses: [],
        }],
        observed: [{
            evidenceId: "reasoning-evidence",
            kind: "correction",
            version: "task-lifecycle-v7",
            sourceKind: "task_lifecycle_reasoning",
            sourceId: "correction-source",
            sourceQuote: "Actually, I changed direction because the customer needs certainty.",
            evidenceRefs: ["suffix:user-correction"],
            observedAt: 1_500,
            valid: true,
            validationReason: "literal user-authored lifecycle evidence validated at capture",
        }],
        comparisons: [{
            evaluationId: "reasoning-evaluation",
            shadowRunId: "reasoning-run",
            comparatorVersion: "shadow-task-comparator-v4",
            inputVersion: "shadow-task-evaluation-input-v4",
            outputVersion: "shadow-task-evaluation-output-v4",
            evaluatorVersion: "shadow-task-evaluator-v4",
            evaluatorModel: "gpt-5.6-terra",
            current: true,
            predictionBound: true,
            predictionBindingReason: "comparator input matches the frozen Shadow prediction",
            predictedHypotheses: [{
                statement: "The user may prioritize customer certainty.",
                confidence: 0.49,
                basis: "contextual_inference",
                evidenceRefs: ["observed:onset-evidence"],
                profileRefs: [],
                contradictionRefs: [],
                quoteValidation: "not_applicable",
                provenanceResolved: true,
                validationReason: "frozen provenance resolves",
            }],
            supportedEvidenceRefs: ["observed:reasoning-evidence"],
            assessment: "matched",
            score: 0.88,
            confidence: 0.9,
            reason: "The source-backed correction supports the comparison.",
            evidenceRefs: ["observed:reasoning-evidence"],
            citationsValid: true,
            unknown: false,
        }],
        counts: {
            onsetHypotheses: 1,
            shadowHypotheses: 1,
            observedReasoning: 1,
            invalidObservedReasoning: 0,
            comparisons: 1,
            knownComparisons: 1,
            unknownComparisons: 0,
        },
        coverage: "supported",
    },
    taskAgentAttempts: [],
    learning: { runs: [], revisions: [], materializations: [] },
    correlations: {},
    exclusions: [],
};

describe("TaskShadowDebugSettings quality campaign", () => {
    beforeEach(() => {
        vi.clearAllMocks();
        mockGetSnapshot.mockResolvedValue(snapshot);
        mockRunCampaign.mockResolvedValue({
            version: qualityCampaign.version,
            capabilityKey: qualityCampaign.capabilityKey,
            inspectedRunIds: ["shadow-ready"],
            executedRunIds: [],
            evaluatedRunIds: ["shadow-ready"],
            existingEvaluationIds: [],
            deferredRunIds: [],
            errors: [],
            releaseEventAdded: false,
            startedAt: 2_000,
            completedAt: 3_000,
            campaign: {
                ...qualityCampaign,
                stage: "collecting_natural_outcomes",
                existingEvidenceExhausted: true,
                counts: {
                    ...qualityCampaign.counts,
                    pairReady: 0,
                    evaluated: 1,
                    processablePairs: 0,
                },
            },
        });
    });

    it("shows source-bound parity gaps without offering repair or execution", async () => {
        mockGetSnapshot.mockResolvedValue({
            ...snapshot,
            releaseGate: {
                version: "task-shadow-release-gate-v7",
                stage: "observer_source_pending",
                complete: false,
                canObserveNaturalTask: true,
                monitoring: true,
                foregroundApp: "Mail",
                latestTaskEpisodeId: "legacy-episode",
                requiredShadowPrompt: "shadow-task-v7",
                historicalShadowRunIds: [],
                observerProvenance: {
                    version: "task-shadow-release-observer-provenance-v2",
                    state: "source_not_covered",
                    reason: "the production source-to-observer link is not exactly covered",
                    sourceComplete: false,
                    originComplete: false,
                    claimTraceCount: 0,
                    candidateTraceCount: 0,
                    candidateDecisionCount: 0,
                    integrityValid: false,
                },
                learningResolution: {
                    version: "shadow-task-learning-materialization-v1",
                    runId: "learning-pending",
                    complete: false,
                    candidateCount: 1,
                    terminalCount: 0,
                    materializedCount: 0,
                    skippedCount: 0,
                    deniedCount: 0,
                    errorCount: 1,
                    invalidCount: 0,
                    missingCount: 0,
                    pendingRevisionIds: ["learning-revision-pending"],
                    errorRevisionIds: ["learning-revision-pending"],
                    terminalRevisionIds: [],
                },
                requiredAction: "Continue working normally.",
                reasons: ["source provenance is incomplete"],
                steps: [
                    ["observer_source", "Real activity source found"],
                    ["observer_origin", "Task start verified"],
                    ["task_episode", "Your task completed"],
                    ["shadow_attempt", "Isoform's shadow attempt completed"],
                    ["user_result", "Your result captured"],
                    ["result_destination", "Your result source verified"],
                    ["shadow_result", "Isoform's result captured"],
                    ["evaluation", "Results compared across 8 dimensions"],
                    ["learning", "Safe learning update recorded"],
                ].map(([key, label]) => ({ key, label, complete: false })),
            },
        } satisfies TaskShadowDebugSnapshot);

        render(<TaskShadowDebugSettings />);

        expect(await screen.findByText("Can Isoform observe your next task?")).toBeInTheDocument();
        expect(screen.getByText("Task source not verified")).toBeInTheDocument();
        const technicalReason = screen.getByText(
            "the production source-to-observer link is not exactly covered",
        );
        expect(technicalReason).not.toBeVisible();
        fireEvent.click(screen.getByText("Technical source details"));
        expect(technicalReason).toBeVisible();
        for (const label of [
            "Real activity source found",
            "Task start verified",
            "Your task completed",
            "Isoform's shadow attempt completed",
            "Your result captured",
            "Your result source verified",
            "Isoform's result captured",
            "Results compared across 8 dimensions",
            "Safe learning update recorded",
        ]) {
            expect(screen.getByText(label)).toBeInTheDocument();
        }
        expect(
            screen.queryByRole("button", { name: /open trace|repair|replay|execute/i }),
        ).not.toBeInTheDocument();
        expect(screen.getByText("Supported insights")).toBeInTheDocument();
        expect(screen.getByText(/0\/1 terminal/)).toBeInTheDocument();
    });

    it("explains a locked desktop without exposing system or gate language", async () => {
        const onOpenBackgroundLearning = vi.fn();
        mockGetSnapshot.mockResolvedValue({
            ...snapshot,
            releaseGate: {
                version: "task-shadow-release-gate-v7",
                stage: "desktop_locked",
                complete: false,
                canObserveNaturalTask: false,
                monitoring: true,
                foregroundApp: "loginwindow",
                selectedWorkflow: "compose.text",
                requiredShadowPrompt: "shadow-task-v7",
                selectedShadowPrompt: "shadow-task-v7",
                historicalShadowRunIds: ["historical-run-1"],
                requiredAction:
                    "Unlock the desktop, then complete one ordinary direct-text task in an observed writing surface.",
                reasons: ["the frontmost system surface is loginwindow"],
                steps: [],
            },
        } satisfies TaskShadowDebugSnapshot);

        render(
            <TaskShadowDebugSettings
                onOpenBackgroundLearning={onOpenBackgroundLearning}
            />,
        );

        const heading = await screen.findByText(
            "Can Isoform observe your next task?",
        );
        const gateSection = heading.closest("section");
        expect(gateSection).not.toBeNull();
        const gate = within(gateSection!);

        expect(gate.getByText("Waiting for desktop unlock")).toBeInTheDocument();
        expect(gate.getByText("Observation: On")).toBeInTheDocument();
        expect(gate.getByText("Task type: Writing")).toBeInTheDocument();
        expect(gate.getByText("Past practice runs: 1")).toBeInTheDocument();
        expect(
            gate.getByText(/Background learning may continue separately/),
        ).toBeInTheDocument();

        const rawApp = gate.getAllByText(/loginwindow/)[0];
        const rawInstruction = gate.getByText(/ordinary direct-text task/);
        expect(rawApp).not.toBeVisible();
        expect(rawInstruction).not.toBeVisible();

        fireEvent.click(gate.getAllByText("Technical details")[0]);
        expect(rawApp).toBeVisible();
        expect(rawInstruction).toBeVisible();
        expect(gate.getByText("Stage: desktop_locked")).toBeVisible();
        expect(gate.getByText("Task code: compose.text")).toBeVisible();

        fireEvent.click(
            gate.getByRole("button", { name: "Review background learning" }),
        );
        expect(onOpenBackgroundLearning).toHaveBeenCalledTimes(1);
    });

    it("explains paused observation without exposing the backend instruction", async () => {
        mockGetSnapshot.mockResolvedValue({
            ...snapshot,
            releaseGate: {
                version: "task-shadow-release-gate-v7",
                stage: "observer_paused",
                complete: false,
                canObserveNaturalTask: false,
                monitoring: false,
                foregroundApp: "",
                requiredShadowPrompt: "shadow-task-v7",
                historicalShadowRunIds: [],
                requiredAction:
                    "Enable observation before starting the direct-text task.",
                reasons: ["activity observation is not running"],
                steps: [],
            },
        } satisfies TaskShadowDebugSnapshot);

        render(<TaskShadowDebugSettings />);

        const gateSection = (await screen.findByText(
            "Can Isoform observe your next task?",
        )).closest("section");
        expect(gateSection).not.toBeNull();
        const gate = within(gateSection!);

        expect(gate.getByText("Observation is paused")).toBeInTheDocument();
        expect(
            gate.getByText(/Turn on observation before your next writing/),
        ).toBeInTheDocument();
        const rawInstruction = gate.getByText(/starting the direct-text task/);
        expect(rawInstruction).not.toBeVisible();
        expect(
            gate.queryByRole("button", { name: "Review background learning" }),
        ).not.toBeInTheDocument();

        fireEvent.click(gate.getAllByText("Technical details")[0]);
        expect(rawInstruction).toBeVisible();
        expect(gate.getByText("Stage: observer_paused")).toBeVisible();
    });

    it("keeps a future gate instruction readable and its stage inspectable", async () => {
        mockGetSnapshot.mockResolvedValue({
            ...snapshot,
            releaseGate: {
                version: "task-shadow-release-gate-v7",
                stage: "future_gate_state",
                complete: false,
                canObserveNaturalTask: true,
                monitoring: true,
                foregroundApp: "Mail",
                selectedWorkflow: "future.workflow",
                requiredShadowPrompt: "shadow-task-v7",
                historicalShadowRunIds: [],
                requiredAction: "Keep working normally while this check finishes.",
                reasons: [],
                steps: [],
            },
        } satisfies TaskShadowDebugSnapshot);

        render(<TaskShadowDebugSettings />);

        const gateSection = (await screen.findByText(
            "Can Isoform observe your next task?",
        )).closest("section");
        expect(gateSection).not.toBeNull();
        const gate = within(gateSection!);

        expect(
            gate.getByText("Keep working normally while this check finishes."),
        ).toBeInTheDocument();
        expect(gate.getByText("Task type: Future workflow")).toBeInTheDocument();
        expect(gate.getByText("Stage: future_gate_state")).not.toBeVisible();
        fireEvent.click(gate.getAllByText("Technical details")[0]);
        expect(gate.getByText("Stage: future_gate_state")).toBeVisible();
    });

    it("processes only evidence reported ready by the backend", async () => {
        render(<TaskShadowDebugSettings />);

        expect(
            await screen.findByText("Can Isoform reliably match your writing?"),
        ).toBeInTheDocument();
        expect(screen.getByText("1 waiting for your result")).toBeInTheDocument();
        expect(screen.getByText("task understanding")).toBeInTheDocument();
        expect(
            screen.getByText("comparisons usable for learning"),
        ).toBeInTheDocument();
        expect(screen.getByText("Did observed tasks finish?")).toBeInTheDocument();
        expect(screen.getByText("What still needs to happen?")).toBeInTheDocument();
        expect(
            screen.getByText(
                "Your result and Isoform's private result are ready to compare.",
            ),
        ).toBeVisible();

        const continuationSection = screen
            .getByText("What still needs to happen?")
            .closest("section");
        expect(continuationSection).not.toBeNull();
        const continuation = within(continuationSection!);
        expect(continuation.getByText("task-shadow-continuation-v2")).not.toBeVisible();
        expect(continuation.getByText("evaluate_pair")).not.toBeVisible();
        expect(continuation.getByText("run-evaluation-pending")).not.toBeVisible();
        expect(continuation.getByText("learning-complete")).not.toBeVisible();

        fireEvent.click(continuation.getAllByText("Technical details")[0]);
        expect(continuation.getByText("task-shadow-continuation-v2")).toBeVisible();

        const rawReason = continuation.getByText(
            "current terminal pair is ready for comparison",
        );
        const continuationItemDetails = rawReason.closest("details");
        expect(continuationItemDetails).not.toBeNull();
        fireEvent.click(
            within(continuationItemDetails!).getByText("Technical details"),
        );
        expect(rawReason).toBeVisible();
        expect(within(continuationItemDetails!).getByText("evaluate_pair")).toBeVisible();
        expect(
            within(continuationItemDetails!).getByText("run-evaluation-pending"),
        ).toBeVisible();

        expect(screen.getByText(/1\/1 terminal/)).toBeInTheDocument();
        const lifecycleSection = screen
            .getByText("Did observed tasks finish?")
            .closest("section");
        expect(lifecycleSection).not.toBeNull();
        const lifecycle = within(lifecycleSection!);
        expect(lifecycle.getByText("Repeated task starts: 1")).toBeVisible();
        expect(lifecycle.getByText(/Not used for learning 2/)).toBeVisible();
        expect(
            lifecycle.getByText(
                "1 past task start was saved more than once across 2 records. Isoform keeps these records for inspection but does not compare them or learn from them. New detections reuse one task record.",
            ),
        ).toBeVisible();
        expect(lifecycle.getByText("gpt-5.6-terra", { exact: false })).not.toBeVisible();
        expect(lifecycle.getByText("possible task window 90m")).not.toBeVisible();
        expect(lifecycle.getByText("task-lifecycle-activity-source-v1", { exact: false })).not.toBeVisible();
        expect(lifecycle.getByText("attached activity extends window: no")).not.toBeVisible();
        expect(lifecycle.getByText("duplicate-episode-alpha", { exact: false })).not.toBeVisible();
        fireEvent.click(lifecycle.getByText("Technical details"));
        expect(lifecycle.getByText("gpt-5.6-terra", { exact: false })).toBeVisible();
        expect(lifecycle.getByText("possible task window 90m")).toBeVisible();
        expect(lifecycle.getByText("task-lifecycle-activity-source-v1", { exact: false })).toBeVisible();
        expect(lifecycle.getByText("attached activity extends window: no")).toBeVisible();
        expect(lifecycle.getByText("duplicate-episode-alpha", { exact: false })).toBeVisible();
        expect(
            screen.getByText(/1 task Isoform noticed is past the result window/),
        ).toBeInTheDocument();

        fireEvent.click(
            screen.getByRole("button", { name: "Compare ready results" }),
        );

        await waitFor(() => expect(mockRunCampaign).toHaveBeenCalledTimes(1));
        await waitFor(() => expect(mockGetSnapshot).toHaveBeenCalledTimes(2));
    });

    it("opens immutable capability release history", async () => {
        mockGetSnapshot.mockResolvedValue({
            ...snapshot,
            qualityCampaign: {
                ...qualityCampaign,
                release: {
                    ...qualityCampaign.release!,
                    state: "qualified",
                    active: true,
                    currentPromptEventCount: 1,
                    latestCurrentPromptEvent: {
                        id: "release-1",
                        eventKey: "release-key-1",
                        version: "task-shadow-capability-release-v1",
                        capabilityKey: "compose.text",
                        promptVersion: "shadow-task-v5",
                        campaignVersion: qualityCampaign.version,
                        learningPolicyVersion: "shadow-task-learning-v2",
                        decision: "qualified",
                        evidenceHash: "a".repeat(64),
                        qualification: qualityCampaign.qualification,
                        evidence: {
                            evaluationIds: ["evaluation-1"],
                            taskEpisodeIds: ["episode-1"],
                            shadowRunIds: ["run-1"],
                            contextHashes: ["context-1"],
                        },
                        authorityEffect: "none",
                        createdAt: 2_000,
                    },
                    events: [{
                        id: "release-1",
                        eventKey: "release-key-1",
                        version: "task-shadow-capability-release-v1",
                        capabilityKey: "compose.text",
                        promptVersion: "shadow-task-v5",
                        campaignVersion: qualityCampaign.version,
                        learningPolicyVersion: "shadow-task-learning-v2",
                        decision: "qualified",
                        evidenceHash: "a".repeat(64),
                        qualification: qualityCampaign.qualification,
                        evidence: {
                            evaluationIds: ["evaluation-1"],
                            taskEpisodeIds: ["episode-1"],
                            shadowRunIds: ["run-1"],
                            contextHashes: ["context-1"],
                        },
                        authorityEffect: "none",
                        createdAt: 2_000,
                    }],
                },
            },
        });

        render(<TaskShadowDebugSettings />);

        expect(await screen.findByText("Reliability decision")).toBeInTheDocument();
        fireEvent.click(
            screen.getByRole("button", { name: "Show reliability history" }),
        );
        expect(screen.getByText("shadow-task-v5")).toBeInTheDocument();
        expect(screen.getByText("1 evaluations · 1 episodes · 1 contexts")).toBeInTheDocument();
    });

    it("opens every episode and exposes the read-only reasoning lineage", async () => {
        mockGetSnapshot.mockResolvedValue({
            ...snapshot,
            reasoningVersions: reasoningDetail.reasoning?.versions.current,
            episodes: [
                reasoningEpisode("reasoning-episode", "Trace reasoning parity"),
                reasoningEpisode("second-episode", "Trace second task"),
            ],
        });
        mockGetDetail.mockResolvedValue(reasoningDetail);

        render(<TaskShadowDebugSettings />);

        expect(await screen.findByText("Reasoning parity")).toBeInTheDocument();
        expect(
            screen.getByText(
                "Actually, I changed direction because the customer needs certainty.",
            ),
        ).toBeInTheDocument();
        expect(screen.getByText(/score 88%/)).toBeInTheDocument();
        expect(screen.getByText("Comparator input · 1 hypothesis")).toBeInTheDocument();
        expect(screen.getByText("Version lineage")).toBeInTheDocument();
        expect(screen.getByText("Result destination provenance")).toBeInTheDocument();
        expect(screen.getByText("Mail / Customer update")).toBeInTheDocument();
        expect(screen.getByText("user_result_candidate")).toBeInTheDocument();
        expect(screen.getByText(/ax_field_diff/)).toBeInTheDocument();
        expect(screen.getByText("result-a, result-b")).toBeInTheDocument();
        const leaseSection = screen
            .getByText("Is this task still current?")
            .closest("section");
        expect(leaseSection).not.toBeNull();
        const lease = within(leaseSection!);
        expect(
            lease.getByText(
                "This task has finished or closed, so it is no longer considered current.",
            ),
        ).toBeVisible();
        expect(
            lease.getByText("task-lifecycle-state-bound-lease-v2", { exact: false }),
        ).not.toBeVisible();
        fireEvent.click(lease.getByText("Technical details"));
        expect(
            lease.getByText("task-lifecycle-state-bound-lease-v2", { exact: false }),
        ).toBeVisible();
        expect(
            lease.getByText(
                "Attached action, reasoning, contradiction, and result evidence extends this window: no.",
            ),
        ).toBeVisible();
        expect(
            screen.getAllByText("compose.text, analyze.text").length,
        ).toBeGreaterThan(0);
        expect(
            screen.getByText("Tasks Isoform can safely practice:"),
        ).toBeInTheDocument();
        expect(screen.getByText("Writing, Reviewing text")).toBeInTheDocument();

        fireEvent.click(screen.getByText("Trace second task").closest("button")!);
        await waitFor(() =>
            expect(mockGetDetail).toHaveBeenLastCalledWith(
                "second-episode",
                "",
                200,
            ),
        );

        expect(
            screen.queryByRole("button", { name: /approve|promote|label|replay/i }),
        ).not.toBeInTheDocument();
    });

    it("opens historical reasoning details with null collection fields", async () => {
        const reasoning = reasoningDetail.reasoning!;
        const onsetHypothesis = reasoning.onsetHypotheses[0];
        const prediction = reasoning.predictions[0];
        const observed = reasoning.observed[0];
        const comparison = reasoning.comparisons[0];
        mockGetSnapshot.mockResolvedValue({
            ...snapshot,
            episodes: [reasoningEpisode("reasoning-episode", "Trace reasoning parity")],
        });
        mockGetDetail.mockResolvedValue({
            ...reasoningDetail,
            reasoning: {
                ...reasoning,
                versions: {
                    ...reasoning.versions,
                    onsetPrompts: null as unknown as typeof reasoning.versions.onsetPrompts,
                },
                onsetHypotheses: [{
                    ...onsetHypothesis,
                    evidenceRefs: null as unknown as string[],
                    profileRefs: null as unknown as string[],
                    contradictionRefs: null as unknown as string[],
                }],
                predictions: [{
                    ...prediction,
                    hypotheses: null as unknown as typeof prediction.hypotheses,
                }],
                observed: [{
                    ...observed,
                    evidenceRefs: null as unknown as string[],
                }],
                comparisons: [{
                    ...comparison,
                    predictedHypotheses: null as unknown as typeof comparison.predictedHypotheses,
                    supportedEvidenceRefs: null as unknown as string[],
                    evidenceRefs: null as unknown as string[],
                }],
            },
        });

        render(<TaskShadowDebugSettings />);

        expect(await screen.findByText("Reasoning parity")).toBeInTheDocument();
        expect(screen.getByText(/Comparator input/)).toHaveTextContent(
            "Comparator input · 0 hypotheses",
        );
        expect(screen.getByText(/0 frozen hypotheses/)).toBeInTheDocument();
    });

    it("renders unsupported reasoning as unknown rather than a zero score", async () => {
        const comparison = reasoningDetail.reasoning!.comparisons[0];
        mockGetSnapshot.mockResolvedValue({
            ...snapshot,
            episodes: [reasoningEpisode("reasoning-episode", "Trace reasoning parity")],
        });
        mockGetDetail.mockResolvedValue({
            ...reasoningDetail,
            reasoning: {
                ...reasoningDetail.reasoning!,
                comparisons: [{
                    ...comparison,
                    assessment: "unknown",
                    score: null,
                    confidence: 0.1,
                    reason: "No independent user-authored reason was observed.",
                    evidenceRefs: [],
                    unknown: true,
                }],
                counts: {
                    ...reasoningDetail.reasoning!.counts,
                    knownComparisons: 0,
                    unknownComparisons: 1,
                },
                coverage: "unknown",
            },
        });

        render(<TaskShadowDebugSettings />);

        expect(await screen.findByText(/score unknown/)).toBeInTheDocument();
        expect(screen.queryByText(/score 0%/)).not.toBeInTheDocument();
    });

    it("distinguishes an unavailable response from a valid empty result", async () => {
        mockGetSnapshot.mockResolvedValueOnce(null);

        const { unmount } = render(<TaskShadowDebugSettings />);

        expect(
            await screen.findByText(/returned no data/),
        ).toBeInTheDocument();
        expect(
            screen.getAllByText(/Detected-task data is unavailable/)[0],
        ).toBeInTheDocument();
        expect(
            screen.queryByText(/No supported tasks have been detected yet/),
        ).not.toBeInTheDocument();

        unmount();
        mockGetSnapshot.mockResolvedValue({
            ...snapshot,
            episodes: [],
        });
        render(<TaskShadowDebugSettings />);

        expect(
            await screen.findByText(/Isoform has not detected any supported tasks yet/),
        ).toBeInTheDocument();
        expect(
            screen.queryByText(/Detected-task data is unavailable/),
        ).not.toBeInTheDocument();
    });
});
