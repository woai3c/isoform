import { useEffect, useMemo, useRef } from "react";
import { useTranslation } from "react-i18next";
import { Check, Loader2, TriangleAlert, Wrench } from "lucide-react";
import { type SopAuthorStatus } from "@/lib/wails-shim-app";
import {
    Dialog,
    DialogCloseButton,
    DialogContent,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";

interface SopTrajectoryDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    /** Live author status mirrored from `sop:author-status` by the parent. */
    authorStatus: SopAuthorStatus | null;
}

function formatEventTime(ts: number, locale: string): string {
    return new Date(ts).toLocaleTimeString(locale, {
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
    });
}

/**
 * Live trajectory viewer for the SOP author loop: the chronological stream of
 * agent events (seeding, assistant/thinking previews, tool calls, errors)
 * behind the "Distilling SOP" indicator. Read-only; updates as long as the
 * parent keeps feeding it the author status.
 */
export function SopTrajectoryDialog({
    open,
    onOpenChange,
    authorStatus,
}: SopTrajectoryDialogProps) {
    const { t, i18n } = useTranslation("app");
    const scrollRef = useRef<HTMLDivElement | null>(null);

    // Events are stored newest-first (prepended on the Go side); the
    // trajectory reads top-to-bottom chronologically.
    const events = useMemo(
        () => [...(authorStatus?.events ?? [])].reverse(),
        [authorStatus],
    );

    useEffect(() => {
        if (open) {
            scrollRef.current?.scrollTo({
                top: scrollRef.current.scrollHeight,
            });
        }
    }, [open, events.length]);

    const running = Boolean(authorStatus?.running);
    const failed = !running && authorStatus?.lastEvent === "failed";
    const saved = !running && authorStatus?.lastEvent === "saved";

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="w-[640px]">
                <DialogHeader>
                    <div className="flex min-w-0 items-center gap-2.5">
                        <DialogTitle className="truncate">
                            {t("sopsPage.trajectoryTitle")}
                        </DialogTitle>
                        {running && (
                            <span className="inline-flex shrink-0 items-center gap-1.5 text-[12px] text-muted">
                                <Loader2 className="size-3.5 animate-spin" />
                                {t("sopsPage.authoring")}
                            </span>
                        )}
                        {saved && (
                            <span className="inline-flex shrink-0 items-center gap-1.5 text-[12px] text-muted">
                                <Check className="size-3.5" />
                                {t("sopsPage.trajectoryDone")}
                            </span>
                        )}
                        {failed && (
                            <span className="inline-flex shrink-0 items-center gap-1.5 text-[12px] text-error">
                                <TriangleAlert className="size-3.5" />
                                {t("sopsPage.trajectoryFailed")}
                            </span>
                        )}
                    </div>
                    <DialogCloseButton />
                </DialogHeader>

                <div
                    ref={scrollRef}
                    className="flex max-h-[60vh] min-h-[200px] flex-col gap-2.5 overflow-y-auto px-6 pt-4 pb-5"
                >
                    {events.length === 0 ? (
                        <p className="py-8 text-center text-[13px] text-muted">
                            {t("sopsPage.trajectoryEmpty")}
                        </p>
                    ) : (
                        events.map((ev, i) => (
                            <div
                                key={`${ev.timeMs}-${i}`}
                                className="flex items-start gap-3"
                            >
                                <span className="mt-0.5 w-[64px] shrink-0 font-mono text-[11px] text-muted/80">
                                    {formatEventTime(ev.timeMs, i18n.language)}
                                </span>
                                <div className="min-w-0 flex-1">
                                    {ev.tool ? (
                                        <span className="mb-0.5 inline-flex items-center gap-1 rounded-full bg-ink/[0.05] px-1.5 py-0.5 text-[11px] font-medium text-ink/70">
                                            <Wrench className="size-3" />
                                            {ev.tool}
                                        </span>
                                    ) : null}
                                    {ev.message && (
                                        <p className="whitespace-pre-wrap break-words text-[12px] leading-relaxed text-ink/90">
                                            {ev.message}
                                        </p>
                                    )}
                                    {ev.error && (
                                        <p className="whitespace-pre-wrap break-words text-[12px] leading-relaxed text-error">
                                            {ev.error}
                                        </p>
                                    )}
                                </div>
                            </div>
                        ))
                    )}
                </div>
            </DialogContent>
        </Dialog>
    );
}
