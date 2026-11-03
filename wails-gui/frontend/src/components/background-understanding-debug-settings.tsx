import {
    ChevronRight,
    Database,
    Loader2,
    RefreshCw,
    ShieldCheck,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import {
    backgroundLearningRunStatusLabel,
    backgroundLearningRunSummary,
    backgroundLearningRunTriggerText,
    backgroundLearningSourceDescription,
    backgroundLearningSourceLastResult,
    backgroundLearningStatusText,
    backgroundLearningUsageText,
} from "@/lib/background-learning-presentation";
import { Button } from "@/components/ui/button";
import {
    GetBackgroundUnderstandingRunDetail,
    GetBackgroundUnderstandingSnapshot,
    type BackgroundUnderstandingRun,
    type BackgroundUnderstandingSnapshot,
} from "@/lib/wails-shim-app";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { DebugMasterDetail } from "./debug-master-detail";

interface BackgroundRunEvent {
    id: string;
    sequence: number;
    state: string;
    detailJson: string;
    detailHash: string;
    occurredAt: number;
}

interface BackgroundSourceItem {
    id: string;
    source: string;
    sourceItemKind: string;
    externalIdHash: string;
    threadIdHash?: string;
    contentHash: string;
    attributionKind: string;
    observedAt?: number;
    metadataJson: string;
}

interface BackgroundMaterialization {
    id: string;
    artifactKind: string;
    artifactId: string;
    action: string;
    summary: string;
    sourceReceiptHash?: string;
}

interface BackgroundRunDetail {
    run: {
        id: string;
        missionKind: string;
        source: string;
        triggerKind: string;
        triggerReason: string;
        grantId?: string;
        policyVersion: string;
        sourceScopeJson: string;
        currentState: string;
        startedAt: number;
        updatedAt: number;
        detailJson: string;
    };
    events: BackgroundRunEvent[];
    sourceItems: BackgroundSourceItem[];
    materializations: BackgroundMaterialization[];
}

function isSnapshot(value: unknown): value is BackgroundUnderstandingSnapshot {
    if (!value || typeof value !== "object") return false;
    const candidate = value as Partial<BackgroundUnderstandingSnapshot>;
    return (
        typeof candidate.enabled === "boolean" &&
        typeof candidate.status === "string" &&
        Array.isArray(candidate.sources) &&
        Array.isArray(candidate.runs)
    );
}

function isRunDetail(value: unknown): value is BackgroundRunDetail {
    if (!value || typeof value !== "object") return false;
    const candidate = value as Partial<BackgroundRunDetail>;
    return (
        Boolean(candidate.run?.id) &&
        Array.isArray(candidate.events) &&
        Array.isArray(candidate.sourceItems) &&
        Array.isArray(candidate.materializations)
    );
}

function jsonValue(value: string) {
    try {
        return JSON.stringify(JSON.parse(value), null, 2);
    } catch {
        return value;
    }
}

function statusLabel(value: string): string {
    switch (value) {
        case "waiting_for_source":
            return "Waiting for an allowed source";
        case "limit_reached":
            return "Daily limit reached";
        case "working":
            return "Learning now";
        case "ready":
            return "Ready";
        case "paused":
            return "Paused";
        case "waiting":
            return "Waiting for capacity";
        case "selected":
            return "Ready to start";
        case "running":
            return "Learning now";
        case "completed":
            return "Completed";
        case "skipped":
            return "Skipped";
        case "failed":
            return "Failed";
        case "cancelled":
            return "Canceled";
        default:
            return value.replaceAll("_", " ");
    }
}

function sourceLabel(value: string): string {
    switch (value) {
        case "gmail":
            return "Gmail";
        case "local_activity":
            return "Retained activity";
        default:
            return value.replaceAll("_", " ");
    }
}

function eventLabel(value: string): string {
    const labels: Record<string, string> = {
        selected: "Source selected",
        running: "Learning started",
        completed: "Learning completed",
        skipped: "No new learning needed",
        failed: "Learning could not finish",
        cancelled: "Learning stopped",
    };
    return labels[value] ?? statusLabel(value);
}

function sourceItemLabel(value: string): string {
    const labels: Record<string, string> = {
        message_metadata: "Email details",
        work_pattern_aggregate: "Work-pattern summary",
    };
    return labels[value] ?? statusLabel(value);
}

function savedLearningLabel(action: string, artifactKind: string): string {
    const actionLabels: Record<string, string> = {
        created: "Created",
        updated: "Updated",
        retained: "Kept",
        skipped: "No change to",
    };
    const artifactLabels: Record<string, string> = {
        knowledge: "reusable know-how",
        memory: "memory",
        self_model: "About you",
        work_pattern_profile: "work pattern",
    };
    return `${actionLabels[action] ?? statusLabel(action)} ${
        artifactLabels[artifactKind] ?? artifactKind.replaceAll("_", " ")
    }`;
}

function historicalSourceAccess(
    sourceScopeJson: string,
    runSource: string,
): string {
    if (runSource === "local_activity") {
        return "Activity already retained on this device.";
    }
    try {
        const scope = JSON.parse(sourceScopeJson) as Record<string, unknown>;
        if (typeof scope.scopeLabel === "string" && scope.scopeLabel.trim()) {
            return `The allowed Gmail view covered ${scope.scopeLabel
                .trim()
                .toLowerCase()}.`;
        }
    } catch {
        // Exact malformed scope remains available in Technical details.
    }
    return "The Gmail view allowed when this learning check began.";
}

function EmptyDetail() {
    return (
        <div className="flex h-full min-h-48 items-center justify-center p-5 text-xs text-muted">
            Select a background learning run.
        </div>
    );
}

function TransportReceipt({ events }: { events: BackgroundRunEvent[] }) {
    let receipt: Record<string, unknown> | null = null;
    for (let index = events.length - 1; index >= 0; index -= 1) {
        try {
            const parsed = JSON.parse(events[index]!.detailJson) as Record<
                string,
                unknown
            >;
            if (parsed.transport) {
                receipt = parsed;
                break;
            }
        } catch {
            // Lifecycle JSON is also rendered raw below.
        }
    }
    if (!receipt) return null;
    const actions = Array.isArray(receipt.actions)
        ? (receipt.actions as Array<Record<string, unknown>>)
        : [];
    return (
        <section>
            <h4 className="font-semibold text-ink">
                How Isoform accessed the source
            </h4>
            <dl className="mt-2 grid grid-cols-2 gap-3 border-y border-border py-3 text-[10px]">
                <div>
                    <dt className="text-muted">Access method</dt>
                    <dd className="mt-1 text-ink">
                        {receipt.transport === "computer_use"
                            ? "Browser control"
                            : statusLabel(String(receipt.transport))}
                    </dd>
                </div>
                <div>
                    <dt className="text-muted">Direct account connection</dt>
                    <dd className="mt-1 text-ink">
                        {receipt.oauthFallback === true ? "Used" : "Not used"}
                    </dd>
                </div>
                <div>
                    <dt className="text-muted">Browser</dt>
                    <dd className="mt-1 text-ink">
                        {String(receipt.browser ?? "-")}
                    </dd>
                </div>
                <div>
                    <dt className="text-muted">Screen-reading status</dt>
                    <dd className="mt-1 text-ink">
                        {receipt.axStatus === "populated"
                            ? "Available"
                            : statusLabel(String(receipt.axStatus ?? "unknown"))}
                    </dd>
                </div>
            </dl>
            <details className="mt-2 text-[9px] text-muted">
                <summary className="cursor-pointer select-none font-medium hover:text-ink">
                    Technical details
                </summary>
                <div className="mt-1 space-y-1 break-all font-mono text-muted">
                    <div>transport {String(receipt.transport)}</div>
                    <div>oauth fallback {String(receipt.oauthFallback)}</div>
                    <div>screen reading {String(receipt.axStatus ?? "-")}</div>
                    <div>snapshot {String(receipt.snapshotHash ?? "-")}</div>
                </div>
                {actions.length > 0 && (
                    <div className="mt-2 border-t border-border">
                        {actions.map((action, index) => (
                            <div
                                key={`${String(action.tool)}-${index}`}
                                className="flex justify-between gap-3 border-b border-border py-2 text-[10px]"
                            >
                                <span className="font-mono text-ink">
                                    {String(action.tool)}
                                </span>
                                <span className="text-muted">
                                    {String(action.outcome)}
                                </span>
                            </div>
                        ))}
                    </div>
                )}
            </details>
        </section>
    );
}

export function BackgroundUnderstandingDebugSettings() {
    const [snapshot, setSnapshot] =
        useState<BackgroundUnderstandingSnapshot | null>(null);
    const [selectedID, setSelectedID] = useState<string | null>(null);
    const [detail, setDetail] = useState<BackgroundRunDetail | null>(null);
    const [loading, setLoading] = useState(true);
    const [detailLoading, setDetailLoading] = useState(false);
    const [compactDetailOpen, setCompactDetailOpen] = useState(false);

    const load = useCallback(async () => {
        setLoading(true);
        try {
            const value = await GetBackgroundUnderstandingSnapshot();
            if (!isSnapshot(value)) {
                throw new Error("Background learning returned incomplete data.");
            }
            setSnapshot(value);
            setSelectedID((current) =>
                value.runs.some((run) => run.id === current)
                    ? current
                    : value.runs[0]?.id ?? null,
            );
        } catch (error) {
            toast.error(`Background learning load failed: ${String(error)}`);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void load();
        return EventsOn("agency:updated", () => void load());
    }, [load]);

    useEffect(() => {
        if (!selectedID) {
            setDetail(null);
            return;
        }
        let cancelled = false;
        setDetailLoading(true);
        GetBackgroundUnderstandingRunDetail(selectedID)
            .then((value) => {
                if (!cancelled) {
                    if (!isRunDetail(value)) {
                        throw new Error("Background learning details are incomplete.");
                    }
                    setDetail(value);
                }
            })
            .catch((error) => {
                if (!cancelled) {
                    setDetail(null);
                    toast.error(
                        `Background learning details could not be loaded: ${String(error)}`,
                    );
                }
            })
            .finally(() => {
                if (!cancelled) setDetailLoading(false);
            });
        return () => {
            cancelled = true;
        };
    }, [selectedID]);

    const selected = useMemo(
        () => snapshot?.runs.find((run) => run.id === selectedID) ?? null,
        [selectedID, snapshot?.runs],
    );
    return (
        <div className="min-h-0">
            <div className="settings-card--padded border-b border-border">
                <div className="flex items-start justify-between gap-3">
                    <div>
                        <h3 className="text-sm font-semibold text-ink">
                            What did Isoform learn in the background?
                        </h3>
                        <p className="mt-1 text-xs text-muted">
                            See which allowed sources Isoform can learn from, why a source ran or waited, and what it saved.
                        </p>
                    </div>
                    <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        onClick={() => void load()}
                        disabled={loading}
                    >
                        {loading ? (
                            <Loader2 className="h-3.5 w-3.5 animate-spin" />
                        ) : (
                            <RefreshCw className="h-3.5 w-3.5" />
                        )}
                        Refresh
                    </Button>
                </div>
                {snapshot && (
                    <>
                        <div className="mt-4 grid grid-cols-2 border-y border-border py-2 sm:grid-cols-4">
                            <div>
                                <div className="text-[10px] text-muted">
                                    Status
                                </div>
                                <div
                                    className="mt-0.5 text-xs font-semibold text-ink"
                                    title={snapshot.status}
                                >
                                    {statusLabel(snapshot.status)}
                                </div>
                            </div>
                            <div className="border-l border-border pl-3">
                                <div className="text-[10px] text-muted">
                                    Learning checks today
                                </div>
                                <div className="mt-0.5 text-xs font-semibold tabular-nums text-ink">
                                    {backgroundLearningUsageText(snapshot)}
                                </div>
                            </div>
                            <div className="border-l border-border pl-3">
                                <div className="text-[10px] text-muted">
                                    Checks attempted
                                </div>
                                <div className="mt-0.5 text-xs font-semibold tabular-nums text-ink">
                                    {snapshot.todayAttemptCount}
                                </div>
                            </div>
                            <div className="border-l border-border pl-3">
                                <div className="text-[10px] text-muted">
                                    Checks set aside
                                </div>
                                <div className="mt-0.5 text-xs font-semibold tabular-nums text-ink">
                                    {snapshot.reservedRunCount}
                                    {snapshot.reservedSource
                                        ? ` for ${sourceLabel(snapshot.reservedSource)}`
                                        : ""}
                                </div>
                            </div>
                        </div>
                        <p className="mt-2 text-xs leading-5 text-muted">
                            {backgroundLearningStatusText(snapshot.status)}
                        </p>
                        <details className="mt-1 text-[9px] text-muted">
                            <summary className="cursor-pointer select-none font-medium hover:text-ink">
                                Technical details
                            </summary>
                            <div className="mt-1 space-y-1 break-all font-mono text-muted">
                                <div>{snapshot.statusReason || "No raw status reason."}</div>
                                <div>{snapshot.schedulingPolicyVersion}</div>
                                <div>
                                    minimum interval {snapshot.minIntervalMinutes} minutes
                                </div>
                            </div>
                        </details>
                        <section
                            aria-label="Allowed learning sources"
                            className="mt-3 grid gap-2 sm:grid-cols-2"
                        >
                            {snapshot.sources.map((source) => {
                                const lastResult =
                                    backgroundLearningSourceLastResult(source);
                                return (
                                    <article
                                        key={source.source}
                                        className="flex items-start gap-2 border border-border p-2.5"
                                    >
                                        <ShieldCheck className="mt-0.5 h-3.5 w-3.5 text-muted" />
                                        <div className="min-w-0">
                                            <div className="text-xs font-medium text-ink">
                                                {source.label}
                                            </div>
                                            <div className="mt-0.5 text-[10px] text-muted">
                                                {source.allowed
                                                    ? "Learning allowed"
                                                    : "Learning not allowed"}
                                                {" · "}
                                                {source.available &&
                                                source.connected
                                                    ? "Ready"
                                                    : "Not ready"}
                                            </div>
                                            <p className="mt-1 text-[10px] leading-4 text-muted">
                                                {backgroundLearningSourceDescription(
                                                    source,
                                                )}
                                            </p>
                                            {lastResult && (
                                                <p className="mt-1 text-[9px] leading-4 text-muted">
                                                    Last check: {lastResult}
                                                </p>
                                            )}
                                            {source.grantId && (
                                                <details className="mt-1 text-[9px] text-muted">
                                                    <summary className="cursor-pointer select-none font-medium hover:text-ink">
                                                        Technical details
                                                    </summary>
                                                    <div className="mt-1 break-all font-mono text-muted">
                                                        grant {source.grantId}
                                                    </div>
                                                </details>
                                            )}
                                        </div>
                                    </article>
                                );
                            })}
                        </section>
                    </>
                )}
            </div>

            <DebugMasterDetail
                compactDetailOpen={compactDetailOpen}
                detailKey={selectedID}
                detailLabel="Background learning details"
                listLabel="Background learning history"
                onBack={() => setCompactDetailOpen(false)}
                list={
                    !snapshot || snapshot.runs.length === 0 ? (
                        <div className="p-4 text-xs text-muted">
                            No background learning has started.
                        </div>
                    ) : (
                        snapshot.runs.map(
                            (run: BackgroundUnderstandingRun) => (
                                <button
                                    key={run.id}
                                    type="button"
                                    aria-current={
                                        selectedID === run.id
                                            ? "true"
                                            : undefined
                                    }
                                    className={`w-full border-b border-border px-3 py-3 text-left hover:bg-ink/[0.03] ${
                                        selectedID === run.id
                                            ? "bg-ink/[0.05]"
                                            : ""
                                    }`}
                                    onClick={() => {
                                        setSelectedID(run.id);
                                        setCompactDetailOpen(true);
                                    }}
                                >
                                    <div className="flex items-center justify-between gap-2">
                                        <span className="truncate text-xs font-medium text-ink">
                                            {sourceLabel(run.source)}
                                        </span>
                                        <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted" />
                                    </div>
                                    <p className="mt-1 line-clamp-2 text-[11px] text-muted">
                                        {backgroundLearningRunSummary(run)}
                                    </p>
                                    <div className="mt-1.5 flex justify-between gap-2 text-[10px] text-muted">
                                        <span>{backgroundLearningRunStatusLabel(run.state)}</span>
                                        <span className="tabular-nums">
                                            {run.sourceItemCount}{" "}
                                            {run.sourceItemCount === 1
                                                ? "source record"
                                                : "source records"}{" "}
                                            / {run.materializationCount}{" "}
                                            {run.materializationCount === 1
                                                ? "learning item"
                                                : "learning items"}
                                        </span>
                                    </div>
                                </button>
                            ),
                        )
                    )
                }
                detail={
                    detailLoading ? (
                        <div className="flex h-full min-h-48 items-center justify-center">
                            <Loader2 className="h-4 w-4 animate-spin text-muted" />
                        </div>
                    ) : !selected || !detail ? (
                        <EmptyDetail />
                    ) : (
                        <div className="space-y-5 p-4 text-xs">
                            <section>
                                <h4 className="font-semibold text-ink">
                                    {sourceLabel(selected.source)}
                                </h4>
                                <p className="mt-1 text-muted">
                                    {backgroundLearningRunTriggerText(selected)}
                                </p>
                                <details className="mt-2 text-[10px] text-muted">
                                    <summary className="cursor-pointer select-none font-medium hover:text-ink">
                                        Technical details
                                    </summary>
                                    <div className="mt-1 space-y-1 break-all font-mono text-muted">
                                        <div>run {detail.run.id}</div>
                                        <div>grant {detail.run.grantId || "none"}</div>
                                        <div>{detail.run.policyVersion}</div>
                                        <div>{selected.triggerReason}</div>
                                        <div>{selected.summary}</div>
                                        <div>{jsonValue(detail.run.detailJson)}</div>
                                    </div>
                                </details>
                            </section>
                            <section>
                                <h4 className="font-semibold text-ink">
                                    What Isoform could access
                                </h4>
                                <p className="mt-1 text-muted">
                                    {historicalSourceAccess(
                                        detail.run.sourceScopeJson,
                                        selected.source,
                                    )}
                                </p>
                                <details className="mt-2 text-[10px] text-muted">
                                    <summary className="cursor-pointer select-none font-medium hover:text-ink">
                                        Technical details
                                    </summary>
                                    <pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap border border-border bg-surface-subtle p-2 font-mono text-[10px]">
                                        {jsonValue(detail.run.sourceScopeJson)}
                                    </pre>
                                </details>
                            </section>
                            <TransportReceipt events={detail.events} />
                            <section>
                                <h4 className="font-semibold text-ink">
                                    What happened
                                </h4>
                                <div className="mt-2 border-t border-border">
                                    {detail.events.map((event) => (
                                        <div
                                            key={event.id}
                                            className="border-b border-border py-2"
                                        >
                                            <div className="flex justify-between gap-2">
                                                <span className="font-medium text-ink">
                                                    {event.sequence}.{" "}
                                                    {eventLabel(event.state)}
                                                </span>
                                            </div>
                                            <details className="mt-1 text-[9px] text-muted">
                                                <summary className="cursor-pointer select-none font-medium hover:text-ink">
                                                    Technical details
                                                </summary>
                                                <div className="mt-1 break-all font-mono text-muted">
                                                    {event.state} · {event.detailHash}
                                                </div>
                                                <pre className="mt-1 whitespace-pre-wrap font-mono text-[9px] text-muted">
                                                    {jsonValue(event.detailJson)}
                                                </pre>
                                            </details>
                                        </div>
                                    ))}
                                </div>
                            </section>
                            <section>
                                <h4 className="flex items-center gap-2 font-semibold text-ink">
                                    <Database className="h-3.5 w-3.5" />
                                    Information reviewed
                                </h4>
                                <div className="mt-2 border-t border-border">
                                    {detail.sourceItems.length === 0 ? (
                                        <p className="py-2 text-muted">None.</p>
                                    ) : (
                                        detail.sourceItems.map((item) => (
                                            <div
                                                key={item.id}
                                                className="border-b border-border py-2"
                                            >
                                                <div className="font-medium text-ink">
                                                    {sourceItemLabel(
                                                        item.sourceItemKind,
                                                    )}
                                                </div>
                                                <details className="mt-1 text-[9px] text-muted">
                                                    <summary className="cursor-pointer select-none font-medium hover:text-ink">
                                                        Technical details
                                                    </summary>
                                                    <div className="mt-1 break-all font-mono text-muted">
                                                        {item.sourceItemKind} ·{" "}
                                                        {item.attributionKind}
                                                    </div>
                                                    <div className="mt-1 break-all font-mono text-muted">
                                                        id {item.externalIdHash}{" "}
                                                        content {item.contentHash}
                                                    </div>
                                                    <pre className="mt-1 whitespace-pre-wrap font-mono text-[9px] text-muted">
                                                        {jsonValue(
                                                            item.metadataJson,
                                                        )}
                                                    </pre>
                                                </details>
                                            </div>
                                        ))
                                    )}
                                </div>
                            </section>
                            <section>
                                <h4 className="font-semibold text-ink">
                                    Saved learning
                                </h4>
                                <div className="mt-2 border-t border-border">
                                    {detail.materializations.length === 0 ? (
                                        <p className="py-2 text-muted">None.</p>
                                    ) : (
                                        detail.materializations.map(
                                            (item) => (
                                                <div
                                                    key={item.id}
                                                    className="border-b border-border py-2"
                                                >
                                                    <div className="font-medium text-ink">
                                                        {savedLearningLabel(
                                                            item.action,
                                                            item.artifactKind,
                                                        )}
                                                    </div>
                                                    {item.summary && (
                                                        <p className="mt-1 text-muted">
                                                            {item.summary}
                                                        </p>
                                                    )}
                                                    <details className="mt-1 text-[9px] text-muted">
                                                        <summary className="cursor-pointer select-none font-medium hover:text-ink">
                                                            Technical details
                                                        </summary>
                                                        <div className="mt-1 break-all font-mono text-muted">
                                                            {item.action} · {item.artifactKind} · {item.artifactId}
                                                        </div>
                                                        {item.sourceReceiptHash && (
                                                            <div className="mt-1 break-all font-mono text-muted">
                                                                source receipt {item.sourceReceiptHash}
                                                            </div>
                                                        )}
                                                    </details>
                                                </div>
                                            ),
                                        )
                                    )}
                                </div>
                            </section>
                        </div>
                    )
                }
            />
        </div>
    );
}
