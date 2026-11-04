import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Loader2 } from "lucide-react";
import { ActivityInlineNotice, errorNotice, type ActivityNotice } from "@/components/activity-inline-notice";
import {
    SetPlaybookSchedule,
    type PlaybookChannels,
    type PlaybookView,
    type SopEntry,
} from "@/lib/wails-shim-app";
import { Checkbox } from "@/components/ui/checkbox";
import { Switch } from "@/components/ui/switch";
import { siteHost } from "@/components/sops/PlaybookSuggestionCard";

// The channels a result can go to, in display order. Mirrors
// engagement.ChoosableChannels in wails-gui/internal/engagement/channel.go.
const CHANNELS = ["desktop", "push", "text", "email"] as const;
type ChannelId = (typeof CHANNELS)[number];

const CHANNEL_LABEL_KEYS = {
    desktop: "sopsPage.delivery.channelDesktop",
    push: "sopsPage.delivery.channelPush",
    text: "sopsPage.delivery.channelText",
    email: "sopsPage.delivery.channelEmail",
} as const;

const NOT_SET_UP_KEYS = {
    push: "sopsPage.delivery.notSetUpPush",
    text: "sopsPage.delivery.notSetUpText",
    email: "sopsPage.delivery.notSetUpEmail",
} as const;

/** Whether a refusal says the playbook's pages changed since they were shown (errPlaybookSitesChanged). */
export function isSitesChanged(err: unknown): boolean {
    return String(err).includes("pages this playbook opens changed");
}

/** Whether a refusal says the playbook's steps wait for approval (errPlaybookStepsUnapproved). */
export function isStepsUnapproved(err: unknown): boolean {
    return String(err).includes("steps changed before approval was added");
}

interface PlaybookDeliveryPanelProps {
    entry: SopEntry;
    plan: PlaybookView | null;
    channels: PlaybookChannels | null;
    defaultChannels: string[];
    onSaved: (view: PlaybookView) => void;
    /** The plans didn't load: say so with Try again, and keep Save off (W21). */
    schedulesFailed?: boolean;
    onRetrySchedules?: () => void;
}

function formatWhen(ms: number, language: string): string {
    if (!ms) return "";
    return new Date(ms).toLocaleString(language, {
        weekday: "short",
        hour: "numeric",
        minute: "2-digit",
    });
}

/**
 * When a playbook runs by itself and where its result goes. Saving approves
 * the pages the playbook names: they are listed here, and a run may open only
 * those (playbook_run.go).
 */
export function PlaybookDeliveryPanel({
    entry,
    plan,
    channels,
    defaultChannels,
    onSaved,
    schedulesFailed = false,
    onRetrySchedules,
}: PlaybookDeliveryPanelProps) {
    const { t, i18n } = useTranslation("app");
    // The saved values, taken from the plan when another playbook opens and
    // after a save. A background reload of the same playbook leaves both
    // them and the user's unsaved edits alone.
    const fromPlan = (p: PlaybookView | null) => ({
        enabled: p?.schedule?.enabled ?? false,
        time: p?.schedule?.time || p?.suggestedTime || "08:55",
        weekdays: p?.schedule?.weekdays ?? p?.proposal?.weekdays ?? false,
        channels: new Set<string>(p?.channels?.length ? p.channels : defaultChannels),
    });
    const [initial, setInitial] = useState(() => fromPlan(plan));
    const [enabled, setEnabled] = useState(initial.enabled);
    const [time, setTime] = useState(initial.time);
    const [weekdays, setWeekdays] = useState(initial.weekdays);
    const [chosen, setChosen] = useState<Set<string>>(initial.channels);
    const [saving, setSaving] = useState(false);

    const reset = (next: ReturnType<typeof fromPlan>) => {
        setInitial(next);
        setEnabled(next.enabled);
        setTime(next.time);
        setWeekdays(next.weekdays);
        setChosen(next.channels);
    };
    const shownId = useRef(entry.id);
    const loadedPlan = useRef(Boolean(plan));
    useEffect(() => {
        // Another playbook, or this playbook's plan arriving for the first
        // time, resets the panel; later reloads of the same plan do not.
        if (shownId.current !== entry.id || (!loadedPlan.current && plan)) {
            shownId.current = entry.id;
            loadedPlan.current = Boolean(plan);
            reset(fromPlan(plan));
        }
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [entry.id, plan]);

    const available = (id: ChannelId): boolean => (channels ? channels[id] : true);
    // A schedule saved before steps were approved waits for a save, which
    // approves the steps shown now.
    const held = plan?.held === "unapproved";
    const sites = entry.sites ?? [];
    const dirty =
        enabled !== initial.enabled ||
        time !== initial.time ||
        weekdays !== initial.weekdays ||
        chosen.size !== initial.channels.size ||
        [...chosen].some((c) => !initial.channels.has(c));

    const toggleChannel = (id: ChannelId, on: boolean) => {
        setChosen((prev) => {
            const next = new Set(prev);
            if (on) next.add(id);
            else next.delete(id);
            return next;
        });
    };

    // In place (W10): a muted line when no place is chosen, copper with Try again on failure.
    const [notice, setNotice] = useState<ActivityNotice | null>(null);
    const save = () => {
        if (chosen.size === 0) {
            setNotice({ tone: "done", text: t("sopsPage.delivery.needChannel") });
            return;
        }
        setNotice(null);
        setSaving(true);
        SetPlaybookSchedule({
            id: entry.id,
            enabled,
            time,
            weekdays,
            channels: CHANNELS.filter((c) => chosen.has(c)),
            sites,
        })
            .then((view) => {
                reset(fromPlan(view));
                onSaved(view);
            })
            .catch((err) => {
                setNotice(errorNotice(
                    isSitesChanged(err) ? t("sopsPage.delivery.sitesChanged") : t("sopsPage.delivery.saveFailed"),
                    save,
                ));
            })
            .finally(() => setSaving(false));
    };

    return (
        <section
            aria-label={t("sopsPage.delivery.title")}
            className="mb-8 rounded-2xl border border-border-input bg-panel px-5 py-4"
        >
            <h2 className="mb-3 text-[15px] font-medium text-ink">{t("sopsPage.delivery.title")}</h2>
            {schedulesFailed && (
                <ActivityInlineNotice notice={errorNotice(t("sopsPage.delivery.scheduleFailed"), onRetrySchedules)} className="mb-3" />
            )}

            <div className="flex flex-wrap items-center gap-x-5 gap-y-3">
                <label className="flex items-center gap-2.5 text-[13px] text-ink">
                    <Switch
                        aria-label={t("sopsPage.delivery.scheduleLabel")}
                        checked={enabled}
                        onCheckedChange={(checked) => setEnabled(checked)}
                    />
                    {t("sopsPage.delivery.scheduleLabel")}
                </label>
                {enabled && (
                    <>
                        <label className="flex items-center gap-2 text-[13px] text-muted">
                            {t("sopsPage.delivery.timeLabel")}
                            <input
                                type="time"
                                value={time}
                                onChange={(e) => setTime(e.target.value)}
                                className="h-8 rounded-lg border border-border-input bg-panel px-2 text-[13px] text-ink outline-none focus:border-ink/20 focus:ring-1 focus:ring-ink/20"
                            />
                        </label>
                        <label className="flex items-center gap-2 text-[13px] text-ink">
                            <Checkbox checked={weekdays} onCheckedChange={(checked) => setWeekdays(checked === true)} />
                            {t("sopsPage.delivery.weekdays")}
                        </label>
                    </>
                )}
            </div>
            <p className="mt-2 text-[12px] text-muted">
                {held
                    ? t("sopsPage.delivery.heldUnapproved")
                    : enabled && plan?.nextRunAt
                      ? t("sopsPage.delivery.nextRun", { when: formatWhen(plan.nextRunAt, i18n.language) })
                      : !enabled
                        ? t("sopsPage.delivery.onDemand")
                        : null}
            </p>
            {plan?.lastRun && (
                <p className="mt-1 text-[12px] leading-relaxed text-muted">
                    {t("sopsPage.delivery.lastRun", {
                        when: formatWhen(plan.lastRun.at, i18n.language),
                        result: plan.lastRun.result,
                    })}
                </p>
            )}

            <h3 className="mb-2 mt-4 text-[13px] font-medium text-ink">{t("sopsPage.delivery.channelsTitle")}</h3>
            <div className="flex flex-col gap-2">
                {CHANNELS.map((id) => {
                    const ready = available(id);
                    return (
                        <label key={id} className="flex items-start gap-2.5 text-[13px] text-ink">
                            <Checkbox
                                className="mt-0.5"
                                checked={chosen.has(id)}
                                onCheckedChange={(checked) => toggleChannel(id, checked === true)}
                            />
                            <span className="flex flex-col">
                                {t(CHANNEL_LABEL_KEYS[id])}
                                {!ready && id !== "desktop" && (
                                    <span className="text-[12px] text-muted">{t(NOT_SET_UP_KEYS[id])}</span>
                                )}
                                {ready && id === "text" && chosen.has("text") && channels && !channels.textNames && (
                                    <span className="text-[12px] text-muted">{t("sopsPage.delivery.textNamesOff")}</span>
                                )}
                            </span>
                        </label>
                    );
                })}
            </div>

            <h3 className="mb-1.5 mt-4 text-[13px] font-medium text-ink">{t("sopsPage.delivery.sitesTitle")}</h3>
            {sites.length === 0 ? (
                <p className="text-[12px] text-muted">{t("sopsPage.delivery.sitesNone")}</p>
            ) : (
                <>
                    <ul className="flex flex-wrap gap-1.5">
                        {sites.map((site) => (
                            <li
                                key={site}
                                className="rounded-full bg-ink/[0.05] px-2 py-0.5 text-[12px] text-ink/80"
                                title={site}
                            >
                                {siteHost(site)}
                            </li>
                        ))}
                    </ul>
                    <p className="mt-1.5 text-[12px] text-muted">{t("sopsPage.delivery.sitesNote")}</p>
                </>
            )}

            <div className="mt-4 flex justify-end">
                <button
                    type="button"
                    disabled={saving || schedulesFailed || (!dirty && !held)}
                    onClick={save}
                    className="inline-flex h-8 items-center gap-1.5 rounded-lg patina-verdigris bg-solid-btn-bg px-3.5 text-[13px] font-semibold text-solid-btn-fg shadow-[var(--shadow-solid-btn)] transition-opacity hover:opacity-90 disabled:opacity-50 cursor-pointer disabled:cursor-default"
                >
                    {saving && <Loader2 className="size-3.5 animate-spin" />}
                    {t("sopsPage.delivery.save")}
                </button>
            </div>
            <ActivityInlineNotice notice={notice} className="mt-2" />
        </section>
    );
}
