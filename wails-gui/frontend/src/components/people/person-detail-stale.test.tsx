import { act, cleanup, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { PersonDetailPanel, type PersonActions } from './person-detail';
import { pageQueryCache } from '@/lib/query-cache';
import { resetPageQueries } from '@/lib/page-queries';

// N53: the selected person's detail paints what is saved, a failed read keeps
// the last detail and tries again quietly (N45), and a stale failure never
// overrides a later success.
const fixture = vi.hoisted(() => ({ bindings: new Map<string, ReturnType<typeof vi.fn>>() }));
vi.mock('@/lib/wails-shim-app', () => new Proxy({}, {
    has: () => true,
    get: (_target, name: string) => {
        if (name === 'then') return undefined;
        if (!fixture.bindings.has(name)) fixture.bindings.set(name, vi.fn().mockResolvedValue([]));
        return fixture.bindings.get(name);
    },
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: () => () => {} }));
vi.mock('@/lib/notify', () => ({ notifyToast: vi.fn() }));
vi.mock('sonner', () => ({ toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }) }));

const actions = new Proxy({} as PersonActions, { get: () => vi.fn() });
const DETAIL = {
    id: 'person', name: 'Fixture person', relationship: '', notes: '', researchSummary: '',
    loops: [], together: [], sharedTopics: [], seenIn: [], messageCount: 0,
};
const FAILED = 'Could not load this person.';

type Deferred = { resolve: (v: unknown) => void; reject: (e: unknown) => void; promise: Promise<unknown> };
function deferred(): Deferred {
    let resolve!: (v: unknown) => void;
    let reject!: (e: unknown) => void;
    const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
    return { resolve, reject, promise };
}

beforeEach(async () => {
    resetPageQueries();
    fixture.bindings.clear();
    await i18n.changeLanguage('en');
});
afterEach(() => {
    cleanup();
    resetPageQueries();
});

describe('PersonDetailPanel reads (N53)', () => {
    it('a stale failure never overrides a later success', async () => {
        const first = deferred();
        const second = deferred();
        fixture.bindings.set('GetPersonDetail', vi.fn().mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise));
        const page = render(<PersonDetailPanel personId="person" revision={0} actions={actions} />);
        await act(async () => { await Promise.resolve(); });
        page.rerender(<PersonDetailPanel personId="person" revision={1} actions={actions} />);
        await act(async () => { second.resolve(DETAIL); });
        expect(await screen.findByText('Fixture person')).toBeInTheDocument();
        await act(async () => { first.reject(new Error('revision changed')); });
        await act(async () => { await new Promise((r) => setTimeout(r, 200)); });
        expect(screen.queryByText(FAILED)).not.toBeInTheDocument();
        expect(screen.getByText('Fixture person')).toBeInTheDocument();
    });

    it('a failure that a later success overtook says nothing', async () => {
        const pending = deferred();
        fixture.bindings.set('GetPersonDetail', vi.fn().mockReturnValueOnce(pending.promise));
        render(<PersonDetailPanel personId="person" revision={0} actions={actions} />);
        await act(async () => { await Promise.resolve(); });
        act(() => { pageQueryCache.set('GetPersonDetail', ['person'], DETAIL); });
        expect(await screen.findByText('Fixture person')).toBeInTheDocument();
        await act(async () => { pending.reject(new Error('revision changed')); });
        await act(async () => { await new Promise((r) => setTimeout(r, 50)); });
        expect(screen.queryByText(FAILED)).not.toBeInTheDocument();
        expect(screen.getByText('Fixture person')).toBeInTheDocument();
    });

    it('a success that lands in the cache paints the detail while the quiet tries run', async () => {
        fixture.bindings.set('GetPersonDetail', vi.fn().mockRejectedValue(new Error('revision changed')));
        render(<PersonDetailPanel personId="person" revision={0} actions={actions} />);
        await act(async () => { await Promise.resolve(); });
        // Another read of the same person lands a success.
        act(() => { pageQueryCache.set('GetPersonDetail', ['person'], DETAIL); });
        expect(await screen.findByText('Fixture person')).toBeInTheDocument();
        expect(screen.queryByText(FAILED)).not.toBeInTheDocument();
    });

    it('clicking the selected person reloads it', async () => {
        const read = vi.fn().mockResolvedValue(DETAIL);
        fixture.bindings.set('GetPersonDetail', read);
        const page = render(<PersonDetailPanel personId="person" revision={0} actions={actions} />);
        expect(await screen.findByText('Fixture person')).toBeInTheDocument();
        expect(read).toHaveBeenCalledTimes(1);
        page.rerender(<PersonDetailPanel personId="person" revision={0} reloadToken={1} actions={actions} />);
        await waitFor(() => expect(read).toHaveBeenCalledTimes(2));
    });

    it('a failed re-read keeps the last detail and tries again quietly', async () => {
        const read = vi.fn()
            .mockResolvedValueOnce(DETAIL)
            .mockRejectedValueOnce(new Error('revision changed'))
            .mockRejectedValueOnce(new Error('revision changed'))
            .mockResolvedValue({ ...DETAIL, name: 'Renamed person' });
        fixture.bindings.set('GetPersonDetail', read);
        const page = render(<PersonDetailPanel personId="person" revision={0} actions={actions} />);
        expect(await screen.findByText('Fixture person')).toBeInTheDocument();
        page.rerender(<PersonDetailPanel personId="person" revision={1} actions={actions} />);
        expect(await screen.findByText('Renamed person', {}, { timeout: 3000 })).toBeInTheDocument();
        expect(screen.queryByText(FAILED)).not.toBeInTheDocument();
    });
});
