import { useCallback, useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import { ChevronRight, Loader2 } from 'lucide-react';
import { toast } from 'sonner';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { EnsureWeeklySummary, GenerateWeeklySummary, GetDailySummary } from '../../wailsjs/go/main/App';
import { GetYourWeekForDate, type MobileBriefCard, type YourWeekEntry } from '@/lib/wails-shim-app';
import { usePagePrivacyWithdrawal } from '@/lib/page-queries';
import { YourWeekCard } from './today/YourWeekCard';
import type { TodayActionHandlers } from './today/types';
import type { main } from '../../wailsjs/go/models';
import { AISummaryCard, firstDigestSentence } from './activity-summary';
import { formatDateStr, formatDurationMs } from './timeline/useCalendarData';

// The Activity page's Week view: the weekly digest plus one row per day
// (date, active time, the day's first digest sentence) that opens that day
// in the Day view.

export function ActivityWeekView({
    date,
    onOpenDay,
    onAskAboutDate,
    yourWeekHandlers,
    onYourWeekCard,
}: {
    /** A day inside the week to show. */
    date: Date;
    /** Opens a day (YYYY-MM-DD) in the Day view. */
    onOpenDay: (dateStr: string) => void;
    onAskAboutDate?: (date: string, scope: 'daily' | 'weekly') => void;
    yourWeekHandlers?: TodayActionHandlers;
    onYourWeekCard?: (briefId: string, card: MobileBriefCard, ask?: boolean) => void;
}) {
    const { t, i18n } = useTranslation('activity');
    const dateStr = formatDateStr(date);
    const [weeklyData, setWeeklyData] = useState<main.WeeklySummaryData | null>(null);
    const [loading, setLoading] = useState(true);
    const [generating, setGenerating] = useState(false);
    // dateStr -> the first sentence of that day's digest (empty when none).
    const [daySentences, setDaySentences] = useState<ReadonlyMap<string, string>>(new Map());
    const summaryGeneration = useRef(0);
    const [yourWeek, setYourWeek] = useState<YourWeekEntry | null>(null);
    const [yourWeekExpanded, setYourWeekExpanded] = useState(false);
    const yourWeekGeneration = useRef(0);

    const readYourWeek = useCallback((generation: number) => {
        GetYourWeekForDate(dateStr).then((entry) => {
            if (yourWeekGeneration.current === generation) setYourWeek(entry);
        }).catch(() => {
            if (yourWeekGeneration.current === generation) setYourWeek(null);
        });
    }, [dateStr]);

    useEffect(() => {
        const generation = ++yourWeekGeneration.current;
        readYourWeek(generation);
        setYourWeekExpanded(false);
        return () => { yourWeekGeneration.current++; };
    }, [readYourWeek]);

    // The cached week at once; Go starts the week's summary in the background
    // when its rules say one is due, and reports it in AutoStatus.
    const loadSummary = useCallback(async () => {
        const generation = summaryGeneration.current;
        setLoading(true);
        try {
            const result = await EnsureWeeklySummary(dateStr);
            if (generation === summaryGeneration.current) setWeeklyData(result);
        } catch {
            if (generation === summaryGeneration.current) setWeeklyData(null);
        } finally {
            if (generation === summaryGeneration.current) setLoading(false);
        }
    }, [dateStr]);

    usePagePrivacyWithdrawal(useCallback(() => {
        summaryGeneration.current += 1;
        const yourWeekRequest = ++yourWeekGeneration.current;
        setWeeklyData(null);
        setDaySentences(new Map());
        setYourWeek(null);
        setYourWeekExpanded(false);
        void loadSummary();
        readYourWeek(yourWeekRequest);
    }, [loadSummary, readYourWeek]));

    useEffect(() => {
        summaryGeneration.current += 1;
        loadSummary();
    }, [loadSummary]);

    useEffect(() => {
        const cleanup1 = EventsOn('activity:summary-ready', () => {
            setGenerating(false);
            loadSummary();
        });
        const cleanup2 = EventsOn('activity:summary-generating', () => setGenerating(true));
        const cleanup3 = EventsOn('activity:summary-error', () => {
            setGenerating(false);
            loadSummary();
        });
        return () => {
            cleanup1();
            cleanup2();
            cleanup3();
        };
    }, [loadSummary]);

    // One-line digest previews for the per-day rows.
    useEffect(() => {
        const days = weeklyData?.dailyStats ?? [];
        if (days.length === 0) {
            setDaySentences(new Map());
            return;
        }
        let cancelled = false;
        const generation = summaryGeneration.current;
        Promise.all(days.map((day) => GetDailySummary(day.date).catch(() => null))).then((summaries) => {
            if (cancelled || generation !== summaryGeneration.current) return;
            const next = new Map<string, string>();
            summaries.forEach((summary, i) => {
                next.set(days[i].date, summary ? firstDigestSentence(summary.aiSummary ?? '') : '');
            });
            setDaySentences(next);
        });
        return () => {
            cancelled = true;
        };
    }, [weeklyData]);

    const handleGenerate = useCallback(async () => {
        const generation = summaryGeneration.current;
        setGenerating(true);
        try {
            await GenerateWeeklySummary(dateStr);
            if (generation !== summaryGeneration.current) return;
            await loadSummary();
        } catch (err) {
            if (generation === summaryGeneration.current) {
                toast.error(err instanceof Error ? err.message : String(err));
            }
        } finally {
            setGenerating(false);
        }
    }, [dateStr, loadSummary]);

    if (loading) {
        return (
            <div className="flex-1 flex items-center justify-center">
                <Loader2 className="h-5 w-5 animate-spin text-muted" />
            </div>
        );
    }

    // totalActiveMs can be 0 because (a) the week truly had no activity, or
    // (b) session rows were rotated out of the local cache cap while the
    // cached AI weekly digest stuck around. Only short-circuit when both are
    // absent — otherwise fall through so the digest still shows.
    if (!weeklyData || (weeklyData.totalActiveMs === 0 && !weeklyData.aiSummary)) {
        return (
            <div className="flex-1 flex items-center justify-center text-muted">
                <div className="w-full max-w-2xl px-6 py-6">
                    {yourWeek?.week && (
                        <YourWeekActivityRow
                            entry={yourWeek}
                            expanded={yourWeekExpanded}
                            onToggle={() => setYourWeekExpanded((value) => !value)}
                            handlers={yourWeekHandlers}
                            onCard={onYourWeekCard}
                            t={t}
                            language={i18n.language}
                        />
                    )}
                    <p className="text-[13px]">{t('summary.noWeeklyActivity')}</p>
                </div>
            </div>
        );
    }

    const weekLabel = t('summary.weekOf', {
        start: new Date(weeklyData.weekStart + 'T00:00:00').toLocaleDateString([], { month: 'short', day: 'numeric' }),
        end: new Date(weeklyData.weekEnd + 'T00:00:00').toLocaleDateString([], { month: 'short', day: 'numeric' }),
    }) as string;

    return (
        <div className="flex-1 overflow-y-auto">
            <div className="max-w-2xl mx-auto px-6 py-6 space-y-5">
                <header className="space-y-1.5">
                    <h2 className="text-[20px] font-semibold text-ink tracking-tight">{weekLabel}</h2>
                    <p className="text-[12px] text-muted">
                        {t('summary.meta.active', { value: formatDurationMs(weeklyData.totalActiveMs) })}
                        <span className="mx-1.5 text-muted/40">·</span>
                        {t('summary.meta.perDay', { value: formatDurationMs(weeklyData.dailyAvgMs) })}
                    </p>
                </header>

                <AISummaryCard
                    summary={weeklyData.aiSummary}
                    summaryStatus={weeklyData.aiSummaryStatus}
                    cachedLanguage={weeklyData.aiSummaryLanguage}
                    generating={generating}
                    autoStatus={weeklyData.autoStatus}
                    onGenerate={handleGenerate}
                    onAskAI={onAskAboutDate ? () => onAskAboutDate(dateStr, 'weekly') : undefined}
                    scope="weekly"
                />

                {yourWeek?.week && (
                    <YourWeekActivityRow
                        entry={yourWeek}
                        expanded={yourWeekExpanded}
                        onToggle={() => setYourWeekExpanded((value) => !value)}
                        handlers={yourWeekHandlers}
                        onCard={onYourWeekCard}
                        t={t}
                        language={i18n.language}
                    />
                )}

                {weeklyData.dailyStats.length > 0 && (
                    <ul className="divide-y divide-border/60 rounded-xl border border-border bg-panel overflow-hidden">
                        {weeklyData.dailyStats.map((day) => {
                            const sentence = daySentences.get(day.date) ?? '';
                            return (
                                <li key={day.date}>
                                    <button
                                        type="button"
                                        onClick={() => onOpenDay(day.date)}
                                        className="w-full flex items-center gap-3 px-4 py-3 text-left hover:bg-ink/[0.02] transition-colors"
                                    >
                                        <span className="w-24 shrink-0 text-[12px] font-medium text-ink tabular-nums">
                                            {new Date(day.date + 'T00:00:00').toLocaleDateString([], {
                                                weekday: 'short',
                                                month: 'short',
                                                day: 'numeric',
                                            })}
                                        </span>
                                        <span className="flex-1 min-w-0 text-[12px] text-muted truncate">
                                            {sentence}
                                        </span>
                                        <span className="shrink-0 text-[11px] text-muted tabular-nums">
                                            {day.activeMs > 0 ? formatDurationMs(day.activeMs) : '—'}
                                        </span>
                                        <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted/60" />
                                    </button>
                                </li>
                            );
                        })}
                    </ul>
                )}
            </div>
        </div>
    );
}

function YourWeekActivityRow({
    entry,
    expanded,
    onToggle,
    handlers,
    onCard,
    t,
    language,
}: {
    entry: YourWeekEntry;
    expanded: boolean;
    onToggle: () => void;
    handlers?: TodayActionHandlers;
    onCard?: (briefId: string, card: MobileBriefCard, ask?: boolean) => void;
    t: TFunction<'activity'>;
    language: string;
}) {
    const week = entry.week;
    if (!week) return null;
    const date = new Intl.DateTimeFormat(language, { month: 'short', day: 'numeric' }).format(new Date(`${week.start}T12:00:00`));
    return (
        <section className="rounded-xl border border-border bg-panel p-3" data-testid="activity-your-week">
            <button type="button" className="w-full rounded-lg px-2 py-1.5 text-left hover:bg-ink/[0.02]" onClick={onToggle} aria-expanded={expanded}>
                <span className="block text-[13px] font-medium text-ink">{t('yourWeek.weekOf', { date })}</span>
                <span className="mt-0.5 block text-[12px] text-muted">{week.lead}</span>
            </button>
            {expanded && handlers && (
                <div className="px-2 pb-2 pt-4">
                    <YourWeekCard
                        week={week}
                        cards={entry.cards ?? []}
                        timeZone={entry.timeZone}
                        handlers={handlers}
                        onCard={(card) => onCard?.(entry.briefId ?? '', card)}
                        onAskCard={(card) => onCard?.(entry.briefId ?? '', card, true)}
                        hideTurnOff
                    />
                </div>
            )}
        </section>
    );
}
