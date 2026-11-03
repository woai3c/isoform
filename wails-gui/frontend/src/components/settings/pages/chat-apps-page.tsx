import { useCallback, useEffect, useState, type ComponentType } from "react";
import { useTranslation } from "react-i18next";
import { ChevronLeft } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
    GetDiscordStatus,
    GetFeishuStatus,
    GetSlackStatus,
    GetTelegramStatus,
    GetWeChatStatus,
} from "../../../../wailsjs/go/main/App";
import {
    DiscordIcon,
    FeishuIcon,
    SlackIcon,
    TelegramIcon,
    WeChatIcon,
} from "@/components/icons/app-icons";
import { SettingsCard } from "@/components/settings/settings-primitives";
import type { ChatAppId } from "@/components/settings/settings-registry";
import {
    DiscordStandaloneSetup,
    SlackStandaloneSetup,
    TelegramStandaloneSetup,
} from "./chat-apps/bot-setups";
import type { ChatAppStatus } from "./chat-apps/chat-app-status";
import { FeishuSettings } from "./chat-apps/feishu-setup";
import { WeChatSettings } from "./chat-apps/wechat-setup";

// ── Chat apps page ──
//
// One list of the chat apps people can message Isoform from. Each row shows
// whether the app is set up and opens that app's existing setup flow as a
// drill-in with a back link. Feishu and Lark share a row: the drill-in starts
// with a Feishu | Lark choice and keeps each brand's state separate. The ids
// slack, discord, telegram, wechat, feishu and lark deep-link straight to the
// drill-in (see resolveSettingsTopic). Until an app is set up, its setup
// opens with the same short "Before you connect" note, so people know what a
// message to Isoform does before they connect it. Each setup keeps its own
// technical parts (setup steps, app ids, whether the receiver is online)
// under the page's Advanced section.

type ChatAppRowId = Exclude<ChatAppId, "lark">;

const CHAT_APP_ROWS: { id: ChatAppRowId; icon: ComponentType<{ className?: string }> }[] = [
    { id: "slack", icon: SlackIcon },
    { id: "discord", icon: DiscordIcon },
    { id: "telegram", icon: TelegramIcon },
    { id: "wechat", icon: WeChatIcon },
    { id: "feishu", icon: FeishuIcon },
];

const FEISHU_BRANDS = ["feishu", "lark"] as const;

function loadChatAppStatuses(): Promise<Partial<Record<ChatAppId, ChatAppStatus>>> {
    return Promise.allSettled([
        GetSlackStatus().then((s) => ["slack", { configured: s.connected, online: s.connected && s.daemon_online }] as const),
        GetDiscordStatus().then((s) => ["discord", { configured: s.bound, online: s.bound && s.daemon_online }] as const),
        GetTelegramStatus().then((s) => ["telegram", { configured: s.connected, online: s.connected && s.daemon_online }] as const),
        GetWeChatStatus().then((s) => ["wechat", { configured: s.connected, online: s.connected && s.online }] as const),
        GetFeishuStatus("feishu").then((s) => ["feishu", { configured: s.connected, online: s.connected && s.daemon_online }] as const),
        GetFeishuStatus("lark").then((s) => ["lark", { configured: s.connected, online: s.connected && s.daemon_online }] as const),
    ]).then((results) => {
        const statuses: Partial<Record<ChatAppId, ChatAppStatus>> = {};
        for (const result of results) {
            if (result.status === "fulfilled") {
                const [id, status] = result.value;
                statuses[id] = status;
            }
        }
        return statuses;
    });
}

/** The Feishu row counts as set up (or online) when either brand is. */
function rowStatus(
    id: ChatAppRowId,
    statuses: Partial<Record<ChatAppId, ChatAppStatus>>,
): ChatAppStatus | undefined {
    if (id !== "feishu") return statuses[id];
    const feishu = statuses.feishu;
    const lark = statuses.lark;
    if (!feishu && !lark) return undefined;
    return {
        configured: Boolean(feishu?.configured || lark?.configured),
        online: Boolean(feishu?.online || lark?.online),
    };
}

/** What a message to Isoform from a chat app does. Slack adds what happens to
 *  channels it is invited to. */
function BeforeYouConnect({ app }: { app: ChatAppRowId }) {
    const { t } = useTranslation("settings");
    return (
        <div className="rounded-md border border-border bg-background px-3 py-3 text-xs text-muted">
            <p className="font-medium text-ink">{t("chatApps.beforeYouConnect.title")}</p>
            <ul className="mt-2 list-disc space-y-1.5 pl-5 leading-relaxed">
                <li>{t("chatApps.beforeYouConnect.session")}</li>
                <li>{t("chatApps.beforeYouConnect.asksFirst")}</li>
                {app === "slack" && <li>{t("chatApps.beforeYouConnect.slackChannels")}</li>}
            </ul>
        </div>
    );
}

function ChatAppStatusChip({ status }: { status: ChatAppStatus | undefined }) {
    const { t } = useTranslation("settings");
    if (!status) return null;
    if (status.online) {
        return (
            <span className="inline-flex items-center rounded-full bg-success-bg px-2 py-0.5 text-[10px] font-medium text-success">
                {t("chatApps.status.connected")}
            </span>
        );
    }
    return (
        <span className="inline-flex items-center rounded-full bg-ink/[0.05] px-2 py-0.5 text-[10px] font-medium text-ink/55">
            {status.configured ? t("chatApps.status.offline") : t("chatApps.status.notSetUp")}
        </span>
    );
}

export interface ChatAppsPageProps {
    /** The app whose setup is open, or undefined for the list. */
    selectedApp?: ChatAppId;
    onSelectApp: (app: ChatAppId | undefined) => void;
}

export function ChatAppsPage({ selectedApp, onSelectApp }: ChatAppsPageProps) {
    const { t } = useTranslation("settings");
    const [statuses, setStatuses] = useState<Partial<Record<ChatAppId, ChatAppStatus>>>({});
    const [statusesLoaded, setStatusesLoaded] = useState(false);
    const showingList = selectedApp === undefined;

    // Load on every visit, the list and a drill-in alike (each remounts this
    // page), so the list reflects a change made in a setup flow after the back
    // link and a drill-in knows whether its app is already set up.
    useEffect(() => {
        let cancelled = false;
        void loadChatAppStatuses().then((next) => {
            if (cancelled) return;
            setStatuses((prev) => ({ ...prev, ...next }));
            setStatusesLoaded(true);
        });
        return () => {
            cancelled = true;
        };
    }, [showingList]);

    const handleStatusChange = useCallback((id: ChatAppId, next: ChatAppStatus) => {
        setStatuses((prev) => {
            const current = prev[id];
            if (current && current.online === next.online && current.configured === next.configured) {
                return prev;
            }
            return { ...prev, [id]: next };
        });
    }, []);

    // One stable callback for whichever setup is open; it reports both whether
    // the app is set up and whether its receiver is online.
    const handleSetupStatus = useCallback(
        (next: ChatAppStatus) => {
            if (selectedApp !== undefined) handleStatusChange(selectedApp, next);
        },
        [handleStatusChange, selectedApp],
    );

    if (selectedApp !== undefined) {
        const rowId: ChatAppRowId = selectedApp === "lark" ? "feishu" : selectedApp;
        // Wait until the app's status is known, so an app that is already set
        // up never flashes the note.
        const selectedStatus = statuses[selectedApp];
        const showBeforeYouConnect =
            (statusesLoaded || selectedStatus !== undefined) && !selectedStatus?.configured;
        return (
            <>
                <button
                    type="button"
                    onClick={() => onSelectApp(undefined)}
                    className="inline-flex items-center gap-1 text-xs text-muted hover:text-ink transition-colors"
                >
                    <ChevronLeft className="h-3.5 w-3.5" />
                    {t("chatApps.back")}
                </button>
                <p className="settings-card-desc">
                    {t(`chatApps.apps.${rowId}.description`)}
                </p>
                {rowId === "feishu" && (
                    <div className="flex flex-wrap items-center gap-3">
                        <span className="text-xs text-muted">{t("chatApps.brandChoice")}</span>
                        <div
                            className="settings-segment"
                            role="radiogroup"
                            aria-label={t("chatApps.brandChoice")}
                        >
                            {FEISHU_BRANDS.map((brand) => (
                                <button
                                    key={brand}
                                    type="button"
                                    role="radio"
                                    aria-checked={selectedApp === brand}
                                    data-active={selectedApp === brand}
                                    onClick={() => onSelectApp(brand)}
                                >
                                    {t(`chatApps.brands.${brand}`)}
                                </button>
                            ))}
                        </div>
                    </div>
                )}
                {showBeforeYouConnect && <BeforeYouConnect app={rowId} />}
                {selectedApp === "slack" && <SlackStandaloneSetup onStatusChange={handleSetupStatus} />}
                {selectedApp === "discord" && <DiscordStandaloneSetup onStatusChange={handleSetupStatus} />}
                {selectedApp === "telegram" && <TelegramStandaloneSetup onStatusChange={handleSetupStatus} />}
                {selectedApp === "wechat" && <WeChatSettings onStatusChange={handleSetupStatus} />}
                {(selectedApp === "feishu" || selectedApp === "lark") && (
                    <FeishuSettings key={selectedApp} brand={selectedApp} onStatusChange={handleSetupStatus} />
                )}
            </>
        );
    }

    return (
        <SettingsCard title={t("chatApps.list.title")} description={t("chatApps.list.description")}>
            {CHAT_APP_ROWS.map(({ id, icon: Icon }) => {
                const status = rowStatus(id, statuses);
                const configured = Boolean(status?.configured);
                // Open the brand that is already set up; Feishu otherwise.
                const target: ChatAppId =
                    id === "feishu" && !statuses.feishu?.configured && statuses.lark?.configured
                        ? "lark"
                        : id;
                const name = t(`chatApps.apps.${id}.name`);
                return (
                    <div key={id} className="settings-row">
                        <div className="flex items-center gap-3 min-w-0">
                            <Icon
                                className={`h-5 w-5 shrink-0 transition-[filter,opacity] ${
                                    status?.online ? "" : "grayscale opacity-40"
                                }`}
                            />
                            <div className="flex flex-col min-w-0">
                                <span className="settings-row-title truncate">{name}</span>
                                <span className="settings-row-desc">
                                    {t(`chatApps.apps.${id}.description`)}
                                </span>
                            </div>
                        </div>
                        <div className="shrink-0 flex items-center gap-2">
                            <ChatAppStatusChip status={status} />
                            <Button
                                size="sm"
                                variant="outline"
                                aria-label={
                                    configured
                                        ? t("chatApps.manageApp", { app: name })
                                        : t("chatApps.setUpApp", { app: name })
                                }
                                onClick={() => onSelectApp(target)}
                            >
                                {configured ? t("chatApps.manage") : t("chatApps.setUp")}
                            </Button>
                        </div>
                    </div>
                );
            })}
        </SettingsCard>
    );
}
