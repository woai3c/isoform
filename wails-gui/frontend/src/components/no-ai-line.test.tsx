import { act, fireEvent, render, renderHook, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { useAIReady } from '@/hooks/useAIReady';
import { NoAILine } from './no-ai-line';

const mocks = vi.hoisted(() => ({ emit: vi.fn(), ready: vi.fn(async () => false) }));

vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsEmit: mocks.emit,
    EventsOn: vi.fn(() => () => {}),
}));
vi.mock('@/lib/wails-shim-app', () => ({ AIReady: mocks.ready }));

// K1 box 2: the box line before typing, Set up to Settings › Your AI, read again when the window returns.
describe('NoAILine', () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        await i18n.changeLanguage('en');
    });

    it('says Isoform needs an AI and Set up opens Your AI', () => {
        render(<NoAILine />);
        expect(screen.getByRole('status')).toHaveTextContent('Isoform needs an AI to answer. · Set up');
        expect(screen.getByRole('status')).toHaveClass('text-warning');
        fireEvent.click(screen.getByRole('button', { name: 'Set up' }));
        expect(mocks.emit).toHaveBeenCalledWith('settings:open', 'ai-provider');
    });

    it('reads in Chinese', async () => {
        await act(async () => { await i18n.changeLanguage('zh-CN'); });
        try {
            render(<NoAILine />);
            expect(screen.getByRole('status')).toHaveTextContent('Isoform 需要一个 AI 才能回答。· 去设置');
        } finally {
            await act(async () => { await i18n.changeLanguage('en'); });
        }
    });

    it('re-reads AIReady when the window comes back to the front', async () => {
        mocks.ready.mockResolvedValueOnce(false).mockResolvedValueOnce(true);
        const { result } = renderHook(() => useAIReady());
        await waitFor(() => expect(result.current).toBe(false));
        act(() => { window.dispatchEvent(new Event('focus')); });
        await waitFor(() => expect(result.current).toBe(true));
        expect(mocks.ready).toHaveBeenCalledTimes(2);
    });
});
