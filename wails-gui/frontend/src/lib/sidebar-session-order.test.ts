import { describe, expect, it } from 'vitest';
import type { Session } from '@/types';
import { groupSidebarSessions } from './sidebar-session-order';

function session(
    localId: string,
    updatedAt: number,
    pinned = false,
    pinnedAt = 0,
): Session {
    return { localId, updatedAt, pinned, pinnedAt } as Session;
}

describe('groupSidebarSessions', () => {
    it('moves pinned chats out of Recent and orders both sections', () => {
        const result = groupSidebarSessions([
            session('recent-old', 100),
            session('pinned-second', 400, true, 20),
            session('recent-new', 300),
            session('pinned-first', 200, true, 10),
        ]);

        expect(result.pinnedSessions.map((item) => item.localId)).toEqual([
            'pinned-first',
            'pinned-second',
        ]);
        expect(result.recentSessions.map((item) => item.localId)).toEqual([
            'recent-new',
            'recent-old',
        ]);
    });

    it('restores an unpinned chat to its updated-time position in Recent', () => {
        const result = groupSidebarSessions([
            session('older', 100),
            session('unpinning', 200),
            session('newer', 300),
        ]);

        expect(result.recentSessions.map((item) => item.localId)).toEqual([
            'newer',
            'unpinning',
            'older',
        ]);
    });
});
