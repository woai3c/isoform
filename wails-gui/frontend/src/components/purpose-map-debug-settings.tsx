import { useCallback, useEffect, useMemo, useState } from "react";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import {
    CheckCircle2,
    ChevronRight,
    CircleAlert,
    Database,
    FileSearch,
    Loader2,
    RefreshCw,
    Search,
    Target,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { DebugMasterDetail } from "@/components/debug-master-detail";
import { cn } from "@/lib/utils";
import {
    GetPurposeDebugExtractionRun,
    GetPurposeDebugOpenLoop,
    GetPurposeDebugSnapshot,
    type PurposeDebugExtractionRunDetail,
    type PurposeDebugExtractionRunSummary,
    type PurposeDebugOpenLoopDetail,
    type PurposeDebugOpenLoopSummary,
    type PurposeDebugSnapshot,
    type PurposeUnderstandingRuntimeSnapshot,
} from "@/lib/wails-shim-app";

type PurposeDebugView = "open-loops" | "extraction";
type PurposeDebugDetail =
    | { kind: "open-loops"; value: PurposeDebugOpenLoopDetail }
    | { kind: "extraction"; value: PurposeDebugExtractionRunDetail };

const views: Array<{
    id: PurposeDebugView;
    label: string;
    icon: typeof Target;
}> = [
    { id: "open-loops", label: "Current priorities", icon: Target },
    { id: "extraction", label: "How Isoform found them", icon: FileSearch },
];

const viewContent: Record<PurposeDebugView, {
    searchLabel: string;
    searchPlaceholder: string;
    filterLabel: string;
    listLabel: string;
    detailLabel: string;
    loadingLabel: string;
    emptyDetailLabel: string;
}> = {
    "open-loops": {
        searchLabel: "Search priorities",
        searchPlaceholder: "Search priority, outcome, or ID",
        filterLabel: "Filter priorities",
        listLabel: "Current priorities",
        detailLabel: "Current priority details",
        loadingLabel: "Loading priorities...",
        emptyDetailLabel: "Select a priority to inspect its evidence and history.",
    },
    extraction: {
        searchLabel: "Search how priorities were found",
        searchPlaceholder: "Search source, model, result, or ID",
        filterLabel: "Filter how priorities were found",
        listLabel: "How priorities were found",
        detailLabel: "Priority source details",
        loadingLabel: "Loading priority sources...",
        emptyDetailLabel: "Select an entry to see what Isoform considered and used.",
    },
};

function formatTime(value: number): string {
    return value > 0 ? new Date(value).toLocaleString() : "Unknown";
}

function compactHash(value?: string): string {
    if (!value) return "Not recorded";
    return value.length > 20 ? `${value.slice(0, 10)}...${value.slice(-8)}` : value;
}

function primaryStateLabel(value: string): string {
    const labels: Record<string, string> = {
        no_change: "No change",
        changed: "Updated",
        recovered: "Recovered",
        no_sources: "No usable sources",
        no_useful_work: "No useful work",
        provenance_failed: "Evidence check failed",
        not_applicable: "Not applicable",
        no_match: "No match",
        included: "Used",
        excluded: "Not used",
        eligible: "Used to guide work",
        active: "Current",
        waiting: "Waiting for more evidence",
        candidate: "Possible priority",
        resolved: "Resolved",
        contradicted: "Conflicts with evidence",
        dismissed: "Dismissed",
        superseded: "Replaced",
    };
    if (labels[value]) return labels[value];
    const label = value.replaceAll("_", " ").trim();
    return label ? label.charAt(0).toUpperCase() + label.slice(1) : "Unknown";
}

function StatusPill({
    label,
    positive = false,
    warning = false,
}: {
    label: string;
    positive?: boolean;
    warning?: boolean;
}) {
    return (
        <span
            title={label}
            className={cn(
                "inline-flex min-h-5 items-center border px-1.5 text-[9px] font-semibold",
                positive && "border-success/25 bg-success/10 text-success",
                warning && "border-warning/25 bg-warning/10 text-warning",
                !positive && !warning && "border-border bg-panel-warm text-muted",
            )}
        >
            {primaryStateLabel(label)}
        </span>
    );
}

function HashLine({ label, value }: { label: string; value?: string }) {
    return (
        <div className="grid min-w-0 grid-cols-[96px_minmax(0,1fr)] gap-3 py-1 text-[10px] sm:grid-cols-[120px_minmax(0,1fr)]">
            <span className="text-muted">{label}</span>
            <code className="min-w-0 break-all text-ink" title={value}>
                {compactHash(value)}
            </code>
        </div>
    );
}

function JsonBlock({ label, value }: { label: string; value: string }) {
    let display = value || "{}";
    try {
        display = JSON.stringify(JSON.parse(display), null, 2);
    } catch {
        // The backend already validates canonical JSON where the contract requires it.
    }
    return (
        <section>
            <h4 className="text-[9px] font-semibold text-muted">{label}</h4>
            <pre className="mt-2 max-h-72 overflow-auto whitespace-pre-wrap break-all border border-border bg-panel-warm p-3 font-mono text-[9px] leading-4 text-ink">
                {display}
            </pre>
        </section>
    );
}

function EmptyDetail({ children }: { children: string }) {
    return <div className="py-12 text-center text-xs leading-5 text-muted">{children}</div>;
}

function PurposeUnderstandingRuntime({
    runtime,
}: {
    runtime: PurposeUnderstandingRuntimeSnapshot;
}) {
    const cycle = runtime.current ?? runtime.last;
    const state = cycle?.state ?? (runtime.started ? "idle" : "unavailable");
    return (
        <section aria-labelledby="purpose-understanding-runtime-heading" className="border border-border">
            <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border px-4 py-3">
                <div>
                    <div className="flex flex-wrap items-center gap-2">
                        <h3 id="purpose-understanding-runtime-heading" className="text-[11px] font-semibold text-ink">
                            When did Isoform last look for priorities?
                        </h3>
                        <StatusPill
                            label={primaryStateLabel(state)}
                            positive={["changed", "recovered", "no_change"].includes(state)}
                            warning={["failed", "unavailable", "cancelled"].includes(state)}
                        />
                        {runtime.running && <Loader2 className="h-3.5 w-3.5 animate-spin text-info" />}
                    </div>
                    <p className="mt-1 text-[9px] leading-4 text-muted">
                        Shows the latest permitted background check. This view cannot change data, grant permission, or start work.
                    </p>
                </div>
                <span className="text-[9px] text-muted">
                    {runtime.pendingTriggers.length} check{runtime.pendingTriggers.length === 1 ? "" : "s"} waiting
                </span>
            </div>
            {cycle ? (
                <div className="grid gap-4 px-4 py-3 lg:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)]">
                    <div>
                        <div className="grid grid-cols-2 gap-3 text-[10px] sm:grid-cols-4 lg:grid-cols-2">
                            <div><div className="text-muted">Sources checked</div><b className="text-ink">{cycle.scannedCount}</b></div>
                            <div><div className="text-muted">Sources used</div><b className="text-ink">{cycle.includedCount}</b></div>
                            <div><div className="text-muted">Current priorities</div><b className="text-ink">{cycle.currentPurposeCount}</b></div>
                            <div><div className="text-muted">Duration</div><b className="text-ink">{cycle.durationMilliseconds ?? 0} ms</b></div>
                        </div>
                        <div className="mt-3 text-[9px] leading-4 text-muted">
                            <div>{cycle.cached ? "No source changes since the last check" : "New source changes found"}</div>
                            <div>{cycle.completedAt ? `Completed ${formatTime(cycle.completedAt)}` : `Started ${formatTime(cycle.startedAt ?? 0)}`}</div>
                            {cycle.triggerKinds.length > 0 && <div>Started by: {cycle.triggerKinds.join(", ")}</div>}
                            {runtime.pendingTriggers.length > 0 && <div>Waiting: {runtime.pendingTriggers.join(", ")}</div>}
                        </div>
                    </div>
                    <div className="min-w-0 border-t border-border pt-2 lg:border-l lg:border-t-0 lg:pl-4 lg:pt-0">
                        <HashLine label="Fingerprint" value={cycle.fingerprint} />
                        <HashLine label="Learning run" value={cycle.runId} />
                        <div className="mt-2 space-y-1 break-all font-mono text-[9px] text-muted">
                            {cycle.model && <div>model · {cycle.model}</div>}
                            {cycle.changedRootIds.length > 0 && <div>changed roots · {cycle.changedRootIds.join(", ")}</div>}
                            {cycle.changedRelationIds.length > 0 && <div>changed relations · {cycle.changedRelationIds.join(", ")}</div>}
                            {cycle.recoveredRunIds.length > 0 && <div>recovered runs · {cycle.recoveredRunIds.join(", ")}</div>}
                        </div>
                        {cycle.error && (
                            <p className="mt-2 text-[10px] leading-4 text-danger">{cycle.error}</p>
                        )}
                    </div>
                </div>
            ) : (
                <p className="px-4 py-3 text-[10px] leading-4 text-muted">
                    Isoform has not completed a priority-learning check since this app started.
                </p>
            )}
        </section>
    );
}

function OpenLoopDetail({
    detail,
    onOpenRoot,
}: {
    detail: PurposeDebugOpenLoopDetail;
    onOpenRoot: (id: string) => void;
}) {
    const { summary, product } = detail;
    return (
        <div className="space-y-6">
            <section>
                <div className="flex flex-wrap items-start justify-between gap-3">
                    <div className="min-w-0">
                        <div className="flex flex-wrap items-center gap-2">
                            <StatusPill label={product.purpose.kind} />
                            <StatusPill
                                label={summary.eligibility.decision}
                                positive={summary.eligibility.eligible}
                                warning={!summary.eligibility.eligible}
                            />
                            <StatusPill label={product.purpose.lifecycleState} />
                            <StatusPill label={product.purpose.supportState} />
                        </div>
                        <h3 className="mt-3 text-base font-semibold text-ink">
                            {product.purpose.title}
                        </h3>
                        <p className="mt-2 text-xs leading-5 text-muted">
                            {product.purpose.desiredOutcome}
                        </p>
                    </div>
                    <span className="font-mono text-[9px] text-muted">
                        {product.purpose.id}
                    </span>
                </div>
            </section>

            <section className="border-t border-border pt-4">
                <h4 className="text-[9px] font-semibold text-muted">
                    Eligibility decision
                </h4>
                <div className="mt-3 grid grid-cols-2 gap-3 text-[10px] sm:grid-cols-4">
                    <div>
                        <div className="text-muted">Current evidence</div>
                        <div className="mt-1 font-semibold text-ink">
                            {summary.eligibility.currentEvidenceCount}
                        </div>
                    </div>
                    <div>
                        <div className="text-muted">Independent sources</div>
                        <div className="mt-1 font-semibold text-ink">
                            {summary.eligibility.independentKeys.length}
                        </div>
                    </div>
                    <div>
                        <div className="text-muted">Explicit support</div>
                        <div className="mt-1 font-semibold text-ink">
                            {summary.eligibility.hasExplicitSupport ? "Yes" : "No"}
                        </div>
                    </div>
                    <div>
                        <div className="text-muted">Duplicate targets</div>
                        <div className="mt-1 font-semibold text-ink">
                            {summary.eligibility.duplicateOfIds.length}
                        </div>
                    </div>
                </div>
                <ul className="mt-3 space-y-1 text-[10px] leading-4 text-muted">
                    {summary.eligibility.reasons.map((reason) => (
                        <li key={reason}>• {reason}</li>
                    ))}
                </ul>
                {summary.eligibility.independentKeys.length > 0 && (
                    <div className="mt-3 flex flex-wrap gap-1">
                        {summary.eligibility.independentKeys.map((key) => (
                            <code
                                key={key}
                                className="min-w-0 max-w-full break-all border border-border bg-panel-warm px-1.5 py-1 text-[9px] text-ink"
                            >
                                {key}
                            </code>
                        ))}
                    </div>
                )}
                <div className="mt-3 border-t border-border pt-2">
                    <HashLine
                        label="Projection ref"
                        value={summary.eligibility.projectionReference}
                    />
                    <HashLine
                        label="Projection hash"
                        value={summary.eligibility.projectionHash}
                    />
                    <HashLine label="Root payload" value={summary.rootPayloadHash} />
                </div>
            </section>

            <section className="border-t border-border pt-4">
                <h4 className="text-[9px] font-semibold text-muted">
                    Immutable events · {product.events.length}
                </h4>
                <div className="mt-2 divide-y divide-border border-y border-border">
                    {product.events.map((event) => (
                        <div key={event.id} className="py-3">
                            <div className="flex flex-wrap items-center justify-between gap-2">
                                <div className="flex items-center gap-2">
                                    <span className="text-[10px] font-semibold text-ink">
                                        {event.sequence}. {event.eventType.replaceAll("_", " ")}
                                    </span>
                                    <StatusPill label={event.lifecycleState} />
                                    <StatusPill label={event.supportState} />
                                </div>
                                <time className="text-[9px] text-muted">
                                    {event.occurredAt}
                                </time>
                            </div>
                            <p className="mt-2 text-[10px] leading-4 text-ink">
                                {event.desiredOutcome}
                            </p>
                            {event.rationale ? (
                                <p className="mt-1 text-[10px] leading-4 text-muted">
                                    Reason: {event.rationale}
                                </p>
                            ) : (
                                <p className="mt-1 text-[10px] text-muted">
                                    Reasoning unknown
                                </p>
                            )}
                            <HashLine label="Event" value={event.id} />
                            <HashLine label="Evidence set" value={event.evidenceSetHash} />
                            <HashLine label="Payload" value={event.payloadHash} />
                        </div>
                    ))}
                </div>
            </section>

            <section className="border-t border-border pt-4">
                <h4 className="text-[9px] font-semibold text-muted">
                    Evidence provenance · {product.evidence.length}
                </h4>
                {product.evidence.length === 0 ? (
                    <p className="mt-2 text-[10px] text-muted">No evidence links.</p>
                ) : (
                    <div className="mt-2 divide-y divide-border border-y border-border">
                        {product.evidence.map((evidence) => (
                            <div key={evidence.id} className="py-3">
                                <div className="flex flex-wrap items-center gap-2">
                                    <span className="text-[10px] font-semibold text-ink">
                                        {evidence.sourceKind} · {evidence.sourceId}
                                    </span>
                                    <StatusPill label={evidence.evidenceClass} />
                                    <StatusPill label={evidence.stance} />
                                </div>
                                <p className="mt-2 break-all font-mono text-[9px] text-muted">
                                    Independence: {evidence.independenceKey}
                                </p>
                                <HashLine label="Source" value={evidence.sourceHash} />
                                <HashLine label="Excerpt" value={evidence.excerptHash} />
                                <HashLine label="Payload" value={evidence.payloadHash} />
                            </div>
                        ))}
                    </div>
                )}
            </section>

            <section className="border-t border-border pt-4">
                <h4 className="text-[9px] font-semibold text-muted">
                    Relations · {product.relations.length}
                </h4>
                {product.relations.length === 0 ? (
                    <p className="mt-2 text-[10px] text-muted">No related priorities.</p>
                ) : (
                    <div className="mt-2 divide-y divide-border border-y border-border">
                        {product.relations.map((relation) => (
                            <button
                                key={relation.id}
                                type="button"
                                className="flex w-full items-center justify-between gap-3 py-3 text-left hover:bg-ink/[0.025]"
                                onClick={() => onOpenRoot(relation.relatedPurposeId)}
                            >
                                <div className="min-w-0">
                                    <div className="text-[10px] font-semibold text-ink">
                                        {relation.relationType.replaceAll("_", " ")} · {relation.direction}
                                    </div>
                                    <div className="mt-1 truncate text-[10px] text-muted">
                                        {relation.relatedTitle || relation.relatedPurposeId}
                                    </div>
                                </div>
                                <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted" />
                            </button>
                        ))}
                    </div>
                )}
            </section>

            <JsonBlock label="Immutable root payload" value={detail.rootPayloadJson} />
        </div>
    );
}

function ExtractionDetail({ detail }: { detail: PurposeDebugExtractionRunDetail }) {
    const { summary } = detail;
    const currentPurpose = detail.currentPurpose ?? [];
    const currentPurposeCaptured = detail.manifestSummary?.currentPurposeCount !== undefined;
    return (
        <div className="space-y-6">
            <section>
                <div className="flex flex-wrap items-center justify-between gap-2">
                    <div className="flex items-center gap-2">
                        <StatusPill
                            label={summary.status}
                            positive={summary.status === "succeeded"}
                            warning={summary.status === "failed"}
                        />
                        <span className="text-[10px] text-muted">{summary.model}</span>
                    </div>
                    <time className="text-[9px] text-muted">
                        {formatTime(summary.createdAt)}
                    </time>
                </div>
                <h3 className="mt-3 break-all font-mono text-[11px] font-semibold text-ink">
                    {summary.id}
                </h3>
                {summary.error && (
                    <p className="mt-2 text-[10px] leading-4 text-danger">{summary.error}</p>
                )}
                <div className="mt-4 grid grid-cols-2 gap-3 text-[10px] sm:grid-cols-4">
                    <div><div className="text-muted">Scanned</div><b className="text-ink">{summary.scannedCount}</b></div>
                    <div><div className="text-muted">Included</div><b className="text-ink">{summary.includedCount}</b></div>
                    <div><div className="text-muted">Hypotheses</div><b className="text-ink">{summary.hypothesisCount}</b></div>
                    <div><div className="text-muted">Records</div><b className="text-ink">{summary.receiptCount}</b></div>
                </div>
                <div className="mt-3 border-t border-border pt-2">
                    <HashLine label="Input" value={summary.inputHash} />
                    <HashLine label="Manifest" value={summary.manifestHash} />
                    <HashLine label="Prompt" value={summary.promptHash} />
                    <HashLine label="Raw output" value={summary.rawOutputHash} />
                    <HashLine label="Output" value={summary.outputHash} />
                    <HashLine label="Run payload" value={summary.payloadHash} />
                </div>
                <p className="mt-3 text-[9px] leading-4 text-muted">
                    Raw source bodies, model prompt text, and raw model output are intentionally omitted.
                </p>
            </section>

            <section className="border-t border-border pt-4">
                <h4 className="text-[9px] font-semibold text-muted">
                    Frozen priority context · {currentPurpose.length}
                </h4>
                {currentPurposeCaptured ? (
                    <>
                        <p className="mt-2 text-[9px] leading-4 text-muted">
                            Complete priority state at the learning cutoff. It is comparison context for reconciliation, never supporting evidence for a hypothesis.
                        </p>
                        <div className="mt-2 grid grid-cols-2 gap-3 text-[10px] sm:grid-cols-4">
                            <div><div className="text-muted">Entries</div><b className="text-ink">{detail.manifestSummary?.currentPurposeCount ?? 0}</b></div>
                            <div><div className="text-muted">Used</div><b className="text-ink">{detail.manifestSummary?.currentPurposeUsedChars ?? 0} chars</b></div>
                            <div><div className="text-muted">Budget</div><b className="text-ink">{detail.manifestSummary?.currentPurposeBudgetChars ?? 0} chars</b></div>
                            <div><div className="text-muted">Eligibility</div><b className="text-ink">{currentPurpose.filter((entry) => entry.eligible).length} eligible</b></div>
                        </div>
                        <HashLine label="Context" value={detail.manifestSummary?.currentPurposeHash} />
                        {currentPurpose.length > 0 && (
                            <div className="mt-2 divide-y divide-border border-y border-border">
                                {currentPurpose.map((entry) => (
                                    <div key={entry.ref} className="py-3">
                                        <div className="flex flex-wrap items-center gap-2">
                                            <span className="text-[10px] font-semibold text-ink">
                                                {entry.title}
                                            </span>
                                            <StatusPill label={entry.kind} />
                                            <StatusPill label={entry.lifecycleState} positive={entry.eligible} />
                                        </div>
                                        <p className="mt-2 text-[10px] leading-4 text-muted">
                                            {entry.desiredOutcome}
                                        </p>
                                        <p className="mt-2 break-all font-mono text-[9px] text-muted">
                                            {entry.ref}
                                        </p>
                                        <HashLine label="Projection" value={entry.projectionHash} />
                                    </div>
                                ))}
                            </div>
                        )}
                    </>
                ) : (
                    <p className="mt-2 text-[9px] leading-4 text-muted">
                        Historical learning version; current priority context was not captured.
                    </p>
                )}
            </section>

            <section className="border-t border-border pt-4">
                <h4 className="text-[9px] font-semibold text-muted">
                    Frozen sources · {detail.sources.length}
                </h4>
                <div className="mt-2 divide-y divide-border border-y border-border">
                    {detail.sources.map((source) => (
                        <div key={source.ref} className="py-3">
                            <div className="flex flex-wrap items-center gap-2">
                                <span className="text-[10px] font-semibold text-ink">
                                    {source.sourceKind} · {source.sourceId}
                                </span>
                                <StatusPill label={source.evidenceClass} />
                                <StatusPill label={source.privacyDecision} />
                            </div>
                            <p className="mt-2 break-all font-mono text-[9px] text-muted">
                                {source.independenceKey}
                            </p>
                            <HashLine label="Source" value={source.sourceHash} />
                            <HashLine label="Prompt content" value={source.promptContentHash} />
                        </div>
                    ))}
                </div>
            </section>

            <section className="border-t border-border pt-4">
                <h4 className="text-[9px] font-semibold text-muted">
                    Complete source manifest · {detail.manifest.length}
                </h4>
                {detail.manifestSummary && (
                    <p className="mt-2 text-[9px] leading-4 text-muted">
                        {detail.manifestSummary.scannedCount} scanned · {detail.manifestSummary.includedCount} included · {detail.manifestSummary.excludedCount} excluded · {detail.manifestSummary.usedChars}/{detail.manifestSummary.budgetChars} chars
                    </p>
                )}
                <div className="mt-2 divide-y divide-border border-y border-border">
                    {detail.manifest.map((entry, index) => (
                        <div key={`${entry.ref}:${index}`} className="py-3">
                            <div className="flex flex-wrap items-center justify-between gap-2">
                                <span className="break-all font-mono text-[9px] text-ink">
                                    {entry.ref}
                                </span>
                                <StatusPill
                                    label={entry.decision}
                                    positive={entry.decision === "included"}
                                    warning={entry.decision === "excluded"}
                                />
                            </div>
                            {entry.reason && (
                                <p className="mt-1 text-[10px] leading-4 text-muted">
                                    {entry.reason}
                                </p>
                            )}
                            <HashLine label="Source" value={entry.sourceHash} />
                            <HashLine label="Prompt content" value={entry.promptContentHash} />
                        </div>
                    ))}
                </div>
            </section>

            <section className="border-t border-border pt-4">
                <h4 className="text-[9px] font-semibold text-muted">
                    Hypotheses · {detail.hypotheses.length}
                </h4>
                <div className="mt-2 divide-y divide-border border-y border-border">
                    {detail.hypotheses.map((hypothesis) => (
                        <div key={hypothesis.id} className="py-3">
                            <div className="flex flex-wrap items-center gap-2">
                                <span className="text-[10px] font-semibold text-ink">
                                    {hypothesis.title}
                                </span>
                                <StatusPill label={hypothesis.operation} />
                                <StatusPill label={hypothesis.reasoningStatus} />
                            </div>
                            <p className="mt-2 text-[10px] leading-4 text-muted">
                                {hypothesis.desiredOutcome}
                            </p>
                            <div className="mt-2 font-mono text-[9px] text-muted">
                                {hypothesis.evidenceRefs.length} exact evidence refs · confidence {Math.round(hypothesis.confidence * 100)}%
                            </div>
                            <HashLine label="Payload" value={hypothesis.payloadHash} />
                        </div>
                    ))}
                </div>
            </section>

            <section className="border-t border-border pt-4">
                <h4 className="text-[9px] font-semibold text-muted">
                    Reconciliation records · {detail.receipts.length}
                </h4>
                <div className="mt-2 divide-y divide-border border-y border-border">
                    {detail.receipts.map((receipt) => (
                        <div key={receipt.id} className="py-3">
                            <div className="flex flex-wrap items-center gap-2">
                                <StatusPill
                                    label={receipt.disposition}
                                    positive={["created", "reinforced", "revised", "resolved"].includes(receipt.disposition)}
                                    warning={receipt.disposition === "rejected"}
                                />
                                <span className="font-mono text-[9px] text-muted">
                                    {receipt.hypothesisId}
                                </span>
                            </div>
                            {receipt.reason && (
                                <p className="mt-2 text-[10px] leading-4 text-muted">
                                    {receipt.reason}
                                </p>
                            )}
                            <HashLine label="Source set" value={receipt.sourceSetHash} />
                            <HashLine label="Payload" value={receipt.payloadHash} />
                        </div>
                    ))}
                </div>
            </section>
        </div>
    );
}

function listCount(snapshot: PurposeDebugSnapshot, view: PurposeDebugView): number {
    switch (view) {
        case "open-loops": return snapshot.counts.openLoops;
        case "extraction": return snapshot.counts.extractionRuns;
    }
}

export function PurposeMapDebugSettings() {
    const [snapshot, setSnapshot] = useState<PurposeDebugSnapshot | null>(null);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);
    const [view, setView] = useState<PurposeDebugView>("open-loops");
    const [query, setQuery] = useState("");
    const [status, setStatus] = useState("all");
    const [selectedId, setSelectedId] = useState<string | null>(null);
    const [detail, setDetail] = useState<PurposeDebugDetail | null>(null);
    const [detailLoading, setDetailLoading] = useState(false);
    const [detailError, setDetailError] = useState<string | null>(null);
    const [compactDetailOpen, setCompactDetailOpen] = useState(false);
    const content = viewContent[view];

    const loadSnapshot = useCallback(async (quiet = false) => {
        if (!quiet) setLoading(true);
        setError(null);
        try {
            const next = await GetPurposeDebugSnapshot(200);
            if (!next || !Array.isArray(next.openLoops) || !next.counts) {
                throw new Error("Priority data returned an invalid response");
            }
            setSnapshot(next);
        } catch (loadError) {
            setError(loadError instanceof Error ? loadError.message : String(loadError));
        } finally {
            if (!quiet) setLoading(false);
        }
    }, []);

    useEffect(() => {
        void loadSnapshot();
        return EventsOn("purpose-understanding:updated", () => {
            void loadSnapshot(true);
        });
    }, [loadSnapshot]);

    const visible = useMemo(() => {
        if (!snapshot) return [];
        const needle = query.trim().toLowerCase();
        if (view === "open-loops") {
            return snapshot.openLoops.filter((item) => {
                const statusMatches = status === "all" ||
                    (status === "eligible" ? item.eligibility.eligible :
                        status === "excluded" ? !item.eligibility.eligible :
                            item.purpose.lifecycleState === status);
                const queryMatches = !needle || [
                    item.purpose.title,
                    item.purpose.desiredOutcome,
                    item.purpose.id,
                    item.purpose.kind,
                    item.purpose.canonicalKey,
                ].some((value) => value.toLowerCase().includes(needle));
                return statusMatches && queryMatches;
            });
        }
        return snapshot.extractionRuns.filter((item) =>
            (status === "all" || item.status === status) &&
            (!needle || [item.id, item.model, item.status, item.error ?? ""]
                .some((value) => value.toLowerCase().includes(needle))),
        );
    }, [query, snapshot, status, view]);

    useEffect(() => {
        const first = visible[0];
        const firstId = !first ? null :
            "purpose" in first ? first.purpose.id : first.id;
        const selectedStillVisible = selectedId && visible.some((item) => {
            const id = "purpose" in item ? item.purpose.id : item.id;
            return id === selectedId;
        });
        if (!selectedStillVisible) setSelectedId(firstId);
    }, [selectedId, visible]);

    const loadDetail = useCallback(async (kind: PurposeDebugView, id: string) => {
        setDetailLoading(true);
        setDetailError(null);
        try {
            const value = kind === "open-loops" ? await GetPurposeDebugOpenLoop(id) :
                await GetPurposeDebugExtractionRun(id);
            setDetail({ kind, value } as PurposeDebugDetail);
        } catch (loadError) {
            setDetail(null);
            setDetailError(loadError instanceof Error ? loadError.message : String(loadError));
        } finally {
            setDetailLoading(false);
        }
    }, []);

    useEffect(() => {
        if (!selectedId) {
            setDetail(null);
            setDetailError(null);
            return;
        }
        void loadDetail(view, selectedId);
    }, [loadDetail, selectedId, view]);

    const select = (nextView: PurposeDebugView, id: string) => {
        setView(nextView);
        setStatus("all");
        setQuery("");
        setSelectedId(id);
        setCompactDetailOpen(true);
    };

    const renderList = () => {
        if (loading) {
            return <div className="flex items-center gap-2 p-5 text-xs text-muted"><Loader2 className="h-4 w-4 animate-spin" /> {content.loadingLabel}</div>;
        }
        if (visible.length === 0) {
            return <div className="p-5 text-xs leading-5 text-muted">Nothing in this view matches the current filters.</div>;
        }
        return visible.map((item) => {
            if ("purpose" in item) {
                const root = item as PurposeDebugOpenLoopSummary;
                return (
                    <button key={root.purpose.id} type="button" aria-current={selectedId === root.purpose.id ? "true" : undefined}
                        className={cn("grid w-full grid-cols-[minmax(0,1fr)_auto] gap-3 border-b border-border px-3 py-3 text-left hover:bg-ink/[0.03]", selectedId === root.purpose.id && "bg-ink/[0.05]")}
                        onClick={() => { setSelectedId(root.purpose.id); setCompactDetailOpen(true); }}>
                        <div className="min-w-0">
                            <div className="truncate text-[11px] font-semibold text-ink">{root.purpose.title}</div>
                            <div className="mt-1 flex flex-wrap gap-1"><StatusPill label={root.purpose.lifecycleState} /><StatusPill label={root.eligibility.decision} positive={root.eligibility.eligible} warning={!root.eligibility.eligible} /></div>
                            <div className="mt-2 text-[9px] text-muted">{root.evidenceCount} evidence · {root.eventCount} events</div>
                        </div>
                        <ChevronRight className="mt-1 h-3.5 w-3.5 text-muted" />
                    </button>
                );
            }
            const run = item as PurposeDebugExtractionRunSummary;
            return (
                <button key={run.id} type="button" aria-current={selectedId === run.id ? "true" : undefined}
                    className={cn("grid w-full grid-cols-[minmax(0,1fr)_auto] gap-3 border-b border-border px-3 py-3 text-left hover:bg-ink/[0.03]", selectedId === run.id && "bg-ink/[0.05]")}
                    onClick={() => { setSelectedId(run.id); setCompactDetailOpen(true); }}>
                    <div className="min-w-0"><div className="flex items-center gap-2"><StatusPill label={run.status} positive={run.status === "succeeded"} warning={run.status === "failed"} /><span className="truncate text-[10px] text-muted">{run.model}</span></div><div className="mt-2 text-[10px] text-ink">{run.includedCount}/{run.scannedCount} sources · {run.hypothesisCount} hypotheses</div><div className="mt-1 truncate font-mono text-[9px] text-muted">{run.id}</div></div>
                    <ChevronRight className="mt-1 h-3.5 w-3.5 text-muted" />
                </button>
            );
        });
    };

    const renderDetail = () => {
        if (!selectedId) return <EmptyDetail>{content.emptyDetailLabel}</EmptyDetail>;
        if (detailLoading) return <div className="flex items-center gap-2 py-12 text-xs text-muted"><Loader2 className="h-4 w-4 animate-spin" /> Loading details...</div>;
        if (detailError) return (
            <div role="alert" className="py-10 text-center"><CircleAlert className="mx-auto h-5 w-5 text-danger" /><p className="mt-3 text-xs text-danger">{detailError}</p><Button variant="outline" size="sm" className="mt-4" onClick={() => void loadDetail(view, selectedId)}>Retry</Button></div>
        );
        if (!detail || detail.kind !== view) return <EmptyDetail>{content.emptyDetailLabel}</EmptyDetail>;
        if (detail.kind === "open-loops") return <OpenLoopDetail detail={detail.value} onOpenRoot={(id) => select("open-loops", id)} />;
        return <ExtractionDetail detail={detail.value} />;
    };

    const statusOptions = view === "open-loops" ? ["all", "eligible", "excluded", "active", "waiting", "candidate", "resolved", "contradicted", "dismissed", "superseded"] :
        ["all", "succeeded", "failed", "no_sources"];

    return (
        <div className="space-y-5">
            <div className="flex flex-wrap items-start justify-between gap-4">
                <div>
                    <h2 className="text-xl font-semibold text-ink">How did Isoform learn these priorities?</h2>
                    <p className="mt-1 max-w-2xl text-xs leading-5 text-muted">
                        Inspect the sources Isoform used and the evidence behind each priority.
                    </p>
                </div>
                <Button variant="outline" size="sm" onClick={() => void loadSnapshot()} disabled={loading}>
                    <RefreshCw className={cn("mr-2 h-3.5 w-3.5", loading && "animate-spin")} />
                    Refresh
                </Button>
            </div>

            {error ? (
                <div role="alert" className="border border-danger/25 bg-danger/5 p-4">
                    <div className="flex items-start gap-3"><CircleAlert className="mt-0.5 h-4 w-4 text-danger" /><div><p className="text-xs font-semibold text-danger">Priorities are unavailable</p><p className="mt-1 text-[10px] leading-4 text-muted">{error}</p><Button variant="outline" size="sm" className="mt-3" onClick={() => void loadSnapshot()}>Retry</Button></div></div>
                </div>
            ) : (
                <>
                    {snapshot && (
                        <PurposeUnderstandingRuntime runtime={snapshot.understanding} />
                    )}
                    <div className="grid grid-cols-2 border border-border sm:grid-cols-4">
                        {[
                            ["Priorities found", snapshot?.counts.openLoops ?? 0],
                            ["Used to guide work", snapshot?.counts.eligibleOpenLoops ?? 0],
                            ["Sources used", snapshot?.counts.evidence ?? 0],
                            ["Priority checks", snapshot?.counts.extractionRuns ?? 0],
                        ].map(([label, value]) => (
                            <div key={label} className="min-w-0 border-b border-r border-border px-3 py-3 last:border-r-0 sm:border-b-0">
                                <div className="text-[9px] text-muted">{label}</div>
                                <div className="mt-1 text-base font-semibold text-ink">{value}</div>
                            </div>
                        ))}
                    </div>

                    <div className="border border-border">
                        <div className="flex min-w-0 flex-wrap border-b border-border bg-panel-warm">
                            {views.map((item) => {
                                const Icon = item.icon;
                                return (
                                    <button key={item.id} type="button" aria-current={view === item.id ? "page" : undefined}
                                        className={cn("inline-flex min-h-10 items-center gap-2 border-r border-border px-3 text-[10px] font-semibold text-muted hover:text-ink", view === item.id && "bg-panel text-ink")}
                                        onClick={() => { setView(item.id); setStatus("all"); setQuery(""); setSelectedId(null); setCompactDetailOpen(false); }}>
                                        <Icon className="h-3.5 w-3.5" />{item.label}<span className="text-muted">{snapshot ? listCount(snapshot, item.id) : 0}</span>
                                    </button>
                                );
                            })}
                        </div>
                        <div className="grid gap-2 border-b border-border p-3 lg:grid-cols-[minmax(0,1fr)_190px]">
                            <label className="relative block">
                                <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted" />
                                <input aria-label={content.searchLabel} value={query} onChange={(event) => setQuery(event.target.value)} placeholder={content.searchPlaceholder} className="h-9 w-full border border-border bg-panel pl-9 pr-3 text-xs text-ink outline-none focus:border-ink/40" />
                            </label>
                            <select aria-label={content.filterLabel} value={status} onChange={(event) => setStatus(event.target.value)} className="h-9 border border-border bg-panel px-3 text-xs text-ink outline-none focus:border-ink/40">
                                {statusOptions.map((option) => <option key={option} value={option}>{option === "all" ? "All statuses" : primaryStateLabel(option)}</option>)}
                            </select>
                        </div>
                        {snapshot && view === "extraction" && snapshot.truncated.extractionRuns && (
                            <div className="border-b border-warning/20 bg-warning/5 px-3 py-2 text-[9px] text-warning">
                                Showing the newest 200 records. Counts include the complete history.
                            </div>
                        )}
                        <DebugMasterDetail compactDetailOpen={compactDetailOpen} detailKey={`${view}:${selectedId ?? ""}`} detailLabel={content.detailLabel} listLabel={content.listLabel} onBack={() => setCompactDetailOpen(false)} list={renderList()} detail={renderDetail()} />
                    </div>

                    <div className="flex flex-wrap items-center justify-between gap-3 text-[9px] text-muted">
                        <div className="flex items-center gap-2">
                            {snapshot?.integrityStatus === "validated" ? <CheckCircle2 className="h-3.5 w-3.5 text-success" /> : <Database className="h-3.5 w-3.5" />}
                            Evidence records {snapshot?.integrityStatus === "validated" ? "verified" : snapshot?.integrityStatus ?? "not loaded"}
                        </div>
                        <span>Read only. This screen cannot change priorities, grant permission, or start work.</span>
                    </div>
                </>
            )}
        </div>
    );
}
