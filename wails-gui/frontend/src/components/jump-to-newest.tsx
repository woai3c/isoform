import { ArrowDown } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/utils';
import { Button } from '@/components/ui/button';

/** Only the wrapper floats over the stream; the shared small Button keeps
 * its normal size and radius. The panel uses the same control with a count.
 * Composer offsets include its 1px border to measure from the outer edge. */
export function JumpToNewest({ count, visible, onJump, compact = false, aboveComposer = false }: {
    count: number;
    visible: boolean;
    onJump: () => void;
    compact?: boolean;
    aboveComposer?: boolean;
}) {
    const { t } = useTranslation('chat');
    if (!visible) return null;
    const label = count > 0 ? t('stream.newReplies', { count }) : t('stream.jumpToNewest');
    return (
        <div className={cn('absolute left-1/2 z-10 -translate-x-1/2 rounded-lg border border-border bg-panel shadow-soft', aboveComposer ? 'bottom-[calc(100%+1px)] mb-3' : 'bottom-3')}>
            <Button size="sm" variant="quiet" className="text-ink" onClick={onJump} aria-label={label} title={label} data-testid="jump-to-newest">
                <ArrowDown className="size-3.5" aria-hidden />
                {count > 0 && <span>{compact ? t('stream.newRepliesShort', { count }) : label}</span>}
            </Button>
        </div>
    );
}
