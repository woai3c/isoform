import type React from "react";
import { useTranslation } from "@/i18n";
import type { DetectionContext, Suggestion } from "../api/types";

interface Props {
    detection: DetectionContext | null;
    topSuggestion: Suggestion | null;
    onActivate?: (suggestion: Suggestion) => void;
}

/**
 * Detection chip rendered inside the pill (image 1):
 *   "[icon] {appName} – Detected"
 * The label intentionally omits the top suggestion's title — that
 * surfaced as duplicate, distracting text inside the chip (the
 * suggestion's own prompt already populates the textarea
 * placeholder, so showing it twice is noise). Falls back to a
 * neutral "Isoform" label when no app context was supplied.
 *
 * Clicking a chip with a top suggestion still fills the composer
 * via onActivate; the chip is just disabled (non-clickable) when
 * no suggestion exists for that app.
 */
export function DetectionChip({ detection, topSuggestion, onActivate }: Props) {
    const { t } = useTranslation("chat");
    if (!detection || !detection.appName) {
        return (
            <div className="detection-chip detection-chip--neutral" aria-hidden>
                <span className="detection-chip__yansu">{t("quickChat.brand")}</span>
            </div>
        );
    }

    const label = t("quickChat.detected", { app: detection.appName });

    return (
        <button
            type="button"
            className="detection-chip"
            onClick={() => topSuggestion && onActivate?.(topSuggestion)}
            disabled={!topSuggestion}
            style={{ "--wails-draggable": "no-drag" } as React.CSSProperties}
            aria-label={label}
        >
            <span className="detection-chip__icon" aria-hidden>
                <AppIcon name={detection.appName} />
            </span>
            <span className="detection-chip__label">{label}</span>
        </button>
    );
}

function AppIcon({ name }: { name: string }) {
    // Tiny inline SVG seed icons. Real product would map common app
    // names → vendor logos; for v1 we render a colored letter tile so
    // every app gets *something*.
    const initial = name.trim().charAt(0).toUpperCase() || "?";
    return (
        <svg width="14" height="14" viewBox="0 0 14 14" fill="none">
            <rect x="0.5" y="0.5" width="13" height="13" rx="3" fill="currentColor" opacity="0.18" />
            <text
                x="7"
                y="10"
                textAnchor="middle"
                fontSize="9"
                fontWeight="700"
                fill="currentColor"
            >
                {initial}
            </text>
        </svg>
    );
}
