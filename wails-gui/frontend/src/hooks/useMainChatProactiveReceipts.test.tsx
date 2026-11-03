import { act, cleanup, render, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useMainChatProactiveReceipts } from './useMainChatProactiveReceipts';

const mocks = vi.hoisted(() => ({ record: vi.fn(), observe: vi.fn(), disconnect: vi.fn() }));
vi.mock('@/lib/wails-shim-app', () => ({ RecordMainChatProactiveReceipt: mocks.record }));

function Harness({ momentId = 'moment-1' }: { momentId?: string | null }) {
    const ref = useMainChatProactiveReceipts(momentId);
    return <section ref={ref} />;
}

describe('main chat proactive receipts', () => {
    let intersect: (entries: Partial<IntersectionObserverEntry>[]) => void;
    let focused = true;

    beforeEach(() => {
        vi.clearAllMocks();
        focused = true;
        Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
        vi.spyOn(document, 'hasFocus').mockImplementation(() => focused);
        mocks.record.mockResolvedValue(undefined);
        vi.stubGlobal('IntersectionObserver', class {
            constructor(callback: typeof intersect) { intersect = callback; }
            observe = mocks.observe;
            disconnect = mocks.disconnect;
        });
    });
    afterEach(() => {
        cleanup();
        vi.restoreAllMocks();
        vi.unstubAllGlobals();
    });

    it('records delivery at mount and opened only in the focused visible viewport', async () => {
        render(<Harness />);
        await waitFor(() => expect(mocks.record.mock.calls).toEqual([['moment-1', 'delivered']]));
        focused = false;
        await act(async () => intersect([{ isIntersecting: true }]));
        expect(mocks.record).toHaveBeenCalledTimes(1);
        Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
        focused = true;
        await act(async () => window.dispatchEvent(new Event('focus')));
        expect(mocks.record).toHaveBeenCalledTimes(1);
        Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'visible' });
        await act(async () => document.dispatchEvent(new Event('visibilitychange')));
        expect(mocks.record.mock.calls).toEqual([['moment-1', 'delivered'], ['moment-1', 'opened']]);
        await act(async () => {
            intersect([{ isIntersecting: false }]);
            intersect([{ isIntersecting: true }]);
            window.dispatchEvent(new Event('focus'));
        });
        expect(mocks.record).toHaveBeenCalledTimes(2);
    });

    it.each(['dialog', 'alertdialog'])('does not open a card behind an active %s and records it after the overlay closes', async (role) => {
        function OverlayHarness({ open }: { open: boolean }) {
            return <>
                <Harness />
                {open && <div role={role} data-state="open" />}
            </>;
        }
        const { rerender } = render(<OverlayHarness open />);
        await act(async () => intersect([{ isIntersecting: true }]));
        expect(mocks.record.mock.calls).toEqual([['moment-1', 'delivered']]);
        await act(async () => window.dispatchEvent(new Event('focus')));
        expect(mocks.record).toHaveBeenCalledTimes(1);
        rerender(<OverlayHarness open={false} />);
        await waitFor(() => expect(mocks.record.mock.calls).toEqual([['moment-1', 'delivered'], ['moment-1', 'opened']]));
    });

    it('waits while Radix marks the background hidden from assistive technology', async () => {
        const { rerender } = render(<div aria-hidden="true"><Harness /></div>);
        await act(async () => intersect([{ isIntersecting: true }]));
        expect(mocks.record.mock.calls).toEqual([['moment-1', 'delivered']]);
        rerender(<div><Harness /></div>);
        await waitFor(() => expect(mocks.record.mock.calls).toEqual([['moment-1', 'delivered'], ['moment-1', 'opened']]));
    });

    it('waits for delivery before opened and retries a failed delivery on focus', async () => {
        mocks.record.mockRejectedValueOnce(new Error('store unavailable'));
        render(<Harness />);
        await act(async () => {});
        expect(mocks.record.mock.calls).toEqual([['moment-1', 'delivered']]);
        await act(async () => intersect([{ isIntersecting: true }]));
        expect(mocks.record.mock.calls).toEqual([
            ['moment-1', 'delivered'], ['moment-1', 'delivered'], ['moment-1', 'opened'],
        ]);
    });

    it('retries an opened receipt without recording delivery again', async () => {
        mocks.record.mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error('store unavailable'));
        render(<Harness />);
        await act(async () => intersect([{ isIntersecting: true }]));
        await act(async () => window.dispatchEvent(new Event('focus')));
        expect(mocks.record.mock.calls).toEqual([
            ['moment-1', 'delivered'], ['moment-1', 'opened'], ['moment-1', 'opened'],
        ]);
    });

    it('never records a synthetic digest wrapper and disconnects on unmount', async () => {
        const { rerender, unmount } = render(<Harness momentId={null} />);
        expect(mocks.record).not.toHaveBeenCalled();
        expect(mocks.observe).not.toHaveBeenCalled();
        rerender(<Harness />);
        await act(async () => {});
        unmount();
        expect(mocks.disconnect).toHaveBeenCalledTimes(1);
        await act(async () => {
            intersect([{ isIntersecting: true }]);
            window.dispatchEvent(new Event('focus'));
        });
        expect(mocks.record.mock.calls).toEqual([['moment-1', 'delivered']]);
    });
});
