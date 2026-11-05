import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import type { ReactNode } from 'react';
import { Pencil, Trash2, UserX } from 'lucide-react';
import type { main } from '../../wailsjs/go/models';
import type { AboutYouBelief } from '@/lib/wails-shim-app';
import { BeliefStatusMark } from '@/components/about-you/belief-status';
import { cn } from '@/lib/utils';

import { beliefInSecondPerson, humanMemoryText, memoryPoints, memoryKind, memoryLabel, relativeWords } from '@/lib/memory-content';
export { memoryText, memoryLabel } from '@/lib/memory-content';

/** Calendar days avoid clock times and keep yesterday correct across DST. */
export function learnedWhen(t: TFunction<'memory'>, ms: number): string {
    if (!Number.isFinite(ms) || ms <= 0) return t('relativeTime.unknown');
    const now = new Date();
    const date = new Date(ms);
    if (Number.isNaN(date.getTime())) return t('relativeTime.unknown');
    return t('row.learned', { when: relativeWords(t, ms, now) });
}

/** One belief: its words, then its confidence and status; edit, forget and
 *  Wrong subject stay one click away on hover and keyboard focus. */
export function MemoryRow({
    entry,
    belief,
    showKind = true,
    selected,
    copies = 1,
    onSelect,
    onEdit,
    onForget,
    onReject,
    highlight = (text) => text,
}: {
    entry: main.MemoryEntry;
    belief?: AboutYouBelief;
    showKind?: boolean;
    /** Saved copies of this idea the row stands for (one card per idea). */
    copies?: number;
    selected?: boolean;
    onSelect: () => void;
    onEdit: () => void;
    onForget: () => void;
    onReject: () => void;
    highlight?: (text: string) => ReactNode;
}) {
    const { t } = useTranslation('memory');
    const label = memoryLabel(entry, t);
    const firstPoint = beliefInSecondPerson(humanMemoryText(memoryPoints(entry.content)[0]?.text ?? ''));
    const changed = entry.updatedAt > 0 ? entry.updatedAt : entry.modTime;
    const learned = belief?.firstLearnedAt ?? 0;
    const unchanged = learned > 0 && changed - learned < 60_000;
    return (
        <div className={cn(
            'group rounded-lg transition-colors',
            selected ? 'bg-surface-active' : 'hover:bg-surface-hover',
        )}>
            <button
                type="button"
                className="flex w-full items-start gap-3 rounded-lg px-2.5 pt-2.5 pb-1 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus"
                onClick={onSelect}
                aria-pressed={!!selected}
            >
                <span className="min-w-0 flex-1">
                    <span className="block text-[13px] leading-[1.45] text-ink line-clamp-3 break-words">{highlight(label)}</span>
                    {firstPoint && firstPoint !== label && (
                        <span className="mt-0.5 block text-xs leading-relaxed text-muted line-clamp-2 break-words">{highlight(firstPoint)}</span>
                    )}
                </span>
                {belief && <BeliefStatusMark belief={belief} />}
            </button>
            <div className="flex min-h-7 flex-wrap items-center gap-x-2 gap-y-1 px-2.5 pb-1.5 text-[11px] text-muted">
                {showKind && <span className="rounded-full bg-surface-active px-2 py-0.5">{t(`kinds.${memoryKind(entry)}`)}</span>}
                {changed > 0 && <span>{t(unchanged ? 'row.learned' : 'row.updated', { when: relativeWords(t, changed) })}</span>}
                {copies > 1 && <span>{t('aboutYou.review.copies', { count: copies })}</span>}
                <span className="ml-auto flex items-center gap-0.5 text-muted opacity-100 [@media(hover:hover)]:opacity-0 group-hover:opacity-100 group-focus-within:opacity-100">
                    <button type="button" className="inline-flex min-h-7 min-w-7 items-center justify-center rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus hover:bg-surface-active hover:text-ink" onClick={onEdit} aria-label={t('row.edit', { title: label })}>
                        <Pencil className="h-3 w-3" />
                    </button>
                    <button type="button" className="inline-flex min-h-7 min-w-7 items-center justify-center rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus hover:bg-error-bg hover:text-error" onClick={onForget} aria-label={t('forget.itemAction', { title: label })}>
                        <Trash2 className="h-3 w-3" />
                    </button>
                    <button type="button" className="inline-flex min-h-7 min-w-7 items-center justify-center rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus hover:bg-surface-active hover:text-ink" onClick={onReject} title={t('rejectAttribution.buttonTitle')} aria-label={t('rejectAttribution.buttonAria', { title: label })}>
                        <UserX className="h-3 w-3" />
                    </button>
                </span>
            </div>
        </div>
    );
}
