import { describe, expect, it } from 'vitest';
import {
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
