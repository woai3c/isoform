import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Check, Copy, Loader2, RefreshCw } from "lucide-react";
import { activeInterval } from "@/lib/app-activity";
import { QRCodeSVG } from "qrcode.react";
import { useTranslation } from "react-i18next";
import { NativeClipboardSetText } from "../../wailsjs/go/main/App";
import { BrowserOpenURL, ClipboardSetText, EventsOn } from "../../wailsjs/runtime/runtime";
import { IPHONE_APP_LINK } from "@/lib/iphone-app-link";
import { GetMobileBriefHealth } from "@/lib/page-queries";
import { notifyToast } from "@/lib/notify";
// The shim declares the iPhone bindings with the PROTOCOL.md section 12 types,
// so the calls and the state below agree on one shape.
import {
    ConfirmMobilePairing,
    DisableMobileConnection,
    EnableMobileConnectionUsingRelay,
    GenerateMobileBriefNow,
    GenerateMobileConnectionCode,
    GetMobileBriefSchedule,
    GetMobileConnectionStatus,
    GetMobileKeepAwake,
    ResetMobileRemoteAccess,
    RevokeMobileDevice,
    SetMobileBriefSchedule,
    SetMobileKeepAwake,
    type MobileBriefHealth,
    type MobileBriefRequestResult,
    type MobileBriefSchedule,
    type MobileConnectionStatus,
    type MobileDeviceView,
    type MobileKeepAwakeStatus,
    type MobilePendingPairing,
} from "@/lib/wails-shim-app";
import { SettingsAdvanced } from "./settings/settings-advanced";
import { SettingsCard } from "./settings/settings-primitives";
import { Button } from "./ui/button";
import {
    Dialog,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
} from "./ui/dialog";
import { Switch } from "./ui/switch";

const STATUS_POLL_MS = 15_000;
const SCHEDULE_POLL_MS = 60_000;
const CODE_TICK_MS = 1_000;
const IDLE_TICK_MS = 30_000;
// Go zero time ("0001-01-01T00:00:00Z") marks "never"; anything before 2000 is treated as unset.
const EARLIEST_TIMESTAMP = Date.UTC(2000, 0, 1);
const CLOCK_PATTERN = /^([01]\d|2[0-3]):[0-5]\d$/;
const REMOTE_ACCESS_STATE_DETAIL_TRANSLATION_PREFIX = "mobileConnection.remoteAccessStateDetail";
const REMOTE_ACCESS_OFF_TRANSLATION_PREFIX = "mobileConnection.remoteAccessOff";
const TIME_INPUT_CLASS =
    "rounded-md border border-border bg-panel px-3 py-1.5 text-xs tabular-nums text-ink focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-50";
const RELAY_INPUT_CLASS =
    "w-full rounded-md border border-border bg-panel px-3 py-1.5 font-mono text-xs text-ink placeholder:text-muted focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-50";

function relayHostname(relayUrl: string): string {
    try {
        return new URL(relayUrl).hostname;
    } catch {
        return "";
    }
}

// Plain names for what a paired iPhone may do (PROTOCOL.md sections 10, 15.1
// and 20.1). desk:read and decide are retired: older phones still hold them, no
// route accepts them, so they are not listed.
const SCOPE_LABELS = {
    "brief:read": "mobileConnection.scopeBriefs",
    "brief:refresh": "mobileConnection.scopeRefresh",
    "presence:read": "mobileConnection.scopePresence",
    push: "mobileConnection.scopeNotifications",
    "chat:read": "mobileConnection.scopeChatRead",
    "chat:write": "mobileConnection.scopeChatWrite",
    "memory:correct": "mobileConnection.scopeMemoryCorrect",
    "handoff:create": "mobileConnection.scopeHandoff",
    "brief:feedback": "mobileConnection.scopeBriefFeedback",
    "offers:read": "mobileConnection.scopeOffersRead",
    "offers:decide": "mobileConnection.scopeOffersDecide",
    "goals:read": "mobileConnection.scopeGoalsRead",
    "goals:write": "mobileConnection.scopeGoalsWrite",
    "library:read": "mobileConnection.scopeLibraryRead",
} as const;

// Same chips as the Chat apps list: green while connected, neutral otherwise.
const STATUS_CHIP_ON_CLASS =
    "inline-flex items-center rounded-full bg-success-bg px-2 py-0.5 text-[10px] font-medium text-success";
const STATUS_CHIP_CLASS =
    "inline-flex items-center rounded-full bg-ink/[0.05] px-2 py-0.5 text-[10px] font-medium text-ink/55";

type KnownScope = keyof typeof SCOPE_LABELS;

type ErrorScope =
    | "statusLoad"
    | "scheduleLoad"
    | "relay"
    | "code"
    | "copy"
    | "approval"
    | "revoke"
    | "remoteReset"
    | "remoteSwitch"
    | "schedule"
    | "brief"
    | "keepAwake";

type StatusAction = "enable" | "disable" | "code" | "approve" | "decline" | "revoke" | "remote-reset";

type ScheduleDraft = Pick<MobileBriefSchedule, "enabled" | "morning" | "evening" | "feedEnabled" | "adaptiveTiming">;

function isKnownScope(scope: string): scope is KnownScope {
    return Object.prototype.hasOwnProperty.call(SCOPE_LABELS, scope);
}

function scheduleDraftOf(schedule: MobileBriefSchedule): ScheduleDraft {
    return {
        enabled: schedule.enabled,
        morning: schedule.morning,
        evening: schedule.evening,
        feedEnabled: schedule.feedEnabled,
        adaptiveTiming: schedule.adaptiveTiming,
    };
}

function sameDraft(a: ScheduleDraft, b: ScheduleDraft): boolean {
    return a.enabled === b.enabled && a.morning === b.morning && a.evening === b.evening && a.feedEnabled === b.feedEnabled && a.adaptiveTiming === b.adaptiveTiming;
}

function parseTimestamp(value: string): number | null {
    if (!value) return null;
    const time = Date.parse(value);
    if (Number.isNaN(time) || time < EARLIEST_TIMESTAMP) return null;
    return time;
}

function formatRelative(time: number, now: number, locale: string): string {
    const seconds = Math.round((time - now) / 1000);
    const formatter = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
    const absolute = Math.abs(seconds);
    if (absolute < 60) return formatter.format(seconds, "second");
    if (absolute < 3_600) return formatter.format(Math.round(seconds / 60), "minute");
    if (absolute < 86_400) return formatter.format(Math.round(seconds / 3_600), "hour");
    return formatter.format(Math.round(seconds / 86_400), "day");
}

function formatDate(value: string, locale: string): string | null {
    const time = parseTimestamp(value);
    if (time === null) return null;
    return new Date(time).toLocaleDateString(locale, {
        year: "numeric",
        month: "short",
        day: "numeric",
    });
}

function formatDateTime(value: string, locale: string): string | null {
    const time = parseTimestamp(value);
    if (time === null) return null;
    return new Date(time).toLocaleString(locale, {
        month: "short",
        day: "numeric",
        hour: "numeric",
        minute: "2-digit",
    });
}

function formatClock(value: string, locale: string): string | null {
    const time = parseTimestamp(value);
    if (time === null) return null;
    return new Date(time).toLocaleTimeString(locale, {
        hour: "numeric",
        minute: "2-digit",
    });
}

function formatCountdown(seconds: number): string {
    return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;
}

function formatPairingCode(code: string): string {
    return /^\d{6}$/.test(code) ? `${code.slice(0, 3)} ${code.slice(3)}` : code;
}

/** Returns the origin of an https relay (or a local http relay for development), or null when invalid. */
function relayOriginFrom(value: string): string | null {
    try {
        const url = new URL(value);
        const local =
            url.hostname === "localhost" || url.hostname === "127.0.0.1" || url.hostname === "[::1]";
        const secure = url.protocol === "https:" || (url.protocol === "http:" && local);
        if (!secure || url.pathname !== "/" || url.search || url.hash || url.username || url.password) {
            return null;
        }
        return url.origin;
    } catch {
        return null;
    }
}

async function copyText(value: string): Promise<boolean> {
    try {
        const nativeRuntime = (window as unknown as {
            runtime?: { ClipboardSetText?: unknown };
        }).runtime?.ClipboardSetText;
        if (nativeRuntime) {
            await NativeClipboardSetText(value);
        } else if (!(await ClipboardSetText(value))) {
            return false;
        }
        return true;
    } catch {
        return false;
    }
}

function SettingRow({
    title,
    description,
    control,
    children,
}: {
    title: ReactNode;
    description?: ReactNode;
    control?: ReactNode;
    children?: ReactNode;
}) {
    return (
        <div className="settings-row">
            <div className="flex min-w-0 flex-col gap-1">
                <span className="settings-row-title break-words">{title}</span>
                {description && <div className="settings-row-desc">{description}</div>}
                {children}
            </div>
            {control && <div className="flex shrink-0 items-center gap-2">{control}</div>}
        </div>
    );
}

function LoadState({
    failed,
    loadingText,
    failedText,
    onRetry,
}: {
    failed: boolean;
    loadingText: string;
    failedText: string;
    onRetry: () => void;
}) {
    const { t } = useTranslation("settings");
    if (!failed) {
        return (
            <p role="status" className="settings-row-desc py-2">
                {loadingText}
            </p>
        );
    }
    return (
        <div className="flex flex-wrap items-center gap-3 py-2">
            <p role="alert" className="text-xs text-error">
                {failedText}
            </p>
            <Button type="button" variant="outline" size="sm" onClick={onRetry}>
                {t("mobileConnection.retry")}
            </Button>
        </div>
    );
}

/**
 * The iPhone page: the connection status, pairing and paired iPhones, the
 * daily brief, then the text-message card, with the server address, what each
 * iPhone may do and other details under one Advanced disclosure.
 */
export function MobileConnectionSettings({
    textMessages,
    advancedExtra,
    onOpenAssistant,
}: {
    /** Rendered after the Daily brief card: the text-message essentials. */
    textMessages?: ReactNode;
    /** Rendered last inside Advanced: the text-message details. */
    advancedExtra?: ReactNode;
    /** Opens Assistant with Advanced expanded, where the switch that brings
     *  the written summary back lives. Without it the note only names it. */
    onOpenAssistant?: () => void;
} = {}) {
    const { t, i18n } = useTranslation("settings");
    const locale = i18n.resolvedLanguage ?? i18n.language;

    const [status, setStatus] = useState<MobileConnectionStatus | null>(null);
    const [schedule, setSchedule] = useState<MobileBriefSchedule | null>(null);
    // Last run, next run and missed runs; null until read, and left as it was
    // when the read fails, because the schedule card is still useful without it.
    const [health, setHealth] = useState<MobileBriefHealth | null>(null);
    const [errors, setErrors] = useState<Partial<Record<ErrorScope, string>>>({});
    const [action, setAction] = useState<StatusAction | null>(null);
    const [briefBusy, setBriefBusy] = useState(false);
    const [feedCopied, setFeedCopied] = useState(false);
    // null means "show the saved value"; a string or draft is the user's edit.
    const [relayDraft, setRelayDraft] = useState<string | null>(null);
    const [scheduleDraft, setScheduleDraft] = useState<ScheduleDraft | null>(null);
    const [copied, setCopied] = useState(false);
    const [now, setNow] = useState(() => Date.now());
    const [dismissedPairingId, setDismissedPairingId] = useState<string | null>(null);
    const [revokeTarget, setRevokeTarget] = useState<MobileDeviceView | null>(null);
    const [remoteResetOpen, setRemoteResetOpen] = useState(false);
    const [remoteSwitchOpen, setRemoteSwitchOpen] = useState(false);
    // The saved "Keep this computer awake for your phone" switch; null until it is first read.
    const [keepAwake, setKeepAwake] = useState<MobileKeepAwakeStatus | null>(null);

    const mounted = useRef(false);
    // Bumped by every read or write; a read whose version is stale when it resolves is dropped.
    const statusVersion = useRef(0);
    const scheduleVersion = useRef(0);
    const keepAwakeVersion = useRef(0);
    const actionRef = useRef<StatusAction | null>(null);
    const scheduleRef = useRef<MobileBriefSchedule | null>(null);
    const scheduleSavingRef = useRef(false);
    // The newest schedule waiting to be saved; a running save picks it up when it finishes.
    const pendingScheduleRef = useRef<ScheduleDraft | null>(null);
    const briefRef = useRef(false);

    const setError = useCallback((scope: ErrorScope, message?: string) => {
        setErrors((current) => ({ ...current, [scope]: message }));
    }, []);

    const applySchedule = useCallback((next: MobileBriefSchedule) => {
        scheduleRef.current = next;
        setSchedule(next);
    }, []);

    const refreshStatus = useCallback(async () => {
        // A status-changing request owns the status until it resolves.
        if (actionRef.current) return;
        const version = ++statusVersion.current;
        try {
            const next = await GetMobileConnectionStatus();
            if (mounted.current && version === statusVersion.current) {
                setStatus(next);
                setError("statusLoad");
            }
        } catch {
            if (mounted.current && version === statusVersion.current) {
                setError("statusLoad", t("mobileConnection.statusFailed"));
            }
        }
    }, [setError, t]);

    const refreshSchedule = useCallback(async () => {
        if (scheduleSavingRef.current) return;
        const version = ++scheduleVersion.current;
        try {
            const next = await GetMobileBriefSchedule();
            if (mounted.current && version === scheduleVersion.current) {
                applySchedule(next);
                setError("scheduleLoad");
            }
        } catch {
            if (mounted.current && version === scheduleVersion.current) {
                setError("scheduleLoad", t("mobileConnection.scheduleLoadFailed"));
            }
        }
    }, [applySchedule, setError, t]);

    const refreshHealth = useCallback(async () => {
        try {
            const next = await GetMobileBriefHealth();
            if (mounted.current) setHealth(next);
        } catch {
            // The schedule card reads without it.
        }
    }, []);

    const refreshKeepAwake = useCallback(async () => {
        const version = ++keepAwakeVersion.current;
        try {
            const next = await GetMobileKeepAwake();
            if (mounted.current && version === keepAwakeVersion.current) setKeepAwake(next);
        } catch {
            // The switch keeps its last value; the status card reports connection trouble.
        }
    }, []);

    // Turning the switch on or off applies at once here; the computer holds or
    // releases the assertion and the phone reads the same value on its next refresh.
    const setKeepAwakeOn = useCallback(
        async (on: boolean) => {
            const previous = keepAwake;
            const version = ++keepAwakeVersion.current;
            setKeepAwake((current) => (current ? { ...current, on } : current));
            try {
                const next = await SetMobileKeepAwake(on);
                if (mounted.current && version === keepAwakeVersion.current) {
                    setKeepAwake(next);
                    setError("keepAwake");
                }
            } catch {
                if (mounted.current && version === keepAwakeVersion.current) {
                    setKeepAwake(previous);
                    setError("keepAwake", t("mobileConnection.keepAwakeFailed"));
                }
            }
        },
        [keepAwake, setError, t],
    );

    const refreshAll = useCallback(() => {
        void refreshStatus();
        void refreshSchedule();
        void refreshHealth();
        void refreshKeepAwake();
    }, [refreshHealth, refreshKeepAwake, refreshSchedule, refreshStatus]);

    useEffect(() => {
        mounted.current = true;
        refreshAll();
        const unsubscribe = EventsOn("mobile:updated", () => refreshAll());
        const onFocus = () => refreshAll();
        window.addEventListener("focus", onFocus);
        // Activity can defer a prediction without a mobile event. Keep the
        // visible schedule current through the same versioned read as focus.
        // The timer stops while the window is hidden or the Mac locked.
        const stopSchedulePoll = activeInterval(() => {
            if (document.visibilityState === "visible" && document.hasFocus()) void refreshSchedule();
        }, SCHEDULE_POLL_MS);
        return () => {
            mounted.current = false;
            statusVersion.current += 1;
            scheduleVersion.current += 1;
            window.removeEventListener("focus", onFocus);
            stopSchedulePoll();
            unsubscribe();
        };
    }, [refreshAll, refreshSchedule]);

    const connectionCode = status?.connectionCode ?? "";
    const hasCode = connectionCode !== "";

    useEffect(() => {
        setNow(Date.now());
        return activeInterval(() => setNow(Date.now()), hasCode ? CODE_TICK_MS : IDLE_TICK_MS);
    }, [hasCode]);

    // Fallback refresh while a code is on screen, so a claim that arrives without an event still shows up.
    useEffect(() => {
        if (!hasCode) return;
        return activeInterval(() => void refreshStatus(), STATUS_POLL_MS);
    }, [hasCode, refreshStatus]);

    useEffect(() => {
        setCopied(false);
    }, [connectionCode]);

    const runStatusAction = async (
        kind: StatusAction,
        call: () => Promise<MobileConnectionStatus>,
        scope: ErrorScope,
        failure: string,
        onSuccess?: () => void,
    ) => {
        if (actionRef.current) return;
        actionRef.current = kind;
        statusVersion.current += 1;
        setAction(kind);
        setError(scope);
        try {
            const next = await call();
            if (mounted.current) {
                setStatus(next);
                setError("statusLoad");
                onSuccess?.();
            }
        } catch {
            if (mounted.current) setError(scope, failure);
        } finally {
            actionRef.current = null;
            if (mounted.current) setAction(null);
        }
    };

    const enabled = Boolean(status?.enabled);
    const busy = action !== null;
    const devices = status?.devices ?? [];
    const hasDevices = devices.length > 0;
    const pending: MobilePendingPairing | null = status?.pendingPairing ?? null;
    const expiresAt = parseTimestamp(status?.expiresAt ?? "");
    const secondsLeft = expiresAt === null ? 0 : Math.max(0, Math.ceil((expiresAt - now) / 1000));
    const codeLive = hasCode && secondsLeft > 0;
    const codeExpired = hasCode && secondsLeft === 0;
    const showCode = enabled && codeLive && pending === null;
    const approvalOpen = pending !== null && dismissedPairingId !== pending.pairingId;

    const relayValue = relayDraft ?? status?.savedRelayUrl ?? "";
    // The wire message remains available to older clients. Desktop copy uses
    // the current frontend language, even if the Mac generated it in another.
    const connectionWarning = status?.warningCode === "relay_loopback" && status.warningHost
        ? t("mobileConnection.loopbackWarning", { host: status.warningHost })
        : null;
    const describeStatus = (
        current: MobileConnectionStatus,
    ): { label: string; description: string; on: boolean } => {
        if (!current.enabled) {
            return {
                label: t("mobileConnection.statusOff"),
                description: t("mobileConnection.statusOffDescription"),
                on: false,
            };
        }
        switch (current.relayState) {
            case "connected": {
                const seenAt = parseTimestamp(current.relayLastSeenAt);
                const host = relayHostname(current.relayUrl).toLowerCase();
                const localOnly =
                    current.warningCode === "relay_loopback" || host === "127.0.0.1" || host === "localhost";
                const description =
                    current.remoteAccess?.state === "ready"
                        ? seenAt === null
                            ? t("mobileConnection.statusConnectedRemoteNoTime")
                            : t("mobileConnection.statusConnectedRemote", {
                                  time: formatRelative(seenAt, now, locale),
                              })
                        : current.remoteAccess
                          ? t("mobileConnection.statusConnectedRemoteNotReady")
                          : localOnly
                            ? seenAt === null
                                ? t("mobileConnection.statusConnectedLocalOnlyNoTime")
                                : t("mobileConnection.statusConnectedLocalOnly", {
                                      time: formatRelative(seenAt, now, locale),
                                  })
                            : seenAt === null
                              ? t("mobileConnection.statusConnectedNoTime")
                              : t("mobileConnection.statusConnectedDescription", {
                                    time: formatRelative(seenAt, now, locale),
                                });
                return {
                    label: t("mobileConnection.statusConnected"),
                    description,
                    on: true,
                };
            }
            case "offline":
                return {
                    label: t("mobileConnection.statusOffline"),
                    description: t("mobileConnection.statusOfflineDescription"),
                    on: false,
                };
            default:
                return {
                    label: t("mobileConnection.statusConnecting"),
                    description: t("mobileConnection.statusConnectingDescription"),
                    on: false,
                };
        }
    };

    const turnOn = () => {
        const raw = relayValue.trim();
        let relay = "";
        if (raw !== "") {
            const origin = relayOriginFrom(raw);
            if (origin === null) {
                setError(
                    "relay",
                    t("mobileConnection.serverInvalidAdvanced"),
                );
                return;
            }
            relay = origin;
        }
        void runStatusAction(
            "enable",
            () => EnableMobileConnectionUsingRelay(relay),
            "relay",
            t("mobileConnection.turnOnFailed"),
            () => setRelayDraft(null),
        );
    };

    const turnOff = () => {
        void runStatusAction(
            "disable",
            () => DisableMobileConnection(),
            "relay",
            t("mobileConnection.turnOffFailed"),
        );
    };

    const createCode = () => {
        void runStatusAction(
            "code",
            () => GenerateMobileConnectionCode(),
            "code",
            t("mobileConnection.generateFailed"),
        );
    };

    const copyLink = async () => {
        if (!connectionCode) return;
        setError("copy");
        const ok = await copyText(connectionCode);
        if (!mounted.current) return;
        if (ok) {
            setCopied(true);
        } else {
            setError("copy", t("mobileConnection.copyFailed"));
        }
    };

    const decide = (approve: boolean) => {
        if (!pending) return;
        const { pairingId } = pending;
        void runStatusAction(
            approve ? "approve" : "decline",
            () => ConfirmMobilePairing(pairingId, approve),
            "approval",
            t("mobileConnection.approvalFailed"),
        );
    };

    const deferApproval = () => {
        if (busy || !pending) return;
        setDismissedPairingId(pending.pairingId);
    };

    const revoke = () => {
        if (!revokeTarget) return;
        const { deviceId } = revokeTarget;
        void runStatusAction(
            "revoke",
            () => RevokeMobileDevice(deviceId),
            "revoke",
            t("mobileConnection.revokeFailed"),
            () => setRevokeTarget(null),
        );
    };

    const closeRevoke = () => {
        if (action === "revoke") return;
        setRevokeTarget(null);
        setError("revoke");
    };

    const resetRemoteAccess = () => {
        void runStatusAction(
            "remote-reset",
            () => ResetMobileRemoteAccess(),
            "remoteReset",
            t("mobileConnection.remoteResetFailed"),
            () => setRemoteResetOpen(false),
        );
    };

    const closeRemoteReset = () => {
        if (action === "remote-reset") return;
        setRemoteResetOpen(false);
        setError("remoteReset");
    };

    const switchToRemoteAccess = () => {
        void runStatusAction(
            "enable",
            () => EnableMobileConnectionUsingRelay(""),
            "remoteSwitch",
            t("mobileConnection.remoteSwitchFailed"),
            () => {
                setRelayDraft(null);
                setRemoteSwitchOpen(false);
            },
        );
    };

    const closeRemoteSwitch = () => {
        if (action === "enable") return;
        setRemoteSwitchOpen(false);
        setError("remoteSwitch");
    };

    const shownSchedule: ScheduleDraft | null =
        scheduleDraft ?? (schedule ? scheduleDraftOf(schedule) : null);
    const morningValid = shownSchedule !== null && CLOCK_PATTERN.test(shownSchedule.morning);
    const eveningValid = shownSchedule !== null && CLOCK_PATTERN.test(shownSchedule.evening);

    // Saves on change, one request at a time; the newest change wins. A failed
    // save puts the controls back to the saved schedule and says so.
    const persistSchedule = async (next: ScheduleDraft) => {
        pendingScheduleRef.current = next;
        if (scheduleSavingRef.current) return;
        scheduleSavingRef.current = true;
        scheduleVersion.current += 1;
        setError("schedule");
        try {
            while (pendingScheduleRef.current !== null) {
                const draft = pendingScheduleRef.current;
                pendingScheduleRef.current = null;
                const base = scheduleRef.current;
                if (base === null) break;
                const saved = await SetMobileBriefSchedule({ ...base, ...draft });
                if (!mounted.current) return;
                applySchedule(saved);
                setError("scheduleLoad");
                const savedDraft = scheduleDraftOf(saved);
                setScheduleDraft((current) =>
                    current !== null && sameDraft(current, savedDraft) ? null : current,
                );
            }
        } catch {
            pendingScheduleRef.current = null;
            if (mounted.current) {
                setScheduleDraft(null);
                setError("schedule", t("mobileConnection.scheduleSaveFailed"));
            }
        } finally {
            scheduleSavingRef.current = false;
        }
    };

    const patchSchedule = (patch: Partial<ScheduleDraft>) => {
        if (!schedule || !shownSchedule) return;
        const next = { ...shownSchedule, ...patch };
        setScheduleDraft(next);
        setError("schedule");
        // A half-typed time stays on screen with a hint; the rest still saves.
        const savable: ScheduleDraft = {
            enabled: next.enabled,
            morning: CLOCK_PATTERN.test(next.morning) ? next.morning : schedule.morning,
            evening: CLOCK_PATTERN.test(next.evening) ? next.evening : schedule.evening,
            feedEnabled: next.feedEnabled,
            adaptiveTiming: next.adaptiveTiming,
        };
        // While a save is running, queue even a change back to the saved
        // value, so the newest choice is what ends up stored.
        const saved = scheduleRef.current ?? schedule;
        if (scheduleSavingRef.current || !sameDraft(savable, scheduleDraftOf(saved))) {
            void persistSchedule(savable);
        }
    };

    const announceBrief = (result: MobileBriefRequestResult) => {
        switch (result.status) {
            case "started":
                notifyToast(
                    t("mobileConnection.briefStartedTitle"),
                    t("mobileConnection.briefStartedBody"),
                    "success",
                );
                return;
            case "running":
                notifyToast(
                    t("mobileConnection.briefRunningTitle"),
                    t("mobileConnection.briefRunningBody"),
                    "info",
                );
                return;
            case "cooldown":
                notifyToast(
                    t("mobileConnection.briefCooldownTitle"),
                    t("mobileConnection.briefCooldownBody", {
                        time: formatClock(result.nextAllowedAt, locale) ?? t("mobileConnection.unknownTime"),
                    }),
                    "info",
                );
                return;
        }
    };

    const generateBrief = async () => {
        if (briefRef.current) return;
        briefRef.current = true;
        setBriefBusy(true);
        setError("brief");
        try {
            const result = await GenerateMobileBriefNow();
            if (mounted.current) announceBrief(result);
            void refreshSchedule();
        } catch {
            if (mounted.current) setError("brief", t("mobileConnection.briefFailed"));
        } finally {
            briefRef.current = false;
            if (mounted.current) setBriefBusy(false);
        }
    };

    const slotLabel = (slot: string): string | null => {
        switch (slot) {
            case "morning":
                return t("mobileConnection.slotMorning");
            case "evening":
                return t("mobileConnection.slotEvening");
            case "on_request":
                return t("mobileConnection.slotOnRequest");
            default:
                return null;
        }
    };

    const describeLastBrief = (current: MobileBriefSchedule): string => {
        const generatedAt = formatDateTime(current.lastGeneratedAt, locale);
        if (generatedAt === null) return t("mobileConnection.briefNever");
        const slot = slotLabel(current.lastSlot);
        return slot === null
            ? t("mobileConnection.briefLastNoSlot", { time: generatedAt })
            : t("mobileConnection.briefLast", { slot, time: generatedAt });
    };

    const describeTimingReason = (reason: string | undefined): string => {
        switch (reason) {
            case "fixed_slot":
                return t("mobileConnection.briefTimingFixed");
            case "learned_opens":
                return t("mobileConnection.briefTimingOpens");
            case "learned_presence":
                return t("mobileConnection.briefTimingPresence");
            case "learned_activity":
                return t("mobileConnection.briefTimingActivity");
            case "before_meeting":
                return t("mobileConnection.briefTimingMeeting");
            case "work_settled":
                return t("mobileConnection.briefTimingSettled");
            case "quiet_hours":
                return t("mobileConnection.briefTimingQuiet");
            case "waiting_for_activity":
                return t("mobileConnection.briefTimingWaiting");
            case "no_window":
                return t("mobileConnection.briefTimingNoWindow");
            default:
                return t("mobileConnection.briefTimingEstimate");
        }
    };

    const describePrediction = (current: MobileBriefSchedule | null): string | null => {
        if (!current?.enabled) return null;
        const reason = describeTimingReason(current.timingReason);
        const time = formatDateTime(current.nextPredictedAt ?? "", locale);
        const slot = slotLabel(current.nextPredictedSlot ?? "");
        if (time !== null && slot !== null) {
            return t("mobileConnection.briefPredicted", { slot, time, reason });
        }
        return current.timingReason === "no_window"
            ? t("mobileConnection.briefPredictionUnavailable", { reason })
            : null;
    };

    const prediction = describePrediction(schedule);

    // The health lines under the last brief: the next run, then the missed and
    // failing runs of the last week when there are any.
    const describeHealth = (current: MobileBriefHealth | null): string[] => {
        if (!current) return [];
        const lines: string[] = [];
        const nextAt = formatDateTime(current.nextRunAt, locale);
        const nextSlot = slotLabel(current.nextSlot);
        if (prediction === null && current.enabled && nextAt !== null && nextSlot !== null) {
            lines.push(t("mobileConnection.briefNext", { slot: nextSlot, time: nextAt }));
        }
        if (current.missedRuns > 0) {
            lines.push(t("mobileConnection.briefMissedWeek", { count: current.missedRuns }));
        }
        const failingSlot = slotLabel(current.failingSlot);
        if (failingSlot !== null && current.failedAttempts > 0) {
            lines.push(
                t("mobileConnection.briefFailing", { slot: failingSlot, count: current.failedAttempts }),
            );
        }
        return lines;
    };

    const deviceName = (device: { deviceName: string }) =>
        device.deviceName || t("mobileConnection.unnamedDevice");

    const connection = status ? describeStatus(status) : null;
    const pendingName = pending ? deviceName(pending) : "";
    const revokeName = revokeTarget ? deviceName(revokeTarget) : "";

    const loadState = (
        <LoadState
            failed={Boolean(errors.statusLoad)}
            loadingText={t("mobileConnection.loading")}
            failedText={t("mobileConnection.statusFailed")}
            onRetry={() => void refreshStatus()}
        />
    );

    // The address input while the connection is off, the saved address while it is on.
    const serverField =
        status === null ? null : (
            <div className="flex flex-col gap-2 pt-1 pb-1">
                {enabled ? (
                    <>
                        <p className="break-all font-mono text-xs text-ink">
                            {status.remoteAccess
                                ? t("mobileConnection.serverRemoteAccess")
                                : status.relayUrl || t("mobileConnection.serverNotSet")}
                        </p>
                        <p className="settings-row-desc">{t("mobileConnection.serverLockedHint")}</p>
                    </>
                ) : (
                    <>
                        {/* Under Advanced the card title already names the field. */}
                        <label
                            htmlFor="mobile-relay-url"
                            className="sr-only"
                        >
                            {t("mobileConnection.serverTitle")}
                        </label>
                        <input
                            id="mobile-relay-url"
                            type="url"
                            inputMode="url"
                            autoComplete="off"
                            spellCheck={false}
                            placeholder={status.relayUrl || t("mobileConnection.serverPlaceholder")}
                            value={relayValue}
                            disabled={busy}
                            onChange={(event) => {
                                setRelayDraft(event.target.value);
                                setError("relay");
                            }}
                            className={RELAY_INPUT_CLASS}
                        />
                    </>
                )}
            </div>
        );

    return (
        <>
            <SettingsCard>
                {status === null || connection === null ? (
                    loadState
                ) : (
                    <div className="flex flex-col">
                        <SettingRow
                            title={
                                <span className="inline-flex flex-wrap items-center gap-2">
                                    {t("mobileConnection.statusLabel")}
                                    <span className={connection.on ? STATUS_CHIP_ON_CLASS : STATUS_CHIP_CLASS}>
                                        {connection.label}
                                    </span>
                                </span>
                            }
                            description={connection.description}
                            control={
                                enabled ? (
                                    <Button
                                        type="button"
                                        variant="outline"
                                        size="sm"
                                        disabled={busy}
                                        onClick={turnOff}
                                    >
                                        {action === "disable" && <Loader2 className="size-3.5 animate-spin" />}
                                        {t("mobileConnection.turnOff")}
                                    </Button>
                                ) : (
                                    <Button
                                        type="button"
                                        size="sm"
                                        disabled={busy}
                                        onClick={turnOn}
                                    >
                                        {action === "enable" && <Loader2 className="size-3.5 animate-spin" />}
                                        {t("mobileConnection.turnOn")}
                                    </Button>
                                )
                            }
                        />
                        {connectionWarning && (
                            <p role="status" className="pb-2 text-xs text-warning">{connectionWarning}</p>
                        )}
                        {errors.relay && (
                            <p role="alert" className="pb-2 text-xs text-error">
                                {errors.relay}
                            </p>
                        )}
                    </div>
                )}
            </SettingsCard>

            {status !== null && (
                <SettingsCard
                    title={t("mobileConnection.remoteAccessTitle")}
                    description={t("mobileConnection.remoteAccessDescription")}
                >
                    <SettingRow
                        title={t("mobileConnection.remoteAccessStatus")}
                        description={
                            status.remoteAccess
                                ? t(`${REMOTE_ACCESS_STATE_DETAIL_TRANSLATION_PREFIX}.${status.remoteAccess.state}`)
                                : status.remoteAccessOffReason
                                  ? t(`${REMOTE_ACCESS_OFF_TRANSLATION_PREFIX}.${status.remoteAccessOffReason}`, {
                                        host: relayHostname(status.relayUrl),
                                    })
                                  : t("mobileConnection.remoteAccessStateDetail.off")
                        }
                        control={
                            status.remoteAccess ? (
                                <Button
                                    type="button"
                                    variant="outline"
                                    size="sm"
                                    disabled={busy || !enabled}
                                    onClick={() => {
                                        setError("remoteReset");
                                        setRemoteResetOpen(true);
                                    }}
                                >
                                    {t("mobileConnection.remoteAccessReset")}
                                </Button>
                            ) : status.remoteAccessOffReason === "saved_relay" ? (
                                <Button
                                    type="button"
                                    variant="outline"
                                    size="sm"
                                    disabled={busy}
                                    onClick={() => {
                                        setError("remoteSwitch");
                                        setRemoteSwitchOpen(true);
                                    }}
                                >
                                    {t("mobileConnection.remoteAccessUseButton")}
                                </Button>
                            ) : null
                        }
                    >
                        {status.remoteAccess &&
                            (status.remoteAccess.state === "failed" ||
                                status.remoteAccess.state === "restarting") && (
                            <p role="status" className="text-xs text-warning">
                                {t("mobileConnection.remoteAccessUnavailable")}
                            </p>
                            )}
                    </SettingRow>
                </SettingsCard>
            )}

            <SettingsCard
                title={hasDevices ? t("mobileConnection.devicesTitle") : t("mobileConnection.pairTitle")}
                description={
                    hasDevices ? t("mobileConnection.devicesDescription") : t("mobileConnection.pairDescription")
                }
            >
                {status === null ? (
                    loadState
                ) : (
                    <div className="flex flex-col gap-4 pb-1">
                        <button
                            type="button"
                            className="self-start text-xs text-accent hover:underline"
                            onClick={() => BrowserOpenURL(IPHONE_APP_LINK)}
                        >
                            {t("mobileConnection.getApp")}
                        </button>
                        {hasDevices && keepAwake !== null && (
                            <SettingRow
                                title={t("mobileConnection.keepAwakeTitle")}
                                description={keepAwake.hasBattery
                                    ? t("mobileConnection.keepAwakeDescription")
                                    : t("mobileConnection.keepAwakeDescriptionDesktop")}
                                control={
                                    <Switch
                                        aria-label={t("mobileConnection.keepAwakeTitle")}
                                        checked={keepAwake.on}
                                        onCheckedChange={(checked) => void setKeepAwakeOn(checked)}
                                    />
                                }
                            >
                                {errors.keepAwake && (
                                    <p role="alert" className="text-xs text-error">
                                        {errors.keepAwake}
                                    </p>
                                )}
                            </SettingRow>
                        )}
                        {hasDevices && (
                            <div className="flex flex-col">
                                {devices.map((device) => {
                                    const name = deviceName(device);
                                    const seenAt = parseTimestamp(device.lastSeenAt);
                                    return (
                                        <SettingRow
                                            key={device.deviceId}
                                            title={name}
                                            description={
                                                seenAt === null
                                                    ? t("mobileConnection.neverSeen")
                                                    : t("mobileConnection.lastSeen", {
                                                          time: formatRelative(seenAt, now, locale),
                                                      })
                                            }
                                            control={
                                                <Button
                                                    type="button"
                                                    variant="destructive"
                                                    size="sm"
                                                    disabled={busy}
                                                    aria-label={t("mobileConnection.disconnectNamed", { name })}
                                                    onClick={() => {
                                                        setError("revoke");
                                                        setRevokeTarget(device);
                                                    }}
                                                >
                                                    {t("mobileConnection.disconnect")}
                                                </Button>
                                            }
                                        />
                                    );
                                })}
                            </div>
                        )}

                        {pending !== null && !approvalOpen && (
                            <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-border bg-background-accent/30 p-3">
                                <p className="text-sm text-ink">
                                    {t("mobileConnection.waitingForApproval", { name: pendingName })}
                                </p>
                                <Button
                                    type="button"
                                    variant="outline"
                                    size="sm"
                                    onClick={() => setDismissedPairingId(null)}
                                >
                                    {t("mobileConnection.review")}
                                </Button>
                            </div>
                        )}

                        {showCode && (
                            <div className="flex flex-col items-center gap-3 rounded-lg border border-border bg-background-accent/30 p-4">
                                <div className="rounded-md bg-panel p-3">
                                    <QRCodeSVG
                                        value={connectionCode}
                                        size={220}
                                        level="M"
                                        marginSize={4}
                                        title={t("mobileConnection.qrTitle")}
                                    />
                                </div>
                                <p className="text-center text-sm text-ink">
                                    {t("mobileConnection.scanInstruction")}
                                </p>
                                {connectionWarning && (
                                    <p role="status" className="text-center text-xs text-warning">{connectionWarning}</p>
                                )}
                                <p className="text-xs tabular-nums text-muted">
                                    {t("mobileConnection.expiresIn", { time: formatCountdown(secondsLeft) })}
                                </p>
                                <Button type="button" variant="outline" size="sm" onClick={() => void copyLink()}>
                                    {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
                                    {copied ? t("mobileConnection.copiedLink") : t("mobileConnection.copyLink")}
                                </Button>
                                <p className="max-w-md text-center text-xs text-muted">
                                    {t("mobileConnection.linkHint")}
                                </p>
                                {errors.copy && (
                                    <p role="alert" className="text-xs text-error">
                                        {errors.copy}
                                    </p>
                                )}
                            </div>
                        )}

                        {!enabled && (
                            <p className="settings-row-desc">{t("mobileConnection.pairOffHint")}</p>
                        )}
                        {enabled && pending === null && !showCode && (
                            codeExpired ? (
                                <p role="status" className="settings-row-desc">
                                    {t("mobileConnection.expired")}
                                </p>
                            ) : (
                                !hasDevices && (
                                    <p className="settings-row-desc">{t("mobileConnection.pairEmpty")}</p>
                                )
                            )
                        )}

                        <div className="flex flex-wrap items-center gap-3">
                            <Button
                                type="button"
                                variant={hasDevices && !showCode ? "outline" : "default"}
                                size={hasDevices && !showCode ? "sm" : "default"}
                                disabled={!enabled || busy || pending !== null}
                                onClick={createCode}
                            >
                                {action === "code" ? (
                                    <Loader2 className="size-4 animate-spin" />
                                ) : showCode ? (
                                    <RefreshCw className="size-4" />
                                ) : null}
                                {showCode
                                    ? t("mobileConnection.newCode")
                                    : hasDevices
                                      ? t("mobileConnection.pairAnother")
                                      : t("mobileConnection.showCode")}
                            </Button>
                            {errors.code && (
                                <p role="alert" className="text-xs text-error">
                                    {errors.code}
                                </p>
                            )}
                        </div>
                    </div>
                )}
            </SettingsCard>

            <SettingsCard
                title={t("mobileConnection.briefTitle")}
                description={t("mobileConnection.briefDescription")}
            >
                {schedule === null || shownSchedule === null ? (
                    <LoadState
                        failed={Boolean(errors.scheduleLoad)}
                        loadingText={t("mobileConnection.briefLoading")}
                        failedText={t("mobileConnection.scheduleLoadFailed")}
                        onRetry={() => void refreshSchedule()}
                    />
                ) : (
                    <div className="flex flex-col">
                        <SettingRow
                            title={t("mobileConnection.briefAutomatic")}
                            description={t("mobileConnection.briefAutomaticDescription")}
                            control={
                                <Switch
                                    aria-label={t("mobileConnection.briefAutomatic")}
                                    checked={shownSchedule.enabled}
                                    onCheckedChange={(checked) => patchSchedule({ enabled: checked })}
                                />
                            }
                        />
                        <SettingRow
                            title={t("mobileConnection.briefAdaptive")}
                            description={t("mobileConnection.briefAdaptiveDescription")}
                            control={
                                <Switch
                                    aria-label={t("mobileConnection.briefAdaptive")}
                                    checked={shownSchedule.adaptiveTiming ?? false}
                                    disabled={!shownSchedule.enabled}
                                    onCheckedChange={(checked) => patchSchedule({ adaptiveTiming: checked })}
                                />
                            }
                        >
                            <p className="text-xs text-muted">{t("mobileConnection.briefAdaptivePrivacy")}</p>
                        </SettingRow>
                        <SettingRow
                            title={t("mobileConnection.briefMorning")}
                            control={
                                <input
                                    type="time"
                                    aria-label={t("mobileConnection.briefMorning")}
                                    value={shownSchedule.morning}
                                    disabled={!shownSchedule.enabled}
                                    aria-invalid={!morningValid}
                                    onChange={(event) => patchSchedule({ morning: event.target.value })}
                                    className={TIME_INPUT_CLASS}
                                />
                            }
                        />
                        <SettingRow
                            title={t("mobileConnection.briefEvening")}
                            control={
                                <input
                                    type="time"
                                    aria-label={t("mobileConnection.briefEvening")}
                                    value={shownSchedule.evening}
                                    disabled={!shownSchedule.enabled}
                                    aria-invalid={!eveningValid}
                                    onChange={(event) => patchSchedule({ evening: event.target.value })}
                                    className={TIME_INPUT_CLASS}
                                />
                            }
                        />
                        <p className="settings-row-desc pt-3">
                            {shownSchedule.enabled
                                ? t("mobileConnection.briefTimesHint")
                                : t("mobileConnection.briefTimesOffHint")}
                        </p>
                        {prediction !== null && (
                            <p className="pt-2 text-xs text-muted">{prediction}</p>
                        )}
                        {shownSchedule.enabled && (!morningValid || !eveningValid) && (
                            <p role="alert" className="pt-2 text-xs text-error">
                                {t("mobileConnection.briefTimeInvalid")}
                            </p>
                        )}
                        {errors.schedule && (
                            <p role="alert" className="pt-2 text-xs text-error">
                                {errors.schedule}
                            </p>
                        )}
                        <SettingRow
                            title={t("mobileConnection.briefNowTitle")}
                            description={t("mobileConnection.briefNowDescription")}
                            control={
                                <Button
                                    type="button"
                                    variant="outline"
                                    size="sm"
                                    disabled={briefBusy}
                                    onClick={() => void generateBrief()}
                                >
                                    {briefBusy && <Loader2 className="size-3.5 animate-spin" />}
                                    {t("mobileConnection.generateBrief")}
                                </Button>
                            }
                        >
                            <p className="text-xs text-muted">{describeLastBrief(schedule)}</p>
                            {describeHealth(health).map((line) => (
                                <p key={line} className="text-xs text-muted">
                                    {line}
                                </p>
                            ))}
                        <SettingRow
                            title={t("mobileConnection.briefFeedTitle")}
                            description={t("mobileConnection.briefFeedDescription")}
                            control={
                                <Switch
                                    aria-label={t("mobileConnection.briefFeedTitle")}
                                    checked={shownSchedule.feedEnabled}
                                    onCheckedChange={(checked) => patchSchedule({ feedEnabled: checked })}
                                />
                            }
                        />
                        {shownSchedule.feedEnabled && (
                            <div className="flex flex-col gap-2 pt-1 pb-1">
                                {schedule.feedUrl ? (
                                    <>
                                        <p className="break-all font-mono text-xs text-ink" data-testid="brief-feed-url">
                                            {schedule.feedUrl}
                                        </p>
                                        <div>
                                            <Button
                                                type="button"
                                                variant="outline"
                                                size="sm"
                                                onClick={() =>
                                                    void copyText(schedule.feedUrl).then((ok) => {
                                                        if (mounted.current && ok) setFeedCopied(true);
                                                    })
                                                }
                                            >
                                                {feedCopied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
                                                {feedCopied
                                                    ? t("mobileConnection.briefFeedCopied")
                                                    : t("mobileConnection.briefFeedCopy")}
                                            </Button>
                                        </div>
                                    </>
                                ) : (
                                    <p className="text-xs text-muted">{t("mobileConnection.briefFeedStarting")}</p>
                                )}
                                <p className="settings-row-desc">{t("mobileConnection.briefFeedHint")}</p>
                            </div>
                        )}
                        </SettingRow>
                        {errors.brief && (
                            <p role="alert" className="pt-2 text-xs text-error">
                                {errors.brief}
                            </p>
                        )}
                    </div>
                )}
            </SettingsCard>

            {textMessages}

            <SettingsAdvanced pageId="iphone" hint={t("mobileConnection.advancedHint")}>
                {status !== null && (
                    <SettingsCard
                        title={t("mobileConnection.serverTitle")}
                        description={t("mobileConnection.serverDescription")}
                    >
                        {serverField}
                    </SettingsCard>
                )}

                {hasDevices && (
                    <SettingsCard
                        title={t("mobileConnection.accessTitle")}
                        description={t("mobileConnection.accessDescription")}
                    >
                        <div className="flex flex-col">
                            {devices.map((device) => {
                                const pairedOn = formatDate(device.createdAt, locale);
                                const accessUntil = formatDate(device.expiresAt, locale);
                                const labels = (device.scopes ?? []).flatMap((scope) =>
                                    isKnownScope(scope) ? [t(SCOPE_LABELS[scope])] : [],
                                );
                                return (
                                    <SettingRow
                                        key={device.deviceId}
                                        title={deviceName(device)}
                                        description={
                                            <span className="flex flex-wrap gap-x-3">
                                                {pairedOn && (
                                                    <span>{t("mobileConnection.pairedOn", { date: pairedOn })}</span>
                                                )}
                                                {accessUntil && (
                                                    <span>
                                                        {t("mobileConnection.accessUntil", { date: accessUntil })}
                                                    </span>
                                                )}
                                            </span>
                                        }
                                    >
                                        {labels.length > 0 ? (
                                            <ul className="mt-1 flex list-disc flex-col gap-0.5 pl-4 text-xs text-ink">
                                                {labels.map((label) => (
                                                    <li key={label}>{label}</li>
                                                ))}
                                            </ul>
                                        ) : (
                                            <p className="text-xs text-muted">{t("mobileConnection.accessNone")}</p>
                                        )}
                                    </SettingRow>
                                );
                            })}
                        </div>
                    </SettingsCard>
                )}

                {schedule !== null && !schedule.narrativeAvailable && (
                    <SettingsCard
                        title={t("mobileConnection.narrativeTitle")}
                        headerRight={
                            onOpenAssistant ? (
                                <Button size="sm" variant="outline" onClick={onOpenAssistant}>
                                    {t("mobileConnection.openAssistant")}
                                </Button>
                            ) : undefined
                        }
                    >
                        <p role="note" className="settings-card-desc">
                            {t("mobileConnection.narrativeUnavailable")}
                        </p>
                    </SettingsCard>
                )}

                {advancedExtra}
            </SettingsAdvanced>

            <Dialog
                open={approvalOpen}
                onOpenChange={(open) => {
                    if (!open) deferApproval();
                }}
            >
                <DialogContent>
                    <DialogHeader>
                        <DialogTitle>{t("mobileConnection.approvalTitle")}</DialogTitle>
                    </DialogHeader>
                    {pending && (
                        <>
                            <div className="px-6 py-5">
                                <DialogDescription>
                                    {t("mobileConnection.approvalDescription", { name: pendingName })}
                                </DialogDescription>
                                <p className="mt-5 text-center text-4xl font-semibold tabular-nums tracking-wider text-ink">
                                    {formatPairingCode(pending.code)}
                                </p>
                                <p className="mt-4 text-center text-xs text-muted">
                                    {t("mobileConnection.approvalCompare")}
                                </p>
                                <p className="mt-3 text-center text-xs text-muted">
                                    {t("mobileConnection.keepAwakeLastStep")}
                                </p>
                                {errors.approval && (
                                    <p role="alert" className="mt-3 text-xs text-error">
                                        {errors.approval}
                                    </p>
                                )}
                            </div>
                            <div className="flex flex-wrap justify-end gap-2 border-t border-border px-6 py-4">
                                <Button type="button" variant="outline" disabled={busy} onClick={deferApproval}>
                                    {t("mobileConnection.decideLater")}
                                </Button>
                                <Button type="button" variant="outline" disabled={busy} onClick={() => decide(false)}>
                                    {action === "decline" && <Loader2 className="size-3.5 animate-spin" />}
                                    {t("mobileConnection.decline")}
                                </Button>
                                <Button type="button" disabled={busy} onClick={() => decide(true)}>
                                    {action === "approve" && <Loader2 className="size-3.5 animate-spin" />}
                                    {t("mobileConnection.approve")}
                                </Button>
                            </div>
                        </>
                    )}
                </DialogContent>
            </Dialog>

            <Dialog
                open={Boolean(revokeTarget)}
                onOpenChange={(open) => {
                    if (!open) closeRevoke();
                }}
            >
                <DialogContent>
                    <DialogHeader>
                        <DialogTitle>{t("mobileConnection.disconnectTitle")}</DialogTitle>
                    </DialogHeader>
                    <div className="px-6 py-5">
                        <DialogDescription>
                            {t("mobileConnection.disconnectDescription", { name: revokeName })}
                        </DialogDescription>
                        {errors.revoke && (
                            <p role="alert" className="mt-3 text-xs text-error">
                                {errors.revoke}
                            </p>
                        )}
                    </div>
                    <div className="flex justify-end gap-2 border-t border-border px-6 py-4">
                        <Button type="button" variant="outline" disabled={busy} onClick={closeRevoke}>
                            {t("mobileConnection.cancel")}
                        </Button>
                        <Button type="button" variant="destructive" disabled={busy} onClick={revoke}>
                            {action === "revoke" && <Loader2 className="size-3.5 animate-spin" />}
                            {t("mobileConnection.confirmDisconnect")}
                        </Button>
                    </div>
                </DialogContent>
            </Dialog>

            <Dialog
                open={remoteResetOpen}
                onOpenChange={(open) => {
                    if (!open) closeRemoteReset();
                }}
            >
                <DialogContent>
                    <DialogHeader>
                        <DialogTitle>{t("mobileConnection.remoteResetTitle")}</DialogTitle>
                    </DialogHeader>
                    <div className="px-6 py-5">
                        <DialogDescription>
                            {t("mobileConnection.remoteResetDescription")}
                        </DialogDescription>
                        {errors.remoteReset && (
                            <p role="alert" className="mt-3 text-xs text-error">
                                {errors.remoteReset}
                            </p>
                        )}
                    </div>
                    <div className="flex justify-end gap-2 border-t border-border px-6 py-4">
                        <Button type="button" variant="outline" disabled={busy} onClick={closeRemoteReset}>
                            {t("mobileConnection.cancel")}
                        </Button>
                        <Button type="button" variant="outline" disabled={busy} onClick={resetRemoteAccess}>
                            {action === "remote-reset" && <Loader2 className="size-3.5 animate-spin" />}
                            {t("mobileConnection.remoteResetConfirm")}
                        </Button>
                    </div>
                </DialogContent>
            </Dialog>

            <Dialog
                open={remoteSwitchOpen}
                onOpenChange={(open) => {
                    if (!open) closeRemoteSwitch();
                }}
            >
                <DialogContent>
                    <DialogHeader>
                        <DialogTitle>{t("mobileConnection.remoteSwitchTitle")}</DialogTitle>
                    </DialogHeader>
                    <div className="px-6 py-5">
                        <DialogDescription>
                            {t("mobileConnection.remoteSwitchDescription", {
                                host: relayHostname(status?.relayUrl ?? ""),
                            })}
                        </DialogDescription>
                        {errors.remoteSwitch && (
                            <p role="alert" className="mt-3 text-xs text-error">
                                {errors.remoteSwitch}
                            </p>
                        )}
                    </div>
                    <div className="flex justify-end gap-2 border-t border-border px-6 py-4">
                        <Button type="button" variant="outline" disabled={busy} onClick={closeRemoteSwitch}>
                            {t("mobileConnection.cancel")}
                        </Button>
                        <Button type="button" variant="outline" disabled={busy} onClick={switchToRemoteAccess}>
                            {action === "enable" && <Loader2 className="size-3.5 animate-spin" />}
                            {t("mobileConnection.remoteSwitchConfirm")}
                        </Button>
                    </div>
                </DialogContent>
            </Dialog>
        </>
    );
}
