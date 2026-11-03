import { describe, expect, it } from 'vitest';
import i18n from '@/i18n';
import { beliefSummary, durationWords } from './about-you';

describe('About you wording', () => {
    const en = i18n.getFixedT('en', 'memory');
    const zh = i18n.getFixedT('zh-CN', 'memory');
    const now = new Date(2026, 9, 22, 9);

    it('names how long a moment lasted, and nothing under a minute', () => {
        expect(durationWords(en, 30_000)).toBe('');
        expect(durationWords(en, 60_000)).toBe('1 minute');
        expect(durationWords(en, 25 * 60_000)).toBe('25 minutes');
        expect(durationWords(en, 2 * 3600_000 + 10 * 60_000)).toBe('2 hours');
        expect(durationWords(zh, 3600_000)).toBe('1 小时');
    });

    it('describes how much of the work the beliefs come from', () => {
        expect(beliefSummary(en, 1, now.getTime() - 3600_000, now)).toBe("1 belief from today's work.");
        expect(beliefSummary(en, 3, new Date(2026, 9, 20).getTime(), now)).toBe('3 beliefs from 3 days of your work.');
        expect(beliefSummary(en, 48, new Date(2026, 8, 10).getTime(), now)).toBe('48 beliefs from 6 weeks of your work.');
        expect(beliefSummary(en, 48, new Date(2026, 5, 1).getTime(), now)).toBe('48 beliefs from 5 months of your work.');
        expect(beliefSummary(zh, 48, new Date(2026, 8, 10).getTime(), now)).toBe('48 条认识，来自你 6 周的工作。');
    });
});
