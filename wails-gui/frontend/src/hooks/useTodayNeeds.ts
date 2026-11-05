import { useSyncExternalStore } from "react";
import { GetNeedsYou, PutOffNeedsYou, type TodayNeed, type TodayNeedsPage } from "@/lib/wails-shim-app";
import { pageQueryCache } from "@/lib/query-cache";
import { onPagePrivacyWithdrawal } from "@/lib/page-privacy-withdrawal";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { waitForAppActive } from "@/lib/app-activity";

type Snapshot = { items: TodayNeed[]; putOff: TodayNeed[]; loading: boolean; failed: boolean };
const EMPTY: TodayNeed[] = [];
let snapshot: Snapshot = { items: EMPTY, putOff: EMPTY, loading: true, failed: false };
let returnTimer: ReturnType<typeof setTimeout> | undefined;
let request = 0;
let cleanups: Array<() => void> = [];
const listeners = new Set<() => void>();

function publish(next: Snapshot) {
    snapshot = next;
    listeners.forEach((listener) => listener());
}

// A put-off item comes back at a time, not on an event: read again then.
function scheduleReturn(at: number | undefined) {
    if (returnTimer !== undefined) clearTimeout(returnTimer);
    returnTimer = undefined;
    if (!at || listeners.size === 0) return;
    returnTimer = setTimeout(() => {
        returnTimer = undefined;
        pageQueryCache.invalidate(["GetNeedsYou"]);
        refreshTodayNeeds();
    }, Math.min(Math.max(at - Date.now(), 0) + 1000, 2 ** 31 - 1));
}

/** Not now, Bring back now and Keep on a Needs you item. A failed save puts
 *  the item back as it was and rejects. */
export async function putOffTodayNeed(need: TodayNeed, action: "put_off" | "bring_back" | "keep" | "forget") {
    const id = need.targetKind === "handoff" ? need.targetId : need.id;
    const before = snapshot;
    if (action === "put_off") publish({ ...snapshot, items: snapshot.items.filter((item) => item.id !== need.id), putOff: [...snapshot.putOff, need] });
    if (action === "forget") publish({ ...snapshot, items: snapshot.items.filter((item) => item.id !== need.id) });
    if (action === "bring_back") publish({ ...snapshot, items: [...snapshot.items, { ...need, putOffUntil: undefined }], putOff: snapshot.putOff.filter((item) => item.id !== need.id) });
    try {
        await PutOffNeedsYou(id, action);
    } catch (error) {
        publish(before);
        throw error;
    } finally {
        pageQueryCache.invalidate(["GetNeedsYou"]);
        refreshTodayNeeds();
    }
}

export function refreshTodayNeeds() {
    const current = ++request;
    void Promise.resolve().then(() => waitForAppActive()).then(() => current === request ? GetNeedsYou(0) : null).then((page) => {
        if (current !== request) return;
        if (!page || !Array.isArray(page.items)) throw new Error("Invalid waiting projection");
        pageQueryCache.set("GetNeedsYou", [0], page);
        publish({ items: page.items, putOff: page.putOff ?? EMPTY, loading: false, failed: false });
        scheduleReturn(page.nextReturnAt);
    }).catch(() => {
        if (current === request) publish({ items: EMPTY, putOff: EMPTY, loading: false, failed: true });
    });
}

function subscribe(listener: () => void) {
    listeners.add(listener);
    if (listeners.size === 1) {
        cleanups.push(EventsOn("today:needs-changed", () => {
            pageQueryCache.invalidate(["GetNeedsYou"]);
            refreshTodayNeeds();
        }));
        cleanups.push(onPagePrivacyWithdrawal(() => {
            request++;
            pageQueryCache.invalidate(["GetNeedsYou"]);
            publish({ items: EMPTY, putOff: EMPTY, loading: true, failed: false });
            refreshTodayNeeds();
        }));
        refreshTodayNeeds();
    }
    return () => {
        listeners.delete(listener);
        if (listeners.size === 0) {
            request++;
            cleanups.forEach((cleanup) => cleanup());
            cleanups = [];
            scheduleReturn(undefined);
            snapshot = { items: EMPTY, putOff: EMPTY, loading: true, failed: false };
        }
    };
}

function getSnapshot(): Snapshot {
    if (snapshot.loading && snapshot.items.length === 0) {
        const cached = pageQueryCache.peek<TodayNeedsPage>("GetNeedsYou", [0]);
        if (cached && Array.isArray(cached.items)) snapshot = { items: cached.items, putOff: cached.putOff ?? EMPTY, loading: false, failed: false };
    }
    return snapshot;
}

/** One ordered list, shared by the Today preview and Tasks. Not now is
 *  stored by the app (PutOffNeedsYou), so the phone and a restart agree. */
export function useTodayNeeds(): Snapshot {
    return useSyncExternalStore(subscribe, getSnapshot);
}
