import { useEffect } from 'react';
import { markLaunchPaint } from '@/lib/launch-perf';
import { useMainChatWaiting } from '@/lib/main-chat-waiting-store';

/** The frame stays current even while Main chat is not mounted. */
export function useMainChatWaitingCount() {
    const state = useMainChatWaiting();
    useEffect(() => {
        if (state.ready) markLaunchPaint('overview_rendered');
    }, [state.ready]);
    return state.ready ? state.count : null;
}
