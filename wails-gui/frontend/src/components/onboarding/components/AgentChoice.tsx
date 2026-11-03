import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { Check, Loader2, RefreshCw } from "lucide-react";
import {
    AcceptManagedModelConsent,
    GetModelAccountsStatus,
    RefreshModelAccounts,
    SetManagedAccountOrder,
    type ModelAccountsStatus,
    type ModelAccountView,
} from "@/lib/wails-shim-app";
import { EventsOn } from "../../../../wailsjs/runtime/runtime";
import { ClaudeCodeAssistantIcon, CodexAssistantIcon } from "../icons";
import { modelGroupDisplayName } from "@/lib/model-group-display";

/** The assistants the routing groups know how to use (see managedAccountProviders in Go). */
export type AssistantId = "claude" | "codex";

const ASSISTANTS: Array<{
    id: AssistantId;
    icon: React.FC<{ className?: string }>;
}> = [
    { id: "claude", icon: ClaudeCodeAssistantIcon },
    { id: "codex", icon: CodexAssistantIcon },
];

const CHAT_GROUP_ID = "yansu-chat";

function isAssistantId(value: string): value is AssistantId {
    return value === "claude" || value === "codex";
}

/**
 * Isoform can think: with the user's own magpie, as soon as it has a routing
 * group or an agent model to address; with the bundled copy, once the chat
 * group has members. The consent note is informational and gates nothing.
 */
export function assistantsReady(status: ModelAccountsStatus | null): boolean {
    if (!status) return false;
    if (status.install?.kind === "user") {
        return (
            status.install.activeGroup !== "" ||
            Object.keys(status.install.agentModels ?? {}).length > 0
        );
    }
    const chat = status.groups.find((group) => group.id === CHAT_GROUP_ID);
    return Boolean(chat && chat.members.length > 0);
}

function capitalize(value: string): string {
    return value ? value.charAt(0).toUpperCase() + value.slice(1) : value;
}

interface AgentChoiceProps {
    /** Called when the choice is applied and the chat group has members. */
    onReady?: (order: AssistantId[]) => void;
    /** Called with the status the gateway returned once the user applied a choice. */
    onApplied?: (status: ModelAccountsStatus) => void;
    /**
     * Open the picker right away even when a choice is already applied, and
     * drop the Back link since there is no ready state to return to. Settings
     * uses it behind its own Change button.
     */
    startChanging?: boolean;
}

/**
 * AgentChoice lets the user pick which signed-in assistant answers first
 * (Claude Code or Codex) and whether the other one steps in as backup. It
 * records the order and the subscription consent in one go, so the routing
 * groups get their members and Isoform can think right away. Used by the
 * onboarding assistant step and by Settings > Assistant to change the order.
 */
export function AgentChoice({ onReady, onApplied, startChanging = false }: AgentChoiceProps) {
    const { t } = useTranslation("onboarding");
    const [status, setStatus] = useState<ModelAccountsStatus | null>(null);
    const [loadError, setLoadError] = useState(false);
    const [busy, setBusy] = useState<"apply" | "refresh" | null>(null);
    const [applyError, setApplyError] = useState("");
    const [primary, setPrimary] = useState<AssistantId | null>(null);
    const [includeBackup, setIncludeBackup] = useState(true);
    const [changing, setChanging] = useState(startChanging);
    const mounted = useRef(false);
    const readyAnnounced = useRef(false);

    const load = useCallback(async () => {
        try {
            const next = await GetModelAccountsStatus();
            if (!mounted.current) return;
            setStatus(next);
            setLoadError(false);
        } catch {
            if (mounted.current) setLoadError(true);
        }
    }, []);

    useEffect(() => {
        mounted.current = true;
        void load();
        const unsubscribe = EventsOn("model-accounts:updated", () => {
            void load();
        });
        return () => {
            mounted.current = false;
            unsubscribe();
        };
    }, [load]);

    const available: ModelAccountView[] = (status?.accounts ?? []).filter(
        (account) => account.on && isAssistantId(account.provider),
    );
    const availableIds = available
        .map((account) => account.provider)
        .filter(isAssistantId);

    // Default to the saved order when it names an available assistant,
    // otherwise to the first one signed in.
    useEffect(() => {
        if (primary !== null || availableIds.length === 0) return;
        const saved = (status?.accountOrder ?? []).find(
            (id): id is AssistantId => isAssistantId(id) && availableIds.includes(id),
        );
        setPrimary(saved ?? availableIds[0]);
        if (status && status.accountOrder.length === 1 && availableIds.length > 1) {
            setIncludeBackup(false);
        }
    }, [availableIds, primary, status]);

    const ready = assistantsReady(status);
    useEffect(() => {
        if (ready && busy !== "apply" && !applyError && !readyAnnounced.current && onReady) {
            readyAnnounced.current = true;
            const order = (status?.accountOrder ?? []).filter(isAssistantId);
            onReady(order);
        }
    }, [applyError, busy, onReady, ready, status]);

    const backupId: AssistantId | null =
        primary && availableIds.length > 1
            ? (availableIds.find((id) => id !== primary) ?? null)
            : null;

    const nameOf = (id: AssistantId) => t(`agent.names.${id}`);

    const apply = async () => {
        if (!status || !primary || busy) return;
        setBusy("apply");
        setApplyError("");
        try {
            const order: AssistantId[] =
                includeBackup && backupId ? [primary, backupId] : [primary];
            let next = await SetManagedAccountOrder(order);
            if (!next.consent.accepted) {
                next = await AcceptManagedModelConsent(next.consent.version);
            }
            if (!mounted.current) return;
            setStatus(next);
            setChanging(false);
            readyAnnounced.current = false;
            onApplied?.(next);
        } catch (error) {
            if (mounted.current) {
                setApplyError(
                    t("agent.applyFailed", {
                        error: error instanceof Error ? error.message : String(error),
                    }),
                );
            }
        } finally {
            if (mounted.current) setBusy(null);
        }
    };

    const refresh = async () => {
        if (busy) return;
        setBusy("refresh");
        try {
            const next = await RefreshModelAccounts();
            if (mounted.current) {
                setStatus(next);
                setLoadError(false);
            }
        } catch {
            if (mounted.current) setLoadError(true);
        } finally {
            if (mounted.current) setBusy(null);
        }
    };

    if (status === null && !loadError) {
        return (
            <div className="flex items-center gap-2 text-[13px] text-muted">
                <Loader2 className="size-4 animate-spin" aria-hidden />
                {t("agent.loading")}
            </div>
        );
    }

    if (loadError) {
        return (
            <div className="flex flex-col gap-6">
                <p className="text-[13px] text-muted">
                    {t("agent.loadFailed")}
                </p>
                <CheckAgainButton busy={busy === "refresh"} onClick={refresh} label={t("agent.checkAgain")} />
            </div>
        );
    }

    // The user's own magpie decides: its routing group is what Isoform
    // thinks with, so there is nothing to choose here.
    if (status && status.install?.kind === "user") {
        const group = status.install.groups.find((entry) => entry.id === status.install.activeGroup);
        return (
            <div
                className="flex flex-col gap-3 rounded-[14px] border border-border bg-panel p-4"
                data-testid="agent-choice-user-magpie"
            >
                <div className="flex items-center gap-3">
                    <span className={`flex size-7 items-center justify-center rounded-full ${ready ? "bg-success/15 text-success" : "bg-warning-bg text-warning"}`}>
                        {ready ? <Check className="size-4 stroke-[2.5]" aria-hidden /> : <Loader2 className="size-4 animate-spin" aria-hidden />}
                    </span>
                    <h4 className="text-[15px] font-medium text-ink">
                        {t("agent.userMagpieHeading")}
                    </h4>
                </div>
                <p className="text-[13px] leading-[20px] text-muted">
                    {t("agent.userMagpieBody")}
                </p>
                {status.install.activeGroup ? (
                    <div className="rounded-[10px] bg-panel dark:bg-background px-3 py-2 text-[13px] text-ink">
                        <div className="font-medium">
                            {t("agent.userMagpieGroup", {
                                group: modelGroupDisplayName(group ?? { id: status.install.activeGroup, name: "" }, t),
                            })}
                        </div>
                        {group && group.members.length > 0 && (
                            <div className="mt-1 text-[12px] text-muted">
                                {t("agent.userMagpieMembers", { members: group.members.join("  →  ") })}
                            </div>
                        )}
                    </div>
                ) : (
                    <p className="text-[12px] text-muted">
                        {t("agent.userMagpieNoGroup")}
                    </p>
                )}
                {!ready && (
                    <CheckAgainButton busy={busy === "refresh"} onClick={refresh} label={t("agent.checkAgain")} />
                )}
            </div>
        );
    }

    if (status && !status.sidecar.healthy && available.length === 0) {
        return (
            <div className="flex flex-col gap-6">
                <div className="flex items-center gap-2 text-[13px] text-muted">
                    <Loader2 className="size-4 animate-spin" aria-hidden />
                    {t("agent.starting")}
                </div>
                <CheckAgainButton busy={busy === "refresh"} onClick={refresh} label={t("agent.checkAgain")} />
            </div>
        );
    }

    if (available.length === 0) {
        return (
            <div className="flex flex-col gap-6" data-testid="agent-choice-empty">
                <div className="flex flex-col gap-2">
                    <h4 className="text-[16px] font-medium text-ink">
                        {t("agent.noneHeading")}
                    </h4>
                    <p className="text-[13px] leading-[20px] text-muted">
                        {t("agent.noneBody")}
                    </p>
                </div>
                <div className="rounded-[12px] border border-border bg-panel px-4 py-3 text-[13px] leading-[22px] text-ink">
                    <div>{t("agent.noneHintClaude")}</div>
                    <div>{t("agent.noneHintCodex")}</div>
                </div>
                <div className="flex flex-wrap items-center gap-4">
                    <CheckAgainButton busy={busy === "refresh"} onClick={refresh} label={t("agent.checkAgain")} />
                    <span className="text-[12px] text-muted">
                        {t("agent.ownKey")}
                    </span>
                </div>
            </div>
        );
    }

    if (ready && !changing && busy !== "apply" && !applyError) {
        const order = (status?.accountOrder ?? []).filter(isAssistantId);
        const first = order[0] ?? primary ?? availableIds[0];
        const backup = order.length > 1 ? order[1] : null;
        return (
            <div className="space-y-3">
                <div
                    className="flex items-center justify-between gap-4 rounded-[12px] border border-success-border bg-panel px-4 py-3"
                    data-testid="agent-choice-ready"
                >
                    <div className="flex items-center gap-3">
                        <span className="flex size-7 items-center justify-center rounded-full bg-panel-warm text-success">
                            <Check className="size-4 stroke-[2.5]" aria-hidden />
                        </span>
                        <p className="text-[14px] text-ink">
                            {backup
                                ? t("agent.readyWithBackup", {
                                      primary: nameOf(first),
                                      backup: nameOf(backup),
                                  })
                                : t("agent.ready", { primary: nameOf(first) })}
                        </p>
                    </div>
                    <button
                        type="button"
                        onClick={() => setChanging(true)}
                        className="text-[13px] text-muted hover:text-ink transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent-2/50 rounded-sm"
                    >
                        {t("agent.change")}
                    </button>
                </div>
            </div>
        );
    }

    return (
        <div className="flex flex-col gap-6">
            <div
                role="radiogroup"
                aria-label={t("agent.heading")}
                className="grid grid-cols-1 gap-3 sm:grid-cols-2"
            >
                {ASSISTANTS.filter((item) => availableIds.includes(item.id)).map((item) => {
                    const account = available.find((entry) => entry.provider === item.id);
                    const selected = primary === item.id;
                    const Icon = item.icon;
                    return (
                        <button
                            key={item.id}
                            type="button"
                            role="radio"
                            aria-checked={selected}
                            data-testid={`agent-card-${item.id}`}
                            onClick={() => setPrimary(item.id)}
                            className={`flex items-start gap-3 rounded-[14px] border p-4 text-left transition-all focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-2/40 ${
                                selected
                                    ? "border-accent bg-surface-active shadow-[var(--shadow-soft)]"
                                    : "border-border bg-panel hover:border-border-strong"
                            }`}
                        >
                            <Icon className={`size-10 shrink-0 ${selected ? "text-accent" : "text-muted"}`} />
                            <span className="flex min-w-0 flex-1 flex-col gap-1">
                                <span className="flex items-center justify-between gap-2">
                                    <span className="text-[15px] font-medium text-ink">
                                        {nameOf(item.id)}
                                    </span>
                                    <span
                                        className={`text-[11px] font-medium uppercase tracking-wide ${
                                            selected ? "text-accent" : "text-muted dark:text-muted/60"
                                        }`}
                                    >
                                        {selected ? t("agent.primary") : t("agent.backup")}
                                    </span>
                                </span>
                                {account?.plan && (
                                    <span className="text-[12px] text-muted">
                                        {t("agent.plan", { plan: capitalize(account.plan) })}
                                    </span>
                                )}
                                {account?.user && (
                                    <span className="truncate text-[12px] text-muted">
                                        {account.user}
                                    </span>
                                )}
                            </span>
                        </button>
                    );
                })}
            </div>

            {backupId && (
                <label className="flex items-center gap-2 text-[13px] text-ink">
                    <input
                        type="checkbox"
                        checked={includeBackup}
                        onChange={(event) => setIncludeBackup(event.target.checked)}
                        className="size-4 accent-[var(--color-accent)]"
                    />
                    {t("agent.useAsBackup", { name: nameOf(backupId) })}
                </label>
            )}

            <p className="text-[12px] leading-[20px] text-muted">
                {t("agent.consent")}
            </p>

            {applyError && (
                <p role="alert" className="text-[12px] text-destructive">
                    {applyError}
                </p>
            )}

            <div className="flex flex-wrap items-center gap-3">
                <button
                    type="button"
                    data-testid="agent-apply"
                    disabled={!primary || busy !== null}
                    onClick={() => void apply()}
                    className="relative h-10 px-6 rounded-[12px] patina-verdigris bg-solid-btn-bg text-solid-btn-fg text-[14px] font-medium tracking-[-0.1504px] leading-5 shadow-[var(--shadow-solid-btn)] transition-opacity hover:opacity-90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-2/40 focus-visible:ring-offset-2 focus-visible:ring-offset-background cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed inline-flex items-center gap-2"
                >
                    {busy === "apply" && <Loader2 className="size-4 animate-spin" aria-hidden />}
                    {busy === "apply"
                        ? t("agent.applying")
                        : primary
                          ? t("agent.apply", { name: nameOf(primary) })
                          : t("agent.applyGeneric")}
                </button>
                {changing && !startChanging && (
                    <button
                        type="button"
                        onClick={() => setChanging(false)}
                        className="text-[13px] text-muted hover:text-ink transition-colors"
                    >
                        {t("actions.back")}
                    </button>
                )}
            </div>
        </div>
    );
}

function CheckAgainButton({
    busy,
    onClick,
    label,
}: {
    busy: boolean;
    onClick: () => void;
    label: string;
}) {
    return (
        <button
            type="button"
            data-testid="agent-check-again"
            disabled={busy}
            onClick={onClick}
            className="inline-flex h-9 items-center gap-2 rounded-[10px] border border-border px-4 text-[13px] text-ink transition-colors hover:border-border-strong disabled:opacity-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-2/40"
        >
            {busy ? (
                <Loader2 className="size-4 animate-spin" aria-hidden />
            ) : (
                <RefreshCw className="size-4" aria-hidden />
            )}
            {label}
        </button>
    );
}
