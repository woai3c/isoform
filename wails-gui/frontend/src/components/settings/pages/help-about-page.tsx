import { AboutUpdates } from "./about-updates";
import { useCallback, useState } from "react";
import { useTranslation } from "react-i18next";
import { Loader2, Copy } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
    GetPerfSnapshot,
    OpenExternalURL,
} from "../../../../wailsjs/go/main/App";
import { DiscordIcon } from "@/components/icons/app-icons";
import { toast } from "sonner";
import {
    SettingsCard,
    SettingsRow,
} from "@/components/settings/settings-primitives";
import { SettingsAdvanced } from "@/components/settings/settings-advanced";
import {
    copyPerfReport,
    type PerfSnapshotShape,
} from "@/components/settings/perf-report";

const DISCORD_INVITE_URL = "https://discord.gg/6XaU6hGKda";

// ── Get help ──

function GetHelp() {
    const { t } = useTranslation("settings");
    const [copyingReport, setCopyingReport] = useState(false);
    // The support report: a one-click "grab the perf snapshot into your
    // clipboard" for users who don't know what devtools is — what they get is
    // the same text-formatted report the Perf Analysis panel produces, safe to
    // paste into a bug report. The text formatter deliberately omits
    // subprocess command lines (which may contain `/Users/<name>/…`), so
    // nothing identifying leaks through this path. It is named "support
    // report" so it never reads like Privacy's "Share diagnostic data".
    const handleCopyReport = useCallback(async () => {
        setCopyingReport(true);
        try {
            const snap =
                (await GetPerfSnapshot()) as unknown as PerfSnapshotShape;
            await copyPerfReport(snap, t("about.supportReport.copiedToast"));
        } catch (err) {
            toast.error(
                t("about.supportReport.errorToast", {
                    error: err instanceof Error ? err.message : String(err),
                }),
            );
        } finally {
            setCopyingReport(false);
        }
    }, [t]);

    return (
        <SettingsCard
            title={t("support.title")}
            description={t("support.description")}
        >
            <SettingsRow
                title={t("support.discordTitle")}
                description={t("support.discordDescription")}
                control={
                    <Button
                        size="sm"
                        variant="secondary"
                        className="border-border text-ink"
                        onClick={() => {
                            void OpenExternalURL(DISCORD_INVITE_URL);
                        }}
                    >
                        <DiscordIcon monochrome className="h-3.5 w-3.5 mr-1.5" />
                        {t("support.discordButton")}
                    </Button>
                }
            />
            <SettingsRow
                title={t("about.supportReport.title")}
                description={t("about.supportReport.description")}
                control={
                    <Button
                        variant="outline"
                        size="sm"
                        onClick={handleCopyReport}
                        disabled={copyingReport}
                    >
                        {copyingReport ? (
                            <Loader2 className="h-3.5 w-3.5 mr-1.5 animate-spin" />
                        ) : (
                            <Copy className="h-3.5 w-3.5 mr-1.5" />
                        )}
                        {copyingReport
                            ? t("about.collecting")
                            : t("about.supportReport.copy")}
                    </Button>
                }
            />
        </SettingsCard>
    );
}

// ── What the support report includes (Advanced) ──

function SupportReportDetails() {
    const { t } = useTranslation("settings");
    return (
        <SettingsCard
            title={t("about.supportReport.details.title")}
            description={t("about.supportReport.details.description")}
        >
            <SettingsRow
                title={t("about.supportReport.details.includedTitle")}
                description={t("about.supportReport.details.included")}
            />
            <SettingsRow
                title={t("about.supportReport.details.excludedTitle")}
                description={t("about.supportReport.details.excluded")}
            />
        </SettingsCard>
    );
}

// ── About page ──

export interface HelpAboutPageProps {
    /** Reopens the first-run setup (moved here from Developer tools). */
    onReplayOnboarding: () => void;
}

/**
 * About (page id "about"): version and updates, getting help, and
 * running setup again. The old "support" page id resolves here. Developer
 * tools have no switch here: Cmd+Shift+Option+D turns them on and off
 * (useDevToolsUnlock).
 */
export function HelpAboutPage({ onReplayOnboarding }: HelpAboutPageProps) {
    const { t } = useTranslation("settings");
    return (
        <>
            <AboutUpdates />
            <SettingsAdvanced pageId="about" hint={t("about.advancedHint")}>
                <GetHelp />
                <SettingsCard>
                    <SettingsRow
                        title={t("onboarding.title")}
                        description={t("onboarding.description")}
                        control={
                            <Button
                                size="sm"
                                variant="outline"
                                onClick={onReplayOnboarding}
                            >
                                {t("onboarding.replayButton")}
                            </Button>
                        }
                    />
                </SettingsCard>
                <SupportReportDetails />
            </SettingsAdvanced>
        </>
    );
}
