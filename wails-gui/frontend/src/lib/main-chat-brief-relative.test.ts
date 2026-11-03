import { describe, expect, it } from 'vitest';
import i18n from '@/i18n';
import type { MobileBriefDocument, MobileBriefParagraph } from '@/lib/wails-shim-app';
import { mainChatBriefRelativeText } from './main-chat-brief-relative';

describe('brief days when read', () => {
    it('renders the walkthrough Friday due date on Monday and leaves the stored words intact', async () => {
        await i18n.changeLanguage('en');
        const paragraph: MobileBriefParagraph = { text: 'The commitment is due today.', refs: ['commitment'] };
        const brief = { asOf: '2026-10-30T18:00:00-07:00', timezone: 'America/Los_Angeles', sections: { commitments: [{ refs: ['commitment'], dueAt: '2026-10-30T23:59:00-07:00' }] } } as unknown as MobileBriefDocument;
        const t = (key: string, options?: Record<string, unknown>) => i18n.t(`chat:${key}` as never, options as never) as string;
        expect(mainChatBriefRelativeText(brief, paragraph, new Date('2026-11-02T12:00:00-08:00'), 'en', t)).toBe('The commitment was due Friday.');
        expect(paragraph.text).toBe('The commitment is due today.');
    });
    it('updates relative days in Chinese and across the local midnight boundary', async () => {
        await i18n.changeLanguage('zh-CN');
        const brief = { asOf: '2026-10-30T18:00:00-07:00', timezone: 'America/Los_Angeles', sections: {} } as unknown as MobileBriefDocument;
        const paragraph = { text: '明天开会。', refs: [] };
        const t = (key: string, options?: Record<string, unknown>) => i18n.t(`chat:${key}` as never, options as never) as string;
        expect(mainChatBriefRelativeText(brief, paragraph, new Date('2026-10-31T10:00:00-07:00'), 'zh-CN', t)).toBe('今天开会。');
    });
});
