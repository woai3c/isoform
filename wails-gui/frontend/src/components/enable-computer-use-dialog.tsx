import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { Camera, Check, PersonStanding } from "lucide-react";
import { activeInterval } from "@/lib/app-activity";
import { toast } from "sonner";
import {
    GetPlatformPermissions,
    OpenPermissionSettings,
    OpenPermissionSettingsFromRect,
} from "../../wailsjs/go/main/App";
import { Dialog, DialogContent } from "@/components/ui/dialog";
import { YansuIcon } from "@/components/icons/app-icons";
import type { PermissionItem } from "@/types";

const PANEL_KEYS = ["accessibility", "screen_recording"] as const;
type PanelKey = (typeof PANEL_KEYS)[number];

// Labels come from settings:permissions.items.<key>.label and the one-line
// reasons from settings:usingApps.setupDialog.<key>.
const PANEL_META: Record<
    PanelKey,
    {
        Icon: typeof PersonStanding;
        accent: string;
    }
> = {
    accessibility: {
        Icon: PersonStanding,
        accent: "bg-accent text-solid-btn-fg",
    },
    screen_recording: {
        Icon: Camera,
        accent: "bg-panel-warm text-ink",
    },
};

type RowState = "pending" | "inflight" | "done";

interface Props {
    open: boolean;
    onOpenChange: (open: boolean) => void;
}

export function EnableComputerUseDialog({ open, onOpenChange }: Props) {
    const { t } = useTranslation("settings");
    const [granted, setGranted] = useState<Record<PanelKey, boolean>>({
        accessibility: false,
        screen_recording: false,
    });
    const [inflight, setInflight] = useState<Record<PanelKey, boolean>>({
        accessibility: false,
        screen_recording: false,
    });

    const refresh = useCallback(async () => {
        try {
            const perms = await GetPlatformPermissions();
            const next: Record<PanelKey, boolean> = {
                accessibility: false,
                screen_recording: false,
            };
            for (const item of perms.items as PermissionItem[]) {
                if (item.key === "accessibility" || item.key === "screen_recording") {
                    next[item.key] = item.granted;
                }
            }
            setGranted(next);
            // Once granted, clear inflight so the row settles into "Done".
            setInflight((prev) => ({
                accessibility: prev.accessibility && !next.accessibility,
                screen_recording: prev.screen_recording && !next.screen_recording,
            }));
        } catch {
            // Swallow — we'll retry on the next tick.
        }
    }, []);

    // Poll while open so the rows update as the user grants permission.
    useEffect(() => {
        if (!open) return;
        void refresh();
        // Stopped while the window is hidden or the Mac locked.
        const stop = activeInterval(refresh, 1000);
        const onFocus = () => void refresh();
        window.addEventListener("focus", onFocus);
        return () => {
            stop();
            window.removeEventListener("focus", onFocus);
        };
    }, [open, refresh]);

    const allDone = granted.accessibility && granted.screen_recording;

    const handleAllow = useCallback(
        async (key: PanelKey, ev: React.MouseEvent<HTMLButtonElement>) => {
            const rect = ev.currentTarget.getBoundingClientRect();
            const chrome = Math.max(0, window.outerHeight - window.innerHeight);
            setInflight((prev) => ({ ...prev, [key]: true }));
            try {
                await OpenPermissionSettingsFromRect(key, {
                    x: window.screenX + rect.left,
                    y: window.screenY + chrome + rect.top,
                    width: rect.width,
                    height: rect.height,
                });
            } catch {
                try {
                    await OpenPermissionSettings(key);
                } catch (e) {
                    setInflight((prev) => ({ ...prev, [key]: false }));
                    toast.error(
                        t("usingApps.setupDialog.openFailed", {
                            label: t(`permissions.items.${key}.label`),
                            error: e instanceof Error ? e.message : String(e),
                        }),
                    );
                }
            }
        },
        [t],
    );

    const rows = useMemo(
        () =>
            PANEL_KEYS.map((key) => ({
                key,
                ...PANEL_META[key],
                label: t(`permissions.items.${key}.label`),
                description: t(`usingApps.setupDialog.${key}`),
                state: granted[key]
                    ? ("done" as RowState)
                    : inflight[key]
                      ? ("inflight" as RowState)
                      : ("pending" as RowState),
            })),
        [granted, inflight, t],
    );

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="w-[440px] p-0 overflow-hidden border-border bg-panel">
                <div className="flex flex-col items-center gap-3 px-8 pt-8 pb-2">
                    <div className="h-16 w-16 rounded-2xl bg-ink/[0.06] flex items-center justify-center shadow-[var(--shadow-soft)]">
                        <YansuIcon className="h-10 w-10" />
                    </div>
                    <h2 className="text-[19px] font-semibold text-ink text-center leading-tight">
                        {t("usingApps.setupDialog.title")}
                    </h2>
                    <p className="text-[13px] text-muted text-center leading-snug max-w-[320px]">
                        {t("usingApps.setupDialog.description")}
                    </p>
                </div>

                <div className="flex flex-col gap-2 px-5 py-5">
                    {rows.map((row) => (
                        <PermissionRowCard
                            key={row.key}
                            label={row.label}
                            description={row.description}
                            Icon={row.Icon}
                            accent={row.accent}
                            state={row.state}
                            onAllow={(ev) => handleAllow(row.key, ev)}
                        />
                    ))}
                </div>

                {allDone && (
                    <div className="px-5 pb-5">
                        <button
                            className="w-full h-10 rounded-lg patina-verdigris bg-solid-btn-bg text-solid-btn-fg shadow-[var(--shadow-solid-btn)] text-sm font-medium hover:opacity-90 transition-colors cursor-pointer"
                            onClick={() => onOpenChange(false)}
                        >
                            {t("usingApps.setupDialog.done")}
                        </button>
                    </div>
                )}
            </DialogContent>
        </Dialog>
    );
}

function PermissionRowCard({
    label,
    description,
    Icon,
    accent,
    state,
    onAllow,
}: {
    label: string;
    description: string;
    Icon: typeof PersonStanding;
    accent: string;
    state: RowState;
    onAllow: (ev: React.MouseEvent<HTMLButtonElement>) => void;
}) {
    const { t } = useTranslation("settings");
    return (
        <div className="rounded-xl bg-ink/[0.04] border border-border/40 px-3.5 py-3">
            <div className="flex items-center gap-3">
                <div
                    className={`h-9 w-9 rounded-lg flex items-center justify-center shrink-0 ${accent}`}
                >
                    <Icon className="h-5 w-5" strokeWidth={2.2} />
                </div>
                <div className="flex flex-col min-w-0 flex-1">
                    <span className="text-[13px] font-semibold text-ink leading-tight">
                        {label}
                    </span>
                    <span className="text-[11.5px] text-muted leading-tight mt-0.5">
                        {description}
                    </span>
                </div>
                {state === "pending" && (
                    <button
                        onClick={onAllow}
                        className="px-4 h-7 rounded-full patina-verdigris bg-solid-btn-bg text-solid-btn-fg shadow-[var(--shadow-solid-btn)] text-[12px] font-semibold hover:opacity-90 transition-colors cursor-pointer shrink-0"
                    >
                        {t("usingApps.setupDialog.allow")}
                    </button>
                )}
                {state === "done" && (
                    <span className="inline-flex items-center gap-1 text-[12px] font-semibold text-accent-2 shrink-0 pr-1">
                        <Check className="h-3.5 w-3.5" strokeWidth={3} />
                        {t("usingApps.setupDialog.done")}
                    </span>
                )}
            </div>
            {state === "inflight" && (
                <div className="mt-2.5 w-full h-8 rounded-md bg-ink/[0.05] border border-border/50 flex items-center justify-center text-[11.5px] font-semibold tracking-wide text-muted uppercase">
                    {t("usingApps.setupDialog.finishInSystemSettings")}
                </div>
            )}
        </div>
    );
}
