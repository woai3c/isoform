import { beforeEach, describe, expect, it } from 'vitest';
import i18n from '@/i18n';
import {
    candidateDatesLabel,
    candidateDurationLabel,
    groupByTime,
    knowHowKind,
    matchWhen,
    originSentence,
    playbookRequest,
    reuseLabel,
    savedThisWeek,
    savedTimestamp,
    scopeLabel,
    timeGroup,
    topSources,
} from './know-how';

const t = i18n.getFixedT('en', 'knowledge');
// Thursday, October 22, 2026, 10:00 local time.
const NOW = new Date(2026, 9, 22, 10, 0);
const at = (days: number) => new Date(2026, 9, 22 - days, 9, 0).getTime();

describe('know-how helpers', () => {
    beforeEach(async () => {
        await i18n.changeLanguage('en');
    });

    it('reads an unknown kind as reference', () => {
        expect(knowHowKind('fix')).toBe('fix');
        expect(knowHowKind('pattern')).toBe('reference');
        expect(knowHowKind(undefined)).toBe('reference');
    });

    it('says reuse counts in words', () => {
        // W15: nothing until it has been used.
        expect(reuseLabel(t, 0)).toBe('');
        expect(reuseLabel(t, 1)).toBe('Used once');
        expect(reuseLabel(t, 2)).toBe('Used twice');
        expect(reuseLabel(t, 4)).toBe('Used 4 times');
    });

    it('groups by when it was saved', () => {
        expect(timeGroup(at(0), NOW)).toBe('today');
        expect(timeGroup(at(1), NOW)).toBe('yesterday');
        expect(timeGroup(at(5), NOW)).toBe('thisWeek');
        expect(timeGroup(at(12), NOW)).toBe('thisMonth');
        expect(timeGroup(at(40), NOW)).toBe('earlier');
        expect(timeGroup(0, NOW)).toBe('earlier');
        const groups = groupByTime([{ savedAt: at(5) }, { savedAt: at(0) }, { savedAt: at(5) }], NOW);
        expect(groups.map(([key, items]) => [key, items.length])).toEqual([['today', 1], ['thisWeek', 2]]);
        expect(savedThisWeek([{ savedAt: at(0) }, { savedAt: at(6) }, { savedAt: at(7) }, { savedAt: 0 }], NOW)).toBe(2);
    });

    it('writes the banner date in words and keeps the raw time for Technical details', () => {
        expect(matchWhen(t, at(0), 'en', NOW)).toBe('earlier today');
        expect(matchWhen(t, at(1), 'en', NOW)).toBe('yesterday');
        expect(matchWhen(t, at(3), 'en', NOW)).toBe('on Oct 19');
        expect(savedTimestamp(Date.UTC(2026, 9, 20, 2, 29, 41))).toBe('2026-10-20T02:29Z');
        expect(savedTimestamp(0)).toBe('');
    });

    it('counts top sources once per entry', () => {
        expect(topSources([{ apps: ['Terminal', 'Terminal'] }, { apps: ['Terminal', 'Claude'] }, { apps: null }], 8)).toEqual([
            ['Terminal', 2],
            ['Claude', 1],
        ]);
    });

    it('names where an entry came from without the raw source', () => {
        expect(originSentence(t, 'activity')).toBe('Learned from your work on this computer.');
        expect(originSentence(t, 'gmail')).toBe('Learned from your Gmail messages.');
        expect(originSentence(t, 'something-new')).toBe('Learned from your work.');
        expect(originSentence(t, 'observed', 4)).toBe('The same steps, in the same order, in 4 sessions.');
        expect(originSentence(t, 'observed', 1)).toBe('The same steps, in the same order, in 1 session.');
    });

    it('reads the scope chip; only mine exists today', () => {
        expect(scopeLabel(t, 'mine')).toBe('Mine');
        expect(scopeLabel(t, undefined)).toBe('Mine');
        expect(scopeLabel(t, '')).toBe('Mine');
        expect(scopeLabel(t, 'team')).toBe('Team');
    });

    it('writes the proposal dates and duration in words', () => {
        expect(candidateDatesLabel(t, [at(7), at(1), at(0)], 'en', NOW)).toBe('Oct 15, yesterday, and today');
        expect(candidateDatesLabel(t, [at(0)], 'en', NOW)).toBe('today');
        expect(candidateDurationLabel(t, 40 * 60_000)).toBe('40 min');
        expect(candidateDurationLabel(t, 60_000)).toBe('1 min');
        expect(candidateDurationLabel(t, 0)).toBe('1 min');
        expect(candidateDurationLabel(t, 66 * 60_000)).toBe('1h 6m');
        expect(candidateDurationLabel(t, 120 * 60_000)).toBe('2h');
    });

    it('W8: Turn into a playbook hands the author the how-to in its own words', () => {
        expect(
            playbookRequest({ title: 'Pair the phone app', takeaway: 'Scan the code.', trigger: 'A new phone', points: ['Open Settings', 'Scan'] }),
        ).toBe('Pair the phone app\nScan the code.\nA new phone\n1. Open Settings\n2. Scan');
        expect(playbookRequest({ title: 'Pair', takeaway: '', trigger: '', points: null } as never)).toBe('Pair');
    });
});
