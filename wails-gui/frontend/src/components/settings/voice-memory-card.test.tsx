import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { VoiceMemoryCard } from './voice-memory-card';
const mocks = vi.hoisted(() => ({ forget: vi.fn(), success: vi.fn(), error: vi.fn() }));
vi.mock('@/lib/wails-shim-app', () => ({ ForgetVoices: mocks.forget }));
vi.mock('sonner', () => ({ toast: { success: mocks.success, error: mocks.error } }));
beforeEach(async () => { vi.clearAllMocks(); mocks.forget.mockResolvedValue(undefined); await i18n.changeLanguage('en'); });
it.each(['en', 'zh-CN'])('forgets stored voices from Privacy in %s', async (language) => {
    await i18n.changeLanguage(language);
    render(<VoiceMemoryCard />);
    fireEvent.click(screen.getByRole('button', { name: language === 'en' ? 'Forget voices' : '忘记声音' }));
    await waitFor(() => expect(mocks.forget).toHaveBeenCalledOnce());
    await waitFor(() => expect(mocks.success).toHaveBeenCalledOnce());
});
it('reports an erasure failure', async () => {
    mocks.forget.mockRejectedValue(new Error('db unavailable'));
    render(<VoiceMemoryCard />);
    fireEvent.click(screen.getByRole('button', { name: 'Forget voices' }));
    await waitFor(() => expect(mocks.error).toHaveBeenCalledOnce());
});
