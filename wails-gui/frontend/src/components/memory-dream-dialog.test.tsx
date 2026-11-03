import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { MemoryDreamDialog } from './memory-dream-dialog';

const mocks = vi.hoisted(() => ({
    status: vi.fn(),
    runs: vi.fn(),
    archives: vi.fn(),
    preview: vi.fn(),
    cancel: vi.fn(),
    notify: vi.fn(),
    events: new Map<string, (...args: unknown[]) => void>(),
}));

vi.mock('../../wailsjs/go/main/App', () => ({
    GetMemoryDreamStatus: mocks.status,
    GetMemoryDreamSettings: vi.fn().mockResolvedValue(null),
    SaveMemoryDreamSettings: vi.fn(),
    PreviewMemoryDream: mocks.preview,
    RunMemoryDream: vi.fn(),
    CancelMemoryDream: mocks.cancel,
    GetMemoryDreamRuns: mocks.runs,
    ListArchivedMemories: mocks.archives,
    RestoreArchivedMemory: vi.fn(),
    UndoMemoryDreamRun: vi.fn(),
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (event: string, callback: (...args: unknown[]) => void) => {
        mocks.events.set(event, callback);
        return () => mocks.events.delete(event);
    },
}));
vi.mock('@/lib/notify', () => ({ notifyToast: mocks.notify }));

const idle = { running: false, nextRunMs: 0, archiveCount: 0 };
const running = { ...idle, running: true, currentRunId: 'dream-1' };

describe('MemoryDreamDialog cancellation', () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        mocks.events.clear();
        mocks.status.mockResolvedValue(idle);
        mocks.runs.mockResolvedValue([]);
        mocks.archives.mockResolvedValue([]);
        mocks.cancel.mockResolvedValue(undefined);
        await i18n.changeLanguage('en');
    });

    it('offers cancellation only while a cycle is running', async () => {
        render(<MemoryDreamDialog open onOpenChange={vi.fn()} />);
        await screen.findByText('Idle');
        expect(screen.queryByRole('button', { name: 'Cancel' })).not.toBeInTheDocument();
        mocks.status.mockResolvedValue(running);
        act(() => mocks.events.get('memory-dream:started')?.());
        expect(await screen.findByRole('button', { name: 'Cancel' })).toBeEnabled();
        expect(screen.getByRole('button', { name: 'Run preview' })).toBeDisabled();
    });

    it('requests cancellation once and waits for completion before offering a new run', async () => {
        mocks.status.mockResolvedValue(running);
        render(<MemoryDreamDialog open onOpenChange={vi.fn()} />);
        fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }));
        await waitFor(() => expect(mocks.cancel).toHaveBeenCalledTimes(1));
        expect(screen.getByRole('button', { name: 'Cancelling…' })).toBeDisabled();
        expect(screen.getByRole('button', { name: 'Run now (live)' })).toBeDisabled();

        mocks.status.mockResolvedValue(idle);
        act(() => mocks.events.get('memory-dream:completed')?.({ status: 'cancelled', dryRun: true }));
        await waitFor(() => expect(screen.queryByRole('button', { name: 'Cancelling…' })).not.toBeInTheDocument());
        expect(screen.getByRole('button', { name: 'Run preview' })).toBeEnabled();
    });

    it('recovers when a scheduled completion arrives before the running flag clears', async () => {
        vi.useFakeTimers();
        try {
            mocks.status.mockResolvedValue(running);
            render(<MemoryDreamDialog open onOpenChange={vi.fn()} />);
            await act(async () => {});
            fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
            await act(async () => {});
            act(() => mocks.events.get('memory-dream:completed')?.({ status: 'cancelled', dryRun: true }));
            await act(async () => {});
            expect(screen.getByRole('button', { name: 'Cancelling…' })).toBeDisabled();
            expect(screen.getByRole('button', { name: 'Run preview' })).toBeDisabled();

            // There is no PreviewMemoryDream/RunMemoryDream promise in this
            // dialog for a scheduled run. Only the follow-up poll sees idle.
            mocks.status.mockResolvedValue(idle);
            await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
            expect(screen.queryByRole('button', { name: 'Cancelling…' })).not.toBeInTheDocument();
            expect(screen.getByRole('button', { name: 'Run preview' })).toBeEnabled();
            const callsAfterIdle = mocks.status.mock.calls.length;
            await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
            expect(mocks.status).toHaveBeenCalledTimes(callsAfterIdle);
        } finally {
            vi.useRealTimers();
        }
    });

    it('polls only status and refreshes history and archives on completion', async () => {
        vi.useFakeTimers();
        try {
            mocks.status.mockResolvedValue(running);
            render(<MemoryDreamDialog open onOpenChange={vi.fn()} />);
            await act(async () => {});
            expect(mocks.runs).toHaveBeenCalledTimes(1);
            expect(mocks.archives).toHaveBeenCalledTimes(1);

            await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
            act(() => mocks.events.get('memory-dream:progress')?.());
            await act(async () => {});
            expect(mocks.status.mock.calls.length).toBeGreaterThan(1);
            expect(mocks.runs).toHaveBeenCalledTimes(1);
            expect(mocks.archives).toHaveBeenCalledTimes(1);

            act(() => mocks.events.get('memory-dream:completed')?.({ status: 'completed', dryRun: false }));
            await act(async () => {});
            expect(mocks.runs).toHaveBeenCalledTimes(2);
            expect(mocks.archives).toHaveBeenCalledTimes(2);
            mocks.status.mockResolvedValue(idle);
            await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
            // A final status transition also refreshes lists, covering a
            // missed completion event without scanning on running polls.
            expect(mocks.runs).toHaveBeenCalledTimes(3);
            expect(mocks.archives).toHaveBeenCalledTimes(3);
            expect(screen.getByRole('button', { name: 'Run preview' })).toBeEnabled();
        } finally {
            vi.useRealTimers();
        }
    });

    it('refreshes lists when polling detects completion without an event', async () => {
        vi.useFakeTimers();
        try {
            mocks.status.mockResolvedValue(running);
            render(<MemoryDreamDialog open onOpenChange={vi.fn()} />);
            await act(async () => {});
            mocks.status.mockResolvedValue(idle);
            await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
            expect(mocks.runs).toHaveBeenCalledTimes(2);
            expect(mocks.archives).toHaveBeenCalledTimes(2);
            expect(screen.getByRole('button', { name: 'Run preview' })).toBeEnabled();
            await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
            expect(mocks.runs).toHaveBeenCalledTimes(2);
            expect(mocks.archives).toHaveBeenCalledTimes(2);
        } finally {
            vi.useRealTimers();
        }
    });

    it('stops running-status polling when the dialog closes or unmounts', async () => {
        vi.useFakeTimers();
        try {
            mocks.status.mockResolvedValue(running);
            const onOpenChange = vi.fn();
            const { rerender, unmount } = render(<MemoryDreamDialog open onOpenChange={onOpenChange} />);
            await act(async () => {});
            rerender(<MemoryDreamDialog open={false} onOpenChange={onOpenChange} />);
            const callsAfterClose = mocks.status.mock.calls.length;
            await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
            expect(mocks.status).toHaveBeenCalledTimes(callsAfterClose);

            rerender(<MemoryDreamDialog open onOpenChange={onOpenChange} />);
            await act(async () => {});
            unmount();
            const callsAfterUnmount = mocks.status.mock.calls.length;
            await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
            expect(mocks.status).toHaveBeenCalledTimes(callsAfterUnmount);
        } finally {
            vi.useRealTimers();
        }
    });

    it('ignores stale refreshes and delayed start events after cancellation', async () => {
        let resolveInitial!: (status: typeof idle) => void;
        mocks.status.mockReturnValueOnce(new Promise((resolve) => { resolveInitial = resolve; }));
        mocks.status.mockResolvedValue(running);
        render(<MemoryDreamDialog open onOpenChange={vi.fn()} />);
        act(() => mocks.events.get('memory-dream:started')?.());
        fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }));
        await waitFor(() => expect(mocks.cancel).toHaveBeenCalledOnce());
        await act(async () => resolveInitial(idle));
        act(() => mocks.events.get('memory-dream:started')?.());
        await waitFor(() => expect(mocks.status.mock.calls.length).toBeGreaterThanOrEqual(4));
        expect(screen.getByRole('button', { name: 'Cancelling…' })).toBeDisabled();
        expect(screen.getByRole('button', { name: 'Run preview' })).toBeDisabled();
    });

    it('reports cancellation failures and lets the user retry', async () => {
        mocks.status.mockResolvedValue(running);
        mocks.cancel.mockRejectedValueOnce(new Error('connection lost'));
        render(<MemoryDreamDialog open onOpenChange={vi.fn()} />);
        fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }));
        await waitFor(() => expect(mocks.notify).toHaveBeenCalledWith('Memory Dream', 'Could not cancel Dream: Error: connection lost', 'error'));
        expect(screen.getByRole('button', { name: 'Cancel' })).toBeEnabled();
    });

    it('does not report an expected preview cancellation as a failed run', async () => {
        let rejectPreview!: (reason: Error) => void;
        mocks.preview.mockReturnValue(new Promise((_, reject) => { rejectPreview = reject; }));
        render(<MemoryDreamDialog open onOpenChange={vi.fn()} />);
        fireEvent.click(await screen.findByRole('button', { name: 'Run preview' }));
        mocks.status.mockResolvedValue(running);
        act(() => mocks.events.get('memory-dream:started')?.());
        fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }));
        await waitFor(() => expect(mocks.cancel).toHaveBeenCalledOnce());
        mocks.status.mockResolvedValue(idle);
        await act(async () => rejectPreview(new Error('context canceled')));
        expect(mocks.notify.mock.calls.some((call) => call[2] === 'error')).toBe(false);
        expect(screen.getByRole('button', { name: 'Run preview' })).toBeEnabled();
    });

    it('localizes the running cancel action in Chinese', async () => {
        await i18n.changeLanguage('zh-CN');
        mocks.status.mockResolvedValue(running);
        render(<MemoryDreamDialog open onOpenChange={vi.fn()} />);
        fireEvent.click(await screen.findByRole('button', { name: '取消' }));
        expect(screen.getByRole('button', { name: '正在取消…' })).toBeDisabled();
        await waitFor(() => expect(mocks.cancel).toHaveBeenCalledOnce());
    });
});
