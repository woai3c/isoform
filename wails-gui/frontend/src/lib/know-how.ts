import type { TFunction } from 'i18next';
import type { main } from '../../wailsjs/go/models';
import { calendarDaysAgo } from '@/lib/memory-content';

/** The kinds ListKnowHow returns; anything else reads as reference. */
export const KNOW_HOW_KINDS = ['decision', 'how_to', 'fix', 'gotcha', 'reference'] as const;
export type KnowHowKind = (typeof KNOW_HOW_KINDS)[number];
export type KnowHowFilter = 'all' | KnowHowKind;

export type TimeGroupKey = 'today' | 'yesterday' | 'thisWeek' | 'thisMonth' | 'earlier';
export const TIME_GROUP_ORDER: TimeGroupKey[] = ['today', 'yesterday', 'thisWeek', 'thisMonth', 'earlier'];

type KnowledgeT = TFunction<'knowledge'>;

export function knowHowKind(kind: string | undefined): KnowHowKind {
    return (KNOW_HOW_KINDS as readonly string[]).includes(kind ?? '') ? (kind as KnowHowKind) : 'reference';
}

/** "Used 4 times", "Used twice", "Not used yet". */
export function reuseLabel(t: KnowledgeT, count: number): string {
    if (!count || count <= 0) return t('reuse.none');
    if (count === 1) return t('reuse.once');
    if (count === 2) return t('reuse.twice');
    return t('reuse.many', { count });
}

/** Today, Yesterday or a short date; null when the time is unknown. */
export function dayInWords(t: KnowledgeT, ms: number, locale: string, now = new Date()): string | null {
    if (!Number.isFinite(ms) || ms <= 0) return null;
    const days = calendarDaysAgo(ms, now);
    if (days === 0) return t('when.today');
    if (days === 1) return t('when.yesterday');
    return shortDate(ms, locale, now);
}

export function shortDate(ms: number, locale: string, now = new Date()): string {
    const date = new Date(ms);
    return date.toLocaleDateString(locale, {
        month: 'short',
        day: 'numeric',
        ...(date.getFullYear() !== now.getFullYear() ? { year: 'numeric' } : {}),
    });
}

/** "Oct 19, 7:40 PM": the moment a version or an entry was saved. */
export function dateTimeInWords(ms: number, locale: string, now = new Date()): string {
    const date = new Date(ms);
    return date.toLocaleString(locale, {
        month: 'short',
        day: 'numeric',
        hour: 'numeric',
        minute: '2-digit',
        ...(date.getFullYear() !== now.getFullYear() ? { year: 'numeric' } : {}),
    });
}

/** The banner's "fixed on Oct 19" in words. */
export function matchWhen(t: KnowledgeT, ms: number, locale: string, now = new Date()): string {
    if (!Number.isFinite(ms) || ms <= 0) return t('match.when.unknown');
    const days = calendarDaysAgo(ms, now);
    if (days === 0) return t('match.when.today');
    if (days === 1) return t('match.when.yesterday');
    return t('match.when.date', { date: shortDate(ms, locale, now) });
}

/** The raw saved time, for Technical details only. */
export function savedTimestamp(ms: number): string {
    if (!Number.isFinite(ms) || ms <= 0) return '';
    return new Date(ms).toISOString().replace(/:\d{2}\.\d{3}Z$/, 'Z');
}

export function timeGroup(ms: number, now = new Date()): TimeGroupKey {
    if (!Number.isFinite(ms) || ms <= 0) return 'earlier';
    const days = calendarDaysAgo(ms, now);
    if (days === 0) return 'today';
    if (days === 1) return 'yesterday';
    if (days < 7) return 'thisWeek';
    const date = new Date(ms);
    if (date.getFullYear() === now.getFullYear() && date.getMonth() === now.getMonth()) return 'thisMonth';
    return 'earlier';
}

export function groupByTime<T extends { savedAt: number }>(cards: T[], now = new Date()): Array<[TimeGroupKey, T[]]> {
    const groups = new Map<TimeGroupKey, T[]>(TIME_GROUP_ORDER.map((key) => [key, []]));
    for (const card of cards) groups.get(timeGroup(card.savedAt, now))!.push(card);
    return [...groups.entries()].filter(([, items]) => items.length > 0);
}

/** Entries saved in the last seven days, today included. */
export function savedThisWeek(cards: Array<{ savedAt: number }>, now = new Date()): number {
    return cards.filter((card) => card.savedAt > 0 && calendarDaysAgo(card.savedAt, now) < 7).length;
}

export function topSources(cards: Array<{ apps: string[] | null }>, limit: number): Array<[string, number]> {
    const counts = new Map<string, number>();
    for (const card of cards) {
        for (const app of new Set((card.apps ?? []).map((name) => name.trim()).filter(Boolean))) {
            counts.set(app, (counts.get(app) ?? 0) + 1);
        }
    }
    return [...counts.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).slice(0, limit);
}

export function matchesKeyword(card: main.KnowHowCard, content: string, keyword: string): boolean {
    const kw = keyword.trim().toLowerCase();
    if (!kw) return true;
    return [card.title, card.takeaway, ...(card.points ?? []), ...(card.apps ?? []), content]
        .some((text) => (text ?? '').toLowerCase().includes(kw));
}

// Product names of the messaging sources the conversation generator writes.
const MESSAGE_SOURCES: Record<string, string> = {
    slack: 'Slack',
    discord: 'Discord',
    feishu: 'Feishu',
    telegram: 'Telegram',
    wechat: 'WeChat',
    teams: 'Teams',
    ringcentral: 'RingCentral',
    gmail: 'Gmail',
};

/** One plain sentence for "Where it came from"; the raw source stays in
 *  Technical details. Observed how-tos name their session count. */
export function originSentence(t: KnowledgeT, source: string, sessionCount?: number): string {
    const key = source.trim().toLowerCase();
    switch (key) {
        case 'observed':
            return t('detail.sources.observed', { count: sessionCount ?? 1 });
        case 'activity':
        case 'conversation':
        case 'meeting':
        case 'shadow_task_evaluation':
            return t(`detail.sources.${key}`);
        default:
            return MESSAGE_SOURCES[key]
                ? t('detail.sources.messages', { app: MESSAGE_SOURCES[key] })
                : t('detail.sources.other');
    }
}

/** The scope chip next to the kind. Only mine exists today; the other scopes
 *  arrive with the ladder. */
export function scopeLabel(t: KnowledgeT, scope: string | undefined): string {
    const value = (scope ?? '').trim();
    switch (value) {
        case 'topic':
        case 'team':
        case 'company':
            return t(`scope.${value}`);
        default:
            return t('scope.mine');
    }
}

/** The proposal's dates in words: "Oct 4, Oct 11, Oct 18 and today". */
export function candidateDatesLabel(t: KnowledgeT, occurredAtMs: number[], locale: string, now = new Date()): string {
    const parts = occurredAtMs.map((ms) => {
        const days = calendarDaysAgo(ms, now);
        if (days === 0) return t('candidate.today');
        if (days === 1) return t('candidate.yesterday');
        return shortDate(ms, locale, now);
    });
    return new Intl.ListFormat(locale, { style: 'long', type: 'conjunction' }).format(parts);
}

/** "40 minutes" — the mean run of the observed sessions. */
export function candidateDurationLabel(t: KnowledgeT, avgDurationMs: number): string {
    const minutes = Math.max(1, Math.round(avgDurationMs / 60000));
    return t('candidate.durationMin', { count: minutes });
}

/** The request a card's button puts in the main chat box. Nothing is sent:
 *  the person reads it, edits it and presses Send. */
export function preparedRequest(t: KnowledgeT, card: Pick<main.KnowHowCard, 'kind' | 'title' | 'takeaway'>, action: 'cta' | 'ask'): string {
    if (action === 'ask') return t('prepare.ask', { title: card.title });
    const kind = knowHowKind(card.kind);
    const lead = kind === 'reference' ? t('prepare.ask', { title: card.title }) : t(`prepare.${kind}`, { title: card.title });
    const takeaway = card.takeaway?.trim();
    return takeaway && kind !== 'reference' ? `${lead} ${t('prepare.takeaway', { takeaway })}` : lead;
}
