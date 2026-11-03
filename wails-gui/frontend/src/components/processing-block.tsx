import { useState, useEffect, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronDown } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { StreamCard } from './stream-card';
import { ThinkingCard } from './thinking-card';
import { ToolGroupCard } from './tool-group-card';
import type { GroupedStreamItem, ExecutorType, MainChatReportTarget } from '@/lib/stream-helpers';
import type { ToolResultsMap } from './grouped-stream-list';

function formatElapsed(seconds: number): string {
    if (seconds < 60) return `${seconds}s`;
    const m = Math.floor(seconds / 60);
    const s = seconds % 60;
    return `${m}m ${s}s`;
}

/** Compute elapsed seconds from a persisted start timestamp.
 *  `startedAt` lives in the Session state so it survives session/workspace switches. */
function useElapsedTimer(streaming: boolean, startedAt: number) {
    const [elapsed, setElapsed] = useState(() =>
        startedAt > 0 ? Math.floor((Date.now() - startedAt) / 1000) : 0
    );

    useEffect(() => {
        if (streaming && startedAt > 0) {
            setElapsed(Math.floor((Date.now() - startedAt) / 1000));
            const id = setInterval(() => {
                setElapsed(Math.floor((Date.now() - startedAt) / 1000));
            }, 1000);
            return () => clearInterval(id);
        }
        // Streaming stopped – freeze at final value
        if (startedAt > 0) {
            setElapsed(Math.floor((Date.now() - startedAt) / 1000));
        }
        return undefined;
    }, [streaming, startedAt]);

    return elapsed;
}

/** System messages to filter out of the processing block */
const FILTERED_MESSAGES = [
    'under-development features enabled',
    'suppress_unstable_features_warning',
];

function isFilteredSystemMessage(g: GroupedStreamItem): boolean {
    if (g.type !== 'single') return false;
    if (g.item.kind !== 'system') return false;
    const text = (g.item.content ?? '').toLowerCase();
    return FILTERED_MESSAGES.some((msg) => text.includes(msg));
}

interface ProcessingBlockProps {
    items: GroupedStreamItem[];
    defaultCollapsed: boolean;
    /** Completed main-chat work lives with the answer, behind its details. */
    answerDetails?: boolean;
    executorType?: ExecutorType;
    streaming?: boolean;
    streamingStartedAt?: number;
    /** tool_use_id → matching tool_result, surfaced when debug mode is on. */
    resultsByToolUseId?: ToolResultsMap;
    /** Opens what a main-chat report line or a side-chat origin points at. */
    onOpenReportTarget?: (target: MainChatReportTarget) => void;
    /** "Use as my message" on a texted reply: puts its words in the composer. */
    onUseTextedReply?: (text: string) => void;
}

export function ProcessingBlock({
    items,
    defaultCollapsed,
    answerDetails = false,
    executorType = 'claude',
    streaming = false,
    streamingStartedAt = 0,
    resultsByToolUseId,
    onOpenReportTarget,
    onUseTextedReply,
}: ProcessingBlockProps) {
    const { t } = useTranslation('chat');
    const [expanded, setExpanded] = useState(!defaultCollapsed);
    const elapsed = useElapsedTimer(streaming, streamingStartedAt);

    useEffect(() => {
        if (defaultCollapsed) setExpanded(false);
    }, [defaultCollapsed]);

    const visibleItems = useMemo(
        () => items.filter((g) => !isFilteredSystemMessage(g)),
        [items]
    );

    const summary = useMemo(() => {
        let stepCount = 0;
        const toolNames = new Set<string>();

        for (const g of visibleItems) {
            if (g.type === 'tool-group') {
                stepCount += g.items.length;
                for (const name of g.toolName.split(', ')) {
                    toolNames.add(name);
                }
            } else if (g.type === 'thinking-group') {
                stepCount += 1 + g.tools.length;
                for (const t of g.tools) {
                    toolNames.add(t.toolName ?? 'tool');
                }
            } else {
                stepCount += 1;
            }
        }

        const names = [...toolNames].join(', ');
        return { stepCount, names };
    }, [visibleItems]);

    if (visibleItems.length === 0) return null;

    return (
        <div className="flex flex-col gap-2.5">
            {/* Header — inline disclosure: text + chevron, no surface */}
            <Button
                type="button"
                variant="quiet"
                size="sm"
                aria-expanded={expanded}
                onClick={() => setExpanded((prev) => !prev)}
                className="h-auto min-h-8 self-start gap-1.5 max-w-full px-0 text-xs font-normal tabular-nums"
            >
                {answerDetails ? t('tools.answerDetails') : <>
                    <span className="shrink-0">{t('tools.steps', { count: summary.stepCount })}</span>
                    {summary.names && <span className="truncate min-w-0">{summary.names}</span>}
                    {!expanded && (streaming || elapsed > 0) && <span className="shrink-0">{formatElapsed(elapsed)}</span>}
                </>}
                <ChevronDown
                    aria-hidden
                    className={`size-3 shrink-0 ml-0.5 opacity-60 transition-transform duration-[var(--motion-duration-slow)] ease-[var(--motion-ease-in-out)] ${
                        expanded ? 'rotate-0' : '-rotate-90'
                    }`}
                />
            </Button>

            {/* Body — dashed outlined container, animated open/close */}
            <div
                aria-hidden={!expanded}
                className="grid transition-[grid-template-rows,opacity] duration-[var(--motion-duration-slow)] ease-[var(--motion-ease-in-out)]"
                style={{
                    gridTemplateRows: expanded ? '1fr' : '0fr',
                    opacity: expanded ? 1 : 0,
                }}
            >
                <div className="min-h-0 overflow-hidden">
                    <div className="rounded-xl border border-border/70 px-4 py-3.5 flex flex-col gap-4">
                        {answerDetails && <p className="text-xs text-muted">
                            {t('tools.steps', { count: summary.stepCount })}
                            {summary.names && ` · ${summary.names}`}
                        </p>}
                        {visibleItems.map((g) => {
                            if (g.type === 'thinking-group') {
                                return (
                                    <ThinkingCard
                                        key={g.thinkingItem.id}
                                        thinkingItem={g.thinkingItem}
                                        tools={g.tools}
                                        defaultCollapsed={false}
                                        streaming={streaming}
                                        resultsByToolUseId={resultsByToolUseId}
                                    />
                                );
                            }
                            if (g.type === 'tool-group') {
                                return (
                                    <ToolGroupCard
                                        key={g.items[0].id}
                                        toolName={g.toolName}
                                        items={g.items}
                                        defaultCollapsed={false}
                                        resultsByToolUseId={resultsByToolUseId}
                                        streaming={streaming}
                                    />
                                );
                            }
                            return (
                                <StreamCard
                                    key={g.item.id}
                                    item={g.item}
                                    executorType={executorType}
                                    compact
                                    resultsByToolUseId={resultsByToolUseId}
                                    onOpenReportTarget={onOpenReportTarget}
                                    onUseTextedReply={onUseTextedReply}
                                />
                            );
                        })}
                        {/* Tail: a status label + separator dot + elapsed
                            timer. While streaming it reads "Working" (with the
                            shimmer); once the run finishes it flips to
                            "Completed" and the shimmer drops, so the duration
                            keeps its label instead of becoming a bare number.
                            The timer lives here while the block is expanded (the
                            header drops its own elapsed chip when expanded) so
                            it's never shown in both places; the body's clip
                            hides this while collapsed, where the header chip
                            takes over. Label, dot, and timer are separate spans
                            so the shimmer applies cleanly to the text only. */}
                        {(streaming || elapsed > 0) && (
                            <div className="flex items-center gap-1.5 text-xs text-muted/80">
                                <span
                                    className={
                                        streaming ? 'working-shimmer-text' : undefined
                                    }
                                >
                                    {streaming
                                        ? t('tools.workingTail')
                                        : t('tools.completedTail')}
                                </span>
                                {elapsed > 0 && (
                                    <>
                                        <span
                                            aria-hidden
                                            className={`size-[3px] rounded-full shrink-0 ${
                                                streaming
                                                    ? 'working-shimmer-dot'
                                                    : 'bg-muted/40'
                                            }`}
                                        />
                                        <span
                                            className={`tabular-nums ${
                                                streaming ? 'working-shimmer-text' : ''
                                            }`}
                                        >
                                            {formatElapsed(elapsed)}
                                        </span>
                                    </>
                                )}
                            </div>
                        )}
                    </div>
                </div>
            </div>
        </div>
    );
}
