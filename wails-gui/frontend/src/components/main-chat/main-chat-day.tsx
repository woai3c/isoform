import { useId, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronRight } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { dayName } from '@/lib/main-chat-view';

/** The line between two local days of the main chat: Today, Yesterday or
 *  the date. */
export function MainChatDayDivider({ day, today }: { day: string; today: string }) {
    const { t, i18n } = useTranslation('chat');
    const name = dayName(day, today, i18n.resolvedLanguage);
    const label = name.kind === 'today' ? t('mainChat.view.today') : name.kind === 'yesterday' ? t('mainChat.view.yesterday') : name.text;
    return (
        <div role="separator" aria-label={label} className="flex items-center gap-3" data-main-chat-day={day}>
            <span aria-hidden className="h-px flex-1 bg-border" />
            <span aria-hidden className="text-[12px] text-muted">{label}</span>
            <span aria-hidden className="h-px flex-1 bg-border" />
        </div>
    );
}

const FOLD_NOTE_SEEN_KEY = 'isoform.mainChat.foldNoteSeen';

/** The fold's footer note shows the first time a fold opens, then never
 *  again (chat-review v1.0.10 H8). */
function foldNoteSeen(): boolean {
    try { return window.localStorage.getItem(FOLD_NOTE_SEEN_KEY) === '1'; } catch { return false; }
}

function markFoldNoteSeen() {
    try { window.localStorage.setItem(FOLD_NOTE_SEEN_KEY, '1'); } catch { /* storage unavailable */ }
}

/** One day's routine updates folded into one row ("3 updates from Isoform")
 *  that opens in place. The rows inside are the chat's own rows, unchanged. */
export function MainChatDayFold({ day, count, children }: { day: string; count: number; children: ReactNode }) {
    const { t } = useTranslation('chat');
    const [open, setOpen] = useState(false);
    const [showNote, setShowNote] = useState(false);
    const contentId = useId();
    const toggle = () => {
        if (!open && !foldNoteSeen()) {
            markFoldNoteSeen();
            setShowNote(true);
        } else if (open) {
            setShowNote(false);
        }
        setOpen((value) => !value);
    };
    return (
        <div className="flex flex-col rounded-xl border border-border-subtle" data-main-chat-fold={day}>
            <Button
                variant="quiet"
                size="sm"
                aria-expanded={open}
                aria-controls={contentId}
                onClick={toggle}
                className="h-9 w-full justify-start gap-2 px-3 text-[12.5px] font-normal"
            >
                <ChevronRight aria-hidden className={cn('size-3.5 transition-transform', open && 'rotate-90')} />
                <span>{t('mainChat.view.foldSummary', { count })}</span>
            </Button>
            <div id={contentId} hidden={!open} className="flex flex-col gap-3 px-3 pb-3 pt-1">
                {open && children}
                {showNote && <p className="text-[12px] text-muted">{t('mainChat.view.foldNote')}</p>}
            </div>
        </div>
    );
}
