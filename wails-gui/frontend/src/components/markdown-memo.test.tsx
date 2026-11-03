import { render } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { MarkdownContent } from './markdown-content';

// The markdown memo is keyed on the rendered content: a parent re-render
// with the same string must not re-run the markdown pipeline, and a changed
// string must re-run exactly once.
const probe = vi.hoisted(() => ({ renders: [] as string[] }));
vi.mock('streamdown', async (importOriginal) => {
    const actual = await importOriginal<typeof import('streamdown')>();
    return {
        ...actual,
        Streamdown: (props: { children: string }) => {
            probe.renders.push(props.children);
            return <div data-testid="streamdown-probe">{props.children}</div>;
        },
    };
});
vi.mock('@/lib/wails-shim-app', () => new Proxy({}, {
    has: () => true,
    get: (_target, name: string) => {
        if (name === 'then') return undefined;
        return (..._args: unknown[]) => Promise.resolve([]);
    },
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: () => () => {}, EventsEmit: vi.fn(), BrowserOpenURL: vi.fn(), ClipboardSetText: vi.fn() }));

it('skips re-rendering for identical content and re-renders on change', () => {
    const view = render(<MarkdownContent content="Hello **world**" />);
    expect(probe.renders).toEqual(['Hello **world**']);
    view.rerender(<MarkdownContent content="Hello **world**" />);
    // Identical content: the memo holds, the pipeline never re-runs.
    expect(probe.renders).toEqual(['Hello **world**']);
    // A content swap re-runs the pipeline, landing on the new text exactly once
    // (the pacer may show one stale frame while it adopts the swap).
    view.rerender(<MarkdownContent content="Something **else** entirely" />);
    const swaps = probe.renders.filter((content) => content === 'Something **else** entirely');
    expect(swaps).toHaveLength(1);
});
