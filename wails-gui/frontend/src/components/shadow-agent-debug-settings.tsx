import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
    AlertCircle,
    Check,
    ChevronRight,
    Copy,
    FileText,
    Loader2,
    RefreshCw,
    Search,
    ShieldCheck,
} from "lucide-react";
import { toast } from "sonner";
import { capabilityLabel } from "@/lib/capability-label";
import { Button } from "@/components/ui/button";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import {
    GetShadowAgentDebugRun,
    GetShadowAgentDebugSnapshot,
    type ShadowAgentDebugSnapshot,
    type ShadowCapabilityDimensionProfile,
    type ShadowCapabilityProfile,
    type ShadowCapabilityQualification,
    type ShadowCorrelationAttempt,
    type ShadowEvaluationDimension,
    type ShadowExecutionAttempt,
    type ShadowRunDebugDetail,
    type ShadowRunDebugSummary,
    type ShadowRunEvaluation,
    type SelfModelEntry,
} from "@/lib/wails-shim-app";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { BackgroundUnderstandingDebugSettings } from "./background-understanding-debug-settings";
import { SelfDreamDebugSettings } from "./self-dream-debug-settings";
import { TaskShadowDebugSettings } from "./task-shadow-debug-settings";
import { TaskShadowObserverDebugSettings } from "./task-shadow-observer-debug-settings";
import { WorkProfileDebugSettings } from "./work-profile-debug-settings";
import { ComputerUseAppAccessDebugSettings } from "./computer-use-app-access-debug-settings";
import { DebugMasterDetail } from "./debug-master-detail";
import {
    DebugViewSwitcher,
    type DebugViewGroup,
} from "./debug-view-switcher";

type DebugView =
    | "runs"
    | "taskEpisodes"
    | "observer"
    | "workProfile"
    | "selfDream"
    | "background"
    | "appAccess"
    | "profiles"
    | "qualifications"
    | "selfModel"
    | "attempts";

const emptySnapshot: ShadowAgentDebugSnapshot = {
    generatedAt: 0,
    runs: [],
    profiles: [],
    qualifications: [],
    selfModel: [],
    attempts: [],
};

function shadowViewUsesSummarySnapshot(view: DebugView): boolean {
    return view === "runs" ||
        view === "profiles" ||
        view === "qualifications" ||
        view === "selfModel" ||
        view === "attempts";
}

function normalizeShadowAgentDebugSnapshot(
    value: unknown,
): ShadowAgentDebugSnapshot {
    if (!value || typeof value !== "object") {
        throw new Error("Shadow learning diagnostics returned no summary data.");
    }
    const candidate = value as Partial<ShadowAgentDebugSnapshot>;
    if (
        typeof candidate.generatedAt !== "number" ||
        !Array.isArray(candidate.runs) ||
        !Array.isArray(candidate.profiles) ||
        !Array.isArray(candidate.qualifications) ||
        !Array.isArray(candidate.selfModel) ||
        !Array.isArray(candidate.attempts)
    ) {
        throw new Error("Shadow learning diagnostics returned an incomplete summary.");
    }
    return candidate as ShadowAgentDebugSnapshot;
}

function formatDateTime(value?: number): string {
    if (!value) return "-";
    return new Date(value).toLocaleString();
}

function formatPercent(value?: number): string {
    if (typeof value !== "number") return "-";
    return `${Math.round(value * 100)}%`;
}

function prettyJSON(value: unknown): string {
    if (typeof value === "string") {
        const trimmed = value.trim().replace(/^```(?:json)?\s*/i, "").replace(/\s*```$/, "");
        try {
            return JSON.stringify(JSON.parse(trimmed), null, 2);
        } catch {
            return value;
        }
    }
    return JSON.stringify(value, null, 2);
}

function statusTone(status: string): string {
    switch (status) {
        case "evaluated":
        case "completed":
        case "ready":
        case "allow":
            return "bg-success/15 text-success";
        case "observed":
        case "awaiting_observation":
        case "no_candidates":
        case "collecting":
        case "safety_unknown":
            return "bg-warning-bg text-warning";
        case "error":
        case "unsupported_runner":
        case "deny":
        case "action_unreliable":
        case "safety_concern":
            return "bg-error-bg text-error";
        default:
            return "bg-muted/15 text-muted";
    }
}

function statusLabel(status: string): string {
    const labels: Record<string, string> = {
        awaiting_observation: "Waiting for your result",
        observed: "Your result found",
        evaluated: "Comparison complete",
        expired: "No result found in time",
        collecting: "Learning from more examples",
        action_unreliable: "Predictions differ too often",
        safety_unknown: "Needs more safety evidence",
        safety_concern: "Safety issue found",
        ready: "Accuracy target met",
        blocked: "Not ready",
        qualified: "Ready to suggest",
        proposed: "Suggested",
        already_proposed: "Already suggested",
        provisional: "Draft",
        active: "Supported",
        retracted: "Retracted",
        behavior_derived: "Inferred from behavior",
        behavior_and_explicit_approval: "Confirmed by you",
        source_backed: "Supported by a stated reason",
        explicit_user_statement: "Stated by you",
        unknown: "Reason unknown",
        completed: "Completed",
        no_candidates: "No matching user result",
        comparison_completed: "Results compared",
        need_qualified_evaluations: "Needs more usable comparisons",
        need_prediction_source_diversity: "Needs more prediction sources",
        need_observation_source_diversity: "Needs more user-result sources",
        need_context_diversity: "Needs more work contexts",
        action_reliability_below_threshold: "Action match is below target",
        need_safety_coverage: "Needs more safety checks",
        qualified_unsafe_evidence: "Usable evidence includes unsafe behavior",
        error: "Error",
        unsupported_runner: "Private practice unavailable",
        allow: "Allowed",
        deny: "Blocked",
    };
    if (labels[status]) return labels[status];
    const label = status.replaceAll("_", " ");
    return label.charAt(0).toUpperCase() + label.slice(1);
}

function riskTierLabel(riskTier: string): string {
    const labels: Record<string, string> = {
        read_only: "Reads only",
        local_write: "May change local files",
        external_write: "May change an external app",
        privileged: "Needs elevated access",
        unknown: "Effect is unknown",
        high_risk: "High-impact action",
    };
    return labels[riskTier] ?? statusLabel(riskTier);
}

function diagnosticReasonLabel(reason?: string): string {
    if (!reason) return "-";
    return /^[a-z0-9_]+$/.test(reason) ? statusLabel(reason) : reason;
}

const LEGACY_GENERIC_SHADOW_ACTION = "Independently perform the same task in isolated shadow mode.";

function predictedActionLabel(value: string): string {
    if (value === LEGACY_GENERIC_SHADOW_ACTION) {
        return "Goal: do the same task privately, then compare the result.";
    }
    return value;
}

function ShadowRuntimeAttemptView({ attempt }: { attempt: ShadowExecutionAttempt }) {
    const { t } = useTranslation("settings");
    const allowedReads = attempt.decisions.filter((item) =>
        item.decision === "allow" && item.action.effect === "read"
    ).length;
    const blockedEffects = attempt.decisions.filter((item) => item.decision === "deny").length;

    return (
        <div className="border-l-2 border-border pl-3 space-y-2">
            <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                    <div className="text-[11px] font-medium text-ink">
                        {attempt.runtimeVersion} · {attempt.model || "-"}
                    </div>
                    <div className="text-[10px] font-mono text-muted break-all">
                        {attempt.agentRunId || attempt.id}
                    </div>
                </div>
                <StatusBadge status={attempt.status} />
            </div>
            <div className="text-[10px] text-muted">
                {formatDateTime(attempt.startedAt)} · {allowedReads} {t("shadowDebug.runtimeReads")} · {blockedEffects} {t("shadowDebug.runtimeBlocked")}
            </div>
            <div className="text-[10px] font-mono text-muted break-all">{attempt.planHash}</div>
            {attempt.decisions.length === 0 ? (
                <div className="text-[11px] text-muted">{t("shadowDebug.runtimeNoDecisions")}</div>
            ) : (
                <div className="overflow-x-auto">
                    <table className="w-full text-[10px]">
                        <thead className="text-muted">
                            <tr>
                                <th className="text-left font-medium pb-1 pr-2">Capability</th>
                                <th className="text-left font-medium pb-1 pr-2">Effect</th>
                                <th className="text-left font-medium pb-1">Decision</th>
                            </tr>
                        </thead>
                        <tbody>
                            {attempt.decisions.map((evaluation, index) => (
                                <tr key={`${evaluation.tool_use_id || evaluation.action.capability}-${index}`} className="border-t border-border">
                                    <td className="py-1.5 pr-2 text-ink break-all">{evaluation.action.capability}</td>
                                    <td className="py-1.5 pr-2 text-muted">{evaluation.action.effect}</td>
                                    <td className="py-1.5">
                                        <StatusBadge status={evaluation.decision} />
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            )}
            {attempt.error && <div className="text-[11px] text-error">{attempt.error}</div>}
            <CodeBlock title={t("shadowDebug.runtimePrompt")} value={attempt.prompt} />
            <CodeBlock title={t("shadowDebug.runtimeOutput")} value={attempt.assistantOutput ?? ""} />
        </div>
    );
}

function StatusBadge({ status }: { status: string }) {
    return (
        <span
            className={`inline-flex px-1.5 py-0.5 text-[10px] font-medium rounded ${statusTone(status)}`}
            title={status}
        >
            {statusLabel(status)}
        </span>
    );
}

function CodeBlock({ title, value }: { title: string; value: string }) {
    const handleCopy = useCallback(async () => {
        try {
            await navigator.clipboard.writeText(value);
            toast.success(title);
        } catch (error) {
            toast.error(String(error));
        }
    }, [title, value]);

    if (!value) return null;
    return (
        <section className="border-t border-border pt-3 space-y-1.5">
            <div className="flex items-center justify-between gap-3">
                <h3 className="text-[11px] font-semibold text-muted">{title}</h3>
                <button
                    type="button"
                    className="h-7 w-7 inline-flex items-center justify-center text-muted hover:text-ink"
                    onClick={handleCopy}
                    title={title}
                >
                    <Copy className="h-3.5 w-3.5" />
                </button>
            </div>
            <pre className="max-h-[320px] overflow-auto whitespace-pre-wrap break-words bg-ink/[0.04] p-2 text-[10px] font-mono text-ink/80">
                {value}
            </pre>
        </section>
    );
}

function DimensionCell({
    label,
    dimension,
}: {
    label: string;
    dimension: ShadowEvaluationDimension;
}) {
    return (
        <div className="min-w-0 border-l border-border pl-2 first:border-l-0 first:pl-0">
            <div className="text-[10px] text-muted">{label}</div>
            <div className="text-xs font-medium text-ink truncate">{dimension.assessment}</div>
            <div className="text-[10px] text-muted">
                {dimension.score == null ? "score -" : `score ${dimension.score.toFixed(2)}`}
                {" · "}
                {formatPercent(dimension.confidence)}
            </div>
        </div>
    );
}

function EvaluationRow({ evaluation }: { evaluation: ShadowRunEvaluation }) {
    return (
        <div className="border-t border-border pt-2 mt-2">
            <div className="grid grid-cols-2 sm:grid-cols-5 gap-y-2">
                <DimensionCell label="action" dimension={evaluation.action} />
                <DimensionCell label="outcome" dimension={evaluation.outcome} />
                <DimensionCell label="timing" dimension={evaluation.timing} />
                <DimensionCell label="reasoning" dimension={evaluation.reasoning} />
                <DimensionCell label="safety" dimension={evaluation.safety} />
            </div>
            {evaluation.summary && (
                <p className="text-[11px] text-muted mt-2">{evaluation.summary}</p>
            )}
            <div className="text-[10px] text-muted/70 mt-1 font-mono break-all">
                {evaluation.comparisonVersion} · {evaluation.evaluatorModel || "-"} · {evaluation.comparatorHash || "-"}
            </div>
            <CodeBlock title="Comparator prompt" value={evaluation.comparatorPrompt ?? ""} />
            <CodeBlock title="Evaluator output" value={prettyJSON(evaluation.evaluatorRaw ?? "")} />
        </div>
    );
}

function RunDetail({
    detail,
    loading,
}: {
    detail: ShadowRunDebugDetail | null;
    loading: boolean;
}) {
    const { t } = useTranslation("settings");
    if (loading) {
        return (
            <div className="h-full flex items-center justify-center text-muted">
                <Loader2 className="h-5 w-5 animate-spin" />
            </div>
        );
    }
    if (!detail) {
        return <div className="h-full flex items-center justify-center text-xs text-muted">{t("shadowDebug.selectRun")}</div>;
    }

    const { run, observations, evaluations } = detail.detail;
    const executionAttempts = Array.isArray(detail.executionAttempts)
        ? detail.executionAttempts
        : [];
    const evaluationsByObservation = new Map<string, ShadowRunEvaluation[]>();
    for (const evaluation of evaluations) {
        const items = evaluationsByObservation.get(evaluation.observationId) ?? [];
        items.push(evaluation);
        evaluationsByObservation.set(evaluation.observationId, items);
    }

    return (
        <div className="space-y-3">
            <header className="space-y-1">
                <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                        <h2 className="text-sm font-semibold text-ink break-words">{capabilityLabel(run.capabilityKey)}</h2>
                        <div className="text-[9px] font-mono text-muted break-all">{run.capabilityKey}</div>
                        <div className="text-[10px] font-mono text-muted break-all">{run.id}</div>
                    </div>
                    <StatusBadge status={detail.detail.status} />
                </div>
                <div className="text-[10px] text-muted">
                    {run.sourceKind}:{run.sourceId} · {formatDateTime(run.createdAt)}
                </div>
            </header>

            <section className="border-t border-border pt-3 space-y-2">
                <h3 className="text-[11px] font-semibold text-muted">{t("shadowDebug.prediction")}</h3>
                <dl className="grid grid-cols-[110px_minmax(0,1fr)] gap-x-3 gap-y-1 text-[11px]">
                    <dt className="text-muted">goal</dt>
                    <dd className="text-ink">{run.prediction.goal}</dd>
                    <dt className="text-muted">action</dt>
                    <dd className="text-ink" title={run.prediction.predictedAction}>
                        {predictedActionLabel(run.prediction.predictedAction)}
                    </dd>
                    <dt className="text-muted">result</dt>
                    <dd className="text-ink">{run.prediction.expectedResult}</dd>
                    <dt className="text-muted">timing</dt>
                    <dd className="text-ink">{run.prediction.expectedTiming || "-"}</dd>
                    <dt className="text-muted">confidence</dt>
                    <dd className="text-ink">
                        goal {formatPercent(run.prediction.goalConfidence)} · action {formatPercent(run.prediction.actionConfidence)} · result {formatPercent(run.prediction.resultConfidence)} · timing {formatPercent(run.prediction.timingConfidence)}
                    </dd>
                    <dt className="text-muted">hash</dt>
                    <dd className="font-mono text-[10px] text-ink break-all">{run.predictionHash}</dd>
                </dl>
                {run.prediction.reasoningHypotheses.length > 0 && (
                    <div className="space-y-1">
                        {run.prediction.reasoningHypotheses.map((hypothesis, index) => (
                            <div key={`${hypothesis.statement}-${index}`} className="border-l-2 border-border pl-2">
                                <div className="text-[11px] text-ink">{hypothesis.statement}</div>
                                <div className="text-[10px] text-muted">
                                    {hypothesis.basis || "hypothesis"} · {formatPercent(hypothesis.confidence)}
                                </div>
                            </div>
                        ))}
                    </div>
                )}
                {run.prediction.candidateExecutionPlan && (
                    <CodeBlock title={t("shadowDebug.candidatePlan")} value={prettyJSON(run.prediction.candidateExecutionPlan)} />
                )}
            </section>

            <section className="border-t border-border pt-3 space-y-3">
                <div className="flex items-center gap-2">
                    <ShieldCheck className="h-3.5 w-3.5 text-muted" />
                    <h3 className="text-[11px] font-semibold text-muted">{t("shadowDebug.runtime")}</h3>
                </div>
                <p className="text-[11px] text-muted">{t("shadowDebug.runtimeBoundary")}</p>
                {executionAttempts.length === 0 ? (
                    <div className="text-[11px] text-muted">{t("shadowDebug.runtimeEmpty")}</div>
                ) : executionAttempts.map((attempt) => (
                    <ShadowRuntimeAttemptView key={attempt.id} attempt={attempt} />
                ))}
            </section>

            <CodeBlock title={t("shadowDebug.frozenContext")} value={run.context.promptContext} />

            <section className="border-t border-border pt-3 space-y-3">
                <div className="flex items-center justify-between">
                    <h3 className="text-[11px] font-semibold text-muted">{t("shadowDebug.timeline")}</h3>
                    <span className="text-[10px] text-muted">{observations.length} / {evaluations.length}</span>
                </div>
                {observations.length === 0 ? (
                    <div className="text-[11px] text-muted">{t("shadowDebug.noObservations")}</div>
                ) : observations.map((observation) => (
                    <div key={observation.id} className="border-l-2 border-border pl-3">
                        <div className="flex items-center justify-between gap-2">
                            <span className="text-[10px] font-mono text-muted">{observation.sourceKind}:{observation.sourceId}</span>
                            <span className="text-[10px] text-muted">{formatDateTime(observation.observedAt)}</span>
                        </div>
                        <div className="text-[11px] text-ink mt-1">{observation.observedAction}</div>
                        {observation.observedResult && (
                            <div className="text-[11px] text-success mt-1">{observation.observedResult}</div>
                        )}
                        {observation.reasoningEvidence.length === 0 ? (
                            <div className="text-[10px] text-muted mt-1">reasoning: unknown</div>
                        ) : observation.reasoningEvidence.map((evidence, index) => (
                            <div key={`${evidence.statement}-${index}`} className="text-[10px] text-muted mt-1">
                                {evidence.kind} · {formatPercent(evidence.confidence)} · {evidence.statement}
                            </div>
                        ))}
                        {(evaluationsByObservation.get(observation.id) ?? []).map((evaluation) => (
                            <EvaluationRow key={evaluation.id} evaluation={evaluation} />
                        ))}
                    </div>
                ))}
            </section>

            <section className="border-t border-border pt-3 space-y-1">
                <div className="flex items-center gap-2">
                    <FileText className="h-3.5 w-3.5 text-muted" />
                    <h3 className="text-[11px] font-semibold text-muted">{t("shadowDebug.memoryProjection")}</h3>
                    {detail.memoryProjection.exists ? <Check className="h-3.5 w-3.5 text-success" /> : null}
                </div>
                <div className="text-[10px] font-mono text-muted break-all">{detail.memoryProjection.path || "-"}</div>
                {detail.memoryProjection.error && (
                    <div className="text-[11px] text-error">{detail.memoryProjection.error}</div>
                )}
                {detail.memoryProjection.content && (
                    <pre className="max-h-[320px] overflow-auto whitespace-pre-wrap bg-ink/[0.04] p-2 text-[10px] font-mono text-ink/80">
                        {detail.memoryProjection.content}
                    </pre>
                )}
            </section>
        </div>
    );
}

function ProfileDimensionRow({
    label,
    dimension,
}: {
    label: string;
    dimension: ShadowCapabilityDimensionProfile;
}) {
    const assessments = Object.entries(dimension.qualifiedAssessmentCounts ?? {})
        .filter(([, count]) => count > 0)
        .map(([assessment, count]) => `${assessment} ${count}`)
        .join(", ");
    return (
        <tr className="border-t border-border">
            <td className="py-2 pr-2 font-medium text-ink">{label}</td>
            <td className="py-2 pr-2 tabular-nums">{dimension.qualifiedCount}/{dimension.totalCount}</td>
            <td className="py-2 pr-2 tabular-nums">{dimension.unknownCount}</td>
            <td className="py-2 pr-2 tabular-nums">{dimension.lowConfidenceCount}</td>
            <td className="py-2 pr-2 tabular-nums">{formatPercent(dimension.reliability)}</td>
            <td className="py-2 text-muted">{assessments || "-"}</td>
        </tr>
    );
}

function ProfileDetail({ profile }: { profile: ShadowCapabilityProfile | null }) {
    const { t } = useTranslation("settings");
    if (!profile) {
        return <div className="h-full flex items-center justify-center text-xs text-muted">{t("shadowDebug.selectProfile")}</div>;
    }
    const dimensions: Array<[string, ShadowCapabilityDimensionProfile]> = [
        ["action", profile.action],
        ["outcome", profile.outcome],
        ["timing", profile.timing],
        ["reasoning", profile.reasoning],
        ["safety", profile.safety],
    ];
    return (
        <div className="space-y-3">
            <header className="space-y-1">
                <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                        <h2 className="text-sm font-semibold text-ink break-words">{capabilityLabel(profile.capabilityKey)}</h2>
                        <div className="text-[9px] font-mono text-muted break-all">{profile.capabilityKey}</div>
                        <div className="text-[10px] font-mono text-muted break-all">{profile.id}</div>
                    </div>
                    <StatusBadge status={profile.readiness.status} />
                </div>
                <div className="text-[10px] text-muted">
                    {profile.policyVersion} · {formatDateTime(profile.createdAt)}
                </div>
            </header>

            <div className="border-y border-border py-2 text-[11px] text-muted">
                {t("shadowDebug.profileBoundary")}
            </div>

            <section className="space-y-2">
                <h3 className="text-[11px] font-semibold text-muted">{t("shadowDebug.profileCoverage")}</h3>
                <div className="grid grid-cols-2 gap-px border-y border-border bg-border py-px sm:grid-cols-3 lg:grid-cols-6">
                    {[
                        ["Usable comparisons", `${profile.qualifiedEvaluationCount}/${profile.evaluationCount}`],
                        ["Private practice", profile.runCount],
                        ["Prediction sources", profile.predictionSourceCount],
                        ["User-result sources", profile.observationSourceCount],
                        ["Source pairs", profile.sourcePairCount],
                        ["Work contexts", profile.contextCount],
                    ].map(([label, value]) => (
                        <div key={String(label)} className="min-w-0 bg-background px-2 py-1.5">
                            <div className="text-[10px] leading-3 text-muted">{label}</div>
                            <div className="text-sm font-semibold text-ink tabular-nums">{value}</div>
                        </div>
                    ))}
                </div>
                {profile.readiness.reasons.length > 0 && (
                    <div>
                        <div className="text-[10px] text-muted mb-1">{t("shadowDebug.profileReasons")}</div>
                        <div className="flex flex-wrap gap-1">
                            {profile.readiness.reasons.map((reason) => (
                                <span
                                    key={reason}
                                    title={reason}
                                    className="px-1.5 py-0.5 text-[10px] bg-warning-bg text-warning"
                                >
                                    {diagnosticReasonLabel(reason)}
                                </span>
                            ))}
                        </div>
                    </div>
                )}
            </section>

            <section className="border-t border-border pt-3">
                <h3 className="text-[11px] font-semibold text-muted mb-2">{t("shadowDebug.profileDimensions")}</h3>
                <div className="overflow-x-auto">
                    <table className="w-full text-[10px]">
                        <thead className="text-left text-muted">
                            <tr>
                                <th className="font-medium pb-1">dimension</th>
                                <th className="font-medium pb-1">qualified</th>
                                <th className="font-medium pb-1">unknown</th>
                                <th className="font-medium pb-1">low confidence</th>
                                <th className="font-medium pb-1">reliability</th>
                                <th className="font-medium pb-1">qualified assessments</th>
                            </tr>
                        </thead>
                        <tbody>
                            {dimensions.map(([label, dimension]) => (
                                <ProfileDimensionRow key={label} label={label} dimension={dimension} />
                            ))}
                        </tbody>
                    </table>
                </div>
            </section>

            <section className="border-t border-border pt-3">
                <div className="flex items-center justify-between gap-2 mb-2">
                    <h3 className="text-[11px] font-semibold text-muted">{t("shadowDebug.profileEvidence")}</h3>
                    <span className="text-[10px] font-mono text-muted">{profile.evidenceHash}</span>
                </div>
                <div className="space-y-0">
                    {profile.evidence.map((item) => (
                        <div key={item.evaluationId} className="border-t border-border py-2 first:border-t-0">
                            <div className="flex items-start justify-between gap-3">
                                <div className="min-w-0 text-[10px] font-mono text-muted break-all">
                                    {item.predictionSourceKind}:{item.predictionSourceId}
                                    {" -> "}
                                    {item.observationSourceKind}:{item.observationSourceId}
                                </div>
                                <span className={item.qualified ? "text-[10px] text-success" : "text-[10px] text-warning"}>
                                    {item.qualified ? "qualified" : item.exclusionReason || "excluded"}
                                </span>
                            </div>
                            <div className="text-[10px] text-ink mt-1">
                                action {item.actionAssessment} · outcome {item.outcomeAssessment} · timing {item.timingAssessment} · reasoning {item.reasoningAssessment} · safety {item.safetyAssessment}
                            </div>
                            <div className="text-[10px] font-mono text-muted/70 mt-1 break-all">
                                {item.shadowRunId} · {item.evaluationId} · {formatDateTime(item.evaluationCreatedAt)}
                            </div>
                        </div>
                    ))}
                </div>
            </section>
        </div>
    );
}

function QualificationDetail({
    qualification,
}: {
    qualification: ShadowCapabilityQualification | null;
}) {
    const { t } = useTranslation("settings");
    if (!qualification) {
        return <div className="h-full flex items-center justify-center text-xs text-muted">{t("shadowDebug.selectQualification")}</div>;
    }
    const thresholdRows = [
        ["evaluations", qualification.actual.qualifiedEvaluations, qualification.thresholds.minQualifiedEvaluations],
        ["prediction sources", qualification.actual.predictionSources, qualification.thresholds.minPredictionSources],
        ["observation sources", qualification.actual.observationSources, qualification.thresholds.minObservationSources],
        ["contexts", qualification.actual.contexts, qualification.thresholds.minContexts],
        ["safety", qualification.actual.qualifiedSafetyAssessments, qualification.thresholds.minQualifiedSafetyAssessments],
        ["practice runs", qualification.actual.completedRuntimeAttempts, qualification.thresholds.minCompletedRuntimeAttempts],
    ] as const;
    return (
        <div className="space-y-3">
            <header className="space-y-1">
                <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                        <h2 className="text-sm font-semibold text-ink break-words">{capabilityLabel(qualification.capabilityKey)}</h2>
                        <div className="text-[9px] font-mono text-muted break-all">{qualification.capabilityKey}</div>
                        <div className="text-[10px] font-mono text-muted break-all">{qualification.id}</div>
                    </div>
                    <StatusBadge status={qualification.status} />
                </div>
                <div className="text-[10px] text-muted">
                    {qualification.riskTier} · {qualification.policyVersion} · {formatDateTime(qualification.createdAt)}
                </div>
            </header>

            <div className="border-y border-border py-2 text-[11px] text-muted">
                {t("shadowDebug.qualificationBoundary")}
            </div>

            <section className="space-y-2">
                <h3 className="text-[11px] font-semibold text-muted">{t("shadowDebug.qualificationThresholds")}</h3>
                <div className="grid grid-cols-2 sm:grid-cols-3 border-y border-border">
                    {thresholdRows.map(([label, actual, required]) => (
                        <div key={label} className="border-b border-l border-border p-2 first:border-l-0">
                            <div className="text-[10px] text-muted">{label}</div>
                            <div className={actual >= required ? "text-sm font-semibold text-success" : "text-sm font-semibold text-warning"}>
                                {actual}/{required}
                            </div>
                        </div>
                    ))}
                </div>
                <div className="text-[10px] text-muted">
                    action reliability {formatPercent(qualification.actual.actionReliability)} / {formatPercent(qualification.thresholds.minActionReliability)}
                </div>
                {qualification.reasons.length > 0 && (
                    <div className="flex flex-wrap gap-1">
                        {qualification.reasons.map((reason) => (
                            <span key={reason} className="px-1.5 py-0.5 text-[10px] bg-warning-bg text-warning">
                                {reason}
                            </span>
                        ))}
                    </div>
                )}
            </section>

            <section className="border-t border-border pt-3">
                <h3 className="text-[11px] font-semibold text-muted mb-2">{t("shadowDebug.qualificationEffects")}</h3>
                <div className="divide-y divide-border border-y border-border">
                    {qualification.effects.map((effect, index) => (
                        <div key={`${effect.capability}:${effect.resource ?? ""}:${index}`} className="py-2">
                            <div className="flex flex-wrap items-center gap-2">
                                <span className="font-mono text-[10px] text-ink">{effect.capability}</span>
                                <span className="text-[10px] text-muted">{effect.effect}</span>
                                <span className="text-[10px] text-muted">{effect.reversible ? "reversible" : "irreversible"}</span>
                            </div>
                            {effect.resource && <div className="mt-1 font-mono text-[10px] text-muted break-all">{effect.resource}</div>}
                        </div>
                    ))}
                </div>
            </section>

            <CodeBlock title={t("shadowDebug.qualificationEvidence")} value={prettyJSON({
                profileId: qualification.profileId,
                profileEvidenceHash: qualification.profileEvidenceHash,
                runtimeEvidenceHash: qualification.runtimeEvidenceHash,
                runtimeAttemptIds: qualification.runtimeAttemptIds,
                representativeShadowRunId: qualification.representativeShadowRunId,
                candidatePlanHash: qualification.candidatePlanHash,
                proposalInitiativeId: qualification.proposalInitiativeId,
            })} />
        </div>
    );
}

function SelfModelDetail({ entry }: { entry: SelfModelEntry | null }) {
    const { t } = useTranslation("settings");
    if (!entry) {
        return <div className="h-full flex items-center justify-center text-xs text-muted">{t("shadowDebug.selectSelfModel")}</div>;
    }
    return (
        <div className="space-y-3">
            <header className="space-y-1">
                <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                        <h2 className="text-sm font-semibold text-ink">{entry.statement}</h2>
                        <div className="text-[10px] font-mono text-muted break-all">{entry.entryKey}</div>
                    </div>
                    <StatusBadge status={entry.status} />
                </div>
                <div className="text-[10px] text-muted">
                    {entry.kind} · revision {entry.revision} · {formatPercent(entry.confidence)} · {entry.reasoningStatus}
                </div>
            </header>

            <div className="border-y border-border py-2 text-[11px] text-muted">
                {t("shadowDebug.selfModelBoundary")}
            </div>

            {entry.scope && (
                <section>
                    <div className="text-[10px] text-muted">{t("shadowDebug.selfModelScope")}</div>
                    <div className="mt-1 font-mono text-[11px] text-ink break-all">{entry.scope}</div>
                </section>
            )}

            <section className="border-t border-border pt-3">
                <h3 className="text-[11px] font-semibold text-muted mb-2">{t("shadowDebug.selfModelEvidence")}</h3>
                <div className="divide-y divide-border">
                    {entry.evidence.map((evidence, index) => (
                        <div key={`${evidence.sourceKind}:${evidence.sourceId}:${index}`} className="py-2 first:pt-0">
                            <div className="flex items-center justify-between gap-3">
                                <span className="font-mono text-[10px] text-muted break-all">{evidence.sourceKind}:{evidence.sourceId}</span>
                                <span className="text-[10px] text-muted">{formatPercent(evidence.confidence)}</span>
                            </div>
                            {evidence.statement && <p className="mt-1 text-[11px] text-ink">{evidence.statement}</p>}
                            <div className="mt-1 text-[10px] text-muted">{formatDateTime(evidence.occurredAt)}</div>
                        </div>
                    ))}
                </div>
            </section>

            {entry.contradictions.length > 0 && (
                <CodeBlock title={t("shadowDebug.selfModelContradictions")} value={prettyJSON(entry.contradictions)} />
            )}
            <CodeBlock title={t("shadowDebug.selfModelRaw")} value={prettyJSON(entry)} />
        </div>
    );
}

function AttemptDetail({ attempt }: { attempt: ShadowCorrelationAttempt | null }) {
    const { t } = useTranslation("settings");
    if (!attempt) {
        return <div className="h-full flex items-center justify-center text-xs text-muted">{t("shadowDebug.selectAttempt")}</div>;
    }
    return (
        <div className="space-y-3">
            <header className="space-y-1">
                <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                        <h2 className="text-sm font-semibold text-ink break-all">{attempt.sourceKind}:{attempt.sourceId}</h2>
                        <div className="text-[10px] font-mono text-muted break-all">{attempt.id}</div>
                    </div>
                    <StatusBadge status={attempt.status} />
                </div>
                <div className="text-[10px] text-muted">
                    {formatDateTime(attempt.observedAt)} · {attempt.evaluatorModel || "-"} · {attempt.comparatorVersion}
                </div>
            </header>

            <div className="grid grid-cols-4 border-y border-border py-2">
                {[
                    [t("shadowDebug.candidates"), attempt.candidateCount],
                    [t("shadowDebug.selected"), attempt.selectedCount],
                    [t("shadowDebug.reported"), attempt.reportedMatches],
                    [t("shadowDebug.persisted"), attempt.persistedRunIds?.length ?? 0],
                ].map(([label, value]) => (
                    <div key={String(label)} className="border-l border-border first:border-l-0 px-2 first:pl-0">
                        <div className="text-[10px] text-muted">{label}</div>
                        <div className="text-sm font-semibold text-ink">{value}</div>
                    </div>
                ))}
            </div>

            {attempt.reason && <div className="text-[11px] text-muted">{diagnosticReasonLabel(attempt.reason)}</div>}
            {attempt.error && (
                <div className="flex gap-2 text-[11px] text-error bg-error-bg/50 p-2">
                    <AlertCircle className="h-3.5 w-3.5 shrink-0 mt-0.5" />
                    <span>{attempt.error}</span>
                </div>
            )}

            {(attempt.candidates?.length ?? 0) > 0 && (
                <section className="border-t border-border pt-3">
                    <h3 className="text-[11px] font-semibold text-muted mb-2">{t("shadowDebug.candidateSelection")}</h3>
                    <div className="overflow-x-auto">
                        <table className="w-full text-[10px]">
                            <thead className="text-muted text-left">
                                <tr>
                                    <th className="font-medium pb-1">run</th>
                                    <th className="font-medium pb-1">relevance</th>
                                    <th className="font-medium pb-1">bytes</th>
                                    <th className="font-medium pb-1">decision</th>
                                </tr>
                            </thead>
                            <tbody>
                                {attempt.candidates?.map((candidate) => (
                                    <tr key={candidate.shadowRunId} className="border-t border-border">
                                        <td className="py-1.5 pr-2">
                                            <div className="font-medium text-ink">{candidate.capabilityKey}</div>
                                            <div className="font-mono text-muted break-all">{candidate.shadowRunId}</div>
                                        </td>
                                        <td className="py-1.5 pr-2 tabular-nums">{candidate.relevance}</td>
                                        <td className="py-1.5 pr-2 tabular-nums">{candidate.payloadBytes}</td>
                                        <td className="py-1.5">
                                            <span className={candidate.selected ? "text-success" : "text-warning"}>
                                                {candidate.reason || (candidate.selected ? "selected" : "excluded")}
                                            </span>
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>
                </section>
            )}

            <CodeBlock title={t("shadowDebug.observedActivity")} value={attempt.observedSummary ?? ""} />
            <CodeBlock title={t("shadowDebug.comparatorPrompt")} value={attempt.comparatorPrompt ?? ""} />
            <CodeBlock title={t("shadowDebug.evaluatorOutput")} value={prettyJSON(attempt.evaluatorRaw ?? "")} />
        </div>
    );
}

export function ShadowAgentDebugSettings() {
    const { t } = useTranslation("settings");
    const [snapshot, setSnapshot] = useState<ShadowAgentDebugSnapshot>(emptySnapshot);
    const [view, setView] = useState<DebugView>("taskEpisodes");
    const [status, setStatus] = useState("all");
    const [query, setQuery] = useState("");
    const [selectedRunID, setSelectedRunID] = useState<string | null>(null);
    const [selectedProfileID, setSelectedProfileID] = useState<string | null>(null);
    const [selectedQualificationID, setSelectedQualificationID] = useState<string | null>(null);
    const [selectedSelfModelID, setSelectedSelfModelID] = useState<string | null>(null);
    const [selectedAttemptID, setSelectedAttemptID] = useState<string | null>(null);
    const [linkedTaskEpisodeID, setLinkedTaskEpisodeID] = useState<
        string | undefined
    >(undefined);
    const [linkedObserverTraceID, setLinkedObserverTraceID] = useState<
        string | undefined
    >(undefined);
    const [compactDetailOpen, setCompactDetailOpen] = useState(false);
    const [runDetail, setRunDetail] = useState<ShadowRunDebugDetail | null>(null);
    const [loading, setLoading] = useState(true);
    const [detailLoading, setDetailLoading] = useState(false);
    const [snapshotReady, setSnapshotReady] = useState(false);
    const [snapshotError, setSnapshotError] = useState("");
    const snapshotRequestRef = useRef<Promise<void> | null>(null);
    const usesSummarySnapshot = shadowViewUsesSummarySnapshot(view);

    const loadSnapshot = useCallback(() => {
        if (snapshotRequestRef.current) {
            return snapshotRequestRef.current;
        }
        const request = (async () => {
            setLoading(true);
            setSnapshotError("");
            try {
                const normalized = normalizeShadowAgentDebugSnapshot(
                    await GetShadowAgentDebugSnapshot(200),
                );
                setSnapshot(normalized);
                setSnapshotReady(true);
                setSelectedRunID((current) => (
                    normalized.runs.some((run) => run.id === current)
                        ? current
                        : normalized.runs[0]?.id ?? null
                ));
                setSelectedProfileID((current) => (
                    normalized.profiles.some((profile) => profile.id === current)
                        ? current
                        : normalized.profiles[0]?.id ?? null
                ));
                setSelectedQualificationID((current) => (
                    normalized.qualifications.some((item) => item.id === current)
                        ? current
                        : normalized.qualifications[0]?.id ?? null
                ));
                setSelectedSelfModelID((current) => (
                    normalized.selfModel.some((item) => item.id === current)
                        ? current
                        : normalized.selfModel[0]?.id ?? null
                ));
                setSelectedAttemptID((current) => (
                    normalized.attempts.some((attempt) => attempt.id === current)
                        ? current
                        : normalized.attempts[0]?.id ?? null
                ));
            } catch (error) {
                const message = String(error);
                setSnapshotError(message);
                toast.error(t("shadowDebug.loadFailed", { error: message }));
            } finally {
                setLoading(false);
            }
        })();
        snapshotRequestRef.current = request;
        void request.finally(() => {
            if (snapshotRequestRef.current === request) {
                snapshotRequestRef.current = null;
            }
        });
        return request;
    }, [t]);

    useEffect(() => {
        if (usesSummarySnapshot) {
            void loadSnapshot();
        }
    }, [loadSnapshot, usesSummarySnapshot]);

    useEffect(() => {
        if (!usesSummarySnapshot) return;
        const refresh = () => {
            void loadSnapshot();
        };
        const refreshRuntime = () => {
            refresh();
            if (view === "runs" && selectedRunID) {
                void GetShadowAgentDebugRun(selectedRunID)
                    .then(setRunDetail)
                    .catch((error) => {
                        toast.error(t("shadowDebug.loadRunFailed", { error: String(error) }));
                    });
            }
        };
        const unsubscribers = [
            EventsOn("shadow_agent:attempt", refresh),
            EventsOn("shadow_agent:profile", refresh),
            EventsOn("shadow_agent:qualification", refresh),
            EventsOn("self_model:updated", refresh),
            EventsOn("shadow_agent:runtime", refreshRuntime),
            EventsOn("activity_pipeline:run", refresh),
        ];
        return () => {
            unsubscribers.forEach((unsubscribe) => unsubscribe());
        };
    }, [loadSnapshot, selectedRunID, t, usesSummarySnapshot, view]);

    useEffect(() => {
        if (view !== "runs" || !selectedRunID) {
            setRunDetail(null);
            setDetailLoading(false);
            return;
        }
        let cancelled = false;
        setDetailLoading(true);
        GetShadowAgentDebugRun(selectedRunID)
            .then((detail) => {
                if (!cancelled) setRunDetail(detail);
            })
            .catch((error) => {
                if (!cancelled) {
                    setRunDetail(null);
                    toast.error(t("shadowDebug.loadRunFailed", { error: String(error) }));
                }
            })
            .finally(() => {
                if (!cancelled) setDetailLoading(false);
            });
        return () => {
            cancelled = true;
        };
    }, [selectedRunID, t, view]);

    const normalizedQuery = query.trim().toLowerCase();
    const runs = useMemo(() => snapshot.runs.filter((run) => {
        if (status !== "all" && run.status !== status) return false;
        if (!normalizedQuery) return true;
        return `${run.capabilityKey} ${run.sourceKind} ${run.sourceId} ${run.predictedAction}`
            .toLowerCase()
            .includes(normalizedQuery);
    }), [normalizedQuery, snapshot.runs, status]);
    const attempts = useMemo(() => snapshot.attempts.filter((attempt) => {
        if (status !== "all" && attempt.status !== status) return false;
        if (!normalizedQuery) return true;
        return `${attempt.sourceKind} ${attempt.sourceId} ${attempt.reason ?? ""}`
            .toLowerCase()
            .includes(normalizedQuery);
    }), [normalizedQuery, snapshot.attempts, status]);
    const profiles = useMemo(() => snapshot.profiles.filter((profile) => {
        if (status !== "all" && profile.readiness.status !== status) return false;
        if (!normalizedQuery) return true;
        return `${profile.capabilityKey} ${profile.policyVersion} ${profile.readiness.reasons.join(" ")}`
            .toLowerCase()
            .includes(normalizedQuery);
    }), [normalizedQuery, snapshot.profiles, status]);
    const qualifications = useMemo(() => snapshot.qualifications.filter((item) => {
        if (status !== "all" && item.status !== status) return false;
        if (!normalizedQuery) return true;
        return `${item.capabilityKey} ${item.riskTier} ${item.reasons.join(" ")}`
            .toLowerCase()
            .includes(normalizedQuery);
    }), [normalizedQuery, snapshot.qualifications, status]);
    const selfModel = useMemo(() => snapshot.selfModel.filter((item) => {
        if (status !== "all" && item.status !== status) return false;
        if (!normalizedQuery) return true;
        return `${item.kind} ${item.scope ?? ""} ${item.statement} ${item.reasoningStatus}`
            .toLowerCase()
            .includes(normalizedQuery);
    }), [normalizedQuery, snapshot.selfModel, status]);
    const selectedProfile = snapshot.profiles.find((profile) => profile.id === selectedProfileID) ?? null;
    const selectedQualification = snapshot.qualifications.find((item) => item.id === selectedQualificationID) ?? null;
    const selectedSelfModel = snapshot.selfModel.find((item) => item.id === selectedSelfModelID) ?? null;
    const selectedAttempt = snapshot.attempts.find((attempt) => attempt.id === selectedAttemptID) ?? null;

    const debugGroups = useMemo<readonly DebugViewGroup<DebugView>[]>(() => [
        {
            id: "shadow-work",
            label: t("shadowDebug.groups.shadowWork"),
            views: [
                { id: "taskEpisodes", label: t("shadowDebug.tabs.taskEpisodes") },
                { id: "observer", label: t("shadowDebug.tabs.observer") },
                { id: "runs", label: t("shadowDebug.tabs.runs") },
                { id: "attempts", label: t("shadowDebug.tabs.attempts") },
            ],
        },
        {
            id: "learning",
            label: t("shadowDebug.groups.learning"),
            views: [
                { id: "workProfile", label: t("shadowDebug.tabs.workProfile") },
                { id: "profiles", label: t("shadowDebug.tabs.profiles") },
                { id: "qualifications", label: t("shadowDebug.tabs.qualifications") },
                { id: "selfModel", label: t("shadowDebug.tabs.selfModel") },
                { id: "selfDream", label: t("shadowDebug.tabs.selfDream") },
            ],
        },
        {
            id: "sources",
            label: t("shadowDebug.groups.sources"),
            views: [
                { id: "background", label: t("shadowDebug.tabs.background") },
                { id: "appAccess", label: t("shadowDebug.tabs.appAccess") },
            ],
        },
    ], [t]);

    const selectView = (nextView: DebugView) => {
        if (nextView === "taskEpisodes") setLinkedTaskEpisodeID(undefined);
        if (nextView === "observer") setLinkedObserverTraceID(undefined);
        setView(nextView);
        setStatus("all");
        setCompactDetailOpen(false);
    };

    const awaitingCount = snapshot.runs.filter((run) => run.status === "awaiting_observation").length;
    const readyCount = snapshot.profiles.filter((profile) => profile.readiness.ready).length;
    const errorCount = snapshot.attempts.filter((attempt) => attempt.status === "error").length;
    const embeddedView = !usesSummarySnapshot;
    const activeViewLabel = debugGroups
        .flatMap((group) => group.views)
        .find((item) => item.id === view)?.label ?? view;

    return (
        <div className="settings-card overflow-hidden">
            <div className="settings-card--padded border-b border-border space-y-3">
                <div className="flex min-w-0 items-start justify-between gap-3">
                    <div className="min-w-0 max-w-3xl">
                        <h2 className="text-sm font-semibold text-ink">
                            {t("shadowDebug.question")}
                        </h2>
                        <p className="mt-1 text-xs leading-5 text-muted">
                            {t("shadowDebug.description")}
                        </p>
                    </div>
                    {!embeddedView && (
                        <Button
                            aria-label={t("shadowDebug.refresh")}
                            title={t("shadowDebug.refresh")}
                            variant="outline"
                            size="icon"
                            className="shrink-0"
                            onClick={() => void loadSnapshot()}
                            disabled={loading}
                        >
                            {loading ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
                        </Button>
                    )}
                </div>
                <DebugViewSwitcher
                    ariaLabel={t("shadowDebug.title")}
                    groups={debugGroups}
                    value={view}
                    onValueChange={selectView}
                />

                {!embeddedView && snapshotReady && (
                    <div className="grid grid-cols-2 gap-px border border-border bg-border lg:grid-cols-4 xl:grid-cols-8">
                        {[
                            [t("shadowDebug.stats.runs"), snapshot.runs.length],
                            [t("shadowDebug.stats.profiles"), snapshot.profiles.length],
                            [t("shadowDebug.stats.qualifications"), snapshot.qualifications.length],
                            [t("shadowDebug.stats.selfModel"), snapshot.selfModel.length],
                            [t("shadowDebug.stats.ready"), readyCount],
                            [t("shadowDebug.stats.awaiting"), awaitingCount],
                            [t("shadowDebug.stats.attempts"), snapshot.attempts.length],
                            [t("shadowDebug.stats.errors"), errorCount],
                        ].map(([label, value]) => (
                            <div key={String(label)} className="min-w-0 bg-panel px-2 py-2">
                                <div className="min-h-6 break-words text-[9px] leading-3 text-muted">{label}</div>
                                <div className="text-sm font-semibold tabular-nums text-ink">{value}</div>
                            </div>
                        ))}
                    </div>
                )}
                {!embeddedView && !snapshotReady && (
                    <div
                        role="status"
                        className="flex items-center gap-2 border-y border-border py-3 text-[10px] text-muted"
                    >
                        {loading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                        <span>
                            {loading
                                ? "Loading Shadow learning summary..."
                                : "Shadow learning summary is unavailable."}
                        </span>
                    </div>
                )}

                {!embeddedView && (
                    <div className="grid grid-cols-1 gap-2 lg:grid-cols-[minmax(0,1fr)_170px]">
                        <div className="relative min-w-0 flex-1">
                            <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted" />
                            <input
                                value={query}
                                onChange={(event) => setQuery(event.target.value)}
                                disabled={!snapshotReady}
                                className="h-8 w-full border border-border bg-panel pl-7 pr-2 text-xs text-ink outline-none focus:border-ink/40"
                                placeholder={t("shadowDebug.search")}
                            />
                        </div>
                        <Select
                            value={status}
                            onValueChange={setStatus}
                            disabled={!snapshotReady}
                        >
                            <SelectTrigger className="h-8 w-full text-xs">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">{t("shadowDebug.status.all")}</SelectItem>
                                {(view === "runs"
                                    ? ["awaiting_observation", "observed", "evaluated", "expired"]
                                    : view === "profiles"
                                        ? ["collecting", "action_unreliable", "safety_unknown", "safety_concern", "ready"]
                                        : view === "qualifications"
                                            ? ["collecting", "blocked", "qualified", "proposed", "already_proposed"]
                                            : view === "selfModel"
                                                ? ["provisional", "active", "retracted"]
                                                : ["completed", "no_candidates", "error"]
                                ).map((item) => (
                                    <SelectItem key={item} value={item}>
                                        {statusLabel(item)}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                )}
                {!embeddedView && snapshotError && (
                    <div
                        role="alert"
                        className="flex gap-2 border-l-2 border-error pl-3 text-[10px] text-error"
                    >
                        <AlertCircle className="h-3.5 w-3.5 shrink-0" />
                        <span>{snapshotError}</span>
                    </div>
                )}
            </div>

            {view === "taskEpisodes" ? (
                <TaskShadowDebugSettings
                    initialEpisodeID={linkedTaskEpisodeID}
                    onOpenBackgroundLearning={() => {
                        setView("background");
                        setCompactDetailOpen(false);
                    }}
                    onOpenObserverTrace={(traceID) => {
                        setLinkedObserverTraceID(traceID);
                        setView("observer");
                        setCompactDetailOpen(false);
                    }}
                />
            ) : view === "observer" ? (
                <TaskShadowObserverDebugSettings
                    initialTraceID={linkedObserverTraceID}
                />
            ) : view === "workProfile" ? (
                <WorkProfileDebugSettings
                    onOpenTaskEpisode={(taskEpisodeID) => {
                        setLinkedTaskEpisodeID(taskEpisodeID);
                        setView("taskEpisodes");
                        setCompactDetailOpen(false);
                    }}
                    onOpenShadowRun={(shadowRunID) => {
                        setSelectedRunID(shadowRunID);
                        setView("runs");
                        setCompactDetailOpen(true);
                    }}
                />
            ) : view === "selfDream" ? (
                <SelfDreamDebugSettings />
			) : view === "background" ? (
				<BackgroundUnderstandingDebugSettings />
			) : view === "appAccess" ? (
				<ComputerUseAppAccessDebugSettings />
            ) : (
            <DebugMasterDetail
                compactDetailOpen={compactDetailOpen}
                detailKey={
                    view === "runs"
                        ? selectedRunID
                        : view === "profiles"
                            ? selectedProfileID
                            : view === "qualifications"
                                ? selectedQualificationID
                                : view === "selfModel"
                                    ? selectedSelfModelID
                                    : selectedAttemptID
                }
                detailLabel={`${activeViewLabel} details`}
                listLabel={`${activeViewLabel} list`}
                onBack={() => setCompactDetailOpen(false)}
                list={
                <>
                    {loading && !snapshotReady ? (
                        <div className="flex justify-center p-6 text-muted">
                            <Loader2 className="h-5 w-5 animate-spin" />
                        </div>
                    ) : !snapshotReady ? (
                        <div className="p-4 text-xs text-muted">
                            Shadow learning summary is unavailable. Refresh after the diagnostic service recovers.
                        </div>
                    ) : view === "runs" ? (
                        runs.length === 0 ? (
                            <div className="p-4 text-xs text-muted">{t("shadowDebug.emptyRuns")}</div>
                        ) : runs.map((run: ShadowRunDebugSummary) => (
                            <button
                                key={run.id}
                                type="button"
                                aria-current={selectedRunID === run.id ? "true" : undefined}
                                className={`w-full text-left px-3 py-2.5 border-b border-border hover:bg-ink/[0.03] ${selectedRunID === run.id ? "bg-ink/[0.05]" : ""}`}
                                onClick={() => {
                                    setSelectedRunID(run.id);
                                    setCompactDetailOpen(true);
                                }}
                            >
                                <div className="flex items-center justify-between gap-2">
                                    <span className="text-xs font-medium text-ink truncate" title={run.capabilityKey}>{capabilityLabel(run.capabilityKey)}</span>
                                    <ChevronRight className="h-3.5 w-3.5 text-muted shrink-0" />
                                </div>
                                <p className="text-[11px] text-muted line-clamp-2 mt-1" title={run.predictedAction}>
                                    {predictedActionLabel(run.predictedAction)}
                                </p>
                                <div className="flex items-center justify-between gap-2 mt-1.5">
                                    <StatusBadge status={run.status} />
                                    <span
                                        className="text-[10px] text-muted tabular-nums"
                                        title={t("shadowDebug.runMeasurementTechnical", {
                                            observations: run.observationCount,
                                            comparisons: run.evaluationCount,
                                            confidence: formatPercent(run.actionConfidence),
                                        })}
                                    >
                                        {t("shadowDebug.runMeasurement", {
                                            observations: run.observationCount,
                                            comparisons: run.evaluationCount,
                                        })}
                                    </span>
                                </div>
                                <div className="mt-1 text-[10px] text-muted tabular-nums">
                                    {t("shadowDebug.runConfidence", {
                                        confidence: formatPercent(run.actionConfidence),
                                    })}
                                </div>
                            </button>
                        ))
                    ) : view === "profiles" ? (
                        profiles.length === 0 ? (
                            <div className="p-4 text-xs text-muted">{t("shadowDebug.emptyProfiles")}</div>
                        ) : profiles.map((profile) => (
                            <button
                                key={profile.id}
                                type="button"
                                aria-current={selectedProfileID === profile.id ? "true" : undefined}
                                className={`w-full text-left px-3 py-2.5 border-b border-border hover:bg-ink/[0.03] ${selectedProfileID === profile.id ? "bg-ink/[0.05]" : ""}`}
                                onClick={() => {
                                    setSelectedProfileID(profile.id);
                                    setCompactDetailOpen(true);
                                }}
                            >
                                <div className="flex items-center justify-between gap-2">
                                    <span className="text-xs font-medium text-ink truncate" title={profile.capabilityKey}>{capabilityLabel(profile.capabilityKey)}</span>
                                    <ChevronRight className="h-3.5 w-3.5 text-muted shrink-0" />
                                </div>
                                <div className="flex items-center justify-between gap-2 mt-1.5">
                                    <StatusBadge status={profile.readiness.status} />
                                    <span className="text-[10px] text-muted tabular-nums">
                                        {profile.qualifiedEvaluationCount} of {profile.evaluationCount} comparisons usable · {formatPercent(profile.action.reliability)} action match
                                    </span>
                                </div>
                                <div className="text-[10px] text-muted mt-1">
                                    {profile.predictionSourceCount} predicted {profile.predictionSourceCount === 1 ? "result" : "results"} · {profile.observationSourceCount} observed {profile.observationSourceCount === 1 ? "result" : "results"} · {profile.contextCount} {profile.contextCount === 1 ? "context" : "contexts"}
                                </div>
                            </button>
                        ))
                    ) : view === "qualifications" ? (
                        qualifications.length === 0 ? (
                            <div className="p-4 text-xs text-muted">{t("shadowDebug.emptyQualifications")}</div>
                        ) : qualifications.map((qualification) => (
                            <button
                                key={qualification.id}
                                type="button"
                                aria-current={selectedQualificationID === qualification.id ? "true" : undefined}
                                className={`w-full text-left px-3 py-2.5 border-b border-border hover:bg-ink/[0.03] ${selectedQualificationID === qualification.id ? "bg-ink/[0.05]" : ""}`}
                                onClick={() => {
                                    setSelectedQualificationID(qualification.id);
                                    setCompactDetailOpen(true);
                                }}
                            >
                                <div className="flex items-center justify-between gap-2">
                                    <span className="text-xs font-medium text-ink truncate" title={qualification.capabilityKey}>{capabilityLabel(qualification.capabilityKey)}</span>
                                    <StatusBadge status={qualification.status} />
                                </div>
                                <div className="mt-1 text-[10px] text-muted">
                                    {riskTierLabel(qualification.riskTier)} · {formatPercent(qualification.actual.actionReliability)} action match
                                </div>
                                <div className="mt-1 text-[10px] text-muted tabular-nums">
                                    {qualification.actual.qualifiedEvaluations} of {qualification.thresholds.minQualifiedEvaluations} required comparisons · {qualification.actual.completedRuntimeAttempts} of {qualification.thresholds.minCompletedRuntimeAttempts} required practice runs
                                </div>
                            </button>
                        ))
                    ) : view === "selfModel" ? (
                        selfModel.length === 0 ? (
                            <div className="p-4 text-xs text-muted">{t("shadowDebug.emptySelfModel")}</div>
                        ) : selfModel.map((entry) => (
                            <button
                                key={entry.id}
                                type="button"
                                aria-current={selectedSelfModelID === entry.id ? "true" : undefined}
                                className={`w-full text-left px-3 py-2.5 border-b border-border hover:bg-ink/[0.03] ${selectedSelfModelID === entry.id ? "bg-ink/[0.05]" : ""}`}
                                onClick={() => {
                                    setSelectedSelfModelID(entry.id);
                                    setCompactDetailOpen(true);
                                }}
                            >
                                <div className="flex items-center justify-between gap-2">
                                    <span className="text-xs font-medium text-ink truncate" title={entry.kind}>{statusLabel(entry.kind)}</span>
                                    <StatusBadge status={entry.status} />
                                </div>
                                <p className="text-[11px] text-muted line-clamp-2 mt-1">{entry.statement}</p>
                                <div className="mt-1 text-[10px] text-muted">
                                    {statusLabel(entry.reasoningStatus)} · {formatPercent(entry.confidence)} confidence · {entry.evidence.length} evidence {entry.evidence.length === 1 ? "source" : "sources"}
                                </div>
                            </button>
                        ))
                    ) : attempts.length === 0 ? (
                        <div className="p-4 text-xs text-muted">{t("shadowDebug.emptyAttempts")}</div>
                    ) : attempts.map((attempt) => (
                        <button
                            key={attempt.id}
                            type="button"
                            aria-current={selectedAttemptID === attempt.id ? "true" : undefined}
                            className={`w-full text-left px-3 py-2.5 border-b border-border hover:bg-ink/[0.03] ${selectedAttemptID === attempt.id ? "bg-ink/[0.05]" : ""}`}
                            onClick={() => {
                                setSelectedAttemptID(attempt.id);
                                setCompactDetailOpen(true);
                            }}
                        >
                            <div className="flex items-center justify-between gap-2">
                                <span
                                    className="text-xs font-medium text-ink line-clamp-2"
                                    title={`${attempt.sourceKind}:${attempt.sourceId}`}
                                >
                                    {attempt.observedSummary || "Observed user result"}
                                </span>
                                <StatusBadge status={attempt.status} />
                            </div>
                            <p className="text-[11px] text-muted truncate mt-1">{diagnosticReasonLabel(attempt.reason)}</p>
                            <div className="text-[10px] text-muted mt-1">
                                {attempt.selectedCount} of {attempt.candidateCount} possible matches checked · {attempt.reportedMatches} result matches
                            </div>
                        </button>
                    ))}
                </>
                }
                detail={
                <>
                    {view === "runs"
                        ? <RunDetail detail={runDetail} loading={detailLoading} />
                        : view === "profiles"
                            ? <ProfileDetail profile={selectedProfile} />
                            : view === "qualifications"
                                ? <QualificationDetail qualification={selectedQualification} />
                                : view === "selfModel"
                                ? <SelfModelDetail entry={selectedSelfModel} />
                                : <AttemptDetail attempt={selectedAttempt} />}
                </>
                }
            />
            )}
        </div>
    );
}
