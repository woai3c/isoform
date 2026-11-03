import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { GitCompare, Languages } from 'lucide-react';
import {
    Dialog,
    DialogCloseButton,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { ScrollArea } from '@/components/ui/scroll-area';
import { EntryDetail, EntryMetadataList, splitHeaderAndBody } from '@/components/entry-content';
import { cn } from '@/lib/utils';
import { lineDiff, diffStats, pairDiff, type DiffLine } from '@/lib/line-diff';
import { notifyToast } from '@/lib/notify';
import { humanMemoryText, memoryLabel, memoryPoints } from '@/lib/memory-content';
import {
    GetMemoryVersion,
    ListMemoryVersions,
    TranslateMemoryEntryToCurrentLanguage,
    TranslateMemoryVersionToCurrentLanguage,
} from '../../wailsjs/go/main/App';
import type { main } from '../../wailsjs/go/models';

function bestTime(entry: main.MemoryEntry): number {
    return entry.updatedAt && entry.updatedAt > 0 ? entry.updatedAt : entry.modTime;
}

/** A snapshot is a point-in-time capture, so it keeps its date and time. */
function formatVersionTimestamp(ms: number, locale: string): string {
    return new Date(ms).toLocaleString(locale, { dateStyle: 'medium', timeStyle: 'short' });
}

function pointsText(content: string): string {
    return memoryPoints(content).map((point) => humanMemoryText(point.text)).join('\n');
}

/**
 * Version history for one memory, from the detail's More menu: the live
 * version and every saved snapshot, a preview, a bullet-level Compare
 * against the live version, and Translate for the chosen version.
 */
export function MemoryHistoryDialog({
    entry,
    onOpenChange,
    onTranslated,
    onOpenActivitySession,
}: {
    entry: main.MemoryEntry | null;
    onOpenChange: (open: boolean) => void;
    onTranslated: (saved: main.MemoryEntry) => void;
    onOpenActivitySession?: (sessionId: string) => void;
}) {
    const { t, i18n } = useTranslation(['memory', 'common']);
    const [versions, setVersions] = useState<VersionListItem[]>([]);
    const [selectedTs, setSelectedTs] = useState<SelectedVersion>(null);
    const [versionContent, setVersionContent] = useState('');
    const [loadingList, setLoadingList] = useState(false);
    const [loadingContent, setLoadingContent] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [compare, setCompare] = useState(false);
    const [translating, setTranslating] = useState(false);

    // Load the version list when the dialog opens for an entry and
    // auto-select the newest snapshot so the preview isn't empty.
    useEffect(() => {
        setVersions([]);
        setSelectedTs(null);
        setVersionContent('');
        setError(null);
        setCompare(false);
        if (!entry) return;
        let cancelled = false;
        setLoadingList(true);
        ListMemoryVersions(entry.id)
            .then((result: VersionListItem[] | null) => {
                if (cancelled) return;
                const list = result ?? [];
                setVersions(list);
                setSelectedTs(list.length > 0 ? list[0].timestampMs : 'current');
            })
            .catch((err: unknown) => {
                if (!cancelled) setError(err instanceof Error ? err.message : String(err));
            })
            .finally(() => {
                if (!cancelled) setLoadingList(false);
            });
        return () => {
            cancelled = true;
        };
    }, [entry?.id]);

    // `'current'` skips the backend call: the live memory is already in
    // `entry.content`. Numeric timestamps read that snapshot.
    useEffect(() => {
        if (!entry || selectedTs === null) {
            setVersionContent('');
            setLoadingContent(false);
            return;
        }
        if (selectedTs === 'current') {
            setVersionContent(entry.content);
            setLoadingContent(false);
            setError(null);
            return;
        }
        let cancelled = false;
        setLoadingContent(true);
        setError(null);
        GetMemoryVersion(entry.id, selectedTs)
            .then((content: string) => {
                if (!cancelled) setVersionContent(content);
            })
            .catch((err: unknown) => {
                if (cancelled) return;
                setVersionContent('');
                setError(err instanceof Error ? err.message : String(err));
            })
            .finally(() => {
                if (!cancelled) setLoadingContent(false);
            });
        return () => {
            cancelled = true;
        };
    }, [entry?.id, entry?.content, selectedTs]);

    const translate = () => {
        if (!entry || translating || selectedTs === null) return;
        setTranslating(true);
        const run = selectedTs === 'current'
            ? TranslateMemoryEntryToCurrentLanguage(entry.id)
            : TranslateMemoryVersionToCurrentLanguage(entry.id, selectedTs);
        run
            .then((saved) => {
                onTranslated(saved);
                onOpenChange(false);
            })
            .catch((err: unknown) => {
                notifyToast(
                    t('toasts.translateFailed.title') as string,
                    err instanceof Error ? err.message : String(err),
                    'error',
                );
            })
            .finally(() => setTranslating(false));
    };

    const rowClass = (on: boolean) => cn(
        'w-full rounded-md px-3 py-2 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus',
        on ? 'bg-surface-active text-ink font-medium' : 'text-muted hover:bg-surface-hover hover:text-ink',
    );

    return (
        <Dialog open={entry !== null} onOpenChange={(open) => { if (!translating) onOpenChange(open); }}>
            <DialogContent className="w-[min(960px,90vw)] h-[80vh]" onKeyDown={(event) => event.stopPropagation()}>
                <DialogHeader>
                    <div className="min-w-0">
                        <DialogTitle>{t('header.versionHistory')}</DialogTitle>
                        <DialogDescription className="mt-1 truncate">{entry ? memoryLabel(entry, t) : ''}</DialogDescription>
                    </div>
                    <div className="flex shrink-0 items-center gap-2">
                        <Button
                            size="sm"
                            variant="outline"
                            onClick={translate}
                            disabled={translating || selectedTs === null}
                            title={t('header.translateSelectedTooltip') as string}
                        >
                            <Languages className={cn('h-3 w-3', translating && 'motion-safe:animate-pulse')} />
                            {translating ? t('header.translating') : t('header.translate')}
                        </Button>
                        <Button
                            size="sm"
                            variant="outline"
                            className={cn(compare && 'bg-surface-active text-ink')}
                            aria-pressed={compare}
                            onClick={() => setCompare((value) => !value)}
                            disabled={selectedTs === null || selectedTs === 'current'}
                            title={(selectedTs === 'current' ? t('header.compareDisabledTooltip') : t('header.compareTooltip')) as string}
                        >
                            <GitCompare className="h-3 w-3" />
                            {t('header.compare')}
                        </Button>
                        <DialogCloseButton disabled={translating} aria-label={t('common:close')} />
                    </div>
                </DialogHeader>
                <div className="flex min-h-0 flex-1">
                    <ScrollArea className="w-56 shrink-0 border-r border-border">
                        <div className="space-y-0.5 p-2">
                            {entry && (
                                <button
                                    type="button"
                                    className={rowClass(selectedTs === 'current')}
                                    onClick={() => {
                                        setSelectedTs('current');
                                        // Compare is meaningless when both sides are the live content.
                                        setCompare(false);
                                    }}
                                >
                                    <span className="block text-xs tabular-nums">{formatVersionTimestamp(bestTime(entry), i18n.language)}</span>
                                    <span className="block text-[10px] uppercase tracking-wide text-muted">{t('sidebar.current')}</span>
                                </button>
                            )}
                            {loadingList ? (
                                <p role="status" className="px-3 py-4 text-center text-xs text-muted">{t('sidebar.loading')}</p>
                            ) : versions.length === 0 ? (
                                <p className="px-3 py-6 text-center text-xs leading-relaxed text-muted">{t('sidebar.noHistoryYet')}</p>
                            ) : (
                                versions.map((version) => (
                                    <button
                                        key={version.timestampMs}
                                        type="button"
                                        className={rowClass(selectedTs === version.timestampMs)}
                                        onClick={() => setSelectedTs(version.timestampMs)}
                                    >
                                        <span className="block text-xs tabular-nums">{formatVersionTimestamp(version.timestampMs, i18n.language)}</span>
                                    </button>
                                ))
                            )}
                        </div>
                    </ScrollArea>
                    <div className="flex min-w-0 flex-1 flex-col">
                        {error && (
                            <p role="alert" className="m-0 border-b border-border px-6 py-2 text-xs text-error">{error}</p>
                        )}
                        {loadingContent ? (
                            <div role="status" className="flex flex-1 items-center justify-center text-xs text-muted">{t('history.loadingSnapshot')}</div>
                        ) : selectedTs === null || !entry ? (
                            <div className="flex flex-1 items-center justify-center text-xs text-muted">
                                {versions.length === 0 ? t('history.noSnapshotsYet') : t('history.selectSnapshotToPreview')}
                            </div>
                        ) : (
                            <ScrollArea className="flex-1">
                                <div className="px-6 py-5">
                                    {compare ? (
                                        <MemoryDiffView
                                            snapshotContent={pointsText(versionContent)}
                                            baseContent={pointsText(entry.content)}
                                            onOpenActivitySession={onOpenActivitySession}
                                        />
                                    ) : (
                                        <EntryDetail
                                            content={pointsText(versionContent)}
                                            onOpenActivitySession={onOpenActivitySession}
                                            compactSessions
                                            collapsibleSessions
                                        />
                                    )}
                                </div>
                            </ScrollArea>
                        )}
                    </div>
                </div>
            </DialogContent>
        </Dialog>
    );
}

interface VersionListItem {
    timestampMs: number;
    sizeBytes: number;
}

/**
 * Version identifier used across the history view: the string literal
 * `'current'` for the live memory content, a numeric `timestampMs` for a
 * specific historical snapshot, or `null` when nothing is selected.
 */
type SelectedVersion = number | 'current' | null;

/**
 * Side-by-stack view rendered when the user clicks "Compare" in version
 * history. Shows the snapshot's Metadata card verbatim, then a Diff card
 * containing a bullet-level diff of the snapshot's body vs the live
 * memory's body — `+` lines are what the live memory added since the
 * snapshot, `−` lines are what the snapshot had but got removed.
 *
 * We diff body-only because frontmatter (Time/Source/Dimension/Apps/
 * Activity Session) changes on every save and would dominate the diff
 * with noise users don't care about.
 */
function MemoryDiffView({
    snapshotContent,
    baseContent,
    onOpenActivitySession,
}: {
    snapshotContent: string;
    baseContent: string;
    onOpenActivitySession?: (sessionId: string) => void;
}) {
    const { t } = useTranslation('memory');
    const { body: snapshotBody } = splitHeaderAndBody(snapshotContent);
    const { body: baseBody } = splitHeaderAndBody(baseContent);
    const diffs = useMemo(
        () => lineDiff(snapshotBody, baseBody),
        [snapshotBody, baseBody],
    );
    const stats = useMemo(() => diffStats(diffs), [diffs]);
    const pairs = useMemo(() => pairDiff(diffs), [diffs]);
    const hasChanges = stats.added > 0 || stats.removed > 0;

    return (
        <div className="space-y-3">
            <div className="rounded-lg border border-border/50 bg-panel/50 px-6 py-5">
                <h2 className="m-0 mb-2.5 text-base font-semibold leading-snug text-ink">
                    {t('history.metadata')}
                </h2>
                <EntryMetadataList
                    content={snapshotContent}
                    onOpenActivitySession={onOpenActivitySession}
                    compactSessions
                    noDivider
                    collapsibleSessions
                />
            </div>
            <div className="rounded-lg border border-border/50 bg-panel/50 overflow-hidden">
                <div className="flex items-baseline gap-2 px-6 pt-5 pb-3">
                    <h2 className="m-0 text-base font-semibold leading-snug text-ink">
                        {t('history.diff')}
                    </h2>
                    {hasChanges && (
                        <span className="ml-auto flex items-baseline gap-1.5 text-[11px] tabular-nums">
                            <span className="text-success">+{stats.added}</span>
                            <span className="text-error">−{stats.removed}</span>
                        </span>
                    )}
                </div>
                {hasChanges ? (
                    <div className="border-t border-border/40">
                        <div className="flex bg-panel/30 border-b border-border/40">
                            <div className="flex-1 px-4 py-2 text-[10px] font-medium text-muted uppercase tracking-wide">
                                {t('history.snapshot')}
                            </div>
                            <div className="w-px bg-border/40" />
                            <div className="flex-1 px-4 py-2 text-[10px] font-medium text-muted uppercase tracking-wide">
                                {t('history.current')}
                            </div>
                        </div>
                        <div>
                            {pairs.map((pair, idx) => (
                                <div key={idx} className="flex">
                                    <DiffCell side="left" line={pair.left} />
                                    <div className="w-px bg-border/40 shrink-0" />
                                    <DiffCell side="right" line={pair.right} />
                                </div>
                            ))}
                        </div>
                    </div>
                ) : (
                    <p className="px-6 pb-5 text-xs text-muted italic">
                        {t('history.noChanges')}
                    </p>
                )}
            </div>
        </div>
    );
}

/**
 * One side of a split-view diff row. `undefined` `line` renders an empty
 * gutter so the opposing cell still lines up. Background tinting
 * depends on both `side` and `op`: a remove on the left tints red (what
 * the snapshot had), an add on the right tints green (what the live
 * version gained). Equal cells stay neutral.
 */
function DiffCell({ side, line }: { side: 'left' | 'right'; line?: DiffLine }) {
    if (!line) {
        return <div className="flex-1 min-w-0 px-4 py-1 bg-panel/20" />;
    }
    const { op, text } = line;
    const isLeftRemove = side === 'left' && op === 'remove';
    const isRightAdd = side === 'right' && op === 'add';
    const bgClass = isLeftRemove
        ? 'bg-error-bg/40'
        : isRightAdd
            ? 'bg-success-bg/40'
            : '';
    const textClass = isLeftRemove
        ? 'text-error'
        : isRightAdd
            ? 'text-success'
            : 'text-ink';

    const isEmpty = text.trim() === '';
    let content: ReactNode;
    if (isEmpty) {
        content = <span>&nbsp;</span>;
    } else if (text.startsWith('## ')) {
        content = <span className="font-semibold">{text.slice(3)}</span>;
    } else if (text.startsWith('# ')) {
        content = <span className="font-bold">{text.slice(2)}</span>;
    } else if (text.startsWith('- ')) {
        content = (
            <>
                <span className="text-muted/70 mr-1.5">•</span>
                {text.slice(2)}
            </>
        );
    } else {
        content = text;
    }

    return (
        <div className={cn('flex-1 min-w-0 px-4 py-1 break-words text-xs leading-relaxed', bgClass, textClass)}>
            {content}
        </div>
    );
}
