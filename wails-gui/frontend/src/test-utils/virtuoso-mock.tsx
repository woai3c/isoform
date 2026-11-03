import { forwardRef, useImperativeHandle, type ReactNode } from 'react';

// Test stand-in for react-virtuoso: the real component renders nothing under
// jsdom (it measures the viewport with APIs jsdom does not implement), so
// tests that exercise the windowed chat stream substitute this mock.
//   - full render (default): every row is in the DOM, like the plain list —
//     guard tests assert content, scroll wiring and render counts against it.
//   - windowed (windowRows: N): only the last N rows render — the render-speed
//     fixture measures the screenful the WebView actually mounts.
// The mock records the props and the scrollToIndex calls StreamBody makes so
// tests can assert the anchoring contract (stable row keys, stable indices).

export interface VirtuosoMockRecord {
    scrollToIndexCalls: Array<{ index: number; align?: string; behavior?: string }>;
    lastProps: {
        firstItemIndex?: number;
        initialTopMostItemIndex?: number;
        rowCount: number;
    } | null;
}

export const virtuosoMockRecord: VirtuosoMockRecord = {
    scrollToIndexCalls: [],
    lastProps: null,
};

export function resetVirtuosoMockRecord(): void {
    virtuosoMockRecord.scrollToIndexCalls = [];
    virtuosoMockRecord.lastProps = null;
}

interface MockVirtuosoProps {
    data: unknown[];
    context?: unknown;
    firstItemIndex?: number;
    initialTopMostItemIndex?: number;
    followOutput?: unknown;
    computeItemKey?: (index: number, row: never) => React.Key;
    increaseViewportBy?: unknown;
    defaultItemHeight?: number;
    customScrollParent?: HTMLElement | null;
    components?: {
        Header?: (props: { context?: unknown }) => ReactNode;
        Footer?: (props: { context?: unknown }) => ReactNode;
    };
    itemContent: (index: number, row: never, context?: unknown) => ReactNode;
}

export function virtuosoMockModule(windowRows?: number) {
    const MockVirtuoso = forwardRef(function MockVirtuoso(
        { data, context, firstItemIndex = 0, initialTopMostItemIndex, components, itemContent, computeItemKey }: MockVirtuosoProps,
        ref: React.Ref<{ scrollToIndex: (arg: { index: number; align?: string; behavior?: string }) => void }>,
    ) {
        virtuosoMockRecord.lastProps = { firstItemIndex, initialTopMostItemIndex, rowCount: data.length };
        useImperativeHandle(ref, () => ({
            scrollToIndex: (arg: { index: number; align?: string; behavior?: string }) => {
                virtuosoMockRecord.scrollToIndexCalls.push(arg);
            },
        }));
        const Header = components?.Header;
        const Footer = components?.Footer;
        const start = windowRows === undefined ? 0 : Math.max(0, data.length - windowRows);
        const rendered = data.slice(start);
        return (
            <div data-testid="virtuoso-mock" data-window-rows={windowRows ?? 'all'}>
                {Header ? <Header context={context} /> : null}
                {rendered.map((row, i) => {
                    const index = firstItemIndex + start + i;
                    const key = computeItemKey ? computeItemKey(index, row as never) : index;
                    return <div key={key} data-index={index}>{itemContent(index, row as never, context)}</div>;
                })}
                {Footer ? <Footer context={context} /> : null}
            </div>
        );
    });
    return { Virtuoso: MockVirtuoso };
}
