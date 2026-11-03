import { useTranslation } from "react-i18next";
import { Loader2 } from "lucide-react";
import type { PlaybookView, SopEntry } from "@/lib/wails-shim-app";

interface PlaybookSuggestionCardProps {
    entry: SopEntry;
    plan: PlaybookView;
    busy: boolean;
    onAccept: () => void;
    onDecline: () => void;
    /** Opens the playbook's steps; left out where they are already shown. */
    onSee?: () => void;
}

/** Joins page names the way the language lists them. */
export function listNames(names: string[], language: string): string {
    try {
        return new Intl.ListFormat(language, { style: "long", type: "conjunction" }).format(names);
    } catch {
        return names.join(", ");
    }
}

/** The host a site address names, for "opens pages on us.posthog.com". */
export function siteHost(site: string): string {
    try {
        return new URL(site).host;
    } catch {
        return site;
    }
}

/**
 * A routine Isoform noticed (playbook_routine.go) and offers to take on: what
 * it saw, in plain words, and a one-tap yes. Accepting schedules the playbook,
 * allows its site and sends the result to a notification and a text.
 */
export function PlaybookSuggestionCard({ entry, plan, busy, onAccept, onDecline, onSee }: PlaybookSuggestionCardProps) {
    const { t, i18n } = useTranslation("app");
    const proposal = plan.proposal;
    if (!proposal) return null;
    const pages = (proposal.titles ?? []).filter(Boolean);
    const runTime = plan.suggestedTime || proposal.time;
    const host = proposal.host || siteHost((entry.sites ?? [])[0] ?? "");
    return (
        <section
            aria-label={t("sopsPage.suggestion.eyebrow")}
            className="mb-6 rounded-2xl border border-border-input bg-panel px-5 py-4 shadow-[var(--shadow-soft)]"
        >
            <p className="mb-1.5 text-[12px] font-medium text-accent">{t("sopsPage.suggestion.eyebrow")}</p>
            <h2 className="mb-2 text-[17px] font-medium text-ink">{entry.title}</h2>
            <p className="text-[14px] leading-relaxed text-ink">
                {t("sopsPage.suggestion.seen", { site: proposal.siteName, time: proposal.time, days: proposal.days })}
                {pages.length > 0 && <> {t("sopsPage.suggestion.pages", { pages: listNames(pages, i18n.language) })}</>}
            </p>
            <p className="mt-1 text-[14px] leading-relaxed text-ink">
                {proposal.weekdays
                    ? t("sopsPage.suggestion.offerWeekdays", { runTime })
                    : t("sopsPage.suggestion.offerDaily", { runTime })}
            </p>
            {host && <p className="mt-1.5 text-[12px] leading-relaxed text-muted">{t("sopsPage.suggestion.reads", { host })}</p>}
            <div className="mt-4 flex flex-wrap items-center gap-2">
                <button
                    type="button"
                    disabled={busy}
                    onClick={onAccept}
                    className="inline-flex h-9 items-center gap-1.5 rounded-lg patina-verdigris bg-solid-btn-bg px-4 text-[13px] font-semibold text-solid-btn-fg shadow-[var(--shadow-solid-btn)] transition-opacity hover:opacity-90 disabled:opacity-60 cursor-pointer"
                >
                    {busy && <Loader2 className="size-3.5 animate-spin" />}
                    {proposal.weekdays ? t("sopsPage.suggestion.acceptWeekdays") : t("sopsPage.suggestion.acceptDaily")}
                </button>
                {onSee && (
                    <button
                        type="button"
                        disabled={busy}
                        onClick={onSee}
                        className="inline-flex h-9 items-center rounded-lg border border-border-input px-3.5 text-[13px] font-medium text-ink transition-colors hover:bg-ink/[0.04] disabled:opacity-60 cursor-pointer"
                    >
                        {t("sopsPage.suggestion.see")}
                    </button>
                )}
                <button
                    type="button"
                    disabled={busy}
                    onClick={onDecline}
                    className="inline-flex h-9 items-center rounded-lg px-3 text-[13px] font-medium text-muted transition-colors hover:bg-ink/[0.04] hover:text-ink disabled:opacity-60 cursor-pointer"
                >
                    {t("sopsPage.suggestion.notNow")}
                </button>
            </div>
        </section>
    );
}
