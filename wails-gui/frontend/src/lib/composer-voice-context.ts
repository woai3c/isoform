import { createContext } from 'react';
import type { VoiceInputState } from '@/hooks/useVoiceInput';

/** The computer's one voice input, shared by every box that offers the mic:
 *  the Chat page's composer and Today's panel (chat-review v1.0.3 #4). */
export interface ComposerVoice {
    state: VoiceInputState;
    error: string | null;
    needsPermission: boolean;
    start: () => void | Promise<void>;
    stop: () => Promise<string>;
    openSettings?: () => void | Promise<void>;
}

export const ComposerVoiceContext = createContext<ComposerVoice | null>(null);
