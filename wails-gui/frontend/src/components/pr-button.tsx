import { GitPullRequestArrow, ExternalLink, Loader2, RefreshCw } from 'lucide-react';
import type { PRInfo } from '@/types';
import { BrowserOpenURL } from '../../wailsjs/runtime/runtime';

interface PRButtonProps {
    prInfo: PRInfo | null;
    loading: boolean;
    createPRUrl: string | null;
    onRefresh: () => void;
}

const stateColors: Record<string, string> = {
    open: 'bg-success',
    merged: 'bg-accent',
    closed: 'bg-muted',
};

export function PRButton({ prInfo, loading, createPRUrl, onRefresh }: PRButtonProps) {
    if (loading) {
        return (
            <button
                className="mr-1 p-1.5 rounded-md text-muted transition-colors"
                style={{ '--wails-draggable': 'no-drag' } as React.CSSProperties}
                title="Loading PR info..."
                disabled
            >
                <Loader2 className="h-4 w-4 animate-spin" />
            </button>
        );
    }

    if (prInfo) {
        const isDraft = prInfo.draft;
        const dotColor = isDraft ? 'bg-warning' : (stateColors[prInfo.state] || 'bg-muted');
        const label = isDraft ? 'draft' : prInfo.state;

        return (
            <div className="flex items-center">
                <button
                    className="mr-0.5 px-2 py-1 rounded-md text-xs flex items-center gap-1.5 transition-colors text-success hover:bg-success-bg"
                    style={{ '--wails-draggable': 'no-drag' } as React.CSSProperties}
                    onClick={() => BrowserOpenURL(prInfo.url)}
                    title={prInfo.title}
                >
                    <GitPullRequestArrow className="h-3.5 w-3.5" />
                    <span>PR #{prInfo.number}</span>
                    <span className={`inline-block w-1.5 h-1.5 rounded-full ${dotColor}`} />
                    <span className="text-muted">{label}</span>
                </button>
                <button
                    className="mr-1 p-1 rounded-md text-muted hover:text-ink hover:bg-border/50 transition-colors"
                    style={{ '--wails-draggable': 'no-drag' } as React.CSSProperties}
                    onClick={onRefresh}
                    title="Refresh PR status"
                >
                    <RefreshCw className="h-3 w-3" />
                </button>
            </div>
        );
    }

    if (createPRUrl) {
        return (
            <button
                className="mr-1 px-2 py-1 rounded-md text-xs flex items-center gap-1.5 transition-colors text-muted hover:text-ink hover:bg-border/50"
                style={{ '--wails-draggable': 'no-drag' } as React.CSSProperties}
                onClick={() => BrowserOpenURL(createPRUrl)}
                title="Create a pull request"
            >
                <GitPullRequestArrow className="h-3.5 w-3.5" />
                <span>Create PR</span>
                <ExternalLink className="h-3 w-3" />
            </button>
        );
    }

    return null;
}
