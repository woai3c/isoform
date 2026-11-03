import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Lightbulb, Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import type { TodaySuggestionDecisionKind, TodaySuggestionView } from "@/lib/wails-shim-app";

/**
 * The one suggestion a week (docs/notifications-design.md section 8): the
 * pattern Isoform saw, the evidence, what accepting creates, and a dismiss
 * that asks why. It uses the same frame as the other Today cards.
 */
export function SuggestionCard({
    suggestion,
    busy,
    failed,
    onResolve,
}: {
    suggestion: TodaySuggestionView;
    busy: boolean;
    failed: boolean;
    onResolve: (decision: TodaySuggestionDecisionKind, reason?: string) => Promise<boolean>;
}) {
    const { t } = useTranslation("today");
    const tr = (key: string, options?: Record<string, unknown>) =>
        t(key as never, options as never) as unknown as string;
    const [dismissing, setDismissing] = useState(false);
    const [reason, setReason] = useState("");
    const [done, setDone] = useState(false);

    const act = async (decision: TodaySuggestionDecisionKind, why?: string) => {
        const ok = await onResolve(decision, why);
        if (ok) {
            setDone(true);
            setDismissing(false);
        }
    };

    const acceptLabel = suggestion.schedule === "repeat" ? tr("suggestion.addToUpcoming") : tr("suggestion.doOnce");
    const acceptDecision: TodaySuggestionDecisionKind = suggestion.schedule === "repeat" ? "add_to_upcoming" : "do_once";
    const outcome =
        suggestion.schedule === "repeat"
            ? suggestion.scheduleDescription
                ? tr("suggestion.repeat", { schedule: suggestion.scheduleDescription })
                : tr("suggestion.repeatNoDetail")
            : tr("suggestion.once");

    return (
        <div className="rounded-2xl border border-border bg-background p-4" data-testid="suggestion-card">
            <div className="flex items-center gap-1.5 text-[10px] font-semibold uppercase tracking-wider text-muted">
                <Lightbulb className="size-3.5" />
                <span>{tr("suggestion.kicker")}</span>
                <span className="opacity-40">{"·"}</span>
                <span>{tr("suggestion.seen", { count: suggestion.times, days: suggestion.days })}</span>
            </div>
            <h3 className="mt-2 text-[15px] font-semibold leading-snug text-ink">{suggestion.title}</h3>
            {suggestion.description && (
                <p className="mt-1 text-[13px] leading-relaxed text-muted">{suggestion.description}</p>
            )}
            {suggestion.evidence.length > 0 && (
                <div className="mt-3 rounded-lg bg-ink/[0.03] px-3 py-2.5">
                    <div className="text-[10px] font-semibold uppercase tracking-wider text-muted">
                        {tr("suggestion.evidenceLabel")}
                    </div>
                    <ul className="mt-1.5 space-y-1">
                        {suggestion.evidence.map((line, index) => (
                            <li key={index} className="text-[12px] leading-snug text-ink/80">
                                {line}
                            </li>
                        ))}
                    </ul>
                </div>
            )}
            <p className="mt-3 text-[12px] text-muted">{outcome}</p>

            {done ? (
                <p className="mt-3 text-[12px] text-muted" role="status">
                    {tr("suggestion.done")}
                </p>
            ) : dismissing ? (
                <div className="mt-3 flex flex-col gap-2">
                    <label htmlFor={`suggestion-reason-${suggestion.id}`} className="text-[12px] font-medium text-ink">
                        {tr("suggestion.reasonLabel")}
                    </label>
                    <Textarea
                        id={`suggestion-reason-${suggestion.id}`}
                        value={reason}
                        onChange={(event) => setReason(event.target.value)}
                        placeholder={tr("suggestion.reasonPlaceholder")}
                        rows={2}
                        className="text-[13px]"
                    />
                    <div className="flex flex-wrap items-center gap-2">
                        <Button size="sm" disabled={busy || !reason.trim()} onClick={() => void act("dismiss", reason.trim())}>
                            {busy && <Loader2 className="size-3.5 animate-spin" />}
                            {tr("suggestion.confirmDismiss")}
                        </Button>
                        <Button size="sm" variant="ghost" disabled={busy} onClick={() => setDismissing(false)}>
                            {tr("suggestion.cancel")}
                        </Button>
                    </div>
                </div>
            ) : (
                <div className="mt-3 flex flex-wrap items-center gap-2">
                    <Button size="sm" disabled={busy} onClick={() => void act(acceptDecision)}>
                        {busy && <Loader2 className="size-3.5 animate-spin" />}
                        {acceptLabel}
                    </Button>
                    <Button size="sm" variant="ghost" disabled={busy} onClick={() => void act("not_now")}>
                        {tr("suggestion.notNow")}
                    </Button>
                    <Button size="sm" variant="ghost" disabled={busy} onClick={() => setDismissing(true)}>
                        {tr("suggestion.dismiss")}
                    </Button>
                </div>
            )}
            {failed && (
                <p role="alert" className="mt-2 text-[12px] text-muted">
                    {tr("suggestion.failed")}
                </p>
            )}
        </div>
    );
}
