import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type {
    WorkPatternProfileDebugDetail,
    WorkPatternProfileDebugSnapshot,
} from "@/lib/wails-shim-app";
import { WorkProfileDebugSettings } from "./work-profile-debug-settings";

const mocks = vi.hoisted(() => ({
    getSnapshot: vi.fn(),
    getDetail: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", async (importOriginal) => {
    const original =
        await importOriginal<typeof import("@/lib/wails-shim-app")>();
    return {
        ...original,
        GetWorkPatternProfileDebugSnapshot: mocks.getSnapshot,
        GetWorkPatternProfileDebugDetail: mocks.getDetail,
    };
});

const rollout = {
    influenceEnabled: false,
    ablationRunCount: 1,
    evaluationCount: 1,
    scoredPairCount: 1,
    profileWins: 1,
    neutralWins: 0,
    ties: 0,
    unknown: 0,
    correctionCount: 0,
    integrityErrorCount: 0,
    averageImprovement: 0.2,
    profileWinRate: 1,
    unknownRate: 0,
    correctionRate: 0,
    minimumScoredPairs: 20,
    minimumImprovement: 0.03,
    minimumProfileWinRate: 0.55,
    maximumUnknownRate: 0.2,
    maximumCorrectionRate: 0.1,
    meetsEvidenceGate: false,
    recommendedDecision: "hold",
    reason: "minimum paired outcome evidence has not been reached",
    policyVersion: "work-pattern-ablation-v3",
    collectionAlwaysEnabled: true,
};

const learningProvenance = {
    version: "verified-shadow-learning-source-v1",
    runId: "learning-run-1",
    revisionId: "learning-revision-1",
    materializationReceiptId: "learning-receipt-1",
    targetKind: "capability_profile",
    targetId: "learning-revision-1",
    projectionKey: "learning-projection-1",
    capabilityKey: "review.pull.request",
    learningKind: "capability_evidence",
    evidenceHash: "9".repeat(64),
    availableAt: 1_799_999_000_050,
    authorityEffect: "none" as const,
};

const snapshot: WorkPatternProfileDebugSnapshot = {
    generatedAt: 1_800_000_000_000,
    limit: 200,
    profiles: [
        {
            id: "profile-1",
            sha256: "a".repeat(64),
            policyVersion: "work-pattern-profile-v1",
            status: "qualified",
            sourceWindowStart: 1_700_000_000_000,
            sourceCutoffAt: 1_799_999_000_000,
            sampleCount: 18,
            sessionCount: 3,
            confidence: 0.8,
            projectionCount: 1,
            evidenceCount: 3,
            exclusionCount: 1,
            correctionCount: 0,
            consumingEpisodeCount: 1,
            consumingRunCount: 1,
            ablationRunCount: 1,
            createdAt: 1_799_999_000_001,
        },
    ],
    rollout,
    learningInfluence: {
        version: "shadow-learning-influence-proof-v1",
        state: "measured_improvement",
        complete: true,
        improved: true,
        summary:
            "A current Shadow learning improved prediction on a later contained task.",
        requiredAction:
            "Keep measuring independent later tasks before changing autonomy.",
        authorityEffect: "none" as const,
        generatedAt: 1_800_000_000_000,
        sourceLearningCount: 1,
        laterTaskCount: 1,
        measuredChainCount: 1,
        learningRevisionId: "learning-revision-1",
        learningReceiptId: "learning-receipt-1",
        learningTargetKind: "capability_profile",
        learningKind: "capability_evidence",
        learningCapability: "review.pull.request",
        learningAvailableAt: 1_799_999_000_050,
        sourceTaskEpisodeIds: ["source-episode-1"],
        sourceShadowRunIds: ["source-shadow-1"],
        sourceEvaluationIds: ["source-evaluation-1"],
        laterTaskEpisodeId: "episode-1",
        laterTaskStatus: "completed",
        laterTaskStartedAt: 1_799_999_100_000,
        contextSnapshotId: "context-1",
        contextSha256: "d".repeat(64),
        profileSnapshotId: "profile-1",
        profileSha256: "a".repeat(64),
        onsetProfilePresent: true,
        onsetPromptIncluded: true,
        onsetPromptReason: "selected_for_onset_prompt",
        onsetPromptSha256: "f".repeat(64),
        shadowContextIncluded: true,
        shadowContextReason: "selected_for_shadow_context",
        shadowContextSha256: "2".repeat(64),
        shadowRunId: "shadow-1",
        shadowExecutionAttemptId: "shadow-attempt-1",
        shadowEvaluationId: "shadow-evaluation-1",
        ablationRunId: "ablation-1",
        ablationEvaluationId: "ablation-evaluation-1",
        neutralScore: 0,
        profileScore: 1,
        lift: 1,
        winner: "profile",
        steps: [
            {
                key: "source_learning",
                label: "Current verified learning",
                complete: true,
            },
            {
                key: "later_task",
                label: "Strictly later user task",
                complete: true,
            },
            {
                key: "onset_admission",
                label: "Learning entered production onset",
                complete: true,
                reason: "selected_for_onset_prompt",
            },
            {
                key: "shadow_admission",
                label: "Learning entered isolated Shadow context",
                complete: true,
                reason: "selected_for_shadow_context",
            },
            {
                key: "contained_result",
                label: "Contained result compared with user result",
                complete: true,
            },
            {
                key: "paired_ablation",
                label: "Neutral and profile branches joined",
                complete: true,
            },
            {
                key: "measured_lift",
                label: "Prediction lift measured",
                complete: true,
            },
        ],
        reasons: [],
    },
    learningCohorts: {
        id: "shadow-learning-influence-cohort-snapshot:snapshot-manifest",
        version: "shadow-learning-influence-cohort-v1",
        policyVersion: "shadow-learning-influence-reliability-v1",
        generatedAt: 1_800_000_000_000,
        readCutoffAt: 1_800_000_000_000,
        manifestSha256: "8".repeat(64),
        authorityEffect: "none" as const,
        productionInfluenceEffect: "none",
        policy: {
            version: "shadow-learning-influence-reliability-v1",
            minimumScoredTasks: 5,
            minimumSourceTasks: 5,
            minimumProfileWinRate: 0.6,
            minimumMeanLift: 0.03,
            maximumUnknownRate: 0.2,
            maximumCorrectionRate: 0.1,
        },
        cohortCount: 1,
        awaitingMeasurementCount: 0,
        earlyCount: 1,
        reliableCount: 0,
        insufficientQualityCount: 0,
        integrityErrorCount: 0,
        cohorts: [
            {
                id: "shadow-learning-influence-cohort:learning-revision-1",
                manifestSha256: "7".repeat(64),
                state: "early",
                summary:
                    "The learning is being measured, but the cohort is still early.",
                learningRevisionId: "learning-revision-1",
                learningReceiptId: "learning-receipt-1",
                learningTargetKind: "capability_profile",
                learningKind: "capability_evidence",
                learningCapability: "review.pull.request",
                learningAvailableAt: 1_799_999_000_050,
                sourceTaskEpisodeCount: 5,
                sourceShadowRunCount: 5,
                sourceEvaluationCount: 5,
                sourceTaskEpisodeIds: [
                    "source-episode-1",
                    "source-episode-2",
                    "source-episode-3",
                    "source-episode-4",
                    "source-episode-5",
                ],
                sourceShadowRunIds: [
                    "source-shadow-1",
                    "source-shadow-2",
                    "source-shadow-3",
                    "source-shadow-4",
                    "source-shadow-5",
                ],
                sourceEvaluationIds: [
                    "source-evaluation-1",
                    "source-evaluation-2",
                    "source-evaluation-3",
                    "source-evaluation-4",
                    "source-evaluation-5",
                ],
                relevantLaterTaskCount: 1,
                onsetOmittedCount: 0,
                shadowOmittedCount: 0,
                awaitingResultCount: 0,
                awaitingAblationCount: 0,
                historicalAblationCount: 0,
                scoredChainCount: 1,
                unknownChainCount: 0,
                profileWins: 1,
                neutralWins: 0,
                ties: 0,
                positiveLiftTotal: 1,
                negativeLiftTotal: 0,
                meanLift: 1,
                profileWinRate: 1,
                unknownRate: 0,
                correctionCount: 0,
                correctedChainCount: 0,
                correctionRate: 0,
                integrityExclusionCount: 0,
                blockers: ["1 of 5 independently scored later tasks"],
                chainCount: 1,
                authorityEffect: "none" as const,
                productionInfluenceEffect: "none",
                chains: [
                    {
                        id: "shadow-learning-influence-chain:episode-1",
                        learningRevisionId: "learning-revision-1",
                        laterTaskEpisodeId: "episode-1",
                        laterTaskStatus: "completed",
                        laterTaskStartedAt: 1_799_999_100_000,
                        state: "measured_improvement",
                        profileSnapshotId: "profile-1",
                        onsetIncluded: true,
                        shadowIncluded: true,
                        shadowRunId: "shadow-1",
                        shadowEvaluationId: "shadow-evaluation-1",
                        ablationRunId: "ablation-1",
                        ablationEvaluationId: "ablation-evaluation-1",
                        neutralScore: 0,
                        profileScore: 1,
                        lift: 1,
                        winner: "profile",
                        historicalPolicyExcluded: false,
                        corrected: false,
                        correctionIds: [],
                        integrityFailure: false,
                        reasons: [],
                        authorityEffect: "none" as const,
                    },
                ],
            },
        ],
    },
};

const detail: WorkPatternProfileDebugDetail = {
    generatedAt: 1_800_000_000_000,
    snapshot: {
        id: "profile-1",
        policyVersion: "work-pattern-profile-v1",
        sourceWindowStart: 1_700_000_000_000,
        sourceCutoffAt: 1_799_999_000_000,
        sourceMaxEventId: 18,
        sourceHash: "b".repeat(64),
        status: "qualified",
        sampleCount: 18,
        sessionCount: 3,
        confidence: 0.8,
        projections: [
            {
                scopeKind: "global",
                scopeKey: "all",
                label: "All retained work",
                evidenceKind: "observed",
                status: "qualified",
                confidence: 0.8,
                sampleCount: 18,
                sessionCount: 3,
                metrics: [
                    {
                        name: "switches_per_active_hour",
                        value: 8,
                        unit: "count",
                        sampleCount: 18,
                        confidence: 0.8,
                    },
                ],
            },
        ],
        exclusions: [
            {
                sourceKind: "activity_session",
                sourceId: "redacted",
                reason: "privacy_excluded",
            },
        ],
        canonicalJson: "{}",
        sha256: "a".repeat(64),
        createdAt: 1_799_999_000_001,
    },
    promptDiagnostic: {
        version: "work-pattern-prompt-diagnostic-v1",
        influenceMode: "profile",
        sourceSnapshotBytes: 1_048_576,
        sourceContextBytes: 1_102_334,
        promptProjectionBytes: 14_612,
        promptBudgetBytes: 15_000,
        rawReferenceCount: 12_431,
        selectedProjectionCount: 6,
        capabilitySignalCount: 2,
        contradictionCount: 0,
        manifestEntryCount: 5_013,
        promptCandidateCount: 14,
        promptIncludedCount: 9,
        promptOmittedCount: 5,
        sourceContextSha256: "e".repeat(64),
        promptProjectionSha256: "f".repeat(64),
        selectionManifestSha256: "1".repeat(64),
        fits: true,
    },
    evidence: [
        {
            id: "evidence-1",
            profileSnapshotId: "profile-1",
            sourceKind: "activity_session",
            sourceId: "session-1",
            observedAt: 1_799_998_000_000,
            contentHash: "c".repeat(64),
            included: true,
            reason: "retained_activity",
            createdAt: 1_799_999_000_001,
        },
    ],
    corrections: [],
    episodes: [
        {
            id: "episode-1",
            status: "completed",
            task: "Review a pull request",
            desiredResult: "A review conclusion exists",
            inferredStartAt: 1_799_999_100_000,
            contextSnapshotId: "context-1",
            contextSha256: "d".repeat(64),
            workProfileSha256: "a".repeat(64),
            profileSelectionVersion: "work-pattern-selection-provenance-v3",
            profileOnsetPresent: true,
            profileOnsetSourceContextSha256: "e".repeat(64),
            profileOnsetPromptSha256: "f".repeat(64),
            profileOnsetSelectionSha256: "1".repeat(64),
            onsetProfileSelectionManifest: [
                {
                    ref: "capability:learning-revision-1",
                    kind: "capability",
                    evidenceClass: "evaluated",
                    included: true,
                    reason: "selected_for_current_context",
                    learning: learningProvenance,
                    onsetPromptIncluded: true,
                    onsetPromptReason: "selected_for_onset_prompt",
                },
            ],
            onsetProfileSelectionReason: "qualified_profile_available",
            profileSourceContextSha256: "e".repeat(64),
            profilePromptSha256: "f".repeat(64),
            profileSelectionSha256: "1".repeat(64),
            profileInfluenceRefs: [
                "metric:global:all:median_dwell_seconds",
            ],
            profileConflict: false,
            profileSelectionManifest: [
                {
                    ref: "projection:global:all",
                    kind: "projection",
                    evidenceClass: "observed",
                    included: true,
                    reason: "selected_for_current_context",
                },
                {
                    ref: "capability:learning-revision-1",
                    kind: "capability",
                    evidenceClass: "evaluated",
                    included: true,
                    reason: "selected_for_current_context",
                    learning: learningProvenance,
                    shadowContextIncluded: true,
                    shadowContextReason: "selected_for_shadow_context",
                },
            ],
            profileSelectionReason: "qualified_profile_available",
            includedContextCount: 4,
            excludedContextCount: 2,
        },
    ],
    shadowRuns: [
        {
            id: "shadow-1",
            taskEpisodeId: "episode-1",
            capabilityKey: "review.pull.request",
            status: "evaluated",
            model: "gpt-5.6-sol",
            promptVersion: "shadow-task-v3",
            contextSha256: "d".repeat(64),
            profileSha256: "a".repeat(64),
            profileInfluenceRefs: [
                "metric:global:all:median_dwell_seconds",
            ],
            profileConflict: false,
            createdAt: 1_799_999_100_001,
        },
    ],
    ablations: [
        {
            id: "ablation-1",
            sourceKind: "activity",
            sourceId: "window-1",
            windowStart: 1_799_999_100_000,
            cutoffAt: 1_799_999_130_000,
            profileSnapshotId: "profile-1",
            profileSha256: "a".repeat(64),
            primaryMode: "neutral",
            neutralAttemptId: "attempt-neutral",
            profileAttemptId: "attempt-profile",
            neutralStatus: "succeeded",
            profileStatus: "succeeded",
            neutralResponse: { episodes: [] },
            profileResponse: {
                episodes: [{ task: "Review a pull request" }],
            },
            model: "gpt-5.6-terra",
            promptVersion: "task-onset-v4",
            startedAt: 1_799_999_130_001,
            completedAt: 1_799_999_130_500,
            createdAt: 1_799_999_130_500,
        },
    ],
    evaluations: [
        {
            id: "ablation-evaluation-1",
            ablationRunId: "ablation-1",
            taskEpisodeId: "episode-1",
            policyVersion: "work-pattern-ablation-v2",
            neutralScore: 0,
            profileScore: 1,
            neutralBoundaryErrorMs: 500,
            profileBoundaryErrorMs: 0,
            winner: "profile",
            evidenceJson: "{}",
            createdAt: 1_799_999_200_000,
        },
    ],
    modelAttempts: [
        {
            id: "attempt-profile",
            phase: "onset",
            modelName: "gpt-5.6-terra",
            modelVersion: "gpt-5.6-terra",
            promptVersion: "task-onset-v4",
            promptText: "Exact profile-aware prompt",
            promptHash: "prompt-hash",
            normalizedInputJson: "{}",
            inputHash: "input-hash",
            status: "succeeded",
            rawOutput: '{"episodes":[]}',
            startedAt: 1_799_999_130_001,
            completedAt: 1_799_999_130_500,
            createdAt: 1_799_999_130_500,
        },
    ],
    replay: {
        profileSnapshotId: "profile-1",
        profileSha256: "a".repeat(64),
        sourceCutoffAt: 1_799_999_000_000,
        onsetAttemptIds: ["attempt-neutral", "attempt-profile"],
        taskEpisodeIds: ["episode-1"],
        shadowRunIds: ["shadow-1"],
    },
    rollout,
};

describe("WorkProfileDebugSettings", () => {
    beforeEach(() => {
        mocks.getSnapshot.mockReset().mockResolvedValue(snapshot);
        mocks.getDetail.mockReset().mockResolvedValue(detail);
    });

    it("opens full revision detail and links consumers", async () => {
        const user = userEvent.setup();
        const onOpenTaskEpisode = vi.fn();
        const onOpenShadowRun = vi.fn();
        render(
            <WorkProfileDebugSettings
                onOpenTaskEpisode={onOpenTaskEpisode}
                onOpenShadowRun={onOpenShadowRun}
            />,
        );

        const patternSearch = await screen.findByPlaceholderText(
            "Search pattern or record hash",
        );
        expect(patternSearch.parentElement?.parentElement).toHaveClass(
            "flex-col",
            "lg:flex-row",
        );

        const learningProofLabel = await screen.findByText(
            "Learning effect",
        );
        expect(learningProofLabel).toBeInTheDocument();
        const learningProof = learningProofLabel.closest("details");
        expect(learningProof).not.toBeNull();
        expect(
            within(learningProof!).getByText("Improved later prediction"),
        ).toHaveAttribute(
            "title",
            "Technical status: measured_improvement",
        );
        await user.click(screen.getByText("Learning effect"));
        expect(
            screen.getByText("Learning entered production onset"),
        ).toBeInTheDocument();
        await user.click(screen.getByRole("button", { name: "Later task" }));
        expect(onOpenTaskEpisode).toHaveBeenCalledWith("episode-1");
        await user.click(screen.getByRole("button", { name: "Isoform's attempt" }));
        expect(onOpenShadowRun).toHaveBeenCalledWith("shadow-1");

        const repeatedLabel = screen.getByText("Repeated learning checks");
        const repeated = repeatedLabel.closest("details");
        expect(repeated).not.toBeNull();
        await user.click(repeatedLabel);
        const repeatedView = within(repeated!);
        expect(
            repeatedView.getByText(
                "1 of 5 independently scored later tasks",
            ),
        ).toBeInTheDocument();
        const cohortLabel = repeatedView.getByText("review.pull.request");
        await user.click(cohortLabel);
        const cohort = cohortLabel.closest("details");
        expect(cohort).not.toBeNull();
        const cohortView = within(cohort!);
        expect(cohortView.getByText("episode-1")).toBeInTheDocument();
        await user.click(cohortView.getByRole("button", { name: "Task" }));
        expect(onOpenTaskEpisode).toHaveBeenLastCalledWith("episode-1");
        await user.click(cohortView.getByRole("button", { name: "Shadow" }));
        expect(onOpenShadowRun).toHaveBeenLastCalledWith("shadow-1");

        await user.click(
            await screen.findByRole("button", { name: /profile-1/ }),
        );
        await waitFor(() =>
            expect(mocks.getDetail).toHaveBeenCalledWith("profile-1", 200),
        );
        expect(
            await screen.findByText("Contextual projections"),
        ).toBeInTheDocument();
        expect(screen.getByText("Results compared")).toBeInTheDocument();
        expect(screen.getByText(/1 paired attempt/)).toBeInTheDocument();
        expect(
            screen.getByRole("button", { name: "Copy replay bundle" }),
        ).toBeInTheDocument();
        expect(screen.getByText("Prompt envelope")).toBeInTheDocument();
        expect(
            screen.getByText("Fits current test limits"),
        ).toBeInTheDocument();
        expect(screen.getByText("14.3 KB")).toBeInTheDocument();
        expect(screen.getByText("Paired neutral/profile evaluation")).toBeInTheDocument();
        expect(
            screen.getByText("What Isoform knew when it made this prediction"),
        ).toBeInTheDocument();
        expect(screen.getByText(/learning-receipt-1/)).toBeInTheDocument();
        expect(
            screen.getByText(
                /profile 100% vs neutral 0% · lift \+100%/,
            ),
        ).toBeInTheDocument();
        expect(screen.getByText("Used at task start")).toHaveAttribute(
            "title",
            "Technical status: onset_included",
        );
        expect(screen.getByText("Included in private practice")).toHaveAttribute(
            "title",
            "Technical status: shadow_included",
        );

        await user.click(
            screen.getAllByRole("button", {
                name: /Review a pull request/,
            })[0],
        );
        expect(onOpenTaskEpisode).toHaveBeenCalledWith("episode-1");

        await user.click(
            screen.getByRole("button", { name: /shadow-1 · evaluated/ }),
        );
        expect(onOpenShadowRun).toHaveBeenCalledWith("shadow-1");
    });
});
