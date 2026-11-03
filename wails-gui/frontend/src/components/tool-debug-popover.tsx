import { Bug } from 'lucide-react';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { formatToolInput } from '@/lib/stream-helpers';
import { cn } from '@/lib/utils';
import type { StreamItem } from '@/types';

interface ToolDebugPopoverProps {
    item: StreamItem;
    /** Paired tool_result for this tool call, looked up by tool_use_id. */
    result?: StreamItem;
    /** Extra classes for the trigger button. */
    className?: string;
}

/**
 * Tiny Bug-icon trigger that pops out raw tool input + paired tool_result
 * + tool_use_id / run_id when clicked. Shown next to a tool call only when
 * debug mode is enabled — the regular UI keeps its human-readable summary
 * intact.
 */
export function ToolDebugPopover({ item, result, className }: ToolDebugPopoverProps) {
    return (
        <Popover>
            <PopoverTrigger asChild>
                <button
                    type="button"
                    aria-label="Show raw tool input and output"
                    className={cn(
                        'inline-flex size-5 shrink-0 items-center justify-center rounded-md text-muted/70 hover:text-ink hover:bg-ink/5 transition-colors cursor-pointer',
                        className,
                    )}
                >
                    <Bug className="size-3" />
                </button>
            </PopoverTrigger>
            <PopoverContent
                align="start"
                sideOffset={6}
                onOpenAutoFocus={(e) => e.preventDefault()}
                className="w-[min(560px,calc(100vw-32px))] max-h-[70vh] overflow-y-auto p-3 flex flex-col gap-3"
            >
                <div className="flex items-center gap-2 border-b border-border/60 pb-2">
                    <Bug className="size-3.5 text-muted shrink-0" />
                    <p className="m-0 font-semibold text-sm font-mono truncate">
                        {item.toolName ?? 'tool'}
                    </p>
                </div>
                <div>
                    <p className="m-0 mb-1 text-[10px] font-semibold uppercase tracking-wider text-muted">
                        input
                    </p>
                    <pre className="m-0 bg-code-bg text-code-fg p-2.5 rounded-md text-[11px] leading-snug overflow-x-auto whitespace-pre-wrap break-words max-h-[260px] overflow-y-auto">
                        {formatToolInput(item.toolInput)}
                    </pre>
                </div>
                <div>
                    <p className="m-0 mb-1 text-[10px] font-semibold uppercase tracking-wider text-muted">
                        tool_result
                    </p>
                    {result ? (
                        <pre className="m-0 bg-code-bg text-code-fg p-2.5 rounded-md text-[11px] leading-snug overflow-x-auto whitespace-pre-wrap break-words max-h-[260px] overflow-y-auto">
                            {result.content ?? ''}
                        </pre>
                    ) : (
                        <p className="m-0 text-[11px] text-muted italic">
                            No tool_result received yet.
                        </p>
                    )}
                </div>
                {(item.toolUseId || item.runId) && (
                    <div className="flex flex-wrap gap-x-3 gap-y-1 text-[10px] text-muted/80 font-mono border-t border-border/60 pt-2">
                        {item.toolUseId && <span>tool_use_id: {item.toolUseId}</span>}
                        {item.runId && <span>run_id: {item.runId}</span>}
                    </div>
                )}
            </PopoverContent>
        </Popover>
    );
}
