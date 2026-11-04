import { act, fireEvent, render, renderHook, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { ACTIVITY_LOAD_DEADLINE_MS, useLoadDeadline } from './activity-day-view';
import { StitchedFailure } from './activity-stitched';

// activity review v1.0.5 V1: What you worked on says so in place when it
// can't load; placeholders show only while it is loading.
describe('What you worked on that cannot load', () => {
    beforeEach(async () => {
        vi.useFakeTimers();
        await i18n.changeLanguage('en');
    });
    afterEach(() => vi.useRealTimers());

    it('says it in copper with Try again as a link', () => {
        const onRetry = vi.fn();
        render(<StitchedFailure onRetry={onRetry} />);
        const alert = screen.getByRole('alert');
        expect(alert).toHaveTextContent("Couldn't load what you worked on. Try again");
        expect(alert).toHaveClass('text-copper');
        const retry = screen.getByRole('button', { name: 'Try again' });
        expect(retry).toHaveClass('text-accent');
        fireEvent.click(retry);
        expect(onRetry).toHaveBeenCalledTimes(1);
    });

    it('turns a read still loading after the deadline into a failure, and Try again restarts it', () => {
        const { result, rerender } = renderHook(({ loading, attempt }) => useLoadDeadline(loading, attempt), {
            initialProps: { loading: true, attempt: 0 },
        });
        act(() => { vi.advanceTimersByTime(ACTIVITY_LOAD_DEADLINE_MS - 1); });
        expect(result.current).toBe(false);
        act(() => { vi.advanceTimersByTime(1); });
        expect(result.current).toBe(true);
        rerender({ loading: true, attempt: 1 });
        expect(result.current).toBe(false);
        rerender({ loading: false, attempt: 1 });
        act(() => { vi.advanceTimersByTime(ACTIVITY_LOAD_DEADLINE_MS); });
        expect(result.current).toBe(false);
    });

    it('says it in Chinese too', async () => {
        await i18n.changeLanguage('zh-CN');
        render(<StitchedFailure onRetry={() => {}} />);
        expect(screen.getByRole('alert')).toHaveTextContent('没能加载你在忙什么。');
        await i18n.changeLanguage('en');
    });
});
