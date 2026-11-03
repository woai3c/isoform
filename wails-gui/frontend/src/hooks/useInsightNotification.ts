import { useEffect, useRef } from 'react';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { notifyImportant } from '@/lib/notify';
import i18n from '@/i18n';

interface InsightEvent {
    kind: 'knowledge' | 'memory';
    id: string;
    title: string;
    source: string;
}

interface UseInsightNotificationOptions {
    onOpenKnowledge?: (id: string) => void;
    onOpenMemory?: (id: string) => void;
}

/**
 * Listens for backend "insight:saved" events (new knowledge or memory created
 * by the generator) and delivers a desktop notification via notifyImportant.
 *
 * The notification has a "View" button that calls the appropriate handler with
 * the entry id, so the page can deep-link to the specific entry. The desktop
 * (OS) notification carries the same id as `clickData` in the form
 * `<kind>:<id>` so the App-level router can dispatch the click to the right
 * page.
 */
export function useInsightNotification(options: UseInsightNotificationOptions = {}) {
    // Stabilize callbacks via refs so the EventsOn subscription is registered
    // once and never torn down / re-created when the parent re-renders.
    const onOpenKnowledgeRef = useRef(options.onOpenKnowledge);
    onOpenKnowledgeRef.current = options.onOpenKnowledge;
    const onOpenMemoryRef = useRef(options.onOpenMemory);
    onOpenMemoryRef.current = options.onOpenMemory;

    useEffect(() => {
        const cleanup = EventsOn('insight:saved', (event: InsightEvent) => {
            // Meeting notes from a recorded call arrive silently; the
            // How you do things page still reloads on the same event.
            if (event.source === 'meeting') return;
            const label = i18n.t(event.kind === 'knowledge' ? 'app:insightNotification.knowledge' : 'app:insightNotification.memory');
            const handler = event.kind === 'knowledge' ? onOpenKnowledgeRef.current : onOpenMemoryRef.current;
            const action = handler ? { label: i18n.t('app:insightNotification.view'), onClick: () => handler(event.id) } : undefined;
            const clickData = `${event.kind}:${event.id}`;
            notifyImportant(label, i18n.t('app:insightNotification.body'), 'success', undefined, action, clickData);
        });
        return cleanup;
    }, []);
}
