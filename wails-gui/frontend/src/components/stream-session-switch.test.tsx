import { act, render } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { MainChatStreamBody } from './main-chat/main-chat-stream-body';
import { useMainChatView } from '@/hooks/useMainChatView';
import { resetRenderTierStores } from '@/lib/privacy-reset';
import type { StreamItem } from '@/types';

// Session-switch guard for the production caller: App.tsx keys
// MainChatStreamBody by the active session's localId, so switching long
// chats mounts the next chat's first screen staged (the on-screen tail plus
// the windowed edge), never the previous chat's hydrated 150 recent rows.
// The windowed mock renders the one old-section row the WebView mounts at
// the bottom, so a staged first screen is five turns here.
vi.mock('react-virtuoso', async () => {
    const { virtuosoMockModule } = await import('@/test-utils/virtuoso-mock');
    return virtuosoMockModule(1);
});

const shim = vi.hoisted(() => ({ payload: {} as Record<string, unknown> }));
vi.mock('@/lib/wails-shim-app', () => new Proxy({}, {
    has: () => true,
    get: (_target, name: string) => {
        if (name === 'then') return undefined;
        return (..._args: unknown[]) => Promise.resolve(shim.payload[name] ?? []);
    },
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: () => () => {}, EventsEmit: vi.fn(), BrowserOpenURL: vi.fn(), ClipboardSetText: vi.fn() }));
vi.mock('sonner', () => ({ toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }) }));

function makeTurns(pairs: number, prefix: string): StreamItem[] {
    const items: StreamItem[] = [];
    for (let i = 0; i < pairs; i++) {
        items.push({ id: `${prefix}-u-${i}`, kind: 'user', content: `Question ${prefix} ${i}`, sortOrder: i * 2 });
        items.push({ id: `${prefix}-a-${i}`, kind: 'assistant', content: `Answer ${prefix} ${i} with **markdown**`, sortOrder: i * 2 + 1 });
    }
    return items;
}

/** Mirrors the production call site (App.tsx): key + sessionLocalId by the
 *  active session's identity. */
function ProductionChat({ items, sessionLocalId }: { items: StreamItem[]; sessionLocalId: string }) {
    const view = useMainChatView({ enabled: true, items });
    return (
        <MainChatStreamBody
            key={sessionLocalId}
            sessionLocalId={sessionLocalId}
            mainChat={view}
            onPrepare={() => {}}
            items={items}
            streaming={false}
            hasProject
            onEditMessage={() => {}}
            hasMore={false}
            onLoadMore={() => {}}
        />
    );
}

async function settle() {
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 60)); });
}

beforeEach(async () => {
    await i18n.changeLanguage('en');
    Element.prototype.scrollIntoView = vi.fn();
    globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
    resetRenderTierStores();
});

describe('switching long chats', () => {
    it('mounts the next chat staged, not the previous chat\'s hydrated rows', async () => {
        const chatA = makeTurns(200, 'aa');
        const chatB = makeTurns(200, 'bb');
        const view = render(<ProductionChat items={chatA} sessionLocalId="session-a" />);
        await settle();
        // First chat hydrated: the mock's one windowed row plus all 150
        // recent rows are mounted.
        expect(document.querySelectorAll('[data-stream-turn]').length).toBe(151);

        // Switch to another long chat: the same commit must show only the
        // staged first screen (the on-screen tail + the windowed edge).
        view.rerender(<ProductionChat items={chatB} sessionLocalId="session-b" />);
        const staged = document.querySelectorAll('[data-stream-turn]').length;
        expect(staged).toBeLessThanOrEqual(6);
        expect(document.body.textContent).toContain('Answer bb 199 with markdown');
        expect(document.body.textContent).not.toContain('Answer bb 100 with markdown');

        // Then it hydrates, and native find reaches the whole recent section.
        await settle();
        expect(document.querySelectorAll('[data-stream-turn]').length).toBe(151);
        expect(document.body.textContent).toContain('Answer bb 100 with markdown');
        expect(document.body.textContent).not.toContain('Answer aa 100 with markdown');
    });

    it('stays mounted (no remount) when the same session streams new items', async () => {
        const chatA = makeTurns(200, 'aa');
        const view = render(<ProductionChat items={chatA} sessionLocalId="session-a" />);
        await settle();
        const anchored = document.querySelector('[data-stream-item-id="aa-a-150"]');
        const last = chatA[chatA.length - 1];
        view.rerender(
            <ProductionChat
                items={[...chatA.slice(0, -1), { ...last, content: `${last.content} more` }]}
                sessionLocalId="session-a"
            />,
        );
        await settle();
        // Same session: no remount, the mounted row node is preserved.
        expect(document.querySelector('[data-stream-item-id="aa-a-150"]')).toBe(anchored);
    });
});
