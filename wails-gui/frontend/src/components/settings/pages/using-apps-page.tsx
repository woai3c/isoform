import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { Switch } from "@/components/ui/switch";
import { SettingsFailureLine, useSettingsFailure } from "@/components/settings/settings-failure";
import {
    ComputerUseAccessCard,
    ComputerUseAppList,
    computerUseStatusId,
    useComputerUseAppAccess,
    type ComputerUseAppAccessState,
} from "@/components/computer-use-app-access-settings";
import { GetCuaCursorOverlayEnabled, SetCuaCursorOverlayEnabled } from "@/lib/wails-shim-app";
import { SettingsCard, SettingsRow } from "@/components/settings/settings-primitives";
import { SettingsAdvanced } from "@/components/settings/settings-advanced";

/** Why the cursor switch cannot be used yet, or null while it can. The cursor
 *  only shows while Isoform uses your apps, so it waits for that setup.
 *  "locked" counts as set up: access is granted and the lock is temporary.
 *  While the status is still being read (or could not be read) the switch
 *  stays usable rather than flashing disabled. */
function cursorBlockedReason(
    access: ComputerUseAppAccessState,
    t: TFunction<"settings">,
): string | null {
    if (!access.snapshot) return null;
    switch (computerUseStatusId(access.snapshot)) {
        case "ready":
        case "read_only_ready":
        case "locked":
            return null;
        case "unavailable":
            return t("usingApps.status.unavailable.description");
        case "permission_required":
        case "unknown":
            return t("cursorOverlay.needsAccess");
    }
}

// ── Computer-Use Cursor Overlay ──
// Toggles the bundled cua-driver's virtual-cursor lens overlay — the
// animated pointer + click markers shown wherever Isoform acts during
// computer use. Backed by Get/SetCuaCursorOverlayEnabled in
// cua_cursor_overlay_config.go; the toggle hot-applies to a running
// daemon and is baked into argv on the next spawn. Disabled, with the
// reason in place of the description, until "Let Isoform use your apps"
// is set up.
function CursorOverlaySettings({ blockedReason }: { blockedReason: string | null }) {
    const { t } = useTranslation('settings');
    const { failure: changeFailure, fail: failChange } = useSettingsFailure();
    const [enabled, setEnabledState] = useState(true);
    const [loading, setLoading] = useState(true);

    useEffect(() => {
        GetCuaCursorOverlayEnabled()
            .then((v) => {
                setEnabledState(v);
                setLoading(false);
            })
            .catch(() => setLoading(false));
    }, []);

    const handleToggle = () => {
        const next = !enabled;
        setLoading(true);
        setEnabledState(next);
        SetCuaCursorOverlayEnabled(next)
            .catch(() => {
                setEnabledState(!next);
                failChange(t('cursorOverlay.toggleFailed'), () => void handleToggle());
            })
            .finally(() => {
                setLoading(false);
            });
    };

    return (
        <SettingsCard>
            <SettingsRow
                title={t('cursorOverlay.rowTitle')}
                description={blockedReason ?? t('cursorOverlay.rowDescription')}
                control={
                    <Switch
                        aria-label={t('cursorOverlay.rowTitle')}
                        checked={enabled}
                        disabled={loading || blockedReason !== null}
                        onCheckedChange={handleToggle}
                    />
                }
            />
            <SettingsFailureLine failure={changeFailure} />
        </SettingsCard>
    );
}

// ── Using your apps page ──

/**
 * Using your apps (page id "app-access"): Isoform works in the apps you are
 * already signed in to through Computer Use. The page opens with whether that
 * works and the one setup control, then the Action cursor switch (the old
 * "cursor-overlay" page). The list of apps Isoform knows how to use sits under
 * Advanced. The composer app chips and the main-window banner deep-link here.
 */
export function UsingAppsPage() {
    const { t } = useTranslation('settings');
    const access = useComputerUseAppAccess();

    return (
        <>
            <ComputerUseAccessCard access={access} />
            <CursorOverlaySettings blockedReason={cursorBlockedReason(access, t)} />
            <SettingsAdvanced pageId="app-access" hint={t('usingApps.advancedHint')}>
                <ComputerUseAppList access={access} />
            </SettingsAdvanced>
        </>
    );
}
