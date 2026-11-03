import { useSyncExternalStore } from 'react';
import { ResolveMainChatProactiveAction } from '@/lib/wails-shim-app';
import { notifyMainChatViewChanged } from '@/lib/main-chat-view';
import { onPagePrivacyWithdrawal } from '@/lib/page-privacy-withdrawal';

/** Do it, Not now, Mute and a playbook offer show their result with Undo
 *  this long before the computer acts (§5). */
export const MAIN_CHAT_HELD_ACTION_MS = 5000;

/** The card actions that wait with Undo. */
export const MAIN_CHAT_HELD_ACTIONS = new Set(['do_once', 'not_now', 'mute_kind', 'add_to_upcoming']);

export type MainChatHeldActionStatus = 'waiting' | 'acting' | 'done' | 'failed';

export interface MainChatHeldAction {
    action: string;
    status: MainChatHeldActionStatus;
    /** The card's state once the computer acted. */
    state?: string;
}

type Held = MainChatHeldAction & { timer?: number };

const held = new Map<string, Held>();
const listeners = new Set<() => void>();
const publish = () => listeners.forEach((listener) => listener());
const subscribe = (listener: () => void) => {
    listeners.add(listener);
    return () => { listeners.delete(listener); };
};
const set = (momentId: string, entry: Held) => {
    held.set(momentId, entry);
    publish();
};

let withdrawalInstalled = false;
function installWithdrawal() {
    if (withdrawalInstalled) return;
    withdrawalInstalled = true;
    // An action still in its 5 seconds never runs once the page withdraws.
    onPagePrivacyWithdrawal(() => {
        for (const [id, entry] of held) {
            if (entry.status === 'waiting') {
                window.clearTimeout(entry.timer);
                held.delete(id);
            }
        }
        publish();
    });
}

/** Shows the action's result at once and acts after 5 seconds unless Undo
 *  comes first. A card acts at most once. */
export function holdMainChatCardAction(momentId: string, action: string): void {
    const current = held.get(momentId);
    if (current && current.status !== 'failed') return;
    installWithdrawal();
    const timer = window.setTimeout(() => {
        set(momentId, { action, status: 'acting' });
        void ResolveMainChatProactiveAction(momentId, action).then((state) => {
            set(momentId, { action, status: 'done', state });
            // What waits on the person changed.
            notifyMainChatViewChanged();
        }).catch(() => {
            set(momentId, { action, status: 'failed' });
        });
    }, MAIN_CHAT_HELD_ACTION_MS);
    set(momentId, { action, status: 'waiting', timer });
}

/** Undo puts the card back as it was, before the computer acts. */
export function undoMainChatCardAction(momentId: string): boolean {
    const current = held.get(momentId);
    if (current?.status !== 'waiting') return false;
    window.clearTimeout(current.timer);
    held.delete(momentId);
    publish();
    return true;
}

export function useMainChatHeldAction(momentId: string): MainChatHeldAction | undefined {
    return useSyncExternalStore(subscribe, () => held.get(momentId), () => undefined);
}

export function resetMainChatHeldActions() {
    for (const entry of held.values()) window.clearTimeout(entry.timer);
    held.clear();
    publish();
}
