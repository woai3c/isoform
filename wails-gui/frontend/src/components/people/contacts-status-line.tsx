// Contacts' line under the Who you know headline: what the sync is doing,
// with the one action that helps (Connect Contacts, Allow access, Try again,
// Stop) on the line itself. Waiting for the first pass says nothing.
import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { CancelContactsSync, GetContactsAccessStatus, RequestContactsAccess, type ContactsAccessStatus } from '@/lib/wails-shim-app';
import { contactsLine, contactsSyncedWords } from './people-format';
import { PeopleFailureLine } from './people-failure';

const LINK = 'cursor-pointer font-medium text-accent-2 underline-offset-2 hover:underline focus-visible:underline disabled:cursor-default disabled:opacity-60';

export function ContactsStatusLine({
    access,
    onOpenSheet,
    onChanged,
}: {
    access: ContactsAccessStatus;
    onOpenSheet: () => void;
    /** Re-read the status after Stop or Try again. */
    onChanged: () => unknown;
}) {
    const { t, i18n } = useTranslation('people');
    const translate = (key: string, args?: Record<string, string>) => String(t(key as never, args as never));
    const [stop, setStop] = useState<'idle' | 'stopping' | 'stopped' | 'failed'>('idle');
    const [resumeFailed, setResumeFailed] = useState(false);
    const before = useRef(0);
    // A new pass after a stop starts the line over.
    const lastState = useRef(access.syncState);
    useEffect(() => {
        if (access.syncState === 'syncing' && lastState.current !== 'syncing' && stop === 'stopped') setStop('idle');
        lastState.current = access.syncState;
    }, [access.syncState, stop]);

    const pressStop = async () => {
        before.current = Date.parse(access.lastImportAt || '') || 0;
        setStop('stopping');
        try {
            await CancelContactsSync();
            const next = await GetContactsAccessStatus().catch(() => null);
            // The pass had already finished: the normal synced line.
            setStop(next && (Date.parse(next.lastImportAt || '') || 0) > before.current && next.syncState !== 'syncing' ? 'idle' : 'stopped');
            await onChanged();
        } catch {
            setStop('failed');
        }
    };

    // Try again after a failed pass resumes the sync (the sheet's Keep
    // Contacts in sync path re-requests access and starts a pass).
    const retrySync = async () => {
        setResumeFailed(false);
        try {
            await RequestContactsAccess();
            await onChanged();
        } catch {
            setResumeFailed(true);
            throw new Error('resume');
        }
    };

    if (stop === 'stopped') {
        return (
            <p role="status" className="text-[12px] text-muted">
                {t('contactsSyncStopped')}
            </p>
        );
    }
    if (stop === 'failed') return <PeopleFailureLine className="text-[12px]" text={t('contactsSyncStopFailed')} onRetry={pressStop} />;

    const line = contactsLine(access, translate, i18n.language);
    switch (line.kind) {
        case 'none':
            return null;
        case 'connect':
            return (
                <p className="text-[12px] text-muted">
                    <button type="button" className={LINK} onClick={onOpenSheet}>
                        {t('contactsSyncConnect')}
                    </button>
                </p>
            );
        case 'resume':
            return (
                <p role="status" className="text-[12px] text-muted">
                    {line.text}
                    <button type="button" className={`ms-1.5 ${LINK}`} onClick={onOpenSheet}>
                        {t('contactsSyncResume')}
                    </button>
                </p>
            );
        case 'access':
            return (
                <p role="status" className="text-[12px] text-muted">
                    {line.text}
                    <button type="button" className={`ms-1.5 ${LINK}`} onClick={onOpenSheet}>
                        {t('contactsImportPermissionGrant')}
                    </button>
                </p>
            );
        case 'syncing':
            return (
                <p role="status" className="text-[12px] text-muted">
                    {line.text}
                    <span aria-hidden> · </span>
                    <button type="button" className={LINK} disabled={stop === 'stopping'} onClick={() => void pressStop()}>
                        {t('contactsSyncStop')}
                    </button>
                </p>
            );
        case 'failed': {
            const synced = contactsSyncedWords(access, translate, i18n.language);
            return (
                <PeopleFailureLine className="text-[12px]" text={resumeFailed ? t('contactsSyncError') : line.text} onRetry={retrySync}>
                    {synced && <span className="ms-1.5 text-muted">{synced}</span>}
                </PeopleFailureLine>
            );
        }
        default:
            return (
                <p role="status" className="text-[12px] text-muted">
                    {line.text}
                </p>
            );
    }
}
