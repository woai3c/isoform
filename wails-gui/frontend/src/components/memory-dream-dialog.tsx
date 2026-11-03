import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Play, RotateCcw, Settings as SettingsIcon, History, Archive as ArchiveIcon, Sparkles, AlertTriangle, ArrowRight, Undo2 } from 'lucide-react';
import { DreamMoonIcon, DreamShieldIcon, DreamCheckIcon } from '@/components/icons/dream-icons';
import {
    Dialog,
    DialogCloseButton,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
} from '@/components/ui/dialog';
import { ScrollArea } from '@/components/ui/scroll-area';
import { notifyToast } from '@/lib/notify';
import { cn } from '@/lib/utils';
import {
    GetMemoryDreamStatus,
    GetMemoryDreamSettings,
    SaveMemoryDreamSettings,
    PreviewMemoryDream,
    RunMemoryDream,
    CancelMemoryDream,
    GetMemoryDreamRuns,
    ListArchivedMemories,
    RestoreArchivedMemory,
    UndoMemoryDreamRun,
} from '../../wailsjs/go/main/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import type { main } from '../../wailsjs/go/models';

interface MemoryDreamDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    /** Called when Dream completes a live run, so the memory list can refresh. */
    onRunCompleted?: () => void;
}

type DreamTab = 'overview' | 'settings' | 'history' | 'archive';

/**
 * MemoryDreamDialog — the user-facing surface for the nightly memory
 * consolidator. Modeled on the memory page's existing dialogs (full-screen
 * Dialog with internal tabs).
 *
 * Trust-building decisions:
 *   - The default tab is Overview, which shows safety guarantees and the
 *     last run summary. Users see "what Dream did and how it stayed safe"
 *     before any controls.
 *   - The big Run-Now button is intentionally a secondary action; Preview
 *     is the primary action. Even past the first two weeks (when settings
 *     defaults to dryRunOnly), a confirm dialog precedes destructive runs.
 *   - Every counter we show maps to a column in DreamRun so the user can
 *     compare history rows to live runs and detect drift.
 */
export function MemoryDreamDialog({ open, onOpenChange, onRunCompleted }: MemoryDreamDialogProps) {
    const { t } = useTranslation('memory');
    const [tab, setTab] = useState<DreamTab>('overview');
    const [status, setStatus] = useState<main.DreamStatus | null>(null);
    const [settings, setSettings] = useState<main.DreamSettings | null>(null);
    const [history, setHistory] = useState<main.DreamRun[]>([]);
    const [archive, setArchive] = useState<main.ArchivedMemory[]>([]);
    const [preview, setPreview] = useState<main.DreamPreview | null>(null);
    const [busyPreview, setBusyPreview] = useState(false);
    const [busyRun, setBusyRun] = useState(false);
    const [busyCancel, setBusyCancel] = useState(false);
    const cancelRequestedRef = useRef(false);
    const refreshVersionRef = useRef(0);
    const listsVersionRef = useRef(0);
    const runningRef = useRef(false);
    const [busyUndo, setBusyUndo] = useState<string | null>(null);
    const [busyRestore, setBusyRestore] = useState<string | null>(null);
    const [savingSettings, setSavingSettings] = useState(false);
    const [confirmingRun, setConfirmingRun] = useState(false);
    // Stable ref to onRunCompleted so the event subscription doesn't churn
    // every time the parent passes a new closure.
    const onRunCompletedRef = useRef(onRunCompleted);
    onRunCompletedRef.current = onRunCompleted;

    const refreshLists = useCallback(async () => {
        const version = ++listsVersionRef.current;
        try {
            const [h, a] = await Promise.all([
                GetMemoryDreamRuns(50),
                ListArchivedMemories(100),
            ]);
            if (version !== listsVersionRef.current) return;
            setHistory(h ?? []);
            setArchive(a ?? []);
        } catch (e) {
            console.error('[dream] list refresh failed', e);
        }
    }, []);

    const refreshStatus = useCallback(async () => {
        const version = ++refreshVersionRef.current;
        try {
            const next = await GetMemoryDreamStatus();
            if (version !== refreshVersionRef.current) return;
            const finished = runningRef.current && !next.running;
            runningRef.current = next.running;
            setStatus(next);
            if (finished) void refreshLists();
            if (!next.running) setBusyCancel(false);
        } catch (e) {
            console.error('[dream] status refresh failed', e);
        }
    }, [refreshLists]);

    const refreshAll = useCallback(async () => {
        await Promise.all([refreshStatus(), refreshLists()]);
    }, [refreshStatus, refreshLists]);

    // First open: load everything. Subsequent opens reuse cached state but
    // also kick a background refresh so the user never sees stale numbers.
    useEffect(() => {
        if (!open) return;
        void refreshAll();
        GetMemoryDreamSettings()
            .then(setSettings)
            .catch((e) => console.error('[dream] settings load failed', e));
    }, [open, refreshAll]);

    // Live progress: when the Go side emits memory-dream:* events we refresh
    // the overview so counters tick up in real time. Unsubscribes both on
    // close and on unmount.
    useEffect(() => {
        if (!open) return;
        const offStarted = EventsOn('memory-dream:started', () => void refreshStatus());
        const offProgress = EventsOn('memory-dream:progress', () => void refreshStatus());
        const offCompleted = EventsOn('memory-dream:completed', (...args: unknown[]) => {
            void refreshAll();
            const payload = args[0] as { dryRun?: boolean } | undefined;
            if (!payload?.dryRun) {
                onRunCompletedRef.current?.();
            }
        });
        return () => {
            offStarted();
            offProgress();
            offCompleted();
        };
    }, [open, refreshAll, refreshStatus]);

    // The completed event can arrive before the backend clears its running
    // flag. Scheduled cycles have no local request to trigger another refresh,
    // so keep checking until status confirms idle. Closing stops the timer.
    useEffect(() => {
        if (!open || !status?.running) return;
        let stopped = false;
        let timer: number;
        const poll = async () => {
            await refreshStatus();
            if (!stopped) timer = window.setTimeout(poll, 1000);
        };
        timer = window.setTimeout(poll, 1000);
        return () => {
            stopped = true;
            window.clearTimeout(timer);
        };
    }, [open, status?.running, refreshStatus]);

    const handlePreview = useCallback(async () => {
        cancelRequestedRef.current = false;
        setBusyPreview(true);
        setPreview(null);
        notifyToast(t('dream.title'), t('dream.toasts.previewStarted'), 'info');
        try {
            const p = await PreviewMemoryDream();
            setPreview(p);
        } catch (e) {
            if (!cancelRequestedRef.current) {
                notifyToast(t('dream.title'), t('dream.toasts.previewFailed', { error: String(e) }), 'error');
            }
        } finally {
            setBusyPreview(false);
            void refreshAll();
        }
    }, [refreshAll, t]);

    const handleRun = useCallback(async () => {
        setConfirmingRun(false);
        cancelRequestedRef.current = false;
        setBusyRun(true);
        notifyToast(t('dream.title'), t('dream.toasts.runStarted'), 'info');
        try {
            await RunMemoryDream();
        } catch (e) {
            if (!cancelRequestedRef.current) {
                notifyToast(t('dream.title'), t('dream.toasts.runFailed', { error: String(e) }), 'error');
            }
        } finally {
            setBusyRun(false);
            void refreshAll();
            onRunCompletedRef.current?.();
        }
    }, [refreshAll, t]);

    const handleCancel = useCallback(async () => {
        cancelRequestedRef.current = true;
        setBusyCancel(true);
        try {
            await CancelMemoryDream();
            // Cancellation is best-effort. Wait for the real status or the
            // completed event before offering another run.
            await refreshStatus();
        } catch (e) {
            cancelRequestedRef.current = false;
            setBusyCancel(false);
            notifyToast(t('dream.title'), t('dream.toasts.cancelFailed', { error: String(e) }), 'error');
        }
    }, [refreshStatus, t]);

    const handleSaveSettings = useCallback(
        async (next: main.DreamSettings) => {
            setSavingSettings(true);
            try {
                const saved = await SaveMemoryDreamSettings(next);
                setSettings(saved);
                notifyToast(t('dream.title'), t('dream.toasts.settingsSaved'), 'success');
            } catch (e) {
                notifyToast(t('dream.title'), t('dream.toasts.settingsFailed', { error: String(e) }), 'error');
            } finally {
                setSavingSettings(false);
            }
        },
        [t],
    );

    const handleUndo = useCallback(
        async (runId: string) => {
            setBusyUndo(runId);
            try {
                const count = await UndoMemoryDreamRun(runId);
                notifyToast(t('dream.title'), t('dream.toasts.undoSuccess', { count }), 'success');
                onRunCompletedRef.current?.();
            } catch (e) {
                notifyToast(t('dream.title'), t('dream.toasts.undoFailed', { error: String(e) }), 'error');
            } finally {
                setBusyUndo(null);
                void refreshAll();
            }
        },
        [refreshAll, t],
    );

    const handleRestore = useCallback(
        async (archiveId: string) => {
            setBusyRestore(archiveId);
            try {
                await RestoreArchivedMemory(archiveId);
                notifyToast(t('dream.title'), t('dream.archive.restoreSuccess'), 'success');
                onRunCompletedRef.current?.();
            } catch (e) {
                notifyToast(t('dream.title'), t('dream.archive.restoreFailed', { error: String(e) }), 'error');
            } finally {
                setBusyRestore(null);
                void refreshAll();
            }
        },
        [refreshAll, t],
    );

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            {/*
              Override the base DialogContent width (520px) to give the
              four-tab layout enough room — at 520px the tabs cramp and
              the safety panel's longer sentences double-wrap on every
              line. 760px is wide enough for any single sentence to land
              on two lines max; `max-w-[92vw]` keeps it sane on small
              displays.
            */}
            <DialogContent className="w-[760px] max-w-[92vw] max-h-[90vh] flex flex-col p-0 overflow-hidden">
                {/*
                  DialogHeader is `flex items-center justify-between` (it expects
                  title-block | close-button), so we wrap Title + Description in a
                  vertical-stack div. Without this wrapper the three children get
                  distributed horizontally and the CJK title text wraps to one
                  character per line.
                */}
                <DialogHeader className="px-6 pt-5 pb-4 border-b border-border/60 items-start">
                    <div className="flex-1 min-w-0 flex flex-col gap-1.5">
                        <DialogTitle className="flex items-center gap-2 text-base font-semibold text-ink">
                            {/*
                              Custom warm moon SVG instead of lucide's blue
                              line glyph — this is the brand mark for Dream,
                              so it earns the extra design weight.
                            */}
                            <DreamMoonIcon className="h-6 w-6 shrink-0" />
                            {t('dream.title')}
                        </DialogTitle>
                        <DialogDescription className="text-xs text-muted leading-relaxed pr-2">
                            {t('dream.description')}
                        </DialogDescription>
                    </div>
                    <DialogCloseButton />
                </DialogHeader>

                <DreamTabBar tab={tab} onSelect={setTab} />

                <ScrollArea className="flex-1">
                    <div className="px-6 py-4">
                        {tab === 'overview' && (
                            <DreamOverview
                                status={status}
                                preview={preview}
                                busyPreview={busyPreview}
                                busyRun={busyRun}
                                busyCancel={busyCancel}
                                onCancel={handleCancel}
                                onPreview={handlePreview}
                                onRequestRun={() => setConfirmingRun(true)}
                            />
                        )}
                        {tab === 'settings' && settings && (
                            <DreamSettingsTab
                                settings={settings}
                                saving={savingSettings}
                                onChange={setSettings}
                                onSave={() => settings && handleSaveSettings(settings)}
                            />
                        )}
                        {tab === 'history' && (
                            <DreamHistoryTab
                                history={history}
                                onUndo={handleUndo}
                                busyUndo={busyUndo}
                            />
                        )}
                        {tab === 'archive' && (
                            <DreamArchiveTab
                                archive={archive}
                                onRestore={handleRestore}
                                busyRestore={busyRestore}
                            />
                        )}
                    </div>
                </ScrollArea>
            </DialogContent>

            <ConfirmRunDialog
                open={confirmingRun}
                onOpenChange={setConfirmingRun}
                onConfirm={handleRun}
                preview={preview}
            />
        </Dialog>
    );
}

/** Tab strip at the top of the dialog. */
function DreamTabBar({ tab, onSelect }: { tab: DreamTab; onSelect: (t: DreamTab) => void }) {
    const { t } = useTranslation('memory');
    const tabs: Array<{ key: DreamTab; label: string; icon: React.ComponentType<{ className?: string }> }> = [
        { key: 'overview', label: t('dream.tabs.overview'), icon: Sparkles },
        { key: 'settings', label: t('dream.tabs.settings'), icon: SettingsIcon },
        { key: 'history', label: t('dream.tabs.history'), icon: History },
        { key: 'archive', label: t('dream.tabs.archive'), icon: ArchiveIcon },
    ];
    // Pill-style tabs over the previous heavy `border-b-2 border-accent`
    // underline:
    //   - The accent-colored underline clashed visually with everything
    //     else (the dialog has very little chrome elsewhere).
    //   - Per project rule, active state is conveyed by a rounded background
    //     highlight, never a left/bottom accent bar.
    return (
        <div className="flex items-center gap-1 px-4 pt-3 pb-2 border-b border-border/60">
            {tabs.map(({ key, label, icon: Icon }) => (
                <button
                    key={key}
                    type="button"
                    onClick={() => onSelect(key)}
                    className={cn(
                        'flex items-center gap-1.5 px-2.5 py-1.5 text-xs rounded-md transition-colors',
                        tab === key
                            ? 'text-ink font-semibold bg-ink/[0.06]'
                            : 'text-muted font-medium hover:text-ink hover:bg-ink/[0.04]',
                    )}
                >
                    <Icon className="h-3.5 w-3.5" />
                    {label}
                </button>
            ))}
        </div>
    );
}

function DreamOverview({
    status,
    preview,
    busyPreview,
    busyRun,
    busyCancel,
    onCancel,
    onPreview,
    onRequestRun,
}: {
    status: main.DreamStatus | null;
    preview: main.DreamPreview | null;
    busyPreview: boolean;
    busyRun: boolean;
    busyCancel: boolean;
    onCancel: () => void;
    onPreview: () => void;
    onRequestRun: () => void;
}) {
    const { t } = useTranslation('memory');
    const lastRun = status?.lastRun;
    const archiveCount = status?.archiveCount ?? 0;
    const nextRunStr = useMemo(() => formatRelativeTime(status?.nextRunMs), [status?.nextRunMs]);
    const lastRunStr = useMemo(() => (lastRun?.endedAt ? formatRelativeTime(lastRun.endedAt) : ''), [lastRun?.endedAt]);

    return (
        <div className="space-y-4">
            {/* Status strip — left side states the current cycle status,
                right side surfaces the two quantities the user actually
                cares about ("when next?" and "how full is the archive?")
                as a small inline stat group. The status dot doubles as the
                animation focal point when a cycle is in flight. */}
            <div className="flex items-start justify-between gap-4 rounded-xl border border-border/60 bg-panel/50 px-4 py-3.5">
                <div className="flex items-start gap-3 min-w-0">
                    <div className="mt-1 relative shrink-0">
                        <div className={cn('h-2 w-2 rounded-full', status?.running ? 'bg-accent' : 'bg-muted/40')} />
                        {status?.running && (
                            <div className="absolute inset-0 h-2 w-2 rounded-full bg-accent/50 animate-ping" />
                        )}
                    </div>
                    <div className="min-w-0">
                        <div className="text-sm font-medium text-ink">
                            {status?.running ? t('dream.status.running') : t('dream.status.idle')}
                        </div>
                        <div className="text-xs text-muted leading-relaxed">
                            {lastRun
                                ? t('dream.status.lastRun', { when: lastRunStr })
                                : t('dream.status.noRunYet')}
                        </div>
                    </div>
                </div>
                <div className="flex flex-col items-end gap-1 shrink-0 text-right">
                    <div className="text-[11px] text-muted">{t('dream.status.nextRun', { when: nextRunStr })}</div>
                    <div className="text-[11px] text-muted">{t('dream.status.archiveCount', { count: archiveCount })}</div>
                </div>
            </div>

            {/*
              Catch-up hint — explains the "what if my machine is off
              at 3am?" question that most users will hit by default,
              since 03:00 is the default DailyTime. Sits as a small
              italic footnote under the status strip so it answers the
              question right where users see the schedule, not buried
              in Settings. Kept low-contrast so it doesn't compete with
              the status line above.
            */}
            <p className="-mt-2 px-1 text-[11px] italic text-muted/80 leading-relaxed">
                {t('dream.status.catchupHint')}
            </p>

            {/* Action row */}
            <div className="flex flex-wrap items-center gap-2">
                <button
                    type="button"
                    onClick={onPreview}
                    disabled={busyPreview || status?.running}
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium patina-verdigris bg-solid-btn-bg text-solid-btn-fg shadow-[var(--shadow-solid-btn)] hover:opacity-90 transition-colors disabled:opacity-50"
                >
                    {busyPreview ? <Loader2 className="h-3 w-3 animate-spin" /> : <Sparkles className="h-3 w-3" />}
                    {busyPreview ? t('dream.actions.previewing') : t('dream.actions.preview')}
                </button>
                <button
                    type="button"
                    onClick={onRequestRun}
                    disabled={busyRun || status?.running}
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium border border-border text-ink hover:bg-border/30 transition-colors disabled:opacity-50"
                >
                    {busyRun ? <Loader2 className="h-3 w-3 animate-spin" /> : <Play className="h-3 w-3" />}
                    {busyRun ? t('dream.actions.running') : t('dream.actions.commit')}
                </button>
                {status?.running && (
                    <button
                        type="button"
                        onClick={onCancel}
                        disabled={busyCancel}
                        className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium border border-border text-ink hover:bg-border/30 transition-colors disabled:opacity-50"
                    >
                        {busyCancel && <Loader2 className="h-3 w-3 animate-spin" />}
                        {busyCancel ? t('dream.actions.cancelling') : t('dream.actions.cancel')}
                    </button>
                )}
            </div>

            {/* Preview report */}
            {preview && <PreviewReport preview={preview} />}

            {/* Safety section: visible by default so trust builds before any
                destructive action runs. */}
            <SafetyPanel />
        </div>
    );
}

function PreviewReport({ preview }: { preview: main.DreamPreview }) {
    const { t } = useTranslation('memory');
    const archive = preview.archiveProposed ?? [];
    const synth = preview.synthesisProposed ?? [];

    if (preview.skipped) {
        return (
            <div className="rounded-lg border border-border/60 bg-panel/40 p-4 text-xs text-muted">
                {t('dream.preview.skippedMessage', { message: preview.skipped })}
            </div>
        );
    }
    if (archive.length === 0 && synth.length === 0) {
        return (
            <div className="rounded-lg border border-border/60 bg-panel/40 p-4 text-xs text-muted">
                {t('dream.preview.empty')}
            </div>
        );
    }

    return (
        <div className="space-y-3">
            <div className="rounded-lg border border-accent/30 bg-accent/5 p-3 text-xs text-ink">
                <div className="font-medium mb-1">{t('dream.preview.heading')}</div>
                <div className="space-y-0.5 text-muted">
                    <div>{t('dream.preview.examined', { count: preview.examined })}</div>
                    {archive.length > 0 && <div>{t('dream.preview.summary', { count: archive.length })}</div>}
                    {synth.length > 0 && <div>{t('dream.preview.synthSummary', { count: synth.length })}</div>}
                    {preview.oversizedClusters > 0 && (
                        <div className="text-warning">
                            {t('dream.preview.oversizedSkipped', { count: preview.oversizedClusters })}
                        </div>
                    )}
                    {preview.rejectedSyntheses > 0 && (
                        <div>{t('dream.preview.rejectedSyntheses', { count: preview.rejectedSyntheses })}</div>
                    )}
                </div>
            </div>

            {archive.length > 0 && <ArchiveProposalsTable proposals={archive} />}
            {synth.length > 0 && <SynthesisProposalsTable proposals={synth} />}
        </div>
    );
}

function ArchiveProposalsTable({ proposals }: { proposals: main.DreamArchiveProposal[] }) {
    const { t } = useTranslation('memory');
    return (
        <div className="rounded-lg border border-border/60 overflow-hidden">
            <div className="px-3 py-2 bg-panel/40 border-b border-border/50 text-xs font-medium text-ink">
                {t('dream.preview.archiveTable')}
            </div>
            <div className="divide-y divide-border/40">
                {proposals.map((p) => (
                    <div key={p.id} className="px-3 py-2 text-xs flex items-start gap-2">
                        <div className="flex-1 min-w-0">
                            <div className="truncate font-medium text-ink">{p.title || p.id}</div>
                            <div className="text-muted text-[10px] font-mono">{p.id}</div>
                        </div>
                        <div className="shrink-0 text-muted">{reasonLabel((k) => t(k as 'dream.preview.reasonExactDup'), p.reason)}</div>
                        {p.mergedInto && (
                            <div className="shrink-0 text-muted text-[10px] font-mono flex items-center gap-1">
                                <ArrowRight className="h-3 w-3" />
                                {p.mergedInto}
                            </div>
                        )}
                    </div>
                ))}
            </div>
        </div>
    );
}

function SynthesisProposalsTable({ proposals }: { proposals: main.DreamSynthesisProposal[] }) {
    const { t } = useTranslation('memory');
    return (
        <div className="rounded-lg border border-border/60 overflow-hidden">
            <div className="px-3 py-2 bg-panel/40 border-b border-border/50 text-xs font-medium text-ink">
                {t('dream.preview.synthesisTable')}
            </div>
            <div className="divide-y divide-border/40">
                {proposals.map((p, i) => (
                    <div key={i} className="px-3 py-3 text-xs space-y-1">
                        <div className="font-medium text-ink">{p.title}</div>
                        <div className="text-muted text-[10px]">
                            {p.dimension} ·{' '}
                            {(p.sourceIds ?? []).map((s, j) => (
                                <span key={s} className="font-mono">
                                    {s}
                                    {j < (p.sourceIds?.length ?? 0) - 1 ? ', ' : ''}
                                </span>
                            ))}
                        </div>
                        <pre className="text-[11px] text-ink whitespace-pre-wrap font-sans bg-panel/40 rounded p-2">{p.content}</pre>
                    </div>
                ))}
            </div>
        </div>
    );
}

/**
 * reasonLabel maps a Go-side ArchiveReason value to its translated label.
 * Defined as a generic untyped helper so the i18next type augmentation
 * doesn't try to unify across both useTranslation overloads — that path
 * triggers TS2589 ("excessively deep type instantiation"). The runtime
 * cost of the `as` cast is zero.
 */
function reasonLabel(t: (key: string) => unknown, reason: string): string {
    switch (reason) {
        case 'exact_dup':
            return String(t('dream.preview.reasonExactDup'));
        case 'expired':
            return String(t('dream.preview.reasonExpired'));
        case 'similarity_merge':
            return String(t('dream.preview.reasonSimilarityMerge'));
        case 'llm_merge':
            return String(t('dream.preview.reasonLlmMerge'));
        default:
            return reason;
    }
}

// SafetyPanel replaces the original bullet-list ("disc + indent") with
// a checkmark-led layout. Each guarantee gets its own line led by a
// small accent-colored check chip; that turns a wall of dense paragraph
// text into a scannable list of commitments. The shield icon sits in
// the section header as the lone "this is the trust section" marker so
// the items themselves stay visually quiet.
function SafetyPanel() {
    const { t } = useTranslation('memory');
    const items = t('dream.safety.items', { returnObjects: true }) as string[];
    return (
        <div className="rounded-lg border border-border/60 bg-panel/40 px-4 py-3.5 space-y-3">
            <div className="flex items-center gap-2 text-xs font-semibold text-ink">
                {/* Custom shield mark — warm terracotta with cream check,
                    not lucide's flat ShieldCheck. */}
                <DreamShieldIcon className="h-4 w-4 shrink-0" />
                {t('dream.safety.title')}
            </div>
            <ul className="space-y-2">
                {(Array.isArray(items) ? items : []).map((item, idx) => (
                    <li key={idx} className="flex items-start gap-2 text-xs leading-relaxed text-muted">
                        <DreamCheckIcon className="mt-0.5 h-4 w-4 shrink-0" />
                        <span>{item}</span>
                    </li>
                ))}
            </ul>
        </div>
    );
}

function DreamSettingsTab({
    settings,
    saving,
    onChange,
    onSave,
}: {
    settings: main.DreamSettings;
    saving: boolean;
    onChange: (next: main.DreamSettings) => void;
    onSave: () => void;
}) {
    const { t } = useTranslation('memory');
    // Local field update helper. We mutate by replacing the object so the
    // parent's state setter sees a new reference.
    const patch = (k: keyof main.DreamSettings, v: unknown) =>
        onChange({ ...settings, [k]: v } as main.DreamSettings);

    return (
        <div className="space-y-4 max-w-2xl">
            <CheckboxRow
                checked={settings.enabled}
                onChange={(v) => patch('enabled', v)}
                label={t('dream.settings.enabled')}
                help={t('dream.settings.enabledHelp')}
            />
            <CheckboxRow
                checked={settings.dryRunOnly}
                onChange={(v) => patch('dryRunOnly', v)}
                label={t('dream.settings.dryRunOnly')}
                help={t('dream.settings.dryRunOnlyHelp')}
            />
            <FieldRow label={t('dream.settings.dailyTime')} help={t('dream.settings.dailyTimeHelp')}>
                <input
                    type="time"
                    value={settings.dailyTime}
                    onChange={(e) => patch('dailyTime', e.target.value)}
                    className="px-2 py-1 text-xs rounded-md border border-border bg-panel/40 text-ink"
                />
            </FieldRow>
            <FieldRow label={t('dream.settings.archiveRetentionDays')} help={t('dream.settings.archiveRetentionDaysHelp')}>
                <input
                    type="number"
                    min={1}
                    max={365}
                    value={settings.archiveRetentionDays}
                    onChange={(e) => patch('archiveRetentionDays', parseInt(e.target.value, 10) || 30)}
                    className="w-24 px-2 py-1 text-xs rounded-md border border-border bg-panel/40 text-ink"
                />
            </FieldRow>

            <CheckboxRow
                checked={settings.llmEnabled}
                onChange={(v) => patch('llmEnabled', v)}
                label={t('dream.settings.llmEnabled')}
                help={t('dream.settings.llmEnabledHelp')}
            />

            <details className="rounded-lg border border-border/60 bg-panel/40 p-3">
                <summary className="text-xs font-medium text-ink cursor-pointer">{t('dream.settings.advanced')}</summary>
                <div className="mt-3 space-y-3 text-xs text-muted">
                    <div>{t('dream.settings.advancedHint')}</div>
                    <FieldRow label={t('dream.settings.similarityThreshold')} help={t('dream.settings.similarityThresholdHelp')}>
                        <input
                            type="number"
                            step={0.01}
                            min={0.7}
                            max={0.999}
                            value={settings.similarityMergeThreshold}
                            onChange={(e) => patch('similarityMergeThreshold', parseFloat(e.target.value) || 0.95)}
                            className="w-24 px-2 py-1 text-xs rounded-md border border-border bg-panel text-ink"
                        />
                    </FieldRow>
                    <FieldRow label={t('dream.settings.llmMergeLow')} help={t('dream.settings.llmMergeLowHelp')}>
                        <input
                            type="number"
                            step={0.01}
                            min={0.6}
                            max={0.95}
                            value={settings.llmMergeLow}
                            onChange={(e) => patch('llmMergeLow', parseFloat(e.target.value) || 0.8)}
                            className="w-24 px-2 py-1 text-xs rounded-md border border-border bg-panel text-ink"
                        />
                    </FieldRow>
                    <FieldRow label={t('dream.settings.llmBatchSize')} help={t('dream.settings.llmBatchSizeHelp')}>
                        <input
                            type="number"
                            min={2}
                            max={32}
                            value={settings.llmBatchSize}
                            onChange={(e) => patch('llmBatchSize', parseInt(e.target.value, 10) || 8)}
                            className="w-24 px-2 py-1 text-xs rounded-md border border-border bg-panel text-ink"
                        />
                    </FieldRow>
                </div>
            </details>

            <div className="pt-2">
                <button
                    type="button"
                    onClick={onSave}
                    disabled={saving}
                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium patina-verdigris bg-solid-btn-bg text-solid-btn-fg shadow-[var(--shadow-solid-btn)] hover:opacity-90 transition-colors disabled:opacity-50"
                >
                    {saving && <Loader2 className="h-3 w-3 animate-spin" />}
                    {saving ? t('dream.actions.saving') : t('dream.actions.saveSettings')}
                </button>
            </div>
        </div>
    );
}

function FieldRow({ label, help, children }: { label: string; help: string; children: React.ReactNode }) {
    return (
        <div className="flex items-start justify-between gap-4 py-2 border-b border-border/30">
            <div className="flex-1 min-w-0">
                <div className="text-xs font-medium text-ink">{label}</div>
                <div className="text-[11px] text-muted mt-0.5 leading-relaxed">{help}</div>
            </div>
            <div className="shrink-0">{children}</div>
        </div>
    );
}

function CheckboxRow({
    checked,
    onChange,
    label,
    help,
}: {
    checked: boolean;
    onChange: (v: boolean) => void;
    label: string;
    help: string;
}) {
    return (
        <label className="flex items-start gap-3 py-2 border-b border-border/30 cursor-pointer">
            <input
                type="checkbox"
                checked={checked}
                onChange={(e) => onChange(e.target.checked)}
                className="mt-0.5 h-3.5 w-3.5 rounded border-border accent-accent"
            />
            <div className="flex-1 min-w-0">
                <div className="text-xs font-medium text-ink">{label}</div>
                <div className="text-[11px] text-muted mt-0.5 leading-relaxed">{help}</div>
            </div>
        </label>
    );
}

function DreamHistoryTab({
    history,
    onUndo,
    busyUndo,
}: {
    history: main.DreamRun[];
    onUndo: (runId: string) => void;
    busyUndo: string | null;
}) {
    const { t } = useTranslation('memory');
    if (history.length === 0) {
        return <div className="text-xs text-muted py-8 text-center">{t('dream.history.empty')}</div>;
    }
    return (
        <div className="space-y-2">
            {history.map((run) => (
                <RunRow key={run.id} run={run} onUndo={onUndo} busyUndo={busyUndo === run.id} />
            ))}
        </div>
    );
}

function RunRow({ run, onUndo, busyUndo }: { run: main.DreamRun; onUndo: (runId: string) => void; busyUndo: boolean }) {
    const { t } = useTranslation('memory');
    const startedAt = run.startedAt ? formatRelativeTime(run.startedAt) : '';
    const counters: Array<{ label: string; value: number }> = [
        { label: t('dream.history.counters.examined'), value: run.examined ?? 0 },
        { label: t('dream.history.counters.exact'), value: run.archivedExact ?? 0 },
        { label: t('dream.history.counters.expired'), value: run.archivedExpired ?? 0 },
        { label: t('dream.history.counters.similarity'), value: run.archivedSimilarity ?? 0 },
        { label: t('dream.history.counters.llm'), value: run.archivedLlm ?? 0 },
        { label: t('dream.history.counters.synthesized'), value: run.synthesizedCreated ?? 0 },
        { label: t('dream.history.counters.oversized'), value: run.oversizedClusters ?? 0 },
        { label: t('dream.history.counters.rejected'), value: run.rejectedSyntheses ?? 0 },
    ];
    const totalArchived = (run.archivedExact ?? 0) + (run.archivedExpired ?? 0) + (run.archivedSimilarity ?? 0) + (run.archivedLlm ?? 0);
    return (
        <div className="rounded-lg border border-border/60 bg-panel/40 p-3 space-y-2">
            <div className="flex items-center justify-between gap-2">
                <div className="flex items-center gap-2 flex-wrap">
                    <span
                        className={cn(
                            'text-[10px] px-1.5 py-0.5 rounded font-medium',
                            run.status === 'completed' && 'bg-success-bg text-success dark:text-success',
                            run.status === 'failed' && 'bg-error-bg text-error dark:text-error',
                            run.status === 'cancelled' && 'bg-muted/20 text-muted',
                            run.status === 'running' && 'bg-accent/20 text-accent',
                        )}
                    >
                        {t(`dream.history.status.${run.status}` as never)}
                    </span>
                    <span className="text-[10px] text-muted">{t(`dream.history.trigger.${run.trigger}` as never)}</span>
                    {run.dryRun && <span className="text-[10px] px-1.5 py-0.5 rounded bg-panel border border-border text-muted">{t('dream.history.dryRunBadge')}</span>}
                    <span className="text-[10px] text-muted">{startedAt}</span>
                    <span className="text-[10px] font-mono text-muted truncate max-w-[180px]">{run.id}</span>
                </div>
                {!run.dryRun && totalArchived > 0 && run.status === 'completed' && (
                    <button
                        type="button"
                        onClick={() => onUndo(run.id)}
                        disabled={busyUndo}
                        className="inline-flex items-center gap-1 px-2 py-1 rounded-md text-[11px] border border-border text-ink hover:bg-border/30 transition-colors disabled:opacity-50"
                    >
                        {busyUndo ? <Loader2 className="h-3 w-3 animate-spin" /> : <Undo2 className="h-3 w-3" />}
                        {busyUndo ? t('dream.actions.undoing') : t('dream.actions.undo')}
                    </button>
                )}
            </div>
            <div className="flex flex-wrap gap-2 text-[10px]">
                {counters
                    .filter((c) => c.value > 0)
                    .map((c) => (
                        <span key={c.label} className="px-1.5 py-0.5 rounded bg-panel border border-border/50 text-muted">
                            <span className="text-ink font-medium">{c.value}</span> {c.label}
                        </span>
                    ))}
            </div>
            {run.error && (
                <div className="text-[11px] text-error">{run.error}</div>
            )}
        </div>
    );
}

function DreamArchiveTab({
    archive,
    onRestore,
    busyRestore,
}: {
    archive: main.ArchivedMemory[];
    onRestore: (archiveId: string) => void;
    busyRestore: string | null;
}) {
    const { t } = useTranslation('memory');
    if (archive.length === 0) {
        return <div className="text-xs text-muted py-8 text-center">{t('dream.archive.empty')}</div>;
    }
    return (
        <div className="space-y-3">
            <div
                className="text-[11px] text-muted"
                dangerouslySetInnerHTML={{ __html: t('dream.archive.subtitle') }}
            />
            <div className="rounded-lg border border-border/60 overflow-hidden">
                <div className="grid grid-cols-[1fr_120px_140px_140px_80px] gap-2 px-3 py-2 bg-panel/40 border-b border-border/50 text-[10px] font-medium uppercase text-muted">
                    <div>{t('dream.archive.table.title')}</div>
                    <div>{t('dream.archive.table.reason')}</div>
                    <div>{t('dream.archive.table.archivedAt')}</div>
                    <div>{t('dream.archive.table.mergedInto')}</div>
                    <div className="text-right">{t('dream.archive.table.actions')}</div>
                </div>
                <div className="divide-y divide-border/40">
                    {archive.map((a) => (
                        <div key={a.archiveId} className="grid grid-cols-[1fr_120px_140px_140px_80px] gap-2 px-3 py-2 text-xs items-center">
                            <div className="min-w-0">
                                <div className="truncate font-medium text-ink">{a.title || a.originalId}</div>
                                <div className="text-[10px] text-muted font-mono truncate">{a.originalId}</div>
                            </div>
                            <div className="text-muted">{reasonLabel((k) => t(k as 'dream.preview.reasonExactDup'), a.reason)}</div>
                            <div className="text-muted text-[11px]">{formatRelativeTime(a.archivedAt)}</div>
                            <div className="text-muted text-[10px] font-mono truncate">{a.mergedInto || '—'}</div>
                            <div className="text-right">
                                <button
                                    type="button"
                                    onClick={() => onRestore(a.archiveId)}
                                    disabled={busyRestore === a.archiveId}
                                    className="inline-flex items-center gap-1 px-2 py-1 rounded-md text-[11px] border border-border text-ink hover:bg-border/30 transition-colors disabled:opacity-50"
                                >
                                    {busyRestore === a.archiveId ? (
                                        <Loader2 className="h-3 w-3 animate-spin" />
                                    ) : (
                                        <RotateCcw className="h-3 w-3" />
                                    )}
                                    {busyRestore === a.archiveId ? t('dream.actions.restoring') : t('dream.actions.restore')}
                                </button>
                            </div>
                        </div>
                    ))}
                </div>
            </div>
        </div>
    );
}

function ConfirmRunDialog({
    open,
    onOpenChange,
    onConfirm,
    preview,
}: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onConfirm: () => void;
    preview: main.DreamPreview | null;
}) {
    const { t } = useTranslation('memory');
    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="w-[400px]">
                <DialogHeader>
                    <DialogTitle className="flex items-center gap-2 text-sm">
                        <AlertTriangle className="h-4 w-4 shrink-0 text-warning" />
                        {t('dream.actions.commit')}
                    </DialogTitle>
                    <DialogCloseButton />
                </DialogHeader>
                <div className="px-6 py-4">
                    <DialogDescription className="text-[13px] text-muted leading-relaxed">
                        {preview && (preview.archiveProposed ?? []).length > 0
                            ? t('dream.preview.summary', { count: (preview.archiveProposed ?? []).length })
                            : t('dream.description')}
                    </DialogDescription>
                </div>
                <div className="flex items-center justify-end gap-2 px-6 pb-5">
                    <button
                        type="button"
                        onClick={() => onOpenChange(false)}
                        className="px-3 py-1.5 rounded-md text-xs font-medium text-ink bg-border/50 hover:bg-border transition-colors"
                    >
                        {t('dream.actions.close')}
                    </button>
                    <button
                        type="button"
                        onClick={onConfirm}
                        className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium patina-verdigris bg-solid-btn-bg text-solid-btn-fg shadow-[var(--shadow-solid-btn)] hover:opacity-90 transition-colors"
                    >
                        <Play className="h-3 w-3" />
                        {t('dream.actions.commit')}
                    </button>
                </div>
            </DialogContent>
        </Dialog>
    );
}

/**
 * formatRelativeTime renders a unix-ms timestamp in a friendly local form.
 * 0 / undefined → empty string so callers can fall through to a placeholder.
 *
 * We chose "today / yesterday / weekday / date" over "x minutes ago" because
 * the cycle is daily-cadence — minute-level precision is noise.
 */
function formatRelativeTime(ms: number | undefined): string {
    if (!ms || ms <= 0) return '';
    const date = new Date(ms);
    const now = new Date();
    const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
    const yesterday = new Date(today.getTime() - 86400000);
    const tomorrow = new Date(today.getTime() + 86400000);
    const hh = String(date.getHours()).padStart(2, '0');
    const mm = String(date.getMinutes()).padStart(2, '0');
    if (date >= today && date < tomorrow) return `Today ${hh}:${mm}`;
    if (date >= yesterday && date < today) return `Yesterday ${hh}:${mm}`;
    if (date >= tomorrow && date < new Date(today.getTime() + 2 * 86400000)) return `Tomorrow ${hh}:${mm}`;
    return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')} ${hh}:${mm}`;
}
