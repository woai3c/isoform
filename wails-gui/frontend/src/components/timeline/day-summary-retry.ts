import { useCallback, useEffect, useState } from 'react';
import { refreshActivitySessionData } from './activity-session-cache';

// A failed day read is not an answer. It is retried once on its own, after
// this delay, and only then does the day say it couldn't load (AR63).
export const DAY_SUMMARY_RETRY_MS = 1500;

type DayRetry = 'idle' | 'trying' | 'gaveUp';

/** Holds the Your day card while a failed read is retried, and says so only
 *  after the retry has failed too. A saved day (hasData) never shows either. */
export function useDaySummaryRetry({ key, load, dateStr, hasData, failing }: {
    key: string;
    load: () => Promise<unknown>;
    dateStr: string;
    hasData: boolean;
    /** The read has failed and nothing is saved for the day. */
    failing: boolean;
}) {
    const [retry, setRetry] = useState<DayRetry>('idle');
    useEffect(() => { setRetry('idle'); }, [dateStr]);
    useEffect(() => { if (hasData) setRetry('idle'); }, [hasData]);

    const retryNow = useCallback(() => {
        setRetry('trying');
        void refreshActivitySessionData(key, load).finally(() => {
            setRetry((current) => (current === 'trying' ? 'gaveUp' : current));
        });
    }, [key, load]);

    useEffect(() => {
        if (!failing || retry !== 'idle') return;
        const timer = setTimeout(retryNow, DAY_SUMMARY_RETRY_MS);
        return () => clearTimeout(timer);
    }, [failing, retry, retryNow]);

    return {
        /** Waiting for the first retry, or while it runs: hold the placeholder. */
        pending: failing && retry !== 'gaveUp',
        /** The retry failed too. */
        failed: failing && retry === 'gaveUp',
        retryNow,
    };
}
