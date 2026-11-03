// Motion helpers for setup v2 (handoff 2026-10-28-1215 §5, §10). Every wait
// and tween runs on requestAnimationFrame, the one clock, and belongs to a
// Run: leaving a screen cancels everything that screen started, so no late
// step ever touches the next screen.

export const CANCELLED = Symbol("setup-motion-cancelled");

/** Ease in and out (quadratic): the handoff's "standard" easing. */
export const ease = (t: number) =>
    t < 0.5 ? 2 * t * t : 1 - Math.pow(-2 * t + 2, 2) / 2;
export const lerp = (a: number, b: number, t: number) => a + (b - a) * t;
/** Forces style recalculation so a class added next restarts its animation. */
export const reflow = (el: HTMLElement) => void el.offsetWidth;

export function prefersReducedMotion(): boolean {
    try {
        return !!window.matchMedia?.("(prefers-reduced-motion: reduce)")
            .matches;
    } catch {
        return false;
    }
}

export class Run {
    private live = true;
    private frames = new Set<number>();
    constructor(readonly reduced: boolean) {}

    get alive() {
        return this.live;
    }

    cancel() {
        this.live = false;
        this.frames.forEach((id) => cancelAnimationFrame(id));
        this.frames.clear();
    }

    private frame(fn: (now: number) => void) {
        const id = requestAnimationFrame((now) => {
            this.frames.delete(id);
            fn(now);
        });
        this.frames.add(id);
    }

    /** Resolves after ms on the frame clock; rejects with CANCELLED. */
    wait(ms: number): Promise<void> {
        return new Promise((resolve, reject) => {
            if (!this.live) return reject(CANCELLED);
            let start: number | null = null;
            const step = (now: number) => {
                if (!this.live) return reject(CANCELLED);
                if (start === null) start = now;
                if (now - start >= ms) resolve();
                else this.frame(step);
            };
            this.frame(step);
        });
    }

    /** Calls f with progress 0..1 every frame for ms. Reduced motion jumps to 1. */
    tween(ms: number, f: (p: number) => void): Promise<void> {
        if (!this.live) return Promise.reject(CANCELLED);
        if (this.reduced) {
            f(1);
            return Promise.resolve();
        }
        return new Promise((resolve, reject) => {
            let start: number | null = null;
            const step = (now: number) => {
                if (!this.live) return reject(CANCELLED);
                if (start === null) start = now;
                const p = Math.min(1, (now - start) / ms);
                f(p);
                if (p < 1) this.frame(step);
                else resolve();
            };
            this.frame(step);
        });
    }

    /** Runs an async story; cancellation ends it quietly. */
    go(story: () => Promise<unknown>) {
        Promise.resolve()
            .then(story)
            .catch((error) => {
                if (error !== CANCELLED) console.error("[setup-v2]", error);
            });
    }
}

/** Offset of el inside root, in layout (unscaled) pixels. */
export function rel(el: HTMLElement, root: HTMLElement) {
    let x = 0;
    let y = 0;
    let node: HTMLElement | null = el;
    while (node && node !== root) {
        x += node.offsetLeft;
        y += node.offsetTop;
        node = node.offsetParent as HTMLElement | null;
    }
    return { x, y };
}
