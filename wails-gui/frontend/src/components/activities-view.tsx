import {
    useEffect,
    useReducer,
    useState,
    type CSSProperties,
} from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import {
    ChevronLeft,
    ChevronRight,
    X,
} from 'lucide-react';
import {
    GetAppIconsForBundleIDs,
    GetOCRHighlightBoxes,
    type ActivityRunFrame,
    type OCRHighlightBox,
} from '../lib/wails-shim-app';
import { cn } from '../lib/utils';
import { snapshotImageURL } from '../lib/host-bridge-media';
import { SnapshotImage } from './snapshot-image';
import { AppIcon, hasAppIcon } from '@/components/icons/app-icons';

// Shared building blocks of the Activity page: native app icons and the
// screenshot strip (thumbnails + fullscreen lightbox) that the Day view
// renders for a moment. The old Activities card stream lived here too; the
// merged Activity page replaced it with the Day view.

// ── App icons ─────────────────────────────────────────────────────────────
// Native macOS app icons resolved by bundle id via GetAppIconsForBundleIDs
// (cached process-wide so fetches dedupe across cards), with a brand-SVG
// then monogram fallback.

const nativeAppIconCache = new Map<string, string>(); // bundleId -> data URL
const nativeAppIconPending = new Set<string>();

/** A timeline appKey is the bare bundle id for real apps; "app:<name>" and
 *  "unknown" carry no fetchable bundle. */
function fetchableBundleId(appKey: string): string {
    const k = (appKey ?? '').trim();
    return !k || k === 'unknown' || k.startsWith('app:') ? '' : k;
}

/** Batch-fetches any not-yet-cached native icons for the given bundle ids and
 *  re-renders the caller when they arrive. Reads come from nativeAppIconCache. */
export function useAppIcons(appKeys: string[]): void {
    const [, force] = useReducer((x: number) => x + 1, 0);
    const wanted = appKeys.map(fetchableBundleId).filter(Boolean);
    const key = wanted.join(',');
    useEffect(() => {
        const missing = wanted.filter((id) => !nativeAppIconCache.has(id) && !nativeAppIconPending.has(id));
        if (missing.length === 0) return;
        missing.forEach((id) => nativeAppIconPending.add(id));
        GetAppIconsForBundleIDs(missing)
            .then((res) => {
                for (const [id, url] of Object.entries(res ?? {})) {
                    if (url) nativeAppIconCache.set(id, url);
                }
            })
            .catch(() => {})
            .finally(() => {
                missing.forEach((id) => nativeAppIconPending.delete(id));
                force();
            });
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [key]);
}

/** Brand-icon candidate bundles derived from a name/key (mirrors the timeline). */
function iconCandidates(appKey: string, appName: string): string[] {
    const stripped = (appKey ?? '').trim().replace(/^app:/, '');
    const name = (appName ?? '').trim();
    const hostName = webContentHostName(stripped, name);
    const values = [stripped, name, name.endsWith('.app') ? name.slice(0, -4) : `${name}.app`];
    if (hostName) {
        values.push(hostName, `${hostName}.app`);
    }
    const lower = `${appKey} ${appName}`.toLowerCase();
    if (lower.includes('slack')) values.push('Slack', 'Slack.app');
    if (lower.includes('chrome') || lower.includes('google')) values.push('Google Chrome', 'Chrome.app');
    if (lower.includes('codex')) values.push('Codex', 'Codex.app');
    if (lower.includes('claude')) values.push('Claude', 'Claude.app');
    if (lower.includes('yansu')) values.push('Yansu', 'Yansu.app');
    return Array.from(new Set(values.filter((v) => v.trim() !== '')));
}

function webContentHostName(appKey: string, appName: string): string {
    if (!appKey.toLowerCase().includes('webkit.webcontent')) return '';
    return appName.replace(/\s+Web\s+Content$/i, '').trim();
}

/** Renders an app's icon: native macOS icon when available, else brand SVG,
 *  else a monogram. Keyed by bundle id — names are never shown in chrome. */
export function AppGlyph({ appKey, appName, className }: { appKey: string; appName: string; className?: string }) {
    const bundle = fetchableBundleId(appKey);
    const nativeUrl = bundle ? nativeAppIconCache.get(bundle) : undefined;
    if (nativeUrl) {
        return <img src={nativeUrl} alt="" aria-hidden className={cn('object-contain', className)} />;
    }
    const brand = iconCandidates(appKey, appName).find((c) => hasAppIcon(c));
    if (brand) {
        return <AppIcon appBundle={brand} className={cn('object-contain', className)} />;
    }
    const letter = (appName || '?').trim().slice(0, 1).toUpperCase();
    return (
        <span
            className={cn(
                'flex items-center justify-center rounded-[3px] bg-ink/[0.08] text-[8px] font-semibold text-muted',
                className,
            )}
        >
            {letter}
        </span>
    );
}

// ── OCR highlight boxes ────────────────────────────────────────────────────

const OCR_BOX_CACHE_MAX = 1000;
const ocrBoxCache = new Map<string, Promise<OCRHighlightBox[]>>();

function getOCRBoxes(path: string, keyword: string): Promise<OCRHighlightBox[]> {
    const key = `${path}\u0000${keyword}`;
    let pending = ocrBoxCache.get(key);
    if (!pending) {
        if (ocrBoxCache.size >= OCR_BOX_CACHE_MAX) {
            const oldest = ocrBoxCache.keys().next().value;
            if (oldest !== undefined) ocrBoxCache.delete(oldest);
        }
        pending = GetOCRHighlightBoxes(path, keyword)
            .then((boxes) => boxes ?? [])
            .catch(() => {
                ocrBoxCache.delete(key);
                return [];
            });
        ocrBoxCache.set(key, pending);
    }
    return pending;
}

function useOCRBoxes(path: string, keywords: string[], enabled: boolean): OCRHighlightBox[] {
    const [result, setResult] = useState<{ key: string; boxes: OCRHighlightBox[] } | null>(null);
    const keywordsKey = keywords.map((s) => s.trim().toLowerCase()).join('\u0000');
    const resultKey = `${path}\u0000${keywordsKey}`;
    useEffect(() => {
        if (!enabled || !path || keywords.length === 0) {
            setResult(null);
            return;
        }
        let cancelled = false;
        Promise.all(keywords.map((kw) => getOCRBoxes(path, kw))).then((results) => {
            if (!cancelled) setResult({ key: resultKey, boxes: results.flat() });
        });
        return () => {
            cancelled = true;
        };
        // keywordsKey stands in for the unstable keywords array identity.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [path, keywordsKey, enabled, resultKey]);
    return enabled && result?.key === resultKey ? result.boxes : [];
}

function OCRBoxOverlay({
    boxes,
    focusX = 0,
    focusY = 0,
    focusW = 1,
    focusH = 1,
}: {
    boxes: OCRHighlightBox[];
    focusX?: number;
    focusY?: number;
    focusW?: number;
    focusH?: number;
}) {
    if (boxes.length === 0) return null;
    const hasFocus = focusW > 0.02 && focusH > 0.02;
    const fx = hasFocus ? focusX : 0;
    const fy = hasFocus ? focusY : 0;
    const fw = hasFocus ? focusW : 1;
    const fh = hasFocus ? focusH : 1;
    return (
        <>
            {boxes.map((box, i) => (
                <span
                    key={`${box.text}-${i}`}
                    className="absolute rounded-sm border-2 border-accent bg-accent-light pointer-events-none"
                    style={{
                        left: `${((box.x - fx) * 100) / fw}%`,
                        top: `${((box.y - fy) * 100) / fh}%`,
                        width: `${(box.width * 100) / fw}%`,
                        height: `${(box.height * 100) / fh}%`,
                    }}
                />
            ))}
        </>
    );
}

// ── Screenshot strip ───────────────────────────────────────────────────────

/** Full-screen preview of a moment's screenshots, with prev/next + Esc. */
export function FrameLightbox({
    frames,
    index,
    highlightKeywords,
    matchedPaths,
    bridgePort,
    onClose,
}: {
    frames: ActivityRunFrame[];
    index: number;
    highlightKeywords: string[];
    matchedPaths: string[];
    bridgePort: number;
    onClose: () => void;
}) {
    const { t } = useTranslation('activity');
    const [i, setI] = useState(index);
    const clamped = Math.max(0, Math.min(i, frames.length - 1));
    // The lightbox renders into document.body, above the Activity dialog. Its
    // own keys/clicks must NOT reach the Activity page's Escape ladder or
    // Radix's DismissableLayer, or closing the preview tears down the whole
    // dialog. Swallow keydown in the capture phase (runs before the page's
    // window listener and Radix's document listener) with
    // stopImmediatePropagation.
    useEffect(() => {
        const onKey = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                e.preventDefault();
                e.stopImmediatePropagation();
                onClose();
            } else if (e.key === 'ArrowLeft') {
                e.preventDefault();
                e.stopImmediatePropagation();
                setI((p) => Math.max(0, p - 1));
            } else if (e.key === 'ArrowRight') {
                e.preventDefault();
                e.stopImmediatePropagation();
                setI((p) => Math.min(frames.length - 1, p + 1));
            }
        };
        window.addEventListener('keydown', onKey, true);
        return () => window.removeEventListener('keydown', onKey, true);
    }, [frames.length, onClose]);
    // This overlay is portaled to document.body, outside the dialog's DOM +
    // React subtree, so Radix reads a backdrop click as "outside" and the
    // dialog's onPointerDownOutside handler would call closeActivity(). Rather
    // than fight event propagation, flag document.body while the preview is
    // open; the dialog's own dismiss handlers refuse to close while the flag is
    // set (see App.tsx / activity-page.tsx).
    useEffect(() => {
        document.body.setAttribute('data-activity-lightbox-open', 'true');
        return () =>
            document.body.removeAttribute('data-activity-lightbox-open');
    }, []);
    const frame = frames[clamped];
    const highlightBoxes = useOCRBoxes(frame?.path ?? '', highlightKeywords, !!frame && highlightKeywords.length > 0);
    if (!frame) return null;
    const src = snapshotImageURL(bridgePort, { filePath: frame.path }) ?? undefined;
    const matched = matchedPaths.includes(frame.path);

    return createPortal(
        // pointer-events-auto is REQUIRED: while the Activity Radix dialog is
        // open it sets `pointer-events: none` on <body>, and this overlay is a
        // direct child of <body> (createPortal target) so it inherits `none` —
        // the image still renders but the close/prev/next buttons receive no
        // clicks and show no cursor. Re-enabling pointer events on the root (the
        // same trick Radix content and Sonner toasts use) restores interaction.
        // Inline zIndex (not a Tailwind z-[...] class, which the JIT/HMR can fail
        // to emit, leaving the overlay stuck under the dialog) above Sonner's
        // toaster (999999999) and the Radix dialog overlay (z-50), so the preview
        // is truly on top — visible, clickable, and never covered by a toast.
        // --wails-draggable: no-drag because this fullscreen overlay covers the
        // 44px window drag band; without opting out, Wails eats clicks on the X
        // (which sits at top-4) as a window-drag and the button never fires.
        <div
            style={
                {
                    zIndex: 2147483647,
                    '--wails-draggable': 'no-drag',
                } as CSSProperties
            }
            className="pointer-events-auto fixed inset-0 flex items-center justify-center bg-code-bg/80 backdrop-blur-sm"
            onClick={onClose}
        >
            <button
                type="button"
                onClick={onClose}
                className="absolute right-4 top-4 rounded-full bg-panel/10 p-2 text-code-fg/80 hover:bg-panel/20 hover:text-code-fg"
                aria-label={t('frameLightbox.close')}
            >
                <X className="h-5 w-5" />
            </button>
            {clamped > 0 && (
                <button
                    type="button"
                    onClick={(e) => {
                        e.stopPropagation();
                        setI((p) => Math.max(0, p - 1));
                    }}
                    className="absolute left-4 rounded-full bg-panel/10 p-2 text-code-fg/80 hover:bg-panel/20 hover:text-code-fg"
                    aria-label={t('frameLightbox.previous')}
                >
                    <ChevronLeft className="h-6 w-6" />
                </button>
            )}
            {clamped < frames.length - 1 && (
                <button
                    type="button"
                    onClick={(e) => {
                        e.stopPropagation();
                        setI((p) => Math.min(frames.length - 1, p + 1));
                    }}
                    className="absolute right-4 top-1/2 -translate-y-1/2 rounded-full bg-panel/10 p-2 text-code-fg/80 hover:bg-panel/20 hover:text-code-fg"
                    aria-label={t('frameLightbox.next')}
                >
                    <ChevronRight className="h-6 w-6" />
                </button>
            )}
            <div
                onClick={(e) => e.stopPropagation()}
                className={cn(
                    'relative max-h-[90vh] max-w-[92vw] overflow-hidden rounded-lg shadow-2xl',
                    matched && 'ring-2 ring-accent',
                )}
            >
                <SnapshotImage
                    src={src}
                    alt=""
                    className="block max-h-[90vh] max-w-[92vw] object-contain"
                    placeholderClassName="h-[40vh] w-[60vw] max-w-[92vw] bg-code-bg/60 text-code-fg/50"
                    showLabel
                />
                <OCRBoxOverlay boxes={highlightBoxes} />
            </div>
            {frames.length > 1 && (
                <div className="absolute bottom-4 left-1/2 -translate-x-1/2 rounded-full bg-code-bg/60 px-3 py-1 text-xs text-code-fg/80 tabular-nums">
                    {clamped + 1} / {frames.length}
                </div>
            )}
        </div>,
        document.body,
    );
}

/** One screenshot of a moment, cropped to the focused-window rect when known.
 *  Click to preview it full-size. */
export function RunFrameThumb({
    frame,
    bridgePort,
    alt,
    highlightKeywords = [],
    matched = false,
    onClick,
}: {
    frame: ActivityRunFrame;
    bridgePort: number;
    alt: string;
    highlightKeywords?: string[];
    matched?: boolean;
    onClick: () => void;
}) {
    const src = snapshotImageURL(bridgePort, { filePath: frame.path }) ?? undefined;
    const hasFocus = frame.focusW > 0.02 && frame.focusH > 0.02;
    const boxes = useOCRBoxes(frame.path, highlightKeywords, highlightKeywords.length > 0);
    return (
        <button
            type="button"
            onClick={onClick}
            className={cn(
                'relative h-16 w-24 rounded-md overflow-hidden border border-border bg-panel shrink-0 cursor-zoom-in hover:border-accent-2/50',
                matched && 'border-accent ring-1 ring-accent',
            )}
        >
            <SnapshotImage
                src={src}
                alt={alt}
                loading="lazy"
                placeholderClassName="absolute inset-0 text-muted"
                className={hasFocus ? 'absolute max-w-none' : 'absolute inset-0 h-full w-full object-cover'}
                style={
                    hasFocus
                        ? {
                              width: `${100 / frame.focusW}%`,
                              height: `${100 / frame.focusH}%`,
                              left: `${(-frame.focusX * 100) / frame.focusW}%`,
                              top: `${(-frame.focusY * 100) / frame.focusH}%`,
                          }
                        : undefined
                }
            />
            <OCRBoxOverlay
                boxes={boxes}
                focusX={frame.focusX}
                focusY={frame.focusY}
                focusW={frame.focusW}
                focusH={frame.focusH}
            />
        </button>
    );
}
