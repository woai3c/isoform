import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Trash2 } from 'lucide-react';
import {
    Dialog,
    DialogCloseButton,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
} from '@/components/ui/dialog';
import {
    ForgetMemoryEntry,
    GetMemoryForgetPreview,
    RejectMemoryAttributionWithPreview,
    type MemoryForgetPreview,
} from '@/lib/wails-shim-app';

interface MemoryForgetDialogProps {
    target: { id: string; title: string } | null;
    rejectionReason?: string;
    onOpenChange: (open: boolean) => void;
    onForgot: (preview: MemoryForgetPreview, correctedAttribution: boolean) => void;
    /** Developer tools on: the itemized list, counts and scope notes. */
    devTools?: boolean;
}

export function MemoryForgetDialog({ target, rejectionReason, onOpenChange, onForgot, devTools = false }: MemoryForgetDialogProps) {
    const { t } = useTranslation(['memory', 'common']);
    const [preview, setPreview] = useState<MemoryForgetPreview | null>(null);
    const [loading, setLoading] = useState(false);
    const [forgetting, setForgetting] = useState(false);
    const [error, setError] = useState<'previewFailed' | 'forgetFailed' | null>(null);
    const [revision, setRevision] = useState(0);
    const correctingAttribution = Boolean(rejectionReason);

    useEffect(() => {
        setPreview(null);
        setError(null);
        if (!target) return;
        let cancelled = false;
        setLoading(true);
        GetMemoryForgetPreview(target.id)
            .then((result) => {
                if (!cancelled) setPreview(result);
            })
            .catch(() => {
                if (!cancelled) setError('previewFailed');
            })
            .finally(() => {
                if (!cancelled) setLoading(false);
            });
        return () => { cancelled = true; };
    }, [target?.id, revision]);

    const handleForget = async () => {
        if (!preview || preview.memoryId !== target?.id || forgetting) return;
        setForgetting(true);
        setError(null);
        try {
            if (rejectionReason) {
                await RejectMemoryAttributionWithPreview(preview.memoryId, rejectionReason, preview.confirmationToken);
            } else {
                await ForgetMemoryEntry(preview.memoryId, preview.confirmationToken);
            }
            onForgot(preview, correctingAttribution);
            onOpenChange(false);
        } catch {
            // A changed scope needs a new preview and explicit confirmation.
            setPreview(null);
            setError('forgetFailed');
        } finally {
            setForgetting(false);
        }
    };

    return (
        <Dialog open={target !== null} onOpenChange={(open) => { if (!forgetting) onOpenChange(open); }}>
            <DialogContent className="w-[520px] max-w-[90vw]" onKeyDown={(event) => event.stopPropagation()}>
                <DialogHeader>
                    <div>
                        <DialogTitle>{t(correctingAttribution ? 'correction.title' : 'forget.title')}</DialogTitle>
                        <DialogDescription className="mt-1">
                            {t(correctingAttribution ? 'correction.description' : 'forget.description', { title: target?.title ?? '' })}
                        </DialogDescription>
                    </div>
                    <DialogCloseButton disabled={forgetting} aria-label={t('common:close')} />
                </DialogHeader>
                <div className="px-6 py-5 space-y-4 overflow-y-auto min-h-0 text-xs text-muted">
                    {error && (
                        <p role="alert" className="text-copper">
                            {correctingAttribution
                                ? t(error === 'previewFailed' ? 'correction.previewFailed' : 'correction.failed')
                                : t(`forget.${error}`)}
                        </p>
                    )}
                    {preview && preview.memoryId === target?.id && (
                        <>
                            <p className="text-ink">{t('forget.bodyRemoves')}</p>
                            <p className="text-ink">{t('forget.bodyDays', { days: preview.retentionDays })}</p>
                            <p className="text-ink">{t('forget.bodyStays')}</p>
                            <p className="text-ink">{t('forget.irreversible')}</p>
                        </>
                    )}
                    {devTools && preview && preview.memoryId === target?.id && (
                        <div data-testid="forget-dev-details" className="space-y-4 border-t border-border-subtle pt-4">
                            <div>
                                <h3 className="font-medium text-ink mb-2">{t('forget.removes')}</h3>
                                <ul className="space-y-2">
                                    {preview.items.map((item) => (
                                        <li key={`${item.kind}:${item.id}`} className="rounded-md border border-border p-3">
                                            <span className="block text-[10px] text-muted mb-1">
                                                {t(`forget.kinds.${item.kind}`)}
                                            </span>
                                            <span className="text-ink break-words">
                                                {item.kind === 'memory' && item.id === target.id
                                                    ? target.title
                                                    : item.title && item.title !== item.id
                                                        ? item.title
                                                        : t(`forget.recordTypes.${item.recordType ?? 'entry'}`)}
                                            </span>
                                        </li>
                                    ))}
                                </ul>
                            </div>
                            <p>{t('forget.storedCopies', {
                                embeddings: preview.embeddingCount,
                                versions: preview.historyCount,
                            })}</p>
                            {preview.items.some((item) => item.kind === 'shadow_learning') && (
                                <p>{t('forget.sharedLearningScope')}</p>
                            )}
                            <p>{t('forget.suppression', { days: preview.retentionDays })}</p>
                            <p>{t('forget.matchingProtection')}</p>
                            <p>{t('forget.scopeLimit')}</p>
                        </div>
                    )}
                </div>
                <div className="flex justify-end gap-2 px-6 pb-5 shrink-0">
                    <button
                        type="button"
                        className="px-3 py-1.5 rounded-md text-xs font-medium text-ink bg-border/50 hover:bg-border disabled:opacity-50"
                        disabled={forgetting}
                        onClick={() => onOpenChange(false)}
                    >
                        {t('common:cancel')}
                    </button>
                    {error ? (
                        <button
                            type="button"
                            className="px-3 py-1.5 rounded-md text-xs font-medium text-ink bg-border/50 hover:bg-border"
                            onClick={() => setRevision((value) => value + 1)}
                        >
                            {t('forget.refresh')}
                        </button>
                    ) : (
                        <button
                            type="button"
                            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium bg-error text-solid-btn-fg hover:opacity-90 disabled:opacity-50"
                            disabled={loading || !preview || preview.memoryId !== target?.id || forgetting}
                            onClick={handleForget}
                        >
                            {forgetting ? <Loader2 className="h-3 w-3 animate-spin" /> : <Trash2 className="h-3 w-3" />}
                            {forgetting
                                ? t(correctingAttribution ? 'correction.correcting' : 'forget.forgetting')
                                : t(correctingAttribution ? 'correction.confirm' : 'forget.confirm')}
                        </button>
                    )}
                </div>
            </DialogContent>
        </Dialog>
    );
}
