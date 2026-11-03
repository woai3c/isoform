import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { GetFindTimeDetails, GetFindTimeDetailsForRange, type FindTimeDetails } from "@/lib/wails-shim-app";

export function FindTimeAnswer({ range }: { range?: { first: string; last: string; minutes: number; nextWeek?: boolean } }) {
    const { t, i18n } = useTranslation("chat");
    const [details, setDetails] = useState<FindTimeDetails | null>(null);
    const [failed, setFailed] = useState(false);
    useEffect(() => {
        let active = true;
        setDetails(null);
        setFailed(false);
        const read = range ? GetFindTimeDetailsForRange(range.first, range.last, range.minutes) : GetFindTimeDetails();
        void read.then((result) => {
            if (active) setDetails(result);
        }).catch(() => { if (active) setFailed(true); });
        return () => { active = false; };
    }, [range?.first, range?.last, range?.minutes]);
    if (failed) return <p role="status" className="text-[13px] text-muted">{t("mainChat.proactive.findTimeFailed")}</p>;
    if (!details) return <p role="status" className="text-[13px] text-muted">{t("mainChat.proactive.findTimeChecking")}</p>;
    const language = i18n.resolvedLanguage ?? i18n.language;
    const dayTime = new Intl.DateTimeFormat(language, { weekday: "long", hour: "numeric", minute: "2-digit" });
    const clock = new Intl.DateTimeFormat(language, { hour: "numeric", minute: "2-digit" });
    const times = details.ranges.flatMap((range) => {
        const start = new Date(range.start), end = new Date(range.end);
        return Number.isFinite(start.getTime()) && Number.isFinite(end.getTime()) && end > start
            ? [t("mainChat.proactive.findTimeRange", { start: dayTime.format(start), end: clock.format(end) })] : [];
    });
    const list = new Intl.ListFormat(language, { style: "long", type: "conjunction" }).format(times);
    const names = new Intl.ListFormat(language, { style: "long", type: "conjunction" }).format(details.checkedCalendars);
    const browser = new Intl.ListFormat(language, { style: "long", type: "conjunction" }).format(details.browserOnly);
    const answer = times.length === 0
        ? details.browserOnly.length && names ? t("mainChat.proactive.findTimeNoneChecked", { calendars: names })
            : details.browserOnly.length ? t("mainChat.proactive.findTimeNoneCheckedGeneric")
                : t(range?.nextWeek ? "mainChat.proactive.findTimeNextWeekNone" : "mainChat.proactive.findTimeNone")
        : details.browserOnly.length && names ? t("mainChat.proactive.findTimeChecked", { calendars: names, times: list })
            : details.browserOnly.length ? t("mainChat.proactive.findTimeCheckedGeneric", { times: list })
                : t(range?.nextWeek ? "mainChat.proactive.findTimeNextWeek" : "mainChat.proactive.findTimeAnswer", { times: list });
    return <p role="status" className="text-[13px] leading-relaxed text-ink">{answer}{browser && ` ${t("mainChat.proactive.findTimeBrowserOnly", { calendars: browser })}`}</p>;
}
