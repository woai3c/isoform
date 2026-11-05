import { act, render, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { NoticedSuggestion } from '@/lib/wails-shim-app';
import { ActivityNoticed } from './activity-noticed';
import { resetPageQueries } from '@/lib/page-queries';

const fixture = vi.hoisted(() => ({
    bindings: new Map<string, ReturnType<typeof vi.fn>>(),
    listeners: new Map<string, Set<(payload: unknown) => void>>(),
}));

vi.mock('@/lib/wails-shim-app', () => new Proxy({}, {
    has: () => true,
    get: (_target, name: string) => {
        if (name === 'then') return undefined;
        if (!fixture.bindings.has(name)) fixture.bindings.set(name, vi.fn().mockResolvedValue([]));
        return fixture.bindings.get(name);
    },
}));

vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (name: string, handler: (payload: unknown) => void) => {
        const listeners = fixture.listeners.get(name) ?? new Set();
        listeners.add(handler);
        fixture.listeners.set(name, listeners);
        return () => listeners.delete(handler);
    },
}));

const DAY = '2026-10-19';
const AT = new Date(2026, 9, 19, 10, 5).getTime();
const followUp: NoticedSuggestion = {
    id: 's-follow',
    kind: 'follow_up',
    momentId: 'act-lamp',
    momentTitle: 'Comparing lamps',
    momentStartMs: AT,
    text: 'Follow up with Priya',
    why: 'They asked.',
};

function fire(name: string) {
    for (const handler of fixture.listeners.get(name) ?? []) handler(undefined);
}

const settle = () => act(async () => { await new Promise((resolve) => setTimeout(resolve, 400)); });

function setHidden(hidden: boolean) {
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => (hidden ? 'hidden' : 'visible') });
    document.dispatchEvent(new Event('visibilitychange'));
}

describe('Isoform noticed launch sequence', () => {
    beforeEach(async () => {
        resetPageQueries();
        fixture.bindings.clear();
        fixture.listeners.clear();
        setHidden(false);
        await i18n.changeLanguage('en');
    });

    it('reads once at launch, once per coalesced burst, and never while hidden', async () => {
        const read = vi.fn().mockResolvedValue([followUp]);
        fixture.bindings.set('GetDayNoticed', read);
        render(<ActivityNoticed dateStr={DAY} onOpenMoment={() => undefined} />);
        await waitFor(() => expect(read).toHaveBeenCalled());
        await settle();
        const afterMount = read.mock.calls.length;
        expect(afterMount).toBe(1);

        // A startup burst: 50 noticed:updated events in a row are one extra read.
        await act(async () => { for (let i = 0; i < 50; i++) fire('noticed:updated'); });
        await settle();
        expect(read.mock.calls.length).toBe(afterMount + 1);

        // The broad launch events no longer read the Noticed strip: they fire
        // constantly at startup and returned zero rows every time.
        await act(async () => {
            for (let i = 0; i < 20; i++) {
                fire('agency:updated');
                fire('activity:activities-updated');
                fire('activity:summary-ready');
            }
        });
        await settle();
        expect(read.mock.calls.length).toBe(afterMount + 1);

        // Hidden: events neither read nor queue a read on the hidden window.
        setHidden(true);
        await act(async () => { for (let i = 0; i < 20; i++) fire('noticed:updated'); });
        await settle();
        expect(read.mock.calls.length).toBe(afterMount + 1);

        // Shown again: exactly one read for everything that changed while hidden.
        setHidden(false);
        await settle();
        expect(read.mock.calls.length).toBe(afterMount + 2);
    });
});
