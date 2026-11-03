import { measureToday } from "@/lib/today-telemetry";
import { memo, useEffect, useLayoutEffect, useRef, useState, type CSSProperties } from 'react';
import { useTranslation } from 'react-i18next';
import type { TodayDayShape } from '@/lib/wails-shim-app';
import { fitChipLabel } from '@/lib/day-strip-chip-label';
import { briefClockTime } from '@/lib/brief-time';
import { cn } from '@/lib/utils';
import { activeInterval } from '@/lib/app-activity';

export type HoldPreview = 'proposed' | 'held' | 'dismissed';
interface Span { start: number; end: number }

let chipMeasureContext: CanvasRenderingContext2D | null | undefined;
let chipMeasureFont = '';

function measureDayStripChip(text: string): number {
    if (typeof document === 'undefined') return text.length * 6;
    if (chipMeasureContext === undefined) chipMeasureContext = document.createElement('canvas').getContext('2d');
    if (!chipMeasureContext) return text.length * 6;
    if (!chipMeasureFont) chipMeasureFont = `500 11.5px ${window.getComputedStyle(document.body).fontFamily || 'sans-serif'}`;
    chipMeasureContext.font = chipMeasureFont;
    return chipMeasureContext.measureText(text).width;
}

/** Older day shapes can carry the recurrence word as their short name. */
function meetingLabel(shortName: string, title: string): string {
    const source = /^weekly$/i.test(shortName.trim()) ? title : shortName;
    return source.replace(/\bweekly\b[\s,:-]*/gi, '').trim();
}

/** Union calendar intervals before finding free time; overlapping meetings
 * never create a false free stretch. A preview hold occupies drawing space only. */
export function freeDaySpans(start: number, end: number, occupied: Span[]): Span[] {
    let cursor = start;
    const free: Span[] = [];
    for (const span of [...occupied].sort((a, b) => a.start - b.start)) {
        if (span.end <= cursor || span.start >= end) continue;
        if (span.start > cursor) free.push({ start: cursor, end: Math.min(span.start, end) });
        cursor = Math.max(cursor, Math.min(span.end, end));
    }
    if (cursor < end) free.push({ start: cursor, end });
    return free;
}

interface DayBlock extends Span {
    event: Omit<TodayDayShape['events'][number], 'start' | 'end'> | null;
    originalStart: number;
    originalEnd: number;
}
interface DrawnBlock extends DayBlock {
    layer: number;
    inset: boolean;
    hidden: DayBlock[];
}

/** Sweep simultaneous intervals, retaining the two shortest at every instant.
 * Merge contiguous pieces with the same geometry. Labels are drawn once per
 * event, independently of inset changes. Hidden events retain tooltip evidence. */
function overlapBlocks(blocks: DayBlock[]): DrawnBlock[] {
    const ordered = [...blocks].sort((a, b) =>
        (b.originalEnd - b.originalStart) - (a.originalEnd - a.originalStart));
    const boundaries = [...new Set(blocks.flatMap((block) => [block.start, block.end]))].sort((a, b) => a - b);
    const pieces = new Map<DayBlock, DrawnBlock[]>();
    for (let i = 0; i < boundaries.length - 1; i++) {
        const start = boundaries[i], end = boundaries[i + 1];
        const active = ordered.filter((block) => block.start < end && block.end > start);
        const visible = active.slice(-2), hidden = active.slice(0, -2);
        visible.forEach((block, index) => {
            const spans = pieces.get(block) ?? [];
            const prior = spans.at(-1);
            const folded = index === 0 ? hidden : [];
            const inset = index === 1;
            if (prior?.end === start && prior.inset === inset) {
                prior.end = end;
                prior.hidden = [...new Set([...prior.hidden, ...folded])];
            } else spans.push({ ...block, start, end, layer: ordered.indexOf(block), inset, hidden: folded });
            pieces.set(block, spans);
        });
    }
    return ordered.flatMap((block) => pieces.get(block) ?? []).sort((a, b) => a.start - b.start || a.layer - b.layer);
}

function BlockLabel({ label, style, className, eventId }: {
    label: string; style: CSSProperties; className: string; eventId?: string;
}) {
    return <span data-testid={eventId !== undefined ? 'today-day-event-label' : 'today-day-hold-label'} data-event-id={eventId}
        className={cn('today-day-block-label pointer-events-none absolute flex items-center justify-start overflow-hidden whitespace-nowrap px-1.5 text-left text-[11.5px] font-medium', className)}
        style={style}>{label}</span>;
}

function NowLine({ initialNow, start, end, layer, clock, label }: {
    initialNow: number; start: number; end: number; layer: number;
    clock: (at: number) => string; label: (time: string) => string;
}) {
    const [now, setNow] = useState(initialNow);
    useEffect(() => {
        // The local one-minute clock: no data read, stopped while hidden.
        setNow(initialNow);
        return activeInterval(() => setNow(Date.now()), 60_000);
    }, [initialNow]);
    const percent = (now - start) / (end - start) * 100;
    return <span data-testid="today-day-now" className="absolute -bottom-1 -top-1 z-10 w-0.5 rounded-sm bg-accent" style={{ zIndex: layer, left: now <= start ? '0px' : now >= end ? 'calc(100% - 2px)' : `clamp(0px, ${percent}%, calc(100% - 2px))` }} title={label(clock(now))}><span aria-hidden className="absolute -left-[3px] -top-[3px] size-2 rounded-full bg-accent" /></span>;
}

function FreeLabels({ spans, start, end, initialNow, isToday, width, language, tr }: {
    spans: Span[]; start: number; end: number; initialNow: number; isToday: boolean; width: number; language: string;
    tr: (key: string, options?: Record<string, unknown>) => string;
}) {
    const [now, setNow] = useState(initialNow);
    useEffect(() => {
        const received = Date.now();
        setNow(initialNow);
        if (!isToday || !Number.isFinite(initialNow)) return;
        return activeInterval(() => setNow(initialNow + Math.max(0, Date.now() - received)), 60_000);
    }, [initialNow, isToday]);
    return <>{spans.map((span) => {
        const ahead = { start: isToday && Number.isFinite(now) ? Math.max(span.start, now) : span.start, end: span.end };
        if (ahead.end - ahead.start < 45 * 60_000) return null;
        const minutes = Math.round((ahead.end - ahead.start) / 60_000);
        const hours = Math.floor(minutes / 60), remainder = minutes % 60;
        const duration = tr(hours && remainder ? 'dayStrip.hoursMinutes' : hours ? 'dayStrip.hours' : 'dayStrip.minutes', { hours, minutes: remainder });
        const full = tr('dayStrip.freeDuration', { duration });
        const short = language.startsWith('zh') ? duration.replace('分钟', '分') : duration;
        const available = width * (ahead.end - ahead.start) / (end - start) - 8;
        const label = measureDayStripChip(full) <= available ? full : measureDayStripChip(short) <= available ? short : '';
        return <span key={span.start} data-testid="today-day-free" className="pointer-events-none absolute top-[6px] z-[2] flex h-7 items-center justify-center whitespace-nowrap text-[11px] text-muted" style={{ left: `${(ahead.start - start) / (end - start) * 100}%`, width: `calc(${(ahead.end - ahead.start) / (end - start) * 100}% - 2px)` }}>{label && <span className="bg-background">{label}</span>}</span>;
    })}</>;
}

interface TodayDayStripProps {
    shape: TodayDayShape | null;
    holdPreview?: HoldPreview;
    timeZone?: string;
    requestedDate: string;
    /** The first days: "Your afternoon" with "From your calendar" above the strip. */
    heading?: { title: string; source: string };
    onOpenMeeting?: (id: string) => void;
    holdTitle?: string;
    onRemoveHold?: (id?: string) => void;
    onOpenHold?: () => void;
}

export const TodayDayStrip = memo(function TodayDayStrip({ shape, holdPreview = 'proposed', timeZone, requestedDate, heading, onOpenMeeting, holdTitle, onRemoveHold, onOpenHold }: TodayDayStripProps) {
    const { t, i18n } = useTranslation('today');
    const tr = (key: string, options?: Record<string, unknown>) => String(t(key as never, options as never));
    const track = useRef<HTMLDivElement>(null);
    const [width, setWidth] = useState(0);
    const [holdMenu, setHoldMenu] = useState<{ id?: string; title: string } | null>(null);
    const [holdNow, setHoldNow] = useState(() => Date.parse(shape?.now ?? ''));
    const shapeClock = useRef({ value: shape?.now, received: Date.now() });
    if (shapeClock.current.value !== shape?.now) shapeClock.current = { value: shape?.now, received: Date.now() };
    useEffect(() => {
        const now = Date.parse(shape?.now ?? '') + Math.max(0, Date.now() - shapeClock.current.received);
        setHoldNow(now);
        if (!shape || !Number.isFinite(now)) return;
        const timers = shape.events.filter((event) => event.holdId).map((event) => Date.parse(event.start))
            .filter((start) => Number.isFinite(start) && start > now)
            .map((start) => window.setTimeout(() => setHoldNow((current) => Math.max(current, start)), start - now));
        return () => timers.forEach((timer) => window.clearTimeout(timer));
    }, [shape?.now, shape?.events]);
    useLayoutEffect(() => {
        const element = track.current;
        if (!element) return;
        const measure = () => setWidth(element.getBoundingClientRect().width);
        measure();
        if (typeof ResizeObserver === 'undefined') return;
        const observer = new ResizeObserver(measure);
        observer.observe(element);
        return () => observer.disconnect();
    }, [shape?.calendarConnected, shape?.rangeStart, shape?.rangeEnd, shape?.events, shape?.hold, holdPreview]);
    if (!shape) return null;
    if (!shape.calendarConnected) return null;
    const start = Date.parse(shape.rangeStart), end = Date.parse(shape.rangeEnd);
    if (!Number.isFinite(start) || !Number.isFinite(end) || end <= start) return null;
    const percent = (at: number) => (at - start) / (end - start) * 100;
    const placement = (span: Span, gap = 2) => ({ left: `${percent(span.start)}%`, width: `calc(${percent(span.end) - percent(span.start)}% - ${gap}px)` });
    const clock = (at: number) => briefClockTime(new Date(at).toISOString(), i18n.language, tr, timeZone) ?? '';
    const events = (shape.events ?? []).map((event) => ({ ...event, holdId: event.holdId && Date.parse(event.start) > holdNow ? event.holdId : undefined, start: Math.max(start, Date.parse(event.start)), end: Math.min(end, Date.parse(event.end)), originalStart: Date.parse(event.start), originalEnd: Date.parse(event.end) }))
        .filter((event) => Number.isFinite(event.start) && Number.isFinite(event.end) && event.end > event.start)
        .sort((a, b) => a.start - b.start);
    const holdStart = Date.parse(shape.hold?.start ?? ''), holdEnd = Date.parse(shape.hold?.end ?? '');
    const hold = shape.hold && holdPreview !== 'dismissed' && holdEnd > start && holdStart < end && holdEnd > holdStart
        ? { start: Math.max(start, holdStart), end: Math.min(end, holdEnd) } : null;
    if (events.length === 0 && !hold) return null;
    const currentMenu = holdMenu && (holdMenu.id ? events.some((event) => event.holdId === holdMenu.id && event.title === holdMenu.title) : hold && holdPreview === 'held' && holdMenu.title === (holdTitle ?? shape.hold?.label ?? '')) ? holdMenu : null;
    const entries: DayBlock[] = [
        ...events.map((event) => ({ ...event, event })),
        ...(hold ? [{ ...hold, event: null, originalStart: holdStart, originalEnd: holdEnd }] : []),
    ];
    const blocks = overlapBlocks(entries);
    const markerLayer = 0;
    const free = freeDaySpans(start, end, [...events, ...(hold ? [hold] : [])]).filter((span) => span.end - span.start >= 45 * 60_000);
    const hours: number[] = [];
    const firstHour = new Date(start);
    firstHour.setMinutes(0, 0, 0);
    for (let at = firstHour.getTime(); at <= end; at += 3_600_000) if (at >= start) hours.push(at);
    const now = Date.parse(shape.now);
    const civilDay = (at: number) => {
        let parts: Intl.DateTimeFormatPart[];
        try { parts = new Intl.DateTimeFormat('en-CA', { year: 'numeric', month: '2-digit', day: '2-digit', timeZone }).formatToParts(at); }
        catch { parts = new Intl.DateTimeFormat('en-CA', { year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(at); }
        const value = (type: string) => Number(parts.find((part) => part.type === type)?.value);
        return Date.UTC(value('year'), value('month') - 1, value('day'));
    };
    const day = /^\d{4}-\d{2}-\d{2}$/.test(requestedDate) ? Date.parse(`${requestedDate}T00:00:00Z`) : NaN;
    const dayDifference = Number.isFinite(now) && Number.isFinite(day) ? Math.round((day - civilDay(now)) / 86_400_000) : null;
    const dayLabel = dayDifference === null || dayDifference === 0 ? '' : dayDifference === 1 ? t('dayStrip.tomorrow')
        : new Date(day).toLocaleDateString(i18n.language, { weekday: 'long', timeZone: 'UTC' });
    return (
        <div className="today-day-strip flex flex-col gap-1.5" data-testid="today-day-strip" role="group" aria-label={heading?.title ?? t('dayStrip.label')}>
            {heading && (
                <div className="flex items-baseline justify-between gap-3" data-testid="today-day-strip-heading">
                    <span className="text-[13px] font-semibold text-ink">{heading.title}</span>
                    <span className="text-[12px] text-muted">{heading.source}</span>
                </div>
            )}
            <div className="relative h-3.5 text-[11px] leading-[14px] text-muted" data-testid="today-day-hours">
                {hours.filter((_, i) => i % 2 === 0).map((at, i) => <span key={at} className={cn('today-day-hour absolute whitespace-nowrap', i % 2 === 1 && 'today-day-hour-secondary')} style={{ left: `${percent(at)}%`, transform: percent(at) > 90 ? 'translateX(-100%)' : percent(at) === 0 ? undefined : 'translateX(-50%)' }}>{clock(at)}</span>)}
            </div>
            <div ref={track} data-testid="today-day-track" data-rows="1" className="patina-ground relative isolate rounded-lg border border-border bg-background" style={{ height: 40 }}>
                {hours.map((at) => <span key={at} aria-hidden className="absolute inset-y-0 w-px bg-border" style={{ left: `${percent(at)}%` }} />)}
                {events.length === 0 && !hold ? <span className="absolute inset-0 flex items-center justify-center text-[12px] text-muted">{dayLabel ? t('dayStrip.emptyOnDay', { day: dayLabel }) : t('dayStrip.empty')}</span> : <FreeLabels spans={dayDifference !== null && dayDifference < 0 ? [] : free} start={start} end={end} initialNow={now} isToday={dayDifference === 0} width={width} language={i18n.language} tr={tr} />}
                {entries.map((entry) => {
                    const pieces = blocks.filter((block) => block.event === entry.event);
                    if (!pieces.length) return null;
                    const covers = blocks.filter((other) => other.layer > pieces[0].layer && other.start < entry.end && other.end > entry.start);
                    const labelSpan = freeDaySpans(entry.start, entry.end, covers).sort((a, b) => (b.end - b.start) - (a.end - a.start))[0];
                    const { event } = entry;
                    const labelWidth = labelSpan ? width * (labelSpan.end - labelSpan.start) / (end - start) - 2 : 0;
                    const rawLabel = event ? event.holdId ? t('dayStrip.held') : event.kind === 'meeting' ? meetingLabel(event.shortName, event.title) : event.shortName
                        : holdPreview === 'held' ? t('dayStrip.held') : t('dayStrip.proposed');
                    const label = fitChipLabel(rawLabel, labelWidth - 12, measureDayStripChip, event?.initial);
                    return <span key={event?.id ?? 'hold'} data-testid={event ? 'today-day-event-group' : 'today-day-hold-group'}
                        className="pointer-events-none absolute inset-0" style={{ zIndex: pieces[0].layer + 1, opacity: entry.originalEnd <= now ? 0.45 : 1 }}>
                        {pieces.map((block, pieceIndex) => {
                            const eventTitle = (item: DayBlock) => t('dayStrip.eventTime', {
                                title: item.event?.title ?? shape.hold!.label, start: clock(item.originalStart), end: clock(item.originalEnd),
                            });
                            const title = [eventTitle(block), ...(block.hidden.length ? [t('dayStrip.more', { count: block.hidden.length }), ...block.hidden.map(eventTitle)] : [])].join('\n');
                            const className = cn('pointer-events-auto absolute flex items-center justify-center overflow-hidden whitespace-nowrap rounded-md text-[11.5px] font-medium', event ? event.holdId ? 'text-[10px] bg-solid-btn-bg text-solid-btn-fg' : event.kind === 'personal' ? 'border-[1.5px] border-ink bg-background text-ink' : 'bg-ink text-background' : holdPreview === 'held' ? 'text-[10px] bg-solid-btn-bg text-solid-btn-fg' : 'border-[1.5px] border-dashed border-accent bg-accent-light text-accent');
                            const style = { ...placement(block, block.end === Math.min(end, block.originalEnd) ? 2 : 0), zIndex: 1, top: block.inset ? 10 : 6, height: block.inset ? 20 : 28 };
                            return event?.kind === 'meeting' && !event.holdId && onOpenMeeting
                                ? <button key={block.start} type="button" data-testid="today-day-event" data-inset={block.inset} data-kind={event.kind} title={title} aria-label={t('dayStrip.openPrep', { event: eventTitle(block) })} tabIndex={pieceIndex === 0 ? 0 : -1} className={cn(className, 'cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent')} style={style} onClick={() => onOpenMeeting(event.id)} />
                                : <span key={block.start} data-testid={event ? 'today-day-event' : 'today-day-hold'} data-inset={block.inset} data-kind={event?.kind} data-state={event ? undefined : holdPreview} title={title} aria-label={title} className={className} style={style} />;
                        })}
                        {labelSpan && <BlockLabel eventId={event?.id} label={label}
                            className={event ? event.holdId ? 'text-solid-btn-fg' : event.kind === 'personal' ? 'text-ink' : 'text-background' : holdPreview === 'held' ? 'text-solid-btn-fg' : 'text-accent'}
                            style={{ ...placement(labelSpan), top: 6, height: 28, zIndex: 2 }} />}
                        {!event && labelSpan && holdPreview === 'held' && onRemoveHold && <button type="button"
                            aria-label={t('holds.heldFor', { title: holdTitle ?? shape.hold?.label })}
                            aria-expanded={!!holdMenu && !holdMenu.id} onClick={() => measureToday("T31", "open", "opened", () => setHoldMenu((open) => open && !open.id ? null : { title: holdTitle ?? shape.hold?.label ?? '' }))}
                            className="pointer-events-auto absolute rounded-md focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
                            style={{ ...placement(labelSpan), top: 6, height: 28, zIndex: 3 }} />}
                        {event?.holdId && labelSpan && onRemoveHold && <button type="button"
                            aria-label={t('holds.heldFor', { title: event.title })} aria-expanded={holdMenu?.id === event.holdId}
                            onClick={() => measureToday("T31", "open", "opened", () => setHoldMenu((open) => open?.id === event.holdId ? null : { id: event.holdId, title: event.title }))}
                            className="pointer-events-auto absolute rounded-md focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
                            style={{ ...placement(labelSpan), top: 6, height: 28, zIndex: 3 }} />}
                        {!event && labelSpan && holdPreview === 'proposed' && onOpenHold && <button type="button"
                            aria-label={t('holds.openOffer', { title: holdTitle ?? shape.hold?.label })} onClick={onOpenHold}
                            className="pointer-events-auto absolute rounded-md focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent"
                            style={{ ...placement(labelSpan), top: 6, height: 28, zIndex: 3 }} />}
                    </span>;
                })}
                {Number.isFinite(now) && dayDifference === 0 && <NowLine initialNow={now} start={start} end={end} layer={markerLayer} clock={clock} label={(time) => String(t('dayStrip.now', { time }))} />}
            </div>
            {currentMenu && onRemoveHold && <div className="self-start rounded-lg border border-border bg-background p-2 text-[13px] shadow-sm">
                <p className="text-muted">{t('holds.heldFor', { title: currentMenu.title })}</p>
                <button type="button" className="mt-1 text-accent" onClick={() => { setHoldMenu(null); onRemoveHold(currentMenu.id); }}>{t('holds.remove')}</button>
            </div>}

        </div>
    );
}, (prior, next) => prior.holdPreview === next.holdPreview && prior.timeZone === next.timeZone
    && prior.requestedDate === next.requestedDate && prior.onOpenMeeting === next.onOpenMeeting && prior.holdTitle === next.holdTitle && prior.onRemoveHold === next.onRemoveHold && prior.onOpenHold === next.onOpenHold && JSON.stringify(prior.shape) === JSON.stringify(next.shape));
