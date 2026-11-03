import { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Clock, Loader2, MoreHorizontal, Pause, Pencil, Play, Trash2 } from 'lucide-react';
import { CronJobDetail, type CronJobDetailHandle } from './CronJobDetail';
import { createScheduleTextLabels, cronToHumanReadable, type ScheduleTextLabels } from './schedule-utils';
import { ConfirmDialog } from '@/components/confirm-dialog';
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { cn } from '@/lib/utils';
import type { CronJobState, CronRunState } from '@/hooks/useCronJobs';
import type { StreamItem } from '@/types';
import {
    Dialog,
    DialogContent,
    DialogHeader,
    DialogTitle,
    DialogDescription,
    DialogCloseButton,
} from '@/components/ui/dialog';

interface AutomationDetailDialogProps {
    job: CronJobState | null;
    open: boolean;
    onOpenChange: (open: boolean) => void;
    sourceSessionLabel?: string;
    onNavigateToSourceSession?: () => void;
    onToggleEnabled: (id: string, enabled: boolean) => void;
    onDelete: (id: string) => void;
    onRunNow: (id: string) => void;
    onUpdate: (id: string, patch: Record<string, unknown>) => Promise<void>;
    loadRuns: (jobId: string) => Promise<CronRunState[]>;
    loadRunLogs: (jobId: string, runId: string) => Promise<StreamItem[]>;
}

function getScheduleLabel(job: CronJobState, runOnDemand: string, labels: ScheduleTextLabels): string {
    if (job.scheduleKind === 'manual') return runOnDemand;
    const cronExpr = job.scheduleKind === 'cron'
        ? (job.scheduleConfig as Record<string, unknown> | null)?.expr as string | undefined
        : undefined;
    return (cronExpr ? cronToHumanReadable(cronExpr, labels) : '') || job.scheduleDescription || job.scheduleKind;
}

function formatNextRun(ms: number): string {
    if (!ms) return '';
    return new Date(ms).toLocaleString([], {
        month: 'short', day: 'numeric',
        hour: '2-digit', minute: '2-digit',
    });
}

export function AutomationDetailDialog({
    job,
    open,
    onOpenChange,
    sourceSessionLabel,
    onNavigateToSourceSession,
    onToggleEnabled,
    onDelete,
    onRunNow,
    onUpdate,
    loadRuns,
    loadRunLogs,
}: AutomationDetailDialogProps) {
    const { t } = useTranslation('app');
    const detailRef = useRef<CronJobDetailHandle>(null);
    const [editing, setEditing] = useState(false);
    const [saving, setSaving] = useState(false);
    const [confirmDelete, setConfirmDelete] = useState(false);

    if (!job) return null;

    const scheduleLabel = getScheduleLabel(job, t('automations.runOnDemand'), createScheduleTextLabels(t));

    return (
        <>
            <Dialog open={open} onOpenChange={onOpenChange}>
                <DialogContent className="w-[800px] h-[80vh] max-h-none overflow-hidden">
                    <DialogHeader>
                        <div className="flex items-center gap-3 min-w-0 flex-1">
                            <div className="shrink-0 flex items-center justify-center w-9 h-9 rounded-lg bg-panel border border-border/60 text-muted">
                                <Clock className="h-4 w-4" strokeWidth={1.75} />
                            </div>
                            <div className="min-w-0">
                                <DialogTitle className="text-sm truncate">
                                    {job.name || t('automations.untitled')}
                                </DialogTitle>
                                <DialogDescription className="text-[11px] mt-0.5 truncate flex items-center gap-1.5">
                                    <span className={cn('inline-block w-1.5 h-1.5 rounded-full shrink-0', job.enabled ? 'bg-automation' : 'bg-muted')} />
                                    <span className="truncate">{!job.enabled ? t('automations.paused') : scheduleLabel}</span>
                                    {job.enabled && job.nextRunAt > 0 && (
                                        <>
                                            <span className="text-muted/40 shrink-0">·</span>
                                            <span className="text-muted shrink-0">{t('automations.nextAt', { time: formatNextRun(job.nextRunAt) })}</span>
                                        </>
                                    )}
                                </DialogDescription>
                            </div>
                        </div>
                        <div className="flex items-center gap-2 shrink-0">
                            {editing ? (
                                <>
                                    <button
                                        type="button"
                                        onClick={() => detailRef.current?.cancelEdit()}
                                        disabled={saving}
                                        className="px-2.5 py-1.5 rounded-md text-xs font-medium text-muted hover:text-ink hover:bg-border/50 transition-colors disabled:opacity-40"
                                    >
                                        {t('automations.cancel')}
                                    </button>
                                    <button
                                        type="button"
                                        onClick={() => void detailRef.current?.save()}
                                        disabled={saving}
                                        className="inline-flex items-center gap-1 px-2.5 py-1.5 rounded-md text-xs font-medium text-ink bg-border/60 hover:bg-border transition-colors disabled:opacity-40"
                                    >
                                        {saving && <Loader2 className="h-3 w-3 animate-spin" />}
                                        {t('automations.save')}
                                    </button>
                                </>
                            ) : (
                                <>
                                    <button
                                        type="button"
                                        onClick={() => onRunNow(job.id)}
                                        className="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-md text-xs font-medium text-ink hover:bg-border/50 transition-colors"
                                        title={t('automations.runNow')}
                                    >
                                        <Play className="h-3 w-3" />
                                        {t('automations.runNow')}
                                    </button>
                                    <DropdownMenu>
                                        <DropdownMenuTrigger asChild>
                                            <button
                                                type="button"
                                                className="p-1.5 rounded-md text-muted hover:text-ink hover:bg-border/50 transition-colors"
                                                title={t('automations.moreActions')}
                                            >
                                                <MoreHorizontal className="h-3.5 w-3.5" />
                                            </button>
                                        </DropdownMenuTrigger>
                                        <DropdownMenuContent align="end">
                                            <DropdownMenuItem onSelect={() => detailRef.current?.enterEdit()}>
                                                <Pencil className="h-3.5 w-3.5" />
                                                {t('automations.edit')}
                                            </DropdownMenuItem>
                                            <DropdownMenuSeparator />
                                            <DropdownMenuItem onSelect={() => onToggleEnabled(job.id, !job.enabled)}>
                                                {job.enabled ? (
                                                    <>
                                                        <Pause className="h-3.5 w-3.5" />
                                                        {t('automations.pause')}
                                                    </>
                                                ) : (
                                                    <>
                                                        <Play className="h-3.5 w-3.5" />
                                                        {t('automations.resume')}
                                                    </>
                                                )}
                                            </DropdownMenuItem>
                                            <DropdownMenuItem
                                                destructive
                                                onSelect={() => setConfirmDelete(true)}
                                            >
                                                <Trash2 className="h-3.5 w-3.5" />
                                                {t('automations.delete')}
                                            </DropdownMenuItem>
                                        </DropdownMenuContent>
                                    </DropdownMenu>
                                </>
                            )}
                            <DialogCloseButton />
                        </div>
                    </DialogHeader>

                    <div className="flex-1 min-h-0 flex flex-col">
                        <CronJobDetail
                            key={job.id}
                            ref={detailRef}
                            job={job}
                            sourceSessionLabel={sourceSessionLabel}
                            onNavigateToSourceSession={onNavigateToSourceSession}
                            onUpdate={onUpdate}
                            loadRuns={loadRuns}
                            loadRunLogs={loadRunLogs}
                            onEditingChange={setEditing}
                            onSavingChange={setSaving}
                        />
                    </div>
                </DialogContent>
            </Dialog>

            <ConfirmDialog
                open={confirmDelete}
                onOpenChange={setConfirmDelete}
                title={t('automations.deleteTitle')}
                description={
                    <>
                        {t('automations.deleteDescriptionStart')} <span className="font-medium text-ink">{job.name || t('automations.deleteFallback')}</span>
                        {t('automations.deleteDescriptionEnd')}
                    </>
                }
                confirmLabel={t('automations.delete')}
                variant="danger"
                onConfirm={() => {
                    onDelete(job.id);
                }}
            />
        </>
    );
}
