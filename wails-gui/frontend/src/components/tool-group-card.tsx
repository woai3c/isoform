import { ToolGroupRow } from './tool-group-row';
import type { StreamItem } from '@/types';
import type { ToolResultsMap } from './grouped-stream-list';

interface ToolGroupCardProps {
    toolName: string;
    items: StreamItem[];
    defaultCollapsed: boolean;
    resultsByToolUseId?: ToolResultsMap;
    streaming?: boolean;
}

export function ToolGroupCard({
    toolName,
    items,
    resultsByToolUseId,
    streaming,
}: ToolGroupCardProps) {
    return (
        <ToolGroupRow
            toolName={toolName}
            items={items}
            resultsByToolUseId={resultsByToolUseId}
            streaming={streaming}
        />
    );
}
