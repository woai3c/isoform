import { fireEvent, render, screen, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { ConfirmDialog } from './confirm-dialog';

beforeEach(async () => { await i18n.changeLanguage('en'); });

describe.each(['paper', 'onedark'])('Native confirmation actions in %s', (theme) => {
    it.each(['default', 'danger'] as const)('assigns the primary fill only to an ordinary action (%s)', (variant) => {
        document.documentElement.dataset.theme = theme;
        const confirm = vi.fn();
        const change = vi.fn();
        render(<ConfirmDialog open onOpenChange={change} title="A decision" description="Review first"
            confirmLabel="Continue" onConfirm={confirm} variant={variant} />);
        const dialog = screen.getByRole('dialog');
        const primary = within(dialog).getByRole('button', { name: 'Continue' });
        const cancel = within(dialog).getByRole('button', { name: 'Cancel' });
        expect(primary).toHaveClass('px-3', 'py-1.5', 'rounded-md');
        if (variant === 'default') expect(primary).toHaveClass('patina-verdigris', 'bg-solid-btn-bg');
        else expect(primary).not.toHaveClass('patina-verdigris');
        expect(cancel).not.toHaveClass('patina-verdigris');
        expect(dialog).not.toHaveClass('patina-ground', 'patina-verdigris');
        fireEvent.click(primary);
        expect(confirm).toHaveBeenCalledOnce();
        expect(change).toHaveBeenCalledWith(false);
    });
});
