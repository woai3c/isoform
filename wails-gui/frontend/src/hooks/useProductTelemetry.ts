import { useEffect, useRef, useState } from "react";
import { GetTelemetryTier, RecordProductActivity } from "../../wailsjs/go/main/App";
import { EventsOn } from "../../wailsjs/runtime/runtime";

export const PRODUCT_HEARTBEAT_MS = 30_000;
export const PRODUCT_IDLE_MS = 5 * 60_000;

/** Start closed, including when the preference cannot be read. Go checks it
 * again at capture and send time, so renderer state never authorizes a send. */
export function useProductTelemetrySharing(): boolean {
    const [sharing, setSharing] = useState(false);
    useEffect(() => {
        let cancelled = false;
        let changed = false;
        const allowed = (tier: string) => tier === "basic" || tier === "detailed";
        void GetTelemetryTier().then((tier) => {
            if (!cancelled && !changed) setSharing(allowed(tier));
        }).catch(() => {});
        const unsubscribe = EventsOn("telemetry:tier-changed", (tier: string) => {
            changed = true;
            setSharing(allowed(tier));
        });
        return () => {
            cancelled = true;
            unsubscribe?.();
        };
    }, []);
    return sharing;
}

export function productWindowActive(): boolean {
    return document.visibilityState === "visible" && document.hasFocus();
}

function newActivitySession(): string | null {
    if (typeof crypto === "undefined") return null;
    try {
        if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
        if (typeof crypto.getRandomValues !== "function") return null;
        // randomUUID is limited to secure contexts on older WebViews.
        // getRandomValues still provides the entropy for an RFC 4122 v4 UUID.
        const bytes = crypto.getRandomValues(new Uint8Array(16));
        bytes[6] = (bytes[6] & 0x0f) | 0x40;
        bytes[8] = (bytes[8] & 0x3f) | 0x80;
        const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
        return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
    } catch {
        // Telemetry must never prevent the application from mounting.
        return null;
    }
}

/** Reports only elapsed foreground time and a random activity-session ID.
 * Input handlers never read a key, pointer position, target or page content. */
export function useProductTelemetry(): void {
    const sharing = useProductTelemetrySharing();
    const sharingRef = useRef(sharing);
    sharingRef.current = sharing;

    useEffect(() => {
        if (!sharing) return;
        let focused = productWindowActive();
        let lastInput = Date.now();
        let accountedAt = lastInput;
        let sessionID = newActivitySession();
        if (!sessionID) return;
        let suspended = false;

        const record = (seconds: number) => {
            if (sharingRef.current && sessionID) {
                void RecordProductActivity(seconds, sessionID).catch(() => {});
            }
        };
        const flush = () => {
            const now = Date.now();
            if (now - accountedAt > 2 * PRODUCT_HEARTBEAT_MS) {
                // Two missed heartbeats indicate a suspended webview. Drop
                // the whole gap and wait for focus or input to resume.
                suspended = true;
                accountedAt = now;
                return;
            }
            if (focused && !suspended) {
                const end = Math.min(now, lastInput + PRODUCT_IDLE_MS);
                // Suspended webviews never backfill the sleep gap.
                const elapsed = Math.min(PRODUCT_HEARTBEAT_MS, end - accountedAt);
                if (elapsed > 0) record(elapsed / 1_000);
            }
            accountedAt = now;
        };
        const resume = () => {
            const now = Date.now();
            if (suspended || now - lastInput >= PRODUCT_IDLE_MS) {
                sessionID = newActivitySession();
                record(0);
            }
            suspended = false;
            lastInput = now;
            accountedAt = now;
        };
        const syncFocus = () => {
            const next = productWindowActive();
            if (next === focused && !(next && (suspended || Date.now() - accountedAt > 2 * PRODUCT_HEARTBEAT_MS))) return;
            flush();
            focused = next;
            if (focused) resume();
        };
        const input = () => {
            if (!productWindowActive()) return;
            const now = Date.now();
            if (!focused || suspended || now - lastInput >= PRODUCT_IDLE_MS || now - accountedAt > 2 * PRODUCT_HEARTBEAT_MS) {
                flush();
                focused = true;
                resume();
                return;
            }
            lastInput = now;
        };
        const hide = () => {
            flush();
            focused = false;
        };

        if (focused) record(0);
        const timer = window.setInterval(flush, PRODUCT_HEARTBEAT_MS);
        window.addEventListener("focus", syncFocus);
        window.addEventListener("blur", syncFocus);
        window.addEventListener("pagehide", hide);
        window.addEventListener("beforeunload", hide);
        document.addEventListener("visibilitychange", syncFocus);
        const inputEvents = ["pointerdown", "pointermove", "keydown", "wheel", "touchstart"] as const;
        for (const name of inputEvents) window.addEventListener(name, input, { passive: true });

        return () => {
            window.clearInterval(timer);
            window.removeEventListener("focus", syncFocus);
            window.removeEventListener("blur", syncFocus);
            window.removeEventListener("pagehide", hide);
            window.removeEventListener("beforeunload", hide);
            document.removeEventListener("visibilitychange", syncFocus);
            for (const name of inputEvents) window.removeEventListener(name, input);
            flush();
        };
    }, [sharing]);
}
