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
    if (!count || count <= 0) return '';
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

/** "40 min" or "1h 6m" — the mean run of the observed sessions. */
export function candidateDurationLabel(t: KnowledgeT, avgDurationMs: number): string {
    const total = Math.max(1, Math.round(avgDurationMs / 60000));
    if (total < 60) return t('candidate.durationMin', { count: total });
    const hours = Math.floor(total / 60);
    const minutes = total % 60;
    return minutes === 0 ? t('candidate.durationH', { hours }) : t('candidate.durationHm', { hours, minutes });
}


export interface KnowHowFields {
    title: string;
    trigger: string;
    steps: string[];
}

const FRONTMATTER = /^---\n([\s\S]*?)\n---\n?/;
const STEPS_HEADING = '## Key Insights';

function unquote(value: string): string {
    const v = value.trim();
    if (v.startsWith('"') && v.endsWith('"')) {
        try {
            return JSON.parse(v) as string;
        } catch {
            return v.slice(1, -1);
        }
    }
    if (v.startsWith("'") && v.endsWith("'")) return v.slice(1, -1).replace(/''/g, "'");
    return v;
}

function sectionRange(lines: string[]): [number, number] | null {
    const start = lines.findIndex((line) => line.trim() === STEPS_HEADING);
    if (start < 0) return null;
    let end = start + 1;
    while (end < lines.length && !/^#{1,2} /.test(lines[end])) end++;
    return [start, end];
}

/** The fields Edit shows (W14), read from the stored file; the header never shows. */
export function knowHowFields(content: string, fallback: KnowHowFields): KnowHowFields {
    const fm = FRONTMATTER.exec(content);
    const field = (key: string) => {
        const line = fm?.[1].split('\n').find((l) => l.startsWith(`${key}:`));
        return line === undefined ? undefined : unquote(line.slice(key.length + 1));
    };
    const body = fm ? content.slice(fm[0].length) : content;
    const lines = body.split('\n');
    const range = sectionRange(lines);
    const steps = range
        ? lines.slice(range[0] + 1, range[1]).map((l) => l.replace(/^\s*(?:[-*]|\d+[.)])\s+/, '').trim()).filter(Boolean)
        : fallback.steps;
    const heading = lines.find((l) => /^# /.test(l));
    return {
        title: field('title') ?? (heading ? heading.slice(2).trim() : fallback.title),
        trigger: field('trigger') ?? fallback.trigger,
        steps,
    };
}

/** Writes the edited fields back into the file, keeping its header and every
 *  other section as they were. */
export function applyKnowHowFields(content: string, fields: KnowHowFields): string {
    const steps = fields.steps.map((s) => s.trim()).filter(Boolean);
    const fm = FRONTMATTER.exec(content);
    let header = '';
    let body = content;
    if (fm) {
        const keep = fm[1].split('\n');
        const set = (key: string, value: string) => {
            const i = keep.findIndex((l) => l.startsWith(`${key}:`));
            const line = `${key}: ${JSON.stringify(value)}`;
            if (i >= 0) keep[i] = line;
            else if (value) keep.push(line);
        };
        set('title', fields.title.trim());
        set('trigger', fields.trigger.trim());
        header = `---\n${keep.join('\n')}\n---\n`;
        body = content.slice(fm[0].length);
    }
    const lines = body.split('\n');
    const h = lines.findIndex((l) => /^# /.test(l));
    if (h >= 0 && fields.title.trim()) lines[h] = `# ${fields.title.trim()}`;
    const block = [STEPS_HEADING, ...steps.map((s) => `- ${s}`), ''];
    const range = sectionRange(lines);
    if (range) lines.splice(range[0], range[1] - range[0], ...block);
    else lines.push('', ...block);
    return header + lines.join('\n').replace(/\n{3,}/g, '\n\n');
}

/** What Turn into a playbook asks the author for: the how-to in its own words. */
export function playbookRequest(card: Pick<main.KnowHowCard, 'title' | 'takeaway' | 'trigger' | 'points'>): string {
    const lines = [card.title, card.takeaway, card.trigger, ...(card.points ?? []).map((point, i) => `${i + 1}. ${point}`)];
    return lines.map((line) => line?.trim()).filter(Boolean).join('\n');
}
