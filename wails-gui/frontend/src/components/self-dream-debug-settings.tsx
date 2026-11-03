import {
    AlertCircle,
    CheckCircle2,
    FlaskConical,
    Loader2,
    RefreshCw,
    Search,
} from "lucide-react";
import {
    useCallback,
    useEffect,
    useMemo,
    useState,
    type ReactNode,
} from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import {
    GetSelfDreamDebugRun,
    GetSelfDreamDebugSnapshot,
    ReplaySelfDreamDebug,
} from "@/lib/wails-shim-app";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { DebugMasterDetail } from "./debug-master-detail";

interface SelfDreamRunSummary {
    id: string;
    status: string;
    triggerKind: string;
    sourceCutoffAt: number;
    inputHash: string;
    model?: string;
    operationCount: number;
    qualifiedCount: number;
    reviewCount: number;
    rejectedCount: number;
    createdAt: number;
}

interface SelfDreamRollout {
    enabled: boolean;
    ready: boolean;
    completedRealRuns: number;
    scoredCandidates: number;
    meanImprovement: number;
    coverageLossCount: number;
    unsupportedReasoningCount: number;
    correctionOrRejectRate: number;
    staleCompilationErrors: number;
    reasons: string[];
}

interface SelfDreamDebugSnapshot {
    generatedAt: number;
    runs: SelfDreamRunSummary[];
    rollout: SelfDreamRollout;
}

interface SelfDreamManifestEntry {
    ref: string;
    sourceKind: string;
    sourceId: string;
    evidenceClass?: string;
    included: boolean;
    reason?: string;
    contentHash?: string;
}

interface SelfDreamOperation {
    id: string;
    operation: string;
    targetEntryIds: string[];
    selfKind: string;
    scope: { kind: string; key?: string };
    statement?: string;
    splitStatements?: string[];
    reasoningStatus: string;
    rationale?: string;
    uncertainty?: string;
    confidence: number;
    evidenceRefs: string[];
    source: string;
    validationStatus: string;
    validationReason?: string;
    autoEligible: boolean;
}

interface SelfDreamDetail {
    status: string;
    run: {
        id: string;
        triggerKind: string;
        policyVersion: string;
        assemblerVersion: string;
        sourceCutoffAt: number;
        baseSelf: Array<{
            id: string;
            kind: string;
            scope?: string;
            statement: string;
            revision: number;
            status: string;
        }>;
        contextHash: string;
        inputHash: string;
        promptVersion: string;
        prompt: string;
        promptHash: string;
        requestedModel: string;
        manifest: SelfDreamManifestEntry[];
    };
    attempt?: {
        status: string;
        safetyMode: string;
        learningEligible: boolean;
        productMutation: boolean;
        model: string;
        rawOutput?: string;
        error?: string;
        durationMs: number;
    };
    operations: SelfDreamOperation[];
    evaluation?: {
        improvement: number;
        coverageLoss: number;
        contradictionRisk: number;
        unsupportedReasoning: number;
        realEvidence: boolean;
        neutral: {
            entryCount: number;
            duplicateEntryCount: number;
            evidenceCoverage: number;
            quality: number;
        };
        dream: {
            entryCount: number;
            duplicateEntryCount: number;
            evidenceCoverage: number;
            quality: number;
        };
    };
    decisions: Array<{
        id: string;
        operationId: string;
        actorKind: string;
        decision: string;
        note?: string;
        createdAt: number;
    }>;
    compilations: Array<{
        id: string;
        operationId: string;
        compilationKind: string;
        sourceEntryIds: string[];
        outputEntryIds: string[];
        createdAt: number;
    }>;
    replays: Array<{
        id: string;
        status: string;
        contextMatchesSource: boolean;
        promptVersion: string;
        model: string;
        error?: string;
        durationMs: number;
        createdAt: number;
    }>;
}

const emptyRollout: SelfDreamRollout = {
    enabled: false,
    ready: false,
    completedRealRuns: 0,
    scoredCandidates: 0,
    meanImprovement: 0,
    coverageLossCount: 0,
    unsupportedReasoningCount: 0,
    correctionOrRejectRate: 0,
    staleCompilationErrors: 0,
    reasons: [],
};

const emptySnapshot: SelfDreamDebugSnapshot = {
    generatedAt: 0,
    runs: [],
    rollout: emptyRollout,
};

function formatDate(value?: number): string {
    return value ? new Date(value).toLocaleString() : "-";
}

function percent(value?: number): string {
    return typeof value === "number" ? `${Math.round(value * 100)}%` : "-";
}

function shortHash(value?: string): string {
    return value ? `${value.slice(0, 10)}…${value.slice(-6)}` : "-";
}

function Section({
    title,
    children,
}: {
    title: string;
    children: ReactNode;
}) {
    return (
        <section className="border-t border-border pt-4">
            <h3 className="text-[10px] font-semibold uppercase text-muted">
                {title}
            </h3>
            <div className="mt-2">{children}</div>
        </section>
    );
}

export function SelfDreamDebugSettings() {
    const [snapshot, setSnapshot] =
        useState<SelfDreamDebugSnapshot>(emptySnapshot);
    const [selectedID, setSelectedID] = useState<string | null>(null);
    const [detail, setDetail] = useState<SelfDreamDetail | null>(null);
    const [query, setQuery] = useState("");
    const [loading, setLoading] = useState(true);
    const [detailLoading, setDetailLoading] = useState(false);
    const [compactDetailOpen, setCompactDetailOpen] = useState(false);
    const [prompt, setPrompt] = useState("");
    const [model, setModel] = useState("");
    const [replaying, setReplaying] = useState(false);
    const [replayError, setReplayError] = useState("");

    const loadSnapshot = useCallback(async () => {
        setLoading(true);
        try {
            const value = (await GetSelfDreamDebugSnapshot(
                200,
            )) as SelfDreamDebugSnapshot;
            const normalized = value
                ? {
                      ...value,
                      runs: Array.isArray(value.runs) ? value.runs : [],
                      rollout: value.rollout ?? emptyRollout,
                  }
                : emptySnapshot;
            setSnapshot(normalized);
            setSelectedID((current) =>
                normalized.runs.some((run) => run.id === current)
                    ? current
                    : (normalized.runs[0]?.id ?? null),
            );
        } catch (error) {
            toast.error(`Profile update diagnostics failed: ${String(error)}`);
        } finally {
            setLoading(false);
        }
    }, []);

    const loadDetail = useCallback(async (runID: string) => {
        setDetailLoading(true);
        try {
            const value = (await GetSelfDreamDebugRun(
                runID,
            )) as SelfDreamDetail;
            setDetail(value);
            setPrompt(value.run.prompt);
            setModel(value.run.requestedModel);
            setReplayError("");
        } catch (error) {
            setDetail(null);
            toast.error(`Profile update draft failed to load: ${String(error)}`);
        } finally {
            setDetailLoading(false);
        }
    }, []);

    useEffect(() => {
        void loadSnapshot();
    }, [loadSnapshot]);

    useEffect(
        () =>
            EventsOn("self_dream:updated", () => {
                void loadSnapshot();
                if (selectedID) void loadDetail(selectedID);
            }),
        [loadDetail, loadSnapshot, selectedID],
    );

    useEffect(() => {
        if (!selectedID) {
            setDetail(null);
            return;
        }
        void loadDetail(selectedID);
    }, [loadDetail, selectedID]);

    const runs = useMemo(() => {
        const needle = query.trim().toLowerCase();
        if (!needle) return snapshot.runs;
        return snapshot.runs.filter((run) =>
            `${run.status} ${run.triggerKind} ${run.model ?? ""} ${run.inputHash}`
                .toLowerCase()
                .includes(needle),
        );
    }, [query, snapshot.runs]);

    const runReplay = async () => {
        if (!detail || replaying) return;
        setReplaying(true);
        setReplayError("");
        try {
            await ReplaySelfDreamDebug({
                runId: detail.run.id,
                prompt,
                model,
            });
            await loadDetail(detail.run.id);
        } catch (error) {
            setReplayError(
                error instanceof Error && error.message.trim()
                    ? error.message
                    : String(error),
            );
        } finally {
            setReplaying(false);
        }
    };

    return (
        <div className="border-t border-border">
            <div className="grid grid-cols-2 gap-px border-b border-border bg-border sm:grid-cols-4">
                {[
                    ["Completed drafts", snapshot.rollout.completedRealRuns],
                    ["Options scored", snapshot.rollout.scoredCandidates],
                    ["Average improvement", percent(snapshot.rollout.meanImprovement)],
                    [
                        "Used for live suggestions",
                        snapshot.rollout.enabled
                            ? "Enabled"
                            : snapshot.rollout.ready
                              ? "Ready, off"
                              : "Collecting",
                    ],
                ].map(([label, value]) => (
                    <div key={String(label)} className="bg-panel px-3 py-2">
                        <p className="text-[9px] text-muted">{label}</p>
                        <p className="mt-0.5 text-[12px] font-semibold text-ink">
                            {value}
                        </p>
                    </div>
                ))}
            </div>

            <div className="flex items-center gap-2 border-b border-border p-3">
                <div className="relative min-w-0 flex-1">
                    <Search
                        className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted"
                        aria-hidden="true"
                    />
                    <input
                        value={query}
                        onChange={(event) => setQuery(event.target.value)}
                        className="h-8 w-full border border-border bg-panel pl-7 pr-2 text-xs text-ink outline-none focus:border-ink/40"
                        placeholder="Search status, model, or input hash"
                    />
                </div>
                <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    aria-label="Refresh profile drafts"
                    disabled={loading}
                    onClick={() => void loadSnapshot()}
                >
                    {loading ? (
                        <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    ) : (
                        <RefreshCw className="h-3.5 w-3.5" />
                    )}
                    <span className="hidden sm:inline">Refresh</span>
                </Button>
            </div>

            <DebugMasterDetail
                compactDetailOpen={compactDetailOpen}
                detailKey={selectedID}
                listLabel="Profile suggestions"
                detailLabel="Profile suggestion details"
                onBack={() => setCompactDetailOpen(false)}
                list={
                    runs.length > 0 ? (
                        <div className="divide-y divide-border">
                            {runs.map((run) => (
                                <button
                                    key={run.id}
                                    type="button"
                                    className={`w-full px-4 py-3 text-left ${
                                        selectedID === run.id
                                            ? "bg-ink/[0.05]"
                                            : "hover:bg-ink/[0.025]"
                                    }`}
                                    onClick={() => {
                                        setSelectedID(run.id);
                                        setCompactDetailOpen(true);
                                    }}
                                >
                                    <div className="flex items-center justify-between gap-2">
                                        <p className="text-[11px] font-medium text-ink">
                                            {run.operationCount}{" "}
                                            {run.operationCount === 1
                                                ? "operation"
                                                : "operations"}
                                        </p>
                                        <span className="text-[9px] font-semibold uppercase text-muted">
                                            {run.status}
                                        </span>
                                    </div>
                                    <p className="mt-1 truncate font-mono text-[9px] text-muted">
                                        {shortHash(run.inputHash)}
                                    </p>
                                    <p className="mt-1 text-[9px] text-muted">
                                        {run.triggerKind} ·{" "}
                                        {formatDate(run.createdAt)}
                                    </p>
                                </button>
                            ))}
                        </div>
                    ) : (
                        <div className="p-6 text-[11px] leading-5 text-muted">
                            No possible profile update matches this search.
                        </div>
                    )
                }
                detail={
                    detailLoading ? (
                        <div className="flex min-h-40 items-center justify-center">
                            <Loader2 className="h-5 w-5 animate-spin text-muted" />
                        </div>
                    ) : detail ? (
                        <div className="space-y-5">
                            <div>
                                <div className="flex flex-wrap items-center justify-between gap-2">
                                    <h2 className="text-sm font-semibold text-ink">
                                        Profile update draft
                                    </h2>
                                    <span className="text-[9px] font-semibold uppercase text-muted">
                                        {detail.status}
                                    </span>
                                </div>
                                <p className="mt-1 font-mono text-[9px] text-muted break-all">
                                    {detail.run.id}
                                </p>
                                <p className="mt-2 text-[10px] text-muted">
                                    {formatDate(detail.run.sourceCutoffAt)} ·{" "}
                                    {detail.run.policyVersion} ·{" "}
                                    {detail.run.assemblerVersion}
                                </p>
                            </div>

                            <Section title="Containment">
                                <div className="grid gap-2 sm:grid-cols-3">
                                    {[
                                        [
                                            "Safety",
                                            detail.attempt?.safetyMode ?? "-",
                                        ],
                                        [
                                            "Learning",
                                            detail.attempt?.learningEligible
                                                ? "eligible"
                                                : "off",
                                        ],
                                        [
                                            "Product mutation",
                                            detail.attempt?.productMutation
                                                ? "on"
                                                : "off",
                                        ],
                                    ].map(([label, value]) => (
                                        <div
                                            key={label}
                                            className="border border-border p-2"
                                        >
                                            <p className="text-[9px] text-muted">
                                                {label}
                                            </p>
                                            <p className="mt-1 text-[10px] font-medium text-ink">
                                                {value}
                                            </p>
                                        </div>
                                    ))}
                                </div>
                            </Section>

                            <Section title="Paired evaluation">
                                {detail.evaluation ? (
                                    <div className="grid grid-cols-2 gap-px border border-border bg-border sm:grid-cols-4">
                                        {[
                                            [
                                                "Improvement",
                                                `${detail.evaluation.improvement >= 0 ? "+" : ""}${detail.evaluation.improvement.toFixed(3)}`,
                                            ],
                                            [
                                                "Coverage",
                                                percent(
                                                    detail.evaluation.dream
                                                        .evidenceCoverage,
                                                ),
                                            ],
                                            [
                                                "Entries",
                                                `${detail.evaluation.neutral.entryCount} → ${detail.evaluation.dream.entryCount}`,
                                            ],
                                            [
                                                "Duplicates",
                                                `${detail.evaluation.neutral.duplicateEntryCount} → ${detail.evaluation.dream.duplicateEntryCount}`,
                                            ],
                                        ].map(([label, value]) => (
                                            <div
                                                key={label}
                                                className="bg-panel p-2"
                                            >
                                                <p className="text-[9px] text-muted">
                                                    {label}
                                                </p>
                                                <p className="mt-1 text-[11px] font-semibold text-ink">
                                                    {value}
                                                </p>
                                            </div>
                                        ))}
                                    </div>
                                ) : (
                                    <p className="text-[10px] text-muted">
                                        No evaluation was persisted.
                                    </p>
                                )}
                            </Section>

                            <Section title="Operations">
                                <div className="space-y-3">
                                    {detail.operations.map((operation) => (
                                        <article
                                            key={operation.id}
                                            className="border border-border p-3"
                                        >
                                            <div className="flex flex-wrap items-center justify-between gap-2">
                                                <p className="text-[10px] font-semibold text-ink">
                                                    {operation.operation} ·{" "}
                                                    {operation.selfKind}
                                                </p>
                                                <span className="text-[9px] font-semibold uppercase text-muted">
                                                    {
                                                        operation.validationStatus
                                                    }
                                                </span>
                                            </div>
                                            <p className="mt-2 text-[10px] leading-4 text-muted">
                                                {operation.statement ||
                                                    operation.splitStatements?.join(
                                                        " / ",
                                                    ) ||
                                                    "No change"}
                                            </p>
                                            <p className="mt-2 text-[9px] text-muted">
                                                {operation.source} ·{" "}
                                                {operation.reasoningStatus} ·{" "}
                                                {operation.evidenceRefs.length}{" "}
                                                refs ·{" "}
                                                {operation.validationReason}
                                            </p>
                                            {operation.uncertainty && (
                                                <p className="mt-2 border-l-2 border-border pl-2 text-[9px] leading-4 text-muted">
                                                    {operation.uncertainty}
                                                </p>
                                            )}
                                        </article>
                                    ))}
                                </div>
                            </Section>

                            <Section title="Understanding at the time">
                                <div className="divide-y divide-border border-y border-border">
                                    {detail.run.baseSelf.map((entry) => (
                                        <div key={entry.id} className="py-2.5">
                                            <p className="text-[10px] leading-4 text-muted">
                                                {entry.statement}
                                            </p>
                                            <p className="mt-1 font-mono text-[9px] text-muted">
                                                {entry.kind} · r{entry.revision} ·{" "}
                                                {entry.id}
                                            </p>
                                        </div>
                                    ))}
                                </div>
                            </Section>

                            <Section title="Evidence considered">
                                <div className="max-h-72 overflow-auto divide-y divide-border border-y border-border">
                                    {detail.run.manifest.map((item) => (
                                        <div key={item.ref} className="py-2.5">
                                            <div className="flex items-start gap-2">
                                                {item.included ? (
                                                    <CheckCircle2
                                                        className="mt-0.5 h-3.5 w-3.5 shrink-0 text-success"
                                                        aria-label="Included"
                                                    />
                                                ) : (
                                                    <AlertCircle
                                                        className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted"
                                                        aria-label="Excluded"
                                                    />
                                                )}
                                                <div className="min-w-0">
                                                    <p className="break-all font-mono text-[9px] text-muted">
                                                        {item.ref}
                                                    </p>
                                                    <p className="mt-1 text-[9px] text-muted">
                                                        {item.sourceKind} ·{" "}
                                                        {item.evidenceClass ||
                                                            item.reason ||
                                                            "-"}
                                                    </p>
                                                </div>
                                            </div>
                                        </div>
                                    ))}
                                </div>
                            </Section>

                            <Section title="Exact frozen replay">
                                <div className="space-y-3">
                                    <label className="block">
                                        <span className="text-[9px] font-semibold uppercase text-muted">
                                            Model
                                        </span>
                                        <input
                                            value={model}
                                            onChange={(event) =>
                                                setModel(event.target.value)
                                            }
                                            className="mt-1 h-8 w-full border border-border bg-panel px-2 font-mono text-[10px] text-ink outline-none focus:border-ink/40"
                                        />
                                    </label>
                                    <label className="block">
                                        <span className="text-[9px] font-semibold uppercase text-muted">
                                            Prompt
                                        </span>
                                        <Textarea
                                            value={prompt}
                                            onChange={(event) =>
                                                setPrompt(event.target.value)
                                            }
                                            className="mt-1 min-h-48 resize-y font-mono text-[10px]"
                                        />
                                    </label>
                                    {replayError && (
                                        <p
                                            role="alert"
                                            className="border-l-2 border-error pl-2 text-[10px] text-error"
                                        >
                                            {replayError}
                                        </p>
                                    )}
                                    <div className="flex flex-wrap items-center justify-between gap-2">
                                        <p className="text-[9px] text-muted">
                                            {detail.replays.length} diagnostic{" "}
                                            {detail.replays.length === 1
                                                ? "replay"
                                                : "replays"}{" "}
                                            · context{" "}
                                            {shortHash(
                                                detail.run.contextHash,
                                            )}
                                        </p>
                                        <Button
                                            type="button"
                                            size="sm"
                                            disabled={
                                                replaying ||
                                                !prompt.trim() ||
                                                !model.trim()
                                            }
                                            onClick={() => void runReplay()}
                                        >
                                            {replaying ? (
                                                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                                            ) : (
                                                <FlaskConical className="h-3.5 w-3.5" />
                                            )}
                                            Replay
                                        </Button>
                                    </div>
                                </div>
                            </Section>

                            {detail.replays.length > 0 && (
                                <Section title="Replay history">
                                    <div className="divide-y divide-border border-y border-border">
                                        {detail.replays.map((replay) => {
                                            const promptMatches =
                                                replay.promptVersion ===
                                                detail.run.promptVersion;
                                            const modelMatches =
                                                replay.model ===
                                                (detail.attempt?.model ||
                                                    detail.run.requestedModel);
                                            return (
                                                <article
                                                    key={replay.id}
                                                    className="py-3"
                                                >
                                                    <div className="flex flex-wrap items-center justify-between gap-2">
                                                        <p className="text-[10px] font-semibold text-ink">
                                                            {replay.status}
                                                        </p>
                                                        <time
                                                            className="text-[9px] text-muted"
                                                            dateTime={new Date(
                                                                replay.createdAt,
                                                            ).toISOString()}
                                                        >
                                                            {formatDate(
                                                                replay.createdAt,
                                                            )}
                                                        </time>
                                                    </div>
                                                    <dl className="mt-2 grid grid-cols-2 gap-2 sm:grid-cols-4">
                                                        {[
                                                            [
                                                                "Context",
                                                                replay.contextMatchesSource
                                                                    ? "matches production"
                                                                    : "mismatch",
                                                            ],
                                                            [
                                                                "Prompt",
                                                                promptMatches
                                                                    ? "matches production"
                                                                    : "custom",
                                                            ],
                                                            [
                                                                "Model",
                                                                modelMatches
                                                                    ? "matches production"
                                                                    : "custom",
                                                            ],
                                                            [
                                                                "Duration",
                                                                `${replay.durationMs} ms`,
                                                            ],
                                                        ].map(
                                                            ([label, value]) => (
                                                                <div key={label}>
                                                                    <dt className="text-[9px] text-muted">
                                                                        {label}
                                                                    </dt>
                                                                    <dd className="mt-0.5 text-[9px] font-medium text-muted">
                                                                        {value}
                                                                    </dd>
                                                                </div>
                                                            ),
                                                        )}
                                                    </dl>
                                                    {replay.error && (
                                                        <p className="mt-2 border-l-2 border-error pl-2 text-[9px] leading-4 text-error">
                                                            {replay.error}
                                                        </p>
                                                    )}
                                                </article>
                                            );
                                        })}
                                    </div>
                                </Section>
                            )}

                            {detail.attempt?.rawOutput && (
                                <Section title="Original model output">
                                    <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words border border-border bg-ink/[0.02] p-3 font-mono text-[9px] leading-4 text-muted">
                                        {detail.attempt.rawOutput}
                                    </pre>
                                </Section>
                            )}

                            <Section title="Decisions and compilations">
                                {detail.decisions.length > 0 ||
                                detail.compilations.length > 0 ? (
                                    <div className="space-y-2">
                                        {detail.decisions.map((decision) => (
                                            <p
                                                key={decision.id}
                                                className="text-[10px] leading-4 text-muted"
                                            >
                                                {decision.actorKind}:{" "}
                                                {decision.decision}
                                                {decision.note
                                                    ? ` · ${decision.note}`
                                                    : ""}
                                            </p>
                                        ))}
                                        {detail.compilations.map(
                                            (compilation) => (
                                                <p
                                                    key={compilation.id}
                                                    className="text-[10px] leading-4 text-muted"
                                                >
                                                    compiled{" "}
                                                    {
                                                        compilation.compilationKind
                                                    }{" "}
                                                    from{" "}
                                                    {
                                                        compilation
                                                            .sourceEntryIds
                                                            .length
                                                    }{" "}
                                                    to{" "}
                                                    {
                                                        compilation
                                                            .outputEntryIds
                                                            .length
                                                    }{" "}
                                                    entries
                                                </p>
                                            ),
                                        )}
                                    </div>
                                ) : (
                                    <p className="text-[10px] text-muted">
                                        Shadow-only. No product decision or
                                        compilation was recorded.
                                    </p>
                                )}
                            </Section>
                        </div>
                    ) : (
                        <div className="flex min-h-40 items-center justify-center text-[11px] text-muted">
                            Select a possible profile update.
                        </div>
                    )
                }
            />
        </div>
    );
}
