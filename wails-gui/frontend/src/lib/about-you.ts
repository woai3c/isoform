import type { TFunction } from 'i18next';
import type { AboutYouBelief, EntryUses } from '@/lib/wails-shim-app';
import { calendarDaysAgo } from '@/lib/memory-content';

export type BeliefStatus = AboutYouBelief['status'];
export type UseSurface = EntryUses['surfaces'][number]['surface'];

/** How long a moment lasted, in words; nothing under a minute. */
export function durationWords(t: TFunction<'memory'>, ms: number): string {
    if (!Number.isFinite(ms) || ms < 60_000) return '';
    const minutes = Math.round(ms / 60_000);
    if (minutes < 60) return t('aboutYou.duration.minutes', { count: minutes });
    return t('aboutYou.duration.hours', { count: Math.round(minutes / 60) });
}

/** The header line: how many beliefs, over how much of the person's work. */
export function beliefSummary(
    t: TFunction<'memory'>,
    count: number,
    earliestMs: number,
    now = new Date(),
): string {
    const days = earliestMs > 0 ? calendarDaysAgo(earliestMs, now) : 0;
    if (days === 0) return t('aboutYou.summary.beliefsToday', { count });
    const span =
        days < 14
            ? t('aboutYou.span.days', { count: days + 1 })
            : days < 60
              ? t('aboutYou.span.weeks', { count: Math.round(days / 7) })
              : t('aboutYou.span.months', { count: Math.round(days / 30) });
    return t('aboutYou.summary.beliefs', { count, span });
}

export const USE_SURFACES: UseSurface[] = ['chat', 'today_chat', 'brief', 'assistant_tool'];
