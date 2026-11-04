import { describe, expect, it } from 'vitest';
import type { StreamItem } from '@/types';
import { buildMainChatDays, dayName, answerChipIds, latestAnswerIds, localDayKey, mainChatStreamRows, routineUpdateCount, toMainChatOverview } from './main-chat-view';

const row = (id: string, extra: Partial<StreamItem> = {}): StreamItem => ({ id, kind: 'system', ...extra });
const status = (id: string, entries: number, extra: Partial<StreamItem> = {}): StreamItem => row(id, {
    toolName: 'main_chat_status',
    rowClass: 'routine',
    toolInput: {
        type: 'main_chat_status', version: 1, period: 'morning', periodStart: 0,
        entries: Array.from({ length: entries }, (_, index) => ({ id: `${id}-${index}`, kind: 'brief_missed', text: 'x', target: { kind: 'none' } })),
    },
    ...extra,
});

const card = (id: string, kind: string, extra: Partial<StreamItem> = {}, items?: unknown[]): StreamItem => row(id, {
    toolName: 'main_chat_proactive',
    rowClass: 'routine',
    toolInput: { momentId: id, kind, headline: 'Thread update unavailable.', body: 'This thread is no longer available.', actionIds: [], actionLabels: {}, target: { kind: 'none' }, state: 'pending', evidence: [], ...(items ? { items } : {}) },
    ...extra,
});

describe('main chat view helpers', () => {
    // Polish sweep 2026-10-27: four "Thread update unavailable." rows sat in
    // Bo's fold. A withdrawn thread update says nothing to act on; it is not
    // shown and does not count toward the fold.
    it('drops withdrawn thread updates from Chat and the Today panel', () => {
        const unavailable = { momentId: 'c', kind: 'thread_update_unavailable', headline: 'x', body: 'x', actionIds: [], actionLabels: {}, target: { kind: 'none' }, state: 'pending', evidence: [] };
        const days = buildMainChatDays([
            card('gone-1', 'thread_update_unavailable', { localDay: '2026-10-22' }),
            card('gone-2', 'thread_update_unavailable', { localDay: '2026-10-22' }),
            card('all-gone-digest', 'digest', { localDay: '2026-10-22' }, [unavailable]),
            status('t-status', 2, { localDay: '2026-10-22' }),
            card('mixed-digest', 'digest', { localDay: '2026-10-22', rowClass: 'waiting' }, [unavailable, { ...unavailable, momentId: 'd', kind: 'decision_due' }]),
        ], '2026-10-22');
        expect(days).toHaveLength(1);
        expect(days[0].routine.map((item) => item.id)).toEqual(['t-status']);
        expect(days[0].routineCount).toBe(2);
        expect(days[0].items.map((item) => item.id)).toEqual(['mixed-digest']);
    });

    it('folds each day’s routine rows after its divider and keeps every other row in order', () => {
        const items: StreamItem[] = [
            row('y-brief', { rowClass: 'routine', localDay: '2026-10-21' }),
            row('y-user', { kind: 'user', rowClass: 'conversation', localDay: '2026-10-21' }),
            status('t-status', 2, { localDay: '2026-10-22' }),
            row('t-card', { rowClass: 'waiting', localDay: '2026-10-22' }),
            row('t-report', { rowClass: 'finished', localDay: '2026-10-22' }),
        ];
        const days = buildMainChatDays(items, '2026-10-22');
        expect(days.map((day) => day.day)).toEqual(['2026-10-21', '2026-10-22']);
        expect(days[0].routine.map((item) => item.id)).toEqual(['y-brief']);
        expect(days[0].items.map((item) => item.id)).toEqual(['y-user']);
        expect(days[1].routine.map((item) => item.id)).toEqual(['t-status']);
        expect(days[1].routineCount).toBe(2);
        expect(days[1].items.map((item) => item.id)).toEqual(['t-card', 't-report']);
        // Nothing is dropped.
        expect(days.flatMap((day) => [...day.routine, ...day.items])).toHaveLength(items.length);
    });

    it('puts rows streamed since the read under today and an updated status row back in its own day', () => {
        const days = buildMainChatDays([
            row('y-user', { kind: 'user', localDay: '2026-10-21' }),
            row('t-user', { kind: 'user', localDay: '2026-10-22' }),
            // Updated yesterday's status row moved to the end of the chat.
            status('y-status', 3, { localDay: '2026-10-21' }),
            row('live-user', { kind: 'user' }),
            row('live-answer', { kind: 'assistant', content: 'Sure.' }),
        ], '2026-10-22');
        expect(days.map((day) => day.day)).toEqual(['2026-10-21', '2026-10-22']);
        expect(days[0].routine.map((item) => item.id)).toEqual(['y-status']);
        expect(days[1].items.map((item) => item.id)).toEqual(['t-user', 'live-user', 'live-answer']);
    });

    it('folds a routine row under its own day even when that day holds nothing else', () => {
        const days = buildMainChatDays([
            row('t-user', { kind: 'user', localDay: '2026-10-22' }),
            // Yesterday's status row, updated and moved to the end.
            status('y-status', 3, { localDay: '2026-10-21' }),
            row('t-answer', { kind: 'assistant', content: 'x', localDay: '2026-10-22' }),
        ], '2026-10-22');
        expect(days.map((day) => day.day)).toEqual(['2026-10-21', '2026-10-22']);
        expect(days[0]).toMatchObject({ routineCount: 3, items: [] });
        expect(days[0].routine.map((item) => item.id)).toEqual(['y-status']);
        expect(days[1]).toMatchObject({ routineCount: 0, routine: [] });
        expect(days[1].items.map((item) => item.id)).toEqual(['t-user', 't-answer']);
    });

    it('never opens an earlier day twice', () => {
        const days = buildMainChatDays([
            row('a', { kind: 'user', localDay: '2026-10-22' }),
            row('late', { kind: 'assistant', content: 'x', localDay: '2026-10-21' }),
        ], '2026-10-22');
        expect(days).toHaveLength(1);
        expect(days[0].items.map((item) => item.id)).toEqual(['a', 'late']);
    });

    it('keeps an updated proactive card under the day it happened', () => {
        const days = buildMainChatDays([
            row('today', { kind: 'user', localDay: '2026-10-22' }),
            row('pause', { kind: 'tool', toolName: 'main_chat_proactive', rowClass: 'waiting', localDay: '2026-10-21' }),
        ], '2026-10-22');
        expect(days[0].day).toBe('2026-10-21');
        expect(days[0].items.map((item) => item.id)).toEqual(['pause']);
    });

    it('counts each status entry once and any other routine row once', () => {
        expect(routineUpdateCount(status('s', 4))).toBe(4);
        expect(routineUpdateCount(row('card', { rowClass: 'routine' }))).toBe(1);
    });

    it('keeps one line for a pause represented by both status and card', () => {
        const line: StreamItem = { id: 'status', kind: 'system', toolName: 'main_chat_status', rowClass: 'routine', localDay: '2026-10-21', toolInput: { period: '2026-10-21', entries: [
            { id: 'pause-line', momentId: 'moment-1', kind: 'capture_paused', text: 'Pause', target: { kind: 'settings', id: 'activity' } },
            { id: 'other', kind: 'brief_missed', text: 'Brief', target: { kind: 'today' } },
        ] } };
        const card: StreamItem = { id: 'pause-card', kind: 'system', toolName: 'main_chat_proactive', rowClass: 'routine', localDay: '2026-10-21', toolInput: { momentId: 'moment-1', kind: 'capture_paused', headline: 'Pause', body: '' } };
        const result = mainChatStreamRows([line, card, { ...line, id: 'duplicate-status' }]);
        expect(result.map((item) => item.id)).toEqual(['status', 'duplicate-status']);
        expect(routineUpdateCount(result[0])).toBe(2);
        expect(routineUpdateCount(result[1])).toBe(1);
        expect(buildMainChatDays(result, '2026-10-22')[0]).toMatchObject({ day: '2026-10-21', routineCount: 3 });
    });

    it('keeps one pause line in its first place and gives it the end a later projection recorded', () => {
        const start = new Date(2026, 9, 29, 22, 5).getTime();
        const end = new Date(2026, 9, 29, 22, 40).getTime();
        const pause = { id: 'pause', momentId: 'moment-pause', kind: 'capture_paused', text: 'Paused', captureSource: 'screen', captureStartAt: start, target: { kind: 'settings' } };
        const first: StreamItem = { id: 'first', kind: 'system', toolName: 'main_chat_status', rowClass: 'routine', localDay: '2026-10-29', toolInput: { period: 'evening', entries: [pause, { id: 'other', kind: 'brief_missed', text: 'Brief', target: { kind: 'today' } }] } };
        const later: StreamItem = { id: 'later', kind: 'system', toolName: 'main_chat_status', rowClass: 'routine', localDay: '2026-10-29', toolInput: { period: 'night', entries: [{ ...pause, id: 'pause-ended', momentId: undefined, captureEndAt: end }] } };
        const card: StreamItem = { id: 'card', kind: 'system', toolName: 'main_chat_proactive', rowClass: 'routine', localDay: '2026-10-29', toolInput: { momentId: 'moment-pause', kind: 'capture_paused', headline: 'Pause', body: '' } };
        const result = mainChatStreamRows([first, card, later]);
        expect(result.map((item) => item.id)).toEqual(['first']);
        const entries = (result[0].toolInput as { entries: { id: string; captureEndAt?: number }[] }).entries;
        expect(entries.map((entry) => [entry.id, entry.captureEndAt])).toEqual([['pause', end], ['other', undefined]]);
    });

    it('keeps two different pauses apart, each with its own times', () => {
        const pause = (id: string, hour: number, end?: number): StreamItem => ({ id, kind: 'system', toolName: 'main_chat_status', rowClass: 'routine', localDay: '2026-10-29', toolInput: { period: id, entries: [
            { id, momentId: id, kind: 'capture_paused', text: 'Paused', captureSource: 'screen', captureStartAt: new Date(2026, 9, 29, hour).getTime(), captureEndAt: end, target: { kind: 'settings' } },
        ] } });
        const result = mainChatStreamRows([pause('early', 9), pause('late', 22, new Date(2026, 9, 29, 23).getTime())]);
        expect(result.map((item) => (item.toolInput as { entries: { captureEndAt?: number }[] }).entries[0].captureEndAt)).toEqual([undefined, new Date(2026, 9, 29, 23).getTime()]);
    });

    it('places capture entries under their recorded day even when a status row was moved today', () => {
        const start = new Date(2026, 10, 1, 22, 5).getTime();
        const status: StreamItem = { id: 'mixed', kind: 'system', toolName: 'main_chat_status', rowClass: 'routine', localDay: '2026-11-02', toolInput: { period: 'morning', entries: [
            { id: 'capture', momentId: 'pause', kind: 'capture_paused', text: 'Old', captureStartAt: start, target: { kind: 'settings' } },
            { id: 'brief', kind: 'brief_missed', text: 'Brief', target: { kind: 'today' } },
        ] } };
        const days = buildMainChatDays(mainChatStreamRows([status]), '2026-11-02');
        expect(days.map((day) => [day.day, day.routineCount])).toEqual([['2026-11-01', 1], ['2026-11-02', 1]]);
    });

    it('deduplicates notices with the same recorded capture source and start without merging different sources', () => {
        const card = (id: string, source: string): StreamItem => ({ id, kind: 'system', toolName: 'main_chat_proactive', rowClass: 'routine', localDay: '2026-11-01', toolInput: { momentId: id, kind: 'capture_paused', headline: 'Pause', body: '', captureSource: source, captureStartAt: 1793595900000 } });
        expect(mainChatStreamRows([card('one', 'screen'), card('retry', 'screen'), card('mic', 'microphone')]).map((item) => item.id)).toEqual(['one', 'mic']);
    });

    it('lists one Capture paused line per event when two capture sources paused together (today-review v1.1.19 R43)', () => {
        const start = new Date(2026, 10, 3, 13, 37).getTime();
        const status = (id: string, source: string, at: number): StreamItem => ({ id, kind: 'system', toolName: 'main_chat_status', rowClass: 'routine', localDay: '2026-11-03', toolInput: { period: id, entries: [
            { id, momentId: id, kind: 'capture_paused', text: 'Capture paused', captureSource: source, captureStartAt: at, target: { kind: 'settings' } },
        ] } });
        const rows = mainChatStreamRows([status('mic', 'microphone', start), status('audio', 'system_audio', start + 20_000), status('later', 'microphone', start + 3_600_000)]);
        expect(rows.map((item) => item.id)).toEqual(['mic', 'later']);
    });

    it('names today, yesterday and older days', () => {
        expect(dayName('2026-10-22', '2026-10-22', 'en')).toEqual({ kind: 'today' });
        expect(dayName('2026-10-21', '2026-10-22', 'en')).toEqual({ kind: 'yesterday' });
        expect(dayName('2026-10-01', '2026-11-01', 'en')).toEqual({ kind: 'date', text: expect.stringContaining('Oct') });
        expect(dayName('2025-12-31', '2026-01-01', 'en')).toEqual({ kind: 'yesterday' });
        expect(dayName('nonsense', '2026-01-01', 'en')).toEqual({ kind: 'date', text: 'nonsense' });
    });

    it('formats a local day like the computer does', () => {
        expect(localDayKey(new Date(2026, 0, 5, 23, 59))).toBe('2026-01-05');
    });

    it('finds the answers of the latest turn only', () => {
        expect(latestAnswerIds([
            row('u1', { kind: 'user' }), row('a1', { kind: 'assistant', content: 'old' }),
            row('u2', { kind: 'user' }), row('t', { kind: 'tool' }), row('a2', { kind: 'assistant', content: 'new' }),
            row('card', { toolName: 'main_chat_proactive' }), row('empty', { kind: 'assistant', content: ' ' }),
        ])).toEqual(['a2']);
        expect(latestAnswerIds([row('__welcome__', { kind: 'assistant', content: 'Hi' })])).toEqual([]);
    });

    it('retains older questions but not stale suggestions', () => {
        expect(answerChipIds([
            row('u1', { kind: 'user' }), row('q1', { kind: 'assistant', content: 'Keep it out?' }),
            row('a1', { kind: 'assistant', content: 'An old answer.' }), row('u2', { kind: 'user' }),
            row('a2', { kind: 'assistant', content: 'A new answer.' }),
            row('tool', { kind: 'assistant', toolName: 'tool', content: 'Tool question?' }),
        ])).toEqual(['q1', 'a2']);
    });

    it('reads the overview defensively', () => {
        expect(toMainChatOverview({})).toEqual({ sessionId: '', day: '', conversations: null, waiting: null, finished: null, routine: null, hideRoutine: false });
        expect(toMainChatOverview({ day: '2026-10-22', conversations: 1, waiting: 2, finished: 3, routine: -1, hideRoutine: true })).toMatchObject({ waiting: 2, routine: 0, hideRoutine: true });
    });
});
