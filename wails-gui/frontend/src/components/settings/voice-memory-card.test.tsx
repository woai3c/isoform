import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { VoiceMemoryCard } from './voice-memory-card';
const mocks = vi.hoisted(() => ({ forget: vi.fn(), success: vi.fn(), error: vi.fn() }));
vi.mock('@/lib/wails-shim-app', () => ({ ForgetVoices: mocks.forget }));
vi.mock('sonner', () => ({ toast: { success: mocks.success, error: mocks.error } }));
beforeEach(async () => { vi.clearAllMocks(); mocks.forget.mockResolvedValue(undefined); await i18n.changeLanguage('en'); });
it.each([
    ['en', 'Forget voices', 'Forget every saved voice?', 'Isoform stops recognizing speakers by voice. Names already in call notes stay. This cannot be undone.'],
    ['zh-CN', '忘记声音', '忘记所有保存的声音？', 'Isoform 将不再通过声音识别说话人。通话笔记中已有的姓名会保留。此操作无法撤销。'],
])('asks first, then forgets stored voices from Privacy in %s (ST22)', async (language, forget, title, description) => {
    await i18n.changeLanguage(language);
    render(<VoiceMemoryCard />);
    fireEvent.click(screen.getByRole('button', { name: forget }));
    expect(mocks.forget).not.toHaveBeenCalled();
    const dialog = await screen.findByRole('alertdialog').catch(() => screen.getByRole('dialog'));
    expect(dialog).toHaveTextContent(title);
    expect(dialog).toHaveTextContent(description);
    fireEvent.click(within(dialog).getByRole('button', { name: forget }));
    await waitFor(() => expect(mocks.forget).toHaveBeenCalledOnce());
    expect(mocks.success).not.toHaveBeenCalled();
});
it('says an erasure failure in copper with a working Try again', async () => {
    mocks.forget.mockRejectedValueOnce(new Error('db unavailable'));
    render(<VoiceMemoryCard />);
    fireEvent.click(screen.getByRole('button', { name: 'Forget voices' }));
    const dialog = await screen.findByRole('alertdialog').catch(() => screen.getByRole('dialog'));
    fireEvent.click(within(dialog).getByRole('button', { name: 'Forget voices' }));
    const alert = await screen.findByRole('alert');
    expect(alert).toHaveTextContent("Couldn't forget your saved voices.");
    expect(alert.className).toContain('text-copper');
    expect(mocks.error).not.toHaveBeenCalled();
    fireEvent.click(within(alert).getByRole('button', { name: 'Try again' }));
    await waitFor(() => expect(mocks.forget).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(screen.queryByRole('alert')).toBeNull());
});
