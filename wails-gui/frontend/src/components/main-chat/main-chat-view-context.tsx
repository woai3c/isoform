import { createContext, useContext } from 'react';
import type { StreamItem } from '@/types';

/** What the main chat's stream needs from its view (RC 2 D1): the day the
 *  computer is on, whether routine updates are hidden, topic names for the
 *  tags on Isoform's rows, and prepared chips under the latest answer. Only
 *  the main chat provides it; every other chat renders as before. */
export interface MainChatStreamView {
    /** The same stream can open item details beside Today or Chat. */
    surface?: 'chat' | 'today';
    /** Today as "YYYY-MM-DD" in this computer's time zone. */
    today: string;
    hideRoutine: boolean;
    topicNames: ReadonlyMap<string, string>;
    chips: Readonly<Record<string, string[]>>;
    /** A chip's words: Chat and Today's panel send them at once (K4, chat-review v1.0.3 #3). */
    onFillChip?: (text: string) => void | boolean | Promise<boolean>;
    /** Chat K4: chips send, so an answered question folds to "You answered: … · Change". */
    chipsSend?: boolean;
    firstReadStarter?: { itemId: string; items: StreamItem[] | null };
}

export const MainChatViewContext = createContext<MainChatStreamView | null>(null);

export function useMainChatStreamView(): MainChatStreamView | null {
    return useContext(MainChatViewContext);
}
