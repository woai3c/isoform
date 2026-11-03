import { displayModelText } from "@/lib/model-display";
import {
    lazy,
    memo,
    startTransition,
    Suspense,
    useEffect,
    useRef,
    useState,
    useSyncExternalStore,
} from "react";
import { useTranslation } from "react-i18next";
import remarkGfm from "remark-gfm";
import {
    Streamdown,
    useIsCodeFenceIncomplete,
    type Components,
} from "streamdown";
import {
    BrowserOpenURL,
    EventsEmit,
    EventsOn,
} from "../../wailsjs/runtime/runtime";
import { ActivitySessionExists } from "../../wailsjs/go/main/App";
import { onPrivacyReset } from "@/lib/privacy-reset";
import { CopyButton } from "./copy-button";

// Prism loads after the first paint (code-highlighter.tsx).
const LazyCodeHighlighter = lazy(() => import("./code-highlighter"));

// ── Activity session UUID linking ───────────────────────────────────
// Inline code containing a bare UUID is rendered as a clickable button
// when that UUID resolves to an existing activity session. Existence is
// checked lazily on first encounter via the lightweight backend
// `ActivitySessionExists` RPC (no events / segments / snapshots load).
//
// Three terminal states drive rendering:
//   - 'unknown'  pending or transient backend error — renders as plain
//                inline code (silently non-clickable per design rule)
//   - 'exists'   definitive hit — renders as a clickable button
//   - 'missing'  definitive miss (sql.ErrNoRows) — renders as plain
//                inline code, will not be retried
//
// Transient errors (monitor not yet initialized, db hiccup) are NOT
// cached — `seenUuids` keeps a record of every uuid we've encountered
// so an `app:ready` event (emitted once startup finishes) can re-check
// every still-unknown id. Without this, valid UUIDs rendered before
// startup completes would stay non-clickable until reload.
//
// Clicks emit `nav:open-activity-session` so App.tsx can route into the
// existing openActivityWithSession flow without threading a callback
// prop through StreamCard -> MarkdownContent (which would invalidate
// the cached components map).
const UUID_RE =
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
type ActivitySessionExistence = "unknown" | "exists" | "missing";
const activitySessionExistence = new Map<string, ActivitySessionExistence>();
const activitySessionInFlight = new Set<string>();
const activitySessionListeners = new Set<() => void>();
const seenUuids = new Set<string>();

function notifyActivitySessionListeners() {
    activitySessionListeners.forEach((l) => l());
}

function ensureActivitySessionCheck(uuid: string) {
    seenUuids.add(uuid);
    // Don't re-check terminal states; 'missing' is only set on definitive
    // not-found, so it's safe to skip.
    if (activitySessionExistence.has(uuid)) return;
    if (activitySessionInFlight.has(uuid)) return;
    activitySessionInFlight.add(uuid);
    ActivitySessionExists(uuid)
        .then((exists) => {
            activitySessionExistence.set(uuid, exists ? "exists" : "missing");
        })
        .catch(() => {
            // Transient error (e.g. activity store not yet initialized).
            // Leave existence unset so app:ready can retry. Still notify
            // so any in-progress 'unknown' subscribers don't get stuck.
        })
        .finally(() => {
            activitySessionInFlight.delete(uuid);
            notifyActivitySessionListeners();
        });
}

function retryUnknownActivitySessions() {
    for (const uuid of seenUuids) {
        if (activitySessionExistence.has(uuid)) continue;
        if (activitySessionInFlight.has(uuid)) continue;
        ensureActivitySessionCheck(uuid);
    }
}

let activitySessionReadySubscribed = false;
function ensureActivitySessionReadySubscription() {
    if (activitySessionReadySubscribed) return;
    activitySessionReadySubscribed = true;
    EventsOn("app:ready", () => {
        retryUnknownActivitySessions();
    });
}

function getActivitySessionExistence(uuid: string): ActivitySessionExistence {
    return activitySessionExistence.get(uuid) ?? "unknown";
}

function useActivitySessionExistence(uuid: string): ActivitySessionExistence {
    ensureActivitySessionReadySubscription();
    useEffect(() => {
        ensureActivitySessionCheck(uuid);
    }, [uuid]);
    return useSyncExternalStore(
        (cb) => {
            activitySessionListeners.add(cb);
            return () => {
                activitySessionListeners.delete(cb);
            };
        },
        () => getActivitySessionExistence(uuid),
        () => getActivitySessionExistence(uuid),
    );
}

function ActivitySessionRef({ uuid }: { uuid: string }) {
    const { t } = useTranslation("app");
    const state = useActivitySessionExistence(uuid);

    if (state !== "exists") {
        return (
            <code className="px-1.5 py-0.5 rounded-md bg-ink/[0.07] text-[13px] font-mono break-all">
                {uuid}
            </code>
        );
    }

    return (
        <button
            type="button"
            onClick={() => EventsEmit("nav:open-activity-session", uuid)}
            className="px-1.5 py-0.5 rounded-md bg-ink/[0.07] hover:bg-ink/[0.12] text-[13px] font-mono break-all cursor-pointer transition-colors"
            title={t("markdownContent.activitySession.open")}
        >
            {uuid}
        </button>
    );
}

// Renders a fenced code block. Calls `useIsCodeFenceIncomplete` so during
// streaming (while the model is still mid-fence) we skip Prism and render a
// plain <pre><code> — running Prism on a growing block every frame is what
// produces visible token stutter. Once the closing fence commits, Streamdown
// flips the hook and we swap in full syntax highlighting.
//
// Sealed blocks defer Prism as well: tokenizing a long block costs far more
// than everything else on the row, so no mount commit — first paint or a row
// scrolled back into the window — ever contains highlight work. The block
// paints with the plain (already styled) form and colors land one block per
// macrotask, the same plain-then-highlighted swap the streaming path uses.
const highlightedBlocks = new Set<string>();

// Highlights run one block per macrotask: a screenful of code fences landing
// in a single commit would freeze the first interaction after opening a chat.
const highlightQueue: Array<() => void> = [];
let highlightScheduled = false;
function scheduleHighlight(run: () => void) {
    highlightQueue.push(run);
    if (highlightScheduled) return;
    highlightScheduled = true;
    const step = () => {
        const next = highlightQueue.shift();
        if (next) next();
        if (highlightQueue.length > 0) {
            setTimeout(step, 0);
        } else {
            highlightScheduled = false;
        }
    };
    setTimeout(step, 0);
}

// The markers carry block lengths and languages — metadata about what was on
// screen. Like every render-tier cache they drop on Forget, protect-app,
// Closed Eyes and sign-out, together with any highlight work still queued.
onPrivacyReset(() => {
    highlightedBlocks.clear();
    highlightQueue.length = 0;
});

/** Test-only view of the marker set and the pending highlight work. */
export const highlightCacheProbe = {
    marked: () => highlightedBlocks.size,
    queued: () => highlightQueue.length,
};

function FencedCodeBlock({ lang, code }: { lang: string; code: string }) {
    const { t } = useTranslation("app");
    const isIncomplete = useIsCodeFenceIncomplete();
    const blockKey = `${lang} ${code.length}`;
    const [settled, setSettled] = useState(false);
    useEffect(() => {
        if (settled) return;
        let cancelled = false;
        scheduleHighlight(() => {
            if (cancelled) return;
            if (highlightedBlocks.size > 500) highlightedBlocks.clear();
            highlightedBlocks.add(blockKey);
            setSettled(true);
        });
        return () => { cancelled = true; };
    }, [settled, blockKey]);
    const plain = isIncomplete || !settled;
    const plainBlock = (
        <pre
            className="m-0 px-4 py-4 bg-code-bg text-code-fg text-[13px] leading-relaxed overflow-x-auto"
            style={{ whiteSpace: "pre-wrap", wordWrap: "break-word" }}
        >
            <code
                style={{
                    fontFamily: "var(--font-mono)",
                    whiteSpace: "pre-wrap",
                    overflowWrap: "anywhere",
                }}
            >
                {code}
            </code>
        </pre>
    );
    return (
        <div className="my-3 rounded-[10px] overflow-hidden border border-code-fg/[0.06]">
            <div className="flex items-center justify-between px-4 py-1.5 bg-code-bg border-b border-code-fg/[0.06]">
                <span className="text-[11px] text-code-fg/50 font-mono">
                    {lang}
                </span>
                <CopyButton
                    text={code}
                    label={t("markdownContent.copyButton.copy")}
                    copiedLabel={t("markdownContent.copyButton.copied")}
                    className="px-2 py-1 text-[11px] text-code-fg/60 hover:text-code-fg"
                />
            </div>
            {plain ? plainBlock : (
                <Suspense fallback={plainBlock}>
                    <LazyCodeHighlighter lang={lang} code={code} />
                </Suspense>
            )}
        </div>
    );
}

// Single, stable components map. Streamdown handles incomplete-markdown
// rebalancing internally (`parseIncompleteMarkdown` default), so we no
// longer need separate "streaming" vs "sealed" maps — the streaming-only
// branching for code blocks lives inside FencedCodeBlock via the
// useIsCodeFenceIncomplete hook.
const components: Components = {
    code({ className, children, ...props }) {
        const match = /language-([\w-]+)/.exec(className || "");
        const codeString = String(children).replace(/\n$/, "");
        if (match) {
            const lang = match[1];
            return <FencedCodeBlock lang={lang} code={codeString} />;
        }
        // Inline `code` fallback — Streamdown routes inline backticks to
        // `inlineCode` below when provided, but we keep this branch for
        // robustness in case the inline split-off doesn't fire.
        return (
            <code
                className="px-1.5 py-0.5 rounded-md bg-ink/[0.07] text-[13px] font-mono break-all"
                {...props}
            >
                {children}
            </code>
        );
    },
    inlineCode({ children, ...props }) {
        const inlineText = String(children ?? "").trim();
        if (UUID_RE.test(inlineText)) {
            return <ActivitySessionRef uuid={inlineText.toLowerCase()} />;
        }
        return (
            <code
                className="px-1.5 py-0.5 rounded-md bg-ink/[0.07] text-[13px] font-mono break-all"
                {...props}
            >
                {children}
            </code>
        );
    },
    p({ children }) {
        return <p className="m-0 mb-3 last:mb-0 leading-relaxed">{children}</p>;
    },
    ul({ children }) {
        return (
            <ul className="m-0 mb-3 last:mb-0 pl-6 list-disc space-y-1">
                {children}
            </ul>
        );
    },
    ol({ children }) {
        return (
            <ol className="m-0 mb-3 last:mb-0 pl-6 list-decimal space-y-1">
                {children}
            </ol>
        );
    },
    li({ children }) {
        return <li className="leading-relaxed">{children}</li>;
    },
    h1({ children }) {
        return (
            <h1 className="m-0 mb-3 text-lg font-semibold leading-snug">
                {children}
            </h1>
        );
    },
    h2({ children }) {
        return (
            <h2 className="m-0 mb-2.5 text-base font-semibold leading-snug">
                {children}
            </h2>
        );
    },
    h3({ children }) {
        return (
            <h3 className="m-0 mb-2 text-sm font-semibold leading-snug">
                {children}
            </h3>
        );
    },
    blockquote({ children }) {
        return (
            <blockquote className="m-0 mb-3 last:mb-0 pl-4 border-l-2 border-accent-2/30 text-muted italic">
                {children}
            </blockquote>
        );
    },
    table({ children }) {
        return (
            <div className="my-3 overflow-x-auto rounded-lg border border-border">
                <table className="w-full text-sm border-collapse">
                    {children}
                </table>
            </div>
        );
    },
    thead({ children }) {
        return <thead className="bg-ink/[0.04]">{children}</thead>;
    },
    th({ children }) {
        return (
            <th className="px-3 py-2 text-left text-xs font-semibold text-muted border-b border-border">
                {children}
            </th>
        );
    },
    td({ children }) {
        return (
            <td className="px-3 py-2 border-b border-border/50">{children}</td>
        );
    },
    hr() {
        return <hr className="my-4 border-border" />;
    },
    a({ href, children }) {
        const linkClass =
            "text-accent-2 underline underline-offset-2 hover:text-accent-2/80 transition-colors cursor-pointer";
        return (
            <a
                href={href}
                className={linkClass}
                onClick={(e) => {
                    e.preventDefault();
                    if (!href) return;
                    if (href.startsWith("automation://")) {
                        EventsEmit(
                            "nav:open-automation",
                            href.replace("automation://", ""),
                        );
                    } else {
                        BrowserOpenURL(href);
                    }
                }}
            >
                {children}
            </a>
        );
    },
    strong({ children }) {
        return <strong className="font-semibold">{children}</strong>;
    },
};

const remarkPlugins = [remarkGfm];

// Streamdown's default rehype pipeline includes rehype-sanitize, which
// strips our custom action protocols (handoff-accept://, etc.) from
// link hrefs — the AI output we render is from our own backend, and the
// action-link onClick handlers in the `a` override above depend on the
// original href being intact. We deliberately bypass the sanitize stage
// by passing an empty rehype list; markdown stays markdown-only here,
// no raw HTML reaches the renderer, so XSS surface is limited to whatever
// our own backend would emit.
const rehypePlugins: never[] = [];

// Lets the same allowlist of action protocols through unchanged. Streamdown's
// urlTransform signature is `(url, key, node) => string | null | undefined`
// — we ignore the extra args, the URL is enough.
const ALLOWED_CUSTOM_SCHEMES = [
    "handoff-accept://",
    "handoff-dismiss://",
    "automation://",
];
function urlTransform(url: string): string {
    for (const scheme of ALLOWED_CUSTOM_SCHEMES) {
        if (url.startsWith(scheme)) return url;
    }
    const safeProtocol = /^(https?|ircs?|mailto|xmpp)$/i;
    const colon = url.indexOf(":");
    const questionMark = url.indexOf("?");
    const numberSign = url.indexOf("#");
    const slash = url.indexOf("/");
    if (
        colon < 0 ||
        (slash > -1 && colon > slash) ||
        (questionMark > -1 && colon > questionMark) ||
        (numberSign > -1 && colon > numberSign) ||
        safeProtocol.test(url.slice(0, colon))
    ) {
        return url;
    }
    return "";
}

// ── Streaming pacer ─────────────────────────────────────────────────
// Server-sent tokens arrive in uneven bursts (e.g. 40 chars in 10ms, then
// 200ms of silence, then another 40 chars). The pacer decouples "received"
// from "displayed": each rAF tick reveals up to a target length obtained by
// linearly interpolating the arrival curve LAG_MS ago. That gives a steady
// per-frame advance even when arrivals are uneven.
//
// Key rule ("burst clamp"): the interp for any arrival only spans the last
// LAG_MS before it. If the gap between two arrivals is larger than LAG_MS,
// we hold flat and then ramp across the final LAG_MS — otherwise a burst
// following a long pause would back-fill the entire pause window in one frame.
//
// Mirrors claude.ai's $V/task() smoother (1000/60 ms tick, arrival history,
// bounded reveal) — the bisection is replaced with plain linear interp.
//
// LAG_MS = the size of the buffer. We always try to display text that arrived
// LAG_MS ago, so a server-side pause up to LAG_MS is fully masked: the UI
// keeps streaming from the buffer while the server is quiet. Bigger values
// mask longer stalls but add more perceptible lag. 400 ms covers the typical
// model inter-batch gaps without feeling sluggish.
const LAG_MS = 400;

interface Arrival {
    t: number;
    len: number;
}

interface SmootherState {
    rawText: string;
    revealedLength: number;
    arrivals: Arrival[];
    rafHandle: number | null;
    mounted: boolean;
}

// Linearly interpolate the arrival curve at queryTime. Each arrival only
// "opens" a reveal window of at most LAG_MS leading up to it, so a burst
// following a pause plays out over LAG_MS rather than back-filling the pause.
function interpolateArrivals(arrivals: Arrival[], queryTime: number): number {
    if (arrivals.length === 0) return 0;

    const first = arrivals[0];
    if (queryTime < first.t) {
        const windowStart = first.t - LAG_MS;
        if (queryTime <= windowStart) return 0;
        const ratio = (queryTime - windowStart) / LAG_MS;
        return first.len * ratio;
    }

    for (let i = 1; i < arrivals.length; i++) {
        const a = arrivals[i - 1];
        const b = arrivals[i];
        if (queryTime < b.t) {
            const windowStart = Math.max(a.t, b.t - LAG_MS);
            if (queryTime <= windowStart) return a.len;
            const ratio = (queryTime - windowStart) / (b.t - windowStart);
            return a.len + (b.len - a.len) * ratio;
        }
    }

    return arrivals[arrivals.length - 1].len;
}

function useSmoothedText(rawText: string): string {
    const [revealed, setRevealed] = useState(rawText);
    const stateRef = useRef<SmootherState>({
        rawText,
        revealedLength: rawText.length,
        arrivals: [],
        rafHandle: null,
        mounted: false,
    });

    useEffect(() => {
        const s = stateRef.current;

        // First mount: whatever came in is treated as already-complete content
        // (e.g. scrolling into an old message). No pacing — show it all.
        if (!s.mounted) {
            s.mounted = true;
            s.rawText = rawText;
            s.revealedLength = rawText.length;
            s.arrivals = [];
            return;
        }

        // Only pace when the new text is a suffix-extension of the old text —
        // a real streaming append. If the parent swapped in unrelated content
        // (message switched, user edit, etc.) adopt it immediately rather
        // than replaying an entire document as a reveal animation.
        const isAppend =
            rawText.length > s.rawText.length && rawText.startsWith(s.rawText);

        if (!isAppend) {
            s.rawText = rawText;
            s.revealedLength = rawText.length;
            s.arrivals = [];
            setRevealed(rawText);
            return;
        }

        const now = performance.now();
        s.rawText = rawText;
        s.arrivals.push({ t: now, len: rawText.length });
        // Prune — we only look back ~LAG_MS + the interp between the two most
        // recent arrivals, so 5 s is plenty.
        const cutoff = now - 5000;
        while (s.arrivals.length > 1 && s.arrivals[0].t < cutoff) {
            s.arrivals.shift();
        }

        if (s.rafHandle == null) {
            const tick = () => {
                const nowTick = performance.now();
                const queryTime = nowTick - LAG_MS;
                const target = interpolateArrivals(s.arrivals, queryTime);

                // Never go backwards; never exceed what was actually received.
                const next = Math.min(
                    s.rawText.length,
                    Math.max(s.revealedLength, target),
                );

                if (next !== s.revealedLength) {
                    s.revealedLength = next;
                    const sliced = s.rawText.slice(0, Math.floor(next));
                    // Mark the per-frame update as a transition so React's
                    // concurrent scheduler can interrupt the heavy render
                    // (chunker + ReactMarkdown) between component boundaries
                    // to let the browser paint. Without this, a monolithic
                    // parse on a long tail blocks paint for the whole parse
                    // duration and the stream visibly stalls.
                    // Mirrors claude.ai's p.startTransition(() => E(...))
                    // wrapping stream-event setState dispatches.
                    startTransition(() => setRevealed(sliced));
                }

                // Keep ticking while there's still content to reveal OR while
                // we're still inside the last arrival's LAG_MS drain window.
                const last = s.arrivals[s.arrivals.length - 1];
                const withinDrainWindow = last && nowTick - last.t < LAG_MS;
                const stillBehind = s.revealedLength < s.rawText.length;

                if (stillBehind || withinDrainWindow) {
                    s.rafHandle = requestAnimationFrame(tick);
                } else {
                    s.rafHandle = null;
                }
            };
            s.rafHandle = requestAnimationFrame(tick);
        }
    }, [rawText]);

    useEffect(
        () => () => {
            const s = stateRef.current;
            if (s.rafHandle != null) cancelAnimationFrame(s.rafHandle);
        },
        [],
    );

    return revealed;
}

interface MarkdownContentProps {
    content: string;
}

export const MarkdownContent = memo(function MarkdownContent({
    content,
}: MarkdownContentProps) {
    // useSmoothedText paces the raw arrival curve into a steady per-frame
    // advance — Streamdown handles incomplete-markdown rebalancing and
    // per-block memoization itself, so feeding it the paced text is all
    // we need to do.
    const { t } = useTranslation("common");
    const paced = displayModelText(useSmoothedText(content), t);
    return (
        <div className="text-sm break-words [overflow-wrap:anywhere]">
            <Streamdown
                components={components}
                remarkPlugins={remarkPlugins}
                rehypePlugins={rehypePlugins}
                urlTransform={urlTransform}
                parseIncompleteMarkdown
            >
                {paced}
            </Streamdown>
        </div>
    );
});
