import { afterEach, expect, it, vi } from 'vitest';
import { GetTodayDayShape } from './wails-shim-app';

const originalGo = (window as any).go;
afterEach(() => { (window as any).go = originalGo; });

it('exports the exact day strip binding and forwards the local date', async () => {
    const shape = {
        rangeStart: '2026-10-23T08:00:00-07:00',
        rangeEnd: '2026-10-23T19:00:00-07:00',
        now: '2026-10-23T09:00:00-07:00',
        events: [], hold: null, calendarConnected: true,
    };
    const binding = vi.fn().mockResolvedValue(shape);
    (window as any).go = { main: { App: { GetTodayDayShape: binding } } };
    await expect(GetTodayDayShape('2026-10-23')).resolves.toEqual(shape);
    expect(binding).toHaveBeenCalledWith('2026-10-23');
});

it('propagates a native reader failure for the hook to withhold the strip', async () => {
    (window as any).go = { main: { App: { GetTodayDayShape: vi.fn(async () => { throw new Error('read failed'); }) } } };
    await expect(GetTodayDayShape('2026-10-23')).rejects.toThrow('read failed');
});
it('preserves the stable date-error rejection from the backend', async () => {
    const invalid = new Error('TODAY_DAY_SHAPE_DATE_INVALID');
    const binding = vi.fn().mockRejectedValue(invalid);
    (window as any).go = { main: { App: { GetTodayDayShape: binding } } };
    await expect(GetTodayDayShape('2026-10-22')).rejects.toBe(invalid);
    expect(binding).toHaveBeenCalledWith('2026-10-22');
});
