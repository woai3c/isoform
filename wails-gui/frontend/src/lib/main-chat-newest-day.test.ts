import { describe, expect, it } from 'vitest';
import type { StreamItem } from '@/types';
import { buildMainChatDays, mainChatStreamRows } from './main-chat-view';

// Bo's first page on 2026-11-05 (sessions.db, newest 50 rows): 44 system cards,
// six of them from 2026-11-04. Every card is a main_chat_proactive system row.
const TODAY = '2026-11-05';

function card(sortOrder: number, localDay: string, rowClass: StreamItem['rowClass']): StreamItem {
    return {
        id: `card-${sortOrder}`,
        kind: 'system',
        content: `Card ${sortOrder}`,
        toolName: 'main_chat_proactive',
        toolInput: { momentId: `moment-${sortOrder}`, kind: 'decision_due', headline: `Headline ${sortOrder}`, body: 'Body', state: 'pending', actionIds: [], actionLabels: {}, evidence: [], target: { kind: 'today', id: '' } },
        sortOrder,
        localDay,
        rowClass,
    } as unknown as StreamItem;
}

function firstPage(rowClass: StreamItem['rowClass']): StreamItem[] {
    const items: StreamItem[] = [];
    for (let i = 0; i < 38; i++) items.push(card(300 + i, i < 20 ? '2026-10-31' : '2026-11-03', rowClass));
    for (let i = 0; i < 6; i++) items.push(card(400 + i, '2026-11-04', rowClass));
    return items;
}

describe('the first page keeps the newest day', () => {
    it('shows the Nov 4 cards when they are conversation rows', () => {
        const days = buildMainChatDays(mainChatStreamRows(firstPage('other')), TODAY);
        const newest = days.find((day) => day.day === '2026-11-04');
        expect(newest).toBeDefined();
        expect(newest!.items.length + newest!.routine.length).toBe(6);
    });

    it('keeps the Nov 4 day when its cards are folded routine rows', () => {
        const days = buildMainChatDays(mainChatStreamRows(firstPage('routine')), TODAY);
        const newest = days.find((day) => day.day === '2026-11-04');
        expect(newest).toBeDefined();
        expect(newest!.routineCount).toBeGreaterThan(0);
    });
});
