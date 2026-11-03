import {
    useCallback,
    useEffect,
    useMemo,
    useRef,
    useState,
    type ReactNode,
} from "react";
import {
    AlertCircle,
    BrainCircuit,
    CheckCircle2,
    ChevronDown,
    ChevronRight,
    Copy,
    FileSearch,
    Loader2,
    Play,
    RefreshCw,
    Search,
    XCircle,
} from "lucide-react";
import { toast } from "sonner";
import { capabilityLabel } from "@/lib/capability-label";
import { Button } from "@/components/ui/button";
import {
    GetTaskShadowDebugDetail,
    GetTaskShadowDebugSnapshot,
    RunTaskShadowQualityCampaign,
    type ShadowTaskPairReadiness,
    type TaskAgentAttempt,
    type TaskShadowDebugDetail,
    type TaskShadowDebugEpisodeSummary,
    type TaskShadowDebugResultDestination,
    type TaskShadowDebugSnapshot,
    type TaskShadowContinuationItem,
    type TaskShadowContinuationSnapshot,
    type TaskShadowReasoningDebug,
    type TaskShadowReasoningHypothesisDebug,
    type TaskShadowCapabilityReleaseSnapshot,
    type TaskShadowQualityCampaign,
} from "@/lib/wails-shim-app";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { DebugMasterDetail } from "./debug-master-detail";

const emptyTaskSnapshot: TaskShadowDebugSnapshot = {
    generatedAt: 0,
    limit: 200,
    enabledWorkflows: [],
    episodes: [],
};

const taskShadowDebugRequestTimeoutMs = 15_000;

function normalizeTaskShadowDebugSnapshot(
    value: unknown,
): TaskShadowDebugSnapshot {
    if (!value || typeof value !== "object") {
        throw new Error("Detected-task diagnostics returned no data.");
    }
    const candidate = value as Partial<TaskShadowDebugSnapshot>;
    if (
        typeof candidate.generatedAt !== "number" ||
        typeof candidate.limit !== "number" ||
        !Array.isArray(candidate.enabledWorkflows) ||
        !candidate.enabledWorkflows.every(
            (workflow) => typeof workflow === "string",
        ) ||
        !Array.isArray(candidate.episodes)
    ) {
        throw new Error("Detected-task diagnostics returned incomplete data.");
    }
    return {
        ...candidate,
        enabledWorkflows: [...candidate.enabledWorkflows],
        episodes: [...candidate.episodes],
    } as TaskShadowDebugSnapshot;
}

async function taskShadowDebugWithTimeout<T>(promise: Promise<T>): Promise<T> {
    let timeoutID: ReturnType<typeof setTimeout> | undefined;
    try {
        return await Promise.race([
            promise,
            new Promise<never>((_, reject) => {
                timeoutID = setTimeout(() => {
                    reject(new Error(
                        "Detected-task diagnostics did not respond within 15 seconds.",
                    ));
                }, taskShadowDebugRequestTimeoutMs);
            }),
        ]);
    } finally {
        if (timeoutID !== undefined) clearTimeout(timeoutID);
    }
}

function taskDateTime(value?: number): string {
    return value ? new Date(value).toLocaleString() : "-";
}

function taskPercent(value?: number): string {
    return typeof value === "number" ? `${Math.round(value * 100)}%` : "-";
}

function taskDuration(value?: number): string {
    if (!value || value <= 0) return "-";
    const minutes = Math.round(value / 60_000);
    if (minutes < 120) return `${minutes}m`;
    const hours = Math.round(minutes / 60);
    if (hours < 48) return `${hours}h`;
    return `${Math.round(hours / 24)}d`;
}

function taskPairGenerationLabel(
    readiness: ShadowTaskPairReadiness,
): string {
    if (readiness.currentGeneration === undefined) return "unknown";
    return readiness.currentGeneration ? "current" : "historical";
}

function taskPrettyJSON(value: unknown): string {
    if (typeof value === "string") {
        const trimmed = value.trim();
        if (!trimmed) return "";
        try {
            return JSON.stringify(JSON.parse(trimmed), null, 2);
        } catch {
            return value;
        }
    }
    return JSON.stringify(value, null, 2);
}

function taskStatusLabel(status: string): string {
    const labels: Record<string, string> = {
        candidate: "Possible task",
        active: "In progress",
        paused: "Paused",
        completed: "Completed",
        abandoned: "Stopped",
        merged: "Combined",
        split: "Split",
        expired: "No result found in time",
        mirroring: "Isoform is trying this task",
        pair_ready: "Both results are ready",
        evaluated: "Results compared",
        outside_production_rollout: "Not available with the current method",
        awaiting_observation: "Waiting for your result",
        awaiting_user_result: "Waiting for your result",
        awaiting_task_terminal: "Waiting for task to finish",
        observed: "Your result found",
        evaluation_pending: "Both results are ready",
        execution_pending: "Waiting for Isoform's attempt",
        learning_pending: "Checking what can be learned",
        learning_materialization_pending: "Updating About you",
        shadow_failed: "Private practice failed",
        first_run_excluded: "Guided task excluded",
        current_valid: "Current method",
        origin_verified: "Task start verified",
        qualified: "Ready to suggest",
        unqualified: "Needs more examples",
        provisional: "Draft",
        ready: "Ready",
        succeeded: "Succeeded",
        complete: "Complete",
        closed: "Closed",
        suspended: "Paused",
        rejected: "Rejected",
        waiting_for_natural_task: "Waiting for a supported task",
        waiting_for_current_shadow_generation: "Waiting for a current attempt",
        observer_source_pending: "Waiting to verify the task source",
        collecting_natural_outcomes: "Waiting for more real results",
        ready_to_process: "Ready to compare results",
        diagnostic_error: "Check could not be completed",
        source_not_covered: "Task source not verified",
        awaiting_task_episode: "Waiting for a task",
        task_episode_pending: "Waiting for task to finish",
        awaiting_current_run: "Waiting for a current attempt",
        scan_truncated: "Not all source records checked",
        origin_missing: "Task start not verified",
        legacy_origin: "Only older source evidence found",
        duplicate_origin: "Multiple task starts found",
        repeated_task_start: "Not used for learning",
        source_pruned_or_cleared: "Activity source no longer available",
        trace_corrupt: "Task-detection record could not be verified",
        boundary_mismatch: "Task boundaries do not match",
        terminal_incomplete: "Task-detection record incomplete",
        production_onset_missing: "Live task start missing",
        historical_generation: "Older method",
        historical_prompt_generation: "Older method",
        evidence_exhausted: "Waiting for new tasks",
        desktop_locked: "Waiting for desktop unlock",
        observer_paused: "Observation is paused",
        frozen_context_insufficient: "Waiting for a clearer task",
        user_result_pending: "Waiting for your result",
        result_destination_pending: "Result source not verified",
        shadow_result_pending: "Waiting for Isoform's result",
        learning_materialization_invalid: "Learning record needs inspection",
        episode_binding_mismatch: "Task link does not match",
        context_binding_mismatch: "Context link does not match",
        workflow_binding_mismatch: "Work type does not match",
        run_binding_mismatch: "Shadow result link does not match",
        covered_source: "Activity source verified",
    };
    if (labels[status]) return labels[status];
    const label = status.replaceAll("_", " ").trim();
    return label ? label.charAt(0).toUpperCase() + label.slice(1) : "Unknown";
}

function taskWorkflowLabel(capabilityKey: string): string {
    const labels: Record<string, string> = {
        "compose.text": "Writing",
        "analyze.text": "Reviewing text",
    };
    return labels[capabilityKey] ?? capabilityLabel(capabilityKey);
}

function taskReleaseGateNextStep(
    gate: NonNullable<TaskShadowDebugSnapshot["releaseGate"]>,
): string {
    const nextSteps: Record<string, string> = {
        complete:
            "This production comparison is complete. Isoform did not gain permission.",
        observer_paused:
            "Turn on observation before your next writing or text-review task.",
        desktop_locked:
            "Unlock the desktop and keep working normally. Shadow learning will wait; Background learning may continue separately when its own settings allow.",
        frozen_context_insufficient:
            "Keep working normally. This task remains observation-only, and the next task may provide enough pre-start context.",
        waiting_for_current_shadow_generation:
            "Keep working normally. The next supported task can create a current private practice run.",
        task_episode_pending:
            "Finish the task in the app where it began so Isoform can observe the result.",
        shadow_attempt_pending:
            "The private practice result is missing or incomplete. Inspect its technical record before relying on it.",
        observer_source_pending:
            "Keep working normally. Isoform still needs a task with a verifiable activity source.",
        observer_origin_pending:
            "Isoform found the activity but could not verify where the task began.",
        user_result_pending:
            "Finish the task in the app where it began so Isoform can capture your result.",
        result_destination_pending:
            "Isoform found a result but could not verify where it was produced.",
        shadow_result_pending:
            "Isoform's private practice did not produce a complete result to compare.",
        evaluation_pending:
            "Both results need a complete, deterministic comparison before Isoform can learn from them.",
        learning_pending:
            "The results were compared. Isoform is checking whether the evidence supports a learning update.",
        learning_materialization_pending:
            "A supported learning update is waiting to be saved in About you.",
        learning_materialization_invalid:
            "The saved learning records conflict. Inspect the exact records before using them.",
    };
    return nextSteps[gate.stage] ?? gate.requiredAction;
}

function taskQualityQuestion(capabilityKey: string): string {
    switch (capabilityKey) {
        case "compose.text":
            return "Can Isoform reliably match your writing?";
        case "analyze.text":
            return "Can Isoform reliably match how you review text?";
        default:
            return "Can Isoform reliably match this task?";
    }
}

function taskQualityNextStep(campaign: TaskShadowQualityCampaign): string {
    switch (campaign.stage) {
        case "qualified":
            return "The current method meets the saved reliability threshold. Permission remains unchanged.";
        case "rejected":
            return "The current method does not meet the saved reliability threshold. Permission remains unchanged.";
        case "ready_to_process":
            return "At least one independently observed result is ready for comparison.";
        case "collecting_natural_outcomes":
            return "Isoform needs more ordinary tasks with independently observed results. Replays do not count.";
        case "evidence_exhausted":
            return "Keep working normally. Isoform will wait for the next supported task instead of reusing old evidence.";
        case "waiting_for_natural_task":
            return "Keep working normally. Isoform is waiting for a supported task it can safely practice.";
        default:
            return campaign.requiredAction;
    }
}

function taskContinuationReason(item: TaskShadowContinuationItem): string {
    switch (item.state) {
        case "execution_pending":
            return "Isoform's private practice still needs to finish.";
        case "awaiting_task_terminal":
            return "This task is still in progress.";
        case "awaiting_user_result":
            return "Isoform is waiting to capture the result of your task.";
        case "evaluation_pending":
            return "Your result and Isoform's private result are ready to compare.";
        case "learning_pending":
            return "The result comparison is ready for a learning check.";
        case "learning_materialization_pending":
            return "A supported learning is waiting to appear in About you.";
        case "complete":
            return "Private practice, comparison, and learning checks are complete.";
        case "historical":
            return "This task used an older Shadow method and is kept for history.";
        case "first_run_excluded":
            return "Guided onboarding work is excluded from Shadow learning.";
        case "shadow_failed":
            return "Isoform's private practice did not finish successfully.";
        case "blocked":
            return "This task cannot continue through the safe comparison loop.";
        default:
            return taskStatusLabel(item.state);
    }
}

function TaskTechnicalDetails({
    children,
    className = "",
}: {
    children: ReactNode;
    className?: string;
}) {
    return (
        <details className={`text-[9px] text-muted ${className}`}>
            <summary className="cursor-pointer select-none font-medium text-muted hover:text-ink">
                Technical details
            </summary>
            <div className="mt-1">{children}</div>
        </details>
    );
}

function TaskStatus({ status }: { status: string }) {
    const tone =
        status === "complete" ||
        status === "completed" ||
        status === "evaluated" ||
        status === "pair_ready" ||
        status === "qualified" ||
        status === "closed" ||
        status === "current_valid" ||
        status === "origin_verified"
            ? "bg-success/15 text-success"
            : status === "outside_production_rollout" ||
                status === "expired" ||
                status === "observation_expired" ||
                status === "source_pruned_or_cleared"
              ? "bg-ink/[0.05] text-muted"
            : status === "suspended" ||
                status === "rejected" ||
                status.includes("invalid") ||
                status.includes("failed") ||
                status.includes("error") ||
                status.includes("mismatch") ||
                status.includes("corrupt") ||
                status.includes("duplicate") ||
                status === "repeated_task_start" ||
                status.includes("truncated") ||
                status.includes("not_covered")
              ? "bg-error-bg text-error"
              : "bg-warning-bg text-warning";
    return (
        <span
            title={status}
            className={`inline-flex rounded px-1.5 py-0.5 text-[10px] font-medium ${tone}`}
        >
            {taskStatusLabel(status)}
        </span>
    );
}

function TaskCapabilityRelease({
    release,
}: {
    release: TaskShadowCapabilityReleaseSnapshot;
}) {
    const [historyOpen, setHistoryOpen] = useState(false);
    const latest = release.latestCurrentPromptEvent;
    return (
        <div className="mt-3 border-t border-border pt-3">
            <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                    <div className="flex items-center gap-2">
                        <h4 className="text-[10px] font-semibold text-ink">
                            Reliability decision
                        </h4>
                        <TaskStatus status={release.state} />
                    </div>
                    <p className="mt-1 text-[9px] leading-4 text-muted">
                        {release.active
                            ? "This method has a saved reliability decision."
                            : release.pendingDecision
                              ? `The ${taskStatusLabel(release.pendingDecision)} decision is waiting to be saved.`
                              : "No reliability decision has been saved for this method."}
                    </p>
                    <div className="mt-0.5 text-[9px] text-muted">
                        Permission remains unchanged.
                    </div>
                </div>
                {release.events.length > 0 && (
                    <button
                        type="button"
                        className="inline-flex h-7 w-7 items-center justify-center text-muted hover:text-ink"
                        aria-label={`${historyOpen ? "Hide" : "Show"} reliability history`}
                        title={`${historyOpen ? "Hide" : "Show"} reliability history`}
                        aria-expanded={historyOpen}
                        onClick={() => setHistoryOpen((current) => !current)}
                    >
                        {historyOpen ? (
                            <ChevronDown aria-hidden="true" className="h-3.5 w-3.5" />
                        ) : (
                            <ChevronRight aria-hidden="true" className="h-3.5 w-3.5" />
                        )}
                    </button>
                )}
            </div>
            <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[9px] text-muted">
                <span>{release.currentPromptEventCount} {release.currentPromptEventCount === 1 ? "decision" : "decisions"} for the current method</span>
                <span>{release.historicalEventCount} older {release.historicalEventCount === 1 ? "decision" : "decisions"}</span>
                {latest && (
                    <>
                        <span>{latest.evidence.evaluationIds.length} {latest.evidence.evaluationIds.length === 1 ? "comparison" : "comparisons"} saved</span>
                        <span>{latest.evidence.contextHashes.length} {latest.evidence.contextHashes.length === 1 ? "context" : "contexts"} saved</span>
                    </>
                )}
            </div>
            {historyOpen && (
                <div className="mt-2 divide-y divide-border border-y border-border">
                    {release.events.map((event) => (
                        <div
                            key={event.id}
                            className="grid gap-1 py-2 text-[9px] sm:grid-cols-[minmax(0,1fr)_auto] sm:gap-3"
                        >
                            <div className="min-w-0">
                                <div className="flex items-center gap-2">
                                    <TaskStatus status={event.decision} />
                                    <span className="font-mono text-ink">
                                        {event.promptVersion}
                                    </span>
                                </div>
                                <div className="mt-1 break-all font-mono text-muted">
                                    {event.evidenceHash}
                                </div>
                                <div className="mt-1 text-muted">
                                    {event.evidence.evaluationIds.length} evaluations ·{" "}
                                    {event.evidence.taskEpisodeIds.length} episodes ·{" "}
                                    {event.evidence.contextHashes.length} contexts
                                </div>
                            </div>
                            <time className="text-muted" dateTime={new Date(event.createdAt).toISOString()}>
                                {taskDateTime(event.createdAt)}
                            </time>
                        </div>
                    ))}
                </div>
            )}
        </div>
    );
}

function TaskReleaseGate({
    gate,
    onOpenObserverTrace,
    onOpenBackgroundLearning,
}: {
    gate: NonNullable<TaskShadowDebugSnapshot["releaseGate"]>;
    onOpenObserverTrace?: (traceID: string) => void;
    onOpenBackgroundLearning?: () => void;
}) {
    const historicalRuns = gate.historicalShadowRunIds || [];
    const provenance = gate.observerProvenance;
    return (
        <section className="border-y border-border py-3">
            <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                    <div className="flex items-center gap-2">
                        <h3 className="text-[11px] font-semibold text-ink">
                            Can Isoform observe your next task?
                        </h3>
                        <TaskStatus status={gate.stage} />
                    </div>
                    <p className="mt-1 text-[10px] leading-4 text-muted">
                        {taskReleaseGateNextStep(gate)}
                    </p>
                    {onOpenBackgroundLearning &&
                        (gate.stage === "desktop_locked" ||
                            gate.stage === "frozen_context_insufficient") && (
                            <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                className="mt-2"
                                onClick={onOpenBackgroundLearning}
                            >
                                Review background learning
                            </Button>
                        )}
                </div>
                <div className="min-w-[10rem] text-[9px] leading-4 text-muted">
                    <div>{`Observation: ${gate.monitoring ? "On" : "Paused"}`}</div>
                    <div>{`Task type: ${gate.selectedWorkflow
                            ? taskWorkflowLabel(gate.selectedWorkflow)
                            : "Waiting"}`}</div>
                    <div>{`Past practice runs: ${historicalRuns.length}`}</div>
                    <details className="mt-1">
                        <summary className="cursor-pointer select-none font-medium text-muted hover:text-ink">
                            Technical details
                        </summary>
                        <div className="mt-1 break-all font-mono">
                            {`Stage: ${gate.stage}`}
                        </div>
                        <div className="break-all font-mono">
                            {`Current app: ${gate.foregroundApp || "not detected"}`}
                        </div>
                        <div className="break-words font-mono">
                            {`Backend next step: ${gate.requiredAction}`}
                        </div>
                        <div className="break-all font-mono">
                            {`Prompt: ${gate.selectedShadowPrompt || "none"}`}
                        </div>
                        <div className="break-all font-mono">
                            {`Required prompt: ${gate.requiredShadowPrompt || "unknown"}`}
                        </div>
                        <div className="break-all font-mono">
                            {`Task code: ${gate.selectedWorkflow || "pending"}`}
                        </div>
                    </details>
                </div>
            </div>
            <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-2 lg:grid-cols-3">
                {gate.steps.map((step) => (
                    <div
                        key={step.key}
                        className="flex min-w-0 items-start gap-2"
                    >
                        {step.complete ? (
                            <CheckCircle2
                                aria-hidden="true"
                                className="mt-0.5 h-3.5 w-3.5 shrink-0 text-success"
                            />
                        ) : (
                            <FileSearch
                                aria-hidden="true"
                                className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted"
                            />
                        )}
                        <div className="min-w-0">
                            <div className="text-[9px] leading-4 text-ink">
                                {step.label}
                            </div>
                        </div>
                    </div>
                ))}
            </div>
            {gate.steps.some((step) => Boolean(step.evidenceId)) && (
                <TaskTechnicalDetails className="mt-2">
                    <dl className="grid grid-cols-[minmax(120px,auto)_minmax(0,1fr)] gap-x-3 gap-y-1">
                        {gate.steps.map((step) => (
                            <div key={step.key} className="contents">
                                <dt>{step.label}</dt>
                                <dd className="break-all font-mono text-ink">
                                    {step.evidenceId || "-"}
                                </dd>
                            </div>
                        ))}
                    </dl>
                </TaskTechnicalDetails>
            )}
            {provenance && (
                <div className="mt-3 grid gap-2 border-t border-border pt-3 text-[9px] sm:grid-cols-[minmax(0,1fr)_auto]">
                    <div className="min-w-0">
                        <div className="flex flex-wrap items-center gap-2">
                            <span className="font-medium text-ink">Activity source</span>
                            <TaskStatus status={provenance.state} />
                        </div>
                        <p className="mt-1 leading-4 text-muted">
                            {provenance.sourceComplete
                                ? provenance.originComplete
                                    ? "This task is linked to real observed activity and a verified task start."
                                    : "Isoform found the activity source but could not verify where this task began."
                                : "Isoform could not link this task to a complete observed activity source."}
                        </p>
                        <details className="group mt-1 text-muted">
                            <summary className="cursor-pointer select-none text-[9px] font-medium text-ink">
                                Technical source details
                            </summary>
                            <div className="hidden group-open:block">
                                <p className="mt-1 leading-4">{provenance.reason}</p>
                                <div className="mt-1 grid min-w-0 grid-cols-2 gap-x-4 gap-y-1 sm:grid-cols-4">
                                    <span className="truncate font-mono">
                                        coverage {provenance.coverageState || "pending"}
                                    </span>
                                    <span className="truncate font-mono">
                                        source {provenance.pipelineRunId || "-"}
                                    </span>
                                    <span className="truncate font-mono">
                                        {provenance.admission || "-"} / {provenance.flow || "-"}
                                    </span>
                                    <span className="truncate font-mono">
                                        {provenance.workflow || "workflow pending"}
                                    </span>
                                    <span className="truncate font-mono">
                                        {provenance.workflowDecisionState || "run pending"}
                                    </span>
                                </div>
                            </div>
                        </details>
                    </div>
                    {provenance.traceId && onOpenObserverTrace && (
                        <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            className="self-start"
                            onClick={() => onOpenObserverTrace(provenance.traceId!)}
                        >
                            <FileSearch className="h-3.5 w-3.5" />
                            <span className="ml-1.5">See how this task was detected</span>
                        </Button>
                    )}
                </div>
            )}
            {gate.learningResolution && (
                <div className="mt-3 border-t border-border pt-3 text-[9px]">
                    <div className="flex flex-wrap items-center gap-2">
                        <span className="font-medium text-ink">Supported insights</span>
                        <TaskStatus
                            status={gate.learningResolution.complete ? "complete" : "pending"}
                        />
                        <span className="text-muted">
                            {gate.learningResolution.materializedCount} supported{" "}
                            {gate.learningResolution.materializedCount === 1
                                ? "update"
                                : "updates"}{" "}
                            saved
                        </span>
                    </div>
                    <TaskTechnicalDetails className="mt-1">
                        <div className="break-all font-mono text-ink">
                            run {gate.learningResolution.runId}
                        </div>
                        <div className="mt-1">
                            {gate.learningResolution.terminalCount}/
                            {gate.learningResolution.candidateCount} terminal ·{" "}
                            {gate.learningResolution.skippedCount} skipped ·{" "}
                            {gate.learningResolution.deniedCount} denied ·{" "}
                            {gate.learningResolution.missingCount} missing ·{" "}
                            {gate.learningResolution.errorCount} errors ·{" "}
                            {gate.learningResolution.invalidCount} invalid
                        </div>
                    </TaskTechnicalDetails>
                </div>
            )}
            {gate.reasons.length > 0 && (
                <TaskTechnicalDetails className="mt-2 border-l-2 border-warning pl-3">
                    <div className="break-words font-mono">
                        {gate.reasons.join(" · ")}
                    </div>
                </TaskTechnicalDetails>
            )}
        </section>
    );
}

function TaskContinuation({
    continuation,
}: {
    continuation: TaskShadowContinuationSnapshot;
}) {
    const items = continuation.items ?? [];
    const counts = [
        ["Practice runs pending", continuation.executionPendingCount],
        ["comparisons pending", continuation.evaluationPendingCount],
        ["learning checks pending", continuation.learningPendingCount],
        ["About you updates pending", continuation.materializationPendingCount ?? 0],
        [
            "waiting for your task",
            (continuation.awaitingTerminalCount ?? 0) +
                (continuation.awaitingResultCount ?? 0),
        ],
        ["complete", continuation.completeCount],
        ["blocked", continuation.blockedCount],
        ["older method", continuation.historicalCount],
        ["guided tasks excluded", continuation.firstRunExcludedCount],
    ] as const;
    return (
        <section className="border-b border-border py-3">
            <div className="flex flex-wrap items-start justify-between gap-3">
                <div>
                    <h3 className="text-[11px] font-semibold text-ink">
                        What still needs to happen?
                    </h3>
                    <p className="mt-1 text-[10px] leading-4 text-muted">
                        Tracks each saved task through Isoform's private practice,
                        result comparison, and supported learning update.
                    </p>
                </div>
                <div className="text-right text-[9px] text-muted">
                    <div className="mt-0.5">
                        {continuation.currentRunCount} current practice{" "}
                        {continuation.currentRunCount === 1 ? "run" : "runs"}
                    </div>
                    <TaskTechnicalDetails className="mt-1 text-left">
                        <div className="break-all font-mono">{continuation.version}</div>
                        <div>{continuation.scannedRunCount} records scanned</div>
                    </TaskTechnicalDetails>
                </div>
            </div>
            <div className="mt-3 grid grid-cols-4 gap-x-3 gap-y-2 lg:grid-cols-9">
                {counts.map(([label, value]) => (
                    <div key={label} className="min-w-0">
                        <div className="text-[9px] text-muted">{label}</div>
                        <div className="mt-0.5 font-mono text-[11px] text-ink">
                            {value}
                        </div>
                    </div>
                ))}
            </div>
            {items.length > 0 && (
                <div className="mt-3 divide-y divide-border border-y border-border">
                    {items.map((item) => (
                        <div key={item.shadowRunId} className="py-2 text-[10px]">
                            <div className="flex min-w-0 flex-wrap items-center gap-2">
                                <TaskStatus status={item.state} />
                                <span className="font-medium text-ink">
                                    {taskWorkflowLabel(item.workflow)}
                                </span>
                            </div>
                            <p className="mt-1 leading-4 text-muted">
                                {taskContinuationReason(item)}
                            </p>
                            <TaskTechnicalDetails className="mt-1">
                                <div className="mb-1 break-words font-mono">
                                    {item.reason || "No raw reason recorded."}
                                </div>
                                <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-2 gap-y-0.5">
                                    <dt>workflow</dt>
                                    <dd className="break-all font-mono text-ink">{item.workflow}</dd>
                                    <dt>task status</dt>
                                    <dd className="break-all font-mono text-ink">{item.episodeStatus || "-"}</dd>
                                    <dt>action</dt>
                                    <dd className="break-all font-mono text-ink">{item.action}</dd>
                                    <dt>episode</dt>
                                    <dd className="break-all font-mono text-ink">{item.taskEpisodeId}</dd>
                                    <dt>run</dt>
                                    <dd className="break-all font-mono text-ink">{item.shadowRunId}</dd>
                                    <dt>attempt</dt>
                                    <dd className="break-all font-mono text-ink">
                                        {item.attemptId || "-"} · {item.attemptStatus || "pending"}
                                    </dd>
                                    <dt>pair</dt>
                                    <dd className="break-all font-mono text-ink">{item.pairState || "-"}</dd>
                                    <dt>evaluation</dt>
                                    <dd className="break-all font-mono text-ink">
                                        {(item.currentEvaluationIds ?? []).join(", ") || "-"}
                                    </dd>
                                    <dt>learning</dt>
                                    <dd className="break-all font-mono text-ink">{item.learningRunId || "-"}</dd>
                                    {item.learningMaterialization && (
                                        <>
                                            <dt>resolution</dt>
                                            <dd className="break-all font-mono text-ink">
                                                {item.learningMaterialization.terminalCount}/
                                                {item.learningMaterialization.candidateCount} terminal ·{" "}
                                                {item.learningMaterialization.missingCount} missing ·{" "}
                                                {item.learningMaterialization.errorCount} errors ·{" "}
                                                {item.learningMaterialization.invalidCount} invalid
                                            </dd>
                                        </>
                                    )}
                                </dl>
                            </TaskTechnicalDetails>
                        </div>
                    ))}
                </div>
            )}
        </section>
    );
}

function TaskQualityCampaign({
    campaign,
    processing,
    onProcess,
}: {
    campaign: TaskShadowQualityCampaign;
    processing: boolean;
    onProcess: () => void;
}) {
    const qualification = campaign.qualification;
    const metrics: Array<[string, string]> = [
        [
            "results compared",
            `${qualification.qualifiedEvaluationCount} of ${qualification.requiredEvaluationCount}`,
        ],
        [
            "different work contexts",
            `${qualification.observedContextCount} of ${qualification.requiredContextCount}`,
        ],
        [
            "task understanding",
            taskPercent(qualification.averageTaskReliability),
        ],
        [
            "result match",
            taskPercent(qualification.averageResultQuality),
        ],
        [
            "comparisons usable for learning",
            `${campaign.counts.safeEvaluations} of ${qualification.requiredSafetyCount ?? 0}`,
        ],
    ];
    return (
        <section className="border-b border-border pb-3">
            <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                        <h3 className="text-[11px] font-semibold text-ink">
                            {taskQualityQuestion(campaign.capabilityKey)}
                        </h3>
                        <TaskStatus status={campaign.stage} />
                    </div>
                    <p className="mt-1 max-w-3xl text-[10px] leading-4 text-muted">
                        {taskQualityNextStep(campaign)}
                    </p>
                    <TaskTechnicalDetails className="mt-1">
                        <div className="break-all font-mono">
                            {campaign.capabilityKey} · {campaign.stage} · {campaign.promptVersion}
                        </div>
                        <div className="mt-1 break-words font-mono">
                            {campaign.requiredAction}
                        </div>
                        {campaign.qualification.reasons.length > 0 && (
                            <div className="mt-1 break-words font-mono">
                                {campaign.qualification.reasons.join(" · ")}
                            </div>
                        )}
                    </TaskTechnicalDetails>
                </div>
                <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={onProcess}
                    disabled={
                        processing || campaign.counts.processablePairs === 0
                    }
                    title="Compare only contained results that already have an independently observed user result"
                >
                    {processing ? (
                        <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    ) : (
                        <Play className="h-3.5 w-3.5" />
                    )}
                    <span className="ml-1.5">Compare ready results</span>
                </Button>
            </div>
            <div className="mt-3 grid grid-cols-2 border-y border-border py-2 lg:grid-cols-5">
                {metrics.map(([label, value]) => (
                    <div
                        key={label}
                        className="border-l border-border px-2 first:border-l-0 first:pl-0"
                    >
                        <div className="text-[9px] uppercase text-muted">
                            {label}
                        </div>
                        <div className="text-[12px] font-semibold tabular-nums text-ink">
                            {value}
                        </div>
                    </div>
                ))}
            </div>
            <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[9px] text-muted">
                <span>{campaign.counts.currentPromptRuns} current practice runs</span>
                {campaign.counts.historicalRuns > 0 && (
                    <span>{campaign.counts.historicalRuns} older practice runs not counted</span>
                )}
                <span>
                    {campaign.counts.awaitingNaturalOutcomes} waiting for {campaign.counts.awaitingNaturalOutcomes === 1 ? "your result" : "your results"}
                </span>
                <span>{campaign.counts.pairReady} ready to compare</span>
                <span>
                    {campaign.counts.currentPromptEvaluations} {campaign.counts.currentPromptEvaluations === 1 ? "comparison" : "comparisons"} using the current method
                </span>
                {campaign.counts.historicalEvaluations > 0 && (
                    <span>
                        {campaign.counts.historicalEvaluations} older {campaign.counts.historicalEvaluations === 1 ? "comparison" : "comparisons"} excluded
                    </span>
                )}
                <span>{campaign.counts.shadowFailures} practice runs blocked</span>
                <span>{campaign.counts.safetyConcerns} safety concerns</span>
            </div>
            {campaign.release && (
                <TaskCapabilityRelease release={campaign.release} />
            )}
            {campaign.blockers.length > 0 && (
                <div className="mt-2 divide-y divide-border border-y border-border">
                    {campaign.blockers.slice(0, 3).map((blocker) => (
                        <div
                            key={`${blocker.taskEpisodeId}:${blocker.shadowRunId ?? blocker.state}`}
                            className="flex items-start justify-between gap-3 py-1.5 text-[9px]"
                        >
                            <div className="min-w-0">
                                <div className="font-medium text-ink">
                                    {taskWorkflowLabel(campaign.capabilityKey)}
                                </div>
                                <div className="mt-0.5 text-muted">
                                    {taskStatusLabel(blocker.state)}
                                </div>
                                <TaskTechnicalDetails className="mt-1">
                                    <div className="break-all font-mono">
                                        task {blocker.taskEpisodeId}
                                    </div>
                                    <div className="break-all font-mono">
                                        attempt {blocker.shadowRunId || "-"}
                                    </div>
                                    <div className="mt-1 break-words font-mono">
                                        {blocker.reasons.join(" · ")}
                                    </div>
                                </TaskTechnicalDetails>
                            </div>
                            <TaskStatus status={blocker.state} />
                        </div>
                    ))}
                </div>
            )}
        </section>
    );
}

function TaskLifecycleHealth({
    health,
}: {
    health: NonNullable<TaskShadowDebugSnapshot["lifecycleHealth"]>;
}) {
    const statusCounts = Object.entries(health.statusCounts ?? {}).filter(
        ([, count]) => count > 0,
    );
    const pairCounts = Object.entries(health.pairStateCounts ?? {}).filter(
        ([, count]) => count > 0,
    );
    const repeatedStartCount = health.duplicateOnsetClusterCount ?? 0;
    const repeatedStartRecordCount = health.duplicateOnsetEpisodeCount ?? 0;
    const repeatedStarts = health.duplicateOnsetClusters ?? [];
    return (
        <section className="border-b border-border pb-3">
            <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                    <h3 className="text-[11px] font-semibold text-ink">
                        Did observed tasks finish?
                    </h3>
                    <p className="mt-1 text-[9px] leading-4 text-muted">
                        Shows whether observed tasks finished naturally or reached the time limit set by their latest state change.
                    </p>
                </div>
                {health.latestAttempt && (
                    <div className="text-right text-[9px] text-muted">
                        <div className="flex items-center justify-end gap-2">
                            <TaskStatus status={health.latestAttempt.status} />
                        </div>
                        <time dateTime={new Date(health.latestAttempt.startedAt).toISOString()}>
                            {taskDateTime(health.latestAttempt.startedAt)}
                        </time>
                    </div>
                )}
            </div>
            <div className="mt-3 grid grid-cols-2 border-y border-border py-2 sm:grid-cols-4">
                {[
                    ["Tasks in progress", health.openCount],
                    ["Results overdue", health.overdueOpenCount],
                    ["Your results found", health.materializedResultEpisodeCount],
                    ["No result in time", health.statusCounts?.expired ?? 0],
                ].map(([label, value]) => (
                    <div
                        key={label}
                        className="border-l border-border px-2 first:border-l-0 first:pl-0"
                    >
                        <div className="text-[9px] uppercase text-muted">{label}</div>
                        <div className="text-[12px] font-semibold tabular-nums text-ink">
                            {value}
                        </div>
                    </div>
                ))}
            </div>
            <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-[9px] text-muted">
                <span>{health.episodeCount} total observed tasks</span>
                <span>Repeated task starts: {repeatedStartCount}</span>
            </div>
            {statusCounts.length > 0 && (
                <div className="mt-2 flex flex-wrap gap-1.5" aria-label="Observed task status counts">
                    {statusCounts.map(([state, count]) => (
                        <span key={state} className="inline-flex items-center gap-1 text-[9px] text-muted">
                            <TaskStatus status={state} />
                            <span className="tabular-nums">{count}</span>
                        </span>
                    ))}
                </div>
            )}
            {pairCounts.length > 0 && (
                <div className="mt-2 text-[9px] leading-4 text-muted">
                    Comparisons: {pairCounts.map(([state, count]) => `${taskStatusLabel(state)} ${count}`).join(" · ")}
                </div>
            )}
            {health.overdueOpenCount > 0 && (
                <div className="mt-2 border-l-2 border-warning pl-3 text-[9px] leading-4 text-muted">
                    {health.overdueOpenCount} task{health.overdueOpenCount === 1 ? " Isoform noticed is" : "s Isoform noticed are"} past the result window and will be marked No result found in time after the next eligible activity update.
                </div>
            )}
            {repeatedStartCount > 0 && (
                <div className="mt-2 border-l-2 border-warning pl-3 text-[9px] leading-4 text-muted">
                    {repeatedStartCount} past task start{repeatedStartCount === 1 ? " was" : "s were"} saved more than once across {repeatedStartRecordCount} records. Isoform keeps these records for inspection but does not compare them or learn from them. New detections reuse one task record.
                </div>
            )}
            <TaskTechnicalDetails className="mt-2">
                <div className="break-all font-mono">
                    {health.policyVersion} · {health.activitySourcePolicy} · {health.promptVersion} · {health.shadowPromptVersion}
                </div>
                {health.latestAttempt && (
                    <div className="mt-1 break-all font-mono">
                        model {health.latestAttempt.model} · attempt {health.latestAttempt.id}
                    </div>
                )}
                <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1">
                    <span>possible task window {taskDuration(health.candidateHorizonMs)}</span>
                    <span>in-progress window {taskDuration(health.activeHorizonMs)}</span>
                    <span>paused window {taskDuration(health.pausedHorizonMs)}</span>
                    <span>attached activity extends window: {health.evidenceRenews ? "yes" : "no"}</span>
                </div>
                {repeatedStarts.length > 0 && (
                    <div className="mt-2 space-y-1 border-t border-border pt-2">
                        {repeatedStarts.map((cluster) => (
                            <div
                                key={`${cluster.sourceKind}:${cluster.sourceId}:${cluster.observedAt}:${cluster.evidenceRef}`}
                                className="break-all font-mono"
                            >
                                <div>
                                    source {cluster.sourceKind}:{cluster.sourceId} · ref {cluster.evidenceRef} · observed {cluster.observedAt}
                                </div>
                                <div>task records {cluster.episodeIds.join(", ")}</div>
                            </div>
                        ))}
                        {health.duplicateOnsetClustersTruncated && (
                            <div>
                                Showing {repeatedStarts.length} of {repeatedStartCount} repeated starts.
                            </div>
                        )}
                    </div>
                )}
            </TaskTechnicalDetails>
        </section>
    );
}

function TaskCodeBlock({
    title,
    value,
}: {
    title: string;
    value: string;
}) {
    if (!value) return null;
    const copy = async () => {
        try {
            await navigator.clipboard.writeText(value);
            toast.success(`${title} copied`);
        } catch (error) {
            toast.error(String(error));
        }
    };
    return (
        <section className="border-t border-border pt-3">
            <div className="mb-1.5 flex items-center justify-between gap-3">
                <h4 className="text-[10px] font-semibold uppercase text-muted">
                    {title}
                </h4>
                <button
                    type="button"
                    className="inline-flex h-7 w-7 items-center justify-center text-muted hover:text-ink"
                    aria-label={`Copy ${title}`}
                    title={`Copy ${title}`}
                    onClick={() => void copy()}
                >
                    <Copy aria-hidden="true" className="h-3.5 w-3.5" />
                </button>
            </div>
            <pre className="max-h-[360px] overflow-auto whitespace-pre-wrap break-words bg-ink/[0.04] p-2 font-mono text-[10px] text-ink/80">
                {value}
            </pre>
        </section>
    );
}

function TaskAttemptDetail({ attempt }: { attempt: TaskAgentAttempt }) {
    return (
        <section className="border-l-2 border-border pl-3">
            <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                    <div className="text-[11px] font-semibold text-ink">
                        {attempt.phase} · {attempt.modelName || "-"}
                    </div>
                    <div className="break-all font-mono text-[9px] text-muted">
                        {attempt.id}
                    </div>
                </div>
                <TaskStatus status={attempt.status} />
            </div>
            <div className="mt-1 text-[10px] text-muted">
                {attempt.promptVersion} · {taskDateTime(attempt.startedAt)}
            </div>
            <div className="mt-1 break-all font-mono text-[9px] text-muted">
                prompt {attempt.promptHash} · input {attempt.inputHash}
            </div>
            {attempt.error && (
                <div className="mt-2 flex gap-2 bg-error-bg/50 p-2 text-[10px] text-error">
                    <AlertCircle
                        aria-hidden="true"
                        className="mt-0.5 h-3.5 w-3.5 shrink-0"
                    />
                    <span>{attempt.error}</span>
                </div>
            )}
            <div className="mt-2 space-y-3">
                <TaskCodeBlock title="Exact model prompt" value={attempt.promptText} />
                <TaskCodeBlock
                    title="Normalized model input"
                    value={taskPrettyJSON(attempt.normalizedInputJson)}
                />
                <TaskCodeBlock
                    title="Raw model output"
                    value={attempt.rawOutput ?? ""}
                />
                <TaskCodeBlock
                    title="Parsed model output"
                    value={taskPrettyJSON(attempt.parsedOutputJson ?? "")}
                />
            </div>
        </section>
    );
}

function TaskReasoningRefs({
    label,
    refs,
}: {
    label: string;
    refs?: string[] | null;
}) {
    const values = refs ?? [];
    if (values.length === 0) return null;
    return (
        <div className="mt-1 text-[9px] leading-4 text-muted">
            <span>{label}: </span>
            <span className="break-all font-mono text-ink">{values.join(", ")}</span>
        </div>
    );
}

function TaskReasoningHypothesis({
    hypothesis,
}: {
    hypothesis: TaskShadowReasoningHypothesisDebug;
}) {
    return (
        <div className="py-2">
            <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="flex flex-wrap items-center gap-1.5">
                    <TaskStatus status={hypothesis.basis} />
                    <span className="text-[9px] tabular-nums text-muted">
                        {taskPercent(hypothesis.confidence)} confidence
                    </span>
                </div>
                <TaskStatus
                    status={
                        hypothesis.provenanceResolved
                            ? "provenance_resolved"
                            : "invalid_provenance"
                    }
                />
            </div>
            <p className="mt-1 text-[10px] leading-4 text-ink">
                {hypothesis.statement}
            </p>
            {hypothesis.sourceQuote && (
                <blockquote className="mt-1 border-l-2 border-border pl-2 text-[10px] leading-4 text-muted">
                    {hypothesis.sourceQuote}
                </blockquote>
            )}
            <TaskReasoningRefs label="evidence" refs={hypothesis.evidenceRefs} />
            <TaskReasoningRefs label="profile" refs={hypothesis.profileRefs} />
            <TaskReasoningRefs
                label="contradictions"
                refs={hypothesis.contradictionRefs}
            />
            <div className="mt-1 text-[9px] text-muted">
                quote {hypothesis.quoteValidation} · {hypothesis.validationReason}
            </div>
        </div>
    );
}

function TaskReasoningVersions({ reasoning }: { reasoning: TaskShadowReasoningDebug }) {
    const current = reasoning.versions.current;
    const observed = [
        ["onset", reasoning.versions.onsetPrompts ?? []],
        ["lifecycle", reasoning.versions.lifecyclePrompts ?? []],
        ["shadow", reasoning.versions.shadowPrompts ?? []],
        ["input", reasoning.versions.evaluationInputs ?? []],
        ["output", reasoning.versions.evaluationOutputs ?? []],
        ["comparator", reasoning.versions.comparators ?? []],
        ["evaluator", reasoning.versions.evaluators ?? []],
    ] as const;
    return (
        <details className="border-t border-border pt-2 text-[9px]">
            <summary className="cursor-pointer font-medium text-ink">
                Version lineage
            </summary>
            <dl className="mt-2 grid grid-cols-[100px_minmax(0,1fr)] gap-x-3 gap-y-1">
                {[
                    ["onset", current.onsetPrompt],
                    ["lifecycle", current.lifecyclePrompt],
                    ["shadow", current.shadowPrompt],
                    ["destination", current.resultDestination || "unversioned"],
                    ["input", current.evaluationInput],
                    ["output", current.evaluationOutput],
                    ["comparator", current.comparator],
                    ["evaluator", current.evaluator],
                ].map(([label, version]) => (
                    <div key={label} className="contents">
                        <dt className="text-muted">current {label}</dt>
                        <dd className="break-all font-mono text-ink">{version}</dd>
                    </div>
                ))}
            </dl>
            <div className="mt-2 divide-y divide-border border-y border-border">
                {observed.map(([label, versions]) => (
                    <div
                        key={label}
                        className="grid grid-cols-[100px_minmax(0,1fr)] gap-3 py-1.5"
                    >
                        <span className="text-muted">observed {label}</span>
                        <span className="break-all font-mono text-ink">
                            {versions.length === 0
                                ? "none"
                                : versions
                                      .map(
                                          (item) =>
                                              `${item.version} (${item.count}${
                                                  item.current ? ", current" : ", historical"
                                              })`,
                                      )
                                      .join(", ")}
                        </span>
                    </div>
                ))}
            </div>
        </details>
    );
}

function TaskReasoningParity({
    reasoning,
}: {
    reasoning?: TaskShadowReasoningDebug;
}) {
    if (!reasoning) {
        return (
            <section className="border-t border-border pt-3">
                <div className="flex items-center justify-between gap-3">
                    <div className="flex items-center gap-2">
                        <BrainCircuit className="h-3.5 w-3.5 text-muted" />
                        <h3 className="text-[11px] font-semibold text-ink">
                            Reasoning parity
                        </h3>
                    </div>
                    <TaskStatus status="backend_unavailable" />
                </div>
                <p className="mt-2 text-[10px] text-muted">
                    The running backend does not expose reasoning diagnostics.
                </p>
            </section>
        );
    }

    const onsetHypotheses = reasoning.onsetHypotheses ?? [];
    const predictions = reasoning.predictions ?? [];
    const observedReasoning = reasoning.observed ?? [];
    const comparisons = reasoning.comparisons ?? [];

    return (
        <section className="border-t border-border pt-3">
            <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="flex items-center gap-2">
                    <BrainCircuit className="h-3.5 w-3.5 text-muted" />
                    <div>
                        <h3 className="text-[11px] font-semibold text-ink">
                            Reasoning parity
                        </h3>
                        <p className="mt-0.5 text-[9px] text-muted">
                            Prediction, independent user evidence, and comparison remain separate.
                        </p>
                    </div>
                </div>
                <TaskStatus status={reasoning.coverage} />
            </div>

            <div className="mt-2 grid grid-cols-4 border-y border-border py-2">
                {[
                    ["predicted", reasoning.counts.onsetHypotheses],
                    ["observed", reasoning.counts.observedReasoning],
                    ["known", reasoning.counts.knownComparisons],
                    ["unknown", reasoning.counts.unknownComparisons],
                ].map(([label, value]) => (
                    <div
                        key={label}
                        className="border-l border-border px-2 first:border-l-0 first:pl-0"
                    >
                        <div className="text-[9px] uppercase text-muted">{label}</div>
                        <div className="text-[12px] font-semibold tabular-nums text-ink">
                            {value}
                        </div>
                    </div>
                ))}
            </div>

            <div className="mt-3">
                <h4 className="text-[10px] font-semibold text-ink">
                    Onset hypotheses
                </h4>
                {onsetHypotheses.length === 0 ? (
                    <p className="mt-1 text-[10px] text-muted">
                        No supported explanation was frozen. Why remains unknown.
                    </p>
                ) : (
                    <div className="mt-1 divide-y divide-border border-y border-border">
                        {onsetHypotheses.map((hypothesis, index) => (
                            <TaskReasoningHypothesis
                                key={`${hypothesis.statement}:${index}`}
                                hypothesis={hypothesis}
                            />
                        ))}
                    </div>
                )}
                {predictions.length > 0 && (
                    <div className="mt-2 divide-y divide-border border-y border-border">
                        {predictions.map((prediction) => {
                            const hypotheses = prediction.hypotheses ?? [];
                            return (
                                <div
                                    key={prediction.shadowRunId}
                                    className="py-1.5 text-[9px]"
                                >
                                    <div className="flex items-center justify-between gap-2">
                                        <span className="break-all font-mono text-ink">
                                            {prediction.shadowRunId}
                                        </span>
                                        <TaskStatus
                                            status={
                                                prediction.current
                                                    ? "current"
                                                    : "historical"
                                            }
                                        />
                                    </div>
                                    <div className="mt-0.5 text-muted">
                                        {prediction.promptVersion} · {hypotheses.length}{" "}
                                        frozen{" "}
                                        {hypotheses.length === 1
                                            ? "hypothesis"
                                            : "hypotheses"}
                                    </div>
                                </div>
                            );
                        })}
                    </div>
                )}
            </div>

            <div className="mt-3 border-t border-border pt-3">
                <h4 className="text-[10px] font-semibold text-ink">
                    Observed rationale and corrections
                </h4>
                {observedReasoning.length === 0 ? (
                    <p className="mt-1 text-[10px] text-muted">
                        No independent user-authored reason has been retained.
                    </p>
                ) : (
                    <div className="mt-1 divide-y divide-border border-y border-border">
                        {observedReasoning.map((item) => (
                            <div key={item.evidenceId} className="py-2">
                                <div className="flex flex-wrap items-center justify-between gap-2">
                                    <div className="flex items-center gap-1.5">
                                        <TaskStatus status={item.kind} />
                                        <span className="font-mono text-[9px] text-muted">
                                            {item.version || "unversioned"}
                                        </span>
                                    </div>
                                    <TaskStatus status={item.valid ? "validated" : "invalid"} />
                                </div>
                                {item.sourceQuote && (
                                    <blockquote className="mt-1 border-l-2 border-border pl-2 text-[10px] leading-4 text-ink">
                                        {item.sourceQuote}
                                    </blockquote>
                                )}
                                <TaskReasoningRefs
                                    label="source refs"
                                    refs={item.evidenceRefs}
                                />
                                <div className="mt-1 text-[9px] text-muted">
                                    {item.validationReason} · {taskDateTime(item.observedAt)}
                                </div>
                            </div>
                        ))}
                    </div>
                )}
            </div>

            <div className="mt-3 border-t border-border pt-3">
                <h4 className="text-[10px] font-semibold text-ink">
                    Comparator reasoning
                </h4>
                {comparisons.length === 0 ? (
                    <p className="mt-1 text-[10px] text-muted">
                        No reasoning comparison has been persisted.
                    </p>
                ) : (
                    <div className="mt-1 divide-y divide-border border-y border-border">
                        {comparisons.map((comparison) => {
                            const predictedHypotheses =
                                comparison.predictedHypotheses ?? [];
                            return (
                                <div key={comparison.evaluationId} className="py-2">
                                    <div className="flex flex-wrap items-center justify-between gap-2">
                                        <div className="flex items-center gap-1.5">
                                            <TaskStatus status={comparison.assessment} />
                                            <span className="text-[9px] tabular-nums text-muted">
                                                score{" "}
                                                {comparison.score === null
                                                    ? "unknown"
                                                    : taskPercent(comparison.score)}{" "}
                                                · confidence{" "}
                                                {taskPercent(comparison.confidence)}
                                            </span>
                                        </div>
                                        <TaskStatus
                                            status={
                                                comparison.current
                                                    ? "current"
                                                    : "historical"
                                            }
                                        />
                                    </div>
                                    <p className="mt-1 text-[10px] leading-4 text-ink">
                                        {comparison.reason ||
                                            "No comparator rationale retained."}
                                    </p>
                                    <div className="mt-1 text-[9px] text-muted">
                                        prediction{" "}
                                        {comparison.predictionBound
                                            ? "bound"
                                            : "not bound"}{" "}
                                        · citations{" "}
                                        {comparison.citationsValid
                                            ? "valid"
                                            : "invalid"}
                                    </div>
                                    <div className="mt-0.5 text-[9px] text-muted">
                                        {comparison.predictionBindingReason}
                                    </div>
                                    <div className="mt-0.5 break-all font-mono text-[9px] text-muted">
                                        {comparison.comparatorVersion} ·{" "}
                                        {comparison.evaluatorVersion ||
                                            "unversioned evaluator"}{" "}
                                        · {comparison.evaluatorModel}
                                    </div>
                                    <TaskReasoningRefs
                                        label="support available"
                                        refs={comparison.supportedEvidenceRefs}
                                    />
                                    <TaskReasoningRefs
                                        label="citations"
                                        refs={comparison.evidenceRefs}
                                    />
                                    <details className="mt-2 text-[9px]">
                                        <summary className="cursor-pointer font-medium text-ink">
                                            Comparator input ·{" "}
                                            {predictedHypotheses.length}{" "}
                                            {predictedHypotheses.length === 1
                                                ? "hypothesis"
                                                : "hypotheses"}
                                        </summary>
                                        <div className="mt-1 divide-y divide-border border-y border-border">
                                            {predictedHypotheses.length === 0 ? (
                                                <p className="py-2 text-muted">
                                                    No predicted reasoning entered this comparison.
                                                </p>
                                            ) : (
                                                predictedHypotheses.map(
                                                    (hypothesis, index) => (
                                                        <TaskReasoningHypothesis
                                                            key={`${hypothesis.statement}:${index}`}
                                                            hypothesis={hypothesis}
                                                        />
                                                    ),
                                                )
                                            )}
                                        </div>
                                    </details>
                                </div>
                            );
                        })}
                    </div>
                )}
            </div>

            <div className="mt-3">
                <TaskReasoningVersions reasoning={reasoning} />
            </div>
        </section>
    );
}

function TaskResultDestinationProvenance({
    items,
}: {
    items?: TaskShadowDebugResultDestination[];
}) {
    const destinations = items ?? [];
    return (
        <section className="border-t border-border pt-3">
            <div className="flex items-center gap-2">
                <FileSearch aria-hidden="true" className="h-3.5 w-3.5 text-muted" />
                <div>
                    <h3 className="text-[11px] font-semibold text-ink">
                        Result destination provenance
                    </h3>
                    <p className="mt-0.5 text-[9px] leading-4 text-muted">
                        Committed text stays redacted; source identity and digests remain inspectable.
                    </p>
                </div>
            </div>
            {destinations.length === 0 ? (
                <p className="mt-2 text-[10px] text-muted">
                    No result destination has been retained for this observed task.
                </p>
            ) : (
                <div className="mt-2 divide-y divide-border border-y border-border">
                    {destinations.map((item, index) => (
                        <div
                            key={`${item.destinationSha256 || item.resultSha256 || item.status}:${index}`}
                            className="py-2.5"
                        >
                            <div className="flex flex-wrap items-start justify-between gap-2">
                                <div className="min-w-0">
                                    <div className="break-words text-[10px] font-medium text-ink">
                                        {item.appName || "Unknown destination"}
                                        {item.windowTitle ? ` / ${item.windowTitle}` : ""}
                                    </div>
                                    <div className="mt-0.5 break-all font-mono text-[9px] text-muted">
                                        {item.appBundleId || "bundle unavailable"}
                                    </div>
                                </div>
                                <TaskStatus status={item.status} />
                            </div>
                            <dl className="mt-2 grid grid-cols-[110px_minmax(0,1fr)] gap-x-3 gap-y-1 text-[9px]">
                                <dt className="text-muted">semantic role</dt>
                                <dd className="break-all font-mono text-ink">
                                    {item.semanticRole || "unknown"}
                                </dd>
                                <dt className="text-muted">capture</dt>
                                <dd className="break-all font-mono text-ink">
                                    {item.captureMethod || "unavailable"} · {item.version || "unversioned"}
                                </dd>
                                <dt className="text-muted">workflow</dt>
                                <dd className="break-all font-mono text-ink">
                                    {item.workflow || "-"}
                                </dd>
                                <dt className="text-muted">result digest</dt>
                                <dd className="break-all font-mono text-ink">
                                    {item.resultSha256 || "-"}
                                </dd>
                                <dt className="text-muted">destination digest</dt>
                                <dd className="break-all font-mono text-ink">
                                    {item.destinationSha256 || "-"}
                                </dd>
                                <dt className="text-muted">observed</dt>
                                <dd className="text-ink">
                                    {taskDateTime(item.latestObservedAt)}
                                </dd>
                                <dt className="text-muted">evidence</dt>
                                <dd className="break-all font-mono text-ink">
                                    {(item.evidenceIds ?? []).join(", ") || "-"}
                                </dd>
                            </dl>
                            <p className="mt-2 text-[9px] leading-4 text-muted">
                                {item.validationReason}
                            </p>
                        </div>
                    ))}
                </div>
            )}
        </section>
    );
}

function TaskEpisodeDetail({
    detail,
    loading,
}: {
    detail: TaskShadowDebugDetail | null;
    loading: boolean;
}) {
    if (loading) {
        return (
            <div
                role="status"
                aria-label="Loading task details"
                className="flex h-full items-center justify-center text-muted"
            >
                <Loader2 className="h-5 w-5 animate-spin" />
            </div>
        );
    }
    if (!detail) {
        return (
            <div className="flex h-full items-center justify-center text-xs text-muted">
                Select a task.
            </div>
        );
    }

    const episode = detail.episode.episode;
    const hypothesis = episode.latestHypothesis;
    const lease = episode.observationLease;
    const leaseState = !lease?.open ? "closed" : lease.due ? "due" : "open";
    const manifest = episode.frozenContext.manifest ?? [];
    const manifestEntryCount =
        episode.frozenContext.manifestEntryCount ?? manifest.length;
    const manifestIncludedCount =
        episode.frozenContext.manifestIncludedCount ??
        manifest.filter((item) => item.included).length;
    const manifestExcludedCount =
        episode.frozenContext.manifestExcludedCount ??
        manifest.filter((item) => !item.included).length;
    return (
        <div className="space-y-4">
            <header>
                <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                        <h2 className="text-sm font-semibold text-ink">
                            {hypothesis.taskHypothesis}
                        </h2>
                        <p className="mt-1 text-[11px] leading-4 text-muted">
                            {hypothesis.desiredResultHypothesis}
                        </p>
                        <div className="mt-1 break-all font-mono text-[9px] text-muted">
                            {episode.id}
                        </div>
                    </div>
                    <TaskStatus status={episode.currentStatus} />
                </div>
                <div className="mt-2 grid grid-cols-3 border-y border-border py-2">
                    {[
                        ["task", taskPercent(hypothesis.taskConfidence)],
                        ["boundary", taskPercent(hypothesis.boundaryConfidence)],
                        ["result", taskPercent(hypothesis.desiredResultConfidence)],
                    ].map(([label, value]) => (
                        <div
                            key={label}
                            className="border-l border-border px-2 first:border-l-0 first:pl-0"
                        >
                            <div className="text-[9px] uppercase text-muted">
                                {label}
                            </div>
                            <div className="text-[12px] font-semibold text-ink">
                                {value}
                            </div>
                        </div>
                    ))}
                </div>
            </header>

            <section className="border-t border-border pt-3">
                <div className="flex items-start justify-between gap-3">
                    <div>
                        <h3 className="text-[11px] font-semibold text-ink">
                            Can Isoform try this task?
                        </h3>
                        <p className="mt-1 text-[10px] leading-4 text-muted">
                            {detail.productionRollout.reason}
                        </p>
                    </div>
                    <TaskStatus status={detail.productionRollout.state} />
                </div>
                <dl className="mt-2 grid grid-cols-[110px_minmax(0,1fr)] gap-x-3 gap-y-1 text-[10px]">
                    <dt className="text-muted">derived workflow</dt>
                    <dd className="break-all font-mono text-ink">
                        {detail.productionRollout.workflow || "-"}
                    </dd>
                    <dt className="text-muted">current scope</dt>
                    <dd className="break-all font-mono text-ink">
                        {(detail.productionRollout.enabledWorkflows ?? []).join(", ") || "-"}
                    </dd>
                </dl>
            </section>

            <section className="border-t border-border pt-3">
                <div className="flex items-center gap-2">
                    <FileSearch aria-hidden="true" className="h-3.5 w-3.5 text-muted" />
                    <h3 className="text-[11px] font-semibold text-ink">
                        Onset and frozen context
                    </h3>
                </div>
                <dl className="mt-2 grid grid-cols-[110px_minmax(0,1fr)] gap-x-3 gap-y-1 text-[10px]">
                    <dt className="text-muted">inferred start</dt>
                    <dd className="text-ink">{taskDateTime(episode.inferredStartAt)}</dd>
                    <dt className="text-muted">detected</dt>
                    <dd className="text-ink">{taskDateTime(episode.detectedAt)}</dd>
                    <dt className="text-muted">context cutoff</dt>
                    <dd className="text-ink">{taskDateTime(episode.frozenContext.cutoffAt)}</dd>
                    <dt className="text-muted">context snapshot</dt>
                    <dd className="break-all font-mono text-ink">
                        {episode.frozenContext.id}
                    </dd>
                    <dt className="text-muted">context hash</dt>
                    <dd className="break-all font-mono text-ink">
                        {episode.frozenContext.sha256}
                    </dd>
                    <dt className="text-muted">raw context</dt>
                    <dd className="text-ink">
                        {episode.frozenContext.canonicalJsonRedacted
                            ? "redacted from debugger"
                            : "available"}
                    </dd>
                </dl>
            </section>

            {lease && (
                <section className="border-t border-border pt-3">
                    <div className="flex items-start justify-between gap-3">
                        <div className="min-w-0">
                            <h3 className="text-[11px] font-semibold text-ink">
                                Is this task still current?
                            </h3>
                            <p className="mt-1 text-[10px] leading-4 text-muted">
                                {!lease.open
                                    ? "This task has finished or closed, so it is no longer considered current."
                                    : lease.due
                                      ? "Its observation window has ended. Isoform excludes it from new task reasoning even before the expiry record is saved."
                                      : `Isoform can continue observing this task through ${taskDateTime(lease.deadlineAt)}.`}
                            </p>
                        </div>
                        <TaskStatus status={leaseState} />
                    </div>
                    <dl className="mt-2 grid grid-cols-[110px_minmax(0,1fr)] gap-x-3 gap-y-1 text-[10px]">
                        <dt className="text-muted">latest state change</dt>
                        <dd className="text-ink">{taskDateTime(lease.anchorAt)}</dd>
                        <dt className="text-muted">observation window</dt>
                        <dd className="text-ink">
                            {lease.open ? taskDuration(lease.horizonMs) : "closed"}
                        </dd>
                        <dt className="text-muted">observe through</dt>
                        <dd className="text-ink">
                            {lease.open ? taskDateTime(lease.deadlineAt) : "-"}
                        </dd>
                    </dl>
                    <TaskTechnicalDetails className="mt-2">
                        <div className="break-all font-mono">
                            {lease.policyVersion} · anchor {lease.anchorKind}
                        </div>
                        {lease.anchorTransitionId && (
                            <div className="mt-1 break-all font-mono">
                                transition {lease.anchorTransitionId}
                            </div>
                        )}
                        <div className="mt-1">
                            Attached action, reasoning, contradiction, and result evidence extends this window: {lease.evidenceRenews ? "yes" : "no"}.
                        </div>
                    </TaskTechnicalDetails>
                </section>
            )}

            <TaskReasoningParity reasoning={detail.reasoning} />

            <TaskResultDestinationProvenance
                items={detail.episode.resultDestinations}
            />

            <section className="border-t border-border pt-3">
                <div className="mb-2 flex items-center justify-between gap-3">
                    <h3 className="text-[11px] font-semibold text-ink">
                        Context manifest
                    </h3>
                    <span className="text-[10px] text-muted">
                        {manifestIncludedCount} included · {manifestExcludedCount} excluded
                    </span>
                </div>
                {episode.frozenContext.manifestTruncated && (
                    <p className="mb-2 text-[9px] leading-4 text-muted">
                        Showing {manifest.length} of {manifestEntryCount} entries;
                        included evidence is shown first.
                    </p>
                )}
                <div className="overflow-x-auto border-y border-border">
                    <table className="w-full text-left text-[10px]">
                        <thead className="text-muted">
                            <tr>
                                <th className="py-1.5 pr-2 font-medium">reference</th>
                                <th className="py-1.5 pr-2 font-medium">source</th>
                                <th className="py-1.5 pr-2 font-medium">observed</th>
                                <th className="py-1.5 font-medium">decision</th>
                            </tr>
                        </thead>
                        <tbody>
                            {manifest.map((item) => (
                                <tr key={item.ref} className="border-t border-border">
                                    <td className="py-2 pr-2">
                                        <div className="break-all font-mono text-ink">
                                            {item.ref}
                                        </div>
                                        <div className="text-muted">
                                            {item.section} · {item.evidenceClass}
                                        </div>
                                    </td>
                                    <td className="py-2 pr-2 text-muted">
                                        {item.sourceKind}:{item.sourceId}
                                    </td>
                                    <td className="py-2 pr-2 text-muted">
                                        {taskDateTime(item.observedAt)}
                                    </td>
                                    <td className="py-2">
                                        <div className="flex items-center gap-1.5">
                                            {item.included ? (
                                                <CheckCircle2
                                                    aria-hidden="true"
                                                    className="h-3.5 w-3.5 text-success"
                                                />
                                            ) : (
                                                <XCircle
                                                    aria-hidden="true"
                                                    className="h-3.5 w-3.5 text-warning"
                                                />
                                            )}
                                            <span className="text-ink">
                                                {item.included ? "included" : "excluded"}
                                            </span>
                                        </div>
                                        <div className="mt-0.5 text-muted">
                                            {item.reason ||
                                                item.privacyDecision ||
                                                `${item.includedChars ?? 0} chars`}
                                        </div>
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            </section>

            <section className="border-t border-border pt-3">
                <h3 className="mb-2 text-[11px] font-semibold text-ink">
                    Isoform's private result
                </h3>
                {detail.runs.length === 0 ? (
                    <p className="text-[10px] text-muted">
                        Isoform has not produced a shadow result for this task. The
                        decision above explains whether a result is waiting or
                        was intentionally skipped.
                    </p>
                ) : (
                    <div className="divide-y divide-border border-y border-border">
                        {detail.runs.map((item) => (
                            <div key={item.run.id} className="py-2">
                                <div className="flex items-start justify-between gap-3">
                                    <div className="min-w-0">
                                        <div className="text-[11px] font-medium text-ink">
                                            {item.run.capabilityKey}
                                        </div>
                                        <div className="break-all font-mono text-[9px] text-muted">
                                            {item.run.id}
                                        </div>
                                    </div>
                                    <TaskStatus
                                        status={
                                            item.pairReadiness?.state ||
                                            item.status
                                        }
                                    />
                                </div>
                                <p className="mt-1 text-[10px] text-muted">
                                    {item.run.prediction.predictedAction}
                                </p>
                                {item.pairReadiness && (
                                    <dl className="mt-2 grid grid-cols-[120px_minmax(0,1fr)] gap-x-3 gap-y-1 text-[9px]">
                                        <dt className="text-muted">workflow</dt>
                                        <dd className="break-all font-mono text-ink">
                                            {item.pairReadiness.workflow || "-"}
                                        </dd>
                                        <dt className="text-muted">generation</dt>
                                        <dd className="break-all font-mono text-ink">
                                            {taskPairGenerationLabel(
                                                item.pairReadiness,
                                            )}{" "}
                                            ·{" "}
                                            {item.pairReadiness
                                                .shadowPromptVersion ||
                                                "unversioned"}
                                        </dd>
                                        <dt className="text-muted">user result</dt>
                                        <dd className="text-ink">
                                            {item.pairReadiness.userResultMaterialized
                                                ? item.pairReadiness.userResultTruncated
                                                    ? "materialized · partial"
                                                    : "materialized"
                                                : "not materialized"}
                                        </dd>
                                        <dt className="text-muted">shadow result</dt>
                                        <dd className="text-ink">
                                            {item.pairReadiness.shadowResultMaterialized
                                                ? "materialized"
                                                : "not materialized"}
                                        </dd>
                                        <dt className="text-muted">user digest</dt>
                                        <dd className="break-all font-mono text-ink">
                                            {item.pairReadiness.userResultSha256 || "-"}
                                        </dd>
                                        <dt className="text-muted">destination digest</dt>
                                        <dd className="break-all font-mono text-ink">
                                            {item.pairReadiness
                                                .userResultDestinationSha256 || "-"}
                                        </dd>
                                        <dt className="text-muted">shadow digest</dt>
                                        <dd className="break-all font-mono text-ink">
                                            {item.pairReadiness.shadowResultSha256 || "-"}
                                        </dd>
                                        {item.pairReadiness.reasons.length > 0 && (
                                            <>
                                                <dt className="text-muted">waiting on</dt>
                                                <dd className="text-warning">
                                                    {item.pairReadiness.reasons.join(", ")}
                                                </dd>
                                            </>
                                        )}
                                    </dl>
                                )}
                                <div className="mt-1 text-[9px] text-muted">
                                    {item.executionAttempts.length} execution ·{" "}
                                    {item.observations.length} observation ·{" "}
                                    {item.evaluations.length} evaluation
                                </div>
                            </div>
                        ))}
                    </div>
                )}
            </section>

            <section className="border-t border-border pt-3">
                <h3 className="mb-2 text-[11px] font-semibold text-ink">
                    Exact model attempts
                </h3>
                {detail.taskAgentAttempts.length === 0 ? (
                    <p className="text-[10px] text-muted">No model attempt recorded.</p>
                ) : (
                    <div className="space-y-4">
                        {detail.taskAgentAttempts.map((attempt) => (
                            <TaskAttemptDetail key={attempt.id} attempt={attempt} />
                        ))}
                    </div>
                )}
            </section>

            <TaskCodeBlock
                title="Episode hypotheses, evidence, transitions, and links"
                value={taskPrettyJSON({
                    hypotheses: detail.episode.hypotheses,
                    evidence: detail.episode.evidence,
                    transitions: detail.episode.transitions,
                    links: detail.episode.links,
                })}
            />
            <TaskCodeBlock
                title="Learning records"
                value={taskPrettyJSON(detail.learning)}
            />
            <TaskCodeBlock
                title="Correlation IDs"
                value={taskPrettyJSON(detail.correlations)}
            />
            <TaskCodeBlock
                title="Exclusions"
                value={taskPrettyJSON(detail.exclusions)}
            />
        </div>
    );
}

export function TaskShadowDebugSettings({
    initialEpisodeID,
    onOpenObserverTrace,
    onOpenBackgroundLearning,
}: {
    initialEpisodeID?: string;
    onOpenObserverTrace?: (traceID: string) => void;
    onOpenBackgroundLearning?: () => void;
}) {
    const [snapshot, setSnapshot] =
        useState<TaskShadowDebugSnapshot>(emptyTaskSnapshot);
    const [selectedEpisodeID, setSelectedEpisodeID] = useState<string | null>(
        null,
    );
    const [compactDetailOpen, setCompactDetailOpen] = useState(
        Boolean(initialEpisodeID),
    );
    const [detail, setDetail] = useState<TaskShadowDebugDetail | null>(null);
    const [loading, setLoading] = useState(true);
    const [detailLoading, setDetailLoading] = useState(false);
    const [campaignProcessing, setCampaignProcessing] = useState(false);
    const [snapshotReady, setSnapshotReady] = useState(false);
    const [query, setQuery] = useState("");
    const [status, setStatus] = useState("all");
    const [error, setError] = useState("");
    const snapshotRequestRef = useRef<Promise<void> | null>(null);

    const loadSnapshot = useCallback(() => {
        if (snapshotRequestRef.current) {
            return snapshotRequestRef.current;
        }
        const request = (async () => {
            setLoading(true);
            setError("");
            try {
                const normalized = normalizeTaskShadowDebugSnapshot(
                    await taskShadowDebugWithTimeout(
                        GetTaskShadowDebugSnapshot(200),
                    ),
                );
                setSnapshot(normalized);
                setSnapshotReady(true);
                setSelectedEpisodeID((current) =>
                    initialEpisodeID &&
                    normalized.episodes.some(
                        (item) => item.id === initialEpisodeID,
                    )
                        ? initialEpisodeID
                        : normalized.episodes.some((item) => item.id === current)
                        ? current
                        : normalized.episodes[0]?.id ?? null,
                );
            } catch (loadError) {
                setError(String(loadError));
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
    }, [initialEpisodeID]);

    const loadDetail = useCallback(async (episodeID: string) => {
        setDetailLoading(true);
        setError("");
        try {
            setDetail(await GetTaskShadowDebugDetail(episodeID, "", 200));
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
        if (initialEpisodeID) {
            setCompactDetailOpen(true);
        }
    }, [initialEpisodeID]);

    useEffect(() => {
        if (!selectedEpisodeID) {
            setDetail(null);
            return;
        }
        void loadDetail(selectedEpisodeID);
    }, [loadDetail, selectedEpisodeID]);

    useEffect(() => {
        const refresh = () => {
            void loadSnapshot();
            if (selectedEpisodeID) {
                void loadDetail(selectedEpisodeID);
            }
        };
        const unsubscribers = [
            EventsOn("shadow_agent:task_pipeline", refresh),
            EventsOn("shadow_agent:task_continuation", refresh),
            EventsOn("shadow_agent:task_evaluated", refresh),
            EventsOn("shadow_agent:quality_campaign", refresh),
            EventsOn("activity_pipeline:run", refresh),
        ];
        return () => {
            unsubscribers.forEach((unsubscribe) => unsubscribe());
        };
    }, [loadDetail, loadSnapshot, selectedEpisodeID]);

    const statuses = useMemo(
        () =>
            Array.from(
                new Set(
                    snapshot.episodes
                        .flatMap((item) => [
                            item.status,
                            item.latestPairState,
                        ])
                        .filter((value): value is string => Boolean(value)),
                ),
            ).sort(),
        [snapshot.episodes],
    );
    const normalizedQuery = query.trim().toLowerCase();
    const episodes = useMemo(
        () =>
            snapshot.episodes.filter((item) => {
                if (
                    status !== "all" &&
                    item.status !== status &&
                    item.latestPairState !== status
                ) {
                    return false;
                }
                if (!normalizedQuery) return true;
                return `${item.id} ${item.taskHypothesis} ${item.desiredResultHypothesis} ${item.latestPairWorkflow} ${item.latestPairState} ${(item.latestPairReasons ?? []).join(" ")}`
                    .toLowerCase()
                    .includes(normalizedQuery);
            }),
        [normalizedQuery, snapshot.episodes, status],
    );

    const refresh = () => {
        void loadSnapshot();
        if (selectedEpisodeID) {
            void loadDetail(selectedEpisodeID);
        }
    };

    const processQualityCampaign = async () => {
        setCampaignProcessing(true);
        setError("");
        try {
            const result = await RunTaskShadowQualityCampaign();
            if (result.errors.length > 0) {
                toast.error(
                    `Campaign completed with ${result.errors.length} error${result.errors.length === 1 ? "" : "s"}`,
                );
            } else {
                toast.success(
                    `Processed ${result.executedRunIds.length} run${result.executedRunIds.length === 1 ? "" : "s"}; evaluated ${result.evaluatedRunIds.length} pair${result.evaluatedRunIds.length === 1 ? "" : "s"}`,
                );
            }
            await loadSnapshot();
            if (selectedEpisodeID) {
                await loadDetail(selectedEpisodeID);
            }
        } catch (campaignError) {
            setError(String(campaignError));
        } finally {
            setCampaignProcessing(false);
        }
    };

    return (
        <div>
            <div className="settings-card--padded space-y-3 border-b border-border">
                <div className="flex items-center justify-between gap-3">
                    <div>
                        <h2 className="text-[12px] font-semibold text-ink">
                            Observed tasks
                        </h2>
                        <p className="mt-0.5 text-[10px] text-muted">
                            Follow each task from detection through Isoform's private result, your result, and the comparison between them.
                        </p>
                        <p className="mt-1 text-[9px] text-muted">
                            Tasks Isoform can safely practice:{" "}
                            <span className="text-ink">
                                {snapshotReady
                                    ? (snapshot.enabledWorkflows ?? [])
                                        .map(taskWorkflowLabel)
                                        .join(", ") || "None yet"
                                    : "Loading..."}
                            </span>
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
                {snapshotReady && snapshot.releaseGate && (
                    <TaskReleaseGate
                        gate={snapshot.releaseGate}
                        onOpenObserverTrace={onOpenObserverTrace}
                        onOpenBackgroundLearning={onOpenBackgroundLearning}
                    />
                )}
                {snapshotReady && snapshot.continuation && (
                    <TaskContinuation continuation={snapshot.continuation} />
                )}
                {snapshotReady && snapshot.qualityCampaign && (
                    <TaskQualityCampaign
                        campaign={snapshot.qualityCampaign}
                        processing={campaignProcessing}
                        onProcess={() => void processQualityCampaign()}
                    />
                )}
                {snapshotReady && snapshot.lifecycleHealth && (
                    <TaskLifecycleHealth health={snapshot.lifecycleHealth} />
                )}
                {snapshotReady ? (
                <div className="grid grid-cols-4 border-y border-border py-2">
                    {[
                        ["Observed tasks", snapshot.episodes.length],
                        [
                            "Private practice",
                            snapshot.episodes.reduce(
                                (total, item) => total + item.linkedShadowRunCount,
                                0,
                            ),
                        ],
                        [
                            "Ready to compare",
                            snapshot.episodes.filter(
                                (item) =>
                                    item.latestPairState === "pair_ready" ||
                                    item.latestPairState === "evaluated",
                            ).length,
                        ],
                        [
                            "Results compared",
                            snapshot.episodes.reduce(
                                (total, item) =>
                                    total + item.persistedEvaluationCount,
                                0,
                            ),
                        ],
                    ].map(([label, value]) => (
                        <div
                            key={label}
                            className="border-l border-border px-2 first:border-l-0 first:pl-0"
                        >
                            <div className="text-[9px] uppercase text-muted">
                                {label}
                            </div>
                            <div className="text-sm font-semibold tabular-nums text-ink">
                                {value}
                            </div>
                        </div>
                    ))}
                </div>
                ) : (
                    <div
                        role="status"
                        className="flex items-center gap-2 border-y border-border py-3 text-[10px] text-muted"
                    >
                        {loading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
                        <span>
                            {loading
                                ? "Loading observed tasks..."
                                : "Detected-task data is unavailable."}
                        </span>
                    </div>
                )}
                <div className="flex gap-2">
                    <div className="relative min-w-0 flex-1">
                        <Search
                            aria-hidden="true"
                            className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted"
                        />
                        <input
                            aria-label="Search observed tasks"
                            value={query}
                            onChange={(event) => setQuery(event.target.value)}
                            disabled={!snapshotReady}
                            className="h-8 w-full border border-border bg-panel pl-7 pr-2 text-xs text-ink outline-none focus:border-ink/40"
                            placeholder="Search task, result, or ID"
                        />
                    </div>
                    <select
                        aria-label="Filter task status"
                        value={status}
                        onChange={(event) => setStatus(event.target.value)}
                        disabled={!snapshotReady}
                        className="h-8 w-[160px] border border-border bg-panel px-2 text-xs text-ink"
                    >
                        <option value="all">All statuses</option>
                        {statuses.map((item) => (
                            <option key={item} value={item}>
                                {taskStatusLabel(item)}
                            </option>
                        ))}
                    </select>
                </div>
                {error && (
                    <div
                        role="alert"
                        className="flex gap-2 border-l-2 border-error pl-3 text-[10px] text-error"
                    >
                        <AlertCircle className="h-3.5 w-3.5 shrink-0" />
                        <span>{error}</span>
                    </div>
                )}
            </div>

            <DebugMasterDetail
                compactDetailOpen={compactDetailOpen}
                detailKey={selectedEpisodeID}
                detailLabel="Observed task details"
                listLabel="Observed tasks"
                onBack={() => setCompactDetailOpen(false)}
                list={
                <>
                    {loading && !snapshotReady ? (
                        <div className="flex justify-center p-6 text-muted">
                            <Loader2 className="h-5 w-5 animate-spin" />
                        </div>
                    ) : !snapshotReady ? (
                        <div className="p-4 text-xs text-muted">
                            Observed-task data is unavailable. Refresh after the diagnostic service recovers.
                        </div>
                    ) : episodes.length === 0 ? (
                        <div className="p-4 text-xs text-muted">
                            {normalizedQuery || status !== "all"
                                ? "No tasks match this view."
                                : "Isoform has not detected any supported tasks yet. Keep working normally; a task will appear after Isoform can verify where it began."}
                        </div>
                    ) : (
                        episodes.map((episode: TaskShadowDebugEpisodeSummary) => (
                            <button
                                key={episode.id}
                                type="button"
                                aria-current={
                                    selectedEpisodeID === episode.id
                                        ? "true"
                                        : undefined
                                }
                                className={`w-full border-b border-border px-3 py-3 text-left hover:bg-ink/[0.03] ${
                                    selectedEpisodeID === episode.id
                                        ? "bg-ink/[0.05]"
                                        : ""
                                }`}
                                onClick={() => {
                                    setSelectedEpisodeID(episode.id);
                                    setCompactDetailOpen(true);
                                }}
                            >
                                <div className="flex items-start justify-between gap-2">
                                    <span className="line-clamp-2 text-xs font-medium text-ink">
                                        {episode.taskHypothesis}
                                    </span>
                                    <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted" />
                                </div>
                                <p className="mt-1 line-clamp-2 text-[10px] text-muted">
                                    {episode.desiredResultHypothesis}
                                </p>
                                <div className="mt-2 flex items-center justify-between gap-2">
                                    <div className="flex flex-wrap items-center gap-1">
                                        <TaskStatus status={episode.status} />
                                        {episode.latestPairState && (
                                            <TaskStatus
                                                status={episode.latestPairState}
                                            />
                                        )}
                                    </div>
                                    <span className="text-[9px] tabular-nums text-muted">
                                        {episode.linkedShadowRunCount} Isoform {episode.linkedShadowRunCount === 1 ? "attempt" : "attempts"} ·{" "}
                                        {episode.persistedEvaluationCount} {episode.persistedEvaluationCount === 1 ? "comparison" : "comparisons"}
                                    </span>
                                </div>
                            </button>
                        ))
                    )}
                </>
                }
                detail={
                    <TaskEpisodeDetail detail={detail} loading={detailLoading} />
                }
            />
        </div>
    );
}
