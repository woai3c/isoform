import { useTranslation } from "react-i18next";
import { Check, Github } from "lucide-react";
import { Button } from "@/components/ui/button";
import { AssistantMCPSettings } from "@/components/assistant-mcp-settings";
import { SettingsCard } from "@/components/settings/settings-primitives";
import { SettingsFailureLine, useSettingsChange } from "@/components/settings/settings-failure";
import { OpenExternalURL } from "@/lib/wails-shim-app";

const SETUP_URL = "https://github.com/yubozhao/isoform/blob/main/docs/isoform-mcp-server.md";

/**
 * Other AI apps (Settings page id "use-yansu-anywhere"): Connect / Revoke for
 * Claude Code and Codex, then the by-hand setup in the public repository.
 * Access comes only from Connect; there is no prompt to paste.
 */
export function UseYansuAnywhereSettings() {
    const { t } = useTranslation("settings");
    const { failure, run } = useSettingsChange(t("useYansuAnywhere.manual.openFailed"));
    return (
        <>
            <AssistantMCPSettings />
            <SettingsCard
                title={t("useYansuAnywhere.manual.title")}
                description={t("useYansuAnywhere.manual.description")}
            >
                <div className="flex flex-col items-start gap-2">
                    <Button type="button" variant="outline" size="sm" onClick={() => void run(() => OpenExternalURL(SETUP_URL))}>
                        <Github className="h-3.5 w-3.5" />
                        {t("useYansuAnywhere.manual.open")}
                    </Button>
                    <SettingsFailureLine failure={failure} />
                </div>
            </SettingsCard>
        </>
    );
}

/** The Advanced card for Other AI apps: what another assistant can recall. */
export function UseYansuAnywhereDetails() {
    const { t } = useTranslation("settings");
    const capabilities = [
        t("useYansuAnywhere.capabilities.workflows"),
        t("useYansuAnywhere.capabilities.problems"),
        t("useYansuAnywhere.capabilities.preferences"),
        t("useYansuAnywhere.capabilities.context"),
    ];
    return (
        <SettingsCard title={t("useYansuAnywhere.capabilitiesTitle")} description={t("useYansuAnywhere.boundaryNote")}>
            <ul className="flex flex-col gap-1">
                {capabilities.map((line) => (
                    <li key={line} className="flex items-start gap-2 settings-row-desc">
                        <Check className="h-3.5 w-3.5 shrink-0 mt-0.5 text-accent" />
                        <span>{line}</span>
                    </li>
                ))}
            </ul>
        </SettingsCard>
    );
}
