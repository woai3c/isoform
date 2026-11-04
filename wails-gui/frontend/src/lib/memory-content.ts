import type { TFunction } from 'i18next';
import type { main } from '../../wailsjs/go/models';

export type MemoryKind = 'learned' | 'preference' | 'person';
export const MEMORY_KINDS: MemoryKind[] = ['learned', 'preference', 'person'];
const systemField =
    /^(Dimension|Source|Updated|Activity Session|Read Provenance|Apps|UUID|ID|Type|Date|Timestamp|Reassigned to|MergedFrom|Merged From|MergedFromRefs)$/i;
const uuid = /[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}/gi;

export function humanMemoryText(text: string): string {
    return text
        .replace(uuid, '')
        .replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')
        .replace(/\*\*|__|`/g, '')
        .replace(/\s+/g, ' ')
        .trim();
}

export type MemoryPoint = {
    id: string;
    text: string;
    start: number;
    end: number;
    lineStart: number;
    lineEnd: number;
};

/** Identity survives unrelated insertions; offsets preserve the source envelope. */
export function memoryPoints(content: string): MemoryPoint[] {
    const points: MemoryPoint[] = [];
    const occurrences = new Map<string, number>();
    let yaml = false;
    let metadataChildren = false;
    let offset = 0;
    for (const line of content.split('\n')) {
        const lineStart = offset;
        offset += line.length + 1;
        if (lineStart === 0 && line.trim() === '---') {
            yaml = true;
            continue;
        }
        if (yaml) {
            if (line.trim() === '---') yaml = false;
            continue;
        }
        const field = line.trim().match(/^- \*\*(.+?)\*\*:/)?.[1];
        if (field && systemField.test(field)) {
            metadataChildren = true;
            continue;
        }
        if (metadataChildren && /^\s+[-*] /.test(line)) continue;
        if (line.trim()) metadataChildren = false;
        if (!line.trim() || /^\s*(#{1,6}\s|```|---\s*$)/.test(line)) continue;
        const prefix = line.match(/^\s*(?:[-*+]\s+|\d+\.\s+|>\s*)?/)?.[0] ?? '';
        const raw = line.slice(prefix.length);
        const segmenter = new Intl.Segmenter(undefined, {
            granularity: 'sentence',
        });
        for (const { segment, index } of segmenter.segment(raw)) {
            const leading = segment.length - segment.trimStart().length;
            const text = segment.trim();
            if (!humanMemoryText(text)) continue;
            const occurrence = occurrences.get(text) ?? 0;
            occurrences.set(text, occurrence + 1);
            points.push({
                id: JSON.stringify([text, occurrence]),
                text,
                start: lineStart + prefix.length + index + leading,
                end:
                    lineStart +
                    prefix.length +
                    index +
                    segment.trimEnd().length,
                lineStart,
                lineEnd: Math.min(offset, content.length),
            });
        }
    }
    return points;
}

// Mirrors beliefInSecondPerson in about_you_second_person.go: a belief reads
// in second person (about-you v1.0.1 Y28), so "Uses design reviews…" and
// "I use design reviews…" both read "You use design reviews…".
const SELF_VERBS = new Set(('block decline get give guard make protect put say set take think try wake answer ask attend avoid batch build call care check choose code commit debug delegate deploy design dictate draft drink edit email enjoy exercise expect favor favour find fix follow gather group hand handle coordinate keep lead learn like listen live log love manage meet merge monitor need open organize plan prefer prioritize push read record refactor rely reply report review run schedule send share ship sign skip sleep speak split start study talk teach tend test track travel treat trust type use value walk want watch work write').split(' '));
const IRREGULAR_VERBS: Record<string, string> = { is: 'are', was: 'were', has: 'have', does: 'do', goes: 'go' };

function baseVerb(word: string, capitalized = true): string | null {
    if (capitalized && !/^\p{Lu}/u.test(word)) return null;
    const lower = word.toLowerCase();
    if (IRREGULAR_VERBS[lower]) return IRREGULAR_VERBS[lower];
    const cut = lower.lastIndexOf('-');
    const prefix = cut >= 0 ? lower.slice(0, cut + 1) : '';
    const verb = lower.slice(cut + 1);
    const candidates = [
        verb.endsWith('ies') ? `${verb.slice(0, -3)}y` : '',
        verb.endsWith('es') ? verb.slice(0, -2) : '',
        verb.endsWith('s') && !verb.endsWith('ss') ? verb.slice(0, -1) : '',
    ];
    const base = candidates.find((candidate) => candidate && SELF_VERBS.has(candidate));
    return base ? prefix + base : null;
}

// v1.0.2 Y28: every verb agrees with "you", so "…, and declines meetings"
// reads "…, and decline meetings". A conjunction right after a plural noun
// ("notes and plans") is a list, so it stays.
function agreeWithYou(sentence: string): string {
    if (!/^You /.test(sentence)) return sentence;
    return sentence.replace(/(\S+)(\s+(?:and|or|but|then)\s+)(\p{Ll}[\p{L}-]*)/gu, (match, before: string, joint: string, word: string) => {
        if (!IRREGULAR_VERBS[word] && !before.endsWith(',') && /s$/i.test(before) && !new RegExp(`\\b${word}\\s+(?:them|it|him|her)\\b`, 'i').test(sentence)) return match;
        const base = baseVerb(word, false);
        return base ? `${before}${joint}${base}` : match;
    });
}

export function beliefInSecondPerson(text: string): string {
    return agreeWithYou(firstPersonToYou(text));
}

function firstPersonToYou(text: string): string {
    let trimmed = text.trim();
    const unquoted = trimmed.replace(/^["“”'‘’]+|["“”'‘’]+$/g, '').trim();
    if (unquoted) trimmed = unquoted;
    const space = trimmed.indexOf(' ');
    if (space <= 0) return trimmed;
    const first = trimmed.slice(0, space);
    const rest = trimmed.slice(space + 1);
    if (first === 'I') {
        const [verb, ...after] = rest.split(' ');
        const agreed = verb === 'am' ? 'are' : verb === 'was' ? 'were' : verb;
        return ['You', agreed, ...after].join(' ');
    }
    if (first === "I'm" || first === 'I’m') return `You're ${rest}`;
    if (first === 'My') return `Your ${rest}`;
    const base = baseVerb(first);
    return base ? `You ${base} ${rest}` : trimmed;
}

export function memoryText(entry: main.MemoryEntry): string {
    return memoryPoints(entry.content)
        .map((point) => humanMemoryText(point.text))
        .join(' ');
}

export function memoryLabel(
    entry: main.MemoryEntry,
    t: TFunction<'memory'>,
): string {
    const title = humanMemoryText(entry.title);
    const typeLabel =
        /^(?:(?:isoform|yansu) )?memory(?: points)?$|^user_insight$|^topic_highlight$|^deep_user_model$|^agentic_memory$|^user insights?$|^topic highlights?$|^deep user model$|^ai operational$|^a preference$|^a fact about a person$|^something i learned about you$|^记忆(?:要点)?$/i;
    if (title && entry.title !== entry.id && !typeLabel.test(title))
        return beliefInSecondPerson(title);
    const heading = entry.content.match(/^# (.+)$/m)?.[1];
    if (
        heading &&
        !typeLabel.test(heading) &&
        heading !== entry.id &&
        humanMemoryText(heading)
    )
        return beliefInSecondPerson(humanMemoryText(heading));
    return (
        beliefInSecondPerson(
            humanMemoryText(memoryPoints(entry.content)[0]?.text ?? '').slice(0, 120),
        ) || t('row.empty')
    );
}

export function memoryKind(entry: main.MemoryEntry): MemoryKind {
    if (/^- \*\*Reassigned to\*\*:/m.test(entry.content)) return 'person';
    if (
        entry.dimension === 'agentic_memory' ||
        /\b(?:prefer|prefers|preference)\b|偏好|更喜欢/.test(
            memoryText(entry).toLowerCase(),
        )
    )
        return 'preference';
    return 'learned';
}

/** Confirmed by the person: That's me wrote confirmed_at into the file's
 *  frontmatter, or they edited it. Used when the About you read is missing. */
export function memoryConfirmed(entry: main.MemoryEntry): boolean {
    if (entry.source === 'user_edit') return true;
    const header = entry.content.match(/^---\r?\n([\s\S]*?)\r?\n---/)?.[1] ?? '';
    return /^['"]?confirmed_at['"]?\s*:\s*\S/m.test(header);
}

export function calendarDaysAgo(ms: number, now = new Date()): number {
    const date = new Date(ms);
    return Math.max(
        0,
        Math.round(
            (Date.UTC(now.getFullYear(), now.getMonth(), now.getDate()) -
                Date.UTC(date.getFullYear(), date.getMonth(), date.getDate())) /
                86400000,
        ),
    );
}

export function memoryDate(
    t: TFunction<'memory'>,
    ms: number,
    locale: string,
): string {
    if (!Number.isFinite(ms) || ms <= 0) return t('detail.dateUnknown');
    const days = calendarDaysAgo(ms);
    if (days === 0) return t('relativeTime.today');
    if (days === 1) return t('relativeTime.yesterday');
    if (days === 2) return t('detail.dayBeforeYesterday');
    return new Date(ms).toLocaleDateString(locale, {
        year: 'numeric',
        month: 'short',
        day: 'numeric',
    });
}

/** Plain words for how long ago, never a clock time: "yesterday", "3 days ago". */
export function relativeWords(t: TFunction<'memory'>, ms: number, now = new Date()): string {
    const days = calendarDaysAgo(ms, now);
    if (days === 0) return t('relativeTime.today');
    if (days === 1) return t('relativeTime.yesterday');
    if (days < 7) return t('relativeTime.daysAgo', { count: days });
    if (days < 14) return t('relativeTime.lastWeek');
    if (days < 30) return t('relativeTime.weeksAgo', { count: Math.floor(days / 7) });
    if (days < 365) return t('relativeTime.monthsAgo', { count: Math.floor(days / 30) });
    return t('relativeTime.yearsAgo', { count: Math.floor(days / 365) });
}

// Mirrors the Go presentation list (system_noise_apps.go): system processes
// that hold focus but never describe the person's work.
const systemNoiseApps = new Set([
    'loginwindow', 'windowmanager', 'window manager', 'systemuiserver', 'system ui server',
    'controlcenter', 'control center', 'notificationcenter', 'notification center', 'notificationcenterui',
    'dock', 'screensaverengine', 'screen saver engine', 'windowserver', 'window server', 'spotlight',
    'usernotificationcenter', 'user notification center',
]);

export function isSystemNoiseApp(name: string): boolean {
    return systemNoiseApps.has(name.toLowerCase().split(/\s+/).filter(Boolean).join(' '));
}

export function memoryApps(apps: string[]): string[] {
    const seen = new Set<string>();
    return apps
        .map((app) => humanMemoryText(app).replace(/^yansu$/i, 'Isoform'))
        .filter((app) => {
            const key = app.toLowerCase();
            if (!key || seen.has(key) || isSystemNoiseApp(app)) return false;
            seen.add(key);
            return true;
        })
        .slice(0, 3);
}
