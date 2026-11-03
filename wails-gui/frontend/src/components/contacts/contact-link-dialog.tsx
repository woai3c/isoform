import { useEffect, useState } from 'react';
import { Loader2, Search } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import {
    LinkPersonToContact,
    SearchImportedContacts,
    type ContactMatchDTO,
} from '@/lib/wails-shim-app';
import {
    Dialog,
    DialogCloseButton,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
} from '../ui/dialog';

export function ContactLinkDialog({
    personID,
    onClose,
    onLinked,
}: {
    personID: string;
    onClose: () => void;
    onLinked: () => void;
}) {
    const { t } = useTranslation(['people', 'common']);
    const [query, setQuery] = useState('');
    const [rows, setRows] = useState<ContactMatchDTO[]>([]);
    const [loading, setLoading] = useState(true);
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState(false);
    useEffect(() => {
        let active = true;
        setLoading(true);
        setError(false);
        const timer = setTimeout(() => {
            SearchImportedContacts(personID, query)
                .then((value) => {
                    if (active) setRows(value ?? []);
                })
                .catch(() => {
                    if (active) setError(true);
                })
                .finally(() => {
                    if (active) setLoading(false);
                });
        }, 150);
        return () => {
            active = false;
            clearTimeout(timer);
        };
    }, [personID, query]);
    const link = async (row: ContactMatchDTO) => {
        setBusy(true);
        setError(false);
        try {
            await LinkPersonToContact(personID, row.contactId);
            onLinked();
        } catch {
            setError(true);
        } finally {
            setBusy(false);
        }
    };
    // Preserve the backend's evidence ranking within each group, including
    // while filtering. Suggestions are always before other name matches.
    const ordered = [
        ...rows.filter((row) => row.suggestedBy),
        ...rows.filter((row) => !row.suggestedBy),
    ];
    return (
        <Dialog
            open
            onOpenChange={(value) => {
                if (!value && !busy) onClose();
            }}
        >
            <DialogContent className="sm:max-w-[520px]">
                <DialogHeader>
                    <div className="flex flex-col gap-1 pr-3">
                        <DialogTitle>{t('contactLinkTitle')}</DialogTitle>
                        <DialogDescription>
                            {t('contactLinkDescription')}
                        </DialogDescription>
                    </div>
                    <DialogCloseButton disabled={busy} aria-label={t('common:close')} />
                </DialogHeader>
                <div className="px-6 py-4 flex flex-col gap-3 min-h-0">
                    <div className="relative">
                        <Search className="pointer-events-none absolute left-2.5 top-2.5 size-3.5 text-muted" />
                        <input
                            autoFocus
                            value={query}
                            disabled={busy}
                            onChange={(event) => setQuery(event.target.value)}
                            placeholder={t('contactLinkSearch')}
                            aria-label={t('contactLinkSearch')}
                            className="h-9 w-full rounded-md border border-border bg-panel pl-8 pr-3 text-[13px] outline-none focus-visible:ring-2 focus-visible:ring-accent-2/30"
                        />
                    </div>
                    {error && (
                        <p role="alert" className="text-[12px] text-error">
                            {t('contactLinkFailed')}
                        </p>
                    )}
                    <div className="flex max-h-[340px] flex-col gap-1 overflow-y-auto rounded-lg border border-border bg-panel/30 p-1">
                        {loading ? (
                            <Loader2 className="m-3 size-4 animate-spin text-muted" />
                        ) : ordered.length === 0 ? (
                            <p className="p-3 text-[13px] text-muted">
                                {t('contactLinkEmpty')}
                            </p>
                        ) : (
                            ordered.map((row) => (
                                <button
                                    type="button"
                                    key={row.contactId}
                                    disabled={busy || !row.available}
                                    onClick={() => void link(row)}
                                    className="flex items-center gap-3 rounded-md px-3 py-2 text-left hover:bg-ink/[0.04] disabled:opacity-50"
                                >
                                    {row.photo && (
                                        <img
                                            src={row.photo}
                                            alt=""
                                            className="size-8 rounded-full object-cover"
                                        />
                                    )}
                                    <span className="min-w-0 flex-1">
                                        <span className="block truncate text-[13px] font-medium text-ink">
                                            {row.displayName}
                                        </span>
                                        <span className="block truncate text-[12px] text-muted">
                                            {row.phones[0] || row.emails[0]}
                                        </span>
                                    </span>
                                    <span className="text-[11px] text-muted">
                                        {!row.available
                                            ? t('contactLinkConnected')
                                            : row.suggestedBy
                                              ? t(
                                                    `contactSuggestion.${row.suggestedBy}`,
                                                )
                                              : ''}
                                    </span>
                                </button>
                            ))
                        )}
                    </div>
                </div>
            </DialogContent>
        </Dialog>
    );
}
