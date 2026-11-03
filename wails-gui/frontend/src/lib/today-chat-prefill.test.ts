import { beforeEach, describe, expect, it } from 'vitest';
import i18n from '@/i18n';
import { formatTodayChatPrefill } from './today-chat-prefill';
import type { TodayChatPrefill } from './today-chat-navigation';

const now = new Date(2026, 9, 17, 10);
const at = (day: number, hour = 9) => new Date(2026, 9, day, hour).getTime();
const format = (item: Partial<TodayChatPrefill>) => formatTodayChatPrefill(
    { kind: 'generic', title: 'Launch plan', ...item }, i18n.getFixedT(null, 'today'), i18n.language, now,
);

describe('Today item-specific draft wording', () => {
    beforeEach(async () => { await i18n.changeLanguage('en'); });

    it.each([
        ['purpose', 'Where do I stand on "Launch plan" and what is the next step?'],
        ['file', 'Walk me through "Launch plan" and what is left to do.'],
        ['news', 'Why does "Launch plan" matter for me?'],
        ['open_loop', 'What should I do about "Launch plan"?'],
        ['generic', 'Help me decide what to do next about "Launch plan".'],
    ])('names the item and asks a specific %s question', (kind, expected) => {
        expect(format({ kind })).toBe(expected);
    });

    it('names work that moved and its observed duration, without its past clock time', () => {
        expect(format({ kind: 'activity', occurredAt: at(17), durationMinutes: 18 })).toBe(
            'Help me decide what to do next about "Launch plan", which I worked on earlier today for 18 minutes.',
        );
    });

    it.each([[16, 'yesterday'], [13, 'Tuesday'], [1, 'October 1']])('describes past work on day %s with %s', (date, day) => {
        const text = format({ kind: 'activity', occurredAt: at(Number(date)) });
        expect(text).toBe(`Help me decide what to do next about "Launch plan", which I worked on ${day}.`);
        expect(text).not.toMatch(/\d+:\d+|AM|PM/);
    });

    it('uses singular duration and does not invent a day for an unknown timestamp', () => {
        expect(format({ kind: 'activity', durationMinutes: 1 })).toContain('for 1 minute.');
        expect(format({ kind: 'activity', occurredAt: NaN })).toBe('Help me decide what to do next about "Launch plan".');
    });

    it('uses a clock time and participant names only for an upcoming meeting', () => {
        expect(format({ kind: 'meeting', startsAt: at(17, 11), people: ['Tim'] })).toBe(
            `Help me prepare for "Launch plan" at ${new Intl.DateTimeFormat('en', { hour: 'numeric', minute: '2-digit' }).format(at(17, 11))} with Tim.`,
        );
        expect(format({ kind: 'meeting', startsAt: at(17), people: ['Tim'] })).toBe('Help me follow up on "Launch plan" from today with Tim.');
    });

    it.each([[16, 'yesterday'], [13, 'Tuesday']])('names the past meeting day %s without a clock time', (date, day) => {
        const text = format({ kind: 'meeting', occurredAt: at(Number(date)), people: ['Tim'] });
        expect(text).toBe(`Help me follow up on "Launch plan" from ${day} with Tim.`);
        expect(text).not.toMatch(/\d+:\d+|AM|PM/);
    });

    it('prepares for a meeting without a clock time when its time is unknown or all-day', () => {
        expect(format({ kind: 'meeting', people: ['Tim'] })).toBe('Help me prepare for "Launch plan" with Tim.');
        expect(format({ kind: 'meeting', occurredAt: at(18), people: ['Tim'] })).toBe('Help me prepare for "Launch plan" with Tim.');
    });

    it('preserves the item title verbatim, including punctuation', () => {
        const title = 'Launch: "Alpha" & review <draft>';
        expect(format({ kind: 'file', title })).toBe(`Walk me through "${title}" and what is left to do.`);
    });

    it('only describes waiting since a known past day', () => {
        expect(format({ kind: 'open_loop', waitingSince: at(13) })).toBe('What should I do about "Launch plan"? It has been waiting since Tuesday.');
        expect(format({ kind: 'open_loop', waitingSince: at(18) })).toBe('What should I do about "Launch plan"?');
    });

    it.each([
        ['activity', '我今天早些时候处理过“发布计划”，花了 18 分钟。帮我决定接下来该怎么做。'],
        ['meeting', '帮我跟进今天的“发布计划”，与 Tim 一起。'],
        ['open_loop', '“发布计划”从星期二起一直在等处理。我该怎么做？'],
        ['purpose', '“发布计划”目前进展如何，下一步是什么？'],
        ['file', '带我看看“发布计划”，以及还有什么待完成。'],
        ['news', '“发布计划”为什么与我有关？'],
        ['generic', '帮我决定“发布计划”接下来该怎么做。'],
    ])('localizes the %s draft into Chinese', async (kind, expected) => {
        await i18n.changeLanguage('zh-CN');
        expect(format({ kind, title: '发布计划', occurredAt: at(17), durationMinutes: 18, startsAt: at(17), waitingSince: at(13), people: ['Tim'] })).toBe(expected);
    });
});

describe('whole brief editable drafts', () => {
    it.each([
        ['en', "Go through today's brief with me: what needs my attention first?"],
        ['zh-CN', '和我一起过一下今天的简报：我最该先关注什么？'],
    ])('keeps the opener in %s', async (language, expected) => {
        await i18n.changeLanguage(language);
        expect(format({ kind: 'brief', title: '2026-10-18' })).toBe(expected);
    });
});
