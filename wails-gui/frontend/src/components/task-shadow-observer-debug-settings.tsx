import { useCallback, useEffect, useMemo, useState } from "react";
import {
    AlertCircle,
    CheckCircle2,
    ChevronRight,
    Link2,
    Loader2,
    RefreshCw,
    Search,
    ShieldAlert,
} from "lucide-react";
import {
    GetTaskShadowObserverDebugDetail,
    GetTaskShadowObserverDebugSnapshot,
    GetTaskShadowObserverCoverageSnapshot,
    type TaskShadowObserverCoverageItem,
    type TaskShadowObserverCoverageSnapshot,
    type TaskShadowObserverDebugDetail,
    type TaskShadowObserverDebugSnapshot,
    type TaskShadowObserverDebugSummary,
    type TaskShadowObserverTraceEvent,
    type TaskShadowWorkflowReadinessDecision,
} from "@/lib/wails-shim-app";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { Button } from "./ui/button";
import { DebugMasterDetail } from "./debug-master-detail";

const emptySnapshot: TaskShadowObserverDebugSnapshot = {
    version: "task-shadow-observer-debug-v1",
    generatedAt: 0,
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
};

const emptyCoverageSnapshot: TaskShadowObserverCoverageSnapshot = {
    version: "task-shadow-observer-coverage-v2",
    generatedAt: 0,
    limit: 200,
    health: "awaiting_instrumented_activity",
    healthReason: "No instrumented activity group has reached task detection yet.",
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
};

function formatDateTime(value?: number): string {
    if (!value) return "-";
    return new Date(value).toLocaleString();
}

function observerTone(state: string): string {
    switch (state) {
        case "completed":
        case "already_handled":
        case "covered":
        case "healthy":
            return "bg-success/15 text-success";
        case "running":
        case "completed_with_errors":
        case "interrupted":
        case "covered_root_only":
        case "pending_dispatch":
        case "source_pruned_or_cleared":
        case "awaiting_instrumented_activity":
            return "bg-warning-bg text-warning";
        case "failed":
        case "corrupt":
        case "attention":
        case "dispatch_failed":
        case "trace_missing":
        case "link_mismatch":
        case "trace_corrupt":
        case "duplicate_trace":
        case "unlinked_current_trace":
        case "scan_truncated":
            return "bg-error-bg text-error";
        default:
            return "bg-muted/15 text-muted";
    }
}

function observerStatusLabel(state: string): string {
    const labels: Record<string, string> = {
        running: "In progress",
        interrupted: "Interrupted",
        completed: "Completed",
        already_handled: "Already handled",
        completed_with_errors: "Completed with errors",
        skipped: "Skipped",
        cancelled: "Canceled",
        failed: "Failed",
        corrupt: "Corrupt record",
        covered: "Covered",
        healthy: "Healthy",
        covered_root_only: "Root-only coverage",
        pending_dispatch: "Task check pending",
        source_pruned_or_cleared: "Source no longer retained",
        awaiting_instrumented_activity: "Waiting for current activity",
        attention: "Needs attention",
        dispatch_failed: "Task check failed",
        trace_missing: "Task-check record missing",
        link_mismatch: "Record link mismatch",
        trace_corrupt: "Task-check record corrupt",
        duplicate_trace: "Duplicate task-check record",
        unlinked_current_trace: "Unlinked task-check record",
        scan_truncated: "Partial scan",
    };
    if (labels[state]) return labels[state];
    const label = state.replaceAll("_", " ");
    return label.charAt(0).toUpperCase() + label.slice(1);
}

function observerTraceState(trace: TaskShadowObserverDebugSummary): string {
    return trace.duplicateOfTraceId ? "already_handled" : trace.state;
}

function observerAdmissionLabel(value: string): string {
    const labels: Record<string, string> = {
        eligible: "Checked for a task",
        prefiltered: "Follow-up only",
        cooldown: "Follow-up only",
        disabled: "Shadow learning is off",
    };
    return labels[value] ?? observerStatusLabel(value);
}

function observerFlowLabel(value: string): string {
    const labels: Record<string, string> = {
        full_onset: "Full task check",
        lifecycle_continuation: "Follow-up check",
    };
    return labels[value] ?? observerStatusLabel(value);
}

function coverageHealthReason(coverage: TaskShadowObserverCoverageSnapshot): string {
    if (coverage.health === "awaiting_instrumented_activity") {
        return "Isoform has not checked any current activity for tasks yet.";
    }
    return coverage.healthReason
        .replaceAll("observer dispatch", "task check")
        .replaceAll("source row", "source record")
        .replaceAll("trace", "task-check record");
}

function ObserverStatus({ state }: { state: string }) {
    return (
        <span
            className={`inline-flex max-w-full items-center px-1.5 py-0.5 text-[9px] font-medium ${observerTone(state)}`}
        >
            <span className="truncate" title={state}>{observerStatusLabel(state)}</span>
        </span>
    );
}

function LabelValue({ label, value }: { label: string; value: string | number }) {
    return (
        <div className="min-w-0">
            <div className="text-[9px] uppercase text-muted">{label}</div>
            <div className="mt-0.5 break-words text-[11px] text-ink">
                {value === "" ? "-" : value}
            </div>
        </div>
    );
}

function HashValue({ label, value }: { label: string; value?: string }) {
    return (
        <div className="min-w-0">
            <div className="text-[9px] uppercase text-muted">{label}</div>
            <div className="mt-0.5 break-all font-mono text-[9px] text-muted">
                {value || "-"}
            </div>
        </div>
    );
}

function WorkflowDecision({ decision }: { decision: TaskShadowWorkflowReadinessDecision }) {
    return (
        <div className="border-l-2 border-border pl-3">
            <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                    <div className="break-all text-[11px] font-medium text-ink">
                        {decision.workflow}
                    </div>
                    <div className="break-all font-mono text-[9px] text-muted">
                        {decision.taskEpisodeId}
                    </div>
                </div>
                <ObserverStatus state={decision.state} />
            </div>
            {decision.reason && (
                <p className="mt-1 text-[10px] text-muted">{decision.reason}</p>
            )}
            <div className="mt-1 text-[9px] text-muted">
                {decision.source} · {decision.frozenIncludedCount}/{decision.frozenManifestCount} context included · exact activity {decision.hasExactActivitySnapshot ? "yes" : "no"} · dispatch {decision.executionDispatched ? "yes" : "no"}
            </div>
            {decision.shadowRunId && (
                <div className="mt-1 break-all font-mono text-[9px] text-muted">
                    run {decision.shadowRunId}
                </div>
            )}
            <HashValue label="Frozen context" value={decision.frozenContextHash} />
        </div>
    );
}

function EventReceipt({ event }: { event: TaskShadowObserverTraceEvent }) {
    const payload = event.payload;
    return (
        <div className="border-l-2 border-border pl-3">
            <div className="flex items-center justify-between gap-2">
                <div className="text-[11px] font-medium text-ink">
                    Event {event.sequence}
                </div>
                <ObserverStatus state={event.status} />
            </div>
            <div className="mt-1 text-[9px] text-muted">
                {formatDateTime(event.occurredAt)}
            </div>
            {payload.reason && (
                <p className="mt-1 text-[10px] text-muted">{payload.reason}</p>
            )}
            {payload.duplicate && (
                <div className="mt-2 space-y-2 border-l-2 border-success pl-3">
                    <p className="text-[10px] font-medium text-ink">
                        Already handled by the original task check
                    </p>
                    <HashValue
                        label="Original task check"
                        value={payload.duplicate.originalTraceId}
                    />
                    <HashValue
                        label="Original root"
                        value={payload.duplicate.originalRootHash}
                    />
                    <HashValue
                        label="Original event"
                        value={payload.duplicate.originalEventHash}
                    />
                    <HashValue
                        label="Exact input"
                        value={payload.duplicate.inputIdentityHash}
                    />
                </div>
            )}
            <div className="mt-2 grid grid-cols-2 gap-x-3 gap-y-2 sm:grid-cols-4">
                <LabelValue label="First task check" value={payload.firstRunDecision ?? "-"} />
                <LabelValue label="Possible task starts" value={payload.onsetWindowCount ?? 0} />
                <LabelValue label="Task-start evidence" value={payload.retainedOnsetEvidenceCount ?? 0} />
                <LabelValue label="Follow-up checks" value={payload.continuation?.scannedRunCount ?? 0} />
                <LabelValue
                    label="Learning restored"
                    value={payload.continuation?.materializationRecoveredRunIds?.length ?? 0}
                />
            </div>
            <div className="mt-2 grid gap-2">
                <HashValue label="Previous hash" value={event.previousHash} />
                <HashValue label="Payload hash" value={event.payloadHash} />
            </div>
            {(payload.diagnostics?.length > 0 || payload.errors?.length > 0) && (
                <div className="mt-2 space-y-1 text-[9px] text-muted">
                    {payload.diagnostics?.map((item) => (
                        <div key={`diagnostic-${item}`}>diagnostic: {item}</div>
                    ))}
                    {payload.errors?.map((item) => (
                        <div key={`error-${item}`} className="text-error">error: {item}</div>
                    ))}
                </div>
            )}
        </div>
    );
}

function ObserverDetail({ detail, loading, onOpenTrace }: {
    detail: TaskShadowObserverDebugDetail | null;
    loading: boolean;
    onOpenTrace: (traceID: string) => void;
}) {
    if (loading) {
        return (
            <div className="flex justify-center py-10 text-muted">
                <Loader2 className="h-5 w-5 animate-spin" />
            </div>
        );
    }
    if (!detail) {
        return <div className="py-8 text-center text-xs text-muted">Select a task detection record.</div>;
    }
    const { summary, trace } = detail;
    const payload = trace.latestEvent.payload;
    const duplicate = payload.duplicate;
    return (
        <div className="space-y-5">
            <section>
                <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                        <h3 className="text-sm font-semibold text-ink">Task detection record</h3>
                        <div className="mt-1 break-all font-mono text-[9px] text-muted">
                            {trace.id}
                        </div>
                    </div>
                    <ObserverStatus state={observerTraceState(summary)} />
                </div>
                <div className={`mt-3 flex items-start gap-2 border-l-2 pl-3 text-[10px] ${summary.integrityValid ? "border-success text-muted" : "border-error text-error"}`}>
                    {summary.integrityValid ? (
                        <CheckCircle2 className="h-3.5 w-3.5 shrink-0 text-success" />
                    ) : (
                        <ShieldAlert className="h-3.5 w-3.5 shrink-0" />
                    )}
                    <span>{summary.integrityValid ? "Root and event hash chain verified." : summary.integrityReason || "Integrity failed."}</span>
                </div>
            </section>

            {duplicate && (
                <section className="border-y border-success/40 py-3">
                    <div className="flex flex-wrap items-start justify-between gap-3">
                        <div className="min-w-0">
                            <h3 className="text-[11px] font-semibold text-ink">
                                Already handled
                            </h3>
                            <p className="mt-1 max-w-xl text-[10px] leading-4 text-muted">
                                This exact retained activity was already processed by an
                                integrity-valid task check. Isoform recorded this source
                                without running task detection again.
                            </p>
                        </div>
                        <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            onClick={() => onOpenTrace(duplicate.originalTraceId)}
                        >
                            <Link2 className="h-3.5 w-3.5" />
                            Open original task check
                        </Button>
                    </div>
                </section>
            )}

            <section className="border-t border-border pt-3">
                <h3 className="mb-2 text-[11px] font-semibold text-ink">Why this activity was checked</h3>
                <div className="grid grid-cols-2 gap-x-3 gap-y-3 sm:grid-cols-4">
                    <LabelValue label="Decision" value={observerAdmissionLabel(trace.admission)} />
                    <LabelValue label="Check type" value={observerFlowLabel(trace.flow)} />
                    <LabelValue label="Enabled" value={trace.shadowEnabledAtAdmission ? "yes" : "no"} />
                    <LabelValue label="Source" value={trace.source} />
                    <LabelValue label="Pipeline run" value={trace.sourcePipelineRunId || "unlinked"} />
                    <LabelValue label="Sessions" value={trace.sessionCount} />
                    <LabelValue label="Declared events" value={trace.declaredEventCount} />
                    <LabelValue label="Retained events" value={trace.retainedEventCount} />
                    <LabelValue label="Created" value={formatDateTime(trace.createdAt)} />
                </div>
                <p className="mt-3 text-[10px] text-muted">
                    {trace.admissionReason || "No admission reason recorded."}
                </p>
                <div className="mt-3 space-y-2">
                    <HashValue label="Group ID" value={trace.groupId} />
                    <HashValue label="Session IDs" value={(trace.sessionIds ?? []).join(", ")} />
                    <HashValue label="Session manifest" value={trace.sessionManifestHash} />
                    <HashValue label="Retained event manifest" value={trace.retainedEventManifestHash} />
                    <HashValue label="Exact input" value={trace.inputIdentityHash} />
                    <HashValue label="Root hash" value={trace.rootHash} />
                    <HashValue label="Runtime instance" value={trace.runtimeInstanceId} />
                </div>
            </section>

            <section className="border-t border-border pt-3">
                <h3 className="mb-2 text-[11px] font-semibold text-ink">
                    {duplicate ? "Duplicate-work boundary" : "What Isoform found"}
                </h3>
                {duplicate && (
                    <p className="mb-3 text-[10px] text-muted">
                        No new task, model call, private practice, learning, permission,
                        or action was created from this duplicate source.
                    </p>
                )}
                <div className="grid grid-cols-2 gap-x-3 gap-y-3 sm:grid-cols-4">
                    <LabelValue label="First task check" value={payload.firstRunDecision ?? "-"} />
                    <LabelValue label="Possible task starts" value={payload.onsetWindowCount ?? 0} />
                    <LabelValue label="Task-start evidence" value={payload.retainedOnsetEvidenceCount ?? 0} />
                    <LabelValue label="Observed tasks" value={payload.taskEpisodeIds?.length ?? 0} />
                    <LabelValue label="Live task checks" value={payload.productionOnsetAttemptIds?.length ?? 0} />
                    <LabelValue label="Replay checks" value={payload.diagnosticOnsetAttemptIds?.length ?? 0} />
                    <LabelValue label="Completion checks" value={payload.lifecycleAttemptIds?.length ?? 0} />
                    <LabelValue label="Private practice" value={payload.shadowRunIds?.length ?? 0} />
                </div>
                {payload.firstRunReason && (
                    <p className="mt-2 text-[10px] text-muted">{payload.firstRunReason}</p>
                )}
            </section>

            <section className="border-t border-border pt-3">
                <h3 className="mb-2 text-[11px] font-semibold text-ink">Observed task</h3>
                {(payload.workflowDecisions ?? []).length === 0 ? (
                    <p className="text-[10px] text-muted">
                        {duplicate
                            ? "This duplicate source did not run another task check."
                            : "No supported task was found in this activity."}
                    </p>
                ) : (
                    <div className="space-y-3">
                        {payload.workflowDecisions.map((decision, index) => (
                            <WorkflowDecision
                                key={`${decision.source}-${decision.taskEpisodeId}-${decision.state}-${index}`}
                                decision={decision}
                            />
                        ))}
                    </div>
                )}
            </section>

            <section className="border-t border-border pt-3">
                <h3 className="mb-2 text-[11px] font-semibold text-ink">Model calls</h3>
                {detail.attemptLinks.length === 0 ? (
                    <p className="text-[10px] text-muted">
                        {duplicate
                            ? "No new model attempt was made for this duplicate source."
                            : "No model attempt was linked."}
                    </p>
                ) : (
                    <div className="space-y-3">
                        {detail.attemptLinks.map((attempt) => (
                            <div key={`${attempt.role}-${attempt.id}`} className="border-l-2 border-border pl-3">
                                <div className="flex items-start justify-between gap-2">
                                    <div className="min-w-0">
                                        <div className="text-[10px] font-medium text-ink">{attempt.role}</div>
                                        <div className="break-all font-mono text-[9px] text-muted">{attempt.id}</div>
                                    </div>
                                    <ObserverStatus state={attempt.found ? attempt.status || "found" : "missing"} />
                                </div>
                                {attempt.found ? (
                                    <div className="mt-1 text-[9px] text-muted">
                                        {attempt.phase} · {attempt.model} · {attempt.promptVersion}
                                    </div>
                                ) : (
                                    <div className="mt-1 text-[9px] text-warning">{attempt.missingReason}</div>
                                )}
                                <HashValue label="Prompt hash" value={attempt.promptHash} />
                                <HashValue label="Input hash" value={attempt.inputHash} />
                            </div>
                        ))}
                    </div>
                )}
            </section>

            <section className="border-t border-border pt-3">
                <h3 className="mb-2 text-[11px] font-semibold text-ink">Technical history</h3>
                <div className="space-y-4">
                    {detail.events.map((event) => (
                        <EventReceipt key={event.id} event={event} />
                    ))}
                </div>
            </section>
        </div>
    );
}

function CoverageDetail({ item }: { item: TaskShadowObserverCoverageItem | null }) {
    if (!item) {
        return <div className="py-8 text-center text-xs text-muted">Select a source link check.</div>;
    }
    return (
        <div className="space-y-5">
            <section>
                <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                        <h3 className="text-sm font-semibold text-ink">Source link check</h3>
                        <div className="mt-1 break-all font-mono text-[9px] text-muted">{item.id}</div>
                    </div>
                    <ObserverStatus state={item.state} />
                </div>
                <p className="mt-3 border-l-2 border-border pl-3 text-[10px] text-muted">
                    {item.reason}
                </p>
            </section>

            <section className="border-t border-border pt-3">
                <h3 className="mb-2 text-[11px] font-semibold text-ink">Activity source link</h3>
                <div className="grid grid-cols-2 gap-x-3 gap-y-3 sm:grid-cols-4">
                    <LabelValue label="Pipeline run" value={item.pipelineRunId || "-"} />
                    <LabelValue label="Admission" value={item.admission || "-"} />
                    <LabelValue label="Expected flow" value={item.expectedFlow || "-"} />
                    <LabelValue label="Trace flow" value={item.traceFlow || "-"} />
                    <LabelValue label="Dispatch" value={item.dispatchStatus || "-"} />
                    <LabelValue label="Trace state" value={item.traceState || "-"} />
                    <LabelValue label="Source started" value={formatDateTime(item.sourceStartedAt)} />
                    <LabelValue label="Trace created" value={formatDateTime(item.traceCreatedAt)} />
                </div>
                {item.dispatchReason && (
                    <p className="mt-3 text-[10px] text-muted">{item.dispatchReason}</p>
                )}
                <div className="mt-3 space-y-2">
                    <HashValue label="Group ID" value={item.groupId} />
                    <HashValue label="Observer trace" value={item.traceId} />
                </div>
            </section>

            <section className="border-t border-border pt-3">
                <h3 className="mb-2 text-[11px] font-semibold text-ink">Integrity</h3>
                <div className={`flex items-start gap-2 border-l-2 pl-3 text-[10px] ${item.integrityValid ? "border-success text-muted" : "border-border text-muted"}`}>
                    {item.integrityValid ? (
                        <CheckCircle2 className="h-3.5 w-3.5 shrink-0 text-success" />
                    ) : (
                        <Link2 className="h-3.5 w-3.5 shrink-0" />
                    )}
                    <span>
                        {item.integrityValid
                            ? "Immutable root and event chain verified."
                            : item.integrityReason || "No integrity-valid trace is available for this source record."}
                    </span>
                </div>
            </section>
        </div>
    );
}

export function TaskShadowObserverDebugSettings({
    initialTraceID,
}: {
    initialTraceID?: string;
}) {
    const [view, setView] = useState<"traces" | "coverage">("traces");
    const [snapshot, setSnapshot] = useState<TaskShadowObserverDebugSnapshot>(emptySnapshot);
    const [coverage, setCoverage] = useState<TaskShadowObserverCoverageSnapshot>(emptyCoverageSnapshot);
    const [selectedTraceID, setSelectedTraceID] = useState<string | null>(
        initialTraceID ?? null,
    );
    const [selectedCoverageID, setSelectedCoverageID] = useState<string | null>(null);
    const [compactDetailOpen, setCompactDetailOpen] = useState(
        Boolean(initialTraceID),
    );
    const [detail, setDetail] = useState<TaskShadowObserverDebugDetail | null>(null);
    const [loading, setLoading] = useState(true);
    const [detailLoading, setDetailLoading] = useState(false);
    const [query, setQuery] = useState("");
    const [status, setStatus] = useState("all");
    const [error, setError] = useState("");

    const loadSnapshot = useCallback(async () => {
        setLoading(true);
        setError("");
        try {
            const [next, nextCoverage] = await Promise.all([
                GetTaskShadowObserverDebugSnapshot(200),
                GetTaskShadowObserverCoverageSnapshot(200),
            ]);
            const source = next ?? emptySnapshot;
            const normalized = {
                ...source,
                traces: Array.isArray(source.traces) ? source.traces : [],
            };
            const coverageSource = nextCoverage ?? emptyCoverageSnapshot;
            const normalizedCoverage = {
                ...coverageSource,
                items: Array.isArray(coverageSource.items) ? coverageSource.items : [],
            };
            setSnapshot(normalized);
            setCoverage(normalizedCoverage);
            setSelectedTraceID((current) =>
                current === initialTraceID ||
                normalized.traces.some((item) => item.id === current)
                    ? current
                    : normalized.traces[0]?.id ?? null,
            );
            setSelectedCoverageID((current) =>
                normalizedCoverage.items.some((item) => item.id === current)
                    ? current
                    : normalizedCoverage.items[0]?.id ?? null,
            );
        } catch (loadError) {
            setError(String(loadError));
        } finally {
            setLoading(false);
        }
    }, [initialTraceID]);

    const loadDetail = useCallback(async (traceID: string) => {
        setDetailLoading(true);
        setError("");
        try {
            setDetail(await GetTaskShadowObserverDebugDetail(traceID));
        } catch (loadError) {
            setDetail(null);
            setError(String(loadError));
        } finally {
            setDetailLoading(false);
        }
    }, []);

    useEffect(() => {
        void loadSnapshot();
    }, [loadSnapshot]);

    useEffect(() => {
        if (!selectedTraceID) {
            setDetail(null);
            return;
        }
        void loadDetail(selectedTraceID);
    }, [loadDetail, selectedTraceID]);

    useEffect(() => {
        if (!initialTraceID) return;
        setView("traces");
        setSelectedTraceID(initialTraceID);
        setCompactDetailOpen(true);
    }, [initialTraceID]);

    useEffect(() => {
        const refresh = () => {
            void loadSnapshot();
            if (selectedTraceID) void loadDetail(selectedTraceID);
        };
        const unsubscribers = [
            EventsOn("shadow_agent:observer_trace", refresh),
            EventsOn("shadow_agent:task_pipeline", refresh),
            EventsOn("activity_pipeline:run", refresh),
        ];
        return () => unsubscribers.forEach((unsubscribe) => unsubscribe());
    }, [loadDetail, loadSnapshot, selectedTraceID]);

    const traceStatuses = useMemo(
        () =>
            Array.from(
                new Set(snapshot.traces.map((item) => observerTraceState(item))),
            ).sort(),
        [snapshot.traces],
    );
    const coverageStatuses = useMemo(
        () => Array.from(new Set(coverage.items.map((item) => item.state))).sort(),
        [coverage.items],
    );
    const normalizedQuery = query.trim().toLowerCase();
    const traces = useMemo(
        () => snapshot.traces.filter((item) => {
            if (status !== "all" && observerTraceState(item) !== status) return false;
            if (!normalizedQuery) return true;
            return `${item.id} ${item.sourcePipelineRunId} ${item.groupId} ${item.flow} ${item.admission} ${item.admissionReason ?? ""} ${item.terminalReason ?? ""} ${item.inputIdentityHash ?? ""} ${item.duplicateOfTraceId ?? ""} ${item.duplicatePolicyVersion ?? ""}`
                .toLowerCase()
                .includes(normalizedQuery);
        }),
        [normalizedQuery, snapshot.traces, status],
    );
    const coverageItems = useMemo(
        () => coverage.items.filter((item) => {
            if (status !== "all" && item.state !== status) return false;
            if (!normalizedQuery) return true;
            return `${item.id} ${item.pipelineRunId ?? ""} ${item.traceId ?? ""} ${item.groupId ?? ""} ${item.admission ?? ""} ${item.state} ${item.reason}`
                .toLowerCase()
                .includes(normalizedQuery);
        }),
        [coverage.items, normalizedQuery, status],
    );
    const selectedCoverageItem = useMemo(
        () => coverage.items.find((item) => item.id === selectedCoverageID) ?? null,
        [coverage.items, selectedCoverageID],
    );
    const statuses = view === "traces" ? traceStatuses : coverageStatuses;
    const alreadyHandledCount = snapshot.traces.filter(
        (item) => Boolean(item.duplicateOfTraceId),
    ).length;
    const traceMetrics: Array<[string, number]> = [
        ["checks", snapshot.counts.total],
        ["in progress", snapshot.counts.running],
        ["interrupted", snapshot.counts.interrupted],
        ["completed", snapshot.counts.completed],
        ["with errors", snapshot.counts.completedWithErrors],
        ["already handled", alreadyHandledCount],
        ["skipped", Math.max(0, snapshot.counts.skipped - alreadyHandledCount)],
        ["canceled", snapshot.counts.cancelled],
        ["failed", snapshot.counts.failed],
        ["corrupt records", snapshot.counts.corrupt],
    ];
    const coverageMetrics: Array<[string, number]> = [
        ["sources", coverage.counts.instrumentedSources],
        ["older sources", coverage.counts.legacySources],
        ["check records", coverage.counts.currentTraces],
        ["linked", coverage.counts.covered],
        ["root only", coverage.counts.coveredRootOnly],
        ["removed", coverage.counts.sourcePruned],
        ["pending", coverage.counts.pendingDispatch],
        ["failed", coverage.counts.dispatchFailed + coverage.counts.traceMissing],
        ["mismatched", coverage.counts.linkMismatch + coverage.counts.duplicateTrace],
        ["corrupt", coverage.counts.traceCorrupt],
        ["unlinked", coverage.counts.unlinkedTrace],
        ["partial", coverage.counts.scanTruncated],
    ];

    const refresh = () => {
        void loadSnapshot();
        if (selectedTraceID) void loadDetail(selectedTraceID);
    };

    const changeView = (next: "traces" | "coverage") => {
        setView(next);
        setQuery("");
        setStatus("all");
        setCompactDetailOpen(false);
    };

    return (
        <div>
            <div className="settings-card--padded space-y-3 border-b border-border">
                <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                        <h2 className="text-[12px] font-semibold text-ink">Task detection</h2>
                        <p className="mt-0.5 text-[10px] text-muted">
                            See when Isoform checked activity for a task, what it found, and whether the source record is complete.
                        </p>
                        <p className="mt-1 text-[9px] text-muted">
                            Metadata only. These records never become task evidence, profile data, permission, or an action.
                        </p>
                    </div>
                    <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        onClick={refresh}
                        disabled={loading || detailLoading}
                    >
                        {loading || detailLoading ? (
                            <Loader2 className="h-3.5 w-3.5 animate-spin" />
                        ) : (
                            <RefreshCw className="h-3.5 w-3.5" />
                        )}
                        <span className="ml-1.5">Refresh</span>
                    </Button>
                </div>
                <div role="tablist" aria-label="Task detection details" className="flex border-b border-border">
                    {(["traces", "coverage"] as const).map((item) => (
                        <button
                            key={item}
                            type="button"
                            role="tab"
                            aria-selected={view === item}
                            className={`border-b-2 px-3 py-1.5 text-[10px] font-medium ${view === item ? "border-ink text-ink" : "border-transparent text-muted hover:text-ink"}`}
                            onClick={() => changeView(item)}
                        >
                            {item === "traces" ? "Task checks" : "Source links"}
                        </button>
                    ))}
                </div>
                {view === "coverage" && (
                    <div role="status" className="flex items-start justify-between gap-3 border-l-2 border-border pl-3">
                        <p className="text-[10px] text-muted">{coverageHealthReason(coverage)}</p>
                        <ObserverStatus state={coverage.health} />
                    </div>
                )}
                <div className={`grid grid-cols-3 gap-y-2 border-y border-border py-2 ${view === "coverage" ? "sm:grid-cols-6" : "sm:grid-cols-5"}`}>
                    {(view === "traces" ? traceMetrics : coverageMetrics).map(([label, value]) => (
                        <div key={label} className="border-l border-border px-2 first:border-l-0 first:pl-0">
                            <div className="whitespace-nowrap text-[9px] uppercase text-muted">{label}</div>
                            <div className="text-sm font-semibold tabular-nums text-ink">{value}</div>
                        </div>
                    ))}
                </div>
                <div className="flex gap-2">
                    <div className="relative min-w-0 flex-1">
                        <Search aria-hidden="true" className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted" />
                        <input
                            aria-label={view === "traces" ? "Search task checks" : "Search source links"}
                            value={query}
                            onChange={(event) => setQuery(event.target.value)}
                            className="h-8 w-full border border-border bg-panel pl-7 pr-2 text-xs text-ink outline-none focus:border-ink/40"
                            placeholder={view === "traces" ? "Search activity, decision, state, or ID" : "Search source, check, state, or ID"}
                        />
                    </div>
                    <select
                        aria-label={view === "traces" ? "Filter task checks by status" : "Filter source links by status"}
                        value={status}
                        onChange={(event) => setStatus(event.target.value)}
                        className="h-8 w-[160px] border border-border bg-panel px-2 text-xs text-ink"
                    >
                        <option value="all">All statuses</option>
                        {statuses.map((item) => (
                            <option key={item} value={item}>{observerStatusLabel(item)}</option>
                        ))}
                    </select>
                </div>
                {error && (
                    <div role="alert" className="flex gap-2 border-l-2 border-error pl-3 text-[10px] text-error">
                        <AlertCircle className="h-3.5 w-3.5 shrink-0" />
                        <span>{error}</span>
                    </div>
                )}
            </div>

            {view === "traces" ? (
                <DebugMasterDetail
                    compactDetailOpen={compactDetailOpen}
                    detailKey={selectedTraceID}
                    detailLabel="Task check details"
                    listLabel="Task checks"
                    onBack={() => setCompactDetailOpen(false)}
                    list={
                        <>
                            {loading ? (
                                <div className="flex justify-center p-6 text-muted">
                                    <Loader2 className="h-5 w-5 animate-spin" />
                                </div>
                            ) : traces.length === 0 ? (
                                <div className="p-4 text-xs text-muted">
                                    Isoform has not checked any retained activity for tasks yet.
                                </div>
                            ) : traces.map((trace: TaskShadowObserverDebugSummary) => (
                                <button
                                    key={trace.id}
                                    type="button"
                                    aria-current={selectedTraceID === trace.id ? "true" : undefined}
                                    className={`w-full border-b border-border px-3 py-3 text-left hover:bg-ink/[0.03] ${selectedTraceID === trace.id ? "bg-ink/[0.05]" : ""}`}
                                    onClick={() => {
                                        setSelectedTraceID(trace.id);
                                        setCompactDetailOpen(true);
                                    }}
                                >
                                    <div className="flex items-start justify-between gap-2">
                                        <div className="min-w-0">
                                            <div
                                                className="truncate text-xs font-medium text-ink"
                                                title={`${trace.admission} · ${trace.flow}`}
                                            >
                                                {trace.duplicateOfTraceId
                                                    ? `Already handled · ${observerFlowLabel(trace.flow)}`
                                                    : `${observerAdmissionLabel(trace.admission)} · ${observerFlowLabel(trace.flow)}`}
                                            </div>
                                            <div className="mt-0.5 truncate font-mono text-[9px] text-muted">
                                                {trace.groupId}
                                            </div>
                                        </div>
                                        <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted" />
                                    </div>
                                    <p className="mt-1 line-clamp-2 text-[10px] text-muted">
                                        {trace.terminalReason || trace.admissionReason || "Waiting for this detection check to finish."}
                                    </p>
                                    <div className="mt-2 flex items-center justify-between gap-2">
                                        <ObserverStatus state={observerTraceState(trace)} />
                                        <span className="text-[9px] tabular-nums text-muted">
                                            source {trace.sourcePipelineRunId || "-"} · {trace.retainedEventCount} of {trace.declaredEventCount} events kept · {trace.taskEpisodeCount} tasks · {trace.shadowRunCount} shadow attempts
                                        </span>
                                    </div>
                                </button>
                            ))}
                        </>
                    }
                    detail={
                        <ObserverDetail
                            detail={detail}
                            loading={detailLoading}
                            onOpenTrace={(traceID) => {
                                setSelectedTraceID(traceID);
                                setCompactDetailOpen(true);
                            }}
                        />
                    }
                />
            ) : (
                <DebugMasterDetail
                    compactDetailOpen={compactDetailOpen}
                    detailKey={selectedCoverageID}
                    detailLabel="Source link details"
                    listLabel="Source links"
                    onBack={() => setCompactDetailOpen(false)}
                    list={
                        <>
                            {loading ? (
                                <div className="flex justify-center p-6 text-muted">
                                    <Loader2 className="h-5 w-5 animate-spin" />
                                </div>
                            ) : coverageItems.length === 0 ? (
                                <div className="p-4 text-xs text-muted">
                                    {coverage.counts.legacySources > 0
                                        ? `Waiting for the first post-instrumentation activity group. ${coverage.counts.legacySources} legacy group records are excluded from health.`
                                        : "Waiting for the first post-instrumentation activity group."}
                                </div>
                            ) : coverageItems.map((item) => (
                                <button
                                    key={item.id}
                                    type="button"
                                    aria-current={selectedCoverageID === item.id ? "true" : undefined}
                                    className={`w-full border-b border-border px-3 py-3 text-left hover:bg-ink/[0.03] ${selectedCoverageID === item.id ? "bg-ink/[0.05]" : ""}`}
                                    onClick={() => {
                                        setSelectedCoverageID(item.id);
                                        setCompactDetailOpen(true);
                                    }}
                                >
                                    <div className="flex items-start justify-between gap-2">
                                        <div className="min-w-0">
                                            <div className="truncate text-xs font-medium text-ink">
                                                {item.admission || item.kind} · source {item.pipelineRunId || "unlinked"}
                                            </div>
                                            <div className="mt-0.5 truncate font-mono text-[9px] text-muted">
                                                {item.traceId || item.groupId || item.id}
                                            </div>
                                        </div>
                                        <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted" />
                                    </div>
                                    <p className="mt-1 line-clamp-2 text-[10px] text-muted">{item.reason}</p>
                                    <div className="mt-2 flex items-center justify-between gap-2">
                                        <ObserverStatus state={item.state} />
                                        <span className="text-[9px] text-muted">
                                            {item.dispatchStatus || "no source writeback"} · {item.traceState || "no trace"}
                                        </span>
                                    </div>
                                </button>
                            ))}
                        </>
                    }
                    detail={<CoverageDetail item={selectedCoverageItem} />}
                />
            )}
        </div>
    );
}
