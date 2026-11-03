import { useTranslation } from "react-i18next";
import { Check, Github } from "lucide-react";
import { CopyButton } from "@/components/copy-button";
import { Button, buttonVariants } from "@/components/ui/button";
import { SettingsCard } from "@/components/settings/settings-primitives";
import { OpenExternalURL } from "../../wailsjs/go/main/App";

/**
 * The one-prompt install instruction for the Isoform skill. This is a literal
 * technical command handed to an external assistant (Claude Code, Cursor, …),
 * so it is intentionally kept out of i18n — only the surrounding copy is
 * translated. Source of truth: https://github.com/Isoform/yansu-skill
 */
const INSTALL_PROMPT =
    "Fetch https://raw.githubusercontent.com/Isoform/yansu-skill/main/SKILL.md; " +
    "save it under your user-level skills directory as `yansu/SKILL.md`; " +
    "confirm the install path.";

const REPO_URL = "https://github.com/Isoform/yansu-skill";

/**
 * Other AI apps (Settings page id "use-yansu-anywhere"): the "Isoform skill",
 * the bridge that lets the other assistants a person already uses recall what
 * Isoform captured. This is the OUTBOUND counterpart to the Skills and Tool
 * servers pages, which install capabilities *into* Isoform. The everyday card
 * holds the two actions; UseYansuAnywhereDetails (under the page's Advanced)
 * covers what the assistant can recall, examples and the full install prompt.
 */
export function UseYansuAnywhereSettings() {
    const { t } = useTranslation("settings");

    return (
        <SettingsCard
            title={t("useYansuAnywhere.card.title")}
            description={t("useYansuAnywhere.card.description")}
        >
            <div className="flex flex-wrap items-center gap-2">
                <CopyButton
                    text={INSTALL_PROMPT}
                    label={t("useYansuAnywhere.copyPrompt")}
                    copiedLabel={t("useYansuAnywhere.copiedPrompt")}
                    className={buttonVariants({ variant: "default", size: "sm" })}
                    iconClassName="h-3.5 w-3.5"
                />
                <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={() => void OpenExternalURL(REPO_URL)}
                >
                    <Github className="h-3.5 w-3.5" />
                    {t("useYansuAnywhere.viewOnGithub")}
                </Button>
            </div>
        </SettingsCard>
    );
}

/** The Advanced cards for Other AI apps: what another assistant can recall,
 *  example uses and the full text of the install prompt. */
export function UseYansuAnywhereDetails() {
    const { t } = useTranslation("settings");

    const capabilities = [
        t("useYansuAnywhere.capabilities.workflows"),
        t("useYansuAnywhere.capabilities.problems"),
        t("useYansuAnywhere.capabilities.preferences"),
        t("useYansuAnywhere.capabilities.context"),
    ];

    const useCases = [
        {
            host: t("useYansuAnywhere.useCases.claudeCode.host"),
            scenario: t("useYansuAnywhere.useCases.claudeCode.scenario"),
        },
        {
            host: t("useYansuAnywhere.useCases.cursor.host"),
            scenario: t("useYansuAnywhere.useCases.cursor.scenario"),
        },
        {
            host: t("useYansuAnywhere.useCases.codex.host"),
            scenario: t("useYansuAnywhere.useCases.codex.scenario"),
        },
    ];

    return (
        <>
            <SettingsCard
                title={t("useYansuAnywhere.capabilitiesTitle")}
                description={t("useYansuAnywhere.boundaryNote")}
            >
                <ul className="flex flex-col gap-1">
                    {capabilities.map((line) => (
                        <li key={line} className="flex items-start gap-2 settings-row-desc">
                            <Check className="h-3.5 w-3.5 shrink-0 mt-0.5 text-accent" />
                            <span>{line}</span>
                        </li>
                    ))}
                </ul>
            </SettingsCard>

            <SettingsCard title={t("useYansuAnywhere.useCasesTitle")}>
                <ul className="flex flex-col gap-3">
                    {useCases.map((uc) => (
                        <li key={uc.host} className="flex flex-col">
                            <span className="settings-row-title">{uc.host}</span>
                            <span className="settings-row-desc">{uc.scenario}</span>
                        </li>
                    ))}
                </ul>
            </SettingsCard>

            <SettingsCard
                title={t("useYansuAnywhere.promptTitle")}
                description={t("useYansuAnywhere.promptDescription")}
            >
                <p className="rounded-md border border-border bg-background-accent/30 p-3 whitespace-pre-wrap break-words font-mono text-[11px] leading-relaxed text-muted select-text">
                    {INSTALL_PROMPT}
                </p>
            </SettingsCard>
        </>
    );
}
