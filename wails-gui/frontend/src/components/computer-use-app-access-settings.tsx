import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
    CheckCircle2,
    ExternalLink,
    Loader2,
    MonitorCog,
    RefreshCw,
    ShieldAlert,
} from "lucide-react";
import { SettingsFailureLine } from "@/components/settings/settings-failure";
import { BrowserOpenURL, EventsOn } from "../../wailsjs/runtime/runtime";
import { EnableComputerUseDialog } from "./enable-computer-use-dialog";
import { Button } from "./ui/button";
import { SettingsCard } from "@/components/settings/settings-primitives";
import {
    GetComputerUseAppAccessSnapshot,
    type ComputerUseAppAccessSnapshot,
} from "@/lib/wails-shim-app";

// ── App access (Computer Use) ──
//
// Isoform works inside the apps and websites the user is already signed in to,
// by seeing and clicking the way a person does. Nothing is connected through a
// provider account. The snapshot (computer_use_app_access.go) says whether
// that works right now and lists the apps Isoform knows its way around.

/** Live app access state, read again on window focus and when a permission
 *  changes, so a grant made in System Settings shows up on return. */
export function useComputerUseAppAccess() {
    const [snapshot, setSnapshot] = useState<ComputerUseAppAccessSnapshot | null>(null);
    const [loading, setLoading] = useState(true);
    const [failed, setFailed] = useState(false);

    const refresh = useCallback(async () => {
        setLoading(true);
        try {
            setSnapshot(await GetComputerUseAppAccessSnapshot());
            setFailed(false);
        } catch {
            setFailed(true);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void refresh();
        const handleFocus = () => void refresh();
        window.addEventListener("focus", handleFocus);
        const offPerm = EventsOn("permissions:changed", () => void refresh());
        return () => {
            window.removeEventListener("focus", handleFocus);
            offPerm();
        };
    }, [refresh]);

    return { snapshot, loading, failed, refresh };
}

export type ComputerUseAppAccessState = ReturnType<typeof useComputerUseAppAccess>;

// Go status ids (computer_use_app_access.go) mapped to plain copy.
const STATUS_IDS = [
    "ready",
    "read_only_ready",
    "permission_required",
    "locked",
    "unavailable",
] as const;
export type ComputerUseStatusId = (typeof STATUS_IDS)[number] | "unknown";

/** The snapshot's status, or "unknown" for a status this build does not know. */
export function computerUseStatusId(snapshot: ComputerUseAppAccessSnapshot): ComputerUseStatusId {
    return (STATUS_IDS as readonly string[]).includes(snapshot.status)
        ? (snapshot.status as ComputerUseStatusId)
        : "unknown";
}

/**
 * Everyday card: whether Isoform can use your apps, with the one setup
 * control (the guided Accessibility and Screen Recording dialog).
 */
export function ComputerUseAccessCard({ access }: { access: ComputerUseAppAccessState }) {
    const { t } = useTranslation("settings");
    const { snapshot, refresh } = access;
    const unread = access.failed && !snapshot;
    const [setupOpen, setSetupOpen] = useState(false);

    const ready = Boolean(
        snapshot?.available && snapshot.accessibility && !snapshot.screenLocked,
    );
    const id = snapshot ? computerUseStatusId(snapshot) : null;
    const title = id ? t(`usingApps.status.${id}.title`) : t("usingApps.status.checking.title");
    const description = id
        ? t(`usingApps.status.${id}.description`)
        : t("usingApps.status.checking.description");

    return (
        <>
            <EnableComputerUseDialog
                open={setupOpen}
                onOpenChange={(open) => {
                    setSetupOpen(open);
                    if (!open) void refresh();
                }}
            />
            <SettingsCard
                title={t("usingApps.access.title")}
                description={t("usingApps.access.description")}
            >
                {unread && (
                    <SettingsFailureLine
                        failure={{ text: t("usingApps.loadFailedToast"), retry: () => void refresh() }}
                    />
                )}
                <div className={unread ? "hidden" : "settings-row"}>
                    <div className="flex min-w-0 items-start gap-3">
                        {ready ? (
                            <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-success" />
                        ) : (
                            <ShieldAlert className="mt-0.5 h-4 w-4 shrink-0 text-warning" />
                        )}
                        <div className="flex min-w-0 flex-col">
                            <span className="settings-row-title">{title}</span>
                            <span className="settings-row-desc">{description}</span>
                        </div>
                    </div>
                    <div className="shrink-0">
                        <Button
                            type="button"
                            size="sm"
                            variant={ready ? "outline" : "default"}
                            onClick={() => setSetupOpen(true)}
                        >
                            <MonitorCog className="h-3.5 w-3.5" />
                            {ready ? t("usingApps.reviewPermissions") : t("usingApps.setUp")}
                        </Button>
                    </div>
                </div>
            </SettingsCard>
        </>
    );
}

const CATEGORY_IDS = ["communication", "planning", "documents", "development"] as const;
const EMAIL_APP_IDS = new Set(["gmail", "outlook", "mail", "apple-mail"]);

/** Advanced: the apps Isoform knows how to use, each with an Open button. */
export function ComputerUseAppList({ access }: { access: ComputerUseAppAccessState }) {
    const { t } = useTranslation("settings");
    const { snapshot, loading, refresh } = access;
    const apps = snapshot?.apps ?? [];

    // settings-review ST73: chat apps read "Messages"; mail apps read "Email".
    const categoryLabel = (category: string, id = "") =>
        category === "communication"
            ? (EMAIL_APP_IDS.has(id) ? t("usingApps.apps.categories.email") : t("usingApps.apps.categories.messages"))
            : (CATEGORY_IDS as readonly string[]).includes(category)
            ? t(`usingApps.apps.categories.${category as (typeof CATEGORY_IDS)[number]}`)
            : t("usingApps.apps.categories.other");

    return (
        <SettingsCard
            title={t("usingApps.apps.title")}
            description={t("usingApps.apps.description")}
            headerRight={
                <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    onClick={() => void refresh()}
                    disabled={loading}
                >
                    {loading ? (
                        <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    ) : (
                        <RefreshCw className="h-3.5 w-3.5" />
                    )}
                    {t("usingApps.apps.refresh")}
                </Button>
            }
        >
            {apps.length === 0 ? (
                <p className="settings-row-desc pb-2">
                    {loading ? t("usingApps.apps.loading") : t("usingApps.apps.empty")}
                </p>
            ) : (
                <div>
                    {apps.map((app) => (
                        <div key={app.id} className="settings-row">
                            <div className="flex min-w-0 flex-col">
                                <span className="settings-row-title truncate">{app.label}</span>
                                <span className="settings-row-desc">{categoryLabel(app.category, app.id)}</span>
                            </div>
                            {app.entryUrl && (
                                <Button
                                    type="button"
                                    size="icon"
                                    variant="ghost"
                                    aria-label={t("usingApps.apps.open", { app: app.label })}
                                    title={t("usingApps.apps.open", { app: app.label })}
                                    onClick={() => BrowserOpenURL(app.entryUrl!)}
                                >
                                    <ExternalLink className="h-3.5 w-3.5" />
                                </Button>
                            )}
                        </div>
                    ))}
                </div>
            )}
        </SettingsCard>
    );
}
