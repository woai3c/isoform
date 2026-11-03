import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Check, Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
    GetAssistantChoice,
    OpenAssistantSignIn,
    SetAgentEffort,
    SetAgentModel,
    SetAnsweringAgent,
    SetBackgroundUsesChoice,
    type AgentChoiceView,
    type AssistantChoiceView,
    type AssistantHealthView,
} from "@/lib/wails-shim-app";
import { cn } from "@/lib/utils";
import { EventsOn } from "../../../wailsjs/runtime/runtime";
import { Button } from "../ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { Switch } from "../ui/switch";
import { canRetryHealth, healthStatusText, useAssistantHealth } from "./assistant-health";
import { SettingsCard, SettingsRow } from "./settings-primitives";

// Radix Select takes no empty value; this stands for the agent's own default.
const AUTOMATIC = "__automatic__";
const AI_CONFIG_CHANGED_EVENT = "ai:config-changed";
const SAVED_MS = 2000;
const EFFORT_STEPS = ["low", "medium", "high", "xhigh", "max"] as const;

function agentName(agent: string, t: TFunction<"settings">): string {
    switch (agent) {
        case "claude-code":
            return t("assistantChoice.agents.claudeCode");
        case "codex":
            return t("assistantChoice.agents.codex");
        case "deepseek":
            return t("assistantChoice.agents.deepseek");
        default:
            return t("assistantChoice.agents.isoform");
    }
}

function effortName(effort: string, t: TFunction<"settings">): string {
    switch (effort) {
        case "low":
            return t("assistantChoice.efforts.low");
        case "medium":
            return t("assistantChoice.efforts.medium");
        case "high":
            return t("assistantChoice.efforts.high");
        case "xhigh":
            return t("assistantChoice.efforts.xhigh");
        default:
            return t("assistantChoice.efforts.max");
    }
}

function backgroundText(agent: string, t: TFunction<"settings">): string {
    switch (agent) {
        case "claude-code":
            return t("assistantChoice.background.description.claudeCode");
        case "codex":
            return t("assistantChoice.background.description.codex");
        default:
            return t("assistantChoice.background.description.isoform");
    }
}

/**
 * A row's state in plain words. Whether it can be picked comes from Isoform
 * (canAnswer), never from health, so a health line that is missing, loading
 * or failed never makes Codex pickable. DeepSeek Harness has no health line:
 * Isoform says whether its CLI is installed.
 */
function rowState(choice: AgentChoiceView, line: AssistantHealthView | undefined, t: TFunction<"settings">, isoformReady: boolean): string {
    switch (choice.agent) {
        case "isoform":
            return isoformReady ? t("assistantChoice.state.alwaysAvailable") : t("assistantChoice.state.isoformUnavailable");
        case "codex":
            if (!choice.canAnswer) return t("assistantChoice.state.toolsUnverified");
            break;
        case "deepseek":
            if (choice.notInstalled) return t("assistantChoice.state.notInstalled");
            if (!choice.canAnswer) return t("assistantChoice.state.toolsUnverified");
            break;
    }
    if (!line) return "";
    if (line.checking) return t("assistantChoice.state.checking");
    switch (line.code) {
        case "ready":
            return t("assistantChoice.state.ready");
        case "signed_out":
            return t("assistantChoice.state.signedOut");
        case "not_installed":
            return t("assistantChoice.state.notInstalled");
        case "tools_unverified":
            return t("assistantChoice.state.toolsUnverified");
        default:
            // Paused, with its reason.
            return healthStatusText(line, t);
    }
}

/** The effort steps for the picked agent's model, in order: what magpie lists for it, else the agent's own. */
function effortStepsFor(choice: AgentChoiceView): string[] {
    const steps = choice.efforts ?? [];
    return EFFORT_STEPS.filter((step) => steps.includes(step));
}

/** Whether an agent has a model or effort to choose; DeepSeek Harness has neither yet. */
function hasSettings(choice: AgentChoiceView): boolean {
    return choice.models.length > 0 || (choice.efforts ?? []).length > 0;
}

/**
 * Settings > Assistant, "Who answers in Isoform": one pick with Isoform as the
 * backup, and the picked agent's model and effort. Every change saves at
 * once. The choices apply only to Isoform's runs.
 */
export function AssistantChoiceCard({ footer, profileModels = false }: { footer?: ReactNode; profileModels?: boolean }) {
    const { t } = useTranslation("settings");
    const [view, setView] = useState<AssistantChoiceView | null>(null);
    const [busy, setBusy] = useState(false);
    const [failed, setFailed] = useState(false);
    const [signInFailed, setSignInFailed] = useState(false);
    const [saved, setSaved] = useState(false);
    const savedTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
    const health = useAssistantHealth();

    const load = useCallback(() => {
        GetAssistantChoice()
            .then((next) => setView(next))
            .catch(() => setFailed(true));
    }, []);

    useEffect(() => {
        load();
        const off = EventsOn(AI_CONFIG_CHANGED_EVENT, load);
        const offModels = EventsOn("model-accounts:updated", load);
        return () => {
            off();
            offModels();
            if (savedTimer.current) clearTimeout(savedTimer.current);
        };
    }, [load]);

    const apply = (save: Promise<AssistantChoiceView>) => {
        setBusy(true);
        setFailed(false);
        setSaved(false);
        save.then((next) => {
            setView(next);
            setSaved(true);
            if (savedTimer.current) clearTimeout(savedTimer.current);
            savedTimer.current = setTimeout(() => setSaved(false), SAVED_MS);
        })
            .catch(() => {
                setFailed(true);
                load();
            })
            .finally(() => setBusy(false));
    };

    const signIn = (agent: string) => {
        setSignInFailed(false);
        OpenAssistantSignIn(agent).catch(() => setSignInFailed(true));
    };

    if (!view) {
        return (
            <SettingsCard title={t("assistantChoice.title")}>
                {failed ? (
                    <p role="alert" className="text-xs text-muted">{t("assistantChoice.loadFailed")}</p>
                ) : (
                    <p role="status" className="settings-row-desc">{t("assistantChoice.loading")}</p>
                )}
                {footer}
            </SettingsCard>
        );
    }

    const picked = view.agents.find((choice) => choice.agent === view.answering) ?? view.agents[0];
    const pickedName = picked ? agentName(picked.agent, t) : "";
    const steps = picked ? effortStepsFor(picked) : [];
    const modelsFor = (choice: AgentChoiceView) => choice.model && !choice.models.includes(choice.model) ? [choice.model, ...choice.models] : choice.models;
    const models = picked ? modelsFor(picked) : [];

    const row = (choice: AgentChoiceView) => {
        const name = agentName(choice.agent, t);
        const line = health.lines.find((item) => item.assistant === choice.agent);
        const state = { text: rowState(choice, line, t, view.isoformReady), pickable: choice.canAnswer };
        const selected = view.answering === choice.agent;
        const retrying = line ? health.retrying === line.assistant || line.checking : false;
        let action: ReactNode = null;
        if (line?.code === "signed_out") {
            action = (
                <Button type="button" variant="outline" size="sm" onClick={() => signIn(choice.agent)}>
                    {t("assistantChoice.signIn")}
                </Button>
            );
        } else if (state.pickable && line && line.code !== "ready" && line.code !== "not_installed" && canRetryHealth(line.code)) {
            action = (
                <Button type="button" variant="outline" size="sm" disabled={retrying} onClick={() => health.retry(line.assistant)}>
                    {retrying && <Loader2 className="size-3.5 animate-spin" />}
                    {t("assistantHealth.tryAgain")}
                </Button>
            );
        }
        return (
            <div
                key={choice.agent}
                className={cn(
                    "flex items-start justify-between gap-3 rounded-lg border px-3 py-2.5 transition-colors",
                    selected ? "border-accent-2/60 bg-accent-2/5" : "border-border",
                )}
            >
                <label className={cn("flex min-w-0 flex-1 items-start gap-2", state.pickable ? "cursor-pointer" : "cursor-not-allowed")}>
                    <input
                        type="radio"
                        name="assistant-answering"
                        value={choice.agent}
                        checked={selected}
                        disabled={busy || (!state.pickable && !selected)}
                        onChange={() => {
                            if (state.pickable) apply(SetAnsweringAgent(choice.agent));
                        }}
                        className="mt-1 accent-accent-2"
                    />
                    <span className="flex min-w-0 flex-col gap-0.5">
                        <span className="text-[13px] font-medium text-ink">{name}</span>
                        {state.text && <span className="text-xs text-muted">{state.text}</span>}
                    </span>
                </label>
                {action}
            </div>
        );
    };

    return (
        <SettingsCard
            title={t("assistantChoice.title")}
            headerRight={
                saved ? (
                    <span role="status" className="flex items-center gap-1 text-xs text-muted">
                        <Check aria-hidden className="size-3.5 text-accent-2" />
                        {t("assistantChoice.saved")}
                    </span>
                ) : undefined
            }
        >
            <div className="flex flex-col gap-2" data-testid="assistant-choice">
                <div className="flex flex-col gap-2" role="radiogroup" aria-label={t("assistantChoice.title")}>
                    {view.agents.map(row)}
                </div>
                <p className="text-xs text-muted">{t("assistantChoice.backupNote")}</p>
                {profileModels && <p className="text-xs text-muted">{t("magpieProfile.executorOrder")}</p>}
                {picked && (
                    <div className="flex flex-col">
                        {!profileModels && <SettingsRow
                            title={t("assistantChoice.model")}
                            description={picked.agent === "isoform" && view.reason === "own_key" ? t("assistantChoice.reason.ownKey") : undefined}
                            control={
                                <Select
                                    value={picked.model || AUTOMATIC}
                                    disabled={busy || (picked.agent === "isoform" && view.reason === "own_key")}
                                    onValueChange={(value) => apply(SetAgentModel(picked.agent, value === AUTOMATIC ? "" : value))}
                                >
                                    <SelectTrigger className="w-[240px] text-xs" aria-label={t("assistantChoice.modelFor", { agent: pickedName })}>
                                        <SelectValue />
                                    </SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value={AUTOMATIC} className="text-xs">
                                            {t("assistantChoice.modelAutomatic")}
                                        </SelectItem>
                                        {models.map((model) => (
                                            <SelectItem key={model} value={model} className="text-xs">
                                                {model}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            }
                        />}
                        {!profileModels && steps.length > 0 && (
                            <SettingsRow
                                title={t("assistantChoice.effort")}
                                description={t("assistantChoice.effortHint")}
                                control={
                                    <div className="settings-segment" role="radiogroup" aria-label={t("assistantChoice.effortFor", { agent: pickedName })}>
                                        {steps.map((step) => (
                                            <button
                                                key={step}
                                                type="button"
                                                role="radio"
                                                aria-checked={picked.effort === step}
                                                data-active={picked.effort === step}
                                                disabled={busy}
                                                onClick={() => apply(SetAgentEffort(picked.agent, step))}
                                            >
                                                {effortName(step, t)}
                                            </button>
                                        ))}
                                    </div>
                                }
                            />
                        )}
                        {!profileModels && <SettingsRow
                            title={t("assistantChoice.background.title")}
                            description={backgroundText(picked.agent, t)}
                            control={
                                <Switch
                                    checked={view.backgroundUsesChoice}
                                    disabled={busy}
                                    aria-label={t("assistantChoice.background.title")}
                                    onCheckedChange={(on) => apply(SetBackgroundUsesChoice(on))}
                                />
                            }
                        />}
                    </div>
                )}
                {!profileModels && view.agents.filter((choice) => choice.agent !== picked?.agent && choice.agent !== "isoform" && hasSettings(choice)).map((choice) => (
                    <div key={choice.agent} className="flex flex-col">
                        <p className="text-xs font-medium text-muted">{t("assistantChoice.settingsFor", { agent: agentName(choice.agent, t) })}</p>
                        <SettingsRow
                            title={t("assistantChoice.model")}
                            control={
                                <Select value={choice.model || AUTOMATIC} disabled={busy} onValueChange={(value) => apply(SetAgentModel(choice.agent, value === AUTOMATIC ? "" : value))}>
                                    <SelectTrigger className="w-[240px] text-xs" aria-label={t("assistantChoice.modelFor", { agent: agentName(choice.agent, t) })}><SelectValue /></SelectTrigger>
                                    <SelectContent>
                                        <SelectItem value={AUTOMATIC} className="text-xs">{t("assistantChoice.modelAutomatic")}</SelectItem>
                                        {modelsFor(choice).map((model) => <SelectItem key={model} value={model} className="text-xs">{model}</SelectItem>)}
                                    </SelectContent>
                                </Select>
                            }
                        />
                        {effortStepsFor(choice).length > 0 && <SettingsRow
                            title={t("assistantChoice.effort")}
                            control={<div className="settings-segment" role="radiogroup" aria-label={t("assistantChoice.effortFor", { agent: agentName(choice.agent, t) })}>
                                {effortStepsFor(choice).map((step) => <button key={step} type="button" role="radio" aria-checked={choice.effort === step} data-active={choice.effort === step} disabled={busy} onClick={() => apply(SetAgentEffort(choice.agent, step))}>{effortName(step, t)}</button>)}
                            </div>}
                        />}
                    </div>
                ))}
                <p className="text-xs text-muted">{t("assistantChoice.scopeNote")}</p>
                {(failed || health.retryFailed || signInFailed) && (
                    <p role="alert" className="text-xs text-muted">
                        {failed
                            ? t("assistantChoice.saveFailed")
                            : signInFailed
                              ? t("assistantChoice.signInFailed")
                              : t("assistantHealth.tryAgainFailed")}
                    </p>
                )}
            </div>
            {footer}
        </SettingsCard>
    );
}
