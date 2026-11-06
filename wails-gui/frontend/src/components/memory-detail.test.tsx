import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { main } from '../../wailsjs/go/models';
import envelopeFixtures from '../../../testdata/memory-point-envelope.json';
import { MemoryDetail } from './memory-detail';
import { pageQueryCache } from '@/lib/query-cache';
import { resetPageQueries } from '@/lib/page-queries';

const mocks = vi.hoisted(() => ({ save: vi.fn() }));
vi.mock('@/lib/wails-shim-app', () => ({
    IdeaMeaningGroups: () => Promise.resolve(null),
    SaveMemoryEntry: mocks.save,
    GetMemoryEvidenceReadable: async () => [],
    GetEntryUses: async () => ({ total: 0, lastAt: 0, surfaces: [] }),
    ListMemoryVersions: async () => [],
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: () => () => {} }));
vi.mock('@/lib/notify', () => ({ notifyToast: vi.fn() }));

const original = '# Writing style\n- Keep replies short.\n- Use examples.\n';
const entry = {
    id: 'writing-style',
    title: 'Writing style',
    content: original,
    apps: [],
    updatedAt: 1000,
} as unknown as main.MemoryEntry;

function detail(content = original) {
    return <MemoryDetail entry={{ ...entry, content }} editRequest={0} onSaved={vi.fn()} onForget={vi.fn()} onReject={vi.fn()} />;
}

describe('memory point drafts and provenance', () => {
    beforeEach(async () => {
        vi.resetAllMocks();
        resetPageQueries();
        await i18n.changeLanguage('en');
        mocks.save.mockImplementation(async (id: string, content: string) => ({ ...entry, id, content }));
    });

    it.each([
        ['insertion before the target', original.replace('- Keep', '- You work on a phone.\n- Keep')],
        ['removal before the target', original.replace('- Keep replies short.\n', '')],
        ['removal of the target', original.replace('- Use examples.\n', '')],
    ])('keeps a draft visible and blocks stale Save after %s', async (_, refreshed) => {
        const { rerender } = render(detail());
        fireEvent.click(screen.getByRole('button', { name: 'Use examples.' }));
        fireEvent.change(screen.getByRole('textbox', { name: 'Memory point' }), { target: { value: 'Offer context.' } });
        rerender(detail(refreshed));
        expect(screen.getByRole('textbox', { name: 'Memory point' })).toHaveValue('Offer context.');
        expect(screen.getByRole('alert')).toHaveTextContent('This memory changed');
        const save = screen.getByRole('button', { name: 'Save' });
        expect(save).toBeDisabled();
        fireEvent.click(save);
        expect(mocks.save).not.toHaveBeenCalled();
        fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
        expect(screen.queryByRole('textbox', { name: 'Memory point' })).not.toBeInTheDocument();
        // Choosing a current point explicitly starts a new draft against the refresh.
        const target = refreshed.includes('Use examples.') ? 'Use examples.' : 'Keep replies short.';
        fireEvent.click(screen.getByRole('button', { name: target }));
        fireEvent.change(screen.getByRole('textbox', { name: 'Memory point' }), { target: { value: 'A current correction.' } });
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        await waitFor(() => expect(mocks.save).toHaveBeenCalledWith(entry.id, refreshed.replace(target, 'A current correction.')));
    });

    it('keeps the intended draft editable across an unchanged-content refresh', async () => {
        const { rerender } = render(detail());
        fireEvent.click(screen.getByRole('button', { name: 'Use examples.' }));
        fireEvent.change(screen.getByRole('textbox', { name: 'Memory point' }), { target: { value: 'Offer context.' } });
        rerender(detail());
        expect(screen.getByRole('textbox', { name: 'Memory point' })).toHaveValue('Offer context.');
        expect(screen.queryByRole('alert')).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        await waitFor(() => expect(mocks.save).toHaveBeenCalledWith(entry.id, original.replace('Use examples.', 'Offer context.')));
    });

    it('retains an add draft after a refresh and prevents appending against unseen changes', async () => {
        const { rerender } = render(detail());
        fireEvent.click(screen.getByRole('button', { name: 'Add a point' }));
        fireEvent.change(screen.getByRole('textbox', { name: 'Memory point' }), { target: { value: 'A new point.' } });
        await act(async () => rerender(detail(original + '- A refreshed fact.\n')));
        expect(screen.getByRole('textbox', { name: 'Memory point' })).toHaveValue('A new point.');
        expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
        expect(screen.getByRole('button', { name: 'Forget point: A refreshed fact.' })).toBeDisabled();
        expect(mocks.save).not.toHaveBeenCalled();
    });

    it.each(envelopeFixtures)('preserves the complete $name envelope during inline edit and removal', async ({ content, edited, removed }) => {
        const { unmount } = render(detail(content));
        expect(screen.queryByRole('button', { name: /point-source/ })).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Use examples.' }));
        fireEvent.change(screen.getByRole('textbox', { name: 'Memory point' }), { target: { value: 'Offer context.' } });
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        await waitFor(() => expect(mocks.save).toHaveBeenLastCalledWith(entry.id, edited));
        unmount();
        render(detail(content));
        fireEvent.click(screen.getByRole('button', { name: 'Forget point: Use examples.' }));
        await waitFor(() => expect(mocks.save).toHaveBeenLastCalledWith(entry.id, removed));
    });
});

describe('cached detail reads', () => {
    beforeEach(async () => {
        vi.resetAllMocks();
        resetPageQueries();
        await i18n.changeLanguage('en');
        mocks.save.mockImplementation(async (id: string, content: string) => ({ ...entry, id, content }));
    });

    it('paints cached evidence and uses at once, without the skeleton', () => {
        pageQueryCache.set('GetMemoryEvidenceReadable', [entry.id], [
            { at: 1761264000000, durationMs: 60000, appName: 'FixtureApp', text: 'A public fixture note.', activitySessionId: 'a1' },
        ]);
        pageQueryCache.set('GetEntryUses', ['memory', entry.id], { total: 3, lastAt: 1761264000000, surfaces: [] });
        render(detail());
        // Synchronous first render: the cached evidence row is there and no
        // aria-busy skeleton is showing.
        expect(screen.getByText('From your FixtureApp: A public fixture note.')).toBeInTheDocument();
        expect(document.querySelector('[aria-busy="true"]')).toBeNull();
    });

    it('shows the skeleton on a true first load', () => {
        render(detail());
        expect(document.querySelector('[aria-busy="true"]')).not.toBeNull();
    });
});
