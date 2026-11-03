import type { TFunction } from "i18next";

/** How long until a brief can be made again, in words: "in 12 minutes",
 *  "in about 2 hours", or "a little later" when unknown. */
export function waitInWords(iso: string, t: TFunction<"app">): string {
    const at = Date.parse(iso);
    const minutes = Number.isFinite(at) ? Math.ceil((at - Date.now()) / 60_000) : 0;
    if (minutes <= 0) return t("searchDialog.brief.cooldownSoon");
    if (minutes < 60) return t("searchDialog.brief.cooldownMinutes", { count: minutes });
    return t("searchDialog.brief.cooldownHours", { count: Math.round(minutes / 60) });
}
