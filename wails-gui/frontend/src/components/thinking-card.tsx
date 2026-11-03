import { MarkdownContent } from './markdown-content';
import { ToolGroupRow } from './tool-group-row';
import type { StreamItem } from '@/types';
import type { ToolResultsMap } from './grouped-stream-list';

interface ThinkingCardProps {
    thinkingItem: StreamItem;
    tools: StreamItem[];
    defaultCollapsed: boolean;
    streaming?: boolean;
    resultsByToolUseId?: ToolResultsMap;
}

export function ThinkingCard({
    thinkingItem,
    tools,
    streaming = false,
    resultsByToolUseId,
}: ThinkingCardProps) {
    // The backend sends a system "Thinking..." placeholder; show a proper label instead.
    const isPlaceholder =
        thinkingItem.kind === 'system' &&
        thinkingItem.content?.trim().toLowerCase().startsWith('thinking');
    const headerLabel = streaming ? 'Thinking...' : 'Thought';

    return (
        <div>
            {isPlaceholder ? (
                <p className="m-0 text-sm font-medium text-muted/80">{headerLabel}</p>
            ) : (
                <div className="select-text text-[13.5px] leading-relaxed text-ink/85">
                    <MarkdownContent content={thinkingItem.content ?? ''} />
                </div>
            )}

            {tools.length > 0 && (
                <div className="mt-2 flex flex-col gap-2">
                    {tools.map((tool) => (
                        <ToolGroupRow
                            key={tool.id}
                            toolName={tool.toolName ?? 'tool'}
                            items={[tool]}
                            resultsByToolUseId={resultsByToolUseId}
                            streaming={streaming}
                        />
                    ))}
                </div>
            )}
        </div>
    );
}
