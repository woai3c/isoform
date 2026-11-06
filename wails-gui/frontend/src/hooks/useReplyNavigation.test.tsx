import { act, render, renderHook } from '@testing-library/react';
import { createRef } from 'react';
import { RetainedPageActiveContext } from '@/lib/retained-page-active';
import { describe, expect, it, vi, afterEach } from 'vitest';
import { useReplyNavigation } from './useReplyNavigation';

// Only the reader's own input may set userScrolledRef. These guards pin the
// rule: programmatic scrolls and layout shifts never mark the reader away,
// and a pane that becomes visible opens on its newest item.

function makeViewport(clientHeight = 400) {
    const root = document.createElement('div');
    const viewport = document.createElement('div');
    viewport.setAttribute('data-radix-scroll-area-viewport', '');
    root.appendChild(viewport);
    let height = clientHeight;
    let scrollHeight = 2000;
    Object.defineProperty(viewport, 'clientHeight', { configurable: true, get: () => height, set: (v) => { height = v; } });
    Object.defineProperty(viewport, 'scrollHeight', { configurable: true, get: () => scrollHeight, set: (v) => { scrollHeight = v; } });
    viewport.scrollTop = 0;
    return { root, viewport };
}

function setup(items: { id: string; kind: string }[] = [{ id: 'a', kind: 'assistant' }]) {
    const { root, viewport } = makeViewport();
    const rootRef = createRef<HTMLDivElement>() as { current: HTMLDivElement | null };
    rootRef.current = root;
    document.body.appendChild(root);
    const hook = renderHook(
        (props: { items: { id: string; kind: string }[] }) =>
            useReplyNavigation(props.items, rootRef as React.RefObject<HTMLDivElement>, {
                viewportSelector: '[data-radix-scroll-area-viewport]',
            }),
        { initialProps: { items } },
    );
    return { hook, viewport, root };
}

function probe(root: HTMLDivElement, items: { id: string; kind: string }[]) {
    const rootRef = { current: root } as React.RefObject<HTMLDivElement>;
    const navRef = { current: null as ReturnType<typeof useReplyNavigation> | null };
    function Inner() {
        navRef.current = useReplyNavigation(items, rootRef, { viewportSelector: '[data-radix-scroll-area-viewport]' });
        return null;
    }
    const ui = (value: boolean) => (
        <RetainedPageActiveContext.Provider value={value}>
            <Inner />
        </RetainedPageActiveContext.Provider>
    );
    return { navRef, ui };
}

describe('useReplyNavigation: reader scroll rule', () => {
    afterEach(() => {
        vi.useRealTimers();
        document.body.innerHTML = '';
    });

    it('a programmatic scroll away from the bottom does not mark the reader away', () => {
        const { hook, viewport } = setup();
        act(() => {
            viewport.scrollTop = 100;
            viewport.dispatchEvent(new Event('scroll'));
        });
        expect(hook.result.current.visible).toBe(false);
        expect(hook.result.current.userScrolledRef.current).toBe(false);
    });

    it('a wheel gesture followed by a scroll away from the bottom marks the reader away', () => {
        const { hook, viewport } = setup();
        act(() => {
            viewport.dispatchEvent(new Event('wheel'));
            viewport.scrollTop = 100;
            viewport.dispatchEvent(new Event('scroll'));
        });
        expect(hook.result.current.userScrolledRef.current).toBe(true);
    });

    it('input older than the window no longer counts as the reader scrolling', () => {
        vi.useFakeTimers();
        const { hook, viewport } = setup();
        act(() => {
            viewport.dispatchEvent(new Event('touchstart'));
        });
        vi.advanceTimersByTime(1000);
        act(() => {
            viewport.scrollTop = 100;
            viewport.dispatchEvent(new Event('scroll'));
        });
        expect(hook.result.current.userScrolledRef.current).toBe(false);
    });
});

describe('useReplyNavigation: when the pane opens on its newest item', () => {
    afterEach(() => {
        document.body.innerHTML = '';
    });

    function mounted() {
        const { root, viewport } = makeViewport();
        document.body.appendChild(root);
        const { navRef, ui } = probe(root, [{ id: 'a', kind: 'assistant' }]);
        const view = render(ui(true));
        const scrollAway = () => act(() => {
            viewport.dispatchEvent(new Event('wheel'));
            viewport.scrollTop = 100;
            viewport.dispatchEvent(new Event('scroll'));
        });
        return { viewport, navRef, view, ui, scrollAway };
    }

    it('a page shown again opens on its newest item, even after the reader scrolled up', () => {
        const { viewport, navRef, view, ui, scrollAway } = mounted();
        scrollAway();
        expect(navRef.current?.userScrolledRef.current).toBe(true);
        act(() => { view.rerender(ui(false)); });
        act(() => { view.rerender(ui(true)); });
        expect(navRef.current?.userScrolledRef.current).toBe(false);
        expect(viewport.scrollTop).toBe(viewport.scrollHeight);
    });

    it('window focus, the screen unlocking and the display waking keep the reader in place', () => {
        const { viewport, navRef, scrollAway } = mounted();
        scrollAway();
        const place = viewport.scrollTop;
        act(() => {
            window.dispatchEvent(new Event('focus'));
            document.dispatchEvent(new Event('visibilitychange'));
            window.dispatchEvent(new Event('resize'));
            // The display sleeping and waking reports the viewport at zero, then its size again.
            Object.defineProperty(viewport, 'clientHeight', { configurable: true, get: () => 0 });
            Object.defineProperty(viewport, 'clientHeight', { configurable: true, get: () => 400 });
        });
        expect(navRef.current?.userScrolledRef.current).toBe(true);
        expect(viewport.scrollTop).toBe(place);
    });

    it('a page that stays on screen does not jump when the viewport resizes', () => {
        const { viewport, navRef, view, ui, scrollAway } = mounted();
        scrollAway();
        const place = viewport.scrollTop;
        act(() => { view.rerender(ui(true)); });
        expect(navRef.current?.userScrolledRef.current).toBe(true);
        expect(viewport.scrollTop).toBe(place);
    });
});
