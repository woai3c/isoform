import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type {
    PurposeDebugExtractionRunDetail,
    PurposeDebugOpenLoopDetail,
    PurposeDebugSnapshot,
} from "@/lib/wails-shim-app";
import { PurposeMapDebugSettings } from "./purpose-map-debug-settings";

const mocks = vi.hoisted(() => ({
    getSnapshot: vi.fn(),
    getOpenLoop: vi.fn(),
    getExtraction: vi.fn(),
    eventsOn: vi.fn(),
    unsubscribe: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    GetPurposeDebugSnapshot: mocks.getSnapshot,
    GetPurposeDebugOpenLoop: mocks.getOpenLoop,
    GetPurposeDebugExtractionRun: mocks.getExtraction,
}));

vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: mocks.eventsOn,
}));

const hash = "a".repeat(64);

function purpose(id: string, title: string) {
    return {
        id,
        canonicalKey: title.toLowerCase().replaceAll(" ", "-"),
        kind: "goal" as const,
        title,
        desiredOutcome: `${title} has a verified outcome.`,
        owner: "user" as const,
        scope: "Current launch",
        dueKind: "window" as const,
        dueValue: "This week",
        lifecycleState: "active" as const,
        supportState: "supported" as const,
        reasoningStatus: "unknown" as const,
        confidence: 1,
        evidenceCount: 1,
        relationCount: id === "purpose-1" ? 1 : 0,
        eligible: true,
        currentEventId: `${id}-event`,
        currentEventType: "discovered",
        advancingInitiativeIds: ["initiative-1"],
        createdAt: "2026-08-02T10:00:00.000Z",
        updatedAt: "2026-08-02T10:00:00.000Z",
    };
}

function rootSummary(id: string, title: string) {
    return {
        purpose: purpose(id, title),
        eligibility: {
            eligible: true,
            decision: "included" as const,
            reasons: ["supported current canonical Purpose"],
            currentEvidenceCount: 1,
            independentKeys: ["user:direct"],
            hasExplicitSupport: true,
            duplicateOfIds: [],
            projectionReference: `purpose_open_loop:${id}:${id}-event:${hash}`,
            projectionHash: hash,
        },
        contractVersion: "purpose-open-loop-v1",
        rootPayloadHash: hash,
        eventCount: 1,
        evidenceCount: 1,
        relationCount: id === "purpose-1" ? 1 : 0,
    };
}

const extractionSummary = {
    id: "extraction-1",
    status: "succeeded",
    model: "gpt-5.6-terra",
    cutoffAt: 1785664800000,
    scannedCount: 12,
    includedCount: 7,
    excludedCount: 5,
    hypothesisCount: 1,
    receiptCount: 1,
    inputHash: hash,
    manifestHash: hash,
    promptHash: hash,
    outputHash: hash,
    rawOutputHash: hash,
    payloadHash: hash,
    startedAt: 1785664800000,
    completedAt: 1785664801000,
    createdAt: 1785664801000,
};

const snapshot: PurposeDebugSnapshot = {
    version: "purpose-debug-v1",
    generatedAt: 1785664804000,
    integrityStatus: "validated",
    understanding: {
        started: true,
        running: true,
        pendingTriggers: ["self_model:updated"],
        current: {
            state: "checking",
            triggerKinds: ["memory:updated"],
            model: "gpt-5.6-sol",
            fingerprint: hash,
            runId: "purpose-understanding-run-1",
            cached: false,
            scannedCount: 12,
            includedCount: 7,
            excludedCount: 5,
            currentPurposeCount: 2,
            changedRootIds: [],
            changedRelationIds: [],
            recoveredRunIds: [],
            startedAt: 1785664800000,
        },
    },
    counts: {
        openLoops: 2,
        eligibleOpenLoops: 2,
        events: 2,
        evidence: 2,
        relations: 1,
        relationEvents: 1,
        extractionRuns: 1,
        extractionHypotheses: 1,
        reconciliationReceipts: 1,
    },
    truncated: {
        extractionRuns: false,
    },
    openLoops: [
        rootSummary("purpose-1", "Prepare launch decision"),
        rootSummary("purpose-2", "Confirm customer decision"),
    ],
    extractionRuns: [extractionSummary],
};

function openLoopDetail(id: string): PurposeDebugOpenLoopDetail {
    const title = id === "purpose-1" ? "Prepare launch decision" : "Confirm customer decision";
    const summary = rootSummary(id, title);
    return {
        summary,
        product: {
            purpose: summary.purpose,
            events: [{
                id: `${id}-event`,
                sequence: 1,
                eventType: "discovered",
                title,
                desiredOutcome: summary.purpose.desiredOutcome,
                owner: "user",
                dueKind: "window",
                dueValue: "This week",
                lifecycleState: "active",
                supportState: "supported",
                reasoningStatus: "unknown",
                confidence: 1,
                evidenceIds: [`${id}-evidence`],
                evidenceSetHash: hash,
                occurredAt: "2026-08-02T10:00:00.000Z",
                createdAt: "2026-08-02T10:00:00.000Z",
                payloadHash: hash,
            }],
            evidence: [{
                id: `${id}-evidence`,
                eventId: `${id}-event`,
                sourceKind: "purpose_product_user_command",
                sourceId: "command-1",
                sourceVersion: "purpose-product-command-v1",
                sourceObservedAt: "2026-08-02T10:00:00.000Z",
                sourceHash: hash,
                evidenceClass: "explicit_user",
                stance: "support",
                independenceKey: "user:direct",
                excerptHash: hash,
                privacyDecision: "redact",
                createdAt: "2026-08-02T10:00:00.000Z",
                payloadHash: hash,
            }],
            relations: id === "purpose-1" ? [{
                id: "relation-1",
                relationType: "related_to",
                direction: "outgoing",
                relatedPurposeId: "purpose-2",
                relatedTitle: "Confirm customer decision",
                relatedState: "active",
                active: true,
                currentEventId: "relation-event-1",
                createdAt: "2026-08-02T10:00:00.000Z",
                payloadHash: hash,
                events: [],
            }] : [],
        },
        rootPayloadJson: JSON.stringify({ id, contractVersion: "purpose-open-loop-v1" }),
    };
}

const extractionDetail: PurposeDebugExtractionRunDetail = {
    summary: extractionSummary,
    query: "current launch",
    sources: [{
        ref: "memory:launch:1",
        sourceKind: "memory",
        sourceId: "launch",
        sourceVersion: "1",
        sourceObservedAt: 1785664700000,
        sourceHash: hash,
        promptContentHash: hash,
        evidenceClass: "explicit_user",
        privacyDecision: "redact",
        independenceKey: "memory:launch",
        truncated: false,
    }],
    currentPurpose: [{
        ref: `purpose:purpose-1:purpose-1-event:${hash}`,
        openLoopId: "purpose-1",
        currentEventId: "purpose-1-event",
        projectionHash: hash,
        canonicalKey: "launch-decision",
        kind: "goal",
        title: "Prepare launch decision",
        desiredOutcome: "The launch decision has a verified outcome.",
        owner: "user",
        dueKind: "window",
        dueValue: "This week",
        reasoningStatus: "unknown",
        lifecycleState: "active",
        supportState: "supported",
        eligible: true,
        duplicateOfIds: [],
    }],
    manifest: [{
        ref: "memory:launch:1",
        sourceKind: "memory",
        sourceId: "launch",
        decision: "included",
        sourceHash: hash,
        promptContentHash: hash,
        includedChars: 420,
    }],
    manifestSummary: {
        version: "purpose-extraction-manifest-v2",
        cutoffAt: 1785664800000,
        budgetChars: 96000,
        usedChars: 420,
        scannedCount: 1,
        includedCount: 1,
        excludedCount: 0,
        entries: [],
        currentPurposeBudgetChars: 96000,
        currentPurposeUsedChars: 560,
        currentPurposeCount: 1,
        currentPurposeHash: hash,
    },
    hypotheses: [{
        id: "hypothesis-1",
        runId: "extraction-1",
        localKey: "launch",
        canonicalKey: "launch",
        kind: "goal",
        operation: "support",
        title: "Prepare launch decision",
        desiredOutcome: "The launch decision has a verified outcome.",
        owner: "user",
        dueKind: "window",
        dueValue: "This week",
        reasoningStatus: "unknown",
        confidence: 0.8,
        evidenceRefs: ["memory:launch:1"],
        payloadJson: "{}",
        payloadHash: hash,
        createdAt: 1785664801000,
    }],
    receipts: [{
        id: "reconcile-1",
        idempotencyKey: "reconcile-1",
        runId: "extraction-1",
        hypothesisId: "hypothesis-1",
        disposition: "created",
        openLoopId: "purpose-1",
        eventId: "purpose-1-event",
        sourceSetHash: hash,
        occurredAt: 1785664801000,
        createdAt: 1785664801000,
        payloadJson: "{}",
        payloadHash: hash,
    }],
};

describe("PurposeMapDebugSettings", () => {
    beforeEach(() => {
        vi.clearAllMocks();
        mocks.eventsOn.mockReturnValue(mocks.unsubscribe);
        mocks.getSnapshot.mockResolvedValue(snapshot);
        mocks.getOpenLoop.mockImplementation((id: string) => Promise.resolve(openLoopDetail(id)));
        mocks.getExtraction.mockResolvedValue(extractionDetail);
    });

    it("loads every read-only diagnostic view and follows exact provenance", async () => {
        const user = userEvent.setup();
        render(<PurposeMapDebugSettings />);

        expect(await screen.findByText("Evidence records verified")).toBeInTheDocument();
        expect(screen.getByText("When did Isoform last look for priorities?")).toBeInTheDocument();
        expect(screen.getByText("New source changes found")).toBeInTheDocument();
        expect(screen.getByText("Waiting: self_model:updated")).toBeInTheDocument();
        expect(await screen.findByRole("heading", { name: "Prepare launch decision" })).toBeInTheDocument();
        expect(screen.getByText(/supported current canonical Purpose/)).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: /replay|repair|execute/i })).not.toBeInTheDocument();

        await user.click(within(screen.getByRole("region", { name: "Current priorities" })).getByRole("button", { name: /Confirm customer decision/i }));
        await waitFor(() => expect(mocks.getOpenLoop).toHaveBeenLastCalledWith("purpose-2"));
        expect(await screen.findByRole("heading", { name: "Confirm customer decision" })).toBeInTheDocument();

        await user.click(screen.getByRole("button", { name: /How Isoform found them/i }));
        expect(screen.getByRole("textbox", { name: "Search how priorities were found" })).toHaveAttribute("placeholder", "Search source, model, result, or ID");
        expect(screen.getByRole("combobox", { name: "Filter how priorities were found" })).toBeInTheDocument();
        expect(screen.getByRole("region", { name: "How priorities were found" })).toBeInTheDocument();
        expect(screen.getByRole("region", { name: "Priority source details" })).toBeInTheDocument();
        expect(await screen.findByText("Complete source manifest · 1")).toBeInTheDocument();
        expect(screen.getByText("Frozen priority context · 1")).toBeInTheDocument();
        expect(screen.getByText(/never supporting evidence for a hypothesis/)).toBeInTheDocument();
        expect(mocks.getExtraction).toHaveBeenCalledWith("extraction-1");
        expect(screen.queryByRole("button", { name: /Work they guided|Results checked/i })).not.toBeInTheDocument();
    });

    it("keeps search, compact detail navigation, and failed reads recoverable", async () => {
        const user = userEvent.setup();
        mocks.getSnapshot
            .mockRejectedValueOnce(new Error("Purpose ledger is unavailable"))
            .mockResolvedValueOnce(snapshot);
        render(<PurposeMapDebugSettings />);

        expect(await screen.findByRole("alert")).toHaveTextContent("Purpose ledger is unavailable");
        await user.click(screen.getByRole("button", { name: "Retry" }));
        expect(await screen.findByText("Evidence records verified")).toBeInTheDocument();

        const search = screen.getByRole("textbox", { name: "Search priorities" });
        expect(search.parentElement?.parentElement).toHaveClass(
            "lg:grid-cols-[minmax(0,1fr)_190px]",
        );
        await user.type(search, "customer decision");
        const list = within(screen.getByRole("region", { name: "Current priorities" }));
        expect(list.getByRole("button", { name: /Confirm customer decision/i })).toBeInTheDocument();
        expect(list.queryByRole("button", { name: /Prepare launch decision/i })).not.toBeInTheDocument();

        await user.click(list.getByRole("button", { name: /Confirm customer decision/i }));
        expect(await screen.findByRole("button", { name: "Back to list" })).toBeInTheDocument();
        await user.click(screen.getByRole("button", { name: "Back to list" }));

        mocks.getOpenLoop.mockRejectedValueOnce(new Error("Purpose detail failed integrity validation"));
        await user.clear(search);
        await user.click(within(screen.getByRole("region", { name: "Current priorities" })).getByRole("button", { name: /Prepare launch decision/i }));
        expect(await screen.findByRole("alert")).toHaveTextContent("Purpose detail failed integrity validation");
        await user.click(screen.getByRole("button", { name: "Retry" }));
        expect(await screen.findByRole("heading", { name: "Prepare launch decision" })).toBeInTheDocument();
    });
});
