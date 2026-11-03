import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useCaptureCounts } from '@/hooks/useCaptureCounts';
import { describeTodayCapture } from '@/components/today/TodayCaptureLine';

/**
 * "Today Isoform saw 412 screens and heard speech during 38 minutes", on
 * today's Activity. It moved here from Today (snappy-and-first-run-1.0.md,
 * section 4): it is a count with nothing to act on, and it reads the large
 * activity store, so it is read after the page has painted, never on the
 * click that opened it. A cached answer shows at once; until there is one the
 * line is absent rather than a placeholder.
 */
export function ActivityCaptureLine() {
    const { t, i18n } = useTranslation('today');
    const tr = (key: string, options?: Record<string, unknown>) => t(key as never, options as never) as unknown as string;
    const [painted, setPainted] = useState(false);
    useEffect(() => {
        const frame = requestAnimationFrame(() => setPainted(true));
        return () => cancelAnimationFrame(frame);
    }, []);
    const { counts } = useCaptureCounts(painted);
    const text = describeTodayCapture(counts, tr, i18n.language);
    if (!text) return null;
    return (
        <p data-testid="activity-capture" className="px-1 text-[12px] leading-snug text-muted">
            {text}
        </p>
    );
}
