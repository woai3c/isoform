import { describe, expect, it } from 'vitest';
import { fitChipLabel } from './day-strip-chip-label';

const measure = (text: string) => text.length;

describe('fitChipLabel', () => {
    it('shows the whole short name only when it fits', () => {
        expect(fitChipLabel('Priya', 5, measure)).toBe('Priya');
        expect(fitChipLabel('Priya', 4, measure, 'P')).toBe('');
        expect(fitChipLabel('P', 5, measure)).toBe('');
        expect(fitChipLabel('NMG Check In', 10, measure)).toBe('');
        expect(fitChipLabel('项目评审', 3, measure, '项')).toBe('');
    });
});
