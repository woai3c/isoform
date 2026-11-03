import {
    createContext,
    useCallback,
    useContext,
    useEffect,
    useState,
    type ReactNode,
    type RefObject,
} from "react";
import { useTranslation } from "react-i18next";
import { ChevronDown } from "lucide-react";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";

// ── Advanced disclosure ──
//
// The one place on a Settings page where the less common controls live, so the
// page opens with the everyday choices and the rest is one click away. Built
// from the same Collapsible pattern as the Advanced toggle in
// model-accounts-card.tsx. Each page remembers whether its Advanced section is
// open; storage can be unavailable (private window, blocked site data), so
// every read and write is guarded and the disclosure still works without it.

const STORAGE_PREFIX = "yansu.settings.advanced.";

export function advancedStorageKey(pageId: string): string {
    return `${STORAGE_PREFIX}${pageId}`;
}

function readStoredOpen(pageId: string): boolean | null {
    try {
        const raw = window.localStorage.getItem(advancedStorageKey(pageId));
        if (raw === "1") return true;
        if (raw === "0") return false;
        return null;
    } catch {
        return null;
    }
}

function writeStoredOpen(pageId: string, open: boolean): void {
    try {
        window.localStorage.setItem(advancedStorageKey(pageId), open ? "1" : "0");
    } catch {
        // Storage unavailable: the disclosure still opens and closes, it just
        // will not be remembered next time.
    }
}

/** Non-zero when Settings opened this page asking for Advanced: a sidebar
 *  search that matched something kept there, or a deep link such as
 *  openSettings("ai-provider", { advanced: true }). Settings hands out a new
 *  number for every such navigation, and the section opens each time the
 *  number changes, even on the page already open after the person collapsed
 *  it. What is remembered for the page does not change. 0 means no reveal. */
export const SettingsAdvancedRevealContext = createContext(0);

// ── Anchors ──
//
// A deep link can name an element to scroll to (openSettings(topic,
// { anchor })). Pages mark that element with settingsAnchor(); the Advanced
// disclosure is always marked, so a link that only asks for Advanced lands
// on it.

const ANCHOR_ATTRIBUTE = "data-settings-anchor";

/** Anchor of the Advanced disclosure on every page. */
export const SETTINGS_ADVANCED_ANCHOR = "advanced";

/** Spread on the element a deep link may scroll to:
 *  `<div {...settingsAnchor("search-index")}>`. */
export function settingsAnchor(id: string): { [ANCHOR_ATTRIBUTE]: string } {
    return { [ANCHOR_ATTRIBUTE]: id };
}

/** The first element under `root` marked with this anchor. */
export function findSettingsAnchor(root: ParentNode | null | undefined, anchor: string): HTMLElement | null {
    if (!root) return null;
    for (const node of Array.from(root.querySelectorAll<HTMLElement>(`[${ANCHOR_ATTRIBUTE}]`))) {
        if (node.getAttribute(ANCHOR_ATTRIBUTE) === anchor) return node;
    }
    return null;
}

const ANCHOR_POLL_MS = 50;
const ANCHOR_WAIT_MS = 2000;

/**
 * Scrolls the anchored element under `rootRef` into view each time `trigger`
 * changes while `anchor` is set. Pages load their data and open Advanced
 * after the first render, so it keeps looking for a short while before
 * giving up.
 */
export function useSettingsAnchorScroll(
    rootRef: RefObject<HTMLElement>,
    anchor: string | undefined,
    trigger: unknown,
): void {
    useEffect(() => {
        if (!anchor) return;
        const deadline = Date.now() + ANCHOR_WAIT_MS;
        let timer: number | undefined;
        const seek = () => {
            const target = findSettingsAnchor(rootRef.current, anchor);
            if (target) {
                target.scrollIntoView?.({ block: "start" });
                return;
            }
            if (Date.now() < deadline) timer = window.setTimeout(seek, ANCHOR_POLL_MS);
        };
        seek();
        return () => window.clearTimeout(timer);
    }, [rootRef, anchor, trigger]);
}

export interface SettingsAdvancedProps {
    /** Settings page id; the open state is remembered per page. */
    pageId: string;
    /** Optional one-line summary of what is inside, shown next to the label. */
    hint?: string;
    /** Open state used when nothing is remembered for this page yet. */
    defaultOpen?: boolean;
    children: ReactNode;
}

export function SettingsAdvanced({
    pageId,
    hint,
    defaultOpen = false,
    children,
}: SettingsAdvancedProps) {
    const { t } = useTranslation("settings");
    const reveal = useContext(SettingsAdvancedRevealContext);
    const [open, setOpen] = useState<boolean>(() => reveal > 0 || (readStoredOpen(pageId) ?? defaultOpen));

    // A search or deep link can land on the page that is already open, and
    // can ask again after the person collapsed the section.
    useEffect(() => {
        if (reveal > 0) setOpen(true);
    }, [reveal]);

    const handleOpenChange = useCallback(
        (next: boolean) => {
            setOpen(next);
            writeStoredOpen(pageId, next);
        },
        [pageId],
    );

    return (
        <Collapsible
            open={open}
            onOpenChange={handleOpenChange}
            className="flex flex-col border-t border-border pt-4"
            {...settingsAnchor(SETTINGS_ADVANCED_ANCHOR)}
        >
            <CollapsibleTrigger className="flex items-center gap-1.5 self-start text-left text-xs text-muted hover:text-ink transition-colors">
                <ChevronDown
                    aria-hidden
                    className={`h-3 w-3 shrink-0 transition-transform ${open ? "rotate-180" : ""}`}
                />
                <span className="font-medium">{t("advanced.label")}</span>
                {hint && <span className="text-muted">{hint}</span>}
            </CollapsibleTrigger>
            <CollapsibleContent className="flex flex-col gap-6 pt-4">{children}</CollapsibleContent>
        </Collapsible>
    );
}
