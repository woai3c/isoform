import { Check, X } from 'lucide-react';
import { MarkdownContent } from '@/components/markdown-content';
import { cn } from '@/lib/utils';

interface PlanReviewCardProps {
    planContent?: string;
    onApprove: () => void;
    onReject: () => void;
    wideMode?: boolean;
}

export function PlanReviewCard({
    planContent,
    onApprove,
    onReject,
    wideMode = false,
}: PlanReviewCardProps) {
    return (
        <div className={`mx-6 mb-4 self-center w-full rounded-xl border border-accent-2/30 bg-panel overflow-hidden${wideMode ? "" : " max-w-3xl"}`}>
            <div className="px-4 py-2.5 border-b border-accent-2/20 flex items-center justify-between">
                <span className="text-xs font-semibold text-accent-2">
                    Plan ready for review
                </span>
            </div>

            {planContent ? (
                <div className="px-4 py-3 max-h-[400px] overflow-y-auto">
                    <MarkdownContent content={planContent} />
                </div>
            ) : (
                <div className="px-4 py-3">
                    <p className="text-xs text-muted">
                        The agent has finished planning. Approve to start execution, or reject to continue planning.
                    </p>
                </div>
            )}

            <div className="px-4 py-3 border-t border-accent-2/20 flex items-center gap-2">
                <button
                    className={cn(
                        'flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-semibold transition-colors cursor-pointer',
                        'patina-verdigris bg-solid-btn-bg text-solid-btn-fg shadow-[var(--shadow-solid-btn)] hover:opacity-90'
                    )}
                    onClick={onApprove}
                >
                    <Check className="size-3.5" />
                    Approve & Execute
                </button>
                <button
                    className={cn(
                        'flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-xs font-semibold transition-colors cursor-pointer',
                        'bg-transparent text-muted border border-border hover:text-ink hover:bg-ink/[0.04]'
                    )}
                    onClick={onReject}
                >
                    <X className="size-3.5" />
                    Reject
                </button>
            </div>
        </div>
    );
}
