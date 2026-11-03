import { useTranslation } from 'react-i18next';
import type { AboutYouBelief } from '@/lib/wails-shim-app';
import { cn } from '@/lib/utils';

/** Four steps, filled up to the belief's computed confidence. */
export function ConfidenceBar({ level }: { level: number }) {
    const { t } = useTranslation('memory');
    const filled = Math.max(0, Math.min(4, Math.round(level)));
    return (
        <span role="img" aria-label={t('aboutYou.confidence', { count: filled })} className="flex gap-0.5">
            {[0, 1, 2, 3].map((step) => (
                <span
                    key={step}
                    className={cn('h-2.5 w-1 rounded-[2px]', step < filled ? 'bg-accent' : 'bg-card-progress')}
                />
            ))}
        </span>
    );
}

/** The confidence bar over its status word, as on each belief row. */
export function BeliefStatusMark({ belief }: { belief: AboutYouBelief }) {
    const { t } = useTranslation('memory');
    return (
        <span className="flex flex-none flex-col items-end gap-1 pt-0.5">
            <ConfidenceBar level={belief.confidence} />
            <span className="text-[11px] leading-none text-muted">{t(`aboutYou.status.${belief.status}`)}</span>
        </span>
    );
}
