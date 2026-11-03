import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { ContactLinkDialog } from './contact-link-dialog';

const mocks = vi.hoisted(() => ({ search: vi.fn(), link: vi.fn() }));
vi.mock('@/lib/wails-shim-app', () => ({ SearchImportedContacts: mocks.search, LinkPersonToContact: mocks.link }));
const card = (name: string, reason = '', available = true) => ({ contactId: `${name}-synthetic-native-7F3A9C`, displayName: name, photo: '', phones: [], emails: [`${name}@example.com`], suggestedBy: reason, available });

describe('ContactLinkDialog', () => {
    beforeEach(async () => { vi.clearAllMocks(); await i18n.changeLanguage('en'); mocks.link.mockResolvedValue(undefined); });
    it('puts suggestions first and keeps identifiers out of the DOM', async () => {
        mocks.search.mockResolvedValue([card('Other'), card('Phone', 'phone'), card('Name', 'name')]);
        const { container } = render(<ContactLinkDialog personID="seen" onClose={vi.fn()} onLinked={vi.fn()} />);
        await screen.findByText('Phone');
        const options = screen.getAllByRole('button').filter((el) => el.textContent?.includes('@example.com'));
        expect(options.map((el) => el.textContent?.split('@')[0])).toEqual([
            expect.stringContaining('Phone'), expect.stringContaining('Name'), expect.stringContaining('Other'),
        ]);
        expect(container.ownerDocument.body.innerHTML).not.toContain('synthetic-native-7F3A9C');
        expect(screen.getByText('Same phone')).toBeInTheDocument();
    });
    it.each(['Maya', '+14155550123', 'ana@example.com'])('searches imported contacts for %s', async (query) => {
        mocks.search.mockResolvedValue([card('Ana')]);
        render(<ContactLinkDialog personID="seen" onClose={vi.fn()} onLinked={vi.fn()} />);
        fireEvent.change(screen.getByRole('textbox'), { target: { value: query } });
        await waitFor(() => expect(mocks.search).toHaveBeenLastCalledWith('seen', query));
    });
    it('links the card into the existing person and completes', async () => {
        mocks.search.mockResolvedValue([card('Ana')]); const linked = vi.fn();
        render(<ContactLinkDialog personID="seen" onClose={vi.fn()} onLinked={linked} />);
        fireEvent.click(await screen.findByRole('button', { name: /Ana/ }));
        await waitFor(() => expect(mocks.link).toHaveBeenCalledWith('seen', 'Ana-synthetic-native-7F3A9C'));
        expect(linked).toHaveBeenCalledOnce();
    });
    it('disables cards already connected elsewhere and shows link failures without raw errors', async () => {
        mocks.search.mockResolvedValue([card('Elsewhere', '', false), card('Ana')]);
        mocks.link.mockRejectedValue(new Error('private:synthetic-native-7F3A9C'));
        render(<ContactLinkDialog personID="seen" onClose={vi.fn()} onLinked={vi.fn()} />);
        expect(await screen.findByRole('button', { name: /Elsewhere/ })).toBeDisabled();
        fireEvent.click(screen.getByRole('button', { name: /Ana/ }));
        expect(await screen.findByRole('alert')).not.toHaveTextContent('synthetic-native-7F3A9C');
    });
});
