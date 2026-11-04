import type { TFunction } from "i18next";

const WINDOW = /^(\d+)[\s-]*(h|hr|hrs|hour|hours|d|day|days|w|week|weeks)$/;

/**
 * A usage window as people say it: "7 days · Fable" is "weekly limit", one day
 * is "daily limit", five hours "5-hour limit". The model's name after "·" is
 * never shown. Null when the label doesn't name a length.
 */
export function usageWindowLimit(label: string, t: TFunction<"settings">): string | null {
    const base = label.split("·")[0].trim().toLowerCase();
    if (base === "weekly") return t("usageWindow.weekly");
    if (base === "daily") return t("usageWindow.daily");
    const match = WINDOW.exec(base);
    if (!match) return null;
    const count = Number(match[1]);
    const unit = match[2][0];
    const hours = unit === "h" ? count : unit === "d" ? count * 24 : count * 24 * 7;
    if (hours === 24 * 7) return t("usageWindow.weekly");
    if (hours === 24) return t("usageWindow.daily");
    if (hours < 24) return t("usageWindow.hours", { count: hours });
    if (hours % 24 === 0) return t("usageWindow.days", { count: hours / 24 });
    return t("usageWindow.hours", { count: hours });
}
