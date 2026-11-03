import {
    forwardRef,
    useCallback,
    useEffect,
    useImperativeHandle,
    useMemo,
    useRef,
    useState,
} from 'react';
import { useTranslation } from 'react-i18next';
import { useConnectedPlatforms } from '@/hooks/useConnectedPlatforms';
import {
    AlertCircle,
    CheckCircle2,
    ChevronDown,
    ChevronRight,
    Clock,
    ExternalLink,
    FolderOpen,
    Loader2,
    X,
} from 'lucide-react';
import { Collapsible, CollapsibleTrigger, CollapsibleContent } from '@/components/ui/collapsible';
import { ScrollArea } from '@/components/ui/scroll-area';
import { FailureLine } from '@/components/tasks/FailureLine';
import { measureTasks, recordTasksAction } from '@/lib/tasks-telemetry';
import { EventsOn } from '../../../wailsjs/runtime/runtime';
import { OpenAutomationDeliverable, SelectWorkDir } from '../../../wailsjs/go/main/App';
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from '@/components/ui/select';
import { GroupedStreamList } from '../grouped-stream-list';
import { MarkdownContent } from '../markdown-content';
import { parseQuotaError, friendlyErrorMessage } from '../stream-card';
import { extractAgentBlocks } from '@/lib/agent-deliverables';
import { cn } from '@/lib/utils';
import { RunBackendLabel, runBackendName } from './RunBackendLabel';
import { AskAboutRun } from './ScheduleAsk';
import type { TFunction } from 'i18next';
import type { CronJobState, CronRunState } from '@/hooks/useCronJobs';
import type { StreamItem } from '@/types';
import {
    type SchedulePreset,
    createScheduleTextLabels,
    getWeekdayToggles,
    weekdaysToCron,
    getPresetSummary,
    parseCronToPreset,
    cronToHumanReadable,
} from './schedule-utils';

export interface CronJobDetailHandle {
    enterEdit(): void;
    cancelEdit(): void;
    save(): Promise<void>;
}

interface CronJobDetailProps {
    job: CronJobState;
    onUpdate: (id: string, patch: Record<string, unknown>) => Promise<void>;
    loadRuns: (jobId: string) => Promise<CronRunState[]>;
    loadRunLogs: (jobId: string, runId: string) => Promise<StreamItem[]>;
    sourceSessionLabel?: string;
    onNavigateToSourceSession?: () => void;
    /** Opens the main chat at an answer asked about a run. */
    onOpenChatMessage?: (sessionId: string, itemId: string) => void;
    /** Reports whether the detail is currently in edit mode, so the parent header can swap actions. */
    onEditingChange?: (editing: boolean) => void;
    /** Reports whether a save is in flight. */
    onSavingChange?: (saving: boolean) => void;
    wideMode?: boolean;
}

function getPromptFromJob(job: CronJobState): string {
    const tc = job.targetConfig as Record<string, unknown> | null;
    if (tc && typeof tc.prompt === 'string') return tc.prompt;
    return '';
}

function getWorkDirFromJob(job: CronJobState): string {
    const tc = job.targetConfig as Record<string, unknown> | null;
    if (tc && typeof tc.project_dir === 'string') return tc.project_dir;
    return '';
}

function getNotifyPlatformFromJob(job: CronJobState): string {
    const value = (job as unknown as Record<string, unknown>).notifyPlatform;
    return typeof value === 'string' ? value : '';
}

const NOTIFY_PLATFORM_ENTRIES: { value: string; label: string }[] = [
    { value: 'slack', label: 'Slack' },
    { value: 'discord', label: 'Discord' },
    { value: 'telegram', label: 'Telegram' },
    { value: 'feishu', label: 'Feishu' },
    { value: 'wechat', label: 'WeChat' },
];

function platformLabel(platform: string, t: (key: any, options?: any) => string): string {
    if (!platform) return t('automations.manual');
    const platformNames: Record<string, string> = {
        chat: 'Chat',
        slack: 'Slack',
        feishu: 'Feishu',
        discord: 'Discord',
        telegram: 'Telegram',
        activity: 'Activity',
    };
    return t('automations.fromPlatform', { platform: platformNames[platform] || platform });
}

function formatTimestamp(ms: number): string {
    if (!ms) return '--';
    return new Date(ms).toLocaleString([], {
        month: 'short', day: 'numeric',
        hour: 'numeric', minute: '2-digit',
    });
}

function formatDuration(ms: number): string {
    if (!ms) return '--';
    if (ms < 1000) return `${ms}ms`;
    const secs = Math.floor(ms / 1000);
    if (secs < 60) return `${secs}s`;
    const mins = Math.floor(secs / 60);
    const remainSecs = secs % 60;
    return `${mins}m ${remainSecs}s`;
}

function RunStatusIcon({ status }: { status: string }) {
    switch (status) {
        case 'running':
            return <Loader2 className="h-3.5 w-3.5 text-automation animate-spin" />;
        case 'error':
        case 'blocked':
            return <AlertCircle className="h-3.5 w-3.5 text-error" />;
        case 'ok':
            return <CheckCircle2 className="h-3.5 w-3.5 text-success" />;
        case 'missed':
            return <AlertCircle className="h-3.5 w-3.5 text-muted" />;
        default:
            return <Clock className="h-3.5 w-3.5 text-muted" />;
    }
}

/** Why a scheduled time passed without a run: the scheduler notes when the
 *  assistant was not ready; otherwise the Mac was asleep or Isoform closed. */
function missedRunText(run: CronRunState, t: TFunction<'app'>): string {
    return run.error ? t('automations.missedNotReady') : t('automations.missedRun');
}

function runErrorText(run: CronRunState, t: TFunction<'app'>): string {
    if (run.status !== 'blocked') {
        return parseQuotaError(run.error)?.message ?? friendlyErrorMessage(run.error);
    }

    const backend = runBackendName(run.backend ?? '', t) ?? t('automations.runError.assistant');
    // Older records may only carry the typed error's count. Never display its
    // raw diagnostic text as localized product copy, including in tooltips.
    const recordedCount = /^(?:assistant|Isoform) stopped after (\d+) actions?(?:[;:]|$)/.exec(run.error)?.[1];
    const beforeActions = /^(?:assistant|Isoform) stopped before any actions(?:[;:]|$)/.test(run.error);
    const count = run.actionsStarted ?? (recordedCount === undefined ? (beforeActions ? 0 : undefined) : Number(recordedCount));
    const unknownOutcome = /(?:^|;\s*)(?:run )?(?:unknown outcome|outcome unknown)(?:[;:]|$)/i.test(run.error);
    let progress = t('automations.runError.stopped', { backend });
    if (count !== undefined && Number.isSafeInteger(count) && count > 0) {
        progress = t('automations.runError.stoppedAfterActions', { backend, count });
    } else if (count === 0 && !unknownOutcome) {
        progress = t('automations.runError.stoppedBeforeActions', { backend });
    }
    return unknownOutcome
        ? t('automations.runError.blockedUnknown', { progress })
        : t('automations.runError.blocked', { progress });
}

const SUBTLE_PILL_CLASS = 'inline-flex items-center rounded-full border border-border/60 bg-panel/40 px-2 py-0.5 text-[10px] text-muted';

function TriggerBadge({ kind }: { kind: string }) {
    const { t } = useTranslation('app');
    // A scheduled run is the expected default — badging every one of them is
    // just noise. Only surface the exceptions (a manual "Run now" or a catch-up).
    if (kind !== 'manual' && kind !== 'catchup') return null;
    const label = kind === 'manual'
        ? t('automations.triggerManual')
        : t('automations.triggerCatchup');
    return <span className={SUBTLE_PILL_CLASS}>{label}</span>;
}

interface DMNotifyResult {
    sent: boolean;
    platform?: string;
    channel_delivered?: boolean;
    channel_target?: string;
    error?: string;
    skipped?: { platform: string; reason: string }[];
    attachments_sent?: number;
    attachment_errors?: string[];
}

function NotifyBadge({ json }: { json: string }) {
    const { t } = useTranslation('app');
    if (!json) return null;
    let result: DMNotifyResult;
    try {
        result = JSON.parse(json) as DMNotifyResult;
    } catch {
        return null;
    }
    if (result.sent && result.platform) {
        const attachInfo: string[] = [t('automations.dmSentVia', { platform: result.platform })];
        if (result.attachments_sent) {
            attachInfo.push(t('automations.filesAttached', { count: result.attachments_sent }));
        }
        if (result.attachment_errors?.length) {
            attachInfo.push(t('automations.failed', { error: result.attachment_errors.join('; ') }));
        }
        const hasAttachmentErrors = (result.attachment_errors?.length ?? 0) > 0;
        return (
            <span
                className={SUBTLE_PILL_CLASS}
                title={attachInfo.join('\n')}
            >
                {t('automations.viaPlatform', { platform: result.platform })}
                {hasAttachmentErrors ? t('automations.fileError') : ''}
            </span>
        );
    }
    if (result.channel_delivered) {
        const target = result.channel_target || '';
        const [platform] = target.split(':');
        return (
            <span
                className={SUBTLE_PILL_CLASS}
                title={t('automations.deliveredToChannel', { target })}
            >
                {platform ? t('automations.viaPlatform', { platform }) : t('automations.viaChannel')}
            </span>
        );
    }
    const reasons = (result.skipped || []).map(s => `${s.platform}: ${s.reason}`).join('\n');
    return (
        <span
            className={SUBTLE_PILL_CLASS}
            title={reasons || result.error || t('automations.noDmSent')}
        >
            {t('automations.noDm')}
        </span>
    );
}

/**
 * Pull the run's plain-language result — the agent's final assistant message,
 * cleaned of agent-only markup. Walking from the end (rather than trusting the
 * stream grouping) keeps this robust: cron logs are sliced by sort-order range
 * and can split the summary into a trailing turn that the transcript view clips.
 */
function extractFinalResult(logs: StreamItem[] | null): string {
    if (!logs) return '';
    for (let i = logs.length - 1; i >= 0; i--) {
        const item = logs[i];
        if (item.kind === 'assistant' && item.content && item.content.trim()) {
            const cleaned = extractAgentBlocks(item.content).content.trim();
            if (cleaned) return cleaned;
        }
    }
    return '';
}

/**
 * Result-first render of a single run's output: the outcome reads as prose,
 * and the step-by-step transcript (tool calls, commands, raw output) tucks
 * behind a quiet "Run details" disclosure. Shared by the featured last run and
 * the run-history rows so both speak the same friendly language.
 */
function RunOutputBody({
    run,
    logs,
    logsLoading,
    onOpenChatMessage,
    onOpenDeliverable,
    logsFailed,
    onRetryLogs,
    openFailed,
}: {
    run: CronRunState;
    logs: StreamItem[] | null;
    logsLoading: boolean;
    onOpenChatMessage?: (sessionId: string, itemId: string) => void;
    onOpenDeliverable: (path: string) => void;
    logsFailed?: boolean;
    onRetryLogs?: () => void;
    openFailed?: string | null;
}) {
    const { t } = useTranslation('app');
    const [detailsOpen, setDetailsOpen] = useState(false);
    const result = useMemo(() => extractFinalResult(logs), [logs]);

    if (logsLoading && !logs) {
        return (
            <div className="flex items-center py-6">
                <Loader2 className="h-3.5 w-3.5 animate-spin text-muted" />
                <span className="text-xs text-muted ml-2">{t('automations.loading')}</span>
            </div>
        );
    }

    // A failed read is never "No log entries" (C13).
    if (logsFailed && !logs) {
        return <FailureLine className="py-2" testId="run-output-failed" text={t('tasks.failure.runOutput')} onRetry={onRetryLogs} />;
    }

    if (!logs || logs.length === 0) {
        return <p className="text-xs text-muted text-center py-4">{t('automations.noLogEntries')}</p>;
    }

    const transcript = (
        <div className="rounded-xl border border-border/70 px-3 py-2.5 max-h-[360px] overflow-y-auto">
            <GroupedStreamList
                items={logs}
                streaming={run.status === 'running'}
                executorType="claude"
                compact
                hideUserPrompt
                onOpenDeliverable={onOpenDeliverable}
            />
        </div>
    );

    const failedFile = openFailed ? openFailed.split(/[\\/]/).pop() || openFailed : null;
    return (
        <div>
            {logsFailed && <FailureLine className="mb-2" text={t('tasks.failure.runOutput')} onRetry={onRetryLogs} />}
            {result ? (
                <div className="text-[13px] text-ink leading-relaxed break-words">
                    <MarkdownContent content={result} />
                </div>
            ) : (
                transcript
            )}

            {result && (
                <>
                    <button
                        type="button"
                        onClick={() => setDetailsOpen((v) => !v)}
                        className="mt-3 inline-flex items-center gap-1 text-[11px] text-muted hover:text-ink transition-colors"
                    >
                        <ChevronRight className={cn('h-3 w-3 transition-transform', detailsOpen && 'rotate-90')} />
                        {t('automations.runDetails')}
                    </button>
                    {detailsOpen && <div className="mt-2">{transcript}</div>}
                </>
            )}

            {openFailed && failedFile && <FailureLine className="mt-2" testId="run-file-failed" text={t('tasks.failure.openFile', { file: failedFile })} onRetry={() => onOpenDeliverable(openFailed)} />}

            {run.status !== 'running' && (
                <div className="mt-3 pt-2.5 border-t border-border/40">
                    <AskAboutRun run={run} onOpenChatMessage={onOpenChatMessage} />
                </div>
            )}
        </div>
    );
}

function RunItem({
    run,
    jobId,
    loadRunLogs,
    onOpenChatMessage,
}: {
    run: CronRunState;
    jobId: string;
    loadRunLogs: (jobId: string, runId: string) => Promise<StreamItem[]>;
    onOpenChatMessage?: (sessionId: string, itemId: string) => void;
}) {
    const { t } = useTranslation('app');
    const [expanded, setExpanded] = useState(false);
    const [logs, setLogs] = useState<StreamItem[] | null>(null);
    const [logsLoading, setLogsLoading] = useState(false);
    const [logsFailed, setLogsFailed] = useState(false);
    const [logsRetry, setLogsRetry] = useState(0);
    const [openFailed, setOpenFailed] = useState<string | null>(null);
    const hasContent = !!(run.error || run.hasLogs);
    const needsInitialLoad = logs === null;

    // "Couldn't open {file}. Try again" under the output (C15), never a toast.
    const handleOpenDeliverable = useCallback((path: string) => {
        setOpenFailed(null);
        measureTasks('C15', 'open', 'opened', async () => OpenAutomationDeliverable(jobId, path)).catch(() => setOpenFailed(path));
    }, [jobId]);

    const handleToggle = useCallback(() => {
        if (!hasContent) return;
        recordTasksAction('C12', 'open', 'opened');
        setExpanded((prev) => !prev);
    }, [hasContent]);

    useEffect(() => {
        if (!expanded || !run.hasLogs) return;

        let active = true;
        let inFlight = false;

        const refreshLogs = async (showLoader: boolean) => {
            if (inFlight) return;
            inFlight = true;
            if (showLoader) setLogsLoading(true);
            try {
                const items = await loadRunLogs(jobId, run.id);
                if (active) { setLogs(items); setLogsFailed(false); }
            } catch {
                if (active && showLoader) setLogsFailed(true);
            } finally {
                if (active && showLoader) setLogsLoading(false);
                inFlight = false;
            }
        };

        void refreshLogs(needsInitialLoad);

        if (run.status !== 'running') {
            return () => { active = false; };
        }

        const intervalId = window.setInterval(() => {
            void refreshLogs(false);
        }, 1000);

        return () => {
            active = false;
            window.clearInterval(intervalId);
        };
    }, [expanded, run.hasLogs, run.status, run.id, jobId, loadRunLogs, needsInitialLoad, logsRetry]);

    return (
        <div className="rounded-lg overflow-hidden">
            <div
                className={cn(
                    'flex items-center gap-2 px-3 py-2 transition-colors',
                    hasContent && 'cursor-pointer hover:bg-ink/[0.06]',
                )}
                onClick={handleToggle}
            >
                <RunStatusIcon status={run.status} />
                <span className="text-xs text-ink tabular-nums">{formatTimestamp(run.startedAt)}</span>
                <TriggerBadge kind={run.triggerKind} />
                <NotifyBadge json={run.notifyResult} />
                <div className="flex-1" />
                <span className="text-xs text-muted tabular-nums">{formatDuration(run.durationMs)}</span>
                {hasContent && (
                    <ChevronDown className={cn('h-3 w-3 text-muted transition-transform', expanded && 'rotate-180')} />
                )}
            </div>
            <RunBackendLabel run={run} className="text-xs text-muted ml-[34px] mr-3 mb-2" />
            {run.error && run.status !== 'missed' && (() => {
                const friendly = runErrorText(run, t);
                return (
                    <p
                        className={cn('text-xs text-error ml-[34px] mr-3 mb-2', !expanded && 'truncate')}
                        title={run.status === 'blocked' ? friendly : run.error}
                    >
                        {friendly}
                    </p>
                );
            })()}
            {run.status === 'missed' && (
                <p className="text-xs text-muted ml-[34px] mr-3 mb-2">{missedRunText(run, t)}</p>
            )}

            {expanded && run.hasLogs && (
                <div className="px-3 pb-3 pt-1">
                    <RunOutputBody
                        run={run}
                        logs={logs}
                        logsLoading={logsLoading}
                        onOpenChatMessage={onOpenChatMessage}
                        onOpenDeliverable={handleOpenDeliverable}
                    logsFailed={logsFailed}
                    onRetryLogs={() => { recordTasksAction('C13', 'do', 'done'); setLogsRetry((n) => n + 1); }}
                    openFailed={openFailed}
                    />
                </div>
            )}
        </div>
    );
}

function FeaturedRunOutput({
    run,
    jobId,
    loadRunLogs,
    label,
    onOpenChatMessage,
}: {
    run: CronRunState;
    jobId: string;
    loadRunLogs: (jobId: string, runId: string) => Promise<StreamItem[]>;
    label?: string;
    onOpenChatMessage?: (sessionId: string, itemId: string) => void;
}) {
    const { t } = useTranslation('app');
    const [logs, setLogs] = useState<StreamItem[] | null>(null);
    const [logsLoading, setLogsLoading] = useState(false);
    const [logsFailed, setLogsFailed] = useState(false);
    const [logsRetry, setLogsRetry] = useState(0);
    const [openFailed, setOpenFailed] = useState<string | null>(null);
    const needsInitialLoad = logs === null;
    const runFailed = Boolean(run.error);

    // "Couldn't open {file}. Try again" under the output (C15), never a toast.
    const handleOpenDeliverable = useCallback((path: string) => {
        setOpenFailed(null);
        measureTasks('C15', 'open', 'opened', async () => OpenAutomationDeliverable(jobId, path)).catch(() => setOpenFailed(path));
    }, [jobId]);

    useEffect(() => {
        if (!run.hasLogs) return;

        let active = true;
        let inFlight = false;

        const refreshLogs = async (showLoader: boolean) => {
            if (inFlight) return;
            inFlight = true;
            if (showLoader) setLogsLoading(true);
            try {
                const items = await loadRunLogs(jobId, run.id);
                if (active) { setLogs(items); setLogsFailed(false); }
            } catch {
                if (active && showLoader) setLogsFailed(true);
            } finally {
                if (active && showLoader) setLogsLoading(false);
                inFlight = false;
            }
        };

        void refreshLogs(needsInitialLoad);

        if (run.status !== 'running') {
            return () => { active = false; };
        }

        const intervalId = window.setInterval(() => {
            void refreshLogs(false);
        }, 1000);

        return () => {
            active = false;
            window.clearInterval(intervalId);
        };
    }, [run.hasLogs, run.status, run.id, jobId, loadRunLogs, needsInitialLoad, logsRetry]);

    return (
        <div>
            <div className="flex items-center gap-2 mb-3">
                <RunStatusIcon status={run.status} />
                <span className="text-xs text-ink tabular-nums">{formatTimestamp(run.startedAt)}</span>
                <TriggerBadge kind={run.triggerKind} />
                <NotifyBadge json={run.notifyResult} />
                {label && <span className="text-[10px] text-muted">({label})</span>}
                <div className="flex-1" />
                <span className="text-xs text-muted tabular-nums">{formatDuration(run.durationMs)}</span>
            </div>

            <RunBackendLabel run={run} className="text-xs text-muted mb-2" />
            {run.error && run.status !== 'missed' && (() => {
                const friendly = runErrorText(run, t);
                return (
                    <p className="text-xs text-error mb-2 leading-relaxed" title={run.status === 'blocked' ? friendly : run.error}>{friendly}</p>
                );
            })()}

            {run.hasLogs && !runFailed && (
                <RunOutputBody
                    run={run}
                    logs={logs}
                    logsLoading={logsLoading}
                    onOpenChatMessage={onOpenChatMessage}
                    onOpenDeliverable={handleOpenDeliverable}
                    logsFailed={logsFailed}
                    onRetryLogs={() => { recordTasksAction('C13', 'do', 'done'); setLogsRetry((n) => n + 1); }}
                    openFailed={openFailed}
                />
            )}

            {!run.hasLogs && run.status === 'missed' && (
                <p className="text-xs text-muted py-2">{missedRunText(run, t)}</p>
            )}
            {!run.hasLogs && !run.error && run.status !== 'running' && run.status !== 'missed' && (
                <p className="text-xs text-muted py-2">{t('automations.noOutputAvailable')}</p>
            )}
        </div>
    );
}

function SectionHeading({ children }: { children: React.ReactNode }) {
    return (
        <h3 className="text-[10px] font-medium text-muted uppercase tracking-wider mb-3">
            {children}
        </h3>
    );
}

export const CronJobDetail = forwardRef<CronJobDetailHandle, CronJobDetailProps>(function CronJobDetail({
    job,
    sourceSessionLabel,
    onNavigateToSourceSession,
    onUpdate,
    loadRuns,
    loadRunLogs,
    onOpenChatMessage,
    onEditingChange,
    onSavingChange,
    wideMode,
}, ref) {
    const { t } = useTranslation('app');
    const { connected: connectedPlatforms, refresh: refreshPlatforms } = useConnectedPlatforms();
    const scheduleTextLabels = useMemo(() => createScheduleTextLabels(t), [t]);
    const weekdayToggles = useMemo(() => getWeekdayToggles(scheduleTextLabels), [scheduleTextLabels]);

    const [runs, setRuns] = useState<CronRunState[]>([]);
    const [runsLoading, setRunsLoading] = useState(true);

    // In-place edit state.
    const [editing, setEditing] = useState(false);
    const [editName, setEditName] = useState('');
    const [editPrompt, setEditPrompt] = useState('');
    const [editSchedulePreset, setEditSchedulePreset] = useState<SchedulePreset>('daily');
    const [editHour, setEditHour] = useState('9');
    const [editMinute, setEditMinute] = useState('0');
    const [editWeekdays, setEditWeekdays] = useState<Set<string>>(() => new Set(['1']));
    const [editCustomCron, setEditCustomCron] = useState('');
    const [editWorkDir, setEditWorkDir] = useState('');
    const [editNotifyDM, setEditNotifyDM] = useState(true);
    const [editNotifyPlatform, setEditNotifyPlatform] = useState('');
    const [saving, setSaving] = useState(false);
    const [saveFailed, setSaveFailed] = useState(false);
    const [folderFailed, setFolderFailed] = useState(false);
    const [runsFailed, setRunsFailed] = useState(false);
    const promptTextareaRef = useRef<HTMLTextAreaElement>(null);

    const clampAndSet = useCallback(
        (setter: (v: string) => void, min: number, max: number) =>
            (e: React.ChangeEvent<HTMLInputElement>) => {
                const raw = e.target.value;
                if (raw === '') { setter(''); return; }
                const n = parseInt(raw, 10);
                if (Number.isNaN(n)) return;
                setter(String(Math.min(Math.max(n, min), max)));
            },
        [],
    );

    const currentPrompt = useMemo(() => getPromptFromJob(job), [job]);
    const currentWorkDir = useMemo(() => getWorkDirFromJob(job), [job]);

    const currentCronExpr = useMemo(() => {
        if (job.scheduleKind !== 'cron') return '';
        const sc = job.scheduleConfig as Record<string, unknown> | null;
        if (sc && typeof sc.expr === 'string') return sc.expr;
        return '';
    }, [job.scheduleKind, job.scheduleConfig]);

    const enterEditMode = useCallback(() => {
        refreshPlatforms();
        setEditName(job.name);
        setEditPrompt(currentPrompt);
        if (job.scheduleKind === 'manual') {
            setEditSchedulePreset('none');
            setEditHour('9');
            setEditMinute('0');
            setEditWeekdays(new Set(['1']));
            setEditCustomCron('');
        } else {
            const parsed = parseCronToPreset(currentCronExpr);
            setEditSchedulePreset(parsed.preset);
            setEditHour(parsed.hour);
            setEditMinute(parsed.minute);
            setEditWeekdays(parsed.weekdays);
            setEditCustomCron(parsed.customCron);
        }
        setEditWorkDir(currentWorkDir);
        setEditNotifyDM(job.notifyDm);
        setEditNotifyPlatform(getNotifyPlatformFromJob(job));
        setEditing(true);
    }, [job.name, currentPrompt, currentCronExpr, currentWorkDir, job.notifyDm, job, refreshPlatforms]);

    const handleSelectWorkDir = useCallback(async () => {
        setFolderFailed(false);
        try {
            const dir = await measureTasks('C07', 'open', 'opened', async () => SelectWorkDir());
            if (dir) setEditWorkDir(dir);
        } catch (err) {
            console.error('[cron-detail] select workdir failed:', err);
            setFolderFailed(true);
        }
    }, []);

    const cancelEdit = useCallback(() => {
        recordTasksAction('C09', 'do', 'undone');
        setSaveFailed(false);
        setFolderFailed(false);
        setEditing(false);
    }, []);

    const buildCronExpr = useCallback((): string => {
        if (editSchedulePreset === 'custom') return editCustomCron.trim();
        const m = editMinute || '0';
        const h = editHour || '9';
        switch (editSchedulePreset) {
            case 'hourly': return `${m} * * * *`;
            case 'daily': return `${m} ${h} * * *`;
            case 'weekdays': return `${m} ${h} * * 1-5`;
            case 'weekly': return `${m} ${h} * * ${weekdaysToCron(editWeekdays)}`;
            default: return `${m} ${h} * * *`;
        }
    }, [editSchedulePreset, editCustomCron, editHour, editMinute, editWeekdays]);

    const saveEdit = useCallback(async () => {
        setSaving(true);
        setSaveFailed(false);
        const started = performance.now();
        try {
            const patch: Record<string, unknown> = {};
            if (editName.trim() && editName.trim() !== job.name) {
                patch.name = editName.trim();
            }
            const promptChanged = editPrompt.trim() !== currentPrompt;
            const workDirChanged = editWorkDir !== currentWorkDir;
            if (promptChanged || workDirChanged) {
                const existingConfig = (job.targetConfig as Record<string, unknown>) || {};
                patch.targetConfig = {
                    ...existingConfig,
                    ...(promptChanged ? { prompt: editPrompt.trim() } : {}),
                    project_dir: editWorkDir,
                };
            }
            if (editSchedulePreset === 'none') {
                if (job.scheduleKind !== 'manual') {
                    patch.scheduleKind = 'manual';
                    patch.scheduleConfig = {};
                }
            } else if (job.scheduleKind === 'manual') {
                const newCron = buildCronExpr();
                if (newCron) {
                    patch.scheduleKind = 'cron';
                    patch.scheduleConfig = { expr: newCron };
                }
            } else if (job.scheduleKind === 'cron') {
                const newCron = buildCronExpr();
                if (newCron && newCron !== currentCronExpr) {
                    const existingSchedule = (job.scheduleConfig as Record<string, unknown>) || {};
                    patch.scheduleConfig = { ...existingSchedule, expr: newCron };
                }
            }
            if (editNotifyDM !== job.notifyDm) {
                patch.notifyDm = editNotifyDM;
            }
            const currentNotifyPlatform = getNotifyPlatformFromJob(job);
            if (editNotifyPlatform !== currentNotifyPlatform) {
                patch.notifyPlatform = editNotifyPlatform;
            }
            if (Object.keys(patch).length > 0) {
                await onUpdate(job.id, patch);
            }
            recordTasksAction('C08', 'do', 'done', started);
            setEditing(false);
        } catch (error) {
            console.error('[cron-detail] failed to save:', error);
            recordTasksAction('C08', 'do', 'failed', started);
            setSaveFailed(true);
        } finally {
            setSaving(false);
        }
    }, [editName, editPrompt, editWorkDir, editNotifyDM, editNotifyPlatform, editSchedulePreset, job, currentPrompt, currentCronExpr, currentWorkDir, buildCronExpr, onUpdate]);

    // Auto-resize prompt textarea while editing.
    useEffect(() => {
        if (editing && promptTextareaRef.current) {
            const el = promptTextareaRef.current;
            el.style.height = 'auto';
            el.style.height = `${el.scrollHeight}px`;
        }
    }, [editing, editPrompt]);

    const refreshRuns = useCallback(async (showLoader: boolean) => {
        if (showLoader) setRunsLoading(true);
        try {
            const nextRuns = await loadRuns(job.id);
            setRuns(nextRuns);
            setRunsFailed(false);
        } catch (error) {
            console.error('[cron-detail] failed to load runs:', error);
            setRunsFailed(true);
        } finally {
            if (showLoader) setRunsLoading(false);
        }
    }, [job.id, loadRuns]);

    useEffect(() => {
        void refreshRuns(true);
        const intervalId = window.setInterval(() => {
            void refreshRuns(false);
        }, 1000);
        return () => { window.clearInterval(intervalId); };
    }, [refreshRuns]);

    useEffect(() => {
        const unsub = EventsOn('cron:run', (run: CronRunState) => {
            if (run.jobId !== job.id) return;
            setRuns(prev => {
                const idx = prev.findIndex(r => r.id === run.id);
                // Newest first, as the store lists them: a missed time is
                // recorded after the fact, so it goes where its time falls.
                if (idx === -1) return [run, ...prev].sort((a, b) => b.startedAt - a.startedAt);
                const next = [...prev];
                next[idx] = run;
                return next;
            });
        });
        return () => { unsub(); };
    }, [job.id]);

    useImperativeHandle(ref, () => ({
        enterEdit: () => enterEditMode(),
        cancelEdit: () => cancelEdit(),
        save: () => saveEdit(),
    }), [enterEditMode, cancelEdit, saveEdit]);

    useEffect(() => {
        onEditingChange?.(editing);
    }, [editing, onEditingChange]);

    useEffect(() => {
        onSavingChange?.(saving);
    }, [saving, onSavingChange]);

    const scheduleLabel = job.scheduleKind === 'manual'
        ? t('automations.runOnDemand')
        : (cronToHumanReadable(currentCronExpr, scheduleTextLabels) || job.scheduleDescription || currentCronExpr);

    const notifyPlatformValue = getNotifyPlatformFromJob(job);
    const notifyEnabled = job.notifyDm && notifyPlatformValue !== 'none';
    const notifyPlatformLabel = notifyPlatformValue === 'none'
        ? t('automations.dontSend')
        : notifyPlatformValue
            ? (NOTIFY_PLATFORM_ENTRIES.find((entry) => entry.value === notifyPlatformValue)?.label ?? notifyPlatformValue)
            : t('automations.autoSystemPicks');

    return (
        <ScrollArea className="flex-1 min-h-0">
            <div className="px-6 py-5">
                <div className={`mx-auto${wideMode ? '' : ' max-w-2xl'}`}>
                    {/* Last execution */}
                    <section>
                        <SectionHeading>{t('automations.lastExecution')}</SectionHeading>
                        {runsFailed && <FailureLine className="mb-2" testId="run-history-failed" text={t('tasks.failure.runHistory')} onRetry={() => { recordTasksAction('C14', 'do', 'done'); void refreshRuns(true); }} />}
                        {runsFailed && runs.length === 0 ? null : runsLoading ? (
                            <div className="flex items-center justify-center py-6">
                                <Loader2 className="h-4 w-4 animate-spin text-muted" />
                            </div>
                        ) : runs.length === 0 ? (
                            <p className="text-xs text-muted py-2">
                                {t('automations.noRuns', { runNow: t('automations.runNow') })}
                            </p>
                        ) : (() => {
                            const latestRun = runs[0];
                            const latestHasOutput = latestRun.hasLogs || latestRun.status === 'running';
                            const fallbackRun = !latestHasOutput
                                ? runs.find(r => r.id !== latestRun.id && r.status === 'ok' && r.hasLogs)
                                : undefined;
                            return (
                                <>
                                    <FeaturedRunOutput
                                        key={latestRun.id}
                                        run={latestRun}
                                        jobId={job.id}
                                        loadRunLogs={loadRunLogs}
                                        onOpenChatMessage={onOpenChatMessage}
                                    />
                                    {fallbackRun && (
                                        <div className="mt-4 pt-3 border-t border-border/40">
                                            <p className="text-[11px] text-muted mb-2">{t('automations.lastSuccessfulOutput')}</p>
                                            <FeaturedRunOutput
                                                key={fallbackRun.id}
                                                run={fallbackRun}
                                                jobId={job.id}
                                                loadRunLogs={loadRunLogs}
                                                onOpenChatMessage={onOpenChatMessage}
                                            />
                                        </div>
                                    )}
                                </>
                            );
                        })()}
                        {/* Run history — grouped with the runs above so it never
                            stacks two bare section labels against Configuration. */}
                        {!runsLoading && runs.length > 1 && (
                            <Collapsible className="mt-6">
                                <CollapsibleTrigger asChild>
                                    <button
                                        type="button"
                                        className="flex items-center gap-1.5 group text-left"
                                    >
                                        <ChevronRight className="h-3 w-3 text-muted transition-transform group-data-[state=open]:rotate-90" />
                                        <h3 className="text-[10px] font-medium text-muted uppercase tracking-wider">
                                            {t('automations.runHistory')}
                                        </h3>
                                        <span className="text-[10px] text-muted/60">{t('automations.moreRuns', { count: runs.length - 1 })}</span>
                                    </button>
                                </CollapsibleTrigger>
                                <CollapsibleContent>
                                    <div className="mt-3 space-y-1">
                                        {runs.slice(1).map((run) => (
                                            <RunItem
                                                key={run.id}
                                                run={run}
                                                jobId={job.id}
                                                loadRunLogs={loadRunLogs}
                                                onOpenChatMessage={onOpenChatMessage}
                                            />
                                        ))}
                                    </div>
                                </CollapsibleContent>
                            </Collapsible>
                        )}
                    </section>

                    {/* Configuration — an explicit divider + top padding guarantees a
                        clear gap from the run list above; relying on the parent's
                        space-y left the two bare section labels glued together. */}
                    <section className="mt-8 pt-6 border-t border-border/40">
                        <SectionHeading>{t('automations.configuration')}</SectionHeading>

                        {editing ? (
                            <div className="space-y-4">
                                {saveFailed && <FailureLine tone="copper" testId="schedule-save-failed" text={t('tasks.failure.save')} onRetry={() => void saveEdit()} />}
                                <div>
                                    <label className="text-[11px] text-muted block mb-1">{t('automations.name')}</label>
                                    <input
                                        type="text"
                                        value={editName}
                                        onChange={(e) => setEditName(e.target.value)}
                                        className="w-full px-2 py-1.5 text-xs text-ink bg-background border border-border rounded-md focus:outline-none focus:ring-1 focus:ring-accent/50"
                                    />
                                </div>
                                <div>
                                    <label className="text-[11px] text-muted block mb-1">{t('automations.description')}</label>
                                    <textarea
                                        ref={promptTextareaRef}
                                        value={editPrompt}
                                        onChange={(e) => setEditPrompt(e.target.value)}
                                        rows={3}
                                        className="w-full px-2 py-1.5 text-xs text-ink bg-background border border-border rounded-md focus:outline-none focus:ring-1 focus:ring-accent/50 resize-none overflow-hidden leading-relaxed"
                                    />
                                </div>
                                <div>
                                    <label className="text-[11px] text-muted block mb-1">{t('automations.schedule')}</label>
                                    <div className="space-y-2">
                                        <Select
                                            value={editSchedulePreset}
                                            onValueChange={(v: string) => { recordTasksAction('C06', 'do', 'done'); setEditSchedulePreset(v as SchedulePreset); }}
                                        >
                                            <SelectTrigger className="w-full h-8 text-xs">
                                                <SelectValue />
                                            </SelectTrigger>
                                            <SelectContent>
                                                <SelectItem value="none" className="text-xs">{t('automations.runOnDemandManual')}</SelectItem>
                                                <SelectItem value="hourly" className="text-xs">{t('automations.hourly')}</SelectItem>
                                                <SelectItem value="daily" className="text-xs">{t('automations.daily')}</SelectItem>
                                                <SelectItem value="weekdays" className="text-xs">{t('automations.weekdays')}</SelectItem>
                                                <SelectItem value="weekly" className="text-xs">{t('automations.weekly')}</SelectItem>
                                                <SelectItem value="custom" className="text-xs">{t('automations.custom')}</SelectItem>
                                            </SelectContent>
                                        </Select>

                                        {editSchedulePreset === 'weekly' && (
                                            <div className="flex gap-1">
                                                {weekdayToggles.map(d => {
                                                    const active = editWeekdays.has(d.value);
                                                    return (
                                                        <button
                                                            key={d.value}
                                                            type="button"
                                                            onClick={() => {
                                                                setEditWeekdays(prev => {
                                                                    const next = new Set(prev);
                                                                    if (next.has(d.value)) {
                                                                        if (next.size > 1) next.delete(d.value);
                                                                    } else {
                                                                        next.add(d.value);
                                                                    }
                                                                    return next;
                                                                });
                                                            }}
                                                            className={cn(
                                                                'px-2 py-1 text-[11px] rounded-md border transition-colors',
                                                                active
                                                                    ? 'border-border-strong bg-active-row-bg text-ink font-medium'
                                                                    : 'border-border text-muted hover:text-ink hover:bg-ink/[0.06]',
                                                            )}
                                                        >
                                                            {d.short}
                                                        </button>
                                                    );
                                                })}
                                            </div>
                                        )}

                                        {editSchedulePreset !== 'none' && editSchedulePreset !== 'custom' && editSchedulePreset !== 'hourly' && (
                                            <div className="flex items-center gap-1.5">
                                                <Clock className="h-3 w-3 text-muted shrink-0" />
                                                <input
                                                    type="number"
                                                    min={0}
                                                    max={23}
                                                    value={editHour}
                                                    onChange={clampAndSet(setEditHour, 0, 23)}
                                                    className="w-14 px-1.5 py-1 text-xs text-ink bg-background border border-border rounded-md focus:outline-none focus:ring-1 focus:ring-accent/50 text-center"
                                                />
                                                <span className="text-xs text-muted">:</span>
                                                <input
                                                    type="number"
                                                    min={0}
                                                    max={59}
                                                    value={editMinute}
                                                    onChange={clampAndSet(setEditMinute, 0, 59)}
                                                    className="w-14 px-1.5 py-1 text-xs text-ink bg-background border border-border rounded-md focus:outline-none focus:ring-1 focus:ring-accent/50 text-center"
                                                />
                                            </div>
                                        )}

                                        {editSchedulePreset === 'hourly' && (
                                            <div className="flex items-center gap-1.5">
                                                <Clock className="h-3 w-3 text-muted shrink-0" />
                                                <span className="text-xs text-muted">{t('automations.atMinute')}</span>
                                                <input
                                                    type="number"
                                                    min={0}
                                                    max={59}
                                                    value={editMinute}
                                                    onChange={clampAndSet(setEditMinute, 0, 59)}
                                                    className="w-14 px-1.5 py-1 text-xs text-ink bg-background border border-border rounded-md focus:outline-none focus:ring-1 focus:ring-accent/50 text-center"
                                                />
                                            </div>
                                        )}

                                        {editSchedulePreset === 'custom' ? (
                                            <input
                                                type="text"
                                                value={editCustomCron}
                                                onChange={(e) => setEditCustomCron(e.target.value)}
                                                placeholder="0 10 * * *"
                                                className="w-full px-2.5 py-1.5 text-xs text-ink bg-background border border-border rounded-md focus:outline-none focus:ring-1 focus:ring-accent/50 font-mono"
                                            />
                                        ) : (
                                            <p className="text-[11px] text-muted">
                                                {getPresetSummary(editSchedulePreset, editHour, editMinute, editWeekdays, scheduleTextLabels)}
                                            </p>
                                        )}
                                    </div>
                                </div>
                                <div>
                                    <label className="text-[11px] text-muted block mb-1">{t('automations.workDirectoryLower')}</label>
                                    <div className="flex items-center gap-2">
                                        <button
                                            type="button"
                                            onClick={handleSelectWorkDir}
                                            className="inline-flex items-center gap-1 px-2 py-1 text-xs rounded-md border border-border text-muted hover:text-ink hover:bg-ink/[0.06] transition-colors shrink-0"
                                        >
                                            <FolderOpen className="h-3 w-3" />
                                            {editWorkDir ? t('automations.change') : t('automations.select')}
                                        </button>
                                        {editWorkDir ? (
                                            <div className="flex items-center gap-1 flex-1 min-w-0">
                                                <span className="text-xs text-ink truncate" title={editWorkDir}>{editWorkDir}</span>
                                                <button
                                                    type="button"
                                                    onClick={() => setEditWorkDir('')}
                                                    className="p-0.5 rounded text-muted hover:text-ink shrink-0"
                                                >
                                                    <X className="h-3 w-3" />
                                                </button>
                                            </div>
                                        ) : (
                                            <span className="text-xs text-muted/60">{t('automations.workDirNotSetTemp')}</span>
                                        )}
                                    </div>
                                    {folderFailed && <FailureLine className="mt-1.5" testId="folder-picker-failed" text={t('tasks.failure.folderPicker')} onRetry={() => void handleSelectWorkDir()} />}
                                </div>
                                <div className="flex items-center justify-between">
                                    <label className="text-[11px] text-muted">{t('automations.completionNotificationLower')}</label>
                                    <button
                                        type="button"
                                        onClick={() => setEditNotifyDM(prev => !prev)}
                                        className={cn(
                                            'relative inline-flex h-5 w-9 items-center rounded-full transition-colors',
                                            editNotifyDM ? 'bg-accent' : 'bg-border',
                                        )}
                                    >
                                        <span
                                            className={cn(
                                                'inline-block h-3.5 w-3.5 rounded-full bg-panel transition-transform',
                                                editNotifyDM ? 'translate-x-[18px]' : 'translate-x-[3px]',
                                            )}
                                        />
                                    </button>
                                </div>
                                {editNotifyDM && (
                                    <div className="flex items-center justify-between">
                                        <label className="text-[11px] text-muted">{t('automations.notificationPlatformLower')}</label>
                                        <Select
                                            value={editNotifyPlatform || '__auto__'}
                                            onValueChange={(v) => setEditNotifyPlatform(v === '__auto__' ? '' : v)}
                                        >
                                            <SelectTrigger className="w-[140px] h-7 text-xs">
                                                <SelectValue />
                                            </SelectTrigger>
                                            <SelectContent>
                                                {(() => {
                                                    const opts: { value: string; label: string; disabled?: boolean }[] = [
                                                        { value: '', label: t('automations.autoSystemPicks') },
                                                    ];
                                                    for (const entry of NOTIFY_PLATFORM_ENTRIES) {
                                                        if (connectedPlatforms.has(entry.value)) {
                                                            opts.push(entry);
                                                        }
                                                    }
                                                    const savedPlat = editNotifyPlatform;
                                                    if (savedPlat && savedPlat !== 'none' && !connectedPlatforms.has(savedPlat)) {
                                                        const entry = NOTIFY_PLATFORM_ENTRIES.find((e) => e.value === savedPlat);
                                                        if (entry) {
                                                            opts.push({ ...entry, label: t('automations.disconnected', { label: entry.label }), disabled: true });
                                                        }
                                                    }
                                                    opts.push({ value: 'none', label: t('automations.dontSend') });
                                                    return opts.map((opt) => (
                                                        <SelectItem
                                                            key={opt.value || '__auto__'}
                                                            value={opt.value || '__auto__'}
                                                            className="text-xs"
                                                            disabled={opt.disabled}
                                                        >
                                                            {opt.label}
                                                        </SelectItem>
                                                    ));
                                                })()}
                                            </SelectContent>
                                        </Select>
                                    </div>
                                )}
                            </div>
                        ) : (
                            <dl className="space-y-4">
                                <div>
                                    <dt className="text-[11px] text-muted mb-1">{t('automations.description')}</dt>
                                    <dd className="text-ink break-words leading-relaxed">
                                        {currentPrompt ? (
                                            <MarkdownContent content={currentPrompt} />
                                        ) : (
                                            <span className="text-xs text-muted/60">{t('automations.noDescriptionConfigured')}</span>
                                        )}
                                    </dd>
                                </div>
                                <div>
                                    <dt className="text-[11px] text-muted mb-1">{t('automations.schedule')}</dt>
                                    <dd className="text-xs text-ink">
                                        {scheduleLabel}
                                        {job.scheduleEvidence && (
                                            <p className="text-[11px] text-muted mt-1">
                                                {t('automations.autoScheduledFrom', { evidence: job.scheduleEvidence })}
                                            </p>
                                        )}
                                        {/* Turning on a paused repeating schedule (or approving a
                                            proposal) waits for its next time instead of running at once. */}
                                        {!job.enabled && (job.scheduleKind === 'cron' || job.scheduleKind === 'every') && (
                                            <p className="text-[11px] text-muted mt-1">
                                                {t('automations.resumeWaitsForNextTime')}
                                            </p>
                                        )}
                                    </dd>
                                </div>
                                {currentWorkDir && (
                                    <div>
                                        <dt className="text-[11px] text-muted mb-1">{t('automations.workDirectoryLower')}</dt>
                                        <dd className="text-xs text-ink font-mono break-all" title={currentWorkDir}>
                                            {currentWorkDir}
                                        </dd>
                                    </div>
                                )}
                                <div className="flex items-center justify-between">
                                    <dt className="text-[11px] text-muted">{t('automations.completionNotificationLower')}</dt>
                                    <dd className="text-xs text-ink">
                                        {notifyEnabled ? t('automations.enabled') : t('automations.disabled')}
                                    </dd>
                                </div>
                                {job.notifyDm && (
                                    <div className="flex items-center justify-between">
                                        <dt className="text-[11px] text-muted">{t('automations.notificationPlatformLower')}</dt>
                                        <dd className="text-xs text-ink">{notifyPlatformLabel}</dd>
                                    </div>
                                )}
                                <div className="flex items-center justify-between">
                                    <dt className="text-[11px] text-muted">{t('automations.source')}</dt>
                                    <dd className="text-xs">
                                        {onNavigateToSourceSession ? (
                                            <button
                                                type="button"
                                                onClick={() => { recordTasksAction('C11', 'open', 'opened'); onNavigateToSourceSession(); }}
                                                className="inline-flex items-center gap-1 text-ink hover:text-accent transition-colors"
                                                title={sourceSessionLabel ? t('automations.goToSourceChatNamed', { name: sourceSessionLabel }) : t('automations.goToSourceChat')}
                                            >
                                                <span className="truncate max-w-[200px]">{sourceSessionLabel || t('automations.sourceChat')}</span>
                                                <ExternalLink className="h-3 w-3 shrink-0" />
                                            </button>
                                        ) : (
                                            <span className="text-muted">{platformLabel(job.sourcePlatform, t)}</span>
                                        )}
                                    </dd>
                                </div>
                            </dl>
                        )}
                    </section>
                </div>
            </div>
        </ScrollArea>
    );
});
