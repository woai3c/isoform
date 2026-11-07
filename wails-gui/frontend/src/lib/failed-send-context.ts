import { createContext } from 'react';

export interface FailedSendActions {
    /** Resends a kept message with its dedupe key. */
    retry: (itemId: string) => void;
    /** Puts a kept message back in the box. */
    edit: (itemId: string) => void;
    /** R75: sends the chat's most recent message again, for a signed-out answer. */
    resendLast: () => void;
    /** R75: the one row that offers Send again (the chat's latest, a signed-out answer); null otherwise. */
    resendItemId: string | null;
    /** Whether an AI is configured; a no-AI kept message reads as an ordinary failed send once it is. */
    aiReady: boolean | null;
}

export const FailedSendContext = createContext<FailedSendActions | null>(null);
