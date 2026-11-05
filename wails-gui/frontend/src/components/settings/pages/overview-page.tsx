import { ExistingTextChannels } from "./existing-text-channels";
import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import { AlertCircle, Loader2 } from "lucide-react";
import { SettingsFailureLine, useSettingsFailure } from "@/components/settings/settings-failure";
import type { TFunction } from "i18next";
import { describeMobileStatus } from "@/lib/mobile-status";
import { usageWindowLimit } from "@/lib/usage-window";
import { SetActivityRetentionDays } from "../../../../wailsjs/go/main/App";
import { EventsOn } from "../../../../wailsjs/runtime/runtime";
import {
    DismissOverviewIssue,
    PauseObservationFor,
    ResumeObservation,
    SetEngagementKindQuiet,
    SetEngagementLevel,
    type EngagementLevel,
    type MobileBriefSchedule,
    type SettingsOverviewIssue,
    type SettingsOverviewView,
} from "@/lib/wails-shim-app";
import { pageQueryCache } from "@/lib/query-cache";
import {
    GetAutomaticMeetingRecordingEnabled,
    GetBackgroundListening,
    GetMobileBriefSchedule,
    GetPlatformPermissions,
    GetSettingsOverview,
    LoadUIState,
    usePagePrivacyWithdrawal,
    usePageQueryRefresh,
} from "@/lib/page-queries";
import { ACTIVITY_LEARNING_FEATURE_KEYS } from "@/components/activity-learning-settings";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { formatBytes } from "@/components/settings/settings-format";
import { markPageContent } from "@/lib/page-switch-timing";
import { publishEngagementSettings } from "@/lib/engagement-settings-events";
import type { PlatformPermissions } from "@/types";
import { openSettingsPage, type OpenSettingsPage } from "./capture-shared";
import { screenPermissionItems } from "./observation-page";
import { RETENTION_DAYS, retentionLabel, shortensRetention } from "./recording-storage";

// ── Overview (page id "overview", RC 1) ──
//
// "How Isoform works for you": one sentence per area, written from the real
// settings, each with an inline control or a link to the page that owns it,
// and "Worth a look" above them with up to three fixes. GetSettingsOverview
// (settings_overview.go) is the one aggregate read; the page adds the few
// reads a sentence needs to stay true (which sources Isoform watches, which
// learning switches are on, whether the brief is on). A source that cannot be
// read fails only its own sentence.

const CLOSED_EYES_CHANGED_EVENT = "closed-eyes:changed";
// "Pause for 1 hour" (PauseObservationFor takes 1-1440 minutes).
const PAUSE_MINUTES = 60;
// The dial positions the inline select offers, quietest first. Mirrors
// engagement.Levels() in wails-gui/internal/engagement/level.go.
const LEVELS: readonly Exclude<EngagementLevel, "custom">[] = ["off", "fewer", "normal", "more"];
// The learning switches Remembers names, in sentence order. Shadow (Learn by
// comparison) lives under Advanced and is not a thing Isoform keeps.
const LEARNING_THINGS = ["events", "memory", "knowledge", "sop"] as const;
type LearningThing = (typeof LEARNING_THINGS)[number];
// Channel ids from SettingsOverviewSpeaksUp.channels, in display order.
const CHANNELS = ["desktop", "push", "text", "email"] as const;
// The ignored apps the sentence names before "and N more".
const IGNORED_APPS_NAMED = 3;
// Mirrors managedAccountProviders in wails-gui/ai_provider_config.go.
const ASSISTANT_NAME_KEYS: Readonly<Record<string, string>> = {
    claude: "onboarding:agent.names.claude",
    codex: "onboarding:agent.names.codex",
};

type Translate = TFunction<"settings">;
type ReadState<T> = { status: "loading" } | { status: "ready"; value: T } | { status: "failed" };

interface SeesSources {
    /** Watch my screen is on and the computer allows it. */
    screen: boolean;
    /** Watch my screen is on, but the computer doesn't allow it yet. */
    screenBlocked: boolean;
    microphone: boolean;
    calls: boolean;
}

/** "a, b and c" in the page's language. */
export function joinList(items: readonly string[], t: Translate): string {
    if (items.length <= 1) return items[0] ?? "";
    return `${items.slice(0, -1).join(t("overview.listSeparator"))}${t("overview.listLastSeparator")}${items[items.length - 1]}`;
}

function clockTime(hhmm: string, locale: string): string {
    const [hours, minutes] = hhmm.split(":").map(Number);
    if (!Number.isInteger(hours) || !Number.isInteger(minutes)) return hhmm;
    const date = new Date();
    date.setHours(hours, minutes, 0, 0);
    return new Intl.DateTimeFormat(locale, { hour: "numeric", minute: "2-digit" }).format(date);
}

/** A time later today ("4:44 PM"), or with its weekday when it is on another day. */
function timeOf(iso: string, now: number, locale: string): string | null {
    const time = Date.parse(iso);
    if (Number.isNaN(time)) return null;
    const sameDay = new Date(time).toDateString() === new Date(now).toDateString();
    return new Intl.DateTimeFormat(locale, {
        ...(sameDay ? {} : { weekday: "short" }),
        hour: "numeric",
        minute: "2-digit",
    }).format(time);
}

/** Sentences one after another: a space between them in English, none in
 *  Chinese. The template places the next sentence as <second/>. */
export function joinSentences(parts: readonly (string | null | undefined | false)[], t: Translate): string {
    return parts
        .filter((part): part is string => Boolean(part))
        .reduce((first, second) => (first ? t("overview.sentences", { first }).replace("<second/>", second) : second), "");
}

/** "in 16 hours", "a minute ago": the nearest whole unit, in the page's language. */
export function relativeTime(iso: string, now: number, locale: string): string | null {
    const time = Date.parse(iso);
    if (Number.isNaN(time)) return null;
    const seconds = Math.round((time - now) / 1000);
    const formatter = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
    const absolute = Math.abs(seconds);
    if (absolute < 60) return formatter.format(Math.sign(seconds) * Math.max(1, Math.round(absolute / 60)), "minute");
    if (absolute < 3_600) return formatter.format(Math.round(seconds / 60), "minute");
    if (absolute < 86_400) return formatter.format(Math.round(seconds / 3_600), "hour");
    return formatter.format(Math.round(seconds / 86_400), "day");
}

function assistantName(id: string, t: Translate): string {
    const key = ASSISTANT_NAME_KEYS[id];
    return key ? (t(key as never) as string) : id;
}

function kindLabel(kind: string, t: Translate): string {
    return t(`proactiveNotifications.kinds.${kind}.label` as never) as string;
}

// ── Reads the sentences add to the aggregate ──

// The reads this page paints, cached and revalidated (stale-while-revalidate):
// a reopen renders the cached values and the cache re-reads them behind it.
const OVERVIEW_READS = [
    "GetSettingsOverview",
    "GetMobileBriefSchedule",
    "GetPlatformPermissions",
    "GetBackgroundListening",
    "GetAutomaticMeetingRecordingEnabled",
    "LoadUIState",
] as const;

function seesSourcesFrom(permissions: PlatformPermissions, listening: boolean, meetings: boolean): SeesSources {
    const screenItems = screenPermissionItems(permissions);
    const microphone =
        permissions.platform === "darwin"
            ? (permissions.items ?? []).some(
                  (item) => item.key === "microphone" && item.toggleable && item.granted && item.enabled,
              )
            : true;
    const screenOn = screenItems.length > 0 && screenItems.every((item) => item.enabled);
    const screenAllowed = screenItems.every((item) => item.granted);
    return {
        screen: screenOn && screenAllowed,
        screenBlocked: screenOn && !screenAllowed,
        microphone: listening && microphone,
        calls: listening && meetings,
    };
}

/** What Isoform watches when it is not paused: the screen (every Watch my
 *  screen permission switch on, as on Observation), the microphone (Listen
 *  on, and on macOS the microphone source on) and calls (Listen and Record
 *  meetings automatically on). */
async function readSeesSources(): Promise<SeesSources> {
    const [permissions, listening, meetings] = await Promise.all([
        GetPlatformPermissions() as Promise<PlatformPermissions>,
        // tsc resolves the generated bindings' types only on the direct
        // wailsjs path; the cached wrappers keep the same signatures.
        GetBackgroundListening() as unknown as Promise<boolean>,
        GetAutomaticMeetingRecordingEnabled(),
    ]);
    return seesSourcesFrom(permissions, listening, meetings);
}

/** The learning switches (ui_state, on unless saved as "false"). */
async function readLearningThings(): Promise<LearningThing[]> {
    const loadLearningSwitch = LoadUIState as unknown as (key: string) => Promise<string>;
    const values = await Promise.all(
        LEARNING_THINGS.map((thing) => loadLearningSwitch(ACTIVITY_LEARNING_FEATURE_KEYS[thing])),
    );
    return LEARNING_THINGS.filter((_thing, index) => values[index] !== "false");
}

async function settle<T>(read: () => Promise<T>): Promise<ReadState<T>> {
    try {
        return { status: "ready", value: await read() };
    } catch {
        return { status: "failed" };
    }
}

// ── Layout pieces ──

function OverviewRow({ label, control, children }: { label: string; control?: ReactNode; children: ReactNode }) {
    return (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-border-divider py-4 last:border-b-0">
            <span className="w-[92px] shrink-0 text-[11px] font-medium uppercase tracking-[0.08em] text-muted">
                {label}
            </span>
            <div className="min-w-0 flex-[1_1_420px] text-[15px] leading-[1.6] text-ink">{children}</div>
            {control && <div className="flex shrink-0 items-center gap-2">{control}</div>}
        </div>
    );
}

// Set by OverviewPage: Try again on any row re-reads the whole overview.
const OverviewRetryContext = createContext<() => void>(() => {});

function RowFailed() {
    const { t } = useTranslation("settings");
    const retry = useContext(OverviewRetryContext);
    return (
        <span className="flex items-center gap-2 text-[13.5px] leading-snug">
            <AlertCircle className="size-[15px] shrink-0 text-copper" aria-hidden />
            <SettingsFailureLine className="text-[13.5px]" failure={{ text: t("overview.rowFailed"), retry }} />
        </span>
    );
}

function PageLink({ onClick, children }: { onClick: () => void; children: ReactNode }) {
    return (
        <Button variant="link" className="h-auto px-0 py-1 text-[13px] font-medium" onClick={onClick}>
            {children}
        </Button>
    );
}

/** A select that sits inside a sentence. */
function InlineSelect({
    label,
    value,
    disabled,
    onChange,
    children,
}: {
    label: string;
    value: string;
    disabled: boolean;
    onChange: (value: string) => void;
    children: ReactNode;
}) {
    return (
        <Select value={value} onValueChange={onChange} disabled={disabled}>
            <SelectTrigger
                aria-label={label}
                className="mx-1 inline-flex h-[30px] w-auto gap-1.5 bg-panel px-2.5 align-middle text-[13px]"
            >
                <SelectValue />
            </SelectTrigger>
            <SelectContent>{children}</SelectContent>
        </Select>
    );
}

const bold = { b: <b className="font-semibold" /> };

// ── Worth a look ──

/** What happened to an issue in this visit, for the signature it had then. */
interface IssueOutcome {
    outcome: "fixed" | "left";
    signature: string;
}
type IssueOutcomes = Record<string, IssueOutcome>;

/** The outcomes that still hold: an issue that comes back with a new
 *  signature (the count doubled, the window ran out again) is open again. */
function liveOutcomes(outcomes: IssueOutcomes, current: SettingsOverviewIssue[]): IssueOutcomes {
    const live: IssueOutcomes = {};
    for (const [id, entry] of Object.entries(outcomes)) {
        const issue = current.find((next) => next.id === id);
        if (!issue || issue.signature === entry.signature) live[id] = entry;
    }
    return live;
}

/** The issues on screen: every current one, plus those already fixed or left
 *  alone in this visit, so their result line stays until Settings closes. */
function mergeIssues(
    shown: SettingsOverviewIssue[],
    current: SettingsOverviewIssue[],
    outcomes: IssueOutcomes,
): SettingsOverviewIssue[] {
    const ids = new Set(current.map((issue) => issue.id));
    const kept = shown.filter((issue) => ids.has(issue.id) || outcomes[issue.id]);
    const keptIds = new Set(kept.map((issue) => issue.id));
    return [
        ...kept.map((issue) => current.find((next) => next.id === issue.id) ?? issue),
        ...current.filter((issue) => !keptIds.has(issue.id)),
    ];
}

function WorthALook({
    issues,
    outcomes,
    busyId,
    view,
    now,
    onFix,
    onLeave,
}: {
    issues: SettingsOverviewIssue[];
    outcomes: IssueOutcomes;
    busyId: string | null;
    view: SettingsOverviewView;
    now: number;
    onFix: (issue: SettingsOverviewIssue) => void;
    onLeave: (issue: SettingsOverviewIssue) => void;
}) {
    const { t, i18n } = useTranslation("settings");
    const locale = i18n.resolvedLanguage ?? "en";
    const open = issues.filter((issue) => !outcomes[issue.id]).length;

    const text = (issue: SettingsOverviewIssue) => {
        switch (issue.kind) {
            case "noisy_kind":
                return {
                    title: t("overview.worth.noisy.title", {
                        kind: kindLabel(issue.params.kind ?? "", t),
                        count: issue.params.sent ?? 0,
                    }),
                    detail: t("overview.worth.noisy.detail"),
                    action: t("overview.worth.noisy.action"),
                    done: t("overview.worth.noisy.done"),
                    warning: true,
                };
            case "phone_same_network_only":
                return {
                    title: t("overview.worth.phone.titleRemoteOff"),
                    detail: (() => {
                        const host = issue.params.host ?? "";
                        switch (issue.params.remoteAccessOff) {
                            case "saved_relay":
                                return t("overview.worth.phone.detailRemoteOff.saved_relay", { host });
                            case "environment_relay":
                                return t("overview.worth.phone.detailRemoteOff.environment_relay", { host });
                            case "build_relay":
                                return t("overview.worth.phone.detailRemoteOff.build_relay", { host });
                            case "disabled":
                                return t("overview.worth.phone.detailRemoteOff.disabled", { host });
                            default:
                                return t("overview.worth.phone.detail");
                        }
                    })(),
                    action: t("overview.worth.phone.action"),
                    done: t("overview.worth.phone.done"),
                    warning: true,
                };
            case "phone_remote_access_unavailable":
                return {
                    title: t("overview.worth.phoneRemote.title"),
                    detail: t("overview.worth.phoneRemote.detail"),
                    action: t("overview.worth.phoneRemote.action"),
                    done: t("overview.worth.phoneRemote.done"),
                    warning: true,
                };
            case "assistant_out_of_usage": {
                const id = issue.params.assistant ?? "";
                const name = assistantName(id, t);
                const window =
                    usageWindowLimit(view.thinksWith.order.find((entry) => entry.assistant === id)?.windowLabel ?? "", t) ?? "";
                const others = view.thinksWith.order
                    .filter((entry) => entry.assistant !== id && entry.percentLeft !== null && entry.percentLeft > 0)
                    .map((entry) => assistantName(entry.assistant, t));
                const when = issue.params.resetsAt ? relativeTime(issue.params.resetsAt, now, locale) : null;
                const parts = [
                    when ? t("overview.worth.assistant.resets", { when }) : null,
                    others.length > 0
                        ? t("overview.worth.assistant.others", { others: joinList(others, t), count: others.length })
                        : null,
                ];
                const detail = parts.some(Boolean) ? joinSentences(parts, t) : "";
                return {
                    title: window
                        ? t("overview.worth.assistant.title", { assistant: name, window })
                        : t("overview.worth.assistant.titleNoWindow", { assistant: name }),
                    detail,
                    action: t("overview.worth.assistant.action"),
                    done: t("overview.worth.assistant.done"),
                    warning: false,
                };
            }
        }
    };

    return (
        <section aria-labelledby="overview-worth" className="settings-card settings-card--padded flex flex-col gap-2.5">
            <h2 id="overview-worth" className="settings-card-title">
                {open > 0 ? t("overview.worth.title", { count: open }) : t("overview.worth.titleDone")}
            </h2>
            {issues.map((issue) => {
                const copy = text(issue);
                const outcome = outcomes[issue.id]?.outcome;
                const busy = busyId === issue.id;
                const titleId = `overview-issue-${issue.id}`;
                return (
                    <div
                        key={issue.id}
                        data-overview-issue={issue.kind}
                        className="flex flex-wrap items-center gap-x-3.5 gap-y-2 border-t border-border-divider pt-2.5"
                    >
                        <span
                            aria-hidden
                            className={`size-2 shrink-0 rounded-full ${copy.warning ? "bg-warning" : "bg-muted"}`}
                        />
                        <div className="flex min-w-0 flex-[1_1_360px] flex-col gap-0.5">
                            <span id={titleId} className="text-[13.5px] font-medium text-ink">
                                {copy.title}
                            </span>
                            {copy.detail && <span className="text-[12.5px] text-muted">{copy.detail}</span>}
                        </div>
                        {outcome ? (
                            <span role="status" className="text-[12.5px] text-muted">
                                {outcome === "fixed" ? copy.done : t("overview.worth.left")}
                            </span>
                        ) : (
                            <div className="flex shrink-0 items-center gap-2">
                                <Button
                                    variant="secondary"
                                    size="sm"
                                    aria-describedby={titleId}
                                    disabled={busyId !== null}
                                    onClick={() => onFix(issue)}
                                >
                                    {busy && <Loader2 className="size-3.5 animate-spin" />}
                                    {copy.action}
                                </Button>
                                <Button
                                    variant="quiet"
                                    size="sm"
                                    aria-describedby={titleId}
                                    disabled={busyId !== null}
                                    onClick={() => onLeave(issue)}
                                >
                                    {t("overview.worth.leave")}
                                </Button>
                            </div>
                        )}
                    </div>
                );
            })}
        </section>
    );
}

// ── The page ──

export function OverviewPage({ onOpenPage, active = true }: { onOpenPage?: OpenSettingsPage; active?: boolean } = {}) {
    const { t, i18n } = useTranslation("settings");
    const { failure: changeFailure, fail: failChange } = useSettingsFailure();
    const locale = i18n.resolvedLanguage ?? "en";

    // A reopen paints the last readings at once; refresh() revalidates them.
    const [overviewSeed] = useState(() => pageQueryCache.peek<SettingsOverviewView>("GetSettingsOverview"));
    const [overview, setOverview] = useState<ReadState<SettingsOverviewView>>(() =>
        overviewSeed === undefined ? { status: "loading" } : { status: "ready", value: overviewSeed },
    );
    const [sources, setSources] = useState<ReadState<SeesSources>>(() => {
        const permissions = pageQueryCache.peek<PlatformPermissions>("GetPlatformPermissions");
        const listening = pageQueryCache.peek<boolean>("GetBackgroundListening");
        const meetings = pageQueryCache.peek<boolean>("GetAutomaticMeetingRecordingEnabled");
        if (permissions === undefined || listening === undefined || meetings === undefined) {
            return { status: "loading" };
        }
        return { status: "ready", value: seesSourcesFrom(permissions, listening, meetings) };
    });
    const [learning, setLearning] = useState<ReadState<LearningThing[]>>(() => {
        const values = LEARNING_THINGS.map((thing) =>
            pageQueryCache.peek<string>("LoadUIState", [ACTIVITY_LEARNING_FEATURE_KEYS[thing]]),
        );
        if (values.some((value) => value === undefined)) return { status: "loading" };
        return { status: "ready", value: LEARNING_THINGS.filter((_thing, index) => values[index] !== "false") };
    });
    const [schedule, setSchedule] = useState<ReadState<MobileBriefSchedule>>(() => {
        const cached = pageQueryCache.peek<MobileBriefSchedule>("GetMobileBriefSchedule");
        return cached === undefined ? { status: "loading" } : { status: "ready", value: cached };
    });
    const [issues, setIssues] = useState<SettingsOverviewIssue[]>(() => {
        if (overviewSeed === undefined) return [];
        // The same derivation refresh() applies to a ready read.
        const current = overviewSeed.worthALook ?? [];
        return mergeIssues([], current, liveOutcomes({}, current));
    });
    const [outcomes, setOutcomes] = useState<IssueOutcomes>({});
    const [busy, setBusy] = useState<string | null>(null);
    // The value an inline select was changed to, shown until the sentence is read again.
    const [pending, setPending] = useState<{ target: string; value: string } | null>(null);
    const [confirmDays, setConfirmDays] = useState<number | null>(null);
    const [now, setNow] = useState(() => Date.now());

    const mounted = useRef(true);
    // Outcomes are written here first so a read that resolves before the next
    // render still sees them.
    const outcomesRef = useRef(outcomes);
    // Bumped by every read; a read that resolves after a newer one started is dropped.
    const readVersion = useRef(0);

    const refresh = useCallback(async () => {
        const version = ++readVersion.current;
        const [nextOverview, nextSources, nextLearning, nextSchedule] = await Promise.all([
            settle(() => GetSettingsOverview()),
            settle(readSeesSources),
            settle(readLearningThings),
            settle(() => GetMobileBriefSchedule()),
        ]);
        if (!mounted.current || version !== readVersion.current) return;
        setOverview(nextOverview);
        setSources(nextSources);
        setLearning(nextLearning);
        setSchedule(nextSchedule);
        setNow(Date.now());
        if (nextOverview.status === "ready") {
            markPageContent('settings');
            const current = nextOverview.value.worthALook ?? [];
            const live = liveOutcomes(outcomesRef.current, current);
            outcomesRef.current = live;
            setOutcomes(live);
            setIssues((shown) => mergeIssues(shown, current, live));
        }
    }, []);

    const retryOverview = useCallback(() => void refresh(), [refresh]);
    usePageQueryRefresh(OVERVIEW_READS, refresh);

    // A privacy transition withdraws the pre-transition sentences
    // synchronously — even while the retained Settings dialog is hidden — and
    // supersedes the old reads; the sweep's refresh re-reads what is still
    // permitted. Ordinary invalidations keep the stale paint.
    usePagePrivacyWithdrawal(useCallback(() => {
        readVersion.current += 1;
        setOverview({ status: "loading" });
        setSources({ status: "loading" });
        setLearning({ status: "loading" });
        setSchedule({ status: "loading" });
        setIssues([]);
        setPending(null);
        setConfirmDays(null);
    }, []));

    useEffect(() => {
        mounted.current = true;
        // A seeded page has content at mount; the timing mark belongs to it.
        if (overviewSeed !== undefined) markPageContent("settings");
        void refresh();
        const onFocus = () => void refresh();
        window.addEventListener("focus", onFocus);
        // The menu bar, the hotkey, Observation and the timer all pause and resume.
        const off = EventsOn(CLOSED_EYES_CHANGED_EVENT, () => void refresh());
        const offPermissions = EventsOn("permissions:changed", () => void refresh());
        return () => {
            mounted.current = false;
            readVersion.current += 1;
            window.removeEventListener("focus", onFocus);
            off();
            offPermissions();
        };
    }, [refresh, overviewSeed]);

    // Reopened after a quiet stretch: re-read whatever outlived its TTL.
    const wasActive = useRef(active);
    useEffect(() => {
        if (active && !wasActive.current) void refresh();
        wasActive.current = active;
    }, [active, refresh]);

    const recordOutcome = (issue: SettingsOverviewIssue, outcome: IssueOutcome["outcome"]) => {
        outcomesRef.current = { ...outcomesRef.current, [issue.id]: { outcome, signature: issue.signature } };
        setOutcomes(outcomesRef.current);
    };

    /** One change at a time. `done` runs before the sentences are read again;
     *  a failure says so and the control keeps its old value. */
    const change = async (
        target: string,
        write: () => Promise<unknown>,
        { done, expected }: { done?: () => void; expected?: (err: unknown) => boolean } = {},
    ) => {
        if (busy !== null) return;
        setBusy(target);
        try {
            await write();
            done?.();
        } catch (err) {
            if (!expected?.(err)) failChange(t("overview.changeFailed"), () => void change(target, write, { done, expected }));
        }
        // A write changes what the sentences say; never re-read it from cache.
        pageQueryCache.invalidate(OVERVIEW_READS);
        await refresh();
        if (mounted.current) {
            setBusy(null);
            setPending(null);
        }
    };

    if (overview.status === "loading") {
        return (
            <div aria-busy="true" className="settings-card settings-card--padded flex flex-col gap-2.5">
                <div className="flex flex-col gap-2">
                    {[95, 85, 90].map((width) => (
                        <span key={width} className="block h-3 rounded-md bg-surface-active" style={{ width: `${width}%` }} />
                    ))}
                </div>
            </div>
        );
    }

    // A failed aggregate read fails every sentence it feeds, and only those.
    const view = overview.status === "ready" ? overview.value : null;
    const unavailable = new Set(view?.unavailable ?? []);
    const failed = (section: string) => view === null || unavailable.has(section);

    const fixIssue = (issue: SettingsOverviewIssue) => {
        switch (issue.kind) {
            case "noisy_kind":
                void change(
                    issue.id,
                    async () => {
                        publishEngagementSettings(await SetEngagementKindQuiet(issue.params.kind ?? "", true));
                    },
                    { done: () => recordOutcome(issue, "fixed") },
                );
                return;
            case "phone_same_network_only":
            case "phone_remote_access_unavailable":
                recordOutcome(issue, "fixed");
                openSettingsPage("iphone", onOpenPage);
                return;
            case "assistant_out_of_usage":
                recordOutcome(issue, "fixed");
                openSettingsPage("ai-provider", onOpenPage);
                return;
        }
    };

    // An issue fixed elsewhere meanwhile is no longer current: the read after
    // it drops the row, so that is not a failure to report.
    const leaveIssue = (issue: SettingsOverviewIssue) => {
        void change(issue.id, () => DismissOverviewIssue(issue.id), {
            done: () => recordOutcome(issue, "left"),
            expected: (err) => String(err instanceof Error ? err.message : err).includes("overview_issue_not_current"),
        });
    };

    // ── Sees ──
    const seesRow = (() => {
        const observation = (
            <PageLink onClick={() => openSettingsPage("activity", onOpenPage)}>{t("overview.sees.link")}</PageLink>
        );
        if (!view) return { body: <RowFailed />, control: observation };
        const sees = view.sees;
        if (!sees.watching) {
            const until = sees.pausedUntil ? timeOf(sees.pausedUntil, now, locale) : null;
            return {
                body: until ? (
                    <Trans t={t} i18nKey="overview.sees.pausedUntil" values={{ time: until }} components={bold} />
                ) : (
                    <Trans t={t} i18nKey="overview.sees.paused" components={bold} />
                ),
                control: (
                    <Button
                        variant="secondary"
                        size="sm"
                        disabled={busy !== null}
                        onClick={() => void change("sees", () => ResumeObservation())}
                    >
                        {busy === "sees" && <Loader2 className="size-3.5 animate-spin" />}
                        {t("overview.sees.resume")}
                    </Button>
                ),
            };
        }
        if (sources.status === "failed") return { body: <RowFailed />, control: observation };
        if (sources.status === "loading") return { body: null };
        if (sources.value.screenBlocked) {
            return {
                body: <Trans t={t} i18nKey="overview.sees.screenBlocked" components={bold} />,
                control: (
                    <Button variant="secondary" size="sm" onClick={() => openSettingsPage("activity", onOpenPage, { anchor: "sees-screen" })}>
                        {t("overview.sees.allow")}
                    </Button>
                ),
            };
        }
        const on = (["screen", "microphone", "calls"] as const).filter((source) => sources.value[source]);
        if (on.length === 0) {
            return {
                body: <Trans t={t} i18nKey="overview.sees.nothing" components={bold} />,
                control: observation,
            };
        }
        return {
            body: (
                <Trans
                    t={t}
                    i18nKey="overview.sees.watching"
                    values={{ sources: joinList(on.map((source) => t(`overview.sees.sources.${source}`)), t) }}
                    components={bold}
                />
            ),
            control: (
                <Button
                    variant="secondary"
                    size="sm"
                    disabled={busy !== null}
                    onClick={() => void change("sees", () => PauseObservationFor(PAUSE_MINUTES))}
                >
                    {busy === "sees" && <Loader2 className="size-3.5 animate-spin" />}
                    {t("overview.sees.pause")}
                </Button>
            ),
        };
    })();

    // ── Speaks up ──
    const speaksUpRow = (() => {
        const control = (
            <PageLink onClick={() => openSettingsPage("notifications", onOpenPage)}>
                {t("overview.speaksUp.link")}
            </PageLink>
        );
        if (!view || failed("speaksUp")) return { body: <RowFailed />, control };
        const speaks = view.speaksUp;
        const level = speaks.level as EngagementLevel;
        const select = (
            <InlineSelect
                label={t("overview.speaksUp.levelLabel")}
                value={pending?.target === "speaksUp" ? pending.value : level}
                disabled={busy !== null}
                onChange={(next) => {
                    if (next === level || next === "custom") return;
                    setPending({ target: "speaksUp", value: next });
                    void change("speaksUp", async () => {
                        publishEngagementSettings(await SetEngagementLevel(next as EngagementLevel));
                    });
                }}
            >
                {LEVELS.map((option) => (
                    <SelectItem key={option} value={option}>
                        {t(`proactiveNotifications.level.options.${option}.label`)}
                    </SelectItem>
                ))}
                {level === "custom" && (
                    <SelectItem value="custom" disabled>
                        {t("proactiveNotifications.level.custom")}
                    </SelectItem>
                )}
            </InlineSelect>
        );
        let detail: string;
        if (level === "off") {
            detail = t("overview.speaksUp.offDetail");
        } else {
            const channels = CHANNELS.filter((channel) => speaks.channels.includes(channel)).map((channel) =>
                t(`overview.speaksUp.channels.${channel}`),
            );
            const clauses = [
                speaks.dailyCap > 0
                    ? t("overview.speaksUp.limit", { count: speaks.dailyCap })
                    : t("overview.speaksUp.noLimit"),
                speaks.quietStart === speaks.quietEnd
                    ? t("overview.speaksUp.noQuiet")
                    : t("overview.speaksUp.quiet", { start: clockTime(speaks.quietStart, locale), end: clockTime(speaks.quietEnd, locale) }),
                channels.length > 0 ? t("overview.speaksUp.on", { channels: joinList(channels, t) }) : null,
            ].filter((clause): clause is string => Boolean(clause));
            detail = t("overview.speaksUp.detail", { clauses: clauses.join(t("overview.speaksUp.clauseSeparator")) });
        }
        return {
            body: (
                <>
                    {t("overview.speaksUp.lead")}
                    {select}
                    {busy === "speaksUp" && <Loader2 className="mr-1 inline size-3.5 animate-spin align-middle" />}
                    {detail}
                </>
            ),
            control,
        };
    })();

    // ── Thinks with ──
    const thinksWithRow = (() => {
        const control = (
            <PageLink onClick={() => openSettingsPage("ai-provider", onOpenPage)}>{t("overview.thinksWith.link")}</PageLink>
        );
        if (!view || failed("thinksWith")) return { body: <RowFailed />, control };
        const order = view.thinksWith.order;
        if (order.length === 0) return { body: t("overview.thinksWith.none"), control };
        const names = order.map((entry) => assistantName(entry.assistant, t));
        const first = order[0];
        const percent = first.percentLeft === null ? null : Math.round(first.percentLeft);
        const ordered =
            names.length === 1
                ? t("overview.thinksWith.one", { first: names[0] })
                : t("overview.thinksWith.many", {
                      first: names[0],
                      rest: names.slice(1).join(t("overview.thinksWith.thenSeparator")),
                  });
        if (percent === null) return { body: ordered, control };
        const window = usageWindowLimit(first.windowLabel, t);
        const left = (
            <Trans
                t={t}
                i18nKey={window ? "overview.thinksWith.left" : "overview.thinksWith.leftNoWindow"}
                values={{ assistant: names[0], percent, window }}
                components={bold}
            />
        );
        return {
            body: <Trans t={t} i18nKey="overview.sentences" values={{ first: ordered }} components={{ second: left }} />,
            control,
        };
    })();

    // ── Remembers ──
    const remembersRow = (() => {
        if (!view || failed("remembers") || learning.status === "failed") {
            return {
                body: <RowFailed />,
                control: (
                    <PageLink onClick={() => openSettingsPage("memory", onOpenPage)}>
                        {t("overview.remembers.link")}
                    </PageLink>
                ),
            };
        }
        if (learning.status === "loading") return { body: null };
        const count = view.remembers.awaitingConfirmCount;
        const things = learning.value.map((thing) => t(`overview.remembers.things.${thing}`));
        return {
            body: joinSentences(
                [
                    things.length > 0
                        ? t("overview.remembers.learns", { things: joinList(things, t) })
                        : t("overview.remembers.learnsNothing"),
                    count > 0 ? t("overview.remembers.waiting", { count }) : t("overview.remembers.nothingWaiting"),
                ],
                t,
            ),
            control: (
                <PageLink onClick={() => openSettingsPage("memory", onOpenPage)}>
                    {count > 0 ? t("overview.remembers.review") : t("overview.remembers.link")}
                </PageLink>
            ),
        };
    })();

    // ── Keeps ──
    const keepsRow = (() => {
        if (!view || failed("keeps")) {
            return {
                body: <RowFailed />,
                control: (
                    <PageLink onClick={() => openSettingsPage("storage", onOpenPage)}>{t("overview.keeps.link")}</PageLink>
                ),
            };
        }
        const keeps = view.keeps;
        const limit = formatBytes(keeps.limitBytes);
        const used = formatBytes(keeps.usedBytes);
        const ratio = keeps.limitBytes > 0 ? Math.min(1, keeps.usedBytes / keeps.limitBytes) : 0;
        const onRetention = (value: string) => {
            const next = Number(value);
            if (!Number.isFinite(next) || next === keeps.retention) return;
            // A shorter time limit deletes recordings now, so it always asks
            // first: the overview does not know how many sessions are kept.
            if (shortensRetention(keeps.retention, next)) {
                setConfirmDays(next);
                return;
            }
            setPending({ target: "keeps", value });
            void change("keeps", () => SetActivityRetentionDays(next));
        };
        return {
            body: (
                <>
                    {t("overview.keeps.lead")}
                    <InlineSelect
                        label={t("privacy.retention.title")}
                        value={pending?.target === "keeps" ? pending.value : String(keeps.retention)}
                        disabled={busy !== null}
                        onChange={onRetention}
                    >
                        {RETENTION_DAYS.map((days) => (
                            <SelectItem key={days} value={String(days)}>
                                {retentionLabel(t, days)}
                            </SelectItem>
                        ))}
                    </InlineSelect>
                    {busy === "keeps" && <Loader2 className="mr-1 inline size-3.5 animate-spin align-middle" />}
                    {keeps.limitBytes <= 0
                        ? t("overview.keeps.detailNoLimit")
                        : keeps.usedBytes > 0
                          ? t("overview.keeps.detail", { limit, used })
                          : t("overview.keeps.detailNothingUsed", { limit })}
                </>
            ),
            control: (
                <div
                    role="progressbar"
                    aria-label={t("overview.keeps.usage", { used, limit })}
                    aria-valuemin={0}
                    aria-valuemax={100}
                    aria-valuenow={Math.round(ratio * 100)}
                    className="h-1.5 w-[120px] overflow-hidden rounded-full bg-card-progress"
                >
                    <div className="h-full bg-accent" style={{ width: `${Math.round(ratio * 100)}%` }} />
                </div>
            ),
        };
    })();

    // ── Briefs ──
    const briefsRow = (() => {
        const control = (
            <PageLink onClick={() => openSettingsPage("iphone", onOpenPage)}>{t("overview.briefs.link")}</PageLink>
        );
        if (!view || failed("briefs") || schedule.status === "failed") return { body: <RowFailed />, control };
        if (schedule.status === "loading") return { body: null, control };
        const brief = schedule.value;
        if (!brief.enabled) return { body: t("overview.briefs.off"), control };
        const times = { morning: clockTime(brief.morning, locale), evening: clockTime(brief.evening, locale) };
        const phone = view.briefs.phone;
        return {
            body: joinSentences(
                [
                    brief.adaptiveTiming ? t("overview.briefs.adaptive", times) : t("overview.briefs.fixed", times),
                    phone && view.briefs.connection
                        ? describeMobileStatus(view.briefs.connection, now, locale, t).description
                        : phone ? null : t("overview.briefs.noPhone"),
                ],
                t,
            ),
            control,
        };
    })();

    // ── Ignores ──
    const ignoresRow = (() => {
        const apps = view?.ignores.apps ?? [];
        const named = apps.slice(0, IGNORED_APPS_NAMED).map((app) => app.name);
        const rest = apps.length - named.length;
        const list = rest > 0 ? [...named, t("overview.ignores.more", { count: rest })] : named;
        return {
            body: !view ? (
                <RowFailed />
            ) : apps.length === 0 ? (
                t("overview.ignores.none")
            ) : (
                t("overview.ignores.some", { apps: joinList(list, t) })
            ),
            control: (
                <Button variant="secondary" size="sm" onClick={() => openSettingsPage("security", onOpenPage)}>
                    {t("overview.ignores.add")}
                </Button>
            ),
        };
    })();

    const rows: { key: string; row: { body: ReactNode; control?: ReactNode } }[] = [
        { key: "sees", row: seesRow },
        { key: "speaksUp", row: speaksUpRow },
        { key: "thinksWith", row: thinksWithRow },
        { key: "remembers", row: remembersRow },
        { key: "keeps", row: keepsRow },
        { key: "briefs", row: briefsRow },
        { key: "ignores", row: ignoresRow },
    ];
    const confirmPeriod = confirmDays === null ? "" : retentionLabel(t, confirmDays);

    return (
        <OverviewRetryContext.Provider value={retryOverview}>
            <SettingsFailureLine failure={changeFailure} />
            {view && issues.length > 0 && (
                <WorthALook
                    issues={issues}
                    outcomes={outcomes}
                    busyId={busy}
                    view={view}
                    now={now}
                    onFix={fixIssue}
                    onLeave={leaveIssue}
                />
            )}
            <section aria-label={t("pages.overview.title")} className="flex flex-col">
                {rows.map(({ key, row }) => (
                    <OverviewRow
                        key={key}
                        label={t(`overview.labels.${key}` as never) as string}
                        control={row.control}
                    >
                        {row.body}
                    </OverviewRow>
                ))}
            </section>
            <ExistingTextChannels />
            <ConfirmDialog
                open={confirmDays !== null}
                onOpenChange={(open) => {
                    if (!open) setConfirmDays(null);
                }}
                title={t("privacy.retention.confirmTitle", { period: confirmPeriod })}
                description={t("privacy.retention.confirmDescription", { period: confirmPeriod })}
                confirmLabel={t("privacy.retention.confirmAction")}
                variant="danger"
                onConfirm={() => {
                    if (confirmDays === null) return;
                    setPending({ target: "keeps", value: String(confirmDays) });
                    void change("keeps", () => SetActivityRetentionDays(confirmDays));
                }}
            />
        </OverviewRetryContext.Provider>
    );
}
