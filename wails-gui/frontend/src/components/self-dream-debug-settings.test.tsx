import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { SelfDreamDebugSettings } from "./self-dream-debug-settings";

const mocks = vi.hoisted(() => ({
    getSnapshot: vi.fn(),
    getRun: vi.fn(),
    replay: vi.fn(),
    eventsOn: vi.fn(() => vi.fn()),
}));

vi.mock("@/lib/wails-shim-app", async (importOriginal) => {
    const original =
        await importOriginal<typeof import("@/lib/wails-shim-app")>();
    return {
        ...original,
        GetSelfDreamDebugSnapshot: mocks.getSnapshot,
        GetSelfDreamDebugRun: mocks.getRun,
        ReplaySelfDreamDebug: mocks.replay,
    };
});

vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: mocks.eventsOn,
}));

const snapshot = {
    generatedAt: 1_800_000_000_000,
    runs: [
        {
            id: "dream-run-1",
            status: "completed",
            triggerKind: "scheduled",
            sourceCutoffAt: 1_799_999_000_000,
            inputHash: "a".repeat(64),
            model: "gpt-5.6-sol",
            operationCount: 1,
            qualifiedCount: 1,
            reviewCount: 0,
            rejectedCount: 0,
            createdAt: 1_799_999_000_001,
        },
    ],
    rollout: {
        enabled: false,
        ready: false,
        completedRealRuns: 4,
        scoredCandidates: 4,
        meanImprovement: 0.06,
        coverageLossCount: 0,
        unsupportedReasoningCount: 0,
        correctionOrRejectRate: 0,
        staleCompilationErrors: 0,
        reasons: ["Need 16 more completed real shadow evaluations."],
    },
};

const detail = {
    status: "completed",
    run: {
        id: "dream-run-1",
        triggerKind: "scheduled",
        policyVersion: "self-dream-v1",
        assemblerVersion: "self-dream-context-v2",
        sourceCutoffAt: 1_799_999_000_000,
        baseSelf: [
            {
                id: "self-1",
                kind: "preference",
                scope: "project:yansu",
                statement: "Draft concise project updates.",
                revision: 1,
                status: "active",
            },
        ],
        contextHash: "b".repeat(64),
        inputHash: "a".repeat(64),
        promptVersion: "self-dream-prompt-v1",
        prompt: "Exact frozen production prompt",
        promptHash: "c".repeat(64),
        requestedModel: "gpt-5.6-sol",
        manifest: [
            {
                ref: "memory:preference-1",
                sourceKind: "memory",
                sourceId: "preference-1",
                evidenceClass: "explicit",
                included: true,
                contentHash: "d".repeat(64),
            },
            {
                ref: "shadow:hidden-1",
                sourceKind: "shadow_run",
                sourceId: "hidden-1",
                included: false,
                reason: "shadow_output_excluded",
            },
        ],
    },
    attempt: {
        status: "completed",
        safetyMode: "local_no_tools",
        learningEligible: false,
        productMutation: false,
        model: "gpt-5.6-sol",
        rawOutput: `{"operations":[]}`,
        durationMs: 1200,
    },
    operations: [
        {
            id: "operation-1",
            operation: "merge",
            targetEntryIds: ["self-1", "self-2"],
            selfKind: "preference",
            scope: { kind: "project", key: "yansu" },
            statement: "Draft concise project updates.",
            reasoningStatus: "unknown",
            uncertainty: "No reason was inferred.",
            confidence: 1,
            evidenceRefs: ["memory:preference-1"],
            source: "deterministic",
            validationStatus: "qualified",
            validationReason: "deterministic_exact_duplicate",
            autoEligible: true,
        },
    ],
    evaluation: {
        improvement: 0.08,
        coverageLoss: 0,
        contradictionRisk: 0,
        unsupportedReasoning: 0,
        realEvidence: true,
        neutral: {
            entryCount: 2,
            duplicateEntryCount: 1,
            evidenceCoverage: 1,
            quality: 0.7,
        },
        dream: {
            entryCount: 1,
            duplicateEntryCount: 0,
            evidenceCoverage: 1,
            quality: 0.78,
        },
    },
    decisions: [],
    compilations: [],
    replays: [
        {
            id: "replay-0",
            status: "completed",
            contextMatchesSource: true,
            promptVersion: "self-dream-prompt-v1",
            model: "gpt-5.6-sol",
            durationMs: 760,
            createdAt: 1_799_999_500_000,
        },
    ],
};

beforeEach(() => {
    vi.clearAllMocks();
    mocks.eventsOn.mockReturnValue(vi.fn());
    mocks.getSnapshot.mockResolvedValue(snapshot);
    mocks.getRun.mockResolvedValue(detail);
    mocks.replay.mockResolvedValue({
        replay: {
            id: "replay-1",
            status: "completed",
            contextMatchesSource: true,
            promptVersion: "self-dream-prompt-v1",
            model: "gpt-5.6-sol",
            durationMs: 800,
            createdAt: 1_800_000_000_000,
        },
        prompt: "Exact frozen production prompt",
    });
});

describe("SelfDreamDebugSettings", () => {
    it("opens full run detail at compact widths and returns with Escape", async () => {
        render(<SelfDreamDebugSettings />);

        const list = await screen.findByRole("region", {
            name: "Profile suggestions",
        });
        const detailPanel = screen.getByRole("region", {
            name: "Profile suggestion details",
        });
        const run = await within(list).findByRole("button", {
            name: /1 operation/i,
        });

        fireEvent.click(run);

        expect(list).toHaveClass("hidden", "xl:block");
        expect(detailPanel).toHaveClass("block");
        expect(
            within(detailPanel).getByRole("button", { name: "Back to list" }),
        ).toBeInTheDocument();
        expect(within(detailPanel).getByText("Containment")).toBeInTheDocument();
        expect(within(detailPanel).getByText("local_no_tools")).toBeInTheDocument();
        expect(within(detailPanel).getAllByText("off")).toHaveLength(2);
        expect(
            within(detailPanel).getByText("Evidence considered"),
        ).toBeInTheDocument();
        expect(
            within(detailPanel).getByText(/shadow_output_excluded/),
        ).toBeInTheDocument();
        expect(
            within(detailPanel).queryByRole("button", {
                name: /approve|accept|execute|allow/i,
            }),
        ).not.toBeInTheDocument();
        expect(
            within(detailPanel).getByText("Replay history"),
        ).toBeInTheDocument();
        expect(
            within(detailPanel).getAllByText("matches production"),
        ).toHaveLength(3);

        fireEvent.keyDown(detailPanel, { key: "Escape" });

        expect(list).toHaveClass("block");
        expect(detailPanel).toHaveClass("hidden", "xl:block");
    });

    it("replays the editable prompt through the isolated debug API", async () => {
        render(<SelfDreamDebugSettings />);

        await waitFor(() =>
            expect(mocks.getRun).toHaveBeenCalledWith("dream-run-1"),
        );
        const prompt = await screen.findByDisplayValue(
            "Exact frozen production prompt",
        );
        fireEvent.change(prompt, {
            target: { value: "Tuned diagnostic prompt" },
        });
        fireEvent.click(screen.getByRole("button", { name: "Replay" }));

        await waitFor(() =>
            expect(mocks.replay).toHaveBeenCalledWith({
                runId: "dream-run-1",
                prompt: "Tuned diagnostic prompt",
                model: "gpt-5.6-sol",
            }),
        );
        expect(mocks.getRun).toHaveBeenCalledTimes(2);
        expect(mocks.eventsOn).toHaveBeenCalledWith(
            "self_dream:updated",
            expect.any(Function),
        );
    });
});
