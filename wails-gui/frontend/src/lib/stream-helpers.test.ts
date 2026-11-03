import { describe, expect, it } from 'vitest';
import {
    groupStreamItems,
    fromStreamItemData,
    toStreamItemData,
    splitIntoTurns,
    getMainChatReport,
    getMainChatProactive,
    getMainChatStatus,
    getSideChatOrigin,
    mergeAppendedItems,
    selectHubReports,
    selectHubSideChats,
    createSession,
    MAIN_CHAT_REPORT_TOOL,
    MAIN_CHAT_PROACTIVE_TOOL,
    MAIN_CHAT_STATUS_TOOL,
    MAIN_CHAT_OBSERVATION_PAUSE_TOOL,
    SIDE_CHAT_ORIGIN_TOOL,
    type GroupedStreamItem,
} from './stream-helpers';
import type { StreamItem } from '@/types';
import type { OpenAnswer } from './open-answers';

function item(
    id: string,
    kind: StreamItem['kind'],
    extra: Partial<StreamItem> = {}
): StreamItem {
    return { id, kind, ...extra };
}

function groupedId(grouped: GroupedStreamItem): string {
    switch (grouped.type) {
        case 'single':
            return grouped.item.id;
        case 'tool-group':
            return grouped.items[0].id;
        case 'thinking-group':
            return grouped.thinkingItem.id;
    }
}

describe('stream grouping', () => {
    it('keeps tool calls in strict stream order instead of grouping by tool name', () => {
        const grouped = groupStreamItems([
            item('think-1', 'thinking', { content: 'first thought' }),
            item('read-1', 'tool', { toolName: 'Read' }),
            item('bash-1', 'tool', { toolName: 'Bash' }),
            item('think-2', 'thinking', { content: 'second thought' }),
            item('read-2', 'tool', { toolName: 'Read' }),
        ]);

        expect(grouped.map((g) => g.type)).toEqual([
            'single',
            'tool-group',
            'tool-group',
            'single',
            'tool-group',
        ]);
        expect(grouped.map(groupedId)).toEqual([
            'think-1',
            'read-1',
            'bash-1',
            'think-2',
            'read-2',
        ]);
        const toolGroups = grouped.filter((g) => g.type === 'tool-group');
        expect(toolGroups.every((g) => g.items.length === 1)).toBe(true);
    });

    it('collapses mid-turn assistant text into one chronological processing block', () => {
        const grouped = groupStreamItems([
            item('user-1', 'user', { content: 'check this' }),
            item('assistant-1', 'assistant', { content: 'I will inspect it.' }),
            item('read-1', 'tool', { toolName: 'Read' }),
            item('assistant-2', 'assistant', { content: 'Done.' }),
        ]);

        const turns = splitIntoTurns(grouped, false);

        expect(turns).toHaveLength(1);
        expect(turns[0].segments).toHaveLength(1);
        expect(
            turns[0].segments[0].processing.map(groupedId)
        ).toEqual(['assistant-1', 'read-1']);
        expect(
            turns[0].segments[0].finals.map((g) =>
                g.type === 'single' ? g.item.id : ''
            )
        ).toEqual(['assistant-2']);
    });

    it('does not promote active streaming assistant text to a visible final summary', () => {
        const grouped = groupStreamItems([
            item('user-1', 'user', { content: 'check this' }),
            item('read-1', 'tool', { toolName: 'Read' }),
            item('assistant-1', 'assistant', { content: 'Here is what I found so far.' }),
        ]);

        const streamingTurns = splitIntoTurns(grouped, true);
        expect(streamingTurns).toHaveLength(1);
        expect(streamingTurns[0].segments).toHaveLength(1);
        expect(
            streamingTurns[0].segments[0].processing.map(groupedId)
        ).toEqual(['read-1', 'assistant-1']);
        expect(streamingTurns[0].segments[0].finals).toEqual([]);

        const completedTurns = splitIntoTurns(grouped, false);
        expect(
            completedTurns[0].segments[0].processing.map(groupedId)
        ).toEqual(['read-1']);
        expect(
            completedTurns[0].segments[0].finals.map(groupedId)
        ).toEqual(['assistant-1']);
    });
});

describe('main chat report items', () => {
    it('keeps a report line after a reply as its own final card', () => {
        const turns = splitIntoTurns(
            groupStreamItems([
                item('u1', 'user', { content: 'hi' }),
                item('a1', 'assistant', { content: 'done' }),
                item('r1', 'system', {
                    content: 'Finished "x".',
                    toolName: MAIN_CHAT_REPORT_TOOL,
                    toolInput: { source: 'handoff', action: 'review', target: { kind: 'session', id: 's' } },
                }),
            ]),
            false,
        );
        expect(turns).toHaveLength(1);
        const [segment] = turns[0].segments;
        expect(segment.processing).toHaveLength(0);
        expect(segment.finals.map(groupedId)).toEqual(['a1', 'r1']);
    });

    it('keeps a plain status row inside processing but a side-chat origin outside it', () => {
        const turns = splitIntoTurns(
            groupStreamItems([
                item('o1', 'system', { content: 'quoted', toolName: SIDE_CHAT_ORIGIN_TOOL, toolInput: { parentId: 'm' } }),
                item('u1', 'user', { content: 'hi' }),
                item('s1', 'system', { content: 'thinking' }),
                item('a1', 'assistant', { content: 'done' }),
            ]),
            false,
        );
        expect(turns).toHaveLength(2);
        expect(turns[0].user).toBeNull();
        expect(turns[0].segments[0].finals.map(groupedId)).toEqual(['o1']);
        expect(turns[1].segments[0].processing.map(groupedId)).toEqual(['s1']);
        expect(turns[1].segments[0].finals.map(groupedId)).toEqual(['a1']);
    });

    it('parses report and origin payloads and rejects other system rows', () => {
        const report = getMainChatReport(
            item('r1', 'system', {
                toolName: MAIN_CHAT_REPORT_TOOL,
                toolInput: { source: 'schedule', action: 'open', target: { kind: 'automation', id: 'job', runId: 'run' }, title: 'Digest', status: 'ok' },
            }),
        );
        expect(report).toEqual({
            source: 'schedule',
            key: undefined,
            action: 'open',
            target: { kind: 'automation', id: 'job', runId: 'run' },
            title: 'Digest',
            status: 'ok',
            summary: undefined,
        });
        expect(getMainChatReport(item('s1', 'system', { content: 'Plan rejected.' }))).toBeNull();
        expect(getMainChatReport(item('r2', 'system', { toolName: MAIN_CHAT_REPORT_TOOL, toolInput: { source: 'handoff' } }))).toBeNull();
        expect(getSideChatOrigin(item('o1', 'system', { content: 'q', toolName: SIDE_CHAT_ORIGIN_TOOL, toolInput: { parentId: 'm', itemId: 'i' } }))).toEqual({ parentId: 'm', itemId: 'i', excerpt: 'q' });
        expect(getSideChatOrigin(item('a1', 'assistant', { content: 'q' }))).toBeNull();
    });
});

describe('mergeAppendedItems', () => {
    it('replaces a paused observing card with its completed row by id', () => {
        const paused = item('pause', 'system', { toolName: MAIN_CHAT_OBSERVATION_PAUSE_TOOL, sortOrder: 1, rowClass: 'other', toolInput: { startAt: 100 } });
        const resumed = { ...paused, rowClass: 'routine' as const, toolInput: { startAt: 100, endAt: 200 } };
        expect(mergeAppendedItems([paused], [resumed])).toEqual([resumed]);
    });
    it('moves the same status row to its new chronological position even when its copy is unchanged', () => {
        const status = item('s1', 'system', { toolName: MAIN_CHAT_STATUS_TOOL, sortOrder: 0, content: 'Done.' });
        const existing = [status, item('u1', 'user', { sortOrder: 1 }), item('a1', 'assistant', { sortOrder: 2 })];
        const fresh = [{ ...status, sortOrder: 3 }];
        expect(mergeAppendedItems(existing, fresh).map((entry) => entry.id)).toEqual(['u1', 'a1', 's1']);
        expect(mergeAppendedItems(existing, fresh)[2].sortOrder).toBe(3);
    });

    it('ignores an older refresh of a status row that already advanced', () => {
        const newer = item('s1', 'system', { toolName: MAIN_CHAT_STATUS_TOOL, sortOrder: 251, content: 'Three entries' });
        const existing = [newer, item('u1', 'user', { sortOrder: 300 })];
        const stale = { ...newer, sortOrder: 250, content: 'Two entries' };
        const merged = mergeAppendedItems(existing, [stale]);
        expect(merged).toBe(existing);
        expect(merged[0].content).toBe('Three entries');
        expect(merged[0].sortOrder).toBe(251);
    });

    it('keeps a pending streamed reply in place while sorting persisted updates', () => {
        const reply = item('live', 'assistant', { content: 'Still writing' });
        const status = item('s1', 'system', { toolName: MAIN_CHAT_STATUS_TOOL, sortOrder: 0 });
        const existing = [status, item('u1', 'user', { sortOrder: 1 }), reply];
        const merged = mergeAppendedItems(existing, [{ ...status, sortOrder: 2 }]);
        expect(merged.map((entry) => entry.id)).toEqual(['u1', 's1', 'live']);
        expect(merged[2]).toBe(reply);
    });
    it('refreshes folded status and proactive state in place without replacing streamed replies', () => {
        const reply = item('a1', 'assistant', { content: 'streaming' });
        const oldStatus = item('s1', 'system', { toolName: MAIN_CHAT_STATUS_TOOL, toolInput: { period: 'morning', entries: [] } });
        const oldCard = item('p1', 'system', { toolName: MAIN_CHAT_PROACTIVE_TOOL, toolInput: { state: 'pending' } });
        const updatedStatus = { ...oldStatus, toolInput: { period: 'morning', entries: [{ id: 'one', text: 'Done.' }] } };
        const updatedCard = { ...oldCard, toolInput: { state: 'dismissed' } };
        const fresh = [{ ...reply, content: 'older copy' }, updatedStatus, updatedCard];
        const merged = mergeAppendedItems([reply, oldStatus, oldCard], fresh);
        expect(merged).toEqual([reply, updatedStatus, updatedCard]);
        expect(merged[0]).toBe(reply);
        expect(mergeAppendedItems(merged, fresh)).toBe(merged);
    });
    it('appends only rows the window does not hold yet, in sort order', () => {
        const streamedReply = item('a1', 'assistant', { content: 'streamed, not yet sorted' });
        const existing = [item('u1', 'user', { sortOrder: 0 }), streamedReply];
        const fresh = [
            item('u1', 'user', { sortOrder: 0 }),
            item('a1', 'assistant', { sortOrder: 1, content: 'streamed, not yet sorted' }),
            item('r2', 'system', { sortOrder: 3, toolName: MAIN_CHAT_REPORT_TOOL }),
            item('r1', 'system', { sortOrder: 2, toolName: MAIN_CHAT_REPORT_TOOL }),
        ];
        const merged = mergeAppendedItems(existing, fresh);
        expect(merged.map((i) => i.id)).toEqual(['u1', 'a1', 'r1', 'r2']);
        expect(merged[1]).toBe(streamedReply);
        expect(mergeAppendedItems(merged, fresh)).toBe(merged);
    });
});

describe('proactive stream payloads', () => {
    it('keeps a legacy week association display-only while retaining explicit origin metadata', () => {
        const legacy = fromStreamItemData({ id: 'old', kind: 'user', content: 'release isoform 1.0', weekAnswerBriefId: '2026-11-02-morning' });
        expect(legacy.weekAnswerBriefId).toBe('2026-11-02-morning');
        expect(toStreamItemData(legacy).automationContext).toBeUndefined();
        expect(toStreamItemData(legacy).weekAnswerBriefId).toBeUndefined();
        const origin = '{"weekAnswerBriefId":"2026-11-02-morning"}';
        const explicit = fromStreamItemData({ id: 'new', kind: 'user', content: 'Launch', automationContext: origin });
        expect(explicit.weekAnswerBriefId).toBe('2026-11-02-morning');
        expect(toStreamItemData(explicit).automationContext).toBe(origin);
    });
    it('maps validated open-answer metadata from stream data', () => {
        const openAnswer: OpenAnswer = {
            id: 'answer-1', sessionId: 'session-1', messageId: 'message-1', question: 'Will it rain?',
            kind: 'weather', settles: 'result', delivery: 'around', windowStart: '2026-10-27T10:00:00-07:00',
            expiresAt: '2026-11-01T00:00:00Z', posts: 0, state: 'open', createdAt: '2026-10-26T09:00:00-07:00',
        };
        const rendered = fromStreamItemData({
            id: 'message-1', kind: 'assistant', openAnswer, cardType: 'answer_ready', createdAt: 1_793_017_200_000,
        });
        expect(rendered.openAnswer).toEqual(openAnswer);
        expect(rendered.cardType).toBe('answer_ready');
        expect(rendered.createdAt).toBe(1_793_017_200_000);
        expect(toStreamItemData(rendered)).toMatchObject({
            openAnswer, cardType: 'answer_ready', createdAt: 1_793_017_200_000,
        });
        expect(fromStreamItemData({ id: 'bad', kind: 'assistant', openAnswer: { ...openAnswer, state: 'unknown' } }).openAnswer).toBeUndefined();
    });

    it('preserves draft, work and permission details on separate digest children', () => {
        const draftContent = { to: 'dana@example.com', subject: 'Re: Planning', text: 'Thank you for waiting.' };
        const work = { handoffId: 'work-1', status: 'completed', summary: 'Ready.', result: 'Full result.', deliverables: [] };
        const permission = { requestId: 'request-1', sessionId: 'session-1', handoffId: 'work-2', toolName: 'Bash', details: 'Run tests.' };
        const child = { headline: 'An update.', body: 'Review when ready.' };
        const row = item('digest', 'system', { toolName: MAIN_CHAT_PROACTIVE_TOOL, toolInput: {
            ...child, momentId: 'digest', kind: 'digest', items: [
                { ...child, momentId: 'mail-1', kind: 'waiting_on_reply', draft: true, draftContent },
                { ...child, momentId: 'work-1', kind: 'work_finished', work },
                { ...child, momentId: 'request-1', kind: 'decision_due', permission },
            ],
        } });
        expect(getMainChatProactive(row)?.items).toMatchObject([
            { momentId: 'mail-1', draftContent, work: undefined, permission: undefined },
            { momentId: 'work-1', draftContent: undefined, work, permission: undefined },
            { momentId: 'request-1', draftContent: undefined, work: undefined, permission },
        ]);
    });

    it('preserves only a well-formed commitment hold source', () => {
        const holdSource = { thing: { title: 'Q4 plan', sourceKind: 'promise', sourceId: 'commitment-1' }, due: '2026-11-02T17:00:00Z' };
        const row = item('p1', 'system', { toolName: MAIN_CHAT_PROACTIVE_TOOL, toolInput: { momentId: 'm1', kind: 'commitment_slipping', headline: 'A plan is due.', body: 'Due soon.', holdSource } });
        expect(getMainChatProactive(row)?.holdSource).toEqual(holdSource);
        expect(getMainChatProactive({ ...row, toolInput: { ...row.toolInput as object, holdSource: { thing: { title: 42 } } } })?.holdSource).toBeUndefined();
    });

    it('preserves work results and permission details without accepting malformed metadata', () => {
        const work = { handoffId: 'work-1', status: 'needs_review', summary: 'Ready.', result: 'Full result.', deliverables: [{ label: 'Report', url: '/tmp/report.md' }] };
        const permission = { requestId: 'request-1', sessionId: 'session-1', handoffId: 'work-1', toolName: 'Bash', details: 'Run tests.' };
        const row = item('p1', 'system', { toolName: MAIN_CHAT_PROACTIVE_TOOL, toolInput: { momentId: 'm1', kind: 'work_finished', headline: 'Ready.', body: 'Review your result.', work, permission } });
        expect(getMainChatProactive(row)).toMatchObject({ work, permission });
        const malformed = { ...row, toolInput: { ...row.toolInput as object, work: { ...work, deliverables: [{ label: 'No URL' }, ...work.deliverables] }, permission: { ...permission, details: { command: 'anything' } } } };
        expect(getMainChatProactive(malformed)).toMatchObject({ work });
        expect(getMainChatProactive(malformed)?.permission).toBeUndefined();
        expect(getMainChatProactive({ ...row, toolInput: { ...row.toolInput as object, work: { handoffId: 42 }, permission: { requestId: '' } } })).toMatchObject({ work: undefined, permission: undefined });
    });

    it('repairs legacy capture and mail destinations, including evidence and status entries', () => {
        for (const [kind, oldTarget, expected] of [
            ['capture_paused', { kind: 'settings', id: 'notifications' }, { kind: 'settings', id: 'activity' }],
            ['waiting_on_reply', { kind: 'today' }, { kind: 'mail' }],
        ] as const) {
            const row = item('p1', 'system', { toolName: MAIN_CHAT_PROACTIVE_TOOL, toolInput: { momentId: 'm1', kind, headline: 'An update.', body: 'An update.', target: oldTarget, evidence: [{ kind, id: 'ref-1', target: oldTarget }] } });
            expect(getMainChatProactive(row)).toMatchObject({ target: expected, evidence: [{ target: expected }] });
        }
        const status = item('s1', 'system', { toolName: MAIN_CHAT_STATUS_TOOL, toolInput: { period: 'morning', entries: [{ id: 'e1', kind: 'capture_paused', text: 'Capture is paused.', target: { kind: 'settings', id: 'notifications' } }] } });
        expect(getMainChatStatus(status)).toMatchObject({ entries: [{ target: { kind: 'settings', id: 'activity' } }] });
    });
    it('parses structured payloads without turning missing or unknown fields into actions', () => {
        const proactive = item('p1', 'system', { toolName: MAIN_CHAT_PROACTIVE_TOOL, toolInput: { momentId: 'm1', kind: 'brief_ready', headline: 'Ready.', body: 'Your brief is ready.', actionIds: ['open', 'unknown'], actionLabels: { open: 'Open brief' }, evidence: [{ kind: 'brief', id: 'b1' }] } });
        expect(getMainChatProactive(proactive)).toMatchObject({ momentId: 'm1', actionIds: ['open'], target: { kind: 'none' }, state: 'pending', evidence: [{ kind: 'brief', id: 'b1', target: { kind: 'none' } }] });
        expect(getMainChatProactive({ ...proactive, toolInput: { momentId: 'm1', body: 2 } })).toBeNull();
        expect(getMainChatStatus(item('s1', 'system', { toolName: MAIN_CHAT_STATUS_TOOL, toolInput: { period: 'morning', entries: [{ id: 'e1', text: 'Done.' }] } }))).toMatchObject({ entries: [{ target: { kind: 'none' } }] });
    });

    it('keeps proactive cards and folded status outside processing blocks', () => {
        const turns = splitIntoTurns(groupStreamItems([
            item('p1', 'system', { toolName: MAIN_CHAT_PROACTIVE_TOOL }),
            item('s1', 'system', { toolName: MAIN_CHAT_STATUS_TOOL }),
        ]), false);
        expect(turns[0].segments[0].finals.map(groupedId)).toEqual(['p1', 's1']);
        expect(turns[0].segments[0].processing).toEqual([]);
    });
});

describe('hub selectors', () => {
    it('takes the newest report lines, oldest first, and the newest side chats', () => {
        const report = (id: string) =>
            item(id, 'system', { toolName: MAIN_CHAT_REPORT_TOOL, toolInput: { source: 'handoff', action: 'review', target: { kind: 'session', id: 's' } } });
        const items = [item('u1', 'user'), report('r1'), item('a1', 'assistant'), report('r2'), report('r3'), item('s1', 'system', { content: 'Plan rejected.' })];
        expect(selectHubReports(items, 2).map((i) => i.id)).toEqual(['r2', 'r3']);
        expect(selectHubReports(items, 10).map((i) => i.id)).toEqual(['r1', 'r2', 'r3']);
        const sessions = [
            { ...createSession('m', 'ws', 'Main'), localId: 'main', kind: 'main' as const, updatedAt: 50 },
            { ...createSession('m', 'ws', 'Old side'), localId: 'old', kind: 'side' as const, updatedAt: 10 },
            { ...createSession('m', 'ws', 'New side'), localId: 'new', kind: 'side' as const, updatedAt: 30 },
            { ...createSession('m', 'ws', 'Plain'), localId: 'plain', updatedAt: 40 },
        ];
        expect(selectHubSideChats(sessions, 5).map((s) => s.localId)).toEqual(['new', 'old']);
        expect(selectHubSideChats(sessions, 1).map((s) => s.localId)).toEqual(['new']);
    });
});

describe('phone idea source attribution', () => {
    const offerReferences = [
        { id: 'suggestion.notes', version: 'a'.repeat(64) },
        { id: 'find.research', version: 'b'.repeat(64) },
    ];

    it('preserves admitted source references through history load, renderer save and reload', () => {
        const stored = {
            id: 'phone-message', kind: 'user', content: 'Edited idea text',
            automationContext: JSON.stringify({ offerReferences }),
        };
        const rendered = fromStreamItemData(stored);
        expect(rendered.offerReferences).toEqual(offerReferences);
        const saved = toStreamItemData(rendered);
        expect(JSON.parse(saved.automationContext!)).toEqual({ offerReferences });
        expect(fromStreamItemData(saved).offerReferences).toEqual(offerReferences);
        expect(saved.content).toBe(stored.content);
        expect(saved.kind).toBe('user');
    });

    it.each([
        { output: 'Automation result', jobId: 'job-1' },
        { handoff: true, handoffId: 'handoff-1' },
    ])('keeps existing message context unchanged alongside source references: %j', (existing) => {
        for (const context of [existing, { ...existing, offerReferences }]) {
            const rendered = fromStreamItemData({ id: 'message', kind: 'user', automationContext: JSON.stringify(context) });
            expect(JSON.parse(toStreamItemData(rendered).automationContext!)).toEqual(context);
        }
    });

    it('keeps ordinary user text free of synthetic source metadata', () => {
        const rendered = fromStreamItemData({ id: 'plain', kind: 'user', content: 'User-authored text' });
        expect(rendered.offerReferences).toBeUndefined();
        expect(toStreamItemData(rendered).automationContext).toBeUndefined();
        expect(toStreamItemData({ ...rendered, offerReferences: [] }).automationContext).toBeUndefined();
    });

    it('reads valid source bindings without failing on malformed metadata entries', () => {
        const sourceMetadata = { offerReferences: [null, 'invalid', { id: 'missing-version' }, ...offerReferences] };
        const rendered = fromStreamItemData({
            id: 'message', kind: 'user', automationContext: JSON.stringify(sourceMetadata),
        });
        expect(rendered.offerReferences).toEqual(offerReferences);
        expect(JSON.parse(toStreamItemData(rendered).automationContext!)).toEqual(sourceMetadata);
    });
});


describe('durable source proof roundtrip', () => {
    it.each([
        { offerReferences: [{ id: 'suggestion.notes', version: 'a'.repeat(64) }], offerSources: [{ reference: { id: 'suggestion.notes', version: 'a'.repeat(64) }, sources: [{ kind: 'session', id: 'source' }], resolved: 'digest', futureProof: { revision: 2 } }] },
        { output: 'Automation result', jobId: 'job', offerSources: null },
        { handoff: true, handoffId: 'handoff', offerReferences: 'invalid' },
    ])('preserves the full source envelope, including unknown and malformed proofs: %j', (context) => {
        const raw = JSON.stringify(context);
        const rendered = fromStreamItemData({ id: 'source-message', kind: 'user', automationContext: raw });
        expect(toStreamItemData(rendered).automationContext).toBe(raw);
        expect(toStreamItemData(fromStreamItemData(toStreamItemData(rendered))).automationContext).toBe(raw);
    });

    it.each(['{"offerReferences":', '{"offer\\u0052eferences":', '{"offer\\u0053ources":null}', '["offerReferences"]', '"offerSources"', 'garbage {"\\u006fferReferences":[]}', '{"broken":, "\\u006fferSources":[]}', '{"OfferReferences":null}', '{"OFFERSOURCES":null}', '{"offerReference\\u017f":null}'])('preserves escaped or truncated source markers: %s', (raw) => {
        expect(toStreamItemData(fromStreamItemData({ id: 'source-message', kind: 'user', automationContext: raw })).automationContext).toBe(raw);
    });
});
