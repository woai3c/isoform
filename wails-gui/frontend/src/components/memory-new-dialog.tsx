import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Save } from 'lucide-react';
import {
    Dialog,
    DialogCloseButton,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { notifyToast } from '@/lib/notify';
import { SaveMemoryEntry } from '../../wailsjs/go/main/App';
import type { main } from '../../wailsjs/go/models';

/** Write a new memory by hand, from the page's More menu. */
export function MemoryNewDialog({
    open,
    onOpenChange,
    onSaved,
}: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onSaved: (saved: main.MemoryEntry) => void;
}) {
    const { t } = useTranslation(['memory', 'common']);
    const [content, setContent] = useState('');
    const [saving, setSaving] = useState(false);

    useEffect(() => {
        if (open) setContent(t('newTemplate') as string);
    }, [open, t]);

    const save = () => {
        if (saving || !content.trim()) return;
        setSaving(true);
        SaveMemoryEntry('', content)
            .then((saved) => {
                onOpenChange(false);
                onSaved(saved);
                notifyToast(t('toasts.saved.title') as string, t('toasts.saved.body') as string, 'success');
            })
            .catch(() => {
                notifyToast(t('toasts.saveFailed.title') as string, t('toasts.saveFailed.body') as string, 'error');
            })
            .finally(() => setSaving(false));
    };

    return (
        <Dialog open={open} onOpenChange={(next) => { if (!saving) onOpenChange(next); }}>
            <DialogContent className="w-[640px] max-w-[90vw] h-[70vh]" onKeyDown={(event) => event.stopPropagation()}>
                <DialogHeader>
                    <div>
                        <DialogTitle>{t('header.newMemory')}</DialogTitle>
                        <DialogDescription className="mt-1">{t('editor.attribution')}</DialogDescription>
                    </div>
                    <DialogCloseButton disabled={saving} aria-label={t('common:close')} />
                </DialogHeader>
                <div className="flex min-h-0 flex-1 flex-col gap-3 px-6 py-4">
                    <textarea
                        className="min-h-0 w-full flex-1 resize-none rounded-xl border border-border bg-panel p-4 font-mono text-xs leading-relaxed text-ink placeholder:text-muted focus:border-border-focus focus:outline-none"
                        value={content}
                        onChange={(event) => setContent(event.target.value)}
                        placeholder={t('writePlaceholder') as string}
                        aria-label={t('editor.label') as string}
                        disabled={saving}
                        autoFocus
                    />
                </div>
                <div className="flex justify-end gap-2 px-6 pb-5">
                    <Button size="sm" variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
                        {t('common:cancel')}
                    </Button>
                    <Button size="sm" onClick={save} disabled={saving || !content.trim()} aria-busy={saving}>
                        <Save className="h-3 w-3" />
                        {saving ? t('header.saving') : t('header.save')}
                    </Button>
                </div>
            </DialogContent>
        </Dialog>
    );
}
