import { describe, expect, it } from 'vitest';
import { groupIdeas, ideaCopyIds, ideaHead, ideaWords, uniqueBy } from './idea-copies';

// Invented paraphrase fixtures: the copies of one idea say it in different words.
type Row = { id: string; title: string; savedAt: number };
const titleOf = (row: Row) => row.title;
const savedAtOf = (row: Row) => row.savedAt;
const ids = (group: { copies: Row[] }) => group.copies.map((row) => row.id);

const RELEASE_NOTES = [
    'You want release notes to name the bugs each build fixes',
    'You want the release notes to name each bug the build fixes',
    'Wants release notes that name every bug fixed in the build',
    'You want release notes naming the real bug fixes in each build',
    'Wants the release notes to list each bug a build fixes',
];

describe('ideaWords', () => {
    it('ignores case, punctuation, word order and plural endings', () => {
        expect([...ideaWords('Keeps Fixtures, stubs and mocks apart.')].sort())
            .toEqual([...ideaWords('mocks and stub fixtures: keeps apart')].sort());
    });

    it('keeps negations as their own words', () => {
        expect(ideaWords('Never merges the staging branch').has('!never')).toBe(true);
        expect(ideaWords('Merges the staging branch').has('!never')).toBe(false);
    });
});

describe('groupIdeas', () => {
    it('groups paraphrases of one idea into one card, the newest copy heading it', () => {
        const rows: Row[] = RELEASE_NOTES.map((title, index) => ({ id: `r${index}`, title, savedAt: index }));
        const groups = groupIdeas(rows, titleOf, savedAtOf);
        expect(groups).toHaveLength(1);
        expect(ideaCopyIds(groups[0], (row) => row.id)).toEqual(['r4', 'r3', 'r2', 'r1', 'r0']);
        expect(ideaHead(groups[0])?.id).toBe('r4');
    });

    it('keeps different ideas apart even when they share one generic word', () => {
        const rows: Row[] = [
            { id: 'notes', title: 'You want release notes to name the bugs each build fixes', savedAt: 3 },
            { id: 'dark', title: 'You prefer dark mode in the evening', savedAt: 2 },
            { id: 'deep', title: 'You schedule deep work before lunch', savedAt: 1 },
        ];
        const groups = groupIdeas(rows, titleOf, savedAtOf);
        expect(groups.map(ids)).toEqual([['notes'], ['dark'], ['deep']]);
    });

    it('does not join a negated title to its positive', () => {
        const rows: Row[] = [
            { id: 'pos', title: 'Merges the staging branch into main after checks', savedAt: 2 },
            { id: 'neg', title: 'Never merges the staging branch into main after checks', savedAt: 1 },
        ];
        expect(groupIdeas(rows, titleOf, savedAtOf)).toHaveLength(2);
    });

    it('groups near-identical copies that differ only in case and punctuation', () => {
        const rows: Row[] = [
            { id: 'a', title: 'You want invoices to carry the payment terms.', savedAt: 2 },
            { id: 'b', title: 'you want INVOICES to carry the payment terms', savedAt: 1 },
        ];
        expect(groupIdeas(rows, titleOf, savedAtOf)).toHaveLength(1);
    });

    it('groups identical titles even when they have only two words', () => {
        const rows: Row[] = [
            { id: 'a', title: 'Short replies', savedAt: 2 },
            { id: 'b', title: 'short replies.', savedAt: 1 },
        ];
        expect(groupIdeas(rows, titleOf, savedAtOf)).toHaveLength(1);
    });

    it('keeps a title with no words as its own group', () => {
        const rows: Row[] = [
            { id: 'p', title: '...', savedAt: 1 },
            { id: 'q', title: '...', savedAt: 2 },
        ];
        expect(groupIdeas(rows, titleOf, savedAtOf)).toHaveLength(2);
    });
});

describe('groupIdeas by meaning', () => {
    it('groups titles with one meaning group even when their words differ', () => {
        const rows: Row[] = [
            { id: 'scope', title: 'You give explicit scope and exclusions when approving destructive actions', savedAt: 3 },
            { id: 'audit', title: 'Approves disk cleanup only after reviewing a size audit of candidates', savedAt: 2 },
            { id: 'dark', title: 'You prefer dark mode in the evening', savedAt: 1 },
        ];
        const groups = groupIdeas(rows, titleOf, savedAtOf, [0, 0, 1]);
        expect(groups.map(ids)).toEqual([['scope', 'audit'], ['dark']]);
    });

    it('keeps a negated title apart from its positive within one meaning group', () => {
        const rows: Row[] = [
            { id: 'pos', title: 'Merges the staging branch after checks', savedAt: 2 },
            { id: 'neg', title: 'Never merges the staging branch after checks', savedAt: 1 },
        ];
        expect(groupIdeas(rows, titleOf, savedAtOf, [0, 0])).toHaveLength(2);
    });
});

describe('groupIdeas joins by words or by meaning (launch walk Y33, W40)', () => {
    it('joins titles that share a meaning group even when the meaning split them', () => {
        const rows: Row[] = [
            { id: 'a', title: 'Offer naming only after both reads settle', savedAt: 2 },
            { id: 'b', title: 'Gate the naming prompt on settled reads', savedAt: 1 },
        ];
        expect(groupIdeas(rows, titleOf, savedAtOf, [0, 0])).toHaveLength(1);
    });

    it('joins titles that share words even when the meaning split them', () => {
        const rows: Row[] = [
            { id: 'a', title: 'Ship the release notes on Monday', savedAt: 2 },
            { id: 'b', title: 'Ship the release notes on Friday', savedAt: 1 },
        ];
        expect(groupIdeas(rows, titleOf, savedAtOf, [0, 1])).toHaveLength(1);
    });

    it('keeps unrelated titles apart when neither words nor meaning link them', () => {
        const rows: Row[] = [
            { id: 'a', title: 'Keeps home folder paths out of committed docs', savedAt: 2 },
            { id: 'b', title: 'Waits for the nightly build before deploying', savedAt: 1 },
        ];
        expect(groupIdeas(rows, titleOf, savedAtOf, [0, 1])).toHaveLength(2);
    });
});

describe('framing of You-sentences does not join beliefs (Y28)', () => {
    it('does not join two beliefs only because both read You care about', () => {
        const rows: Row[] = [
            { id: 'a', title: 'You care about Fact 1', savedAt: 2 },
            { id: 'b', title: 'You care about Fact 2', savedAt: 1 },
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
        expect(memoryZh.aboutYou.review.copies_other).toBe('出现过 {{count}} 次');
        expect(knowledgeEn.reuse.copies_other).toBe('Seen {{count}} times');
        expect(knowledgeZh.reuse.copies_other).toBe('出现过 {{count}} 次');
    });
});
