import { afterEach, describe, expect, it } from 'vitest';
import i18n from '@/i18n';
import { eventActionLabel, isRoleName, stepEventIndexes } from './activity-page';
import type { activitymon } from '../../wailsjs/go/models';

const ev = (fields: Partial<activitymon.ActivityEvent>) => ({ appName: 'Claude', timestamp: 1_000, ...fields }) as activitymon.ActivityEvent;

afterEach(async () => {
    await i18n.changeLanguage('en');
});

describe('Activity steps in words (live walk AR57)', () => {
    it('treats accessibility roles as no name', () => {
        for (const role of ['AXGroup', 'Button', 'StaticText', 'AXButton', '']) expect(isRoleName(role)).toBe(true);
        expect(isRoleName('Allow for this session')).toBe(false);
    });

    it('a click reads in words with its app, and a role-only click has no object', () => {
        const t = i18n.getFixedT('en', 'activity');
        const click = eventActionLabel(ev({ kind: 'click', clickLabel: 'Allow for this session', clickRole: 'AXButton' }), t);
        expect(`${click.verb} ${click.object}`).toBe('Clicked Allow for this session in Claude');
        expect(eventActionLabel(ev({ kind: 'focus', focusRole: 'AXGroup' }), t).object).toBe('');
    });

    it('a click and its focus are one step; a step with no readable name is not a step', () => {
        const events = [
            ev({ kind: 'focus', focusLabel: 'Allow for this session', timestamp: 1_000 }),
            ev({ kind: 'click', clickLabel: 'Allow for this session', timestamp: 1_200 }),
            ev({ kind: 'focus', focusRole: 'AXGroup', timestamp: 2_000 }),
            ev({ kind: 'click', clickRole: 'AXButton', clickLabel: 'Button', timestamp: 3_000 }),
            ev({ kind: 'focus', focusLabel: 'Message', timestamp: 4_000 }),
        ];
        expect(stepEventIndexes(events)).toEqual([1, 4]);
    });
});
