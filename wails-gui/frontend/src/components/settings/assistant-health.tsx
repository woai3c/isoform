import { useCallback, useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { GetAssistantHealth, RetryAssistantHealth, type AssistantHealthView } from "@/lib/wails-shim-app";
import { EventsOn } from "../../../wailsjs/runtime/runtime";
import { Button } from "../ui/button";
import { BriefSignIn } from "../today/BriefSignIn";

// Emitted by the desktop whenever an assistant is paused, checked or resumed.
const HEALTH_CHANGED_EVENT = "assistant-health:changed";
// Emitted when accounts are refreshed or the assistant order changes.
const AI_CONFIG_CHANGED_EVENT = "ai:config-changed";

// States "Try again" cannot change: nothing is wrong, or the limit is the
// product's (Codex) or the account's (usage) and clears on its own.
const NO_RETRY = new Set(["ready", "tools_unverified", "usage_limit"]);

export function canRetryHealth(code: string): boolean {
    return !NO_RETRY.has(code);
}

function assistantName(id: string, t: TFunction<"settings">): string {
    return id === "codex" ? t("assistantHealth.codex") : t("assistantHealth.claudeCode");
}

export function healthStatusText(line: AssistantHealthView, t: TFunction<"settings">): string {
    if (line.checking) return t("assistantHealth.checking");
    switch (line.code) {
        case "ready":
            return t("assistantHealth.ready");
        case "signed_out":
            return t("assistantHealth.signedOut");
        case "sign_in_check_failed":
            return t("assistantHealth.signInCheckFailed");
        case "not_installed":
            return t("assistantHealth.notInstalled");
        case "unsupported_version":
            return t("assistantHealth.unsupportedVersion");
        case "tools_unverified":
            return t("assistantHealth.toolsUnverified");
        case "tool_connection":
            return t("assistantHealth.toolConnection");
        case "check_failed":
            return t("assistantHealth.checkFailed");
        case "repeated_errors":
            return t("assistantHealth.repeatedErrors");
        case "usage_limit":
            return t("assistantHealth.usageLimit");
        default:
            return t("assistantHealth.unavailable");
    }
}

export function healthWhenText(line: AssistantHealthView, t: TFunction<"settings">, locale: string): string | null {
    if (line.checking || !line.retryAt) return null;
    const time = Date.parse(line.retryAt);
    if (Number.isNaN(time) || time <= Date.now()) return null;
    const formatted = new Intl.DateTimeFormat(locale, { hour: "numeric", minute: "2-digit" }).format(time);
    if (line.code === "usage_limit") return t("assistantHealth.backAt", { time: formatted });
    if (line.code === "check_failed" || line.code === "repeated_errors") return t("assistantHealth.nextCheck", { time: formatted });
    return null;
}

/** Each assistant's health, kept current, with Try again. */
export function useAssistantHealth() {
    const [lines, setLines] = useState<AssistantHealthView[]>([]);
    const [retrying, setRetrying] = useState<string | null>(null);
    const [retryFailed, setRetryFailed] = useState(false);

    const load = useCallback(() => {
        GetAssistantHealth()
            .then((next) => setLines(next ?? []))
            .catch(() => {
                // Without health, Settings shows the order alone; nothing is invented.
            });
    }, []);

    useEffect(() => {
        load();
        const offs = [EventsOn(HEALTH_CHANGED_EVENT, load), EventsOn(AI_CONFIG_CHANGED_EVENT, load)];
        return () => offs.forEach((off) => off());
    }, [load]);

    const retry = (assistant: string) => {
        setRetrying(assistant);
        setRetryFailed(false);
        RetryAssistantHealth(assistant)
            .then((next) => setLines(next ?? []))
            .catch(() => setRetryFailed(true))
            .finally(() => setRetrying(null));
    };

    return { lines, retrying, retryFailed, retry };
}

/** One line per assistant Isoform can route to: whether it answers now, and why not. */
export function AssistantHealthLines() {
    const { t, i18n } = useTranslation("settings");
    const { lines, retrying, retryFailed, retry } = useAssistantHealth();

    if (lines.length === 0) return null;
    return (
        <div className="flex flex-col pt-3" data-testid="assistant-health">
            <p className="text-xs font-medium text-ink">{t("assistantHealth.title")}</p>
            <ul className="flex flex-col">
                {lines.map((line) => {
                    const when = healthWhenText(line, t, i18n.language);
                    const busy = retrying === line.assistant || line.checking;
                    return (
                        <li
                            key={line.assistant}
                            className="flex items-center justify-between gap-3 border-b border-border py-2 text-xs last:border-b-0"
                        >
                            <span className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                                <span className="font-medium text-ink">{assistantName(line.assistant, t)}</span>
                                {line.code === "signed_out" && <span aria-hidden="true" className="text-muted">·</span>}
                                <span className="text-muted">{healthStatusText(line, t)}</span>
                                {line.code === "signed_out" && <span aria-hidden="true" className="text-muted">·</span>}
                                {line.code === "signed_out" && <BriefSignIn assistant={line.assistant} size="12px" />}
                                {when && <span className="text-muted">{when}</span>}
                            </span>
                            {canRetryHealth(line.code) && (
                                <Button
                                    type="button"
                                    variant="outline"
                                    size="sm"
                                    disabled={busy}
                                    onClick={() => retry(line.assistant)}
                                >
                                    {busy && <Loader2 className="size-3.5 animate-spin" />}
                                    {t("assistantHealth.tryAgain")}
                                </Button>
                            )}
                        </li>
                    );
                })}
            </ul>
            {retryFailed && (
                <p role="alert" className="text-xs text-muted">
                    {t("assistantHealth.tryAgainFailed")}
                </p>
            )}
        </div>
    );
}
