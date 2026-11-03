import { measureToday } from "@/lib/today-telemetry";
import { useId } from "react";
import { useTranslation } from "react-i18next";
import { Pause, Play } from "lucide-react";
import { type MobileBriefAudio } from "@/lib/wails-shim-app";
import { toggleBriefPlayback, useBriefPlayback } from "@/lib/brief-audio";

/** "m:ss" for a duration in seconds; a missing duration reads as nothing. */
export function formatAudioDuration(seconds: number): { minutes: string; seconds: string } | null {
    if (!Number.isFinite(seconds) || seconds <= 0) return null;
    const total = Math.round(seconds);
    return {
        minutes: String(Math.floor(total / 60)),
        seconds: String(total % 60).padStart(2, "0"),
    };
}

/**
 * "Hear your brief" at the top of the brief's card (RC 1): the brief's own
 * spoken version, never another brief's. One control with the brief's time
 * and the duration, loading the file on the first press and pausing on the
 * next. The audio arrives as base64 from the Mac's store and plays through an
 * audio element, so the AAC or WAV file plays as stored.
 */
export function BriefAudioPlayer({ briefId, audio, caption }: {
    briefId: string;
    audio: MobileBriefAudio;
    /** What the brief is and when it was made ("Morning brief · 7:42 AM"). */
    caption?: string;
}) {
    const { t } = useTranslation("today");
    const state = useBriefPlayback(briefId);
    const metaId = useId();
    const toggle = () => toggleBriefPlayback(briefId).catch(() => {});

    const duration = formatAudioDuration(audio.durationSeconds);
    const label = state === "playing" ? t("audio.pause") : t("audio.play");
    const meta = [caption, duration ? t("audio.duration", duration) : ""].filter(Boolean).join(" · ");

    return (
        <div className="flex flex-col items-start gap-1">
            <button
                type="button"
                data-testid="brief-audio-toggle"
                aria-label={label}
                aria-describedby={meta ? metaId : undefined}
                aria-pressed={state === "playing"}
                aria-busy={state === "loading"}
                disabled={state === "loading"}
                onClick={() => void measureToday("T08", "do", "done", toggle)}
                className="group flex items-center gap-3 rounded-full pr-2 text-left outline-none disabled:cursor-wait focus-visible:ring-2 focus-visible:ring-border-focus"
            >
                <span className="patina-verdigris flex size-9 shrink-0 items-center justify-center rounded-full bg-solid-btn-bg text-solid-btn-fg shadow-[var(--shadow-solid-btn)] transition-opacity group-hover:opacity-90">
                    {state === "playing" ? (
                        <Pause className="size-3.5" fill="currentColor" />
                    ) : (
                        <Play className="size-3.5 translate-x-px" fill="currentColor" />
                    )}
                </span>
                <span className="flex flex-col">
                    <span className="text-[13px] font-semibold text-ink">{label}</span>
                    {meta && <span id={metaId} className="text-[12px] tabular-nums text-muted">{meta}</span>}
                </span>
            </button>
            {state === "failed" && (
                <span role="alert" className="text-[12px] text-muted">
                    {t("audio.failed")}
                </span>
            )}
        </div>
    );
}
