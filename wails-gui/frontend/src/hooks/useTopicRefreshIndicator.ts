import { useEffect, useState } from 'react';

/** A pending publication gets one delayed indicator for its whole lifetime. */
export function useTopicRefreshIndicator(pending: boolean) {
    const [visible, setVisible] = useState(false);
    useEffect(() => {
        if (!pending) { setVisible(false); return; }
        const timer = window.setTimeout(() => setVisible(true), 1000);
        return () => window.clearTimeout(timer);
    }, [pending]);
    return pending && visible;
}
