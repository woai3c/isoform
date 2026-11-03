import { useSyncExternalStore } from 'react';
import { SendMainChatProactiveDraft } from '@/lib/wails-shim-app';
import { notifyMainChatViewChanged } from '@/lib/main-chat-view';
import { onPagePrivacyWithdrawal } from '@/lib/page-privacy-withdrawal';

/** Send on a draft card waits this long with Undo before Mail gets it. */
export const MAIN_CHAT_DRAFT_SEND_HOLD_MS = 5000;

export type MainChatDraftSendStatus = 'waiting' | 'sending' | 'sent' | 'refused' | 'unknown';

export interface MainChatDraftSend {
    status: MainChatDraftSendStatus;
    /** The words that went, when the person edited the draft. */
    text: string;
    sentAt?: number;
}

type HeldSend = MainChatDraftSend & { timer?: number };

const sends = new Map<string, HeldSend>();
const listeners = new Set<() => void>();
const publish = () => listeners.forEach((listener) => listener());
const subscribe = (listener: () => void) => {
    listeners.add(listener);
    return () => { listeners.delete(listener); };
};
const set = (momentId: string, send: HeldSend) => {
    sends.set(momentId, send);
    publish();
};

let withdrawalInstalled = false;
function installWithdrawal() {
    if (withdrawalInstalled) return;
    withdrawalInstalled = true;
    // A send still in its 5 seconds never goes once the page withdraws.
    onPagePrivacyWithdrawal(() => {
        for (const [id, send] of sends) {
            if (send.status === 'waiting') {
                window.clearTimeout(send.timer);
                sends.delete(id);
            }
        }
        publish();
    });
}

/** Holds a draft's Send for 5 seconds, then hands it to Mail. A send that
 *  is waiting, going or gone is never started twice. */
export function holdMainChatDraftSend(momentId: string, text = ''): void {
    const current = sends.get(momentId);
    if (current && (current.status === 'waiting' || current.status === 'sending' || current.status === 'sent')) return;
    installWithdrawal();
    const timer = window.setTimeout(() => {
        set(momentId, { status: 'sending', text });
        void SendMainChatProactiveDraft(momentId, text).then(() => {
            set(momentId, { status: 'sent', text, sentAt: Date.now() });
            // What waits on the person changed.
            notifyMainChatViewChanged();
        }).catch((error: unknown) => {
            const reason = error instanceof Error ? error.message : String(error);
            set(momentId, { status: reason.includes('send outcome is unknown') ? 'unknown' : 'refused', text });
        });
    }, MAIN_CHAT_DRAFT_SEND_HOLD_MS);
    set(momentId, { status: 'waiting', text, timer });
}

/** Undo stops a send in its 5 seconds, before Mail gets it. */
export function undoMainChatDraftSend(momentId: string): boolean {
    const current = sends.get(momentId);
    if (current?.status !== 'waiting') return false;
    window.clearTimeout(current.timer);
    sends.delete(momentId);
    publish();
    return true;
}

export function useMainChatDraftSend(momentId: string): MainChatDraftSend | undefined {
    return useSyncExternalStore(subscribe, () => sends.get(momentId), () => undefined);
}

export function resetMainChatDraftSends() {
    for (const send of sends.values()) window.clearTimeout(send.timer);
    sends.clear();
    publish();
}
