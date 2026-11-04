import { describe, expect, it } from 'vitest';
import i18n from '@/i18n';
import type { MobileBriefDocument, MobileBriefParagraph } from '@/lib/wails-shim-app';
import { mainChatBriefMissedText, mainChatBriefRelativeText, mainChatPastLineText } from './main-chat-brief-relative';
import { computerClock } from '@/lib/brief-time';

describe('brief days when read', () => {
    it('renders the walkthrough Friday due date on Monday and leaves the stored words intact', async () => {
        await i18n.changeLanguage('en');
        const paragraph: MobileBriefParagraph = { text: 'The commitment is due today.', refs: ['commitment'] };
        const brief = { asOf: '2026-10-30T18:00:00-07:00', timezone: 'America/Los_Angeles', sections: { commitments: [{ refs: ['commitment'], dueAt: '2026-10-30T23:59:00-07:00' }] } } as unknown as MobileBriefDocument;
        const t = (key: string, options?: Record<string, unknown>) => i18n.t(`chat:${key}` as never, options as never) as unknown as string;
        expect(mainChatBriefRelativeText(brief, paragraph, new Date('2026-11-02T12:00:00-08:00'), 'en', t)).toBe('The commitment was due Friday.');
        expect(paragraph.text).toBe('The commitment is due today.');
    });
    it('updates relative days in Chinese and across the local midnight boundary', async () => {
        await i18n.changeLanguage('zh-CN');
        const brief = { asOf: '2026-10-30T18:00:00-07:00', timezone: 'America/Los_Angeles', sections: {} } as unknown as MobileBriefDocument;
        const paragraph = { text: '明天开会。', refs: [] };
        const t = (key: string, options?: Record<string, unknown>) => i18n.t(`chat:${key}` as never, options as never) as unknown as string;
        expect(mainChatBriefRelativeText(brief, paragraph, new Date('2026-10-31T10:00:00-07:00'), 'zh-CN', t)).toBe('今天开会。');
    });
});

describe('past fold lines read from their own day (Thursday polish C2)', () => {
    const t = (key: string, options?: Record<string, unknown>) => i18n.t(`chat:${key}` as never, options as never) as unknown as string;
    it('says "was due" for a day already past and names the day from the line, not from today', async () => {
        await i18n.changeLanguage('en');
        const thursday = '2026-10-29';
        const monday = '2026-11-02';
        expect(mainChatPastLineText('PT is due Thursday.', thursday, monday, 'en', t)).toBe('PT was due Thursday.');
        expect(mainChatPastLineText('PT is due today.', thursday, monday, 'en', t)).toBe('PT was due Thursday.');
        expect(mainChatPastLineText('The forms are due tomorrow.', thursday, monday, 'en', t)).toBe('The forms were due Friday.');
        expect(mainChatPastLineText('Calendar checked for tomorrow.', thursday, monday, 'en', t)).toBe('Calendar checked for Friday.');
        expect(mainChatPastLineText('Today: two meetings.', thursday, monday, 'en', t)).toBe('Thursday: two meetings.');
        expect(mainChatPastLineText('The permit is due Monday.', thursday, monday, 'en', t)).toBe('The permit is due today.');
        expect(mainChatPastLineText('The permit is due tomorrow.', '2026-11-01', monday, 'en', t)).toBe('The permit is due today.');
        expect(mainChatPastLineText('PT is due today.', '2026-11-01', monday, 'en', t)).toBe('PT was due yesterday.');
        expect(mainChatPastLineText('PT is due today.', '2026-10-20', monday, 'en', t)).toBe('PT was due Oct 20.');
        for (const line of ['PT is due Thursday.', 'PT is due today.', 'Calendar checked for tomorrow.']) {
            expect(mainChatPastLineText(line, thursday, monday, 'en', t)).not.toMatch(/\bis due\b|\btoday\b|\btomorrow\b/i);
        }
    });
    it('leaves lines from today and lines with no day alone', async () => {
        await i18n.changeLanguage('en');
        expect(mainChatPastLineText('PT is due today.', '2026-11-02', '2026-11-02', 'en', t)).toBe('PT is due today.');
        expect(mainChatPastLineText('PT is due today.', undefined, '2026-11-02', 'en', t)).toBe('PT is due today.');
    });
    it('works out Chinese day words the same way', async () => {
        await i18n.changeLanguage('zh-CN');
        expect(mainChatPastLineText('理疗周四到期。', '2026-10-29', '2026-11-02', 'zh-CN', t)).toBe('理疗已于星期四到期。');
        expect(mainChatPastLineText('理疗今天到期。', '2026-10-29', '2026-11-02', 'zh-CN', t)).toBe('理疗已于星期四到期。');
        expect(mainChatPastLineText('明天的日历已查看。', '2026-10-29', '2026-11-02', 'zh-CN', t)).toBe('星期五的日历已查看。');
        await i18n.changeLanguage('en');
    });
});

describe('a missed brief is worded from its own day (chat-review v1.0.10 H9)', () => {
    const t = (key: string, options?: Record<string, unknown>) => i18n.t(`chat:${key}` as never, options as never) as unknown as string;
    const tToday = (key: string, options?: Record<string, unknown>) => i18n.t(`today:${key}` as never, options as never) as unknown as string;
    it("says last night's evening brief, its time and Today's cause in English", async () => {
        await i18n.changeLanguage('en');
        const evening = { briefDay: '2026-11-01', briefSlot: 'evening', briefTime: '18:00' };
        expect(mainChatBriefMissedText(evening, '2026-11-02', 'en', t, tToday)).toBe("Last night's evening brief didn't come at 6:00 PM.");
        expect(mainChatBriefMissedText({ ...evening, briefCause: 'asleep' }, '2026-11-02', 'en', t, tToday)).toBe("Last night's evening brief didn't come at 6:00 PM: your computer was asleep.");
        expect(mainChatBriefMissedText({ ...evening, briefCause: 'closed' }, '2026-11-02', 'en', t, tToday)).toBe("Last night's evening brief didn't come at 6:00 PM: Isoform wasn't open.");
        expect(mainChatBriefMissedText(evening, '2026-11-01', 'en', t, tToday)).toBe("This evening's evening brief didn't come at 6:00 PM.");
        expect(mainChatBriefMissedText({ briefDay: '2026-11-01', briefSlot: 'morning', briefTime: '07:30', briefCause: 'failed' }, '2026-11-02', 'en', t, tToday)).toBe("Yesterday's morning brief didn't come at 7:30 AM: it couldn't be made.");
        expect(mainChatBriefMissedText({ ...evening, briefDay: '2026-10-29' }, '2026-11-02', 'en', t, tToday)).toBe("Thursday's evening brief didn't come at 6:00 PM.");
        expect(mainChatBriefMissedText({ ...evening, briefDay: '2026-10-20' }, '2026-11-02', 'en', t, tToday)).toBe("The Oct 20 evening brief didn't come at 6:00 PM.");
    });
    it('says the same in Chinese with Today\'s cause words', async () => {
        await i18n.changeLanguage('zh-CN');
        const evening = { briefDay: '2026-11-01', briefSlot: 'evening', briefTime: '18:00', briefCause: 'asleep' };
        expect(mainChatBriefMissedText(evening, '2026-11-02', 'zh-CN', t, tToday)).toBe(`昨晚的晚间简报没有在${mainChatBriefMissedClock('zh-CN')}送达：电脑在睡眠。`);
    });
    it('leaves a line without the brief day, slot or time to its saved text', () => {
        expect(mainChatBriefMissedText({ briefSlot: 'evening', briefTime: '18:00' }, '2026-11-02', 'en', t, tToday)).toBeNull();
        expect(mainChatBriefMissedText({ briefDay: '2026-11-01', briefSlot: 'on-request', briefTime: '18:00' }, '2026-11-02', 'en', t, tToday)).toBeNull();
        expect(mainChatBriefMissedText({ briefDay: '2026-11-01', briefSlot: 'evening' }, '2026-11-02', 'en', t, tToday)).toBeNull();
        expect(mainChatBriefMissedText({ briefDay: '2026-11-03', briefSlot: 'evening', briefTime: '18:00' }, '2026-11-02', 'en', t, tToday)).toBeNull();
    });
});

function mainChatBriefMissedClock(language: string) {
    return computerClock(new Date(2026, 10, 1, 18, 0), language);
}
