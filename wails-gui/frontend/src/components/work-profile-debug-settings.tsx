import { useCallback, useEffect, useMemo, useState } from "react";
import {
    Check,
    ChevronRight,
    Copy,
    ExternalLink,
    Loader2,
    RefreshCw,
    Search,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import {
    GetWorkPatternProfileDebugDetail,
    GetWorkPatternProfileDebugSnapshot,
    type ShadowLearningInfluenceCohortSnapshot,
    type ShadowLearningInfluenceProof,
    type TaskAgentAttempt,
    type WorkPatternProfileAblationBranchMetrics,
    type WorkPatternProfileDebugDetail,
    type WorkPatternProfileDebugSnapshot,
    type WorkPatternProfileDebugSummary,
    type WorkPatternProfileRolloutStatus,
} from "@/lib/wails-shim-app";
import { DebugMasterDetail } from "./debug-master-detail";

const emptyRollout: WorkPatternProfileRolloutStatus = {
    influenceEnabled: false,
    ablationRunCount: 0,
    evaluationCount: 0,
    scoredPairCount: 0,
    profileWins: 0,
    neutralWins: 0,
    ties: 0,
    unknown: 0,
    correctionCount: 0,
    integrityErrorCount: 0,
    averageImprovement: 0,
    profileWinRate: 0,
    unknownRate: 0,
    correctionRate: 0,
    minimumScoredPairs: 20,
    minimumImprovement: 0.03,
    minimumProfileWinRate: 0.55,
    maximumUnknownRate: 0.2,
    maximumCorrectionRate: 0.1,
    meetsEvidenceGate: false,
    recommendedDecision: "hold",
    reason: "No paired outcomes have been evaluated.",
    policyVersion: "work-pattern-ablation-v3",
    collectionAlwaysEnabled: true,
};

const emptyLearningInfluence: ShadowLearningInfluenceProof = {
    version: "shadow-learning-influence-proof-v1",
    state: "awaiting_source_learning",
    complete: false,
    improved: false,
    summary: "No evaluated Shadow comparison is available yet.",
    requiredAction: "Complete more contained Shadow comparisons.",
    authorityEffect: "none",
    generatedAt: 0,
    sourceLearningCount: 0,
    laterTaskCount: 0,
    measuredChainCount: 0,
    sourceTaskEpisodeIds: [],
    sourceShadowRunIds: [],
    sourceEvaluationIds: [],
    onsetProfilePresent: false,
    steps: [],
    reasons: [],
};

const emptyLearningCohorts: ShadowLearningInfluenceCohortSnapshot = {
    id: "",
    version: "shadow-learning-influence-cohort-v1",
    policyVersion: "shadow-learning-influence-reliability-v1",
    generatedAt: 0,
    readCutoffAt: 0,
    manifestSha256: "",
    authorityEffect: "none",
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
    cohortCount: 0,
    awaitingMeasurementCount: 0,
    earlyCount: 0,
    reliableCount: 0,
    insufficientQualityCount: 0,
    integrityErrorCount: 0,
    cohorts: [],
};

const emptySnapshot: WorkPatternProfileDebugSnapshot = {
    generatedAt: 0,
    limit: 200,
    profiles: [],
    rollout: emptyRollout,
    learningInfluence: emptyLearningInfluence,
    learningCohorts: emptyLearningCohorts,
};

function formatDate(value?: number): string {
    return value ? new Date(value).toLocaleString() : "-";
}

function formatPercent(value?: number): string {
    return typeof value === "number" ? `${Math.round(value * 100)}%` : "-";
}

function formatSignedPercent(value?: number): string {
    if (typeof value !== "number") return "-";
    return `${value >= 0 ? "+" : ""}${Math.round(value * 100)}%`;
}

function formatBytes(value?: number): string {
    if (typeof value !== "number") return "-";
    if (value < 1024) return `${value} B`;
    return `${(value / 1024).toFixed(1)} KB`;
}

function formatMetric(value: number, unit: string): string {
    if (unit === "ratio") return formatPercent(value);
    if (unit === "seconds") return `${value.toFixed(1)}s`;
    if (unit === "milliseconds") return `${Math.round(value)}ms`;
    if (Number.isInteger(value)) return String(value);
    return value.toFixed(2);
}

function prettyJSON(value: unknown): string {
    if (typeof value === "string") {
        try {
            return JSON.stringify(JSON.parse(value), null, 2);
        } catch {
            return value;
        }
    }
    return JSON.stringify(value, null, 2);
}

function shortHash(value?: string): string {
    return value ? value.slice(0, 12) : "-";
}

async function copyText(label: string, value: string) {
    try {
        await navigator.clipboard.writeText(value);
        toast.success(`${label} copied`);
    } catch (error) {
        toast.error(String(error));
    }
}

function StatusBadge({ status }: { status: string }) {
    const labels: Record<string, string> = {
        awaiting_ablation: "Waiting for comparison",
        awaiting_measurement: "Waiting for measurement",
        awaiting_result: "Waiting for result",
        awaiting_source_learning: "Waiting for learning",
        completed: "Completed",
        corrected: "Corrected",
        early: "Too early",
        enabled: "In use",
        evaluated: "Compared",
        failed: "Failed",
        hold: "On hold",
        included: "Included",
        insufficient: "Not enough evidence",
        insufficient_quality: "Needs stronger results",
        integrity_error: "Evidence check failed",
        measured_improvement: "Improved later prediction",
        neutral: "Baseline result",
        omitted: "Excluded",
        onset_included: "Used at task start",
        onset_omitted: "Not used at task start",
        parse_failed: "Could not read result",
        profile: "Using observed patterns",
        qualified: "Meets test threshold",
        reliable: "Prediction reliable",
        shadow_included: "Included in private practice",
        shadow_omitted: "Not used for Isoform's attempt",
        succeeded: "Completed",
        tie: "Same result",
        unknown: "Unknown",
        unscored: "Not measured",
    };
    const label =
        labels[status] ??
        (status.endsWith("_included")
            ? "Included"
            : status.endsWith("_omitted")
              ? "Excluded"
              : status
                    .replaceAll("_", " ")
                    .replace(/^./, (character) => character.toUpperCase()));
    const tone =
        status === "qualified" ||
        status === "succeeded" ||
        status === "profile" ||
        status === "included" ||
        status.endsWith("included") ||
        status === "reliable" ||
        status === "measured_improvement"
            ? "bg-success/15 text-success"
            : status === "insufficient" ||
                status === "neutral" ||
                status === "tie" ||
                status === "hold" ||
                status === "early" ||
                status === "insufficient_quality" ||
                status === "omitted" ||
                status.endsWith("omitted")
              ? "bg-warning-bg text-warning"
              : status === "failed" ||
                  status === "parse_failed" ||
                  status === "unknown" ||
                  status === "integrity_error"
                ? "bg-error-bg text-error"
                : "bg-muted/15 text-muted";
    return (
        <span
            title={`Technical status: ${status}`}
            className={`inline-flex rounded px-1.5 py-0.5 text-[10px] font-medium ${tone}`}
        >
            {label}
        </span>
    );
}

function LearningInfluenceStrip({
    proof,
    onOpenTaskEpisode,
    onOpenShadowRun,
}: {
    proof: ShadowLearningInfluenceProof;
    onOpenTaskEpisode?: (id: string) => void;
    onOpenShadowRun?: (id: string) => void;
}) {
    return (
        <details className="border-b border-border py-3">
            <summary className="cursor-pointer list-none">
                <div className="flex flex-wrap items-start justify-between gap-3">
                    <div className="w-full min-w-0 lg:w-auto">
                        <div className="flex items-center gap-2">
                            <span className="text-[11px] font-semibold text-ink">
                                Learning effect
                            </span>
                            <StatusBadge status={proof.state} />
                        </div>
                        <p className="mt-1 max-w-2xl text-[10px] leading-4 text-muted">
                            {proof.summary}
                        </p>
                    </div>
                    <div className="grid w-full grid-cols-3 gap-x-3 text-right lg:w-auto">
                        {[
                            ["Lessons", proof.sourceLearningCount],
                            ["Later tasks", proof.laterTaskCount],
                            ["Compared", proof.measuredChainCount],
                        ].map(([label, value]) => (
                            <div key={String(label)}>
                                <div className="text-[9px] text-muted">
                                    {label}
                                </div>
                                <div className="text-xs font-semibold tabular-nums text-ink">
                                    {value}
                                </div>
                            </div>
                        ))}
                    </div>
                </div>
            </summary>
            <div className="mt-3 border-t border-border pt-3">
                <div className="grid gap-2 lg:grid-cols-2">
                    {proof.steps.map((step) => (
                        <div
                            key={step.key}
                            className="flex min-h-7 items-start gap-2 text-[10px]"
                        >
                            <span
                                className={`mt-0.5 inline-flex h-4 w-4 shrink-0 items-center justify-center border ${
                                    step.complete
                                        ? "border-success/40 bg-success/10 text-success"
                                        : "border-border text-muted"
                                }`}
                            >
                                {step.complete && <Check className="h-3 w-3" />}
                            </span>
                            <span className="min-w-0">
                                <span className="font-medium text-ink">
                                    {step.label}
                                </span>
                                {step.reason && (
                                    <span className="ml-1 text-muted">
                                        {step.reason}
                                    </span>
                                )}
                            </span>
                        </div>
                    ))}
                </div>
                <div className="mt-3 flex flex-wrap items-center gap-2 border-t border-border pt-3 text-[10px] text-muted">
                    <span>
                        Neutral {formatPercent(proof.neutralScore)}
                    </span>
                    <span>
                        Profile {formatPercent(proof.profileScore)}
                    </span>
                    <span>Lift {formatSignedPercent(proof.lift)}</span>
                    <span>Authority {proof.authorityEffect}</span>
                    {proof.laterTaskEpisodeId && onOpenTaskEpisode && (
                        <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            className="ml-auto h-7 text-[10px]"
                            onClick={() =>
                                onOpenTaskEpisode(proof.laterTaskEpisodeId!)
                            }
                        >
                            <ExternalLink className="h-3 w-3" />
                            Later task
                        </Button>
                    )}
                    {proof.shadowRunId && onOpenShadowRun && (
                        <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            className="h-7 text-[10px]"
                            onClick={() => onOpenShadowRun(proof.shadowRunId!)}
                        >
                            <ExternalLink className="h-3 w-3" />
                            Isoform's attempt
                        </Button>
                    )}
                </div>
                <p className="mt-2 text-[9px] leading-4 text-muted">
                    {proof.requiredAction}
                </p>
                {proof.reasons.length > 0 && (
                    <p className="mt-1 font-mono text-[9px] leading-4 text-muted">
                        {proof.reasons.join(" · ")}
                    </p>
                )}
            </div>
        </details>
    );
}

function LearningCohortStrip({
    snapshot,
    onOpenTaskEpisode,
    onOpenShadowRun,
}: {
    snapshot: ShadowLearningInfluenceCohortSnapshot;
    onOpenTaskEpisode?: (id: string) => void;
    onOpenShadowRun?: (id: string) => void;
}) {
    const aggregateState = snapshot.integrityErrorCount
        ? "integrity_error"
        : snapshot.reliableCount
          ? "reliable"
          : snapshot.insufficientQualityCount
            ? "insufficient_quality"
            : snapshot.earlyCount
              ? "early"
              : "awaiting_measurement";
    return (
        <details className="border-b border-border py-3">
            <summary className="cursor-pointer list-none">
                <div className="flex flex-wrap items-start justify-between gap-3">
                    <div className="w-full min-w-0 lg:w-auto">
                        <div className="flex items-center gap-2">
                            <span className="text-[11px] font-semibold text-ink">
                                Repeated learning checks
                            </span>
                            <StatusBadge status={aggregateState} />
                        </div>
                        <p className="mt-1 max-w-2xl text-[10px] leading-4 text-muted">
                            Later independent tasks measured against each exact saved lesson.
                        </p>
                    </div>
                    <div className="grid w-full grid-cols-3 gap-x-3 text-right lg:w-auto">
                        {[
                            ["Test groups", snapshot.cohortCount],
                            ["Reliable patterns", snapshot.reliableCount],
                            ["Evidence issues", snapshot.integrityErrorCount],
                        ].map(([label, value]) => (
                            <div key={String(label)}>
                                <div className="text-[9px] text-muted">
                                    {label}
                                </div>
                                <div className="text-xs font-semibold tabular-nums text-ink">
                                    {value}
                                </div>
                            </div>
                        ))}
                    </div>
                </div>
            </summary>
            <div className="mt-3 border-t border-border pt-2">
                <div className="flex flex-wrap items-center gap-3 px-1 py-2 text-[9px] text-muted">
                    <span>Cutoff {formatDate(snapshot.readCutoffAt)}</span>
                    <span>Authority {snapshot.authorityEffect}</span>
                    <span>
                        Production influence {snapshot.productionInfluenceEffect}
                    </span>
                    {snapshot.manifestSha256 && (
                        <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            className="ml-auto h-6 px-1.5 text-[9px]"
                            onClick={() =>
                                void copyText(
                                    "Comparison-group record",
                                    snapshot.manifestSha256,
                                )
                            }
                        >
                            <Copy className="h-3 w-3" />
                            {shortHash(snapshot.manifestSha256)}
                        </Button>
                    )}
                </div>
                {snapshot.cohorts.length === 0 ? (
                    <p className="border-t border-border px-1 py-4 text-[10px] text-muted">
                        No current verified learning revision is available for repeated measurement.
                    </p>
                ) : (
                    snapshot.cohorts.map((cohort) => (
                        <details
                            key={cohort.id}
                            className="border-t border-border px-1 py-2"
                        >
                            <summary className="cursor-pointer list-none">
                                <div className="flex flex-wrap items-center gap-x-4 gap-y-1">
                                    <div className="min-w-0 flex-1">
                                        <div className="flex items-center gap-2">
                                            <span className="truncate text-[10px] font-medium text-ink">
                                                {cohort.learningCapability}
                                            </span>
                                            <StatusBadge status={cohort.state} />
                                        </div>
                                        <p className="mt-1 text-[9px] leading-4 text-muted">
                                            {cohort.summary}
                                        </p>
                                    </div>
                                    <span className="text-[9px] tabular-nums text-muted">
                                        {cohort.scoredChainCount}/
                                        {snapshot.policy.minimumScoredTasks} scored
                                    </span>
                                    <span className="text-[9px] tabular-nums text-muted">
                                        wins {formatPercent(cohort.profileWinRate)}
                                    </span>
                                    <span className="text-[9px] tabular-nums text-muted">
                                        lift {formatSignedPercent(cohort.meanLift)}
                                    </span>
                                    <ChevronRight className="h-3.5 w-3.5 text-muted" />
                                </div>
                            </summary>
                            <div className="mt-2 border-t border-border pt-2">
                                <div className="grid grid-cols-2 gap-x-5 gap-y-2 text-[9px] sm:grid-cols-4">
                                    {[
                                        ["Source tasks", cohort.sourceTaskEpisodeCount],
                                        ["Later tasks", cohort.relevantLaterTaskCount],
                                        ["Unknown", cohort.unknownChainCount],
                                        ["Corrections", cohort.correctedChainCount],
                                        ["Onset omitted", cohort.onsetOmittedCount],
                                        ["Shadow omitted", cohort.shadowOmittedCount],
                                        ["Awaiting result", cohort.awaitingResultCount],
                                        ["Awaiting ablation", cohort.awaitingAblationCount],
                                    ].map(([label, value]) => (
                                        <div key={String(label)}>
                                            <div className="text-muted">{label}</div>
                                            <div className="mt-0.5 font-medium tabular-nums text-ink">
                                                {value}
                                            </div>
                                        </div>
                                    ))}
                                </div>
                                {cohort.blockers.length > 0 && (
                                    <p className="mt-3 font-mono text-[9px] leading-4 text-muted">
                                        {cohort.blockers.join(" · ")}
                                    </p>
                                )}
                                <div className="mt-3 border-t border-border">
                                    {cohort.chains.length === 0 ? (
                                        <p className="py-3 text-[9px] text-muted">
                                            Waiting for a later task to freeze this learning.
                                        </p>
                                    ) : (
                                        cohort.chains.map((chain) => (
                                            <div
                                                key={chain.id}
                                                className="flex flex-wrap items-center gap-2 border-b border-border py-2 last:border-b-0"
                                            >
                                                <StatusBadge status={chain.state} />
                                                <span className="min-w-0 flex-1 truncate font-mono text-[9px] text-muted">
                                                    {chain.laterTaskEpisodeId}
                                                </span>
                                                <span className="text-[9px] tabular-nums text-muted">
                                                    {formatSignedPercent(chain.lift)}
                                                </span>
                                                {chain.corrected && (
                                                    <StatusBadge status="corrected" />
                                                )}
                                                {onOpenTaskEpisode && (
                                                    <Button
                                                        type="button"
                                                        variant="outline"
                                                        size="sm"
                                                        className="h-6 px-1.5 text-[9px]"
                                                        onClick={() =>
                                                            onOpenTaskEpisode(
                                                                chain.laterTaskEpisodeId,
                                                            )
                                                        }
                                                    >
                                                        <ExternalLink className="h-3 w-3" />
                                                        Task
                                                    </Button>
                                                )}
                                                {chain.shadowRunId &&
                                                    onOpenShadowRun && (
                                                        <Button
                                                            type="button"
                                                            variant="outline"
                                                            size="sm"
                                                            className="h-6 px-1.5 text-[9px]"
                                                            onClick={() =>
                                                                onOpenShadowRun(
                                                                    chain.shadowRunId!,
                                                                )
                                                            }
                                                        >
                                                            <ExternalLink className="h-3 w-3" />
                                                            Shadow
                                                        </Button>
                                                    )}
                                            </div>
                                        ))
                                    )}
                                </div>
                            </div>
                        </details>
                    ))
                )}
            </div>
        </details>
    );
}

function RolloutStrip({
    rollout,
}: {
    rollout: WorkPatternProfileRolloutStatus;
}) {
    return (
        <div className="border-y border-border py-3">
            <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="w-full min-w-0 lg:w-auto lg:max-w-sm">
                    <div className="flex items-center gap-2">
                        <span className="text-[11px] font-semibold text-ink">
                            Personalization effect
                        </span>
                        <StatusBadge
                            status={
                                rollout.influenceEnabled
                                    ? "enabled"
                                    : rollout.recommendedDecision
                            }
                        />
                    </div>
                    <p className="mt-1 text-[10px] leading-4 text-muted">
                        {rollout.reason}
                    </p>
                </div>
                <div className="grid w-full min-w-0 grid-cols-[repeat(auto-fit,minmax(72px,1fr))] gap-x-3 gap-y-2 text-right lg:w-auto lg:flex-1">
                    {[
                        ["Results compared", rollout.scoredPairCount],
                        ["Better with patterns", rollout.profileWins],
                        ["Win rate", formatPercent(rollout.profileWinRate)],
                        [
                            "Correction rate",
                            formatPercent(rollout.correctionRate),
                        ],
                        ["Evidence errors", rollout.integrityErrorCount],
                        [
                            "Average gain",
                            `${rollout.averageImprovement >= 0 ? "+" : ""}${formatPercent(
                                rollout.averageImprovement,
                            )}`,
                        ],
                    ].map(([label, value]) => (
                        <div key={String(label)} className="min-w-0">
                            <div className="text-[9px] leading-3 text-muted">
                                {label}
                            </div>
                            <div className="text-xs font-semibold tabular-nums text-ink">
                                {value}
                            </div>
                        </div>
                    ))}
                </div>
            </div>
        </div>
    );
}

function AblationBranchMetrics({
    label,
    metrics,
}: {
    label: string;
    metrics?: WorkPatternProfileAblationBranchMetrics;
}) {
    if (!metrics) return null;
    const values = [
        ["Predictions", metrics.predictionCount],
        ["Matched", metrics.matchedPredictionCount],
        ["Precision", formatPercent(metrics.onsetPrecision)],
        ["Recall", formatPercent(metrics.onsetRecall)],
        ["Task", formatPercent(metrics.taskMatch)],
        ["Action", formatPercent(metrics.actionMatch)],
        ["Result", formatPercent(metrics.resultMatch)],
        [
            "Boundary",
            metrics.boundaryErrorMs === undefined
                ? "-"
                : `${metrics.boundaryErrorMs}ms`,
        ],
        ["Split errors", metrics.splitErrorCount],
        ["Merge errors", metrics.mergeErrorCount],
        ["Calibration", formatPercent(metrics.calibrationError)],
        ["Composite", formatPercent(metrics.compositeScore)],
    ];
    return (
        <div className="border-t border-border pt-2">
            <div className="text-[10px] font-semibold text-ink">{label}</div>
            <dl className="mt-2 grid grid-cols-2 gap-x-4 gap-y-1 text-[9px] sm:grid-cols-3">
                {values.map(([name, value]) => (
                    <div
                        key={String(name)}
                        className="flex min-w-0 justify-between gap-2"
                    >
                        <dt className="truncate text-muted">{name}</dt>
                        <dd className="shrink-0 font-medium tabular-nums text-ink">
                            {value}
                        </dd>
                    </div>
                ))}
            </dl>
        </div>
    );
}

function CodeBlock({
    title,
    value,
}: {
    title: string;
    value: string;
}) {
    return (
        <div className="min-w-0">
            <div className="mb-1 flex items-center justify-between gap-2">
                <span className="text-[10px] font-semibold text-muted">
                    {title}
                </span>
                <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="h-6 w-6"
                    aria-label={`Copy ${title}`}
                    title={`Copy ${title}`}
                    onClick={() => void copyText(title, value)}
                >
                    <Copy aria-hidden="true" className="h-3 w-3" />
                </Button>
            </div>
            <pre className="max-h-72 overflow-auto whitespace-pre-wrap break-all border border-border bg-ink/[0.025] p-2 text-[9px] leading-4 text-ink">
                {value || "-"}
            </pre>
        </div>
    );
}

function AttemptDetail({ attempt }: { attempt: TaskAgentAttempt }) {
    return (
        <details className="border-t border-border py-3">
            <summary className="flex cursor-pointer list-none items-center justify-between gap-3">
                <div className="min-w-0">
                    <div className="truncate text-[11px] font-medium text-ink">
                        {attempt.promptVersion} · {attempt.modelName}
                    </div>
                    <div className="truncate font-mono text-[9px] text-muted">
                        {attempt.id}
                    </div>
                </div>
                <StatusBadge status={attempt.status} />
            </summary>
            <div className="mt-3 space-y-3">
                <CodeBlock title="Exact prompt" value={attempt.promptText} />
                <CodeBlock
                    title="Normalized input"
                    value={prettyJSON(attempt.normalizedInputJson)}
                />
                <CodeBlock title="Raw output" value={attempt.rawOutput ?? ""} />
                {attempt.error && (
                    <p className="text-[10px] text-error">{attempt.error}</p>
                )}
            </div>
        </details>
    );
}

function ProfileListRow({
    profile,
    selected,
    onSelect,
}: {
    profile: WorkPatternProfileDebugSummary;
    selected: boolean;
    onSelect: () => void;
}) {
    return (
        <button
            type="button"
            className={`w-full border-b border-border px-4 py-3 text-left hover:bg-ink/[0.025] ${
                selected ? "bg-ink/[0.04]" : ""
            }`}
            aria-current={selected ? "true" : undefined}
            onClick={onSelect}
        >
            <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                    <div className="flex items-center gap-2">
                        <StatusBadge status={profile.status} />
                        <span className="text-[10px] text-muted">
                            {profile.sessionCount} sessions
                        </span>
                    </div>
                    <div className="mt-2 truncate font-mono text-[10px] text-ink">
                        {profile.id}
                    </div>
                    <div className="mt-1 text-[9px] text-muted">
                        {formatDate(profile.sourceCutoffAt)} ·{" "}
                        {profile.projectionCount} projections ·{" "}
                        {profile.ablationRunCount} paired{" "}
                        {profile.ablationRunCount === 1
                            ? "attempt"
                            : "attempts"}
                    </div>
                </div>
                <ChevronRight
                    aria-hidden="true"
                    className="mt-1 h-3.5 w-3.5 shrink-0 text-muted"
                />
            </div>
        </button>
    );
}

function WorkProfileDetail({
    detail,
    loading,
    onOpenTaskEpisode,
    onOpenShadowRun,
}: {
    detail: WorkPatternProfileDebugDetail | null;
    loading: boolean;
    onOpenTaskEpisode?: (id: string) => void;
    onOpenShadowRun?: (id: string) => void;
}) {
    if (loading) {
        return (
            <div className="flex min-h-52 items-center justify-center text-muted">
                <Loader2 className="h-4 w-4 animate-spin" />
            </div>
        );
    }
    if (!detail) {
        return (
            <div className="py-16 text-center text-xs text-muted">
                Select a work pattern to inspect its evidence.
            </div>
        );
    }
    const replayJSON = prettyJSON(detail.replay);
    const learningInfluence = detail.episodes.flatMap((episode) => {
        const byRevision = new Map<
            string,
            {
                episode: (typeof detail.episodes)[number];
                learning: NonNullable<
                    (typeof episode.profileSelectionManifest)[number]["learning"]
                >;
                onset?: (typeof episode.onsetProfileSelectionManifest)[number];
                shadow?: (typeof episode.profileSelectionManifest)[number];
            }
        >();
        for (const onset of episode.onsetProfileSelectionManifest ?? []) {
            if (!onset.learning) continue;
            byRevision.set(onset.learning.revisionId, {
                episode,
                learning: onset.learning,
                onset,
            });
        }
        for (const shadow of episode.profileSelectionManifest ?? []) {
            if (!shadow.learning) continue;
            const existing = byRevision.get(shadow.learning.revisionId);
            byRevision.set(shadow.learning.revisionId, {
                episode,
                learning: shadow.learning,
                onset: existing?.onset,
                shadow,
            });
        }
        return Array.from(byRevision.values());
    });
    return (
        <div className="space-y-7">
            <section>
                <div className="flex flex-wrap items-start justify-between gap-3">
                    <div className="min-w-0">
                        <div className="flex items-center gap-2">
                            <h3 className="text-sm font-semibold text-ink">
                                Saved work pattern
                            </h3>
                            <StatusBadge status={detail.snapshot.status} />
                        </div>
                        <p className="mt-1 break-all font-mono text-[9px] text-muted">
                            {detail.snapshot.id}
                        </p>
                    </div>
                    <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        onClick={() => void copyText("Replay bundle", replayJSON)}
                    >
                        <Copy aria-hidden="true" className="h-3.5 w-3.5" />
                        Copy replay bundle
                    </Button>
                </div>
                <dl className="mt-4 grid grid-cols-2 gap-x-5 gap-y-3 sm:grid-cols-4">
                    {[
                        ["Cutoff", formatDate(detail.snapshot.sourceCutoffAt)],
                        ["Created", formatDate(detail.snapshot.createdAt)],
                        ["Samples", detail.snapshot.sampleCount],
                        ["Sessions", detail.snapshot.sessionCount],
                        ["Confidence", formatPercent(detail.snapshot.confidence)],
                        ["Evidence", detail.evidence.length],
                        ["Corrections", detail.corrections.length],
                        ["Used by", detail.episodes.length + detail.shadowRuns.length],
                    ].map(([label, value]) => (
                        <div key={String(label)}>
                            <dt className="text-[9px] text-muted">{label}</dt>
                            <dd className="mt-0.5 break-words text-[11px] font-medium text-ink">
                                {value}
                            </dd>
                        </div>
                    ))}
                </dl>
                <CodeBlock title="SHA-256" value={detail.snapshot.sha256} />
            </section>

            {detail.promptDiagnostic && (
                <section>
                    <div className="flex flex-wrap items-center justify-between gap-3">
                        <div>
                            <h3 className="text-xs font-semibold text-ink">
                                Prompt envelope
                            </h3>
                            <p className="mt-1 text-[9px] text-muted">
                                Profile-conditioned preflight · no model call or
                                product mutation
                            </p>
                        </div>
                        <span
                            className={
                                detail.promptDiagnostic.fits
                                    ? "inline-flex items-center gap-1 text-[10px] font-medium text-success"
                                    : "text-[10px] font-medium text-error"
                            }
                        >
                            {detail.promptDiagnostic.fits && (
                                <Check
                                    aria-hidden="true"
                                    className="h-3.5 w-3.5"
                                />
                            )}
                            {detail.promptDiagnostic.fits
                                ? "Fits current test limits"
                                : "Could not validate"}
                        </span>
                    </div>
                    <dl className="mt-3 grid grid-cols-2 gap-x-5 gap-y-3 border-y border-border py-3 sm:grid-cols-4">
                        {[
                            [
                                "Stored snapshot",
                                formatBytes(
                                    detail.promptDiagnostic
                                        .sourceSnapshotBytes,
                                ),
                            ],
                            [
                                "Selected context",
                                formatBytes(
                                    detail.promptDiagnostic.sourceContextBytes,
                                ),
                            ],
                            [
                                "Model summary",
                                formatBytes(
                                    detail.promptDiagnostic
                                        .promptProjectionBytes,
                                ),
                            ],
                            [
                                "Boundary",
                                formatBytes(
                                    detail.promptDiagnostic.promptBudgetBytes,
                                ),
                            ],
                            [
                                "Raw references",
                                detail.promptDiagnostic.rawReferenceCount,
                            ],
                            [
                                "Evidence entries",
                                detail.promptDiagnostic.manifestEntryCount,
                            ],
                            [
                                "Packed candidates",
                                detail.promptDiagnostic.promptIncludedCount,
                            ],
                            [
                                "Omitted candidates",
                                detail.promptDiagnostic.promptOmittedCount,
                            ],
                        ].map(([label, value]) => (
                            <div key={String(label)}>
                                <dt className="text-[9px] text-muted">
                                    {label}
                                </dt>
                                <dd className="mt-0.5 break-words text-[11px] font-medium tabular-nums text-ink">
                                    {value}
                                </dd>
                            </div>
                        ))}
                    </dl>
                    <div className="mt-3 grid gap-1 font-mono text-[9px] text-muted">
                        <div className="break-all">
                            source{" "}
                            {detail.promptDiagnostic.sourceContextSha256}
                        </div>
                        <div className="break-all">
                            prompt{" "}
                            {detail.promptDiagnostic.promptProjectionSha256}
                        </div>
                        <div className="break-all">
                            selection{" "}
                            {
                                detail.promptDiagnostic
                                    .selectionManifestSha256
                            }
                        </div>
                    </div>
                    {detail.promptDiagnostic.error && (
                        <p className="mt-2 text-[10px] text-error">
                            {detail.promptDiagnostic.error}
                        </p>
                    )}
                </section>
            )}

            <section>
                <h3 className="text-xs font-semibold text-ink">
                    Contextual projections
                </h3>
                <div className="mt-2">
                    {detail.snapshot.projections.map((projection) => (
                        <details
                            key={`${projection.scopeKind}:${projection.scopeKey}`}
                            className="border-t border-border py-3"
                        >
                            <summary className="flex cursor-pointer list-none items-center justify-between gap-3">
                                <div className="min-w-0">
                                    <div className="truncate text-[11px] font-medium text-ink">
                                        {projection.label}
                                    </div>
                                    <div className="mt-0.5 text-[9px] text-muted">
                                        {projection.scopeKind} ·{" "}
                                        {projection.sampleCount} samples ·{" "}
                                        {projection.sessionCount} sessions
                                    </div>
                                </div>
                                <StatusBadge status={projection.status} />
                            </summary>
                            <div className="mt-3 overflow-x-auto">
                                <table className="w-full text-[10px]">
                                    <thead className="text-muted">
                                        <tr>
                                            <th className="pb-1 pr-3 text-left font-medium">
                                                Metric
                                            </th>
                                            <th className="pb-1 pr-3 text-right font-medium">
                                                Value
                                            </th>
                                            <th className="pb-1 pr-3 text-right font-medium">
                                                Samples
                                            </th>
                                            <th className="pb-1 text-right font-medium">
                                                Confidence
                                            </th>
                                        </tr>
                                    </thead>
                                    <tbody>
                                        {projection.metrics.map((metric) => (
                                            <tr
                                                key={metric.name}
                                                className="border-t border-border"
                                            >
                                                <td className="py-1.5 pr-3 text-ink">
                                                    {metric.name}
                                                </td>
                                                <td className="py-1.5 pr-3 text-right tabular-nums text-ink">
                                                    {formatMetric(
                                                        metric.value,
                                                        metric.unit,
                                                    )}
                                                </td>
                                                <td className="py-1.5 pr-3 text-right tabular-nums text-muted">
                                                    {metric.sampleCount}
                                                </td>
                                                <td className="py-1.5 text-right tabular-nums text-muted">
                                                    {formatPercent(
                                                        metric.confidence,
                                                    )}
                                                </td>
                                            </tr>
                                        ))}
                                    </tbody>
                                </table>
                            </div>
                        </details>
                    ))}
                </div>
            </section>

            <section>
                <div className="flex flex-wrap items-end justify-between gap-3">
                    <div>
                        <h3 className="text-xs font-semibold text-ink">
                            What Isoform knew when it made this prediction
                        </h3>
                        <p className="mt-1 text-[9px] leading-4 text-muted">
                            Profile evidence available when each prediction was made.
                        </p>
                    </div>
                    {learningInfluence.length > 0 && (
                        <div className="text-[9px] tabular-nums text-muted">
                            {
                                learningInfluence.filter(
                                    ({ onset }) =>
                                        onset?.onsetPromptIncluded === true,
                                ).length
                            }{" "}
                            onset ·{" "}
                            {
                                learningInfluence.filter(
                                    ({ shadow }) =>
                                        shadow?.shadowContextIncluded === true,
                                ).length
                            }{" "}
                            Shadow
                        </div>
                    )}
                </div>
                <div className="mt-2">
                    {learningInfluence.map(({ episode, onset, shadow, learning }) => {
                        const evaluation = detail.evaluations.find(
                            (item) => item.taskEpisodeId === episode.id,
                        );
                        const scoreDelta =
                            typeof evaluation?.profileScore === "number" &&
                            typeof evaluation.neutralScore === "number"
                                ? evaluation.profileScore - evaluation.neutralScore
                                : undefined;
                        return (
                            <button
                                key={`${episode.id}:${learning.revisionId}`}
                                type="button"
                                className="flex w-full items-start justify-between gap-4 border-t border-border py-3 text-left hover:bg-ink/[0.025]"
                                onClick={() => onOpenTaskEpisode?.(episode.id)}
                            >
                                <div className="min-w-0 flex-1">
                                    <div className="flex flex-wrap items-center gap-2">
                                        <span className="text-[11px] font-medium text-ink">
                                            {learning.capabilityKey}
                                        </span>
                                        <StatusBadge
                                            status={
                                                onset?.onsetPromptIncluded
                                                    ? "onset_included"
                                                    : "onset_omitted"
                                            }
                                        />
                                        <StatusBadge
                                            status={
                                                shadow?.shadowContextIncluded
                                                    ? "shadow_included"
                                                    : "shadow_omitted"
                                            }
                                        />
                                        {evaluation && (
                                            <StatusBadge
                                                status={evaluation.winner}
                                            />
                                        )}
                                    </div>
                                    <div className="mt-1 text-[9px] leading-4 text-muted">
                                        {episode.task} · {learning.targetKind} ·{" "}
                                        {learning.learningKind}
                                    </div>
                                    <div className="mt-1 text-[9px] leading-4 text-muted">
                                        Onset {onset?.onsetPromptReason ?? "not recorded"} ·
                                        Shadow {" "}
                                        {shadow?.shadowContextReason ?? "not recorded"}
                                    </div>
                                    <div className="mt-1 break-all font-mono text-[9px] leading-4 text-muted">
                                        revision {learning.revisionId} · receipt{" "}
                                        {learning.materializationReceiptId}
                                    </div>
                                    <div className="mt-1 text-[9px] leading-4 text-muted">
                                        Available {formatDate(learning.availableAt)} ·
                                        authority{" "}
                                        {learning.authorityEffect}
                                        {evaluation
                                            ? ` · profile ${formatPercent(evaluation.profileScore)} vs neutral ${formatPercent(evaluation.neutralScore)} · lift ${formatSignedPercent(scoreDelta)}`
                                            : " · outcome comparison pending"}
                                    </div>
                                </div>
                                <ExternalLink className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted" />
                            </button>
                        );
                    })}
                    {learningInfluence.length === 0 && (
                        <p className="border-t border-border py-4 text-[10px] text-muted">
                            No verified evaluated learning has reached a frozen prediction yet.
                        </p>
                    )}
                </div>
            </section>

            <section>
                <h3 className="text-xs font-semibold text-ink">
                    Evidence and exclusions
                </h3>
                <div className="mt-2 overflow-x-auto">
                    <table className="w-full text-[10px]">
                        <thead className="text-muted">
                            <tr>
                                <th className="pb-1 pr-3 text-left font-medium">
                                    Source
                                </th>
                                <th className="pb-1 pr-3 text-left font-medium">
                                    Decision
                                </th>
                                <th className="pb-1 text-left font-medium">
                                    Reason
                                </th>
                            </tr>
                        </thead>
                        <tbody>
                            {detail.evidence.map((item) => (
                                <tr key={item.id} className="border-t border-border">
                                    <td className="py-1.5 pr-3 font-mono text-ink">
                                        {item.sourceKind}:{item.sourceId}
                                    </td>
                                    <td className="py-1.5 pr-3">
                                        {item.included ? (
                                            <span className="inline-flex items-center gap-1 text-success">
                                                <Check className="h-3 w-3" />
                                                included
                                            </span>
                                        ) : (
                                            <span className="text-warning">excluded</span>
                                        )}
                                    </td>
                                    <td className="py-1.5 text-muted">
                                        {item.reason}
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
                {detail.snapshot.exclusions.length > 0 && (
                    <CodeBlock
                        title="Profile exclusions"
                        value={prettyJSON(detail.snapshot.exclusions)}
                    />
                )}
                {detail.corrections.length > 0 && (
                    <CodeBlock
                        title="Append-only contradictions"
                        value={prettyJSON(detail.corrections)}
                    />
                )}
            </section>

            <section>
                <h3 className="text-xs font-semibold text-ink">
                    Frozen consumers
                </h3>
                <div className="mt-2">
                    {detail.episodes.map((episode) => (
                        <button
                            key={episode.id}
                            type="button"
                            className="flex w-full items-start justify-between gap-3 border-t border-border py-3 text-left hover:bg-ink/[0.025]"
                            onClick={() => onOpenTaskEpisode?.(episode.id)}
                        >
                            <div className="min-w-0">
                                <div className="truncate text-[11px] font-medium text-ink">
                                    {episode.task}
                                </div>
                                <div className="mt-0.5 truncate font-mono text-[9px] text-muted">
                                    {episode.id}
                                </div>
                                {episode.profileInfluenceRefs.length > 0 && (
                                    <div className="mt-1 break-all text-[9px] leading-4 text-muted">
                                        Profile refs:{" "}
                                        {episode.profileInfluenceRefs.join(
                                            ", ",
                                        )}
                                        {episode.profileConflict
                                            ? " · current evidence conflicted"
                                            : ""}
                                    </div>
                                )}
                                {episode.profileSelectionManifest.length > 0 && (
                                    <div className="mt-1 break-all text-[9px] leading-4 text-muted">
                                        Selection:{" "}
                                        {episode.profileSelectionManifest
                                            .filter((item) => item.included)
                                            .length}{" "}
                                        included ·{" "}
                                        {episode.profileSelectionManifest
                                            .filter((item) => !item.included)
                                            .length}{" "}
                                        omitted ·{" "}
                                        {episode.profileSelectionReason}
                                    </div>
                                )}
                                {episode.profilePromptSha256 && (
                                    <div className="mt-1 break-all font-mono text-[9px] leading-4 text-muted">
                                        source{" "}
                                        {shortHash(
                                            episode.profileSourceContextSha256,
                                        )}{" "}
                                        · prompt{" "}
                                        {shortHash(
                                            episode.profilePromptSha256,
                                        )}{" "}
                                        · selection{" "}
                                        {shortHash(
                                            episode.profileSelectionSha256,
                                        )}
                                    </div>
                                )}
                            </div>
                            <ExternalLink className="h-3.5 w-3.5 shrink-0 text-muted" />
                        </button>
                    ))}
                    {detail.shadowRuns.map((run) => (
                        <button
                            key={run.id}
                            type="button"
                            className="flex w-full items-start justify-between gap-3 border-t border-border py-3 text-left hover:bg-ink/[0.025]"
                            onClick={() => onOpenShadowRun?.(run.id)}
                        >
                            <div className="min-w-0">
                                <div className="truncate text-[11px] font-medium text-ink">
                                    {run.capabilityKey}
                                </div>
                                <div className="mt-0.5 truncate font-mono text-[9px] text-muted">
                                    {run.id} · {run.status}
                                </div>
                                {run.profileInfluenceRefs.length > 0 && (
                                    <div className="mt-1 break-all text-[9px] leading-4 text-muted">
                                        Profile refs:{" "}
                                        {run.profileInfluenceRefs.join(", ")}
                                        {run.profileConflict
                                            ? " · current evidence conflicted"
                                            : ""}
                                    </div>
                                )}
                            </div>
                            <ExternalLink className="h-3.5 w-3.5 shrink-0 text-muted" />
                        </button>
                    ))}
                    {detail.episodes.length === 0 &&
                        detail.shadowRuns.length === 0 && (
                            <p className="border-t border-border py-4 text-[10px] text-muted">
                                No TaskEpisode or ShadowRun has consumed this revision.
                            </p>
                        )}
                </div>
            </section>

            <section>
                <h3 className="text-xs font-semibold text-ink">
                    Paired neutral/profile evaluation
                </h3>
                <div className="mt-2">
                    {detail.ablations.map((run) => {
                        const evaluation = detail.evaluations.find(
                            (item) => item.ablationRunId === run.id,
                        );
                        return (
                            <details
                                key={run.id}
                                className="border-t border-border py-3"
                            >
                                <summary className="flex cursor-pointer list-none items-center justify-between gap-3">
                                    <div className="min-w-0">
                                        <div className="truncate font-mono text-[10px] text-ink">
                                            {run.id}
                                        </div>
                                        <div className="mt-0.5 text-[9px] text-muted">
                                            {formatDate(run.createdAt)} ·{" "}
                                            {run.model} · primary {run.primaryMode}
                                        </div>
                                    </div>
                                    <StatusBadge
                                        status={evaluation?.winner ?? "unscored"}
                                    />
                                </summary>
                                <div className="mt-3 grid gap-3 lg:grid-cols-2">
                                    <CodeBlock
                                        title={`Neutral · ${run.neutralStatus}`}
                                        value={prettyJSON(run.neutralResponse)}
                                    />
                                    <CodeBlock
                                        title={`Profile · ${run.profileStatus}`}
                                        value={prettyJSON(run.profileResponse)}
                                    />
                                </div>
                                {evaluation && (
                                    <div className="mt-3 space-y-3">
                                        <div className="grid grid-cols-2 gap-4 text-[10px] sm:grid-cols-4">
                                            <span>
                                                Neutral score{" "}
                                                <strong>
                                                    {formatPercent(
                                                        evaluation.neutralScore,
                                                    )}
                                                </strong>
                                            </span>
                                            <span>
                                                Profile score{" "}
                                                <strong>
                                                    {formatPercent(
                                                        evaluation.profileScore,
                                                    )}
                                                </strong>
                                            </span>
                                            <span>
                                                Neutral boundary{" "}
                                                <strong>
                                                    {evaluation.neutralBoundaryErrorMs ??
                                                        "-"}
                                                    ms
                                                </strong>
                                            </span>
                                            <span>
                                                Profile boundary{" "}
                                                <strong>
                                                    {evaluation.profileBoundaryErrorMs ??
                                                        "-"}
                                                    ms
                                                </strong>
                                            </span>
                                        </div>
                                        <div className="grid gap-4 lg:grid-cols-2">
                                            <AblationBranchMetrics
                                                label="Neutral dimensions"
                                                metrics={
                                                    evaluation.metrics?.neutral
                                                }
                                            />
                                            <AblationBranchMetrics
                                                label="Scores with patterns"
                                                metrics={
                                                    evaluation.metrics?.profile
                                                }
                                            />
                                        </div>
                                        <p className="text-[9px] leading-4 text-muted">
                                            Merge errors are scoped to{" "}
                                            {evaluation.metrics?.mergeScope ??
                                                "legacy evaluation"}
                                            ; the alternate branch remains
                                            diagnostic-only and never executes.
                                        </p>
                                    </div>
                                )}
                            </details>
                        );
                    })}
                    {detail.ablations.length === 0 && (
                        <p className="border-t border-border py-4 text-[10px] text-muted">
                            This revision has no paired onset runs yet.
                        </p>
                    )}
                </div>
            </section>

            <section>
                <h3 className="text-xs font-semibold text-ink">
                    Exact model attempts
                </h3>
                <div className="mt-2">
                    {detail.modelAttempts.map((attempt) => (
                        <AttemptDetail key={attempt.id} attempt={attempt} />
                    ))}
                </div>
            </section>
        </div>
    );
}

export function WorkProfileDebugSettings({
    initialProfileID,
    onOpenTaskEpisode,
    onOpenShadowRun,
}: {
    initialProfileID?: string;
    onOpenTaskEpisode?: (id: string) => void;
    onOpenShadowRun?: (id: string) => void;
}) {
    const [snapshot, setSnapshot] =
        useState<WorkPatternProfileDebugSnapshot>(emptySnapshot);
    const [selectedID, setSelectedID] = useState<string | null>(
        initialProfileID ?? null,
    );
    const [detail, setDetail] =
        useState<WorkPatternProfileDebugDetail | null>(null);
    const [query, setQuery] = useState("");
    const [status, setStatus] = useState("all");
    const [loading, setLoading] = useState(true);
    const [detailLoading, setDetailLoading] = useState(false);
    const [compactDetailOpen, setCompactDetailOpen] = useState(false);

    const loadSnapshot = useCallback(async () => {
        setLoading(true);
        try {
            const next = await GetWorkPatternProfileDebugSnapshot(200);
            const normalized = {
                ...next,
                profiles: Array.isArray(next.profiles) ? next.profiles : [],
                rollout: next.rollout ?? emptyRollout,
                learningInfluence:
                    next.learningInfluence ?? emptyLearningInfluence,
                learningCohorts: {
                    ...(next.learningCohorts ?? emptyLearningCohorts),
                    cohorts: Array.isArray(next.learningCohorts?.cohorts)
                        ? next.learningCohorts.cohorts.map((cohort) => ({
                              ...cohort,
                              blockers: Array.isArray(cohort.blockers)
                                  ? cohort.blockers
                                  : [],
                              chains: Array.isArray(cohort.chains)
                                  ? cohort.chains
                                  : [],
                          }))
                        : [],
                },
            };
            setSnapshot(normalized);
            setSelectedID((current) =>
                normalized.profiles.some((item) => item.id === current)
                    ? current
                    : normalized.profiles[0]?.id ?? null,
            );
        } catch (error) {
            toast.error(`Could not load observed patterns: ${String(error)}`);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void loadSnapshot();
    }, [loadSnapshot]);

    useEffect(() => {
        if (initialProfileID) {
            setSelectedID(initialProfileID);
            setCompactDetailOpen(true);
        }
    }, [initialProfileID]);

    useEffect(() => {
        if (!selectedID) {
            setDetail(null);
            return;
        }
        let cancelled = false;
        setDetailLoading(true);
        GetWorkPatternProfileDebugDetail(selectedID, 200)
            .then((next) => {
                if (!cancelled) setDetail(next);
            })
            .catch((error) => {
                if (!cancelled) {
                    setDetail(null);
                    toast.error(
                        `Could not load work pattern: ${String(error)}`,
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

    const profiles = useMemo(() => {
        const normalizedQuery = query.trim().toLowerCase();
        return snapshot.profiles.filter((profile) => {
            if (status !== "all" && profile.status !== status) return false;
            if (!normalizedQuery) return true;
            return `${profile.id} ${profile.sha256} ${profile.policyVersion} ${profile.status}`
                .toLowerCase()
                .includes(normalizedQuery);
        });
    }, [query, snapshot.profiles, status]);

    return (
        <div>
            <div className="border-b border-border px-4 py-4">
                <div className="flex flex-wrap items-center justify-between gap-3">
                    <div className="min-w-0">
                        <h2 className="text-sm font-semibold text-ink">
                            Work patterns
                        </h2>
                        <p className="mt-1 text-[10px] text-muted">
                            See which patterns Isoform tested, whether they improved later predictions, and which evidence was excluded.
                        </p>
                    </div>
                    <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={loading}
                        onClick={() => void loadSnapshot()}
                    >
                        {loading ? (
                            <Loader2 className="h-3.5 w-3.5 animate-spin" />
                        ) : (
                            <RefreshCw className="h-3.5 w-3.5" />
                        )}
                        Refresh
                    </Button>
                </div>
                <div className="mt-4">
                    <RolloutStrip rollout={snapshot.rollout} />
                </div>
                <LearningInfluenceStrip
                    proof={snapshot.learningInfluence}
                    onOpenTaskEpisode={onOpenTaskEpisode}
                    onOpenShadowRun={onOpenShadowRun}
                />
                <LearningCohortStrip
                    snapshot={snapshot.learningCohorts}
                    onOpenTaskEpisode={onOpenTaskEpisode}
                    onOpenShadowRun={onOpenShadowRun}
                />
                <div className="mt-3 flex flex-col gap-2 lg:flex-row lg:items-center">
                    <div className="relative min-w-0 flex-1">
                        <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted" />
                        <input
                            value={query}
                            onChange={(event) => setQuery(event.target.value)}
                            className="h-8 w-full border border-border bg-panel pl-7 pr-2 text-xs text-ink outline-none focus:border-ink/40"
                            placeholder="Search pattern or record hash"
                        />
                    </div>
                    <Select value={status} onValueChange={setStatus}>
                        <SelectTrigger className="h-8 w-full text-xs lg:w-[150px]">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                            <SelectItem value="all">All statuses</SelectItem>
                            <SelectItem value="qualified">Ready to use</SelectItem>
                            <SelectItem value="insufficient">
                                Not enough evidence
                            </SelectItem>
                        </SelectContent>
                    </Select>
                </div>
            </div>
            <DebugMasterDetail
                compactDetailOpen={compactDetailOpen}
                detailKey={selectedID}
                listLabel="Work patterns"
                detailLabel="Work pattern details"
                onBack={() => setCompactDetailOpen(false)}
                list={
                    profiles.length > 0 ? (
                        profiles.map((profile) => (
                            <ProfileListRow
                                key={profile.id}
                                profile={profile}
                                selected={profile.id === selectedID}
                                onSelect={() => {
                                    setSelectedID(profile.id);
                                    setCompactDetailOpen(true);
                                }}
                            />
                        ))
                    ) : (
                        <div className="px-4 py-14 text-center text-xs text-muted">
                            {loading
                                ? "Loading observed patterns..."
                                : "No observed patterns match this view."}
                        </div>
                    )
                }
                detail={
                    <WorkProfileDetail
                        detail={detail}
                        loading={detailLoading}
                        onOpenTaskEpisode={onOpenTaskEpisode}
                        onOpenShadowRun={onOpenShadowRun}
                    />
                }
            />
        </div>
    );
}
