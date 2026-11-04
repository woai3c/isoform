import { act, cleanup, render, renderHook, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { TodayDayStrip } from '@/components/today/TodayDayStrip';
import { dayShapeFixture } from '@/components/today/today-next.fixture';
import { resetPageQueries } from '@/lib/page-queries';
import { useTodayDayShape } from './useTodayDayShape';

const runtime = vi.hoisted(() => ({ listeners: new Map<string, () => unknown>() }));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: (event: string, listener: () => unknown) => {
    runtime.listeners.set(event, listener);
    return () => runtime.listeners.delete(event);
} }));

// Exercise the real shim, cached reader and hook with a fixture native method.
const originalGo = (window as any).go;
const invalidDateCode = 'TODAY_DAY_SHAPE_DATE_INVALID';
beforeEach(() => { resetPageQueries(); runtime.listeners.clear(); });
afterEach(async () => {
    vi.useRealTimers();
    cleanup();
    resetPageQueries();
    (window as any).go = originalGo;
    await i18n.changeLanguage('en');
});

it.each(['en', 'zh-CN'])('reports the stable invalid-date rejection on initial read in %s', async (language) => {
    await i18n.changeLanguage(language);
    // The strip reads the brief's own day while that is today (today-review v1.1.20 R44).
    vi.useFakeTimers({ toFake: ['Date'] });
    vi.setSystemTime(new Date(2026, 9, 22, 9));
    const binding = vi.fn().mockRejectedValue(invalidDateCode);
    (window as any).go = { main: { App: { GetTodayDayShape: binding } } };
    const hook = renderHook(() => useTodayDayShape(true, '2026-10-22'));
    function Strip() {
        const { shape, dateISO } = useTodayDayShape(true, '2026-10-22');
        return <TodayDayStrip shape={shape} requestedDate={dateISO} />;
    }
    const strip = render(<Strip />);
    await waitFor(() => expect(hook.result.current.failed).toBe(true));
    expect(binding).toHaveBeenCalledWith('2026-10-22');
    expect(hook.result.current.shape).toBeNull();
    expect(hook.result.current.lastUpdatedAt).toBeNull();
    // Today review v1.1.6 leaves an unknown strip absent.
    expect(strip.queryByTestId('today-day-strip')).not.toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.queryByText(invalidDateCode)).not.toBeInTheDocument();
});

it.each(['en', 'zh-CN'])('keeps a visible strip when its date read fails on reread in %s', async (language) => {
    await i18n.changeLanguage(language);
    const binding = vi.fn().mockResolvedValueOnce(dayShapeFixture).mockRejectedValue(invalidDateCode);
    (window as any).go = { main: { App: { GetTodayDayShape: binding } } };
    const hook = renderHook(() => useTodayDayShape(true, '2026-10-23'));
    function Strip() {
        const { shape, dateISO } = useTodayDayShape(true, '2026-10-23');
        return <TodayDayStrip shape={shape} requestedDate={dateISO} />;
    }
    const strip = render(<Strip />);
    await screen.findByTestId('today-day-strip');
    await act(async () => { await runtime.listeners.get('today-dayshape:updated')!(); });
    await waitFor(() => expect(binding).toHaveBeenCalledTimes(2));
    expect(hook.result.current.shape).toEqual(dayShapeFixture);
    expect(strip.getByTestId('today-day-strip')).toBeInTheDocument();
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.queryByText(invalidDateCode)).not.toBeInTheDocument();
});
