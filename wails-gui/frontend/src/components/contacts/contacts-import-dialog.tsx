import { useEffect, useState } from 'react';
import { Loader2 } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import {
    AcceptContactsConsent,
    GetContactsAccessStatus,
    PickVCardFile,
    ImportContactsFromVCard,
    OpenPermissionSettings,
    RemoveImportedContacts,
    RequestContactsAccess,
    type ContactsAccessStatus,
} from '@/lib/wails-shim-app';
import { ConfirmDialog } from '../confirm-dialog';
import {
    Dialog,
    DialogCloseButton,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
} from '../ui/dialog';
import { Button } from '../ui/button';
import { Checkbox } from '../ui/checkbox';
import { PeopleFailureLine } from '../people/people-failure';

/** The step that failed, named in its own words with Try again. */
type FailedStep = 'access' | 'consent' | 'sync' | 'vcard' | 'errors' | 'remove' | 'settings';
const FAILED_STEP_KEY: Record<FailedStep, string> = {
    access: 'contactsImportAccessFailed',
    consent: 'contactsImportConsentFailed',
    sync: 'contactsSyncFailed',
    vcard: 'contactsImportVCardFailed',
    errors: 'contactsImportErrors',
    remove: 'contactsImportRemoveFailed',
    settings: 'contactsImportSettingsFailed',
};

const CONTACTS_CONSENT_VERSION = 3;

// The sheet is for consent and managing access. Saving cards happens quietly
// in the backend; opening or dismissing this sheet never enumerates contacts.
export function ContactsImportDialog({
    open,
    onOpenChange,
    onImported,
}: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onImported: () => void;
}) {
    const { t } = useTranslation(['people', 'common']);
    const [access, setAccess] = useState<ContactsAccessStatus | null>(null);
    const [checked, setChecked] = useState(false);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState<FailedStep | null>(null);
    const [checkAttempt, setCheckAttempt] = useState(0);
    const [removeOpen, setRemoveOpen] = useState(false);

    useEffect(() => {
        if (!open) return;
        let active = true;
        setAccess(null);
        setChecked(false);
        setError(null);
        GetContactsAccessStatus()
            .then((value) => {
                if (active) setAccess(value);
            })
            .catch(() => {
                if (active) setError('access');
            });
        return () => {
            active = false;
        };
    }, [open, checkAttempt]);

    const connect = async () => {
        setBusy(true);
        setError(null);
        let consent: ContactsAccessStatus;
        try {
            consent = await AcceptContactsConsent(CONTACTS_CONSENT_VERSION);
            setAccess(consent);
        } catch {
            setError('consent');
            setBusy(false);
            return;
        }
        try {
            const next = consent.supported && consent.permission !== 'granted'
                ? await RequestContactsAccess()
                : consent;
            setAccess(next);
            onImported();
            if (next.permission === 'granted') onOpenChange(false);
        } catch {
            setError('sync');
        } finally {
            setBusy(false);
        }
    };
    const retry = async () => {
        setBusy(true);
        setError(null);
        try {
            const next = await RequestContactsAccess();
            setAccess(next);
            onImported();
            if (next.permission === 'granted') onOpenChange(false);
        } catch {
            setError('sync');
        } finally {
            setBusy(false);
        }
    };
    const openSettings = () => {
        setError(null);
        void OpenPermissionSettings('contacts').catch(() => setError('settings'));
    };
    const importFile = async () => {
        setBusy(true);
        setError(null);
        try {
            const path = await PickVCardFile();
            if (!path) return;
            const result = await ImportContactsFromVCard(
                path,
                CONTACTS_CONSENT_VERSION,
            );
            onImported();
            if (result.errors.length) {
                setError('errors');
                return;
            }
            onOpenChange(false);
        } catch {
            setError('vcard');
        } finally {
            setBusy(false);
        }
    };
    const remove = async () => {
        setBusy(true);
        setError(null);
        try {
            const result = await RemoveImportedContacts();
            if (result.errors.length) {
                setError('remove');
                return;
            }
            onImported();
            onOpenChange(false);
        } catch {
            setError('remove');
        } finally {
            setBusy(false);
        }
    };
    const retryStep: Record<FailedStep, () => unknown> = {
        access: () => setCheckAttempt((n) => n + 1),
        consent: connect,
        sync: retry,
        vcard: importFile,
        errors: importFile,
        remove,
        settings: openSettings,
    };

    return (
        <>
            <Dialog
                open={open}
                onOpenChange={(value) => {
                    if (!busy) onOpenChange(value);
                }}
            >
                <DialogContent className="sm:max-w-[520px]">
                    <DialogHeader>
                        <div className="flex flex-col gap-1 pr-3">
                            <DialogTitle>{t('contactsSyncTitle')}</DialogTitle>
                            <DialogDescription>
                                {t('contactsImportConsentWhy')}
                            </DialogDescription>
                        </div>
                        <DialogCloseButton disabled={busy} aria-label={t('common:close')} />
                    </DialogHeader>
                    <div className="flex flex-col gap-4 overflow-y-auto px-6 py-5 text-[13px] text-muted">
                        {!access && !error && (
                            <Loader2 className="size-4 animate-spin" />
                        )}
                        {access && !access.consentAccepted && (
                            <>
                                <p>
                                    {t(
                                        access.supported
                                            ? 'contactsImportConsentWhat'
                                            : 'contactsFileConsentWhat',
                                    )}
                                </p>
                                <p>{t('contactsImportConsentWhere')}</p>
                                <p>{t('contactsImportConsentNever')}</p>
                                <label className="flex cursor-pointer items-start gap-2 text-ink">
                                    <Checkbox
                                        checked={checked}
                                        disabled={busy}
                                        onCheckedChange={(value) =>
                                            setChecked(value === true)
                                        }
                                    />
                                    <span>
                                        {t(
                                            access.supported
                                                ? 'contactsImportConsentAccept'
                                                : 'contactsFileConsentAccept',
                                        )}
                                    </span>
                                </label>
                                <Button
                                    disabled={
                                        !checked ||
                                        busy ||
                                        (!access.supported &&
                                            !access.vcardSupported)
                                    }
                                    onClick={() => void connect()}
                                >
                                    {busy && (
                                        <Loader2 className="size-3.5 animate-spin" />
                                    )}
                                    {t(
                                        access.supported
                                            ? 'contactsSyncConnect'
                                            : 'contactsImportConsentContinue',
                                    )}
                                </Button>
                            </>
                        )}
                        {access?.consentAccepted && (
                            <>
                                <p>
                                    {t(
                                        access.supported
                                            ? 'contactsImportConsentSummary'
                                            : 'contactsFileConsentSummary',
                                    )}
                                </p>
                                {access.permission === 'denied' && (
                                    <>
                                        <p>
                                            {t(
                                                'contactsImportPermissionDenied',
                                            )}
                                        </p>
                                        <Button
                                            variant="outline"
                                            disabled={busy}
                                            onClick={openSettings}
                                        >
                                            {t('contactsImportOpenSettings')}
                                        </Button>
                                    </>
                                )}
                                {access.permission === 'restricted' ? (
                                    <p>
                                        {t(
                                            'contactsImportPermissionRestricted',
                                        )}
                                    </p>
                                ) : (
                                    access.supported && (
                                        <Button
                                            disabled={busy}
                                            onClick={() => void retry()}
                                        >
                                            {t(
                                                access.permission === 'granted'
                                                    ? 'contactsSyncResume'
                                                    : access.permission === 'denied' ? 'contactsImportRetry' : 'contactsImportPermissionGrant',
                                            )}
                                        </Button>
                                    )
                                )}
                            </>
                        )}
                        {access?.consentAccepted &&
                            !access.supported &&
                            access.vcardSupported && (
                                <Button
                                    disabled={busy}
                                    onClick={() => void importFile()}
                                >
                                    {t('contactsImportVCardButton')}
                                </Button>
                            )}
                        {access && !access.supported && (
                            <p>{t('contactsImportUnavailable')}</p>
                        )}
                        {error && (
                            <PeopleFailureLine
                                text={t(FAILED_STEP_KEY[error] as never) as unknown as string}
                                onRetry={retryStep[error]}
                            />
                        )}
                        {access &&
                            (access.importedCount > 0 ||
                                (access.supported && (access.consentAccepted || access.lastImportAt))) && (
                                <button
                                    type="button"
                                    disabled={busy}
                                    className="text-left text-[12px] hover:text-ink"
                                    onClick={() => setRemoveOpen(true)}
                                >
                                    {t('contactsSyncRemove')}
                                </button>
                            )}
                    </div>
                </DialogContent>
            </Dialog>
            <ConfirmDialog
                open={removeOpen}
                onOpenChange={setRemoveOpen}
                title={t('contactsImportRemoveTitle')}
                description={t('contactsSyncRemoveDescription')}
                confirmLabel={t('contactsImportRemoveConfirm')}
                variant="danger"
                onConfirm={() => void remove()}
            />
        </>
    );
}
