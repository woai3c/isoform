import { SettingReadError } from "./setting-read-error";
import { invalidateActivitySessionCache } from '@/components/timeline/activity-session-cache';
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { X, CheckCircle2, Plus } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { ConfirmDialog } from "@/components/confirm-dialog";
import {
    AddSensitiveApp,
    AddVoiceInputApp,
    GetAskBeforeRiskyCommands,
    GetSensitiveApps,
    GetVoiceInputApps,
    ListSystemApps,
    RemoveSensitiveApp,
    RemoveVoiceInputApp,
    SetAskBeforeRiskyCommands,
} from "../../../../wailsjs/go/main/App";
import { GetReadAssistantHistory, SetReadAssistantHistory } from "@/lib/wails-shim-app";
import { usePermissions } from "@/hooks/usePermissions";
import { useTelemetryPreference } from "@/hooks/useTelemetryPreference";
import type { PermissionItem, PlatformPermissions } from "@/types";
import { SettingsCard, SettingsRow } from "@/components/settings/settings-primitives";
import { SettingsAdvanced, settingsAnchor } from "@/components/settings/settings-advanced";
import { VoiceMemoryCard } from "@/components/settings/voice-memory-card";
import { formatBytes } from "@/components/settings/settings-format";
import { OnThisMacLines } from "@/components/onboarding/v2/OnThisMac";
import {
    PermissionStatusChip,
    SettingsControlCard,
    SettingsReason,
    openSettingsPage,
    permissionLabel,
    permissionPurpose,
    useListening,
    type OpenSettingsPage,
} from "./capture-shared";
import {
    GIB,
    RETENTION_DAYS,
    formatEarliest,
    retentionLabel,
    shortensRetention,
    useRecordingStorage,
    type RecordingStorage,
} from "./recording-storage";

// ── Privacy page (page id "security") ──
//
// What stays private. It opens with the four lines of setup's "On this computer"
// popover (handoff-2026-10-28-1315-setup-v2.1.0 §1), then three everyday
// cards: the apps Isoform ignores (hidden from screen recordings, and apps
// Isoform does not listen to), how long recordings are kept, and diagnostic
// data. Taking an app off either list asks
// first, because Isoform then records or transcribes it again. The full list
// of computer permissions and the technical detail of diagnostic data sit
// under Advanced. The storage limit lives on the Storage page.

type SystemAppInfo = { name: string; englishName?: string; bundleId: string; icon: string };

function RecordingRetentionCard({ storage }: { storage: RecordingStorage }) {
    const { t } = useTranslation("settings");
    const [confirmDays, setConfirmDays] = useState<number | null>(null);
    const title = t("privacy.retention.title");
    const { stats } = storage;

    const handleSelect = (value: string) => {
        const next = Number(value);
        if (!Number.isFinite(next) || next === storage.days) return;
        // While usage is unknown (still loading, or it failed) a shorter
        // time limit asks first too.
        if (shortensRetention(storage.days, next) && (stats === null || stats.retainedSessionCount > 0)) {
            setConfirmDays(next);
            return;
        }
        storage.saveDays(next);
    };

    // Clamp to 100% so data above a newly lowered limit doesn't read as
    // "120% used" before the next rotation pass catches up.
    const capBytes = storage.maxGB * GIB;
    const usageRatio = stats && capBytes > 0 ? Math.min(1, stats.retainedBytes / capBytes) : 0;
    const earliest = stats ? formatEarliest(stats.earliestRetainedMs, t) : "";

    let summary: ReactNode = null;
    if (stats && stats.retainedSessionCount > 0) {
        summary = (
            <div className="flex flex-col gap-2 pt-1">
                <SettingsReason>
                    {earliest
                        ? t("privacy.retention.summaryWithOldest", {
                              used: formatBytes(stats.retainedBytes),
                              maxGB: storage.maxGB,
                              count: stats.retainedSessionCount,
                              oldest: earliest,
                          })
                        : t("privacy.retention.summary", {
                              used: formatBytes(stats.retainedBytes),
                              maxGB: storage.maxGB,
                              count: stats.retainedSessionCount,
                          })}
                </SettingsReason>
                {/* Subtle by design: the numbers are the source of truth, the
                    bar is a quick "am I close to the limit?" read. */}
                <div className="h-1 w-full rounded-full bg-ink/[0.06] overflow-hidden">
                    <div
                        className="h-full bg-accent-2/70 transition-[width] duration-300"
                        style={{ width: `${Math.round(usageRatio * 100)}%` }}
                    />
                </div>
            </div>
        );
    } else if (stats) {
        summary = <SettingsReason>{t("privacy.retention.empty")}</SettingsReason>;
    }

    const confirmPeriod = confirmDays === null ? "" : retentionLabel(t, confirmDays);

    return (
        <>
            <SettingsControlCard
                title={title}
                description={t("privacy.retention.description")}
                control={
                    storage.loading ? null : (
                        <Select
                            value={String(storage.days)}
                            onValueChange={handleSelect}
                            disabled={storage.saving}
                        >
                            <SelectTrigger aria-label={title} className="w-[140px] h-8 text-xs">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                {RETENTION_DAYS.map((d) => (
                                    <SelectItem key={d} value={String(d)}>
                                        {retentionLabel(t, d)}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    )
                }
            >
                {summary}
            </SettingsControlCard>
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
                    if (confirmDays !== null) storage.saveDays(confirmDays);
                }}
            />
        </>
    );
}

// ── Apps Isoform ignores ──

function AppIcon({ src }: { src?: string }) {
    return src ? (
        <img src={src} alt="" className="h-6 w-6 shrink-0 rounded" />
    ) : (
        <div className="h-6 w-6 shrink-0 rounded bg-ink/[0.06]" />
    );
}

function AppListRow({ icon, name, muted, trailing }: { icon?: string; name: string; muted?: boolean; trailing: ReactNode }) {
    return (
        <div className={`flex items-center justify-between py-2.5 gap-3${muted ? " opacity-60" : ""}`}>
            <div className="flex items-center gap-2.5 min-w-0">
                <AppIcon src={icon} />
                <span className="settings-row-title truncate">{name}</span>
            </div>
            {trailing}
        </div>
    );
}

/** An app waiting for the user to confirm its removal from one of the lists. */
interface PendingRemoval {
    id: string;
    name: string;
}

/** Asks before an app leaves an ignore list: removing it lets Isoform record
 *  or transcribe that app again. */
function ConfirmRemoveAppDialog({
    pending,
    onClose,
    onConfirm,
    copy,
}: {
    pending: PendingRemoval | null;
    onClose: () => void;
    onConfirm: (id: string) => void;
    copy: { title: string; description: string; confirm: string };
}) {
    return (
        <ConfirmDialog
            open={pending !== null}
            onOpenChange={(open) => {
                if (!open) onClose();
            }}
            title={copy.title}
            description={copy.description}
            confirmLabel={copy.confirm}
            variant="danger"
            onConfirm={() => {
                if (pending) onConfirm(pending.id);
            }}
        />
    );
}

function RemoveAppButton({ onClick }: { onClick: () => void }) {
    const { t } = useTranslation("settings");
    const label = t("privacy.ignoredApps.remove");
    return (
        <button
            type="button"
            onClick={onClick}
            className="flex items-center justify-center h-6 w-6 rounded-md text-muted hover:text-ink hover:bg-ink/[0.04] transition-colors shrink-0"
            title={label}
            aria-label={label}
        >
            <X className="h-3.5 w-3.5" />
        </button>
    );
}

function matchesSearch(app: SystemAppInfo, search: string): boolean {
    if (!search) return true;
    const q = search.toLowerCase();
    return (
        app.name.toLowerCase().includes(q) ||
        (app.englishName ?? "").toLowerCase().includes(q) ||
        app.bundleId.toLowerCase().includes(q)
    );
}

/** "Add app" button that turns into a searchable list of installed apps. */
function AppPicker({ candidates, onPick }: { candidates: SystemAppInfo[]; onPick: (app: SystemAppInfo) => void }) {
    const { t } = useTranslation("settings");
    const [open, setOpen] = useState(false);
    const [search, setSearch] = useState("");
    const matches = useMemo(() => candidates.filter((app) => matchesSearch(app, search)), [candidates, search]);

    if (!open) {
        return (
            <Button
                size="sm"
                variant="outline"
                className="self-start"
                onClick={() => {
                    setSearch("");
                    setOpen(true);
                }}
            >
                <Plus className="h-3.5 w-3.5" />
                {t("privacy.ignoredApps.addApp")}
            </Button>
        );
    }

    return (
        <div className="flex flex-col gap-2 pt-1">
            <input
                type="text"
                value={search}
                onChange={(e) => setSearch(e.target.value)}
                placeholder={t("privacy.ignoredApps.searchPlaceholder")}
                aria-label={t("privacy.ignoredApps.searchPlaceholder")}
                autoFocus
                className="h-8 rounded-md border border-border bg-background px-2.5 text-xs outline-none focus:ring-1 focus:ring-ring"
            />
            <div className="flex flex-col max-h-48 overflow-y-auto divide-y divide-border/30 pr-2.5 [scrollbar-gutter:stable]">
                {matches.map((app) => (
                    <button
                        key={app.bundleId}
                        type="button"
                        onClick={() => onPick(app)}
                        className="flex items-center gap-2.5 py-2 px-1 text-left hover:bg-ink/[0.04] rounded transition-colors w-full"
                    >
                        <AppIcon src={app.icon} />
                        <span className="settings-row-title truncate flex-1">{app.name}</span>
                        <Plus className="h-3.5 w-3.5 text-muted shrink-0" />
                    </button>
                ))}
                {matches.length === 0 && (
                    <span className="text-xs text-muted py-2">{t("privacy.ignoredApps.noMatches")}</span>
                )}
            </div>
            <div className="flex justify-end pt-1">
                <Button size="sm" onClick={() => setOpen(false)}>
                    {t("privacy.ignoredApps.done")}
                </Button>
            </div>
        </div>
    );
}

function AppListHeading({ title, description }: { title: string; description: string }) {
    return (
        <div className="flex flex-col">
            <h3 className="settings-row-title">{title}</h3>
            <p className="settings-row-desc">{description}</p>
        </div>
    );
}

// Display names for the built-in password-manager prefixes when the app is
// not found among installed apps.
const DEFAULT_APP_LABELS: Record<string, string> = {
    "com.1password": "1Password",
    "com.agilebits": "1Password",
    "com.bitwarden": "Bitwarden",
    "com.lastpass": "LastPass",
    "com.dashlane": "Dashlane",
    "org.keepassxc": "KeePassXC",
    "com.enpass": "Enpass",
    "com.nordpass": "NordPass",
    "com.roboform": "RoboForm",
    "de.marcels-it-blog.KeePass": "KeePass",
    "com.apple.keychainaccess": "Keychain Access",
    "com.apple.Passwords": "Passwords",
};

interface SensitiveAppEntry {
    bundlePrefix: string;
    category: string;
    isDefault: boolean;
    icon?: string;
}

/** Apps whose windows are blurred in screen recordings. Password managers are
 *  always hidden; the user adds the rest. */
function HiddenAppsSection({ systemApps }: { systemApps: SystemAppInfo[] }) {
    const { t } = useTranslation("settings");
    const [apps, setApps] = useState<SensitiveAppEntry[]>([]);
    const [loading, setLoading] = useState(true);
    const [pendingRemoval, setPendingRemoval] = useState<PendingRemoval | null>(null);

    useEffect(() => {
        GetSensitiveApps()
            .then((list: SensitiveAppEntry[]) => setApps(list ?? []))
            .catch(() => toast.error(t("sensitiveApps.toasts.loadFailed")))
            .finally(() => setLoading(false));
    }, [t]);

    const handleAdd = async (app: SystemAppInfo) => {
        try {
            await AddSensitiveApp(app.bundleId, app.name);
            setApps((prev) => {
                const next: SensitiveAppEntry[] = [
                    ...prev.filter((a) => a.isDefault || a.bundlePrefix !== app.bundleId),
                    { bundlePrefix: app.bundleId, category: app.name, isDefault: false, icon: app.icon },
                ];
                next.sort((a, b) => a.bundlePrefix.localeCompare(b.bundlePrefix));
                return next;
            });
        } catch {
            toast.error(t("sensitiveApps.toasts.addFailed"));
        } finally {
            invalidateActivitySessionCache(true);
        }
    };

    const handleRemove = (bundlePrefix: string) => {
        RemoveSensitiveApp(bundlePrefix)
            .then(() => {
                setApps((prev) => prev.filter((a) => a.isDefault || a.bundlePrefix !== bundlePrefix));
            })
            .catch(() => toast.error(t("sensitiveApps.toasts.removeFailed")))
            .finally(() => invalidateActivitySessionCache(true));
    };

    const userApps = useMemo(() => apps.filter((a) => !a.isDefault), [apps]);
    const systemAppByBundleId = useMemo(
        () =>
            systemApps.reduce<Record<string, SystemAppInfo>>((acc, app) => {
                acc[app.bundleId.toLowerCase()] = app;
                return acc;
            }, {}),
        [systemApps],
    );
    // Only the password managers that are actually installed, once each.
    const installedDefaults = useMemo(() => {
        const seen = new Set<string>();
        return apps
            .filter((a) => a.isDefault)
            .filter((app) => {
                const lower = app.bundlePrefix.toLowerCase();
                const installed = systemApps.find((sa) => sa.bundleId.toLowerCase().startsWith(lower));
                if (!installed || seen.has(installed.name)) return false;
                seen.add(installed.name);
                return true;
            });
    }, [apps, systemApps]);
    const candidates = useMemo(() => {
        const prefixes = apps.map((a) => a.bundlePrefix.toLowerCase());
        return systemApps.filter((sa) => {
            const bid = sa.bundleId.toLowerCase();
            return !prefixes.some((p) => bid.startsWith(p) || p === bid);
        });
    }, [apps, systemApps]);

    const renderRow = (app: SensitiveAppEntry) => {
        const lower = app.bundlePrefix.toLowerCase();
        const installed = app.isDefault
            ? systemApps.find((sa) => sa.bundleId.toLowerCase().startsWith(lower))
            : systemAppByBundleId[lower];
        const name =
            installed?.name ?? (app.isDefault ? DEFAULT_APP_LABELS[app.bundlePrefix] : undefined) ?? app.category;
        return (
            <AppListRow
                key={`${app.isDefault ? "default" : "user"}-${app.bundlePrefix}`}
                icon={installed?.icon ?? app.icon}
                name={name}
                muted={app.isDefault}
                trailing={
                    app.isDefault ? (
                        <span className="text-[11px] text-muted shrink-0">{t("privacy.ignoredApps.alwaysHidden")}</span>
                    ) : (
                        <RemoveAppButton onClick={() => setPendingRemoval({ id: app.bundlePrefix, name })} />
                    )
                }
            />
        );
    };

    return (
        <section className="flex flex-col gap-2">
            <AppListHeading
                title={t("privacy.ignoredApps.hiddenTitle")}
                description={t("privacy.ignoredApps.hiddenDescription")}
            />
            {loading ? (
                <span className="text-xs text-muted">{t("privacy.ignoredApps.loading")}</span>
            ) : (
                (installedDefaults.length > 0 || userApps.length > 0) && (
                    <div className="flex flex-col divide-y divide-border/50">
                        {installedDefaults.map(renderRow)}
                        {userApps.map(renderRow)}
                    </div>
                )
            )}
            <AppPicker candidates={candidates} onPick={(app) => void handleAdd(app)} />
            <ConfirmRemoveAppDialog
                pending={pendingRemoval}
                onClose={() => setPendingRemoval(null)}
                onConfirm={handleRemove}
                copy={{
                    title: t("privacy.ignoredApps.confirmUnhide.title", { name: pendingRemoval?.name ?? "" }),
                    description: t("privacy.ignoredApps.confirmUnhide.description"),
                    confirm: t("privacy.ignoredApps.confirmUnhide.confirm"),
                }}
            />
        </section>
    );
}

interface VoiceInputAppEntry {
    bundleId: string;
    appName: string;
    isDefault: boolean;
}

/** Apps Isoform does not listen to while they are in front, such as dictation
 *  tools. Only meaningful while listening is on, so it gives way to a reason
 *  and a link to Observation while listening is off. */
function NotListenedAppsSection({
    systemApps,
    listeningOff,
    onOpenPage,
}: {
    systemApps: SystemAppInfo[];
    listeningOff: boolean;
    onOpenPage?: OpenSettingsPage;
}) {
    const { t } = useTranslation("settings");
    const [apps, setApps] = useState<VoiceInputAppEntry[]>([]);
    const [loading, setLoading] = useState(true);
    const [pendingRemoval, setPendingRemoval] = useState<PendingRemoval | null>(null);

    useEffect(() => {
        GetVoiceInputApps()
            .then((list: VoiceInputAppEntry[]) => setApps(list ?? []))
            .catch(() => toast.error(t("voiceInputApps.toasts.loadFailed")))
            .finally(() => setLoading(false));
    }, [t]);

    const handleAdd = async (app: SystemAppInfo) => {
        try {
            await AddVoiceInputApp(app.bundleId, app.name);
            setApps((prev) => {
                const next: VoiceInputAppEntry[] = [
                    ...prev.filter((a) => a.bundleId !== app.bundleId),
                    { bundleId: app.bundleId, appName: app.name, isDefault: false },
                ];
                next.sort((a, b) => a.appName.localeCompare(b.appName));
                return next;
            });
        } catch {
            toast.error(t("voiceInputApps.toasts.addFailed"));
        }
    };

    const handleRemove = (bundleId: string) => {
        RemoveVoiceInputApp(bundleId)
            .then(() => {
                setApps((prev) => prev.filter((a) => a.bundleId !== bundleId));
            })
            .catch(() => toast.error(t("voiceInputApps.toasts.removeFailed")));
    };

    const userApps = useMemo(() => apps.filter((a) => !a.isDefault), [apps]);
    const systemAppByBundleId = useMemo(
        () =>
            systemApps.reduce<Record<string, SystemAppInfo>>((acc, app) => {
                acc[app.bundleId.toLowerCase()] = app;
                return acc;
            }, {}),
        [systemApps],
    );
    const candidates = useMemo(() => {
        const listed = new Set(apps.map((a) => a.bundleId.toLowerCase()));
        return systemApps.filter((sa) => !listed.has(sa.bundleId.toLowerCase()));
    }, [apps, systemApps]);

    return (
        <section className="flex flex-col gap-2">
            <AppListHeading
                title={t("privacy.ignoredApps.notListenedTitle")}
                description={t("privacy.ignoredApps.notListenedDescription")}
            />
            {listeningOff ? (
                <div className="flex flex-col gap-2">
                    <SettingsReason>{t("privacy.ignoredApps.notListenedOff")}</SettingsReason>
                    <Button
                        size="sm"
                        variant="outline"
                        className="self-start"
                        onClick={() => openSettingsPage("activity", onOpenPage)}
                    >
                        {t("privacy.ignoredApps.openObservation")}
                    </Button>
                </div>
            ) : (
                <>
                    {loading ? (
                        <span className="text-xs text-muted">{t("privacy.ignoredApps.loading")}</span>
                    ) : (
                        userApps.length > 0 && (
                            <div className="flex flex-col divide-y divide-border/50">
                                {userApps.map((app) => {
                                    const installed = systemAppByBundleId[app.bundleId.toLowerCase()];
                                    const name = installed?.name ?? app.appName;
                                    return (
                                        <AppListRow
                                            key={app.bundleId}
                                            icon={installed?.icon}
                                            name={name}
                                            trailing={
                                                <RemoveAppButton
                                                    onClick={() => setPendingRemoval({ id: app.bundleId, name })}
                                                />
                                            }
                                        />
                                    );
                                })}
                            </div>
                        )
                    )}
                    <AppPicker candidates={candidates} onPick={(app) => void handleAdd(app)} />
                </>
            )}
            <ConfirmRemoveAppDialog
                pending={pendingRemoval}
                onClose={() => setPendingRemoval(null)}
                onConfirm={handleRemove}
                copy={{
                    title: t("privacy.ignoredApps.confirmListen.title", { name: pendingRemoval?.name ?? "" }),
                    description: t("privacy.ignoredApps.confirmListen.description"),
                    confirm: t("privacy.ignoredApps.confirmListen.confirm"),
                }}
            />
        </section>
    );
}

/** Both per-app lists in one place: people who want Isoform to leave an app
 *  alone look here, whether they mean seeing it or hearing it. */
function IgnoredAppsCard({ listeningOff, onOpenPage }: { listeningOff: boolean; onOpenPage?: OpenSettingsPage }) {
    const { t } = useTranslation("settings");
    const [systemApps, setSystemApps] = useState<SystemAppInfo[]>([]);

    useEffect(() => {
        ListSystemApps()
            .then((list) => setSystemApps(list ?? []))
            .catch(() => {});
    }, []);

    return (
        <SettingsCard title={t("privacy.ignoredApps.title")} description={t("privacy.ignoredApps.description")}>
            <div className="flex flex-col gap-5 pb-1">
                <HiddenAppsSection systemApps={systemApps} />
                <div className="border-t border-border/50" />
                <NotListenedAppsSection systemApps={systemApps} listeningOff={listeningOff} onOpenPage={onOpenPage} />
            </div>
        </SettingsCard>
    );
}

// ── Diagnostic data ──

function DiagnosticDataCard() {
    const { t } = useTranslation("settings");
    const { enabled, loading, saving, setEnabled } = useTelemetryPreference();
    const title = t("privacy.diagnostics.title");

    const handleChange = (next: boolean) => {
        void setEnabled(next).catch(() => {
            toast.error(t("privacy.diagnostics.saveFailed"));
        });
    };

    return (
        <SettingsControlCard
            title={title}
            description={t("privacy.diagnostics.description")}
            control={<Switch aria-label={title} checked={enabled} disabled={loading || saving} onCheckedChange={handleChange} />}
        />
    );
}

// Asking before risky Bash commands in attended chats. Off by default: attended
// runs execute Bash without asking, while hard blocks (private keys, password
// stores, Isoform's own data) hold either way. Backed by
// Get/SetAskBeforeRiskyCommands in risky_commands_config.go.
function RiskyCommandsCard() {
    const { t } = useTranslation("settings");
    const [enabled, setEnabled] = useState<boolean | null>(null);
    const [readFailed, setReadFailed] = useState(false);
    const title = t("privacy.riskyCommands.title");

    const read = () => {
        setEnabled(null);
        setReadFailed(false);
        GetAskBeforeRiskyCommands().then((value) => {
            if (typeof value !== "boolean") throw new Error("Invalid setting");
            setEnabled(value);
        }).catch(() => setReadFailed(true));
    };
    useEffect(() => { read(); }, []);

    const handleChange = (next: boolean) => {
        setEnabled(next);
        SetAskBeforeRiskyCommands(next).catch(() => {
            read();
            toast.error(t("privacy.riskyCommands.saveFailed"));
        });
    };

    return (
        <SettingsControlCard
            title={title}
            description={t("privacy.riskyCommands.description")}
            control={readFailed ? <SettingReadError retry={read} /> : enabled === null ? null : <Switch aria-label={title} checked={enabled} onCheckedChange={handleChange} />}
        />
    );
}

/** How long the forget waits for Undo before it runs. */
export const ASSISTANT_HISTORY_FORGET_MS = 5000;

// Reading the Claude Code and Codex history on this computer (first-day
// handoff §7). On by default. Turning it off forgets what came from it, after
// an Undo window; turning it on reads the history again at once. Backed by
// Get/SetReadAssistantHistory in assistant_history.go.
function AssistantHistoryCard() {
    const { t } = useTranslation("settings");
    const [enabled, setEnabled] = useState<boolean | null>(null);
    const [readFailed, setReadFailed] = useState(false);
    const pending = useRef<ReturnType<typeof setTimeout> | null>(null);
    const undoToast = useRef<string | number | undefined>(undefined);
    const title = t("privacy.assistantHistory.title");

    const read = () => {
        setEnabled(null);
        setReadFailed(false);
        GetReadAssistantHistory().then((on) => {
            if (typeof on !== "boolean") throw new Error("Invalid setting");
            setEnabled(on);
        }).catch(() => setReadFailed(true));
    };
    useEffect(() => { read(); }, []);

    // Leaving the page doesn't cancel the forget: the switch already shows off.
    useEffect(() => () => {
        if (!pending.current) return;
        clearTimeout(pending.current);
        pending.current = null;
        if (undoToast.current !== undefined) toast.dismiss(undoToast.current);
        SetReadAssistantHistory(false).catch(() => toast.error(t("privacy.assistantHistory.forgetFailed"), { duration: Infinity }));
    }, []);

    const save = (next: boolean) => {
        SetReadAssistantHistory(next).catch(async () => {
            // The preference may already be saved even if erasing a record
            // failed. Show the persisted switch state and report the error.
            try {
                setEnabled(await GetReadAssistantHistory());
            } catch {
                setEnabled(null);
                setReadFailed(true);
            }
            toast.error(t(next ? "privacy.assistantHistory.saveFailed" : "privacy.assistantHistory.forgetFailed"), { duration: Infinity });
        });
    };

    const handleChange = (next: boolean) => {
        setEnabled(next);
        if (next) {
            if (pending.current) {
                clearTimeout(pending.current);
                pending.current = null;
                return;
            }
            save(true);
            return;
        }
        pending.current = setTimeout(() => {
            pending.current = null;
            save(false);
        }, ASSISTANT_HISTORY_FORGET_MS);
        undoToast.current = toast(t("privacy.assistantHistory.forgetting"), {
            duration: ASSISTANT_HISTORY_FORGET_MS,
            action: {
                label: t("privacy.assistantHistory.undo"),
                onClick: () => {
                    if (pending.current) clearTimeout(pending.current);
                    pending.current = null;
                    setEnabled(true);
                },
            },
        });
    };

    return (
        <SettingsControlCard
            title={title}
            description={t("privacy.assistantHistory.description")}
            control={readFailed ? <SettingReadError retry={read} /> : enabled === null ? null : <Switch aria-label={title} checked={enabled} onCheckedChange={handleChange} />}
        />
    );
}

// ── Advanced: computer permissions, status only ──

function PermissionStatusRow({
    item,
    platform,
    onGrant,
    onOpenPeople,
}: {
    item: PermissionItem;
    platform: string;
    onOpenPeople?: () => void;
    onGrant: (item: PermissionItem) => void;
}) {
    const { t } = useTranslation("settings");
    let description: string;
    if (item.key === "contacts") {
        description = t(platform === "darwin" ? "permissions.items.contacts.importNote" : "permissions.items.contacts.vcardNote");
    } else if (item.toggleable && !item.enabled) {
        description = t("privacy.permissions.usedForTurnedOff", { purpose: permissionPurpose(t, item.key) });
    } else {
        description = t("privacy.permissions.usedFor", { purpose: permissionPurpose(t, item.key) });
    }
    return (
        <SettingsRow
            title={item.key === "contacts" && platform !== "darwin" ? t("permissions.items.contacts.vcardLabel") : permissionLabel(t, item.key)}
            description={description}
            control={item.key === "contacts" ? (
                onOpenPeople ? <Button type="button" variant="outline" size="sm" onClick={onOpenPeople}>{t("learningMemory.knows.openPeople")}</Button> : null
            ) : (
                <PermissionStatusChip
                    granted={item.granted}
                    onGrant={item.actionable && item.key !== "automation_messages" ? () => onGrant(item) : undefined}
                />
            )}
        />
    );
}

/** The one full list of what the computer lets Isoform use. Watching and
 *  listening are switched on Observation, so there are no switches here. */
function PermissionListCard({
    permissions,
    loading,
    onGrant,
    onOpenPeople,
}: {
    onOpenPeople?: () => void;
    permissions: PlatformPermissions | null;
    loading: boolean;
    onGrant: (item: PermissionItem) => void;
}) {
    const { t } = useTranslation("settings");
    const items = permissions?.items ?? [];

    let body: ReactNode;
    if (!permissions) {
        body = (
            <span className="text-xs text-muted">
                {loading ? t("permissions.checking") : t("permissions.unavailable")}
            </span>
        );
    } else if (items.length === 0) {
        body = (
            <div className="flex items-center gap-2 text-xs text-accent-2">
                <CheckCircle2 className="h-3.5 w-3.5" />
                {t("permissions.noneRequired")}
            </div>
        );
    } else {
        body = items.map((item) => (
            <div key={item.key} {...(item.key === "contacts" ? settingsAnchor("contacts") : {})}><PermissionStatusRow item={item} platform={permissions.platform} onGrant={onGrant} onOpenPeople={onOpenPeople} /></div>
        ));
    }

    return (
        <SettingsCard title={t("privacy.permissions.title")} description={t("privacy.permissions.description")}>
            {body}
        </SettingsCard>
    );
}

// ── What stays on this computer ──

function WhatStaysCard() {
    const { t } = useTranslation("onboarding");
    return (
        <SettingsCard title={t("v2.local.title")}>
            <OnThisMacLines className="settings-card-desc flex flex-col gap-1.5 [&_span]:font-semibold [&_span]:text-[var(--s-ink)]" />
        </SettingsCard>
    );
}

// ── Page ──

/** Privacy (page id "security"): what stays private. */
export function PrivacyPage({ onOpenPage, onOpenPeople }: { onOpenPage?: OpenSettingsPage; onOpenPeople?: () => void } = {}) {
    const { t } = useTranslation("settings");
    const storage = useRecordingStorage();
    const listening = useListening();
    const { permissions, loading, requestPermission } = usePermissions();

    return (
        <>
            <WhatStaysCard />
            <IgnoredAppsCard listeningOff={listening.enabled === false} onOpenPage={onOpenPage} />
            <RecordingRetentionCard storage={storage} />
            <VoiceMemoryCard />
            <DiagnosticDataCard />
            <AssistantHistoryCard />
            <RiskyCommandsCard />
            <SettingsAdvanced pageId="security" hint={t("privacy.advanced.hint")}>
                <PermissionListCard
                    permissions={permissions}
                    loading={loading}
                    onGrant={(item) => void requestPermission(item)}
                    onOpenPeople={onOpenPeople}
                />
                <SettingsControlCard
                    title={t("privacy.diagnostics.detailsTitle")}
                    description={t("privacy.diagnostics.detailsDescription")}
                />
            </SettingsAdvanced>
        </>
    );
}
