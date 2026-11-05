import { useEffect, useRef, useState } from 'react';
import { EventsOn as RuntimeEventsOn } from '../../wailsjs/runtime/runtime';

/**
 * Whether the window is being looked at: shown, and the Mac neither locked nor
 * asleep. The lock state comes from the backend's "system:state" event
 * (context_signals.go).
 */
let systemAway = false;
let installed = false;
let wasActive = true;
let offSystemState: (() => void) | undefined;
const changeListeners = new Set<(active: boolean) => void>();

function notifyChange() {
    const active = appActive();
    if (active === wasActive && !active) return;
    wasActive = active;
    for (const listener of [...changeListeners]) listener(active);
}

function install() {
    if (installed || typeof document === 'undefined') return;
    installed = true;
    wasActive = !systemAway && document.visibilityState !== 'hidden';
    document.addEventListener('visibilitychange', notifyChange);
    window.addEventListener('focus', notifyChange);
    try {
        offSystemState = RuntimeEventsOn('system:state', (state?: unknown) => {
            systemAway = state === 'locked' || state === 'sleep';
            notifyChange();
        });
    } catch {
        // Without the runtime (the browser bridge) only visibility counts.
    }
}

export function appActive(): boolean {
    install();
    return !systemAway && (typeof document === 'undefined' || document.visibilityState !== 'hidden');
}

/** Wait for a shown, unlocked window before starting a background read. */
export function waitForAppActive(): Promise<void> | undefined {
    if (appActive()) return undefined;
    return new Promise<void>((resolve) => {
        const off = onAppActivity((active) => { if (active) { off(); resolve(); } });
    }).then(() => waitForAppActive());
}

/** Calls listener with true each time the window is shown, focused or
 *  unlocked, and with false when it is hidden, locked or asleep. */
export function onAppActivity(listener: (active: boolean) => void): () => void {
    install();
    changeListeners.add(listener);
    return () => { changeListeners.delete(listener); };
}

/** Calls listener each time the window is shown, focused or unlocked again. */
export function onAppShown(listener: () => void): () => void {
    return onAppActivity((active) => { if (active) listener(); });
}

/** Test seam: forget the lock state and listeners between tests. */
export function resetAppActivity() {
    systemAway = false;
    changeListeners.clear();
    if (installed && typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', notifyChange);
        window.removeEventListener('focus', notifyChange);
    }
    offSystemState?.();
    offSystemState = undefined;
    installed = false;
    wasActive = true;
}

/** Runs tick every ms while the window is looked at, and once when it is
 *  shown again; the timer is stopped while it is hidden or locked. */
export function activeInterval(tick: () => void, ms: number): () => void {
    let timer: number | undefined;
    const stop = () => { if (timer !== undefined) window.clearInterval(timer); timer = undefined; };
    const startTimer = () => { stop(); timer = window.setInterval(tick, ms); };
    if (appActive()) startTimer();
    const off = onAppActivity((active) => {
        if (!active) { stop(); return; }
        tick();
        startTimer();
    });
    return () => { stop(); off(); };
}

/**
 * A safety resync on a budget: refresh runs at most once per budgetMs, only
 * while the window is looked at, and when the window is shown again only if
 * the budget has passed since the last run. Events drive the screen; this only
 * catches what an event may have missed. The caller's own mount read is the
 * first run, so the budget starts at mount.
 */
export function useBudgetedRefresh(refresh: () => void, budgetMs: number, enabled = true) {
    const latest = useRef(refresh);
    latest.current = refresh;
    useEffect(() => {
        if (!enabled || !(budgetMs > 0)) return;
        let last = Date.now();
        return activeInterval(() => {
            if (Date.now() - last < budgetMs) return;
            last = Date.now();
            latest.current();
        }, budgetMs);
    }, [budgetMs, enabled]);
}

/** A local clock for what moves with time alone (the now line, a duration
 *  counting up): no data read, paused while the window is hidden or locked,
 *  and current again the moment it is shown. */
export function useActiveClock(ms: number, enabled = true): number {
    const [now, setNow] = useState(() => Date.now());
    useEffect(() => {
        if (!enabled) return;
        return activeInterval(() => setNow(Date.now()), ms);
    }, [ms, enabled]);
    return now;
}
