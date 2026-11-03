import { useSyncExternalStore } from "react";
import { StartDesktopTaskOffer, type TaskSuggestionView } from "@/lib/wails-shim-app";
import { onPagePrivacyWithdrawal } from "@/lib/page-privacy-withdrawal";

export interface PendingTaskStart { offer: TaskSuggestionView; timer: number }
let pending: PendingTaskStart[] = [];
const started = new Set<string>();
const listeners = new Set<() => void>();
function publish(next: PendingTaskStart[]) { pending = next; listeners.forEach((listener) => listener()); }
function subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; }
export function usePendingTaskStarts() { return useSyncExternalStore(subscribe, () => pending, () => pending); }
export function undoTaskStart(id: string) {
    const item = pending.find((entry) => entry.offer.id === id);
    if (!item?.timer) return;
    window.clearTimeout(item.timer);
    publish(pending.filter((entry) => entry !== item));
}
export function taskOfferStarted(offer: TaskSuggestionView) { return started.has(`${offer.id}:${offer.version}`); }
export function reconcilePendingTaskStarts(offers: TaskSuggestionView[]) {
    const stale = pending.filter((item) => item.timer !== 0 && !offers.some((offer) => offer.id === item.offer.id && offer.version === item.offer.version));
    stale.forEach((item) => window.clearTimeout(item.timer));
    if (stale.length) publish(pending.filter((item) => !stale.includes(item)));
}
export function beginTaskStart(offer: TaskSuggestionView, failed: () => void, succeeded?: () => void) {
    if (pending.some((entry) => entry.offer.id === offer.id)) return;
    const item: PendingTaskStart = { offer, timer: window.setTimeout(() => {
        item.timer = 0;
        publish([...pending]);
        void StartDesktopTaskOffer(offer.id, offer.version).then(() => {
            started.add(`${offer.id}:${offer.version}`);
            publish(pending.filter((entry) => entry !== item));
            succeeded?.();
        }).catch(() => {
            publish(pending.filter((entry) => entry !== item));
            failed();
        });
    }, 5000) };
    publish([...pending, item]);
}
export function clearPendingTaskStarts() {
    pending.forEach((item) => window.clearTimeout(item.timer));
    started.clear();
    publish([]);
}
onPagePrivacyWithdrawal(clearPendingTaskStarts);
