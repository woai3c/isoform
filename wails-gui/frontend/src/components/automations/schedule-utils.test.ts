import { describe, expect, it } from 'vitest';
import { cronToHumanReadable, getPresetSummary, getWeekdayToggles } from './schedule-utils';

const zhLabels = {
    runOnDemand: '按需运行',
    everyHourAt: '每小时 :{{minute}}',
    daily: '每天 {{time}}',
    weekdays: '工作日 {{time}}',
    weekdayLabels: {
        '1': '周一',
        '2': '周二',
        '3': '周三',
        '4': '周四',
        '5': '周五',
        '6': '周六',
        '0': '周日',
    },
};

describe('schedule text helpers', () => {
    it('keeps English defaults for existing callers', () => {
        expect(getPresetSummary('daily', '9', '0', new Set(['1']))).toBe('Daily 09:00');
        expect(cronToHumanReadable('30 * * * *')).toBe('Every hour at :30');
    });

    it('accepts translated labels for summaries and weekday toggles', () => {
        expect(cronToHumanReadable('15 10 * * 1-5', zhLabels)).toBe('工作日 10:15');
        expect(getPresetSummary('weekly', '8', '5', new Set(['1', '3']), zhLabels)).toBe('周一, 周三 08:05');
        expect(getWeekdayToggles(zhLabels).map((d) => d.short)).toEqual([
            '周一',
            '周二',
            '周三',
            '周四',
            '周五',
            '周六',
            '周日',
        ]);
    });
});
