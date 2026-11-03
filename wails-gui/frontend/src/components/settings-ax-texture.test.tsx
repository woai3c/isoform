import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { SettingsPage } from './settings-page';
import type { useKeybindings } from '@/hooks/useKeybindings';

vi.mock('@/lib/wails-shim-app', () => new Proxy({}, {
    has: () => true,
    get: (_target, name) => name === 'then' ? undefined : vi.fn().mockResolvedValue([]),
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: vi.fn(() => () => {}) }));

beforeEach(async () => { localStorage.clear(); await i18n.changeLanguage('en'); });

describe.each(['paper', 'onedark'])('AX parser selection in %s', (theme) => {
    it('uses a flat settings segment and keeps Probe as the primary action', async () => {
        document.documentElement.dataset.theme = theme;
        render(<SettingsPage onClose={vi.fn()} onReplayOnboarding={vi.fn()} vimMode={false}
            onVimModeChange={vi.fn()} keybindings={{} as ReturnType<typeof useKeybindings>}
            onOpenMemory={vi.fn()} onOpenPeople={vi.fn()} onOpenProfile={vi.fn()} initialTopic="ax-parser" devToolsUnlocked />);
        const group = await screen.findByRole('radiogroup', { name: i18n.t('settings:axParser.modeLabel') });
        expect(group).toHaveClass('settings-segment');
        const choices = within(group).getAllByRole('radio');
        expect(choices[0]).toBeChecked();
        for (const choice of choices) expect(choice).not.toHaveClass('patina-verdigris', 'bg-solid-btn-bg');
        expect(choices[0].style.backgroundColor).toBe('var(--color-surface-active)');
        fireEvent.click(choices[1]);
        expect(choices[1]).toBeChecked();
        expect(choices[0]).not.toBeChecked();
        expect(choices[1].style.backgroundColor).toBe('var(--color-surface-active)');
        expect(choices[0].style.backgroundColor).toBe('');
        expect(screen.getByRole('button', { name: i18n.t('settings:axParser.probeButton') })).toHaveClass('patina-verdigris');
        await waitFor(() => expect(screen.getByRole('button', { name: i18n.t('settings:axParser.probeButton') })).toBeDisabled());
    });
});
