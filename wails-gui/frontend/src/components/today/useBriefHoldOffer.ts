import { useEffect, useState } from "react";
import { DismissHoldOffer, GetHoldSettings, ProposeHold, UndoDismissHoldOffer, type MobileBriefDocument } from "@/lib/wails-shim-app";
import { onHoldSettingsChanged } from "@/lib/hold-settings-events";
import { isCommitmentItemKey } from "./commitment-key";
import type { HoldCardSource } from "./HoldCard";

/** A closing is eligible only when its references identify one readable commitment. */
export function briefHoldSource(brief: MobileBriefDocument | null): HoldCardSource | null {
    const closing = brief?.narrative?.at(-1);
    if (!brief || !closing || !/[?？]\s*$/.test(closing.text) || !/\bfind (?:a |some )?time\b|找(?:个|一个|些)?时间|找.*时段/i.test(closing.text)) return null;
    const refs = new Set(closing.refs ?? []);
    const matches = (brief.sections.commitments ?? []).filter((item) => !item.evidenceUnavailable && isCommitmentItemKey(item.key) && item.refs?.some((ref) => refs.has(ref)));
    if (matches.length !== 1) return null;
    const item = matches[0];
    return { thing: { title: item.title, sourceKind: "brief", sourceId: item.key! }, due: item.dueAt, reason: item.attentionReason };
}

function localDay(value: string, timeZone?: string): string {
    const date = new Date(value);
    if (!Number.isFinite(date.getTime())) return "";
    const format = (zone?: string) => new Intl.DateTimeFormat("en-CA", { timeZone: zone, year: "numeric", month: "2-digit", day: "2-digit" }).formatToParts(date);
    let parts: Intl.DateTimeFormatPart[];
    try { parts = format(timeZone); } catch { parts = format(); }
    return ["year", "month", "day"].map((part) => parts.find((entry) => entry.type === part)?.value).join("-");
}

export function useBriefHoldOffer(brief: MobileBriefDocument | null, enabled: boolean, day: string, timeZone?: string) {
    const source = enabled ? briefHoldSource(brief) : null;
    const [offer, setOffer] = useState<HoldCardSource | null>(null);
    const [dismissal, setDismissal] = useState<string | null>(null);
    const [shared, setShared] = useState(false);
    const [version, setVersion] = useState(0);
    useEffect(() => onHoldSettingsChanged(() => setVersion((value) => value + 1)), []);
    useEffect(() => {
        let active = true;
        setOffer(null); setDismissal(null); setShared(false);
        if (!source || !day) return () => { active = false; };
        void GetHoldSettings().then(async (settings) => {
            if (!settings.granted || !settings.offerHolds || !settings.calendarId || settings.browserOnly?.length) return;
            const proposal = await ProposeHold(source.thing, source.due ?? "", 0);
            if (!active || proposal.dismissed || proposal.existing) return;
            const slot = proposal.slots.find((candidate) => localDay(candidate.start, timeZone) === day);
            if (slot) { setShared(!!settings.calendars?.find((calendar) => calendar.id === settings.calendarId)?.meetingsAccount); setOffer({ ...source, preferred: slot }); }
        }).catch(() => { /* Keep the original question when availability is unknown. */ });
        return () => { active = false; };
    }, [source?.thing.sourceId, source?.thing.title, source?.due, source?.reason, day, timeZone, version]);
    useEffect(() => {
        if (!dismissal) return;
        const timer = window.setTimeout(() => { setOffer(null); setDismissal(null); }, 5000);
        return () => window.clearTimeout(timer);
    }, [dismissal]);
    const dismiss = () => {
        if (offer) void DismissHoldOffer(offer.thing).then(setDismissal).catch(() => setOffer(null));
    };
    const undo = () => {
        if (dismissal) void UndoDismissHoldOffer(dismissal).then(() => setDismissal(null)).catch(() => { setOffer(null); setDismissal(null); });
    };
    const current = source && offer?.thing.sourceId === source.thing.sourceId && offer.thing.title === source.thing.title && offer.preferred && localDay(offer.preferred.start, timeZone) === day ? offer : null;
    return { offer: current, shared: !!current && shared, dismissal: current ? dismissal : null, dismiss, undo };
}
