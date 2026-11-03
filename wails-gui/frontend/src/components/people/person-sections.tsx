// The person detail's folded sections: everything the Who you know page showed
// before RC 2 that the new layout keeps one click away (recent messages,
// Activity sessions, the research dossier, the voice fingerprint, chat
// handles and messages by app). Each section renders without its own
// heading; DetailFold supplies it.
import { ReactNode, useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { ChevronRight, FileSearch, Globe, Loader2, PenLine, RefreshCw, Search, Trash2 } from 'lucide-react';
import {
    DeletePersonResearch,
    DeleteVoiceSample,
    EnrollVoiceSample,
    StartPersonResearch,
    TestVoiceMatch,
    type PersonResearchDTO,
    type PrincipalActivitySessionDTO,
    type PrincipalDTO,
    type PrincipalStatsDTO,
    type VoiceEnrollmentStatusDTO,
    type VoiceMatchResultDTO,
    type VoiceSampleDTO,
} from '@/lib/wails-shim-app';
import {
    GetPersonResearch,
    GetVoiceEnrollmentStatus,
    ListPrincipalActivitySessions,
    ListVoiceSamples,
} from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';
import { EventsOn } from '../../../wailsjs/runtime/runtime';
import { MarkdownContent } from '../markdown-content';
import { AppIcon, hasAppIcon } from '../icons/app-icons';
import { Button } from '../ui/button';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '../ui/collapsible';
import { cn } from '@/lib/utils';
import { appLabel, formatAbsolute, maskHandleId, type Translate } from './people-format';

// Maps the lowercase chat-app ids stored on sightings/handles (e.g. "slack",
// "wechat") to the case-sensitive bundle keys the shared AppIcon component
// expects (e.g. "Slack.app", "WeChat.app"). Apps without a brand icon
// (imessage, email) are simply absent and fall back to text only.
const APP_BUNDLE_BY_ID: Record<string, string> = {
    slack: 'Slack.app',
    wechat: 'WeChat.app',
    weixin: 'WeChat.app',
    telegram: 'Telegram.app',
    discord: 'Discord.app',
    feishu: 'Feishu.app',
    lark: 'Feishu.app',
};

function appBundleForId(app: string): string | null {
    const bundle = APP_BUNDLE_BY_ID[app.trim().toLowerCase()];
    return bundle && hasAppIcon(bundle) ? bundle : null;
}

// ── Fold ──

/** One folded section of the person detail: a heading row that opens in
 *  place. Closed content is not mounted, so its reads wait for the click. */
export function DetailFold({
    label,
    count,
    defaultOpen = false,
    children,
}: {
    label: string;
    count?: number;
    defaultOpen?: boolean;
    children: ReactNode;
}) {
    const [open, setOpen] = useState(defaultOpen);
    return (
        <Collapsible open={open} onOpenChange={setOpen} className="border-t border-border-divider">
            <CollapsibleTrigger className="flex w-full items-center gap-2 rounded-md py-2.5 text-left text-[13px] font-semibold text-ink transition-colors hover:text-ink/80 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-2/40">
                <ChevronRight className={cn('size-3.5 shrink-0 text-muted transition-transform duration-150', open && 'rotate-90')} aria-hidden />
                <span className="flex-1">{label}</span>
                {typeof count === 'number' && count > 0 && <span className="text-[12px] font-normal text-muted tabular-nums">{count}</span>}
            </CollapsibleTrigger>
            <CollapsibleContent className="pb-4 pl-5">{children}</CollapsibleContent>
        </Collapsible>
    );
}

// ── Recent messages ──

export function RecentMessagesList({ stats }: { stats: PrincipalStatsDTO }) {
    const { t, i18n } = useTranslation('people');
    const tr: Translate = (key, options) => t(key as never, options as never) as unknown as string;
    return (
        <ul className="divide-y divide-border-divider overflow-hidden rounded-lg border border-border bg-panel">
            {stats.recentMessages.map((m) => (
                <li key={`${m.app}-${m.seenAt}`} className="flex flex-col gap-1 px-3 py-2.5">
                    <div className="flex items-center gap-2 text-[11px] text-muted">
                        <span className="font-medium text-ink/70">{appLabel(tr, m.app)}</span>
                        <span aria-hidden>·</span>
                        <span>{formatAbsolute(i18n.language, m.seenAt)}</span>
                    </div>
                    <p className="whitespace-pre-wrap text-[13px] leading-snug text-ink">{m.preview}</p>
                </li>
            ))}
        </ul>
    );
}

// ── Technical details ──

/** Chat handles with their account IDs, messages by app and first and last
 *  seen: exact values, kept out of the scan path. */
export function PersonTechnicalDetails({ principal, stats }: { principal: PrincipalDTO; stats?: PrincipalStatsDTO }) {
    const { t, i18n } = useTranslation('people');
    const tr: Translate = (key, options) => t(key as never, options as never) as unknown as string;
    const handles = (principal.appHandles ?? []).filter((h) => h.app !== 'contacts' && h.app !== 'vcard');
    const rows: Array<[string, string]> = [[tr('technical.kind'), tr(`kind.${principal.kind}`)]];
    if (stats && stats.firstSeenAt > 0) rows.push([tr('technical.firstSeen'), formatAbsolute(i18n.language, stats.firstSeenAt)]);
    if (stats && stats.lastSeenAt > 0) rows.push([tr('technical.lastSeen'), formatAbsolute(i18n.language, stats.lastSeenAt)]);
    if (stats) rows.push([tr('technical.messages'), tr('rowMessages', { count: stats.totalMessages })]);
    rows.push([tr('technical.id'), principal.id]);
    return (
        <div className="flex flex-col gap-4">
            <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1.5 text-[12px]">
                {rows.map(([label, value]) => (
                    <div key={label} className="contents">
                        <dt className="text-muted">{label}</dt>
                        <dd className="min-w-0 break-all text-ink">{value}</dd>
                    </div>
                ))}
            </dl>
            {stats && stats.byApp.length > 0 && (
                <div className="flex flex-col gap-2">
                    <h4 className="text-[12px] font-medium text-muted">{tr('byAppHeading')}</h4>
                    <ByAppBars stats={stats} />
                </div>
            )}
            {handles.length > 0 && (
                <div className="flex flex-col gap-2">
                    <h4 className="text-[12px] font-medium text-muted">{tr('appHandlesHeading')}</h4>
                    <ul className="divide-y divide-border-divider overflow-hidden rounded-lg border border-border bg-panel">
                        {handles.map((h) => {
                            const bundle = appBundleForId(h.app);
                            return (
                                <li key={h.id} className="flex items-center gap-3 px-3 py-2 text-[12px]">
                                    <span className="flex w-[88px] shrink-0 items-center gap-1.5 font-medium text-ink/80">
                                        {bundle && <AppIcon appBundle={bundle} className="h-3.5 w-3.5 shrink-0 object-contain" />}
                                        <span className="truncate">{appLabel(tr, h.app)}</span>
                                    </span>
                                    <span className="min-w-0 flex-1 truncate">
                                        {h.displayHandle ? (
                                            <>
                                                <span className="text-ink">{h.displayHandle}</span>
                                                {h.handleId && <span className="ml-1 text-muted">({h.handleId})</span>}
                                            </>
                                        ) : (
                                            <span className="text-muted">{tr('appHandleConnectedMasked', { id: maskHandleId(h.handleId) })}</span>
                                        )}
                                    </span>
                                </li>
                            );
                        })}
                    </ul>
                </div>
            )}
        </div>
    );
}

function ByAppBars({ stats }: { stats: PrincipalStatsDTO }) {
    const { t } = useTranslation('people');
    const tr: Translate = (key, options) => t(key as never, options as never) as unknown as string;
    const max = Math.max(1, ...stats.byApp.map((b) => b.messageCount));
    // A single source has no distribution to show — a normalized bar would
    // always sit at 100% and read as decoration. Render a plain line instead.
    if (stats.byApp.length === 1) {
        const b = stats.byApp[0];
        const bundle = appBundleForId(b.app);
        return (
            <div className="flex items-center gap-2 text-[12.5px]">
                {bundle && <AppIcon appBundle={bundle} className="h-4 w-4 shrink-0 object-contain" />}
                <span className="text-ink">{appLabel(tr, b.app)}</span>
                <span className="text-muted">· {tr('rowMessages', { count: b.messageCount })}</span>
            </div>
        );
    }
    return (
        <div className="flex flex-col gap-2">
            {stats.byApp.map((b) => {
                const bundle = appBundleForId(b.app);
                return (
                    <div key={b.app} className="flex items-center gap-3 text-[12px]">
                        <span className="flex w-[100px] shrink-0 items-center gap-2 text-ink/80">
                            {bundle && <AppIcon appBundle={bundle} className="h-4 w-4 shrink-0 object-contain" />}
                            <span className="truncate">{appLabel(tr, b.app)}</span>
                        </span>
                        <div className="h-1 flex-1 overflow-hidden rounded-full bg-surface-active">
                            <div className="h-full rounded-full bg-ink/20 transition-[width] duration-300" style={{ width: `${(b.messageCount / max) * 100}%` }} />
                        </div>
                        <span className="w-[40px] text-right text-[11px] tabular-nums text-muted">{b.messageCount}</span>
                    </div>
                );
            })}
        </div>
    );
}

// ── Activity sessions ──

// Every Activity session this person appeared in (most recent first); a
// row opens the Activity page at that session.
export function ActivitySessionsList({
    principalId,
    principalName,
    onOpenActivitySession,
}: {
    principalId: string;
    principalName: string;
    onOpenActivitySession?: (sessionId: string, anchorTimestamp?: number, principalName?: string) => void;
}) {
    const { t, i18n } = useTranslation('people');
    const tr: Translate = (key, options) => t(key as never, options as never) as unknown as string;
    const [sessions, setSessions] = useState<PrincipalActivitySessionDTO[] | null>(
        // A reopened fold paints the cached rows at once; the read below
        // revalidates them.
        () => pageQueryCache.peek('ListPrincipalActivitySessions', [principalId, 50]) ?? null,
    );
    const [failed, setFailed] = useState(false);
    const [attempt, setAttempt] = useState(0);

    useEffect(() => {
        let cancelled = false;
        const seeded = pageQueryCache.peek<PrincipalActivitySessionDTO[]>('ListPrincipalActivitySessions', [principalId, 50]);
        setSessions(seeded ?? null);
        setFailed(false);
        if (attempt > 0) pageQueryCache.invalidate(['ListPrincipalActivitySessions'], true, [principalId, 50]);
        ListPrincipalActivitySessions(principalId, 50)
            .then((rows) => {
                if (!cancelled) setSessions(rows ?? []);
            })
            .catch(() => {
                if (!cancelled) setFailed(true);
            });
        return () => {
            cancelled = true;
        };
    }, [principalId, attempt]);

    if (failed) {
        return (
            <div className="flex items-center gap-3 text-[12.5px] text-ink">
                <span>{tr('detail.sessionsFailed')}</span>
                <Button variant="secondary" size="sm" onClick={() => setAttempt((n) => n + 1)}>
                    {tr('overview.retry')}
                </Button>
            </div>
        );
    }
    if (sessions === null) {
        return (
            <ul aria-busy="true" aria-label={tr('activitySessionsLoadingHeading')} className="flex flex-col gap-2">
                {[0, 1, 2].map((i) => (
                    <li key={i} className="flex flex-col gap-1.5">
                        <span className="h-3 w-2/3 animate-pulse rounded bg-surface-active" />
                        <span className="h-2.5 w-1/2 animate-pulse rounded bg-surface-active" />
                    </li>
                ))}
            </ul>
        );
    }
    if (sessions.length === 0) return <p className="text-[12px] text-muted">{tr('activitySessionsEmpty')}</p>;
    return (
        <ul className="divide-y divide-border-divider overflow-hidden rounded-lg border border-border bg-panel">
            {sessions.map((sess) => {
                // Cap the title at three app names — raw joined lists run
                // to a full line of system noise ("Dock, loginwindow, …").
                const shown = sess.appNames.slice(0, 3).join(', ');
                const extra = sess.appNames.length - 3;
                const apps = !sess.appNames.length
                    ? tr('activitySessionsUnknownApp')
                    : extra > 0
                      ? tr('activitySessionsMoreApps', { apps: shown, count: extra })
                      : shown;
                const interactive = !!onOpenActivitySession;
                const Tag: 'button' | 'div' = interactive ? 'button' : 'div';
                return (
                    <li key={sess.sessionId}>
                        <Tag
                            type={interactive ? ('button' as const) : undefined}
                            onClick={interactive ? () => onOpenActivitySession!(sess.sessionId, sess.lastSeenAt > 0 ? sess.lastSeenAt : undefined, principalName) : undefined}
                            className={cn('flex w-full items-center gap-3 px-3 py-2.5 text-left transition-colors', interactive && 'cursor-pointer hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent-2/40')}
                        >
                            <span className="flex min-w-0 flex-1 flex-col gap-1">
                                <span className="flex items-center gap-2 text-[12.5px] text-ink">
                                    <span className="truncate">{apps}</span>
                                    {sess.isMeeting && (
                                        <span className="rounded-full border border-border bg-panel-warm px-1.5 py-0.5 text-[11px] text-muted">{tr('activitySessionsMeetingBadge')}</span>
                                    )}
                                </span>
                                <span className="text-[11px] text-muted">{formatAbsolute(i18n.language, sess.startTime)}</span>
                            </span>
                            {interactive && <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted" aria-hidden />}
                        </Tag>
                    </li>
                );
            })}
        </ul>
    );
}

// ── Research dossier ──

// ResearchStep is one action in the live trajectory: a search query, a page
// read, or the final writing step. kind drives the icon + localized verb;
// title is the raw query/host shown verbatim.
type ResearchStep = { kind: string; title: string };

// The AI research dossier for a person: a web-sourced Contact / Who They Are
// / Your Relationship profile. Research runs in the background
// (StartPersonResearch); the "people:research" event refreshes it as the
// status changes rather than polling. onChanged lets the detail re-read its
// summary when a run finishes or the dossier is deleted.
export function PersonDossier({ principalId, onChanged }: { principalId: string; onChanged?: () => void }) {
    const { t, i18n } = useTranslation('people');
    const tr = useCallback<Translate>((key, options) => t(key as never, options as never) as unknown as string, [t]);
    const [research, setResearch] = useState<PersonResearchDTO | null>(null);
    // Live phase streamed from the agent (searching / reading / writing) so the
    // user sees motion during a multi-minute run instead of a static spinner.
    const [progress, setProgress] = useState<{ phase: string; detail: string } | null>(null);
    const [steps, setSteps] = useState<ResearchStep[]>([]);

    const load = useCallback(async () => {
        try {
            setResearch(await GetPersonResearch(principalId));
        } catch {
            // A read failure leaves the prior state; the user can retry.
        }
    }, [principalId]);

    useEffect(() => {
        // A reopened fold paints the cached dossier at once; a terminal
        // research event invalidates it before the re-read (page-queries).
        setResearch(pageQueryCache.peek<PersonResearchDTO>('GetPersonResearch', [principalId]) ?? null);
        setProgress(null);
        setSteps([]);
        void load();
        return EventsOn('people:research', (payload: unknown) => {
            const p = payload as { principalId?: string; status?: string; phase?: string; detail?: string; step?: { kind?: string; title?: string } } | undefined;
            if (p?.principalId !== principalId) return;
            if (p.status === 'researching') {
                setProgress(p.phase ? { phase: p.phase, detail: p.detail ?? '' } : null);
                setResearch((prev) => (prev ? { ...prev, status: 'researching' } : prev));
                if (p.step?.kind) {
                    const step: ResearchStep = { kind: p.step.kind, title: p.step.title ?? '' };
                    setSteps((prev) => [...prev, step].slice(-12));
                }
            } else {
                // Terminal (ready / failed / idle) — clear live state and refetch.
                setProgress(null);
                setSteps([]);
                void load();
                onChanged?.();
            }
        });
    }, [principalId, load, onChanged]);

    const status = research?.status ?? 'idle';
    const researching = status === 'researching';
    const hasDossier = !!research?.markdown;

    const startResearch = useCallback(async () => {
        setSteps([]);
        setProgress(null);
        setResearch((prev) =>
            prev
                ? { ...prev, status: 'researching' }
                : ({ principalId, status: 'researching', summary: '', markdown: '', sources: [], model: '', error: '', researchedAt: 0, updatedAt: 0 } as PersonResearchDTO),
        );
        try {
            await StartPersonResearch(principalId, '');
        } catch (err) {
            toast.error(tr('dossierStartErrorToast', { error: String(err) }));
            void load();
        }
    }, [principalId, tr, load]);

    const deleteDossier = useCallback(async () => {
        try {
            await DeletePersonResearch(principalId);
            setResearch(null);
            toast.success(tr('dossierDeletedToast'));
            onChanged?.();
        } catch (err) {
            toast.error(tr('dossierStartErrorToast', { error: String(err) }));
        }
    }, [principalId, tr, onChanged]);

    const phaseLabel = (): string => {
        switch (progress?.phase) {
            case 'searching':
                return tr('dossierPhaseSearching');
            case 'reading':
                return progress.detail ? tr('dossierPhaseReading', { detail: progress.detail }) : tr('dossierPhaseReadingGeneric');
            case 'writing':
                return tr('dossierPhaseWriting');
            case 'thinking':
                return tr('dossierPhaseThinking');
            default:
                return tr('dossierResearching');
        }
    };

    const stepLabel = (s: ResearchStep): string => {
        switch (s.kind) {
            case 'context':
                return tr('dossierStepContext');
            case 'search':
                return s.title ? tr('dossierStepSearch', { query: s.title }) : tr('dossierPhaseSearching');
            case 'read':
                return s.title ? tr('dossierStepRead', { host: s.title }) : tr('dossierPhaseReadingGeneric');
            case 'write':
                return tr('dossierPhaseWriting');
            default:
                return s.title || tr('dossierResearching');
        }
    };

    const stepIcon = (kind: string) => {
        switch (kind) {
            case 'search':
                return Search;
            case 'read':
                return Globe;
            case 'write':
                return PenLine;
            default:
                return FileSearch;
        }
    };

    return (
        <div className="flex flex-col gap-3">
            <div>
                <Button variant="outline" size="sm" onClick={() => void startResearch()} disabled={researching} aria-busy={researching}>
                    {researching ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <RefreshCw className="h-3.5 w-3.5" />}
                    {researching ? tr('dossierResearching') : hasDossier ? tr('dossierRefreshButton') : tr('dossierResearchButton')}
                </Button>
            </div>
            {hasDossier ? (
                <div className="flex flex-col gap-3">
                    {research!.researchedAt > 0 && (
                        <p className="text-[11px] text-muted">{tr('dossierUpdatedAt', { when: new Date(research!.researchedAt).toLocaleString(i18n.language) })}</p>
                    )}
                    <div className="text-ink">
                        <MarkdownContent content={research!.markdown} />
                    </div>
                    {research!.sources.length > 0 && (
                        <div className="flex flex-col gap-1.5">
                            <h5 className="text-[12px] font-medium text-muted">{tr('dossierSourcesHeading', { count: research!.sources.length })}</h5>
                            <ol className="flex flex-col gap-1">
                                {research!.sources.map((src) => (
                                    <li key={src.n} className="text-[12px] leading-relaxed text-muted">
                                        <span className="tabular-nums text-ink/70">[{src.n}]</span>{' '}
                                        <a href={src.url} target="_blank" rel="noreferrer" className="break-all text-accent-2 hover:underline">
                                            {src.title || src.url}
                                        </a>
                                    </li>
                                ))}
                            </ol>
                        </div>
                    )}
                    <Button variant="destructive" size="sm" className="self-start" onClick={() => void deleteDossier()}>
                        <Trash2 className="h-3.5 w-3.5" />
                        {tr('dossierDeleteButton')}
                    </Button>
                </div>
            ) : status === 'failed' ? (
                <p className="text-[12px] leading-relaxed text-error">{research?.error ? `${tr('dossierFailed')}: ${research.error}` : tr('dossierFailed')}</p>
            ) : researching ? (
                <div className="flex flex-col gap-2.5 py-1">
                    <div className="h-1 w-full overflow-hidden rounded-full bg-surface-active">
                        <div className="research-progress-indeterminate h-full w-1/3 rounded-full bg-accent-2/70" />
                    </div>
                    {steps.length > 0 ? (
                        <ol className="flex flex-col gap-1.5">
                            {steps.map((s, i) => {
                                const isLast = i === steps.length - 1;
                                const Icon = stepIcon(s.kind);
                                return (
                                    <li key={i} className={cn('flex items-center gap-2 text-[12px]', isLast ? 'text-ink' : 'text-muted')}>
                                        {isLast ? <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-accent-2" /> : <Icon className="h-3.5 w-3.5 shrink-0 text-muted/60" />}
                                        <span className="truncate">{stepLabel(s)}</span>
                                    </li>
                                );
                            })}
                        </ol>
                    ) : (
                        <div className="flex items-center gap-2 text-[12px] text-muted">
                            <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin" />
                            <span className="truncate">{phaseLabel()}</span>
                        </div>
                    )}
                    <p className="text-[11px] leading-relaxed text-muted/70">{tr('dossierResearchingHint')}</p>
                </div>
            ) : (
                <p className="text-[12px] leading-relaxed text-muted">{tr('dossierEmpty')}</p>
            )}
        </div>
    );
}

// ── Voice fingerprint (You only) ──

// Mic samples the background-audio identity pipeline uses to verify "this
// voice is actually me" on every captured speech chunk. Without enrollment
// the system can't tell a coworker speaking into your mic apart from you.
// See voice_enrollment.go for the backend.
export function VoiceFingerprint({ principalId }: { principalId: string }) {
    const { t, i18n } = useTranslation('people');
    const tr: Translate = (key, options) => t(key as never, options as never) as unknown as string;
    const reload = useCallback(async () => {
        try {
            const [s, list] = await Promise.all([GetVoiceEnrollmentStatus(principalId), ListVoiceSamples(principalId)]);
            setStatus(s);
            setSamples(list ?? []);
        } catch (err: unknown) {
            console.warn('[people] voice fingerprint reload failed:', err);
        }
    }, [principalId]);

    const [status, setStatus] = useState<VoiceEnrollmentStatusDTO | null>(
        () => pageQueryCache.peek('GetVoiceEnrollmentStatus', [principalId]) ?? null,
    );
    const [samples, setSamples] = useState<VoiceSampleDTO[]>(
        () => pageQueryCache.peek('ListVoiceSamples', [principalId]) ?? [],
    );
    const [recording, setRecording] = useState(false);
    const [recordSecondsLeft, setRecordSecondsLeft] = useState(0);
    const [testing, setTesting] = useState(false);
    const [testSecondsLeft, setTestSecondsLeft] = useState(0);
    const [testResult, setTestResult] = useState<VoiceMatchResultDTO | null>(null);
    const enrollDurationSec = 10;
    const testDurationSec = 5;

    useEffect(() => {
        void reload();
    }, [reload]);

    const handleRecord = async () => {
        if (recording) return;
        setRecording(true);
        setRecordSecondsLeft(enrollDurationSec);
        const tick = setInterval(() => setRecordSecondsLeft((s) => Math.max(0, s - 1)), 1000);
        try {
            await EnrollVoiceSample(principalId, enrollDurationSec);
            // The enrollment just changed both reads; a fresh cache entry
            // would serve the pre-enroll state.
            pageQueryCache.invalidate(['GetVoiceEnrollmentStatus', 'ListVoiceSamples'], true, [principalId]);
            await reload();
            toast.success(tr('voicePrintEnrollSuccessToast'));
        } catch (err: unknown) {
            toast.error(tr('voicePrintEnrollErrorToast', { error: String(err) }));
        } finally {
            clearInterval(tick);
            setRecording(false);
            setRecordSecondsLeft(0);
        }
    };

    const handleDelete = async (id: number) => {
        try {
            await DeleteVoiceSample(id);
            pageQueryCache.invalidate(['GetVoiceEnrollmentStatus', 'ListVoiceSamples'], true, [principalId]);
            await reload();
            toast.success(tr('voicePrintDeletedToast'));
        } catch (err: unknown) {
            toast.error(tr('voicePrintDeleteErrorToast', { error: String(err) }));
        }
    };

    const handleTest = async () => {
        if (testing) return;
        setTesting(true);
        setTestSecondsLeft(testDurationSec);
        setTestResult(null);
        const tick = setInterval(() => setTestSecondsLeft((s) => Math.max(0, s - 1)), 1000);
        try {
            setTestResult(await TestVoiceMatch(principalId, testDurationSec));
        } catch (err: unknown) {
            toast.error(tr('voicePrintTestErrorToast', { error: String(err) }));
        } finally {
            clearInterval(tick);
            setTesting(false);
            setTestSecondsLeft(0);
        }
    };

    const enrolled = (status?.sampleCount ?? 0) > 0;
    const modelReady = status?.modelReady ?? false;

    return (
        <div className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center justify-between gap-2">
                <span className={cn('rounded-full px-2 py-0.5 text-[11px] font-medium', enrolled ? 'bg-surface-active text-ink' : 'bg-panel-warm text-muted')}>
                    {enrolled ? tr('voicePrintBadgeEnrolled', { count: status?.sampleCount ?? 0 }) : tr('voicePrintBadgeNotEnrolled')}
                </span>
                <div className="flex items-center gap-2">
                    {enrolled && (
                        <Button
                            variant="outline"
                            size="sm"
                            onClick={() => void handleTest()}
                            disabled={testing || recording || !modelReady}
                            aria-busy={testing}
                            title={tr('voicePrintTestButtonTitle', { seconds: testDurationSec })}
                        >
                            {testing ? tr('voicePrintTesting', { seconds: testSecondsLeft }) : tr('voicePrintTestButton', { seconds: testDurationSec })}
                        </Button>
                    )}
                    <Button
                        size="sm"
                        onClick={() => void handleRecord()}
                        disabled={recording || testing || !modelReady}
                        aria-busy={recording}
                        title={!modelReady ? status?.modelMissingHint : tr('voicePrintRecordButtonTitle', { seconds: enrollDurationSec })}
                    >
                        {recording ? tr('voicePrintRecording', { seconds: recordSecondsLeft }) : tr('voicePrintRecordButton', { seconds: enrollDurationSec })}
                    </Button>
                </div>
            </div>
            <p className="text-[12px] leading-relaxed text-muted">{enrolled ? tr('voicePrintEnrolledHint') : tr('voicePrintEmptyHint')}</p>
            {!modelReady && status?.modelMissingHint && <p className="text-[12px] text-warning">{status.modelMissingHint}</p>}
            {samples.length > 0 && (
                <ul className="divide-y divide-border-divider overflow-hidden rounded-lg border border-border bg-panel">
                    {samples.map((s) => (
                        <li key={s.id} className="flex items-center gap-3 px-3 py-2">
                            <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                                <span className="truncate text-[12px] font-medium text-ink">{s.label || tr('voicePrintSampleLabelFallback')}</span>
                                <span className="text-[11px] text-muted">{tr('voicePrintSampleMeta', { seconds: s.audioSeconds.toFixed(1), when: formatAbsolute(i18n.language, s.capturedAt) })}</span>
                            </div>
                            <Button variant="destructive" size="icon" onClick={() => void handleDelete(s.id)} title={tr('voicePrintDeleteAria')} aria-label={tr('voicePrintDeleteAria')}>
                                <Trash2 className="h-3.5 w-3.5" />
                            </Button>
                        </li>
                    ))}
                </ul>
            )}
            {testResult && <VoiceMatchResultPanel result={testResult} />}
        </div>
    );
}

// The result of a single TestVoiceMatch call: the verdict, the best score
// against the threshold, and a per-sample breakdown so the user can see
// which clip matched best.
function VoiceMatchResultPanel({ result }: { result: VoiceMatchResultDTO }) {
    const { t, i18n } = useTranslation('people');
    const tr: Translate = (key, options) => t(key as never, options as never) as unknown as string;
    if (result.verdict === 'no_enrollment') {
        return <div className="rounded-md border border-warning-border bg-warning-bg px-3 py-2 text-[12px] text-warning">{tr('voicePrintTestNoEnrollment')}</div>;
    }
    const isMatch = result.verdict === 'match';
    const pct = Math.max(0, Math.min(100, Math.round(result.bestScore * 100)));
    const thresholdPct = Math.max(0, Math.min(100, Math.round(result.threshold * 100)));
    return (
        <div className={cn('flex flex-col gap-2 rounded-md border px-3 py-2.5', isMatch ? 'border-success-border bg-success-bg' : 'border-warning-border bg-warning-bg')}>
            <div className="flex items-baseline justify-between gap-3">
                <span className={cn('text-[12px] font-semibold', isMatch ? 'text-success' : 'text-warning')}>
                    {isMatch ? tr('voicePrintTestVerdictMatch') : tr('voicePrintTestVerdictNoMatch')}
                </span>
                <span className="text-[11px] tabular-nums text-muted">
                    {tr('voicePrintTestScoreLine', { score: result.bestScore.toFixed(3), threshold: result.threshold.toFixed(2) })}
                </span>
            </div>
            <div className="relative h-1.5 overflow-hidden rounded-full bg-surface-active">
                <div className={cn('absolute inset-y-0 left-0 rounded-full', isMatch ? 'bg-success' : 'bg-warning')} style={{ width: `${pct}%` }} />
                <div className="absolute inset-y-0 w-px bg-ink/60" style={{ left: `${thresholdPct}%` }} title={tr('voicePrintTestThresholdMarker', { threshold: result.threshold.toFixed(2) })} />
            </div>
            <p className="text-[11px] leading-relaxed text-muted">{isMatch ? tr('voicePrintTestHintMatch') : tr('voicePrintTestHintNoMatch')}</p>
            {result.enrolledSampleHits && result.enrolledSampleHits.length > 1 && (
                <Collapsible className="text-[11px] text-muted">
                    <CollapsibleTrigger className="cursor-pointer select-none rounded hover:text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-2/40">
                        {tr('voicePrintTestPerSampleHeading', { count: result.enrolledSampleHits.length })}
                    </CollapsibleTrigger>
                    <CollapsibleContent>
                        <ul className="mt-1.5 flex flex-col gap-0.5 pl-3">
                            {result.enrolledSampleHits
                                .slice()
                                .sort((a, b) => b.score - a.score)
                                .map((h) => (
                                    <li key={h.sampleId} className="flex justify-between gap-2">
                                        <span className="truncate">{h.label || formatAbsolute(i18n.language, h.capturedAt)}</span>
                                        <span className="tabular-nums">{h.score.toFixed(3)}</span>
                                    </li>
                                ))}
                        </ul>
                    </CollapsibleContent>
                </Collapsible>
            )}
        </div>
    );
}
