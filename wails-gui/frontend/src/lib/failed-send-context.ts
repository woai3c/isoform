import { createContext } from 'react';

export interface FailedSendActions {
    /** Resends a kept message with its dedupe key. */
    retry: (itemId: string) => void;
    /** Puts a kept message back in the box. */
    edit: (itemId: string) => void;
    /** Whether an AI is configured; a no-AI kept message reads as an ordinary failed send once it is. */
    aiReady: boolean | null;
}

export const FailedSendContext = createContext<FailedSendActions | null>(null);
