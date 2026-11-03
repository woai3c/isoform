import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { PersonDetailPanel, type PersonActions } from './person-detail';
import { pageQueryCache } from '@/lib/query-cache';
import { resetPageQueries } from '@/lib/page-queries';

// The person detail beside Who you know renders from the cache on a warm open
// (retained selection, repeated deep link); the skeleton is for a cold read.
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

beforeEach(async () => {
    resetPageQueries();
    fixture.bindings.clear();
    await i18n.changeLanguage('en');
});
afterEach(() => {
    cleanup();
    resetPageQueries();
});

describe('PersonDetailPanel with the page query cache', () => {
    it('paints the cached detail at once, without the skeleton, and skips the wire while fresh', () => {
        pageQueryCache.set('GetPersonDetail', ['person'], DETAIL);
        fixture.bindings.set('GetPersonDetail', vi.fn().mockResolvedValue(DETAIL));
        render(<PersonDetailPanel personId="person" revision={0} actions={actions} />);
        expect(screen.getByText('Fixture person')).toBeInTheDocument();
        expect(document.querySelector('[aria-busy="true"]')).toBeNull();
        expect(fixture.bindings.get('GetPersonDetail')).not.toHaveBeenCalled();
    });

    it('shows the skeleton on a true first load, then the detail', async () => {
        fixture.bindings.set('GetPersonDetail', vi.fn().mockResolvedValue(DETAIL));
        render(<PersonDetailPanel personId="person" revision={0} actions={actions} />);
        expect(document.querySelector('[aria-busy="true"]')).not.toBeNull();
        expect(await screen.findByText('Fixture person')).toBeInTheDocument();
    });

    it('a revision bump re-reads even while the cache is fresh', async () => {
        pageQueryCache.set('GetPersonDetail', ['person'], DETAIL);
        const read = vi.fn().mockResolvedValue({ ...DETAIL, name: 'Renamed person' });
        fixture.bindings.set('GetPersonDetail', read);
        const page = render(<PersonDetailPanel personId="person" revision={0} actions={actions} />);
        expect(screen.getByText('Fixture person')).toBeInTheDocument();
        page.rerender(<PersonDetailPanel personId="person" revision={1} actions={actions} />);
        expect(await screen.findByText('Renamed person')).toBeInTheDocument();
        expect(read).toHaveBeenCalled();
    });
});
