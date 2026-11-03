import { useCallback, useEffect, useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { QRCodeSVG } from "qrcode.react";
import { activeInterval } from "@/lib/app-activity";
import { Info, RefreshCw, Loader2, Check, Globe } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Avatar } from "@/components/ui/avatar";
import { CopyButton } from "@/components/copy-button";
import {
    DisconnectFeishu,
    GetArchivedFeishuApp,
    ReuseArchivedFeishuApp,
    GetLarkAccountStatus,
    LarkCliUserLoginStart,
    LarkCliUserLoginPoll,
    GetFeishuChats,
    GetFeishuReceiverStatus,
    GetFeishuScanChatFilter,
    GetFeishuStatus,
    RefreshFeishuPermissions,
    SetFeishuScanChatFilter,
    StartFeishuDaemon,
    RestartFeishuReceiver,
    StartFeishuRegistration,
    PollFeishuRegistration,
} from "../../../../../wailsjs/go/main/App";
import { BrowserOpenURL } from "../../../../../wailsjs/runtime/runtime";
import type { main } from "../../../../../wailsjs/go/models";
import { FeishuIcon } from "@/components/icons/app-icons";
import { toast } from "sonner";
import { notifyImportant } from "@/lib/notify";
import { Checkbox } from "@/components/ui/checkbox";
import { SettingsCard, SettingsRow } from "@/components/settings/settings-primitives";
import { SettingsAdvanced } from "@/components/settings/settings-advanced";
import type { ChatAppStatus } from "./chat-app-status";
import { DisconnectButton } from "./disconnect-button";
import { PairingCodeBlock } from "./pairing-code-block";

type FeishuRegistration = {
    session_id: string;
    verification_uri_complete: string;
    verification_uri: string;
    user_code: string;
    expires_in: number;
    interval: number;
};

type FeishuArchivedApp = {
    app_id: string;
    feishu_user_name?: string;
    feishu_account_id?: string;
    brand?: string;
    connected_at?: string;
};

// ── Feishu settings ──

const FEISHU_FULL_PERMISSION_IMPORT = JSON.stringify({
    scopes: {
        tenant: [
            "application:application:self_manage",
            "application:bot.basic_info:read",
            "application:bot.menu:write",
            "cardkit:card:read",
            "cardkit:card:write",
            "contact:contact.base:readonly",
            "docs:document.comment:create",
            "docs:document.comment:delete",
            "docs:document.comment:read",
            "docs:document.comment:update",
            "docs:document.comment:write_only",
            "docx:document:create",
            "docx:document:readonly",
            "docx:document:write_only",
            "docx:document.block:convert",
            "drive:drive.metadata:readonly",
            "im:chat.members:bot_access",
            "im:chat:create",
            "im:chat:read",
            "im:chat:update",
            "im:message.group_at_msg.include_bot:readonly",
            "im:message.group_at_msg:readonly",
            "im:message.p2p_msg:readonly",
            "im:message.pins:read",
            "im:message.pins:write_only",
            "im:message.reactions:read",
            "im:message.reactions:write_only",
            "im:message:readonly",
            "im:message:recall",
            "im:message:send_as_bot",
            "im:message:send_multi_users",
            "im:message:send_sys_msg",
            "im:message:update",
            "im:resource",
            "wiki:node:read",
        ],
        user: [
            "contact:user.employee_id:readonly",
            "offline_access",
            "base:app:copy",
            "base:field:create",
            "base:field:delete",
            "base:field:read",
            "base:field:update",
            "base:record:create",
            "base:record:delete",
            "base:record:retrieve",
            "base:record:update",
            "base:table:create",
            "base:table:read",
            "base:table:update",
            "base:view:read",
            "base:view:write_only",
            "base:app:create",
            "base:app:update",
            "base:app:read",
            "sheets:spreadsheet.meta:read",
            "sheets:spreadsheet:read",
            "sheets:spreadsheet:create",
            "sheets:spreadsheet:write_only",
            "docs:document:export",
            "docs:document.media:upload",
            "board:whiteboard:node:create",
            "board:whiteboard:node:read",
            "calendar:calendar:read",
            "calendar:calendar.event:create",
            "calendar:calendar.event:read",
            "calendar:calendar.event:reply",
            "calendar:calendar.event:update",
            "calendar:calendar.free_busy:read",
            "contact:contact.base:readonly",
            "contact:user.base:readonly",
            "contact:user:search",
            "docs:document.comment:create",
            "docs:document.comment:read",
            "docs:document.comment:update",
            "docs:document.media:download",
            "docs:document:copy",
            "docx:document:create",
            "docx:document:readonly",
            "docx:document:write_only",
            "drive:drive.metadata:readonly",
            "drive:file:download",
            "drive:file:upload",
            "im:chat.members:read",
            "im:chat:read",
            "im:message",
            "im:message.group_msg:get_as_user",
            "im:message.p2p_msg:get_as_user",
            "im:message:readonly",
            "search:docs:read",
            "search:message",
            "space:document:move",
            "space:document:retrieve",
            "task:comment:read",
            "task:comment:write",
            "task:task:read",
            "task:task:write",
            "task:task:writeonly",
            "task:tasklist:read",
            "task:tasklist:write",
            "wiki:node:copy",
            "wiki:node:create",
            "wiki:node:move",
            "wiki:node:read",
            "wiki:node:retrieve",
            "wiki:space:read",
            "wiki:space:retrieve",
            "wiki:space:write_only",
            "contact:user.basic_profile:readonly",
            "mail:user_mailbox.message:send",
            "mail:user_mailbox.message:readonly",
            "mail:user_mailbox.message:modify",
            "mail:user_mailbox.folder:read",
        ],
    },
}, null, 2);
const FEISHU_OPEN_PLATFORM_APPS_URL = "https://open.feishu.cn/app";

function feishuPermissionURL(appID?: string) {
    if (!appID) {
        return FEISHU_OPEN_PLATFORM_APPS_URL;
    }
    return `https://open.feishu.cn/app/${encodeURIComponent(appID)}/auth`;
}

function withFeishuTimeout<T>(promise: Promise<T>, timeoutMs: number, timeoutMessage: string): Promise<T> {
    return new Promise((resolve, reject) => {
        const timer = window.setTimeout(() => {
            reject(new Error(timeoutMessage));
        }, timeoutMs);
        promise.then(
            (value) => {
                window.clearTimeout(timer);
                resolve(value);
            },
            (error) => {
                window.clearTimeout(timer);
                reject(error);
            },
        );
    });
}

function ReceiverErrorHint() {
    const { t } = useTranslation('settings');
    return (
        <span className="group relative ml-auto flex-shrink-0 cursor-default">
            <Info className="h-3.5 w-3.5 text-error" />
            <span className="pointer-events-none absolute bottom-full right-0 mb-1.5 w-64 rounded-md bg-ink px-2.5 py-2 text-[11px] leading-relaxed text-background opacity-0 shadow-md transition-opacity group-hover:opacity-100">
                <Trans
                    i18nKey="connectors.feishu.receiverErrorHint"
                    t={t}
                    components={{ strong: <strong /> }}
                />
            </span>
        </span>
    );
}

export function FeishuSettings({ brand, onStatusChange }: { brand: "feishu" | "lark"; onStatusChange: (status: ChatAppStatus) => void }) {
    const { t } = useTranslation('settings');
    const isLark = brand === "lark";
    const brandTitle = isLark ? t('connectors.feishu.larkTitle') : t('connectors.feishu.title');
    // Brand-aware translator: injects {{brand}} so shared Feishu strings render
    // with the correct platform name (Feishu vs Lark).
    const tb = (key: string, opts?: Record<string, unknown>): string =>
        String(t(key as never, { brand: brandTitle, ...(opts ?? {}) } as never));
    const [status, setStatus] = useState<{
        connected: boolean;
        app_id?: string;
        bot_name?: string;
        feishu_user_name?: string;
        feishu_account_id?: string;
        full_permissions?: {
            checked?: boolean;
            ready?: boolean;
            missing?: string[];
            granted_count?: number;
            required_count?: number;
        };
        permission_ready?: boolean;
        daemon_online: boolean;
        paired?: boolean;
        pairing_code?: string;
    }>({ connected: false, daemon_online: false });

    const [receiverStatus, setReceiverStatus] = useState<main.FeishuReceiverStatus>(
        {
            configured: false,
            started: false,
            connected: false,
            status: "not_configured",
        },
    );
    // Debounced receiver status: only show non-connected banners after a short
    // delay so that transient status flickers (e.g. during restart) don't cause
    // the "Credentials saved" banner to flash.
    const [debouncedReceiverStatus, setDebouncedReceiverStatus] = useState(receiverStatus);
    useEffect(() => {
        if (receiverStatus.status === "connected") {
            // Connected — update immediately so the banner disappears fast.
            setDebouncedReceiverStatus(receiverStatus);
            return;
        }
        // Non-connected — delay so brief flickers are hidden.
        const timer = window.setTimeout(() => {
            setDebouncedReceiverStatus(receiverStatus);
        }, 5000);
        return () => window.clearTimeout(timer);
    }, [receiverStatus.status]);
    const [loading, setLoading] = useState(true);
    // Report only once the first status check is back, so the page never
    // treats the placeholder state as "not set up".
    useEffect(() => {
        if (loading) return;
        onStatusChange({ configured: status.connected, online: status.connected && status.daemon_online });
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [loading, status.connected, status.daemon_online]);
    const [polling, setPolling] = useState(false);
    const [permissionRefreshing, setPermissionRefreshing] = useState(false);
    const [permissionReady, setPermissionReady] = useState(false);
    const [registration, setRegistration] = useState<FeishuRegistration | null>(null);
    const [archivedApp, setArchivedApp] = useState<FeishuArchivedApp | null>(null);
    const [reuseLoading, setReuseLoading] = useState(false);
    const [forceNewApp, setForceNewApp] = useState(false);
    const fetchingRef = useRef(false);
    // Bumped on explicit connect/disconnect actions so an in-flight status poll
    // started before the action can't overwrite the new state when it resolves.
    const statusEpochRef = useRef(0);
    const daemonPollIntervalRef = useRef<ReturnType<typeof setInterval> | null>(
        null,
    );
    // The bot name is backfilled server-side on first GetFeishuStatus, so an
    // already-connected account often reports it empty on the very first fetch.
    // Re-poll a few times so the card resolves the bot label without a reload;
    // capped so a permanently-unavailable name can't loop forever.
    const botNameRetriesRef = useRef(0);

    const fetchStatus = useCallback(async () => {
        if (fetchingRef.current) return null;
        fetchingRef.current = true;
        const epoch = statusEpochRef.current;
        try {
            const [s, receiver] = await Promise.all([
                GetFeishuStatus(brand),
                GetFeishuReceiverStatus(brand),
            ]);
            // A connect/disconnect happened while this poll was in flight — drop
            // its (now stale) result so it can't resurrect the old state.
            if (epoch !== statusEpochRef.current) {
                return null;
            }
            setStatus(s);
            if (s.permission_ready || s.feishu_user_name) {
                setPermissionReady(true);
            }
            setReceiverStatus(receiver);
            if (s.connected && !s.bot_name && botNameRetriesRef.current < 3) {
                botNameRetriesRef.current += 1;
                window.setTimeout(() => { void fetchStatus(); }, 1500);
            } else if (s.bot_name) {
                botNameRetriesRef.current = 0;
            }
            return { status: s };
        } catch {
            return null;
        } finally {
            setLoading(false);
            fetchingRef.current = false;
        }
    }, []);

    useEffect(() => {
        fetchStatus();
    }, [fetchStatus]);

    // Load the reusable archived app once on mount so the reuse card is correct
    // after re-opening the page. GetArchivedFeishuApp returns null while an app is
    // connected, so this is a no-op in the connected case.
    useEffect(() => {
        let cancelled = false;
        GetArchivedFeishuApp(brand)
            .then((app) => {
                if (!cancelled) setArchivedApp(app);
            })
            .catch(() => {
                if (!cancelled) setArchivedApp(null);
            });
        return () => {
            cancelled = true;
        };
    }, []);

    useEffect(() => {
        if (!status.connected) return;
        // The connection's status while the page is looked at; paused while
        // the window is hidden or the Mac locked.
        return activeInterval(() => { fetchStatus(); }, 3000);
    }, [status.connected, fetchStatus]);

    useEffect(() => {
        return () => {
            if (daemonPollIntervalRef.current) {
                clearInterval(daemonPollIntervalRef.current);
                daemonPollIntervalRef.current = null;
            }
        };
    }, []);

    useEffect(() => {
        if (!polling) return;
        const interval = setInterval(async () => {
            const result = await fetchStatus();
            if (result?.status.connected) {
                setPolling(false);
                notifyImportant(tb('connectors.feishu.notify.connectedTitle'), tb('connectors.feishu.notify.connectedBody'), "success");
                StartFeishuDaemon(brand)
                    .then(() => {
                        if (daemonPollIntervalRef.current) {
                            clearInterval(daemonPollIntervalRef.current);
                            daemonPollIntervalRef.current = null;
                        }
                        let attempts = 0;
                        daemonPollIntervalRef.current = setInterval(
                            async () => {
                                attempts++;
                                const next = await fetchStatus();
                                if (
                                    next?.status.daemon_online ||
                                    attempts >= 5
                                ) {
                                    if (daemonPollIntervalRef.current) {
                                        clearInterval(
                                            daemonPollIntervalRef.current,
                                        );
                                        daemonPollIntervalRef.current = null;
                                    }
                                }
                            },
                            2000,
                        );
                    })
                    .catch((err) => {
                        console.error("[feishu] daemon start failed:", err);
                        notifyImportant(tb('connectors.feishu.notify.daemonFailedTitle'), tb('connectors.feishu.notify.daemonFailedBody'), "error");
                    });
            }
        }, 2000);
        return () => clearInterval(interval);
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [polling, fetchStatus]);

    const pollRegistration = useCallback(
        (sessionID: string, intervalSec: number) => {
            const tick = async () => {
                try {
                    const result = await PollFeishuRegistration(sessionID);
                    if (result === "connected") {
                        setRegistration(null);
                        // Reuse the existing fetchStatus → StartFeishuDaemon effect.
                        setPolling(true);
                        return;
                    }
                    if (result === "expired" || result === "error") {
                        setRegistration(null);
                        setPolling(false);
                        toast.error(t("connectors.feishu.deviceAuth.failed"));
                        return;
                    }
                } catch (err) {
                    console.error("[feishu] registration poll error:", err);
                }
                window.setTimeout(tick, Math.max(2, intervalSec) * 1000);
            };
            window.setTimeout(tick, Math.max(2, intervalSec) * 1000);
        },
        [t],
    );

    const startConnectFlow = async () => {
        statusEpochRef.current += 1;
        setPolling(true);
        try {
            const existing = await fetchStatus();
            if (existing?.status.connected) {
                setRegistration(null);
                await StartFeishuDaemon(brand);
                await fetchStatus();
                setPolling(false);
                return;
            }

            const reg = await StartFeishuRegistration(brand);
            setRegistration(reg);
            pollRegistration(reg.session_id, reg.interval || 5);
        } catch (err) {
            console.error(
                "[feishu] registration failed:",
                err,
            );
            setRegistration(null);
            setPolling(false);
            toast.error(t("connectors.feishu.deviceAuth.startFailedWithError", { error: String(err) }));
        }
    };

    const handleConnect = () => {
        startConnectFlow();
    };

    const handleReuseApp = async () => {
        setReuseLoading(true);
        statusEpochRef.current += 1;
        try {
            await ReuseArchivedFeishuApp(brand);
            setArchivedApp(null);
            await fetchStatus();
            toast.success(tb('connectors.feishu.reuse.successToast'));
        } catch (err) {
            // The archived app is gone (e.g. deleted in Feishu) — fall back to
            // creating a new one.
            setForceNewApp(true);
            toast.error(tb('connectors.feishu.reuse.failedToast', { error: String(err) }));
        } finally {
            setReuseLoading(false);
        }
    };

    const handleCreateNewApp = () => {
        setForceNewApp(true);
        startConnectFlow();
    };

    const handleDisconnect = async () => {
        // Invalidate any in-flight status poll BEFORE awaiting the backend so a
        // poll that already read connected=true can't write it back.
        statusEpochRef.current += 1;
        if (daemonPollIntervalRef.current) {
            clearInterval(daemonPollIntervalRef.current);
            daemonPollIntervalRef.current = null;
        }
        setRegistration(null);
        setPolling(false);
        setForceNewApp(false);
        setPermissionReady(false);
        // Optimistically switch to the disconnected/reuse view in the same render,
        // built from the account we're disconnecting, so there's no flash of the
        // connected card and no dependency on a backend round-trip. The app being
        // disconnected is exactly the one that gets archived, so this preview is
        // accurate; the mount loader reconciles it on the next page open.
        setArchivedApp({
            app_id: status.app_id ?? "",
            feishu_user_name: status.feishu_user_name,
            feishu_account_id: status.feishu_account_id,
        });
        setStatus({ connected: false, daemon_online: false });
        try {
            await DisconnectFeishu(brand);
            toast.success(tb('connectors.feishu.disconnectedToast'));
        } catch (err) {
            // Disconnect failed — re-sync the real state.
            await fetchStatus();
            toast.error(tb('connectors.feishu.errors.disconnectFailed', { error: String(err) }));
        }
    };

    const handleRetry = async () => {
        try {
            await RestartFeishuReceiver(brand);
            await fetchStatus();
            toast.success(tb('connectors.feishu.receiverRestartingToast'));
        } catch (err) {
            toast.error(tb('connectors.feishu.errors.restartReceiverFailed', { error: String(err) }));
        }
    };

    const handleRefreshPermissions = async () => {
        setPermissionRefreshing(true);
        try {
            const next = await withFeishuTimeout(
                RefreshFeishuPermissions(brand),
                12000,
                tb('connectors.feishu.account.permissionCheckTimedOut'),
            );
            setStatus(next);
            await fetchStatus();
            const check = next.full_permissions;
            if (check?.ready) {
                setPermissionReady(true);
                toast.success(tb('connectors.feishu.account.permissionReadyToast'));
                return;
            }
            toast.info(tb('connectors.feishu.account.permissionMissingToast', {
                count: check?.missing?.length ?? 0,
                required: check?.required_count ?? 0,
                granted: check?.granted_count ?? 0,
            }));
        } catch (err) {
            toast.error(tb('connectors.feishu.account.permissionRefreshFailedToast', { error: String(err) }));
        } finally {
            setPermissionRefreshing(false);
        }
    };

    if (loading) {
        return (
            <SettingsCard title={brandTitle}>
                <p className="text-sm text-muted">{t('connectors.common.loading')}</p>
            </SettingsCard>
        );
    }

    const receiverStatusLabel = tb(`connectors.feishu.receiverStatus.${receiverStatus.status}` as never) as string;

    // Offer reusing the previously connected app instead of scanning to create a
    // new one, unless the user opted to create new or a scan is already in flight.
    const showReuseCard = !!archivedApp && !forceNewApp && !registration && !polling;

    return (
        <>
            <SettingsCard
                title={status.connected ? brandTitle : tb('connectors.feishu.quickStart.title')}
                description={status.connected ? undefined : tb('connectors.feishu.quickStart.description')}
                headerRight={status.connected ? (
                    <div className="flex items-center gap-1.5">
                        <span className={`inline-block h-2 w-2 rounded-full ${status.daemon_online ? "bg-success" : "bg-warning"}`} />
                        <span className="text-xs text-muted">
                            {status.daemon_online ? t('connectors.common.online') : t('connectors.common.offline')}
                        </span>
                    </div>
                ) : undefined}
            >
                {status.connected ? (
                    <div className="flex flex-col gap-3">
                        <div className="flex flex-col gap-4">
                            <div className="flex items-center gap-3">
                                <Avatar
                                    fallback={status.bot_name || tb('connectors.feishu.account.botNameUnavailable')}
                                    size="md"
                                />
                                <span className="min-w-0 truncate text-sm font-medium text-ink">
                                    {status.bot_name || tb('connectors.feishu.account.botNameUnavailable')}
                                </span>
                            </div>
                            {!permissionReady && !isLark && (
                                <p className="text-xs text-muted">
                                    {tb('connectors.feishu.account.fullAccessPointer')}
                                </p>
                            )}
                        </div>
                        <PairingCodeBlock
                            pairingCode={status.pairing_code}
                            title={t('connectors.common.pairing.title')}
                            hint={t('connectors.feishu.pairHint')}
                            idleNote={status.paired ? t('connectors.common.pairing.paired') : null}
                        />
                        {debouncedReceiverStatus.status !== "connected" && (
                            <div
                                className={`rounded-md border px-3 py-2 text-xs ${
                                    debouncedReceiverStatus.status === "error"
                                        ? "border-error-border bg-error-bg text-error"
                                        : "border-border bg-background text-muted"
                                }`}
                            >
                                <div className="flex items-center gap-2">
                                    <span
                                        className={`inline-block h-2 w-2 rounded-full ${
                                            debouncedReceiverStatus.status === "error"
                                                ? "bg-error"
                                                : "bg-warning"
                                        }`}
                                    />
                                    <span className="font-medium text-ink">
                                        {receiverStatusLabel}
                                    </span>
                                    {(debouncedReceiverStatus.status === "error" || debouncedReceiverStatus.status === "saved") && (
                                        <ReceiverErrorHint />
                                    )}
                                </div>
                            </div>
                        )}
                        <div className="flex items-center gap-2">
                            <DisconnectButton appName={brandTitle} onConfirm={handleDisconnect} />
                            {(receiverStatus.status === "error" || receiverStatus.status === "saved") && (
                                <Button variant="outline" size="sm" onClick={handleRetry}>
                                    <RefreshCw className="mr-1.5 h-3.5 w-3.5" />
                                    {t('connectors.common.retry')}
                                </Button>
                            )}
                        </div>
                    </div>
                ) : (
                    <div className="flex flex-col gap-3">
                        {showReuseCard && archivedApp ? (
                            <div className="flex flex-col gap-3 rounded-md border border-border bg-background p-4">
                                <div className="flex flex-col gap-1">
                                    <p className="text-sm font-medium text-ink">
                                        {tb('connectors.feishu.reuse.title')}
                                    </p>
                                    <p className="text-xs text-muted">
                                        {tb('connectors.feishu.reuse.description')}
                                    </p>
                                </div>
                                <div className="flex items-center gap-3 rounded-md border border-border bg-panel/30 px-3 py-2">
                                    <Avatar
                                        fallback={archivedApp.feishu_user_name || tb('connectors.feishu.account.nameUnavailable')}
                                        size="sm"
                                    />
                                    <div className="flex min-w-0 flex-col">
                                        <span className="truncate text-sm font-medium text-ink">
                                            {archivedApp.feishu_user_name || tb('connectors.feishu.account.nameUnavailable')}
                                        </span>
                                        {archivedApp.feishu_account_id && (
                                            <span className="truncate text-xs text-muted">
                                                {tb('connectors.feishu.account.id', { id: archivedApp.feishu_account_id })}
                                            </span>
                                        )}
                                    </div>
                                </div>
                                <div className="flex items-center gap-2">
                                    <Button
                                        variant="connect"
                                        size="sm"
                                        className="w-fit"
                                        disabled={reuseLoading}
                                        onClick={handleReuseApp}
                                    >
                                        {reuseLoading ? (
                                            <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                                        ) : (
                                            <FeishuIcon className="mr-1.5 h-3.5 w-3.5" />
                                        )}
                                        {tb('connectors.feishu.reuse.reuseButton')}
                                    </Button>
                                    <Button
                                        variant="outline"
                                        size="sm"
                                        className="w-fit"
                                        disabled={reuseLoading}
                                        onClick={handleCreateNewApp}
                                    >
                                        {tb('connectors.feishu.reuse.newButton')}
                                    </Button>
                                </div>
                            </div>
                        ) : (
                          <>
                        {registration && (
                            <div className="flex flex-col items-center gap-3 rounded-md border border-border bg-background p-5">
                                <p className="text-sm font-medium text-ink">
                                    {tb('connectors.feishu.deviceAuth.scanTitle')}
                                </p>
                                <div className="rounded-md bg-panel p-3">
                                    <QRCodeSVG
                                        value={registration.verification_uri_complete}
                                        size={200}
                                    />
                                </div>
                                <p className="max-w-xs text-center text-xs text-muted">
                                    {tb('connectors.feishu.deviceAuth.scanHint')}
                                </p>
                                {registration.user_code && (
                                    <p className="font-mono text-sm tracking-widest text-ink">
                                        {registration.user_code}
                                    </p>
                                )}
                            </div>
                        )}
                        <div className="rounded-md border border-border bg-background px-3 py-3 text-xs text-muted">
                            <p className="font-medium text-ink">
                                {tb('connectors.feishu.quickStart.whatHappensTitle')}
                            </p>
                            <ol className="mt-2 list-decimal space-y-1.5 pl-5">
                                <li>{tb('connectors.feishu.quickStart.whatHappens1')}</li>
                                <li>{tb('connectors.feishu.quickStart.whatHappens2')}</li>
                                <li>{tb('connectors.feishu.quickStart.whatHappens3')}</li>
                            </ol>
                            <p className="mt-3 border-t border-border pt-2 text-xs text-muted">
                                {tb('connectors.feishu.quickStart.credentialNote')}
                            </p>
                        </div>
                        {polling ? (
                            <div className="flex items-center gap-2">
                                <Button disabled size="sm" className="w-fit">
                                    <RefreshCw className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                                    {tb('connectors.feishu.waitingForFeishu')}
                                </Button>
                                <Button
                                    variant="ghost"
                                    size="sm"
                                    className="w-fit"
                                    onClick={() => {
                                        setPolling(false);
                                        setRegistration(null);
                                        // Return to the reuse choice (if an archived
                                        // app exists) rather than the bare connect view.
                                        setForceNewApp(false);
                                    }}
                                >
                                    {t('connectors.common.cancel')}
                                </Button>
                            </div>
                        ) : (
                            <Button
                                variant="connect"
                                size="sm"
                                onClick={handleConnect}
                                className="w-fit"
                            >
                                <FeishuIcon className="mr-1.5 h-3.5 w-3.5" />
                                {tb('connectors.feishu.connectButton')}
                            </Button>
                        )}
                          </>
                        )}
                    </div>
                )}
            </SettingsCard>
            {status.connected && (
                <SettingsAdvanced
                    pageId="chat-apps"
                    hint={isLark ? tb('connectors.feishu.advancedHintLark') : tb('connectors.feishu.advancedHint')}
                >
                    <SettingsCard
                        title={tb('connectors.feishu.appCard.title')}
                        description={isLark
                            ? tb('connectors.feishu.appCard.descriptionLark')
                            : tb('connectors.feishu.appCard.description')}
                    >
                        {status.app_id && (
                            <SettingsRow
                                title={tb('connectors.feishu.appCard.appIdLabel')}
                                control={
                                    <span className="font-mono text-xs text-muted select-text">{status.app_id}</span>
                                }
                            />
                        )}
                        {!isLark && (
                            permissionReady ? (
                                <SettingsRow
                                    title={tb('connectors.feishu.appCard.fullAccessLabel')}
                                    control={
                                        <span className="flex items-center gap-1.5 text-xs text-ink">
                                            <Check className="h-3.5 w-3.5 text-success" />
                                            {tb('connectors.feishu.appCard.fullAccessOn')}
                                        </span>
                                    }
                                />
                            ) : (
                                <div className="mt-3 flex flex-col gap-2 rounded-md border border-border bg-background/50 p-3 text-xs text-muted">
                                    <p className="text-sm font-medium text-ink">
                                        {tb('connectors.feishu.account.fullAccessTitle')}
                                    </p>
                                    <p>{tb('connectors.feishu.account.currentAccess')}</p>
                                    <p>{tb('connectors.feishu.account.fullAccessBody')}</p>
                                    {!status.feishu_user_name && (
                                        <p>{tb('connectors.feishu.account.profileNameIncluded')}</p>
                                    )}
                                    <p>{tb('connectors.feishu.account.permissionHowTo')}</p>
                                    <div className="flex flex-wrap items-center gap-2 pt-1">
                                        <Button
                                            type="button"
                                            variant="outline"
                                            size="sm"
                                            className="h-7 px-2.5 text-xs"
                                            onClick={() => BrowserOpenURL(feishuPermissionURL(status.app_id))}
                                        >
                                            <Globe className="mr-1.5 h-3.5 w-3.5" />
                                            {tb('connectors.feishu.account.openPermissionPage')}
                                        </Button>
                                        <CopyButton
                                            text={FEISHU_FULL_PERMISSION_IMPORT}
                                            label={tb('connectors.feishu.account.copyFullPermissionJson')}
                                            copiedLabel={tb('connectors.feishu.account.copiedFullPermissionJson')}
                                            className="rounded-md border border-border px-2.5 py-1.5 text-xs font-medium text-ink hover:border-accent hover:text-accent"
                                            iconClassName="h-3.5 w-3.5"
                                        />
                                        <Button
                                            type="button"
                                            variant="outline"
                                            size="sm"
                                            className="h-7 px-2.5 text-xs"
                                            disabled={permissionRefreshing}
                                            onClick={handleRefreshPermissions}
                                        >
                                            {permissionRefreshing ? (
                                                <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                                            ) : (
                                                <RefreshCw className="mr-1.5 h-3.5 w-3.5" />
                                            )}
                                            {tb('connectors.feishu.account.refreshPermissionStatus')}
                                        </Button>
                                    </div>
                                </div>
                            )
                        )}
                    </SettingsCard>
                    <LarkUserIdentityPanel brand={brand} brandTitle={brandTitle} />
                    {!isLark && <FeishuChatsPanel daemonOnline={status.daemon_online} />}
                </SettingsAdvanced>
            )}
        </>
    );
}

// ── Lark user-identity (personal data) panel ──
//
// lark-cli runs in application (bot) identity by default; reading the user's
// personal data (their groups, calendar, mailbox, private docs) needs a
// user-identity login (lark-cli auth login, a device flow). This panel surfaces
// that upgrade for the connected brand.
function LarkUserIdentityPanel({ brand, brandTitle }: { brand: "feishu" | "lark"; brandTitle: string }) {
    const { t } = useTranslation('settings');
    const tb = (key: string, opts?: Record<string, unknown>): string =>
        String(t(key as never, { brand: brandTitle, ...(opts ?? {}) } as never));
    const [authed, setAuthed] = useState(false);
    const [scopesReady, setScopesReady] = useState(false);
    const [available, setAvailable] = useState(false);
    const [login, setLogin] = useState<{ verification_url: string; user_code: string; device_code: string } | null>(null);
    const [busy, setBusy] = useState(false);
    const pollRef = useRef(false);

    const refresh = useCallback(async () => {
        try {
            const s = await GetLarkAccountStatus(brand);
            setAvailable(!!s?.configured && !!s?.runnable);
            setAuthed(!!s?.user_authed);
            setScopesReady(!!s?.scopes_ready);
        } catch {
            setAvailable(false);
        }
    }, [brand]);

    useEffect(() => {
        refresh();
    }, [refresh]);

    const startLogin = async () => {
        setBusy(true);
        try {
            const payload = await LarkCliUserLoginStart(brand);
            setLogin(payload);
            pollRef.current = true;
            const tick = async () => {
                if (!pollRef.current) return;
                try {
                    const result = await LarkCliUserLoginPoll(brand, payload.device_code);
                    if (result === "connected") {
                        pollRef.current = false;
                        setLogin(null);
                        await refresh();
                        toast.success(tb('connectors.feishu.userIdentity.successToast'));
                        return;
                    }
                    if (result === "expired" || result === "error") {
                        pollRef.current = false;
                        setLogin(null);
                        toast.error(tb('connectors.feishu.userIdentity.failedToast'));
                        return;
                    }
                } catch {
                    // transient; keep polling
                }
                window.setTimeout(tick, 1500);
            };
            window.setTimeout(tick, 1500);
        } catch (err) {
            toast.error(tb('connectors.feishu.userIdentity.failedWithErrorToast', { error: String(err) }));
        } finally {
            setBusy(false);
        }
    };

    const cancelLogin = () => {
        pollRef.current = false;
        setLogin(null);
    };

    if (!available) return null;

    return (
        <SettingsCard title={tb('connectors.feishu.userIdentity.title')}>
            {authed ? (
                <div className="flex flex-col gap-1.5">
                    <div className="flex items-center gap-2 text-sm text-ink">
                        <Check className="h-4 w-4 text-success" />
                        {tb('connectors.feishu.userIdentity.authedLabel')}
                    </div>
                    <p className="text-xs text-muted">
                        {tb(
                            scopesReady
                                ? 'connectors.feishu.userIdentity.authedDescription'
                                : 'connectors.feishu.userIdentity.authedDescriptionScopesPending',
                        )}
                    </p>
                </div>
            ) : login ? (
                <div className="flex flex-col items-center gap-3 rounded-md border border-border bg-background p-5">
                    <p className="text-sm font-medium text-ink">
                        {tb('connectors.feishu.userIdentity.scanTitle')}
                    </p>
                    <div className="rounded-md bg-panel p-3">
                        <QRCodeSVG value={login.verification_url} size={200} />
                    </div>
                    <p className="max-w-xs text-center text-xs text-muted">
                        {tb('connectors.feishu.userIdentity.scanHint')}
                    </p>
                    {login.user_code && (
                        <p className="font-mono text-sm tracking-widest text-ink">{login.user_code}</p>
                    )}
                    <div className="flex items-center gap-2">
                        <Button disabled size="sm" className="w-fit">
                            <RefreshCw className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                            {tb('connectors.feishu.userIdentity.waiting')}
                        </Button>
                        <Button variant="ghost" size="sm" className="w-fit" onClick={cancelLogin}>
                            {t('connectors.common.cancel')}
                        </Button>
                    </div>
                </div>
            ) : (
                <div className="flex flex-col gap-2">
                    <p className="text-xs text-muted">
                        {tb('connectors.feishu.userIdentity.description')}
                    </p>
                    <Button variant="connect" size="sm" className="w-fit" disabled={busy} onClick={startLogin}>
                        {busy ? (
                            <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />
                        ) : (
                            <FeishuIcon className="mr-1.5 h-3.5 w-3.5" />
                        )}
                        {tb('connectors.feishu.userIdentity.authorizeButton')}
                    </Button>
                </div>
            )}
        </SettingsCard>
    );
}

// ── Feishu chats panel ──

function FeishuChatsPanel({ daemonOnline }: { daemonOnline: boolean }) {
    const { t } = useTranslation('settings');
    const [chats, setChats] = useState<
        { chat_id: string; name: string; chat_type: string }[]
    >([]);
    const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState("");
    const [warning, setWarning] = useState("");
    const isFetchingRef = useRef(false);
    const filterLoadedRef = useRef(false);
    const prevDaemonOnlineRef = useRef(daemonOnline);

    const fetchChats = useCallback(async () => {
        if (isFetchingRef.current) return;
        isFetchingRef.current = true;
        try {
            const result = await GetFeishuChats();
            if (result.error) {
                setError(result.error);
                setChats([]);
            } else {
                setError("");
                setChats(result.chats ?? []);
            }
            setWarning(result.warning ?? "");
        } catch {
            setError(t('connectors.channels.loadChatsFailed'));
        } finally {
            setLoading(false);
            isFetchingRef.current = false;
        }
    }, [t]);

    // Sentinel value: when the user explicitly selects "None", we persist
    // this marker so the backend sees len(filter)>0 and the allow-list
    // matches no real chat ID, effectively scanning nothing.
    // nil/empty in config means "no filter saved yet = scan all".
    const NONE_SENTINEL = "__none__";

    // Load saved chat filter once chats are available.
    useEffect(() => {
        if (chats.length === 0 || filterLoadedRef.current) return;
        filterLoadedRef.current = true;

        GetFeishuScanChatFilter()
            .then((saved) => {
                if (saved && saved.length > 0) {
                    if (saved.length === 1 && saved[0] === NONE_SENTINEL) {
                        // Explicit "scan none" selection.
                        setSelectedIds(new Set());
                    } else {
                        const validIds = new Set(chats.map((ch) => ch.chat_id));
                        setSelectedIds(new Set(
                            saved.filter((id) => validIds.has(id)),
                        ));
                    }
                } else {
                    // No filter saved yet -- default to all chats.
                    setSelectedIds(new Set(chats.map((ch) => ch.chat_id)));
                }
            })
            .catch(() => {
                setSelectedIds(new Set(chats.map((ch) => ch.chat_id)));
            });
    }, [chats]);

    const saveFilter = useCallback(
        (ids: Set<string>) => {
            // When nothing is selected, persist the sentinel so the backend
            // distinguishes "scan none" from "no filter = scan all".
            const toSave = ids.size === 0 ? [NONE_SENTINEL] : Array.from(ids);
            SetFeishuScanChatFilter(toSave).catch((err) => {
                console.error("[feishu] failed to save chat filter:", err);
            });
        },
        [],
    );

    const toggleChat = useCallback(
        (id: string, checked: boolean) => {
            setSelectedIds((prev) => {
                const next = new Set(prev);
                if (checked) {
                    next.add(id);
                } else {
                    next.delete(id);
                }
                saveFilter(next);
                return next;
            });
        },
        [saveFilter],
    );

    const selectAll = useCallback(() => {
        const allIds = new Set(chats.map((ch) => ch.chat_id));
        setSelectedIds(allIds);
        saveFilter(allIds);
    }, [chats, saveFilter]);

    const selectNone = useCallback(() => {
        const empty = new Set<string>();
        setSelectedIds(empty);
        saveFilter(empty);
    }, [saveFilter]);

    useEffect(() => {
        fetchChats();
    }, [fetchChats]);

    // Re-fetch chats when daemon transitions to online.
    useEffect(() => {
        if (daemonOnline && !prevDaemonOnlineRef.current) {
            filterLoadedRef.current = false;
            fetchChats();
        }
        prevDaemonOnlineRef.current = daemonOnline;
    }, [daemonOnline, fetchChats]);

    const selectedCount = selectedIds.size;

    const chatTypeLabel = (chatType: string): string | null => {
        switch (chatType) {
            case "group": return t('connectors.channels.chatTypeGroup');
            case "p2p": return t('connectors.channels.chatTypeDM');
            default: return null;
        }
    };

    const renderChatRow = (ch: (typeof chats)[0]) => {
        const isSelected = selectedIds.has(ch.chat_id);
        return (
            <label
                key={ch.chat_id}
                className={`flex items-center gap-2 text-sm rounded px-1.5 py-1 transition-colors cursor-pointer ${
                    isSelected
                        ? "text-ink"
                        : "text-muted hover:bg-background-accent/30"
                }`}
            >
                <Checkbox
                    checked={isSelected}
                    onCheckedChange={(checked) =>
                        toggleChat(ch.chat_id, !!checked)
                    }
                    className="h-3.5 w-3.5"
                />
                <span className="truncate">{ch.name || ch.chat_id}</span>
                {chatTypeLabel(ch.chat_type) && (
                    <span className="text-xs text-muted ml-auto shrink-0">
                        {chatTypeLabel(ch.chat_type)}
                    </span>
                )}
            </label>
        );
    };

    return (
        <SettingsCard
            title={t('connectors.feishu.chats.title')}
            description={t('connectors.feishu.chats.description')}
            headerRight={!loading && !error && chats.length > 0 ? (
                <span className="text-xs text-muted">
                    {t('connectors.feishu.chats.selectedCount', { count: selectedCount, total: chats.length })}
                </span>
            ) : undefined}
        >
            {warning && (
                <p className="text-xs text-warning bg-warning-bg border border-warning-border rounded px-2.5 py-1.5 mb-2">
                    {warning}
                </p>
            )}
            {loading ? (
                <p className="text-sm text-muted">{t('connectors.channels.loadingChats')}</p>
            ) : error ? (
                <p className="text-sm text-error">{error}</p>
            ) : chats.length === 0 ? (
                <p className="text-sm text-muted">
                    {t('connectors.channels.noChatsYetFeishu')}
                </p>
            ) : (
                <div className="flex flex-col gap-1">
                    <div className="flex items-center justify-between gap-3 px-1.5 mb-0.5">
                        <p className="text-[11px] font-medium text-muted uppercase tracking-wide">
                            {t('connectors.channels.chatsBotJoined', { count: chats.length })}
                        </p>
                        <div className="flex gap-1">
                            <Button
                                type="button"
                                size="sm"
                                variant="ghost"
                                onClick={selectAll}
                                className="h-6 px-2 text-[11px]"
                            >
                                {t('connectors.channels.all')}
                            </Button>
                            <Button
                                type="button"
                                size="sm"
                                variant="ghost"
                                onClick={selectNone}
                                className="h-6 px-2 text-[11px]"
                            >
                                {t('connectors.channels.none')}
                            </Button>
                        </div>
                    </div>
                    {chats.map(renderChatRow)}
                </div>
            )}
        </SettingsCard>
    );
}
