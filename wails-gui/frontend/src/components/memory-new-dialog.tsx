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
    const [failed, setFailed] = useState(false);

    useEffect(() => {
        if (open) {
            setContent('');
            setFailed(false);
        }
    }, [open]);

    const save = () => {
        if (saving || !content.trim()) return;
        setSaving(true);
        setFailed(false);
        SaveMemoryEntry('', noteFile(content))
            .then((saved) => {
                onOpenChange(false);
                onSaved(saved);
            })
            .catch(() => setFailed(true))
            .finally(() => setSaving(false));
    };

    return (
        <Dialog open={open} onOpenChange={(next) => { if (!saving) onOpenChange(next); }}>
            <DialogContent className="w-[640px] max-w-[90vw] h-[70vh]" onKeyDown={(event) => event.stopPropagation()}>
                <DialogHeader>
                    <div>
                        <DialogTitle>{t('newNote.title')}</DialogTitle>
                        <DialogDescription className="mt-1">{t('newNote.line')}</DialogDescription>
                    </div>
                    <DialogCloseButton disabled={saving} aria-label={t('common:close')} />
                </DialogHeader>
                <div className="flex min-h-0 flex-1 flex-col gap-3 px-6 py-4">
                    <textarea
                        className="min-h-0 w-full flex-1 resize-none rounded-xl border border-border bg-panel p-4 text-[13px] leading-relaxed text-ink placeholder:text-muted focus:border-border-focus focus:outline-none"
                        value={content}
                        onChange={(event) => setContent(event.target.value)}
                        placeholder={t('newNote.placeholder') as string}
                        aria-label={t('newNote.title') as string}
                        disabled={saving}
                        autoFocus
                    />
                    {failed && (
                        <p role="alert" className="m-0 flex flex-wrap items-center gap-2 text-[12.5px] text-copper">
                            <span>{t('aboutYou.detail.saveFailed')}</span>
                            <button type="button" className="text-accent hover:underline" disabled={saving} onClick={save}>{t('aboutYou.retry')}</button>
                        </p>
                    )}
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

/** The note as a memory file: Isoform names it from the first line, so the
 *  person only writes the note. */
export function noteFile(note: string): string {
    const text = note.trim();
    const first = text.split(/\n/)[0].replace(/^#+\s*/, '').trim();
    const title = first.length > 60 ? `${first.slice(0, 57).trimEnd()}…` : first;
    return `# ${title}\n- **Source**: user_edit\n\n${text}\n`;
}
