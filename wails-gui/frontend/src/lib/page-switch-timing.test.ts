import { afterEach, beforeEach, expect, it, vi } from 'vitest';

const report = vi.hoisted(() => vi.fn(async () => undefined));
vi.mock('./wails-shim-app', () => ({ ReportPageFirstPaint: report }));

import { beginPageSwitch, markPageContent, noteBindingSource } from './page-switch-timing';

let frames: FrameRequestCallback[] = [];
beforeEach(() => {
    report.mockClear();
    frames = [];
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => { frames.push(callback); return frames.length; });
});
afterEach(() => vi.restoreAllMocks());
function paint() {
    // Two frames: the commit, then the browser's chance to paint it.
    for (let i = 0; i < 2; i++) {
        const pending = frames;
        frames = [];
        pending.forEach((callback) => callback(performance.now()));
    }
}

// Snappy 1.0, "Done when": every page's first paint is measured on Bo's Mac.
// The line reaches the Go log in every build, once per page switch, with only
// the page, the time and where the painted value came from.
it('reports one first paint per page switch, with its source', () => {
    beginPageSwitch('tasks');
    noteBindingSource('ListHandoffs', 'cache');
    markPageContent('tasks');
    markPageContent('tasks');
    paint();
    expect(report).toHaveBeenCalledTimes(1);
    const [page, ms, source] = report.mock.calls[0] as unknown as [string, number, string];
    expect(page).toBe('tasks');
    expect(ms).toBeGreaterThanOrEqual(0);
    expect(source).toBe('cache');
});

it('ignores a mark for a page that is no longer in front', () => {
    beginPageSwitch('today');
    beginPageSwitch('chat');
    markPageContent('today');
    paint();
    expect(report).not.toHaveBeenCalled();
    markPageContent('chat', 'network');
    paint();
    expect(report).toHaveBeenCalledWith('chat', expect.any(Number), 'network');
});
