import { act, fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { ProfilePage } from './profile-page';
const mocks = vi.hoisted(() => ({ ensure: vi.fn(), refresh: vi.fn() }));
vi.mock('@/hooks/useUserProfile', () => ({ useUserProfile: () => ({ profile: { name: 'Alex' }, loading: false }), refreshUserProfile: mocks.refresh }));
vi.mock('@/lib/wails-shim-app', () => ({ EnsureSelfProfile: mocks.ensure }));
beforeEach(() => { vi.clearAllMocks(); });
it.each(['en', 'zh-CN'])('keeps name editing and recoverable failures on Profile in %s', async (language) => {
    await i18n.changeLanguage(language);
    const t = i18n.getFixedT(language, 'app');
    render(<ProfilePage />);
    const input = screen.getByLabelText(t('userMenu.nameLabel'));
    expect(input).toHaveValue('Alex');
    fireEvent.change(input, { target: { value: '  Alex User  ' } });
    mocks.ensure.mockRejectedValueOnce(new Error('store closed'));
    await act(async () => fireEvent.click(screen.getByText(t('userMenu.saveName'))));
    expect(screen.getByRole('alert')).toHaveTextContent(t('userMenu.nameSaveError'));
    expect(input).toHaveValue('  Alex User  ');
    mocks.ensure.mockResolvedValueOnce({});
    await act(async () => fireEvent.click(screen.getByText(t('userMenu.saveName'))));
    expect(mocks.ensure).toHaveBeenLastCalledWith('Alex User', []);
    expect(mocks.refresh).toHaveBeenCalledOnce();
});

it('has no card title under the Profile page and shows Save only once the name changes (ST71)', async () => {
    await i18n.changeLanguage('en');
    const t = i18n.getFixedT('en', 'app');
    render(<ProfilePage />);
    expect(screen.queryByRole('heading', { name: t('userMenu.profile') })).not.toBeInTheDocument();
    expect(screen.queryByText(t('userMenu.saveName'))).not.toBeInTheDocument();
    expect(screen.queryByText(t('userMenu.cancelName'))).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(t('userMenu.nameLabel')), { target: { value: 'Alex Rivers' } });
    expect(screen.getByText(t('userMenu.saveName'))).toBeInTheDocument();
});
