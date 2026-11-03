import { useLayoutEffect, useRef } from "react";

/**
 * FLIP animation for the composer as it transitions between the centered
 * welcome layout (empty session) and the docked layout (active chat).
 *
 * Caller attaches `welcomeRef` to the composer wrapper while the welcome
 * view is rendered, and `chatRef` to the composer wrapper once the chat
 * view is rendered. We snapshot the currently-visible wrapper's bounding
 * rect on every commit, so at the moment of transition we know exactly
 * where the outgoing composer sat. On the flip we compute the Y delta
 * against the newly-mounted wrapper and animate it back to its resting
 * position, which makes the composer appear to glide from one spot to
 * the other instead of teleporting.
 *
 * Handles both directions (welcome -> chat on first submit, chat ->
 * welcome when the user opens a new chat from an active session).
 */
export function useComposerDockSlide(isEmptySession: boolean) {
    const welcomeRef = useRef<HTMLDivElement>(null);
    const chatRef = useRef<HTMLDivElement>(null);
    const lastWelcomeRectRef = useRef<DOMRect | null>(null);
    const lastChatRectRef = useRef<DOMRect | null>(null);
    const prevIsEmptyRef = useRef(isEmptySession);

    // Refresh the currently-visible wrapper's rect on every commit so we
    // have the most recent position available at the moment of transition.
    useLayoutEffect(() => {
        if (isEmptySession && welcomeRef.current) {
            lastWelcomeRectRef.current =
                welcomeRef.current.getBoundingClientRect();
        } else if (!isEmptySession && chatRef.current) {
            lastChatRectRef.current =
                chatRef.current.getBoundingClientRect();
        }
    });

    useLayoutEffect(() => {
        const wasEmpty = prevIsEmptyRef.current;
        prevIsEmptyRef.current = isEmptySession;
        if (wasEmpty === isEmptySession) return;

        // Pick the incoming wrapper and the outgoing (old) rect. On
        // welcome -> chat we animate chatRef starting from the welcome
        // rect; on chat -> welcome we animate welcomeRef starting from
        // the chat rect.
        const el = isEmptySession ? welcomeRef.current : chatRef.current;
        if (!el) return;

        const newRect = el.getBoundingClientRect();
        let oldTop: number | null = null;
        const oldRect = isEmptySession
            ? lastChatRectRef.current
            : lastWelcomeRectRef.current;
        if (oldRect) {
            oldTop = oldRect.top;
        } else if (isEmptySession) {
            // Chat rect wasn't captured (e.g., app launched straight into
            // chat, or the browser skipped a layout pass). Fall back to
            // the viewport bottom so the user still sees a slide up.
            oldTop = window.innerHeight - newRect.height;
        }
        if (oldTop === null) return;

        const deltaY = oldTop - newRect.top;
        if (Math.abs(deltaY) < 1) return;

        // Set starting position synchronously (no transition), then flip
        // to the rest position in the next frame so the CSS transition
        // engages.
        el.style.transition = "none";
        el.style.transform = `translateY(${deltaY}px)`;
        // Force a reflow so the browser picks up the starting transform
        // before we install the transition in the next frame.
        void el.offsetHeight;

        const raf = requestAnimationFrame(() => {
            el.style.transition =
                "transform 520ms cubic-bezier(0.16, 1, 0.3, 1)";
            el.style.transform = "translateY(0)";
        });

        const cleanup = () => {
            el.style.transition = "";
            el.style.transform = "";
            el.removeEventListener("transitionend", onEnd);
            window.clearTimeout(safetyTimer);
        };
        const onEnd = (ev: TransitionEvent) => {
            if (ev.propertyName !== "transform") return;
            cleanup();
        };
        el.addEventListener("transitionend", onEnd);
        // Guardrail: some webviews drop transitionend occasionally; make
        // sure we always clear the inline styles.
        const safetyTimer = window.setTimeout(cleanup, 900);

        return () => {
            cancelAnimationFrame(raf);
            cleanup();
        };
    }, [isEmptySession]);

    return { welcomeRef, chatRef };
}
