import { describe, expect, it } from 'vitest';
import { groupIdeas, ideaCopyIds, ideaHead, ideaKey, uniqueBy } from './idea-copies';

type Row = { id: string; title: string; savedAt: number };
const titleOf = (row: Row) => row.title;
const savedAtOf = (row: Row) => row.savedAt;

describe('ideaKey', () => {
    it('ignores case, whitespace, punctuation and word order', () => {
        const key = ideaKey('You prefer short replies.');
        expect(ideaKey('  you PREFER   short, replies ')).toBe(key);
        expect(ideaKey('Short replies: you prefer')).toBe(key);
    });

    it('keeps different wording apart', () => {
        expect(ideaKey('You prefer short replies')).not.toBe(ideaKey('You prefer short replies on Fridays'));
        expect(ideaKey('You prefer short replies')).not.toBe(ideaKey('You avoid short replies'));
    });

    it('matches Chinese titles that differ only in punctuation and spacing', () => {
        expect(ideaKey('你喜欢简短的回复。')).toBe(ideaKey('你喜欢简短的回复'));
    });

    it('returns an empty key for a title without words', () => {
        expect(ideaKey(' - ... ')).toBe('');
    });
});

describe('groupIdeas', () => {
    it('folds five copies of one idea into one group, newest first', () => {
        const rows: Row[] = [
            { id: 'a', title: 'You prefer short replies', savedAt: 10 },
            { id: 'b', title: 'you prefer short replies.', savedAt: 50 },
            { id: 'c', title: 'You prefer short replies', savedAt: 30 },
            { id: 'd', title: 'You prefer  short replies', savedAt: 20 },
            { id: 'e', title: 'Short replies, you prefer', savedAt: 40 },
        ];
        const groups = groupIdeas(rows, titleOf, savedAtOf);
        expect(groups).toHaveLength(1);
        expect(ideaCopyIds(groups[0], (row) => row.id)).toEqual(['b', 'e', 'c', 'd', 'a']);
        expect(ideaHead(groups[0])?.id).toBe('b');
    });

    it('keeps different titles as separate ideas in first-seen order', () => {
        const rows: Row[] = [
            { id: 'x', title: 'You write in short sentences', savedAt: 3 },
            { id: 'y', title: 'You open the daily brief first', savedAt: 2 },
            { id: 'z', title: 'You write in short sentences', savedAt: 1 },
        ];
        const groups = groupIdeas(rows, titleOf, savedAtOf);
        expect(groups.map((group) => group.copies.map((row) => row.id))).toEqual([['x', 'z'], ['y']]);
    });

    it('picks the newest copy the view can show', () => {
        const rows: Row[] = [
            { id: 'new', title: 'Deploy after review', savedAt: 9 },
            { id: 'old', title: 'Deploy after review', savedAt: 1 },
        ];
        const [group] = groupIdeas(rows, titleOf, savedAtOf);
        expect(ideaHead(group, (row) => row.id === 'old')?.id).toBe('old');
        expect(ideaHead(group, () => false)).toBeUndefined();
    });

    it('never merges titles without words', () => {
        const rows: Row[] = [
            { id: 'p', title: '...', savedAt: 1 },
            { id: 'q', title: '...', savedAt: 2 },
        ];
        expect(groupIdeas(rows, titleOf, savedAtOf)).toHaveLength(2);
    });
});

describe('uniqueBy', () => {
    it('keeps the first item for each key', () => {
        expect(uniqueBy([{ k: 1, v: 'a' }, { k: 1, v: 'b' }, { k: 2, v: 'c' }], (item) => String(item.k)).map((item) => item.v)).toEqual(['a', 'c']);
    });
});

describe('copies strings', () => {
    it('exist in English and zh-CN, with the count placeholder', async () => {
        const memoryEn = (await import('@/i18n/locales/en/memory.json')).default as Record<string, any>;
        const memoryZh = (await import('@/i18n/locales/zh-CN/memory.json')).default as Record<string, any>;
        const knowledgeEn = (await import('@/i18n/locales/en/knowledge.json')).default as Record<string, any>;
        const knowledgeZh = (await import('@/i18n/locales/zh-CN/knowledge.json')).default as Record<string, any>;
        expect(memoryEn.aboutYou.review.copies_other).toBe('Seen {{count}} times');
        expect(memoryZh.aboutYou.review.copies_other).toContain('{{count}}');
        expect(memoryZh.aboutYou.review.copies_other).toBe('出现过 {{count}} 次');
        expect(knowledgeEn.reuse.copies_other).toBe('Seen {{count}} times');
        expect(knowledgeZh.reuse.copies_other).toBe('出现过 {{count}} 次');
    });
});
