import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type {
    TaskShadowObserverDebugDetail,
    TaskShadowObserverDebugSnapshot,
    TaskShadowObserverCoverageSnapshot,
    TaskShadowObserverTraceEventPayload,
} from "@/lib/wails-shim-app";
import { TaskShadowObserverDebugSettings } from "./task-shadow-observer-debug-settings";

const mockGetSnapshot = vi.fn();
const mockGetDetail = vi.fn();
const mockGetCoverage = vi.fn();
const eventCallbacks = new Map<string, () => void>();
const mockEventsOn = vi.fn((eventName: string, callback: () => void) => {
    eventCallbacks.set(eventName, callback);
    return vi.fn();
});

vi.mock("@/lib/wails-shim-app", async (importOriginal) => {
    const original = await importOriginal<typeof import("@/lib/wails-shim-app")>();
    return {
        ...original,
        GetTaskShadowObserverDebugSnapshot: (...args: unknown[]) =>
            mockGetSnapshot(...args),
        GetTaskShadowObserverDebugDetail: (...args: unknown[]) =>
            mockGetDetail(...args),
        GetTaskShadowObserverCoverageSnapshot: (...args: unknown[]) =>
            mockGetCoverage(...args),
    };
});

vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: (eventName: string, callback: () => void) =>
        mockEventsOn(eventName, callback),
}));

const startedPayload: TaskShadowObserverTraceEventPayload = {
    reason: "Shadow enabled at observer admission",
    firstRunDecision: "not_checked",
    onsetWindowCount: 0,
    retainedOnsetEvidenceCount: 0,
    productionOnsetAttemptIds: [],
    diagnosticOnsetAttemptIds: [],
    lifecycleAttemptIds: [],
    taskEpisodeIds: [],
    expiredTaskEpisodeIds: [],
    shadowRunIds: [],
    workflowDecisions: [],
    continuation: {
        scannedRunCount: 0,
        executionDispatchedIds: [],
        evaluationDispatchedIds: [],
        errorCount: 0,
    },
    diagnostics: [],
    errors: [],
};

const completedPayload: TaskShadowObserverTraceEventPayload = {
    reason: "Observer flow completed with one production task-mirror run",
    shadowEnabledAtExecution: true,
    firstRunDecision: "included",
    onsetWindowCount: 2,
    retainedOnsetEvidenceCount: 38,
    productionOnsetAttemptIds: ["attempt-production"],
    diagnosticOnsetAttemptIds: ["attempt-diagnostic"],
    lifecycleAttemptIds: ["attempt-lifecycle"],
    taskEpisodeIds: ["episode-1"],
    expiredTaskEpisodeIds: [],
    shadowRunIds: ["shadow-run-1"],
    workflowDecisions: [{
        source: "captured_onset",
        taskEpisodeId: "episode-1",
        workflow: "compose.text",
        state: "run_created",
        reason: "created from immutable onset context",
        frozenContextHash: "context-hash",
        frozenManifestCount: 5,
        frozenIncludedCount: 3,
        hasExactActivitySnapshot: true,
        shadowRunId: "shadow-run-1",
        executionDispatched: true,
    }],
    continuation: {
        version: "task-shadow-continuation-v2",
        trigger: "activity_group_full",
        scannedRunCount: 4,
        executionDispatchedIds: ["shadow-run-1"],
        evaluationDispatchedIds: [],
        materializationRecoveredRunIds: [],
        errorCount: 0,
    },
    diagnostics: [],
    errors: [],
};

const snapshot: TaskShadowObserverDebugSnapshot = {
    version: "task-shadow-observer-debug-v1",
    generatedAt: 1_800_000_000_000,
    limit: 200,
    counts: {
        total: 1,
        running: 0,
        interrupted: 0,
        completed: 1,
        completedWithErrors: 0,
        skipped: 0,
        cancelled: 0,
        failed: 0,
        corrupt: 0,
    },
    traces: [{
        id: "observer-trace-1",
        state: "completed",
        integrityValid: true,
        groupId: "activity-group-1",
        flow: "full_onset",
        admission: "eligible",
        admissionReason: "generic activity admission passed",
        shadowEnabledAtAdmission: true,
        sourcePipelineRunId: 91,
        windowStart: 1_799_999_000_000,
        windowEnd: 1_799_999_060_000,
        sessionCount: 2,
        declaredEventCount: 40,
        retainedEventCount: 38,
        firstRunDecision: "included",
        onsetWindowCount: 2,
        retainedOnsetEvidenceCount: 38,
        productionAttemptCount: 1,
        diagnosticAttemptCount: 1,
        taskEpisodeCount: 1,
        shadowRunCount: 1,
        workflowDecisionCount: 1,
        terminalReason: completedPayload.reason,
        latestEventAt: 1_800_000_000_000,
        createdAt: 1_799_999_100_000,
    }],
};

const detail: TaskShadowObserverDebugDetail = {
    version: "task-shadow-observer-debug-v1",
    generatedAt: 1_800_000_000_000,
    summary: snapshot.traces[0],
    trace: {
        id: "observer-trace-1",
        contractVersion: "task-shadow-observer-trace-v1",
        runtimeInstanceId: "runtime-1",
        groupId: "activity-group-1",
        sessionIds: ["session-1", "session-2"],
        sessionManifestHash: "session-manifest-hash",
        source: "activity_triager",
        flow: "full_onset",
        admission: "eligible",
        admissionReason: "generic activity admission passed",
        windowStart: 1_799_999_000_000,
        windowEnd: 1_799_999_060_000,
        sessionCount: 2,
        declaredEventCount: 40,
        retainedEventCount: 38,
        retainedEventManifestHash: "event-manifest-hash",
        shadowEnabledAtAdmission: true,
        sourcePipelineRunId: 91,
        rootHash: "root-hash",
        createdAt: 1_799_999_100_000,
        latestEvent: {
            id: "observer-event-2",
            traceId: "observer-trace-1",
            sequence: 2,
            status: "completed",
            payload: completedPayload,
            payloadHash: "terminal-payload-hash",
            previousHash: "started-payload-hash",
            occurredAt: 1_800_000_000_000,
            createdAt: 1_800_000_000_000,
        },
    },
    events: [
        {
            id: "observer-event-1",
            traceId: "observer-trace-1",
            sequence: 1,
            status: "started",
            payload: startedPayload,
            payloadHash: "started-payload-hash",
            previousHash: "root-hash",
            occurredAt: 1_799_999_100_000,
            createdAt: 1_799_999_100_000,
        },
        {
            id: "observer-event-2",
            traceId: "observer-trace-1",
            sequence: 2,
            status: "completed",
            payload: completedPayload,
            payloadHash: "terminal-payload-hash",
            previousHash: "started-payload-hash",
            occurredAt: 1_800_000_000_000,
            createdAt: 1_800_000_000_000,
        },
    ],
    attemptLinks: [{
        id: "attempt-production",
        role: "production_onset",
        found: true,
        phase: "onset",
        status: "succeeded",
        model: "gpt-5.6-terra",
        promptVersion: "task-onset-v5",
        promptHash: "prompt-hash",
        inputHash: "input-hash",
        startedAt: 1_799_999_200_000,
        completedAt: 1_799_999_201_000,
    }],
};

const duplicatePayload: TaskShadowObserverTraceEventPayload = {
    ...startedPayload,
    reason: "Exact retained activity was already handled by an integrity-valid task check.",
    duplicate: {
        policyVersion: "task-shadow-observer-exact-input-v1",
        originalTraceId: "observer-trace-1",
        originalRootHash: "original-root-hash",
        originalEventHash: "original-event-hash",
        originalStatus: "completed",
        inputIdentityHash: "exact-input-hash",
    },
};

const duplicateSummary = {
    ...snapshot.traces[0],
    id: "observer-trace-duplicate",
    state: "skipped",
    sourcePipelineRunId: 92,
    inputIdentityHash: "exact-input-hash",
    duplicateOfTraceId: "observer-trace-1",
    duplicatePolicyVersion: "task-shadow-observer-exact-input-v1",
    firstRunDecision: "not_checked",
    onsetWindowCount: 0,
    retainedOnsetEvidenceCount: 0,
    productionAttemptCount: 0,
    diagnosticAttemptCount: 0,
    taskEpisodeCount: 0,
    shadowRunCount: 0,
    workflowDecisionCount: 0,
    terminalReason: duplicatePayload.reason,
};

const duplicateDetail: TaskShadowObserverDebugDetail = {
    version: "task-shadow-observer-debug-v1",
    generatedAt: 1_800_000_000_000,
    summary: duplicateSummary,
    trace: {
        ...detail.trace,
        id: "observer-trace-duplicate",
        contractVersion: "task-shadow-observer-trace-v2",
        sourcePipelineRunId: 92,
        inputIdentityHash: "exact-input-hash",
        rootHash: "duplicate-root-hash",
        latestEvent: {
            id: "observer-event-duplicate",
            traceId: "observer-trace-duplicate",
            sequence: 1,
            status: "skipped",
            payload: duplicatePayload,
            payloadHash: "duplicate-event-hash",
            previousHash: "duplicate-root-hash",
            occurredAt: 1_800_000_000_000,
            createdAt: 1_800_000_000_000,
        },
    },
    events: [{
        id: "observer-event-duplicate",
        traceId: "observer-trace-duplicate",
        sequence: 1,
        status: "skipped",
        payload: duplicatePayload,
        payloadHash: "duplicate-event-hash",
        previousHash: "duplicate-root-hash",
        occurredAt: 1_800_000_000_000,
        createdAt: 1_800_000_000_000,
    }],
    attemptLinks: [],
};

const coverageSnapshot: TaskShadowObserverCoverageSnapshot = {
    version: "task-shadow-observer-coverage-v2",
    generatedAt: 1_800_000_000_000,
    limit: 200,
    health: "attention",
    healthReason: "One or more production source-to-observer links require attention.",
    counts: {
        instrumentedSources: 2,
        legacySources: 95,
        currentTraces: 1,
        legacyTraces: 0,
        covered: 1,
        coveredRootOnly: 0,
        pendingDispatch: 0,
        dispatchFailed: 0,
        traceMissing: 1,
        linkMismatch: 0,
        traceCorrupt: 0,
        duplicateTrace: 0,
        sourcePruned: 0,
        unlinkedTrace: 0,
        scanTruncated: 0,
    },
    items: [
        {
            id: "source:92:missing-trace",
            kind: "source",
            state: "trace_missing",
            reason: "source row records a trace that is not present",
            pipelineRunId: 92,
            traceId: "missing-trace",
            groupId: "activity-group-missing",
            admission: "eligible",
            expectedFlow: "full_onset",
            dispatchStatus: "recorded",
            dispatchReason: "observer trace recorded",
            integrityValid: false,
            sourceStartedAt: 1_799_999_200_000,
            sourceCompletedAt: 1_799_999_201_000,
        },
        {
            id: "source:91:observer-trace-1",
            kind: "source",
            state: "covered",
            reason: "source row and immutable observer trace match exactly",
            pipelineRunId: 91,
            traceId: "observer-trace-1",
            groupId: "activity-group-1",
            admission: "eligible",
            expectedFlow: "full_onset",
            traceFlow: "full_onset",
            dispatchStatus: "recorded",
            dispatchReason: "observer trace recorded",
            traceState: "completed",
            integrityValid: true,
            sourceStartedAt: 1_799_999_100_000,
            sourceCompletedAt: 1_799_999_101_000,
            traceCreatedAt: 1_799_999_100_000,
            observerRecordedAt: 1_800_000_000_000,
        },
    ],
    loadedItems: 2,
    scannedTraces: 1,
};

beforeEach(() => {
    vi.clearAllMocks();
    eventCallbacks.clear();
    mockGetSnapshot.mockResolvedValue(snapshot);
    mockGetDetail.mockResolvedValue(detail);
    mockGetCoverage.mockResolvedValue(coverageSnapshot);
});

describe("TaskShadowObserverDebugSettings", () => {
    it("opens a complete immutable observer trace and returns with Back or Escape", async () => {
        render(<TaskShadowObserverDebugSettings />);

        await waitFor(() => expect(mockGetSnapshot).toHaveBeenCalledWith(200));
        await waitFor(() => expect(mockGetDetail).toHaveBeenCalledWith("observer-trace-1"));
        expect(screen.getByText("checks")).toBeInTheDocument();
        expect(screen.getByText("canceled")).toBeInTheDocument();
        const list = screen.getByRole("region", { name: "Task checks" });
        const detailPanel = screen.getByRole("region", { name: "Task check details" });
        const row = within(list).getByRole("button");

        fireEvent.click(row);

        expect(row).toHaveAttribute("aria-current", "true");
        expect(list).toHaveClass("hidden", "xl:block");
        expect(detailPanel).toHaveClass("block");
        expect(within(detailPanel).getByText("Root and event hash chain verified.")).toBeInTheDocument();
        expect(within(detailPanel).getByText("Observed task")).toBeInTheDocument();
        expect(within(detailPanel).getByText("compose.text")).toBeInTheDocument();
        expect(within(detailPanel).getByText("production_onset")).toBeInTheDocument();
        expect(within(detailPanel).getByText("Technical history")).toBeInTheDocument();
        expect(within(detailPanel).getByText("Event 1")).toBeInTheDocument();
        expect(within(detailPanel).getByText("Event 2")).toBeInTheDocument();

        for (const forbidden of ["Replay", "Recover", "Execute", "Approve"]) {
            expect(within(detailPanel).queryByRole("button", { name: forbidden })).not.toBeInTheDocument();
        }

        fireEvent.keyDown(detailPanel, { key: "Escape" });
        expect(list).toHaveClass("block");
        expect(detailPanel).toHaveClass("hidden", "xl:block");

        fireEvent.click(row);
        fireEvent.click(within(detailPanel).getByRole("button", { name: "Back to list" }));
        expect(list).toHaveClass("block");
    });

    it("filters, refreshes, and follows observer events without a mutation control", async () => {
        render(<TaskShadowObserverDebugSettings />);
        await screen.findByText("Checked for a task · Full task check");

        fireEvent.change(screen.getByLabelText("Search task checks"), {
            target: { value: "missing group" },
        });
        expect(screen.getByText("Isoform has not checked any retained activity for tasks yet.")).toBeInTheDocument();

        fireEvent.change(screen.getByLabelText("Search task checks"), {
            target: { value: "activity-group-1" },
        });
        expect(screen.getByText("Checked for a task · Full task check")).toBeInTheDocument();

        const refreshCalls = mockGetSnapshot.mock.calls.length;
        fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
        await waitFor(() => expect(mockGetSnapshot.mock.calls.length).toBeGreaterThan(refreshCalls));

        const eventCalls = mockGetSnapshot.mock.calls.length;
        await act(async () => {
            eventCallbacks.get("shadow_agent:observer_trace")?.();
            await Promise.resolve();
        });
        await waitFor(() => expect(mockGetSnapshot.mock.calls.length).toBeGreaterThan(eventCalls));
    });

    it("opens exact source coverage and keeps the health gate read-only", async () => {
        render(<TaskShadowObserverDebugSettings />);
        await waitFor(() => expect(mockGetCoverage).toHaveBeenCalledWith(200));

        fireEvent.click(screen.getByRole("tab", { name: "Source links" }));
        expect(screen.getByText(coverageSnapshot.healthReason)).toBeInTheDocument();
        expect(screen.getByText("sources")).toBeInTheDocument();
        expect(screen.getByText("older sources")).toBeInTheDocument();
        const list = screen.getByRole("region", { name: "Source links" });
        const detailPanel = screen.getByRole("region", { name: "Source link details" });
        const missingRow = within(list).getByRole("button", { name: /source 92/ });

        fireEvent.click(missingRow);
        expect(within(detailPanel).getByText("Source link check")).toBeInTheDocument();
        expect(within(detailPanel).getByText("source row records a trace that is not present")).toBeInTheDocument();
        expect(within(detailPanel).getByText("observer trace recorded")).toBeInTheDocument();
        expect(within(detailPanel).getByText("No integrity-valid trace is available for this source record.")).toBeInTheDocument();

        fireEvent.keyDown(detailPanel, { key: "Escape" });
        fireEvent.change(screen.getByLabelText("Search source links"), {
            target: { value: "observer-trace-1" },
        });
        expect(within(list).getByRole("button", { name: /source 91/ })).toBeInTheDocument();
        expect(within(list).queryByRole("button", { name: /source 92/ })).not.toBeInTheDocument();

        fireEvent.change(screen.getByLabelText("Filter source links by status"), {
            target: { value: "covered" },
        });
        for (const forbidden of ["Replay", "Repair", "Retry", "Link", "Execute", "Approve"]) {
            expect(screen.queryByRole("button", { name: forbidden })).not.toBeInTheDocument();
        }
    });

    it("explains cold-start legacy rows without claiming missing coverage", async () => {
        mockGetCoverage.mockResolvedValue({
            ...coverageSnapshot,
            health: "awaiting_instrumented_activity",
            healthReason: "No instrumented activity group has reached observer dispatch yet.",
            counts: {
                ...coverageSnapshot.counts,
                instrumentedSources: 0,
                currentTraces: 0,
                covered: 0,
                traceMissing: 0,
                legacySources: 95,
            },
            items: [],
            loadedItems: 0,
            scannedTraces: 0,
        });
        render(<TaskShadowObserverDebugSettings />);
        await waitFor(() => expect(mockGetCoverage).toHaveBeenCalledWith(200));
        fireEvent.click(screen.getByRole("tab", { name: "Source links" }));

        expect(screen.getByText("Waiting for the first post-instrumentation activity group. 95 legacy group records are excluded from health.")).toBeInTheDocument();
        expect(screen.queryByText("source row records a trace that is not present")).not.toBeInTheDocument();
    });

    it("explains an exact duplicate and opens its original task check read-only", async () => {
        mockGetSnapshot.mockResolvedValue({
            ...snapshot,
            counts: {
                ...snapshot.counts,
                total: 2,
                skipped: 1,
            },
            traces: [duplicateSummary, snapshot.traces[0]],
        });
        mockGetDetail.mockImplementation((traceID: string) =>
            Promise.resolve(traceID === duplicateSummary.id ? duplicateDetail : detail));

        render(<TaskShadowObserverDebugSettings />);

        await waitFor(() =>
            expect(mockGetDetail).toHaveBeenCalledWith("observer-trace-duplicate"));
        const list = screen.getByRole("region", { name: "Task checks" });
        const detailPanel = screen.getByRole("region", { name: "Task check details" });
        const duplicateRow = within(list).getByRole("button", {
            name: /Already handled.*Full task check/i,
        });
        fireEvent.click(duplicateRow);

        expect(within(detailPanel).getByRole("heading", {
            name: "Already handled",
        })).toBeInTheDocument();
        expect(within(detailPanel).getByText(
            "No new task, model call, private practice, learning, permission, or action was created from this duplicate source.",
        )).toBeInTheDocument();
        expect(within(detailPanel).getByText(
            "No new model attempt was made for this duplicate source.",
        )).toBeInTheDocument();

        fireEvent.click(within(detailPanel).getByRole("button", {
            name: "Open original task check",
        }));
        await waitFor(() =>
            expect(mockGetDetail).toHaveBeenCalledWith("observer-trace-1"));
        await waitFor(() =>
            expect(within(detailPanel).getByText("compose.text")).toBeInTheDocument());

        for (const forbidden of ["Replay", "Recover", "Execute", "Approve"]) {
            expect(within(detailPanel).queryByRole("button", { name: forbidden })).not.toBeInTheDocument();
        }
    });
});
