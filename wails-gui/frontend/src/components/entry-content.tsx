import { useEffect, useMemo, useState } from 'react';
import type React from 'react';
import { ChevronDown, ChevronRight } from 'lucide-react';
import { cn } from '@/lib/utils';
import { MarkdownContent } from '@/components/markdown-content';

interface EntryContentProps {
    content: string;
    highlightKeyword?: string;
    /** Skip the leading # title and consecutive - **Key**: Value metadata lines */
    skipHeader?: boolean;
    onOpenActivitySession?: (sessionId: string) => void;
}

export interface SessionBullet {
    id: string;
    time: string;
    apps: string;
}

export interface SessionGroup {
    /** e.g. "2026-04-20 11:30" — when this update was written */
    updateTime: string;
    /** e.g. "initial" | "update" | "pre-migration" */
    updateKind: string;
    sessions: SessionBullet[];
}

export interface FrontmatterItem {
    key: string;
    value: string;
    /**
     * Flat session list. Populated for knowledge entries (never grouped) and
     * for legacy memory entries that haven't been re-touched since the
     * grouped-format migration. `groups` is the preferred shape for memory —
     * if both are present, prefer `groups`.
     */
    sessions?: SessionBullet[];
    /**
     * Grouped-by-update sessions. Memory entries use this shape after any
     * update, so trajectory (which group triggered which change) is visible.
     */
    groups?: SessionGroup[];
}

function unquoteYamlScalar(value: string): string {
    const trimmed = value.trim();
    if (
        (trimmed.startsWith('"') && trimmed.endsWith('"')) ||
        (trimmed.startsWith("'") && trimmed.endsWith("'"))
    ) {
        return trimmed.slice(1, -1);
    }
    return trimmed;
}

function parseYamlListInline(value: string): string[] {
    const trimmed = value.trim();
    if (!trimmed.startsWith('[') || !trimmed.endsWith(']')) return [];
    return trimmed
        .slice(1, -1)
        .split(',')
        .map((part) => unquoteYamlScalar(part))
        .filter(Boolean);
}

function splitOKFFrontmatter(content: string): {
    frontmatter: Record<string, string | string[]>;
    body: string;
    endLine: number;
} | null {
    const lines = content.replace(/^\uFEFF/, '').split('\n');
    if (lines[0]?.trim() !== '---') return null;
    let end = -1;
    for (let i = 1; i < lines.length; i++) {
        if (lines[i].trim() === '---') {
            end = i;
            break;
        }
    }
    if (end < 0) return null;

    const frontmatter: Record<string, string | string[]> = {};
    for (let i = 1; i < end; i++) {
        const line = lines[i];
        const match = line.match(/^([A-Za-z0-9_-]+):\s*(.*)$/);
        if (!match) continue;
        const key = match[1];
        const value = match[2].trim();
        if (value === '') {
            const list: string[] = [];
            let j = i + 1;
            while (j < end) {
                const item = lines[j].match(/^\s*-\s+(.+)$/);
                if (!item) break;
                list.push(unquoteYamlScalar(item[1]));
                j++;
            }
            frontmatter[key] = list.filter(Boolean);
            i = j - 1;
            continue;
        }
        const inlineList = parseYamlListInline(value);
        frontmatter[key] = inlineList.length > 0 ? inlineList : unquoteYamlScalar(value);
    }

    return {
        frontmatter,
        body: lines.slice(end + 1).join('\n').replace(/^\n+/, ''),
        endLine: end,
    };
}

function okfValue(
    frontmatter: Record<string, string | string[]>,
    key: string,
): string {
    const value = frontmatter[key];
    return Array.isArray(value) ? value.join(', ') : value || '';
}

function okfList(
    frontmatter: Record<string, string | string[]>,
    key: string,
): string[] {
    const value = frontmatter[key];
    if (Array.isArray(value)) return value;
    return value ? value.split(',').map((part) => part.trim()).filter(Boolean) : [];
}

/**
 * Parse the leading `- **Key**: Value` block of a memory / knowledge markdown
 * body. Handles three accepted shapes for `Activity Session`:
 *
 *   1. Legacy single-line: `- **Activity Session**: <id>`
 *   2. Flat nested bullet list (knowledge + pre-migration memory)
 *   3. Grouped-by-update (current memory format) — sessions nested under
 *      `- **Update <ts>** (<kind>)` headers
 *
 * Shape 3 populates `groups`; shapes 1 and 2 populate `sessions`. Callers
 * that don't care about grouping can always derive a flat list by
 * flattening `groups` themselves.
 */
export function parseFrontmatter(content: string): FrontmatterItem[] {
    const okf = splitOKFFrontmatter(content);
    if (okf) {
        const items: FrontmatterItem[] = [];
        const type = okfValue(okf.frontmatter, 'type');
        const timestamp = okfValue(okf.frontmatter, 'timestamp');
        const source = okfValue(okf.frontmatter, 'source');
        const dimension = okfValue(okf.frontmatter, 'dimension');
        const apps = okfList(okf.frontmatter, 'apps');
        const sessions = okfList(okf.frontmatter, 'activity_sessions');
        if (type) items.push({ key: 'Type', value: type });
        if (timestamp) items.push({ key: 'Timestamp', value: timestamp });
        if (source) items.push({ key: 'Source', value: source });
        if (dimension) items.push({ key: 'Dimension', value: dimension });
        if (apps.length > 0) items.push({ key: 'Apps', value: apps.join(', ') });
        if (sessions.length > 0) {
            items.push({
                key: 'Activity Session',
                value: '',
                sessions: sessions.map((id) => ({ id, time: '', apps: '' })),
            });
        }
        return items;
    }

    const lines = content.split('\n');
    let i = 0;
    while (i < lines.length && lines[i].trim() === '') i++;
    if (i < lines.length && lines[i].startsWith('# ')) i++;

    const out: FrontmatterItem[] = [];
    while (i < lines.length) {
        const trimmed = lines[i].trim();
        if (trimmed === '') {
            i++;
            continue;
        }
        const match = trimmed.match(/^- \*\*(.+?)\*\*:\s*(.*)$/);
        if (!match) break;
        const key = match[1];
        const value = match[2].trim();

        if (key === 'Activity Session') {
            if (value !== '') {
                // Legacy single-line.
                out.push({
                    key,
                    value,
                    sessions: [{ id: value, time: '', apps: '' }],
                });
                i++;
                continue;
            }

            const flatSessions: SessionBullet[] = [];
            const groups: SessionGroup[] = [];
            let currentGroup: SessionGroup | null = null;

            let j = i + 1;
            while (j < lines.length) {
                const raw = lines[j];
                if (raw.trim() === '') {
                    j++;
                    continue;
                }
                const isIndentedBullet = /^\s+- /.test(raw);
                if (!isIndentedBullet) break;

                const header = parseSessionGroupHeader(raw);
                if (header) {
                    currentGroup = { ...header, sessions: [] };
                    groups.push(currentGroup);
                    i = j;
                    j++;
                    continue;
                }

                const bullet = parseSessionBullet(raw);
                if (bullet) {
                    if (currentGroup) {
                        currentGroup.sessions.push(bullet);
                    } else {
                        flatSessions.push(bullet);
                    }
                }
                i = j;
                j++;
            }

            if (groups.length > 0) {
                out.push({ key, value, groups });
            } else {
                out.push({ key, value, sessions: flatSessions });
            }
            i++;
            continue;
        }

        out.push({ key, value });
        i++;
    }
    return out;
}

/**
 * Parse a group header line like `  - **Update 2026-04-20 11:30** (initial)`.
 * Returns null when the line does not match, so the outer parser can fall
 * through to session-bullet handling.
 */
export function parseSessionGroupHeader(
    raw: string,
): { updateTime: string; updateKind: string } | null {
    const trimmed = raw.replace(/^\s+- /, '').trim();
    const match = trimmed.match(/^\*\*Update\s+(.+?)\*\*\s*\(([^)]+)\)\s*$/);
    if (!match) return null;
    return {
        updateTime: match[1].trim(),
        updateKind: match[2].trim(),
    };
}

/**
 * Flatten a frontmatter item's sessions — regardless of whether the parser
 * returned grouped or flat data. Callers that only need jump targets (e.g.
 * summary cards) use this to get a single-list view.
 */
export function flattenSessions(item: FrontmatterItem): SessionBullet[] {
    if (item.groups && item.groups.length > 0) {
        return item.groups.flatMap((g) => g.sessions);
    }
    return item.sessions ?? [];
}

/**
 * Parse one nested bullet like `  - <id> — 10:28, cmux, Obsidian` into its
 * structured pieces. Returns null when the line is malformed so the parser
 * can skip it without poisoning the rest of the list.
 */
export function parseSessionBullet(raw: string): SessionBullet | null {
    const trimmed = raw.replace(/^\s+- /, '').trim();
    if (!trimmed) return null;
    const emIdx = trimmed.indexOf(' — ');
    if (emIdx < 0) {
        return { id: trimmed, time: '', apps: '' };
    }
    const id = trimmed.slice(0, emIdx).trim();
    const rest = trimmed.slice(emIdx + 3).trim();
    const commaIdx = rest.indexOf(', ');
    if (commaIdx < 0) {
        return { id, time: rest, apps: '' };
    }
    return {
        id,
        time: rest.slice(0, commaIdx).trim(),
        apps: rest.slice(commaIdx + 2).trim(),
    };
}

interface SessionJumpListProps {
    sessions: SessionBullet[];
    onOpen: (sessionId: string) => void;
    /**
     * When true, hide the leading `HH:mm` time column to fit into narrower
     * surfaces (e.g., the memory version-history preview). The apps label
     * and session id tooltip are kept — we never surface a raw UUID as the
     * primary jump label.
     */
    compact?: boolean;
}

/**
 * Primary label for a session jump button. Prefers the apps list (most
 * recognisable — "Code, Chrome, 微信"), falls back to the `HH:mm` time if
 * apps are missing (legacy single-line `Activity Session` entries), and
 * finally degrades to a generic "Session" label when both are empty. The
 * raw UUID is intentionally never shown — it only appears in the button's
 * `title` tooltip for debugging.
 */
function sessionPrimaryLabel(s: SessionBullet): string {
    if (s.apps) return s.apps;
    if (s.time) return s.time;
    return 'Session';
}

/**
 * Renders a vertical list of contributing activity sessions as independently
 * clickable jump buttons. Each row shows `HH:mm  apps` (or just `apps` in
 * compact mode) so a user can pick which session to open by recognisable
 * context rather than by a UUID.
 */
export function SessionJumpList({ sessions, onOpen, compact }: SessionJumpListProps) {
    if (sessions.length === 0) return null;
    return (
        <div className="flex-1 min-w-0 space-y-0.5">
            {sessions.map((s) => {
                const showTimeColumn = !compact && !!s.time;
                const primary = sessionPrimaryLabel(s);
                const isFallbackLabel = !s.apps;
                return (
                    <button
                        key={s.id}
                        className="flex items-baseline gap-2 w-full min-w-0 text-left text-accent hover:underline cursor-pointer text-[11px]"
                        onClick={() => onOpen(s.id)}
                        title={s.id}
                    >
                        {showTimeColumn && (
                            <span className="text-muted tabular-nums shrink-0 w-10">
                                {s.time}
                            </span>
                        )}
                        <span
                            className={cn(
                                'block flex-1 min-w-0 truncate',
                                isFallbackLabel && 'italic text-muted',
                            )}
                        >
                            {primary}
                        </span>
                    </button>
                );
            })}
        </div>
    );
}

interface GroupedSessionJumpListProps {
    groups: SessionGroup[];
    onOpen: (sessionId: string) => void;
    /** See SessionJumpList.compact — hides the per-session time column. */
    compact?: boolean;
}

/**
 * Memory-entry session list rendered as grouped-by-update sections. Each
 * group is labelled with its update time + kind so the user can see which
 * group of sessions drove each change to the entry.
 *
 * How you do things pages should keep using `SessionJumpList` — knowledge stays flat
 * (facts don't need trajectory).
 */
export function GroupedSessionJumpList({
    groups,
    onOpen,
    compact,
}: GroupedSessionJumpListProps) {
    // Show newest update first. Sort a copy (don't mutate the caller's array).
    // `updateTime` is lexicographically sortable — values are `YYYY-MM-DD HH:mm`.
    const sortedGroups = useMemo(
        () => [...groups].sort((a, b) => b.updateTime.localeCompare(a.updateTime)),
        [groups],
    );

    if (sortedGroups.length === 0) return null;
    return (
        <div className="flex-1 min-w-0 space-y-1.5">
            {sortedGroups.map((group, idx) => (
                <div key={`${group.updateTime}-${idx}`} className="space-y-0.5">
                    <div className="flex items-baseline gap-1.5 text-[10px] text-muted">
                        <span className="tabular-nums">{group.updateTime}</span>
                        <span className="uppercase tracking-wide">
                            {group.updateKind}
                        </span>
                    </div>
                    <div className="pl-2 border-l border-subtle space-y-0.5">
                        {group.sessions.map((s) => {
                            const showTimeColumn = !compact && !!s.time;
                            const primary = sessionPrimaryLabel(s);
                            const isFallbackLabel = !s.apps;
                            return (
                                <button
                                    key={s.id}
                                    className="flex items-baseline gap-2 w-full min-w-0 text-left text-accent hover:underline cursor-pointer text-[11px]"
                                    onClick={() => onOpen(s.id)}
                                    title={s.id}
                                >
                                    {showTimeColumn && (
                                        <span className="text-muted tabular-nums shrink-0 w-10">
                                            {s.time}
                                        </span>
                                    )}
                                    <span
                                        className={cn(
                                            'block flex-1 min-w-0 truncate',
                                            isFallbackLabel && 'italic text-muted',
                                        )}
                                    >
                                        {primary}
                                    </span>
                                </button>
                            );
                        })}
                    </div>
                </div>
            ))}
        </div>
    );
}

/**
 * Wraps matching substrings in <mark> tags for search highlighting.
 * Case-insensitive, highlights all occurrences.
 *
 * TODO: Implement this function (~5-8 lines)
 *
 * Trade-offs to consider:
 * - Use string split (safe) vs regex (needs escaping for special chars like .*+)
 * - Return original text as-is when keyword is empty/whitespace
 * - Must handle multiple occurrences in a single string
 */
function highlightText(text: string, _keyword: string): React.ReactNode {
    // TODO: implement — see trade-offs in comment above
    return text;
}

/** Render a single text span, optionally with keyword highlighting */
function TextSpan({ text, keyword }: { text: string; keyword?: string }) {
    if (!keyword?.trim()) return <>{text}</>;
    return <>{highlightText(text, keyword)}</>;
}

/**
 * Split an entry's markdown into its frontmatter-style header (title +
 * `- **Key**: Value` metadata + nested Activity Session bullets) and the
 * free-form body that follows. Callers that want the minimal `EntryContent`
 * renderer for the metadata block but a real markdown renderer for the
 * body use this to slice the content in two.
 */
export function splitHeaderAndBody(content: string): { header: string; body: string } {
    const okf = splitOKFFrontmatter(content);
    if (okf) {
        return {
            header: content.split('\n').slice(0, okf.endLine + 1).join('\n'),
            body: okf.body,
        };
    }

    const lines = content.split('\n');
    const startLine = getHeaderLineCount(lines);
    return {
        header: lines.slice(0, startLine).join('\n'),
        body: lines.slice(startLine).join('\n'),
    };
}

/**
 * Renders frontmatter metadata (`- **Key**: Value`) for a memory / knowledge
 * entry as a labelled list. Activity Session rows go through
 * `GroupedSessionJumpList` / `SessionJumpList` so both the grouped-by-update
 * memory format and the flat knowledge format show up as clickable session
 * jumps — not as raw `**...**` markdown. Callers render the body separately.
 */
export function EntryMetadataList({
    content,
    onOpenActivitySession,
    compactSessions,
    noDivider,
    collapsibleSessions,
}: {
    content: string;
    onOpenActivitySession?: (sessionId: string) => void;
    /** Hide per-session time + apps in Activity Session rows. */
    compactSessions?: boolean;
    /**
     * Drop the bottom divider used to separate metadata from the body. Pass
     * this when the list is rendered as a standalone card (the card border
     * already provides separation).
     */
    noDivider?: boolean;
    /**
     * When true, the Activity Session row renders collapsed by default — the
     * label becomes a toggle showing the total count; clicking it reveals
     * the full session jump list. Used by the memory detail page where the
     * session list can get long enough to overwhelm the metadata block.
     */
    collapsibleSessions?: boolean;
}) {
    const items = useMemo(() => parseFrontmatter(content), [content]);
    const [sessionsExpanded, setSessionsExpanded] = useState(false);

    // Re-collapse when the underlying entry switches. `sessionsExpanded`
    // lives in component state, so without this effect the expanded flag
    // leaks across entry changes — navigating from memory A (where the
    // user expanded sessions) to memory B would show B's sessions already
    // expanded, violating the "collapsed by default" contract.
    useEffect(() => {
        setSessionsExpanded(false);
    }, [content]);

    if (items.length === 0) return null;

    return (
        <div className={cn('select-text', noDivider ? '' : 'mb-4 pb-4 border-b border-border/50')}>
            {items.map((item) => {
                const isSessionRow =
                    item.key === 'Activity Session' &&
                    onOpenActivitySession &&
                    ((item.groups && item.groups.length > 0) ||
                        (item.sessions && item.sessions.length > 0));

                if (isSessionRow) {
                    const totalSessions = item.groups && item.groups.length > 0
                        ? item.groups.reduce((sum, g) => sum + g.sessions.length, 0)
                        : (item.sessions?.length ?? 0);
                    const sessionList = item.groups && item.groups.length > 0 ? (
                        <GroupedSessionJumpList
                            groups={item.groups}
                            onOpen={onOpenActivitySession}
                            compact={compactSessions}
                        />
                    ) : (
                        <SessionJumpList
                            sessions={item.sessions ?? []}
                            onOpen={onOpenActivitySession}
                            compact={compactSessions}
                        />
                    );

                    if (!collapsibleSessions) {
                        return (
                            <div
                                key={item.key}
                                className="flex items-baseline gap-2 text-xs py-0.5"
                            >
                                <span className="font-medium text-muted shrink-0 min-w-[7rem]">
                                    {item.key}
                                </span>
                                {sessionList}
                            </div>
                        );
                    }

                    return (
                        <div key={item.key} className="text-xs py-0.5">
                            <button
                                type="button"
                                className="group flex items-baseline gap-2 w-full text-left cursor-pointer"
                                onClick={() => setSessionsExpanded((v) => !v)}
                                aria-expanded={sessionsExpanded}
                            >
                                <span className="font-medium text-muted shrink-0 min-w-[7rem] group-hover:text-ink transition-colors">
                                    {item.key}
                                </span>
                                <span className="text-muted/70 tabular-nums">
                                    ({totalSessions})
                                </span>
                                {sessionsExpanded ? (
                                    <ChevronDown className="h-3 w-3 shrink-0 self-center text-muted/60 group-hover:text-ink transition-colors" />
                                ) : (
                                    <ChevronRight className="h-3 w-3 shrink-0 self-center text-muted/60 group-hover:text-ink transition-colors" />
                                )}
                            </button>
                            {sessionsExpanded && (
                                <div className="mt-1 ml-[7rem]">{sessionList}</div>
                            )}
                        </div>
                    );
                }

                return (
                    <div
                        key={item.key}
                        className="flex items-baseline gap-2 text-xs py-0.5"
                    >
                        <span className="font-medium text-muted shrink-0 min-w-[7rem]">
                            {normalizeMetadataKey(item.key)}
                        </span>
                        <span className="text-ink break-words">
                            {item.value}
                        </span>
                    </div>
                );
            })}
        </div>
    );
}

/**
 * Memory uses `Updated` in its frontmatter, knowledge uses `Date`. Both mean
 * "when this entry was last written" — surface them as `Time` so the two
 * pages present the same metadata shape.
 */
function normalizeMetadataKey(key: string): string {
    if (key === 'Updated' || key === 'Date' || key === 'Timestamp') return 'Time';
    return key;
}

/**
 * Determine how many leading lines to skip when skipHeader is true.
 * Skips: first `# ` title line, then consecutive blank or `- **Key**: Value`
 * lines, plus the indented bullets that belong to an `Activity Session`
 * nested block (so the session list doesn't leak into the body preview).
 */
function getHeaderLineCount(lines: string[]): number {
    if (lines[0]?.trim() === '---') {
        for (let i = 1; i < lines.length; i++) {
            if (lines[i].trim() === '---') {
                let next = i + 1;
                while (next < lines.length && lines[next].trim() === '') next++;
                return next;
            }
        }
    }

    let i = 0;
    // Skip leading blank lines
    while (i < lines.length && lines[i].trim() === '') i++;
    // Skip `# Title` line
    if (i < lines.length && lines[i].startsWith('# ')) i++;
    // Skip consecutive blank lines, `- **Key**: Value` metadata lines, and
    // the indented bullets that follow a nested `Activity Session` block.
    while (i < lines.length) {
        const line = lines[i];
        if (line.trim() === '' || /^- \*\*(.+?)\*\*:\s*.*/.test(line) || /^\s+- /.test(line)) {
            i++;
        } else {
            break;
        }
    }
    return i;
}

/** Shared simple markdown renderer for memory and knowledge entry content */
export function EntryContent({
    content,
    highlightKeyword,
    skipHeader,
    onOpenActivitySession,
}: EntryContentProps) {
    const lines = content.split('\n');
    const startLine = skipHeader ? getHeaderLineCount(lines) : 0;
    const elements: React.ReactNode[] = [];

    for (let i = startLine; i < lines.length; i++) {
        const line = lines[i];

        if (line.startsWith('# ')) {
            elements.push(
                <h1 key={i} className="text-lg font-semibold text-ink mb-3">
                    <TextSpan text={line.slice(2)} keyword={highlightKeyword} />
                </h1>,
            );
        } else if (line.startsWith('## ')) {
            elements.push(
                <h2 key={i} className="text-sm font-semibold text-ink mt-4 mb-2">
                    <TextSpan text={line.slice(3)} keyword={highlightKeyword} />
                </h2>,
            );
        } else if (line.startsWith('- **')) {
            const match = line.match(/^- \*\*(.+?)\*\*:\s*(.+)/);
            if (match) {
                const isSessionLink =
                    match[1] === 'Activity Session' && onOpenActivitySession;
                elements.push(
                    <div key={i} className="flex items-baseline gap-2 text-xs py-0.5">
                        <span className="font-medium text-muted shrink-0">
                            {match[1] === 'Updated' ? 'Time' : match[1]}:
                        </span>
                        {isSessionLink ? (
                            <button
                                className="text-accent hover:underline cursor-pointer"
                                onClick={() => onOpenActivitySession(match[2].trim())}
                            >
                                {match[2]}
                            </button>
                        ) : (
                            <span className="text-ink">
                                <TextSpan
                                    text={match[2]}
                                    keyword={highlightKeyword}
                                />
                            </span>
                        )}
                    </div>,
                );
            } else {
                elements.push(
                    <div key={i} className="text-xs text-ink py-0.5 pl-3">
                        <TextSpan text={line.slice(2)} keyword={highlightKeyword} />
                    </div>,
                );
            }
        } else if (line.startsWith('- ')) {
            elements.push(
                <div key={i} className="flex gap-2 text-xs text-ink py-0.5 pl-1">
                    <span className="text-muted shrink-0">&bull;</span>
                    <span>
                        <TextSpan text={line.slice(2)} keyword={highlightKeyword} />
                    </span>
                </div>,
            );
        } else if (line.trim() === '') {
            elements.push(<div key={i} className="h-2" />);
        } else {
            elements.push(
                <p key={i} className="text-xs text-ink leading-relaxed">
                    <TextSpan text={line} keyword={highlightKeyword} />
                </p>,
            );
        }
    }

    return <div>{elements}</div>;
}

/**
 * Unified detail-pane renderer for a memory / knowledge entry: a `Metadata`
 * card on top (frontmatter as a labelled list, Activity Session rendered as
 * clickable session-jump rows that default to collapsed when
 * `collapsibleSessions` is set) and a body card below (free-form markdown
 * rendered through `MarkdownContent`). The two cards share the same padding
 * so memory and knowledge present the same shape.
 */
export function EntryDetail({
    content,
    onOpenActivitySession,
    cardPadding = 'px-6 py-5',
    compactSessions,
    collapsibleSessions,
}: {
    content: string;
    onOpenActivitySession?: (sessionId: string) => void;
    /** Override inner padding of each card. */
    cardPadding?: string;
    /** Hide per-session time column inside Activity Session rows. */
    compactSessions?: boolean;
    /** Default-collapse the Activity Session row (click to expand). */
    collapsibleSessions?: boolean;
}) {
    const { body } = splitHeaderAndBody(content);
    const hasBody = body.trim() !== '';
    const hasMetadata = useMemo(
        () => parseFrontmatter(content).length > 0,
        [content],
    );
    const cardClass = cn(
        'rounded-lg border border-border/50 bg-panel select-text',
        cardPadding,
    );
    return (
        <div className="space-y-3">
            {hasMetadata && (
                <div className={cardClass}>
                    <h2 className="m-0 mb-2.5 text-base font-semibold leading-snug text-ink">
                        Metadata
                    </h2>
                    <EntryMetadataList
                        content={content}
                        onOpenActivitySession={onOpenActivitySession}
                        compactSessions={compactSessions}
                        noDivider
                        collapsibleSessions={collapsibleSessions}
                    />
                </div>
            )}
            {hasBody && (
                <div className={cardClass}>
                    <MarkdownContent content={body} />
                </div>
            )}
        </div>
    );
}
