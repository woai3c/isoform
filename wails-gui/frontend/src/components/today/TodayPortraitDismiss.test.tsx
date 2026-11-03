import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { useTodayPortraitCard } from './TodayPortraitCard';

const mocks = vi.hoisted(() => ({ read: vi.fn(), dismiss: vi.fn(), invalidate: vi.fn() }));
vi.mock('@/lib/wails-shim-app', () => ({ DismissPortraitTodayCard: mocks.dismiss }));
vi.mock('@/lib/page-queries', () => ({ GetPortraitTodayCard: mocks.read, usePagePrivacyWithdrawal: vi.fn(), usePageQueryRefresh: vi.fn() }));
vi.mock('@/lib/query-cache', () => ({ pageQueryCache: { peek: vi.fn(), invalidate: mocks.invalidate } }));

beforeEach(() => { vi.clearAllMocks(); mocks.read.mockResolvedValue({ name: ['blueprint', 'builder'], sides: [], sentence: [] }); });

it('retains a portrait when the dismissal cannot be saved, and hides it after a successful retry', async () => {
    mocks.dismiss.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(undefined);
    const { result } = renderHook(() => useTodayPortraitCard(true));
    await waitFor(() => expect(result.current.card).not.toBeNull());
    await act(async () => { await expect(result.current.dismiss()).rejects.toThrow('offline'); });
    expect(result.current.card).not.toBeNull();
    expect(mocks.invalidate).not.toHaveBeenCalled();
    await act(async () => { await result.current.dismiss(); });
    expect(result.current.card).toBeNull();
    expect(mocks.invalidate).toHaveBeenCalledWith(['GetPortraitTodayCard']);
});
