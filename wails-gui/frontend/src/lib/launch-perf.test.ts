import { beforeEach, describe, expect, it, vi } from 'vitest';

const report = vi.hoisted(() => vi.fn(async (_stage: string) => {}));
vi.mock('./wails-shim-app', () => ({ ReportLaunchStage: report }));

beforeEach(() => { vi.resetModules(); report.mockClear(); });

describe('launch paint milestones', () => {
    it('releases optional startup only after all first-screen reads painted', async () => {
        const frames: FrameRequestCallback[] = [];
        vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => frames.push(callback));
        const { markLaunchStage, markLaunchPaint } = await import('./launch-perf');
        markLaunchStage('first_data');
        markLaunchPaint('chat_rendered');
        markLaunchPaint('counts_rendered');
        markLaunchPaint('overview_rendered');
        expect(report.mock.calls.flat()).not.toContain('first_screen_rendered');
        while (frames.length) frames.shift()!(0);
        expect(report.mock.calls.filter(([name]) => name === 'first_screen_rendered')).toHaveLength(1);
        markLaunchStage('first_data');
        expect(report.mock.calls.filter(([name]) => name === 'first_data')).toHaveLength(1);
        vi.unstubAllGlobals();
    });

    it('diagnostic bridge errors never reject a successful page read', async () => {
        report.mockRejectedValueOnce(new Error('bridge unavailable'));
        const { markLaunchStage } = await import('./launch-perf');
        expect(() => markLaunchStage('first_data')).not.toThrow();
        await Promise.resolve();
    });
});
