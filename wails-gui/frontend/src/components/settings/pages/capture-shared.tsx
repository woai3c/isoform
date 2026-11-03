import { Children, useCallback, useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import type { TFunction } from "i18next";
import {
    IsSystemAudioBackgroundListeningSupported,
    SetBackgroundListening,
    SetPermissionEnabled,
} from "../../../../wailsjs/go/main/App";
import { GetBackgroundListening } from "@/lib/page-queries";
import { EventsEmit, EventsOn } from "../../../../wailsjs/runtime/runtime";
import { isWindows } from "@/lib/platform";
import type { PermissionItem } from "@/types";
import { SettingsCard, SettingsRow } from "@/components/settings/settings-primitives";
import type { SettingsOpenOptions, SettingsPageId } from "@/components/settings/settings-registry";

// Pieces shared by the Observation page (what Isoform sees and hears) and the
// Privacy page (what stays private): the card whose one control sits beside
// its title, the listening state both pages depend on, the inline Grant chip
// for a missing computer permission, and the jump between Settings pages
// (Notifications uses it to send you to the iPhone page, and iPhone to send
// you to Assistant > Advanced).

// ── Moving between Settings pages ──

export type OpenSettingsPage = (page: SettingsPageId, options?: SettingsOpenOptions) => void;

/** Opens another Settings page, with Advanced expanded when `options.advanced`
 *  is set. Settings passes `onOpenPage` so the jump stays inside the open
 *  dialog; the `settings:open` event is the fallback every other surface
 *  already uses. */
export function openSettingsPage(
    page: SettingsPageId,
    onOpenPage?: OpenSettingsPage,
    options?: SettingsOpenOptions,
): void {
    if (onOpenPage) {
        if (options) onOpenPage(page, options);
        else onOpenPage(page);
        return;
    }
    if (options) EventsEmit("settings:open", page, options);
    else EventsEmit("settings:open", page);
}

// ── Card with its control beside the title ──

/**
 * A SettingsCard whose one main control (a switch or a select) sits beside the
 * title, so the card reads as a sentence ("Listen [on]") instead of repeating
 * its title in a row underneath. Dependent rows, reasons and inline grants go
 * in `children`. The description keeps SettingsCard's own spacing and drops
 * its trailing margin when there is nothing below it.
 */
export function SettingsControlCard({
    title,
    description,
    control,
    children,
}: {
    title: string;
    description: string;
    control?: ReactNode;
    children?: ReactNode;
}) {
    return (
        <SettingsCard title={title} headerRight={control}>
            <p className="settings-card-desc -mt-2">{description}</p>
            {Children.toArray(children).length > 0 && <div className="mt-2 flex flex-col">{children}</div>}
        </SettingsCard>
    );
}

/** A short muted line under a card: why a control is off or unavailable. */
export function SettingsReason({ children }: { children: ReactNode }) {
    return <p className="settings-row-desc">{children}</p>;
}

// ── Listening (microphone and computer audio) ──

export interface ListeningState {
    /** null until read, or when it could not be read. */
    enabled: boolean | null;
    loadFailed: boolean;
    saving: boolean;
    systemAudioSupported: boolean;
    setEnabled: (next: boolean) => Promise<void>;
}

function errorMessage(err: unknown): string {
    if (typeof err === "string") return err;
    if (err instanceof Error) return err.message;
    return String(err);
}

/**
 * Whether Isoform listens in the background. Observation shows the switch;
 * Privacy hides the "not listened to" app list while it is off. On Windows
 * the Microphone permission item is the listening switch (Go routes it to
 * SetBackgroundListening), so writes go through it there.
 */
export function useListening(): ListeningState {
    const { t } = useTranslation("settings");
    const [enabled, setEnabledState] = useState<boolean | null>(null);
    const [loadFailed, setLoadFailed] = useState(false);
    const [saving, setSaving] = useState(false);
    const [systemAudioSupported, setSystemAudioSupported] = useState(false);

    useEffect(() => {
        let cancelled = false;
        Promise.all([GetBackgroundListening(), IsSystemAudioBackgroundListeningSupported()])
            .then(([on, systemAudio]) => {
                if (cancelled) return;
                setEnabledState(on);
                setSystemAudioSupported(systemAudio);
                setLoadFailed(false);
            })
            .catch(() => {
                if (!cancelled) setLoadFailed(true);
            });
        const off = EventsOn("background-listening:changed", (next: unknown) => {
            setEnabledState(Boolean(next));
            setLoadFailed(false);
        });
        return () => {
            cancelled = true;
            off();
        };
    }, []);

    const setEnabled = useCallback(
        async (next: boolean) => {
            setEnabledState(next);
            setSaving(true);
            try {
                if (isWindows) {
                    await SetPermissionEnabled("microphone", next);
                } else {
                    await SetBackgroundListening(next);
                }
            } catch (err) {
                setEnabledState(!next);
                toast.error(t("observation.listen.toggleFailed", { error: errorMessage(err) }));
            } finally {
                setSaving(false);
            }
        },
        [t],
    );

    return { enabled, loadFailed, saving, systemAudioSupported, setEnabled };
}

// ── Computer permissions ──

export function permissionLabel(t: TFunction<"settings">, key: string): string {
    return t(`permissions.items.${key}.label` as never) as string;
}

export function permissionPurpose(t: TFunction<"settings">, key: string): string {
    return t(`permissions.items.${key}.usedFor` as never) as string;
}

/** Granted / Not granted status, or a Grant button while access is missing
 *  and the computer can be asked for it. */
export function PermissionStatusChip({
    granted,
    onGrant,
}: {
    granted: boolean;
    onGrant?: () => void;
}) {
    const { t } = useTranslation("settings");
    if (granted || !onGrant) {
        return (
            <span className="inline-flex items-center rounded-full bg-ink/[0.05] px-2 py-0.5 text-[10px] font-medium text-ink/55">
                {granted ? t("permissions.chipGranted") : t("permissions.chipNotGranted")}
            </span>
        );
    }
    return (
        <button
            type="button"
            onClick={(event) => {
                event.preventDefault();
                onGrant();
            }}
            className="inline-flex items-center rounded-full bg-[var(--s-accent-soft)] px-2 py-0.5 text-[10px] font-medium text-[var(--s-accent)] transition-colors cursor-pointer hover:bg-[var(--s-accent-ring)]"
        >
            {t("permissions.chipGrant")}
        </button>
    );
}

/** One permission the computer has not given yet, with what it is for and a
 *  Grant button. Shown inline only where that access is needed. */
export function MissingPermissionRow({
    item,
    onGrant,
}: {
    item: PermissionItem;
    onGrant: (item: PermissionItem) => void;
}) {
    const { t } = useTranslation("settings");
    return (
        <SettingsRow
            title={permissionLabel(t, item.key)}
            description={t("privacy.permissions.usedFor", { purpose: permissionPurpose(t, item.key) })}
            control={<PermissionStatusChip granted={false} onGrant={() => onGrant(item)} />}
        />
    );
}
