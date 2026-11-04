import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { ContactsImportDialog } from './contacts-import-dialog';

const mocks = vi.hoisted(() => ({ get: vi.fn(), accept: vi.fn(), request: vi.fn(), remove: vi.fn(), pick: vi.fn(), file: vi.fn() }));
vi.mock('@/lib/wails-shim-app', () => ({
    GetContactsAccessStatus: mocks.get, AcceptContactsConsent: mocks.accept,
    RequestContactsAccess: mocks.request, RemoveImportedContacts: mocks.remove,
    OpenPermissionSettings: vi.fn(), PickVCardFile: mocks.pick, ImportContactsFromVCard: mocks.file,
}));
const initial = { supported: true, permission: 'not_determined', consentAccepted: false, importedCount: 0, lastImportAt: '' };
const granted = { ...initial, permission: 'granted', consentAccepted: true };

describe('Contacts consent', () => {
    beforeEach(async () => {
        vi.clearAllMocks(); await i18n.changeLanguage('en');
        mocks.get.mockResolvedValue(initial);
        mocks.accept.mockResolvedValue({ ...initial, consentAccepted: true });
        mocks.request.mockResolvedValue(granted);
    });
    it('opens without reading contacts or requesting permission', async () => {
        render(<ContactsImportDialog open onOpenChange={vi.fn()} onImported={vi.fn()} />);
        await screen.findByRole('checkbox');
        expect(mocks.accept).not.toHaveBeenCalled(); expect(mocks.request).not.toHaveBeenCalled();
        expect(screen.queryByRole('searchbox')).not.toBeInTheDocument();
        expect(screen.queryByText('Import selected contacts')).not.toBeInTheDocument();
    });
    it.each(['en', 'zh-CN'])('requires consent and closes after grant in %s', async (lang) => {
        await i18n.changeLanguage(lang);
        const close = vi.fn(); const changed = vi.fn();
        render(<ContactsImportDialog open onOpenChange={close} onImported={changed} />);
        const connect = await screen.findByRole('button', { name: i18n.t('people:contactsSyncConnect') });
        expect(connect).toBeDisabled();
        fireEvent.click(screen.getByRole('checkbox')); fireEvent.click(connect);
        await waitFor(() => expect(mocks.accept).toHaveBeenCalledWith(3));
        await waitFor(() => expect(close).toHaveBeenCalledWith(false));
        expect(mocks.request).toHaveBeenCalledOnce(); expect(changed).toHaveBeenCalledOnce();
        expect(screen.queryByText(/Select all shown/)).not.toBeInTheDocument();
    });
    it.each(['en', 'zh-CN'])('uses file-only acceptance copy without promising sync in %s', async (lang) => {
        await i18n.changeLanguage(lang);
        mocks.get.mockResolvedValue({ ...initial, supported: false, vcardSupported: true, permission: 'not_applicable' });
        render(<ContactsImportDialog open onOpenChange={vi.fn()} onImported={vi.fn()} />);
        expect(await screen.findByText(i18n.t('people:contactsFileConsentAccept'))).toBeInTheDocument();
        expect(screen.getByText(i18n.t('people:contactsFileConsentWhat'))).toBeInTheDocument();
        expect(screen.queryByText(i18n.t('people:contactsImportConsentAccept'))).not.toBeInTheDocument();
        expect(mocks.request).not.toHaveBeenCalled();
    });
    it('does not queue a second grant refresh when the computer already allows Contacts', async () => {
        mocks.accept.mockResolvedValue(granted);
        render(<ContactsImportDialog open onOpenChange={vi.fn()} onImported={vi.fn()} />);
        fireEvent.click(await screen.findByRole('checkbox'));
        fireEvent.click(screen.getByRole('button', { name: 'Connect Contacts' }));
        await waitFor(() => expect(mocks.accept).toHaveBeenCalledWith(3));
        expect(mocks.request).not.toHaveBeenCalled();
    });
    it('does not request OS access after a consent save failure' , async () => {
        mocks.accept.mockRejectedValue(new Error('failed'));
        render(<ContactsImportDialog open onOpenChange={vi.fn()} onImported={vi.fn()} />);
        fireEvent.click(await screen.findByRole('checkbox'));
        fireEvent.click(screen.getByRole('button', { name: 'Connect Contacts' }));
        await screen.findByRole('alert'); expect(mocks.request).not.toHaveBeenCalled();
    });
    it('keeps source-scoped removal behind confirmation', async () => {
        mocks.get.mockResolvedValue({ ...granted, importedCount: 2 });
        mocks.remove.mockResolvedValue({ errors: [], removed: 2 });
        const changed = vi.fn();
        render(<ContactsImportDialog open onOpenChange={vi.fn()} onImported={changed} />);
        fireEvent.click(await screen.findByRole('button', { name: 'Remove imported contacts' }));
        expect(mocks.remove).not.toHaveBeenCalled();
        fireEvent.click(screen.getByRole('button', { name: i18n.t('people:contactsImportRemoveConfirm') }));
        await waitFor(() => expect(mocks.remove).toHaveBeenCalledOnce());
        expect(changed).toHaveBeenCalledOnce();
    });
});

it('keeps file import as a fallback on computers without native Contacts', async () => {
    await i18n.changeLanguage('en');
    mocks.get.mockResolvedValue({ ...granted, supported: false, vcardSupported: true, permission: 'not_applicable' });
    render(<ContactsImportDialog open onOpenChange={vi.fn()} onImported={vi.fn()} />);
    mocks.pick.mockResolvedValue('/synthetic.vcf'); mocks.file.mockResolvedValue({ errors: [] });
    fireEvent.click(await screen.findByRole('button', { name: i18n.t('people:contactsImportVCardButton') }));
    expect(screen.queryByRole('button', { name: 'Remove imported contacts' })).not.toBeInTheDocument();
    await waitFor(() => expect(mocks.file).toHaveBeenCalledWith('/synthetic.vcf', 3));
});

it('offers System Settings and a check-again action after access is denied', async () => {
    await i18n.changeLanguage('en');
    mocks.get.mockResolvedValue({ ...granted, permission: 'denied' });
    render(<ContactsImportDialog open onOpenChange={vi.fn()} onImported={vi.fn()} />);
    expect(await screen.findByRole('button', { name: i18n.t('people:contactsImportOpenSettings') })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: i18n.t('people:contactsImportRetry') })).toBeInTheDocument();
});

it('keeps removal available after consent even when a partial sync created no new people', async () => {
    await i18n.changeLanguage('en');
    mocks.get.mockResolvedValue({ ...granted, importedCount: 0, lastImportAt: '', syncState: 'error' });
    render(<ContactsImportDialog open onOpenChange={vi.fn()} onImported={vi.fn()} />);
    expect(await screen.findByRole('button', { name: 'Remove imported contacts' })).toBeInTheDocument();
});

describe('N21: the Contacts sheet names the step that failed, in copper, with Try again', () => {
    beforeEach(async () => { vi.clearAllMocks(); await i18n.changeLanguage('en'); });
    it('a failed access check', async () => {
        mocks.get.mockRejectedValueOnce(new Error('x')).mockResolvedValue(initial);
        render(<ContactsImportDialog open onOpenChange={vi.fn()} onImported={vi.fn()} />);
        const alert = await screen.findByRole('alert');
        expect(alert).toHaveTextContent("Couldn't check Contacts access.");
        expect(alert).toHaveClass('text-copper');
        fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
        expect(await screen.findByRole('checkbox')).toBeInTheDocument();
    });
    it('a failed consent save, then a failed sync start', async () => {
        mocks.get.mockResolvedValue(initial);
        mocks.accept.mockRejectedValueOnce(new Error('x')).mockResolvedValue({ ...initial, consentAccepted: true });
        mocks.request.mockRejectedValue(new Error('x'));
        render(<ContactsImportDialog open onOpenChange={vi.fn()} onImported={vi.fn()} />);
        fireEvent.click(await screen.findByRole('checkbox'));
        fireEvent.click(screen.getByRole('button', { name: 'Connect Contacts' }));
        expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't save your consent. No contacts were read.");
        fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
        expect(await screen.findByText("Couldn't turn on Contacts sync.")).toBeInTheDocument();
    });
    it('a failed removal', async () => {
        mocks.get.mockResolvedValue({ ...granted, importedCount: 2 });
        mocks.remove.mockRejectedValue(new Error('x'));
        render(<ContactsImportDialog open onOpenChange={vi.fn()} onImported={vi.fn()} />);
        fireEvent.click(await screen.findByRole('button', { name: 'Remove imported contacts' }));
        fireEvent.click(screen.getByRole('button', { name: i18n.t('people:contactsImportRemoveConfirm') }));
        expect(await screen.findByText("Couldn't remove the imported contacts.")).toBeInTheDocument();
        expect(screen.queryByText(/Could not update Contacts/)).not.toBeInTheDocument();
    });
    it('a vCard file with details that did not save', async () => {
        mocks.get.mockResolvedValue({ ...granted, supported: false, vcardSupported: true, permission: 'not_applicable' });
        mocks.pick.mockResolvedValue('/synthetic.vcf'); mocks.file.mockResolvedValue({ errors: ['x'] });
        render(<ContactsImportDialog open onOpenChange={vi.fn()} onImported={vi.fn()} />);
        fireEvent.click(await screen.findByRole('button', { name: i18n.t('people:contactsImportVCardButton') }));
        expect(await screen.findByText("Some contact details couldn't be saved.")).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument();
    });
});
