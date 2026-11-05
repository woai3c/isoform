import { pagePrefetchSteps, type PagePrefetchStep } from './page-queries';
import { appActive, onAppShown } from './app-activity';

// Idle prefetch (Bo, 2026-10-23): after Today's first paint, warm every page's
// primary reads during idle time, one query at a time, in pagePrefetchSteps
// order. The walker pauses while the user is typing or a chat reply streams —
// the page they are on keeps the machine — and resumes on the next idle
// period. Runs once per app session; fresh entries skip the wire, so a later
// hover or visit reuses what landed here.

const TYPING_QUIET_MS = 2000;
/** How long a parked walk waits before re-checking when the platform has no
 *  requestIdleCallback. Bounds the setTimeout fallback: without it a parked
 *  walk would re-arm a zero-delay timer every macrotask for the whole pause. */
const PARKED_RECHECK_MS = 500;

let typingUntil = 0;
let streaming = false;
let watching = false;
let started = false;
let cancelled = false;
let donePromise: Promise<void> | undefined;

/** A chat reply is streaming (App wires the active session's flag here). */
export function setPrefetchStreaming(on: boolean) { streaming = on; }

/** True while a walk should stay parked: recent keystroke in an editable, or
 * a streaming reply. */
export function prefetchPaused(now = Date.now()): boolean {
    return streaming || now < typingUntil;
}

function isEditable(target: EventTarget | null): boolean {
    const element = target as HTMLElement | null;
    return Boolean(element && (element.tagName === 'INPUT' || element.tagName === 'TEXTAREA' || element.isContentEditable));
}

const onKeydown = (event: KeyboardEvent) => {
    if (isEditable(event.target)) typingUntil = Date.now() + TYPING_QUIET_MS;
};

function watchTyping() {
    if (watching) return;
    watching = true;
    window.addEventListener('keydown', onKeydown, true);
}

interface IdleDeps {
    steps?: PagePrefetchStep[];
    /** Defaults to requestIdleCallback with a setTimeout fallback. */
    onIdle?: (run: () => void) => void;
    now?: () => number;
}

function defaultOnIdle(run: () => void) {
    if (window.requestIdleCallback) window.requestIdleCallback(run, { timeout: 2000 });
    else setTimeout(run, 0);
}

/** Walks the prefetch steps, one query at a time, each on its own idle
 * callback. Idempotent: later callers join the first walk. */
export function startIdlePrefetch(deps: IdleDeps = {}): Promise<void> {
    if (started) return donePromise!;
    started = true;
    cancelled = false;
    watchTyping();
    const steps = deps.steps ?? pagePrefetchSteps();
    const onIdle = deps.onIdle ?? defaultOnIdle;
    const customIdle = deps.onIdle !== undefined;
    const now = deps.now ?? (() => Date.now());
    let index = 0;
    donePromise = new Promise<void>((resolve) => {
        const next = () => {
            if (cancelled) return resolve();
            if (index >= steps.length) return resolve();
            if (!appActive()) {
                const off = onAppShown(() => { off(); next(); });
                return;
            }
            // Parked work re-checks on later idle periods instead of polling.
            // The setTimeout fallback (no requestIdleCallback, e.g. WebKit)
            // parks on a bounded timer: a zero-delay re-arm would spin for
            // the whole pause.
            if (prefetchPaused(now())) {
                if (!customIdle && !window.requestIdleCallback) {
                    setTimeout(next, PARKED_RECHECK_MS);
                    return;
                }
                return onIdle(next);
            }
            const step = steps[index++];
            onIdle(() => {
                if (!appActive()) { index--; next(); return; }
                void step.run().catch(() => undefined).then(() => next());
            });
        };
        next();
    });
    return donePromise;
}

/** Test hook: drops the walk so a fresh startIdlePrefetch re-runs. */
export function resetIdlePrefetch() {
    cancelled = true;
    started = false;
    streaming = false;
    typingUntil = 0;
    donePromise = undefined;
}
