import { describe, expect, it } from 'vitest';
import {
    beliefInSecondPerson,
    isSystemNoiseApp,
    memoryApps,
    memoryConfirmed,
    memoryKind,
    memoryLabel,
    memoryPoints,
    relativeWords,
} from './memory-content';
import type { main } from '../../wailsjs/go/models';
import i18n from '@/i18n';
import envelopeFixtures from '../../../testdata/memory-point-envelope.json';

const id = 'b4c8e1a9-4f72-4b18-a19d-6f143d66e98b';
describe('memory content projection', () => {
    it('gives a saved subjectless belief a subject', () => {
        expect(beliefInSecondPerson('Accepts partial pull requests when unresolved items are called out')).toBe('You accept partial pull requests when unresolved items are called out');
    });
    it.each(envelopeFixtures)('projects only facts from legacy $name envelopes', ({ content }) => {
        expect(memoryPoints(content).map((point) => point.text)).toEqual([
            'Keep replies short.',
            'Use examples.',
        ]);
    });

    it('keeps sentence edit offsets against both formats and never treats source metadata as a point', () => {
        for (const envelope of [
            `---\nuuid: ${id}\napps: [Safari]\nread_provenance: private\n---\n# Writing style\n`,
            `# Writing style\n- **Dimension**: user_insight\n- **Activity Session**:\n  - **Update 2026-10-18 11:30** (initial)\n    - ${id}\n- **Reassigned to**: Ada (id=${id})\n`,
        ]) {
            const content =
                envelope +
                '\n- Keep replies short. Use examples.\n- 保留事实。不要猜测。\n';
            const points = memoryPoints(content);
            expect(points.map((point) => point.text)).toEqual([
                'Keep replies short.',
                'Use examples.',
                '保留事实。',
                '不要猜测。',
            ]);
            const point = points[1];
            expect(
                content.slice(0, point.start) +
                    'Offer context.' +
                    content.slice(point.end),
            ).toBe(content.replace('Use examples.', 'Offer context.'));
        }
    });
    it('keeps point identities across unrelated insertions and distinguishes repeated facts', () => {
        const content = '- Keep replies short.\n- Use examples.\n- Use examples.\n';
        const points = memoryPoints(content);
        expect(new Set(points.map((point) => point.id)).size).toBe(3);
        expect(memoryPoints('- I review on my phone.\n' + content).slice(1).map((point) => point.id))
            .toEqual(points.map((point) => point.id));
    });
    it('recognizes an explicitly reassigned person and preferences without treating every topic as a person', () => {
        expect(
            memoryKind({
                content: '- **Reassigned to**: Ada\n- Ada writes daily.',
                dimension: 'topic_highlight',
            } as main.MemoryEntry),
        ).toBe('person');
        expect(
            memoryKind({
                content: '- I prefer concise replies.',
                dimension: 'user_insight',
            } as main.MemoryEntry),
        ).toBe('preference');
        expect(
            memoryKind({
                content: '- I work on a phone app.',
                dimension: 'topic_highlight',
            } as main.MemoryEntry),
        ).toBe('learned');
    });
    it('uses an authored heading or first point when a title is an opaque ID or type label', () => {
        const t = i18n.getFixedT('en', 'memory');
        expect(
            memoryLabel(
                {
                    id,
                    title: id,
                    content: '# Writing style\n- Keep replies short.',
                } as main.MemoryEntry,
                t,
            ),
        ).toBe('Writing style');
        expect(
            memoryLabel(
                {
                    id,
                    title: 'Isoform Memory',
                    content: '- Keep replies short.',
                } as main.MemoryEntry,
                t,
            ),
        ).toBe('Keep replies short.');
    });
    it('deduplicates apps without case sensitivity, names at most three, and uses the current product name', () => {
        expect(
            memoryApps([' ChatGPT ', 'chatgpt', 'Safari', 'Yansu', 'Terminal']),
        ).toEqual(['ChatGPT', 'Safari', 'Isoform']);
    });
    it('never names system processes such as loginwindow or the Dock', () => {
        expect(
            memoryApps(['loginwindow', 'Dock', 'Window Server', 'ChatGPT', 'Spotlight', 'Safari']),
        ).toEqual(['ChatGPT', 'Safari']);
        expect(isSystemNoiseApp('Notification  Center')).toBe(true);
        expect(isSystemNoiseApp('Docker')).toBe(false);
    });
    it('says how long ago in words, never a clock time', () => {
        const t = i18n.getFixedT('en', 'memory');
        const now = new Date(2026, 9, 22, 9);
        expect(relativeWords(t, new Date(2026, 9, 22, 1).getTime(), now)).toBe('today');
        expect(relativeWords(t, new Date(2026, 9, 21, 23).getTime(), now)).toBe('yesterday');
        expect(relativeWords(t, new Date(2026, 9, 18).getTime(), now)).toBe('4 days ago');
        expect(relativeWords(t, new Date(2026, 9, 1).getTime(), now)).toBe('3 weeks ago');
    });
    it('reads a confirmation from the frontmatter or a user edit', () => {
        const entry = (content: string, source = 'activity') => ({ content, source } as main.MemoryEntry);
        expect(memoryConfirmed(entry('---\nconfirmed_at: 2026-10-22T09:00:00Z\n---\n- Keep replies short.'))).toBe(true);
        expect(memoryConfirmed(entry('---\nuuid: x\n---\n- Keep replies short.', 'user_edit'))).toBe(true);
        expect(memoryConfirmed(entry('---\nuuid: x\n---\n- confirmed_at: not frontmatter'))).toBe(false);
        expect(memoryConfirmed(entry('- confirmed_at: 2026-10-22T09:00:00Z'))).toBe(false);
    });
});

describe('beliefInSecondPerson (about-you v1.0.1 Y28)', () => {
    it.each([
        ['Verifies release checksums', 'You verify release checksums'],
        ['Turns on Auto-fix', 'You turn on Auto-fix'],
        ['Queues a full check after each merge', 'You queue a full check after each merge'],
        ['Compares recent runs with a baseline', 'You compare recent runs with a baseline'],
        ['Never lets uncertain replies replay automatically', 'You never let uncertain replies replay automatically'],
        ['Always checks the results', 'You always check the results'],
        ['Hands work to other agents and coordinates them through a hub', 'You hand work to other agents and coordinate them through a hub'],
        ['Uses design reviews to find launch blockers before release', 'You use design reviews to find launch blockers before release'],
        ['Wants public repo commits to carry the real fix messages', 'You want public repo commits to carry the real fix messages'],
        ['Fast-merges urgent fixes after focused checks pass', 'You fast-merge urgent fixes after focused checks pass'],
        ['Relies on short standups', 'You rely on short standups'],
        ['Pushes fixes to a small branch first', 'You push fixes to a small branch first'],
        ['Has a standing Friday review', 'You have a standing Friday review'],
        ['“Prefers bun over npm”', 'You prefer bun over npm'],
        ['I prefer bun over npm', 'You prefer bun over npm'],
        ['I am a morning writer', 'You are a morning writer'],
        ['My mornings are for deep work', 'Your mornings are for deep work'],
        ['You keep emails to three sentences', 'You keep emails to three sentences'],
        ['Tools for the Lisbon trip', 'You care about tools for the Lisbon trip'],
        ['News about Maya Okafor', 'You care about news about Maya Okafor'],
        ['习惯在终端里用语音输入', '习惯在终端里用语音输入'],
        // v1.0.2 Y28: every verb agrees with "you".
        ['Keeps 8 to 11 AM free for writing and code review, and declines meetings then.', 'You keep 8 to 11 AM free for writing and code review, and decline meetings then.'],
        ['You keep 8 to 11 AM free, and declines meetings then', 'You keep 8 to 11 AM free, and decline meetings then'],
        ['Drafts a reply and sends it before noon', 'You draft a reply and send it before noon'],
        ['Reviews pull requests and is quick to merge', 'You review pull requests and are quick to merge'],
        ['Keeps notes and plans in one doc', 'You keep notes and plans in one doc'],
        ['Guards mornings, then answers email after lunch', 'You guard mornings, then answer email after lunch'],
    ])('%s', (input, want) => {
        expect(beliefInSecondPerson(input)).toBe(want);
    });

    it('labels a subjectless title in second person', () => {
        const entry = { id: 'm1', title: 'Keeps the backup hub read-only', content: '# Keeps the backup hub read-only\n- Keeps the backup hub read-only.' } as main.MemoryEntry;
        expect(memoryLabel(entry, i18n.getFixedT('en', 'memory'))).toBe('You keep the backup hub read-only');
    });
});

describe('beliefInSecondPerson subjectless beliefs (launch walk Y28)', () => {
    it.each([
        ['Approves disk cleanup only after reviewing a size audit of candidates', 'You approve disk cleanup only after reviewing a size audit of candidates'],
        ['Requires the focused tests to pass before pushing', 'You need the focused tests to pass before pushing'],
        ['Building Isoform desktop app and iPhone companion with Claude and Codex', "You're building Isoform desktop app and iPhone companion with Claude and Codex"],
        ['Meeting notes stay in the hub', 'You care about meeting notes stay in the hub'],
    ])('%s', (input, want) => {
        expect(beliefInSecondPerson(input)).toBe(want);
    });
});

describe('beliefInSecondPerson Title Case entries (launch walk Y28, reopened)', () => {
    it.each([
        ['Preferences for short release notes and quiet hours', 'You prefer short release notes and quiet hours'],
        ['Caution before sending alerts to outside teams', "You're careful before sending alerts to outside teams"],
        ['Maintaining the sample backlog document weekly', "You're maintaining the sample backlog document weekly"],
        ['Atlas product direction and wearable hardware research', 'You care about Atlas product direction and wearable hardware research'],
        ['Yansu release checklist for pilot customers', 'You care about Isoform release checklist for pilot customers'],
    ])('%s', (input, want) => {
        expect(beliefInSecondPerson(input)).toBe(want);
    });

    it('keeps a memory title unconverted apart from the product name', () => {
        expect(beliefInSecondPerson('Writing style', { nounPhrase: false })).toBe('Writing style');
        expect(beliefInSecondPerson('Yansu roadmap', { nounPhrase: false })).toBe('Isoform roadmap');
    });
});

describe('bare instructions and events stay as written (launch walk Y28)', () => {
    it.each(['Use examples.', 'Keep replies short.', 'Prepared a weekly update.'])('%s', (input) => {
        expect(beliefInSecondPerson(input)).toBe(input);
    });
});

describe('Y28 leftovers (v1.0.11 walk)', () => {
    it('gives a belief title its subject, so a confirmed product line reads as a sentence about you', () => {
        expect(beliefInSecondPerson('Isoform product direction and wearable hat hardware research')).toBe(
            'You care about Isoform product direction and wearable hat hardware research',
        );
    });

    it('reads a preference for a product as care, not a claimed preference', () => {
        expect(beliefInSecondPerson('Preferences for Isoform Spoken Briefs and Contextual Chat')).toBe(
            'You care about Isoform Spoken Briefs and Contextual Chat',
        );
        expect(beliefInSecondPerson('Preferences for short release notes')).toBe('You prefer short release notes');
    });

    it('keeps a plain memory name unchanged', () => {
        expect(beliefInSecondPerson('Writing style', { nounPhrase: false })).toBe('Writing style');
    });
});
