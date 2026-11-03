import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { ExternalLink } from 'lucide-react';
import { SearchIcon } from '@/assets/icons/SearchIcon';
import { getToolSummary } from '@/lib/tool-icons';
import { useShowToolSteps } from '@/hooks/useShowToolSteps';
import { ToolDebugPopover } from './tool-debug-popover';
import { AnimatedEllipsis } from './animated-ellipsis';
import type { StreamItem } from '@/types';

interface ToolGroupItemProps {
    item: StreamItem;
    index: number;
    /** Paired tool_result, surfaced via the debug popover when tool steps are enabled. */
    result?: StreamItem;
    /**
     * True when this individual call has not yet produced a tool_result.
     * The chip dims slightly so the user can tell which call is still
     * mid-flight inside a multi-call group. When the placeholder's
     * `toolInput` is also empty (tool_use_start fired but tool_use hasn't
     * arrived yet) the chip renders a "preparing" indicator instead of
     * vanishing.
     */
    pending?: boolean;
}

const WEB_SEARCH_TOOL_NAMES = new Set(['WebSearch', 'web_search', 'search']);
const WEB_FETCH_TOOL_NAMES = new Set(['WebFetch', 'web_fetch']);

export function ToolGroupItem({ item, index, result, pending = false }: ToolGroupItemProps) {
    const { t } = useTranslation('chat');
    const summary = useMemo(
        () => getToolSummary(item.toolName ?? '', item.toolInput, index),
        [item.toolName, item.toolInput, index]
    );
    const [showToolSteps] = useShowToolSteps();

    // Render an explicit "preparing" placeholder while the model is still
    // streaming the tool's input JSON. Without this, the chip would be
    // hidden until tool_use lands and the user would just see "Running
    // command..." with no chip — making it look like the UI is stuck.
    if (!summary) {
        if (!pending) return null;
        return (
            <span
                className="inline-flex items-center gap-1.5 rounded-md border border-ink/10 bg-panel-warm px-2 py-1 text-xs text-muted shadow-[var(--shadow-soft)] opacity-70 animate-pulse-slow"
                title={t('tools.preparingTitle')}
            >
                <span className="truncate">
                    {item.toolName ?? t('tools.preparingFallback')}
                </span>
                <AnimatedEllipsis className="text-muted" />
            </span>
        );
    }

    const toolName = item.toolName ?? '';
    const isSearch = WEB_SEARCH_TOOL_NAMES.has(toolName);
    const isFetch = WEB_FETCH_TOOL_NAMES.has(toolName);

    return (
        <span className="inline-flex items-center gap-1">
            <span
                className={`inline-flex items-center max-w-[260px] gap-1.5 rounded-md border border-ink/10 bg-panel-warm px-2 py-1 text-xs text-ink shadow-[var(--shadow-soft)] transition-opacity duration-[var(--motion-duration-fast)] ${
                    pending ? 'opacity-60 animate-pulse-slow' : 'opacity-100'
                }`}
                title={summary}
            >
                {isSearch && (
                    <SearchIcon className="size-3 shrink-0 text-muted" />
                )}
                <span className="truncate">{summary}</span>
                {isFetch && (
                    <ExternalLink className="size-3 shrink-0 text-muted" />
                )}
            </span>
            {showToolSteps && <ToolDebugPopover item={item} result={result} />}
        </span>
    );
}
