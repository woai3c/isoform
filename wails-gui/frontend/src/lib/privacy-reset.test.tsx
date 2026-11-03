import { act, render } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { getStreamEditDraft, setStreamEditDraft } from './stream-edit-drafts';
import { resetRenderTierStores } from './privacy-reset';
import { highlightCacheProbe, MarkdownContent } from '@/components/markdown-content';

// Guards for the account-status baseline behind the privacy reset
// (lib/privacy-reset.ts): while the signed-in identity set is unknown, any
// account event is a possible withdrawal and must fail closed; a late or
// stale status completion must never reinstate trust; a healthy baseline
// keeps ordinary quota refreshes from touching anyone's unsent words.
const fixture = vi.hoisted(() => ({
    listeners: new Map<string, Set<(payload: unknown) => void>>(),
    status: vi.fn(),
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (name: string, handler: (payload: unknown) => void) => {
        const set = fixture.listeners.get(name) ?? new Set();
        set.add(handler);
        fixture.listeners.set(name, set);
        return () => set.delete(handler);
    },
    EventsEmit: vi.fn(),
    BrowserOpenURL: vi.fn(),
    ClipboardSetText: vi.fn(),
}));
vi.mock('@/lib/wails-shim-app', () => ({ GetModelAccountsStatus: fixture.status }));

const ACCOUNT = { provider: 'claude', kind: 'subscription', name: 'Bo', on: true, windows: [{ used: 10 }] };
const FENCED = 'Words.\n\n```ts\nexport const n: number = 1;\n```';

function fire(payload: unknown) {
    act(() => {
        fixture.listeners.get('model-accounts:updated')?.forEach((handler) => handler(payload));
    });
}

async function populateStores() {
    setStreamEditDraft('private-message', 'private words');
    render(<MarkdownContent content={FENCED + FENCED} />);
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 5)); });
    expect(getStreamEditDraft('private-message')).toBe('private words');
}

function expectStoresCleared() {
    expect(getStreamEditDraft('private-message')).toBeNull();
    expect(highlightCacheProbe.marked()).toBe(0);
    expect(highlightCacheProbe.queued()).toBe(0);
}

beforeEach(async () => {
    await i18n.changeLanguage('en');
    fixture.status.mockReset();
});

describe('account baseline behind the privacy reset', () => {
    it('fails closed on the first withdrawal while the baseline read is held', async () => {
        fixture.status.mockReturnValue(new Promise(() => {}));
        resetRenderTierStores();
        await populateStores();
        fire({ accounts: [] });
        expectStoresCleared();
    });

    it('fails closed on the first withdrawal after the baseline read failed', async () => {
        fixture.status.mockRejectedValue(new Error('status unavailable'));
        resetRenderTierStores();
        await act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)); });
        await populateStores();
        fire({ accounts: [] });
        expectStoresCleared();
    });

    it('discards a stale baseline completion: it never reinstates trust or retains stores', async () => {
        let resolveStatus: (value: { accounts: unknown[] }) => void = () => {};
        fixture.status.mockReturnValue(new Promise((resolve) => { resolveStatus = resolve; }));
        resetRenderTierStores();
        await populateStores();
        // The first event arrives while the baseline is unknown: fail closed.
        fire({ accounts: [] });
        expectStoresCleared();
        // The old status finally resolves with a signed-in account — stale.
        await act(async () => { resolveStatus({ accounts: [ACCOUNT] }); });
        // It must not have reinstated the account as trusted: an event that
        // removes it again is still a withdrawal.
        setStreamEditDraft('private-message', 'private words');
        fire({ accounts: [ACCOUNT] });
        expect(getStreamEditDraft('private-message')).toBe('private words');
        fire({ accounts: [] });
        expect(getStreamEditDraft('private-message')).toBeNull();
    });

    it('healthy initialization: ordinary quota refreshes preserve drafts, withdrawal clears them', async () => {
        fixture.status.mockResolvedValue({ accounts: [ACCOUNT] });
        resetRenderTierStores();
        await act(async () => { await new Promise((resolve) => setTimeout(resolve, 30)); });
        await populateStores();
        // Same signed-in account, only the quota numbers moved.
        fire({ accounts: [{ ...ACCOUNT, windows: [{ used: 90 }] }] });
        expect(getStreamEditDraft('private-message')).toBe('private words');
        fire({ accounts: [] });
        expectStoresCleared();
    });

    it('resets on an unreadable payload (privacy bias)', async () => {
        fixture.status.mockResolvedValue({ accounts: [ACCOUNT] });
        resetRenderTierStores();
        await act(async () => { await new Promise((resolve) => setTimeout(resolve, 30)); });
        await populateStores();
        fire(null);
        expectStoresCleared();
    });
});
