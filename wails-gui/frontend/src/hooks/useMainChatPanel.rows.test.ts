import { describe, expect, it, vi } from 'vitest';
import { MAIN_CHAT_REPORT_TOOL } from '@/lib/stream-helpers';
import type { StreamItem } from '@/types';
import { panelRows } from './useMainChatPanel';

vi.mock('@/lib/wails-shim-app', () => ({
    SendMainChat: vi.fn(),
    SendMainChatWithContext: vi.fn(),
}));

describe('Main Chat panel rows', () => {
    it('keeps the same stream rows as the Chat page', () => {
        const receipt: StreamItem = {
            id: 'receipt', kind: 'system', toolName: MAIN_CHAT_REPORT_TOOL,
            toolInput: { source: 'message_send', action: '', target: { kind: 'none' }, channel: 'mail', sentAt: 1 },
        };
        const user: StreamItem = { id: 'user', kind: 'user', content: 'Send the reply' };
        const otherReport: StreamItem = {
            id: 'handoff-report', kind: 'system', toolName: MAIN_CHAT_REPORT_TOOL, content: 'Work finished',
            toolInput: { source: 'handoff', action: 'review', target: { kind: 'session', id: 'work-1' }, status: 'completed' },
        };
        const tool: StreamItem = { id: 'tool', kind: 'system', toolName: 'other_tool', content: 'Hidden' };
        expect(panelRows([receipt, user, otherReport, tool])).toEqual([receipt, user, otherReport, tool]);
    });

    it('keeps conversation rows regardless of their stored id prefix', () => {
        const rows: StreamItem[] = [
            { id: '__old-question', kind: 'user', content: 'What changed?' },
            { id: '__old-answer', kind: 'assistant', content: 'The plan changed.' },
        ];
        expect(panelRows(rows)).toEqual(rows);
    });
});
