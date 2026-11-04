import { describe, expect, it } from 'vitest';
import i18n from '@/i18n';
import { durationWords, formatAbsolute, togetherWords } from './people-format';

const t = (lng: string) => (key: string, options?: Record<string, unknown>) => i18n.t(`people:${key}` as never, { lng, ...options } as never) as unknown as string;

describe('Who you know times', () => {
    it('N26: durations read 24 min, 1h 6m, 1h 30m, 9h and 3 days', () => {
        const en = t('en');
        expect(durationWords(en, 24 * 60_000)).toBe('24 min');
        expect(durationWords(en, 66 * 60_000)).toBe('1h 6m');
        expect(durationWords(en, 90 * 60_000)).toBe('1h 30m');
        expect(durationWords(en, 9 * 3_600_000)).toBe('9h');
        expect(durationWords(en, 3 * 86_400_000)).toBe('3 days');
        expect(durationWords(t('zh-CN'), 90 * 60_000)).toBe('1 小时 30 分钟');
    });
    it('N23: absolute times use the computer clock', () => {
        const today = new Date(new Date().setHours(13, 5, 0, 0));
        expect(formatAbsolute('en', today.getTime())).toBe('1:05 PM');
        const yesterday = new Date(today);
        yesterday.setDate(today.getDate() - 1);
        yesterday.setHours(19, 1, 0, 0);
        expect(formatAbsolute('en', yesterday.getTime())).toBe('Yesterday, 7:01 PM');
        expect(formatAbsolute('en', today.getTime())).not.toMatch(/13:05|\/2026/);
    });
});

describe('N41: each Together line says who did it', () => {
    const line = (kind: string, title: string, app: string, direction?: string, durationMs = 0) => ({ kind, title, app, durationMs, direction });

    it('a message from them and one from you, in English', () => {
        const en = t('en');
        expect(togetherWords(en, line('message', '', 'email', 'in'), 'Priya Raman')).toBe('Priya emailed you');
        expect(togetherWords(en, line('message', '', 'wechat', 'in'), 'Priya Raman')).toBe('Priya messaged you · WeChat');
        expect(togetherWords(en, line('message', '', 'email', 'out'), 'Priya Raman')).toBe('You emailed Priya');
        expect(togetherWords(en, line('message', '', 'wechat', 'out'), 'Priya Raman')).toBe('You messaged Priya · WeChat');
        expect(togetherWords(en, line('activity', 'Messaged Priya about the launch timing', 'WeChat'), 'Priya Raman')).toBe('You messaged Priya about the launch timing · WeChat');
        expect(togetherWords(en, line('activity', 'Wrote the Halcyon launch notes for Priya', 'Pages'), 'Priya Raman')).toBe('You wrote the Halcyon launch notes for Priya · Pages');
        expect(togetherWords(en, line('activity', 'You reviewed the deck with Priya', 'Keynote'), 'Priya Raman')).toBe('You reviewed the deck with Priya · Keynote');
        expect(togetherWords(en, line('activity', 'Halcyon launch notes', 'Pages'), 'Priya Raman')).toBe('Halcyon launch notes · Pages');
        expect(togetherWords(en, line('meeting', 'Kickoff call', 'calendar', '', 66 * 60_000), 'Priya Raman')).toBe('Kickoff call · 1h 6m');
    });

    it('a message whose direction Isoform does not know names neither side', () => {
        const en = t('en');
        expect(togetherWords(en, line('message', '', 'email'), 'Priya Raman')).toBe('Email with Priya');
        expect(togetherWords(en, line('message', '', 'slack', ''), 'Priya Raman')).toBe('Messages with Priya · Slack');
    });

    it('zh-CN: incoming says they wrote to you, never 给…发了消息', () => {
        const zh = t('zh-CN');
        expect(togetherWords(zh, line('message', '', 'email', 'in'), '王晓雨')).toBe('王晓雨给你发了邮件');
        expect(togetherWords(zh, line('message', '', 'wechat', 'in'), '王晓雨')).toBe('王晓雨给你发了消息 · WeChat');
        expect(togetherWords(zh, line('message', '', 'wechat', 'out'), '王晓雨')).toBe('你给王晓雨发了消息 · WeChat');
        expect(togetherWords(zh, line('message', '', 'email'), '王晓雨')).toBe('和王晓雨的邮件');
        expect(togetherWords(zh, line('activity', '和王晓雨聊了发布时间', 'WeChat'), '王晓雨')).toBe('你和王晓雨聊了发布时间 · WeChat');
        expect(togetherWords(zh, line('activity', '你为王晓雨写了发布说明', 'Pages'), '王晓雨')).toBe('你为王晓雨写了发布说明 · Pages');
        expect(togetherWords(zh, line('message', '', 'wechat', 'in'), '王晓雨')).not.toMatch(/^给/);
    });
});
