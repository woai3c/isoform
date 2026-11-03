import { useCallback, useEffect, useState } from 'react';
import {
    CreateCronJob,
    UpdateCronJob,
    DeleteCronJob,
    RunCronJobNow,
    GetCronRuns,
    LoadCronRunLogs,
    ReorderCronJobs,
} from '../../wailsjs/go/main/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { ListCronJobs } from '@/lib/page-queries';
import type { main } from '../../wailsjs/go/models';
import { toast } from 'sonner';
import i18n from '@/i18n';
import type { StreamItem, StreamItemKind } from '@/types';
import { tryParseJSON } from '@/lib/stream-helpers';

export type CronJobState = main.CronJobState;
export type CronRunState = main.CronRunState & {
    candidateBackends?: string[];
    backend?: string;
    fallbackFrom?: string[];
    backendReason?: string;
    model?: string;
    actionsStarted?: number;
};

export interface CreateAutomationInput {
    name: string;
    prompt: string;
    cronExpr: string;
    workDir: string;
    notifyDm?: boolean;
    notifyPlatform?: string;
}

/**
 * Plain-text reason from a rejected binding call. The desktop explains a
 * refusal (for example a schedule another one covers, or expired evidence) in
 * the error, so it is shown under a translated lead line.
 */
function refusalReason(err: unknown): string | undefined {
    const text =
        typeof err === 'string' ? err : err instanceof Error ? err.message : '';
    return text.trim() || undefined;
}

export interface RefusalOptions {
    onRefused?: (reason?: string) => void;
}

export function useCronJobs() {
    const [jobs, setJobs] = useState<CronJobState[]>([]);
    const [loading, setLoading] = useState(true);

    // Initial load + event subscriptions.
    useEffect(() => {
        const refetch = () => {
            ListCronJobs().then((result) => {
                setJobs(result || []);
                setLoading(false);
            }).catch(() => setLoading(false));
        };

        refetch();

        const unsubState = EventsOn('cron:state', (job: CronJobState & { deleted?: boolean }) => {
            setJobs(prev => {
                if (job.deleted) {
                    return prev.filter(j => j.id !== job.id);
                }
                const idx = prev.findIndex(j => j.id === job.id);
                if (idx === -1) return [...prev, job];
                const next = [...prev];
                next[idx] = job;
                return next;
            });
        });

        // Safety net for dropped cron:state events. When the activity triager
        // creates an automation from a background goroutine while the WebKit
        // webview is throttled or backgrounded, the JS listener may never
        // fire. Refetching on window focus / visibility change lets stale
        // state self-heal as soon as the user interacts with the app again.
        //
        // The shared cache enforces the same 60-second minimum across cron
        // consumers; rapid alt-tab / window switching only reads fresh values.
        let lastFocusRefetch = 0;
        const FOCUS_REFETCH_MIN_INTERVAL_MS = 60_000;
        const throttledRefetch = () => {
            const now = Date.now();
            if (now - lastFocusRefetch < FOCUS_REFETCH_MIN_INTERVAL_MS) return;
            lastFocusRefetch = now;
            refetch();
        };
        const handleFocus = () => throttledRefetch();
        const handleVisibility = () => {
            if (document.visibilityState === 'visible') throttledRefetch();
        };
        window.addEventListener('focus', handleFocus);
        document.addEventListener('visibilitychange', handleVisibility);

        return () => {
            unsubState();
            window.removeEventListener('focus', handleFocus);
            document.removeEventListener('visibilitychange', handleVisibility);
        };
    }, []);

    const createJob = useCallback(async (input: CreateAutomationInput): Promise<CronJobState> => {
        console.log('[cron-jobs] createJob: calling CreateCronJob binding...');
        let result: CronJobState;
        try {
            result = await CreateCronJob({
                name: input.name,
                description: '',
                scheduleKind: 'cron',
                scheduleConfig: { expr: input.cronExpr } as any,
                targetKind: 'agent',
                targetConfig: {
                    prompt: input.prompt,
                    ...(input.workDir ? { project_dir: input.workDir } : {}),
                } as any,
                deleteAfterRun: false,
                retryPolicy: { max_attempts: 0 } as any,
                sourceSessionId: '',
                sourcePlatform: '',
                notifyDm: input.notifyDm ?? true,
                ...(input.notifyPlatform != null ? { notifyPlatform: input.notifyPlatform } : {}),
            } as any);
        } catch (err) {
            const message = err instanceof Error ? err.message : String(err);
            if (message.toLowerCase().includes('duplicate automation')) {
                throw new Error('Automation already exists');
            }
            throw err instanceof Error ? err : new Error(message);
        }
        console.log('[cron-jobs] createJob: binding returned', result.id);
        // Optimistically add to list immediately (cron:state event may arrive slightly later).
        setJobs(prev => {
            const idx = prev.findIndex(j => j.id === result.id);
            if (idx !== -1) return prev; // already added by event
            return [...prev, result];
        });
        return result;
    }, []);

    // Turning a schedule on can be refused (an Agency proposal whose evidence
    // expired, one covered by a related schedule, or decision storage that is
    // unavailable). The toggle follows cron:state, so it stays as it was; the
    // notice says why. Resolves false when the change was refused.
    // Callers that say the failure in place (Scheduled's detail) pass
    // onRefused instead of getting a toast.
    const toggleEnabled = useCallback(async (id: string, enabled: boolean, opts?: RefusalOptions): Promise<boolean> => {
        console.log('[cron-jobs] toggleEnabled:', id, enabled);
        try {
            await UpdateCronJob(id, { enabled } as any);
        } catch (err) {
            console.error('[cron-jobs] toggleEnabled failed:', id, err);
            if (opts?.onRefused) { opts.onRefused(refusalReason(err)); return false; }
            toast.error(
                i18n.t(enabled ? 'app:automations.enableFailed' : 'app:automations.pauseFailed'),
                { duration: 5000, description: refusalReason(err) },
            );
            return false;
        }
        console.log('[cron-jobs] toggleEnabled: done', id);
        return true;
    }, []);

    const updateJob = useCallback(async (id: string, patch: Record<string, unknown>) => {
        // Wails serialises plain JS objects as JSON which Go stores verbatim
        // in json.RawMessage fields. Do NOT use jsonToBytes / number[] encoding.
        console.log('[cron-jobs] updateJob:', id, Object.keys(patch));
        await UpdateCronJob(id, patch as any);
        console.log('[cron-jobs] updateJob: done', id);
    }, []);

    // Resolves false when the delete was refused; the schedule stays listed.
    const deleteJob = useCallback(async (id: string, opts?: RefusalOptions): Promise<boolean> => {
        console.log('[cron-jobs] deleteJob:', id);
        try {
            await DeleteCronJob(id);
        } catch (err) {
            console.error('[cron-jobs] deleteJob failed:', id, err);
            if (opts?.onRefused) { opts.onRefused(refusalReason(err)); return false; }
            toast.error(i18n.t('app:automations.deleteFailed'), {
                duration: 5000,
                description: refusalReason(err),
            });
            return false;
        }
        console.log('[cron-jobs] deleteJob: done', id);
        setJobs(prev => prev.filter(j => j.id !== id));
        return true;
    }, []);

    // Resolves false when the run was refused (for example a schedule Isoform
    // proposed that is still waiting for approval) and shows why.
    const runNow = useCallback(async (id: string, opts?: RefusalOptions): Promise<boolean> => {
        try {
            await RunCronJobNow(id);
            return true;
        } catch (err) {
            console.error('[cron-jobs] runNow failed:', id, err);
            if (opts?.onRefused) { opts.onRefused(refusalReason(err)); return false; }
            toast.error(i18n.t('app:automations.runFailed'), {
                duration: 5000,
                description: refusalReason(err),
            });
            return false;
        }
    }, []);

    const loadRuns = useCallback(async (jobId: string): Promise<CronRunState[]> => {
        const result = await GetCronRuns(jobId);
        return result || [];
    }, []);

    const reorderJobs = useCallback(async (ids: string[]) => {
        setJobs(prev => {
            const jobMap = new Map(prev.map(j => [j.id, j]));
            return ids.map(id => jobMap.get(id)).filter((j): j is CronJobState => j != null);
        });
        await ReorderCronJobs(ids);
    }, []);

    const loadRunLogs = useCallback(async (jobId: string, runId: string): Promise<StreamItem[]> => {
        const result = await LoadCronRunLogs(jobId, runId);
        if (!result) return [];
        return result.map((item) => ({
            id: item.id,
            kind: item.kind as StreamItemKind,
            content: item.content,
            toolName: item.toolName,
            toolInput: item.toolInput ? tryParseJSON(item.toolInput) : undefined,
            runId: item.runId,
            sessionId: item.sessionId,
            sortOrder: item.sortOrder,
        }));
    }, []);

    return { jobs, loading, createJob, toggleEnabled, updateJob, deleteJob, runNow, reorderJobs, loadRuns, loadRunLogs };
}
