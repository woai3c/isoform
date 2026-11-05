import { Fragment, useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useSettingsDevTools } from "@/components/settings/settings-dev-tools";
import { CheckCircle2, Copy, Loader2, RefreshCw } from "lucide-react";
import { activeInterval } from "@/lib/app-activity";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import {
    AcceptManagedModelConsent,
    GetModelAccountsStatus,
    RefreshModelAccounts,
    RevokeManagedModelConsent,
    SetAIProviderMode,
    type ModelAccountView,
    type ModelAccountWindowView,
    type ModelAccountsStatus,
    type ModelGroupView,
    type ModelSidecarView,
} from "@/lib/wails-shim-app";
import { modelGroupDisplayName } from "@/lib/model-group-display";
import { usageWindowLimit } from "@/lib/usage-window";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { ConfirmDialog } from "./confirm-dialog";
import { type AssistantId } from "./onboarding/components/AgentChoice";
import { AssistantChoiceCard } from "./settings/assistant-choice";
import { MagpieProfileCard } from "./settings/magpie-profile";
import { AssistantHealthLines } from "./settings/assistant-health";
import { SettingsAdvanced } from "./settings/settings-advanced";
import { SettingsCard, SettingsRow } from "./settings/settings-primitives";
import { ToolsOnThisMacCard } from "./settings/tools-on-this-mac";
import { Button } from "./ui/button";
import { Switch } from "./ui/switch";

// Settings > Assistant. The everyday cards say who Isoform thinks with, how
// much usage is left and what is installed on this Mac; a problem line shows
// only when something is wrong. Everything technical (the user's own key or
// model, routing, gateway status) sits under one Advanced disclosure.

const PAGE_ID = "ai-provider";
const CONSENT_VERSION = 1;
const CLOCK_TICK_MS = 60_000;
// Go zero time ("0001-01-01T00:00:00Z") marks "never"; anything before 2000 is treated as unset.
const EARLIEST_TIMESTAMP = Date.UTC(2000, 0, 1);
const ROUTING_KEYS = ["chat", "background", "light"] as const;
// Member ids or provider prefixes the routing groups may use for the registered API key.
const BYOK_MEMBER_IDS = new Set(["byok", "api_key", "api-key", "yansu-byok"]);
// Isoform's own routing group in either magpie (magpieIsoformGroupID in magpie_bootstrap.go).
const ISOFORM_GROUP_ID = "isoform";

type RoutingKey = (typeof ROUTING_KEYS)[number];
type ErrorScope = "load" | "accept" | "revoke" | "mode" | "refresh";
type BusyAction = "accept" | "revoke" | "refresh" | "mode";
type SidecarState = "starting" | "ready" | "degraded" | "unavailable" | "inactive";

interface WindowRow {
    key: string;
    label: string;
    percent: number | null;
    resets: string | null;
    // The window's reset time has passed, so the stored numbers are stale until the next read.
    refreshing: boolean;
}

/** One model or assistant Isoform asks, in order. */
interface ThinksWithEntry {
    key: string;
    name: string;
    detail: string | null;
}

interface Problem {
    message: string;
    busy: boolean;
    retry: () => void;
}

function isAssistantId(value: string): value is AssistantId {
    return value === "claude" || value === "codex";
}

function capitalize(value: string): string {
    return value ? value.charAt(0).toUpperCase() + value.slice(1) : value;
}

function parseTimestamp(value: string): number | null {
    if (!value) return null;
    const time = Date.parse(value);
    if (Number.isNaN(time) || time < EARLIEST_TIMESTAMP) return null;
    return time;
}

function formatReset(value: string, now: number, locale: string): string | null {
    const time = parseTimestamp(value);
    if (time === null) return null;
    const seconds = Math.round((time - now) / 1000);
    const formatter = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
    const absolute = Math.abs(seconds);
    if (absolute < 60) return formatter.format(seconds, "second");
    if (absolute < 3_600) return formatter.format(Math.round(seconds / 60), "minute");
    if (absolute < 86_400) return formatter.format(Math.round(seconds / 3_600), "hour");
    return formatter.format(Math.round(seconds / 86_400), "day");
}

/** Share of a quota window that is still left, from used and remaining amounts in the same unit. */
function remainingPercent(quota: ModelAccountWindowView): number | null {
    const total = quota.used + quota.remaining;
    if (!Number.isFinite(total) || total <= 0) return null;
    return Math.min(100, Math.max(0, Math.round((quota.remaining / total) * 100)));
}

function sidecarStateOf(sidecar: ModelSidecarView, managed: boolean): SidecarState {
    if (!sidecar.available) return "unavailable";
    // The sidecar is stopped on purpose outside managed mode, so its run state means nothing there.
    if (!managed) return "inactive";
    if (sidecar.degraded) return "degraded";
    if (sidecar.running && sidecar.healthy) return "ready";
    return "starting";
}

/** "kimi-code" -> "Kimi Code". */
export function titleWords(value: string): string {
    return value.split(/[-_\s]+/).filter(Boolean).map(capitalize).join(" ");
}

const PLAN_TIERS = new Set(["free", "plus", "pro", "max", "team", "business", "enterprise"]);

/** settings-review ST70: "max" -> "Max plan", "kimi-code" -> "Kimi Code"; never the raw label or the account email. */
export function planDisplayName(plan: string, planName: (plan: string) => string): string {
    const words = plan.trim().split(/\s+/);
    const tier = words[0]?.toLowerCase() ?? "";
    if (words.length === 1 && PLAN_TIERS.has(tier)) return planName(capitalize(tier));
    return titleWords(plan.trim());
}

function accountDetail(account: ModelAccountView, planName: (plan: string) => string): string | null {
    const plan = account.plan?.trim() ?? "";
    if (!/^(?:free|plus|pro|max|team|business|enterprise)(?:\s+\d+x)?$/i.test(plan) && plan.toLowerCase() !== "kimi-code") return null;
    return planDisplayName(plan, planName);
}

/** Splits a magpie model id ("claude/claude-sonnet-5-5") into its provider prefix and model. */
function splitMember(member: string): { prefix: string; model: string | null } {
    const slash = member.indexOf("/");
    if (slash < 0) return { prefix: member.trim().toLowerCase(), model: null };
    return { prefix: member.slice(0, slash).trim().toLowerCase(), model: member.slice(slash + 1) };
}

function ProblemLine({ problem, retryLabel }: { problem: Problem; retryLabel: string }) {
    return (
        <div className="flex flex-wrap items-center gap-3 rounded-lg bg-ink/[0.03] px-3 py-2">
            <p role="alert" className="min-w-0 flex-1 text-xs text-ink">
                {problem.message}
            </p>
            <Button type="button" variant="outline" size="sm" disabled={problem.busy} onClick={problem.retry}>
                {problem.busy && <Loader2 className="size-3.5 animate-spin" />}
                {retryLabel}
            </Button>
        </div>
    );
}

function AccountRow({
    title,
    detail,
    trailing,
    windows,
    balance,
}: {
    title: string;
    detail: string | null;
    trailing: string | null;
    windows: WindowRow[];
    balance: string | null;
}) {
    const { t } = useTranslation("settings");
    return (
        <div className="settings-row">
            <div className="flex min-w-0 flex-1 flex-col gap-1">
                <span className="settings-row-title break-words">{title}</span>
                {detail && <span className="settings-row-desc break-words">{detail}</span>}
                {windows.map((row) => (
                    <div key={row.key} className="mt-1 flex flex-col gap-1">
                        <div className="flex flex-wrap items-baseline justify-between gap-x-3 text-xs">
                            <span className="text-ink">{row.label}</span>
                            <span className="flex flex-wrap gap-x-2 tabular-nums text-muted">
                                {row.refreshing ? (
                                    <span>{t("modelAccounts.windowRefreshing")}</span>
                                ) : (
                                    <>
                                        {row.percent !== null && (
                                            <span>
                                                {t("modelAccounts.windowRemaining", { percent: row.percent })}
                                            </span>
                                        )}
                                        {row.resets !== null && (
                                            <span>{t("modelAccounts.windowResets", { time: row.resets })}</span>
                                        )}
                                    </>
                                )}
                            </span>
                        </div>
                        {row.percent !== null && (
                            <div
                                role="progressbar"
                                aria-label={row.label}
                                aria-valuemin={0}
                                aria-valuemax={100}
                                aria-valuenow={row.percent}
                                className="h-1 w-full overflow-hidden rounded-full bg-ink/[0.06]"
                            >
                                <div
                                    className="h-full rounded-full bg-accent-2/70 transition-[width] duration-300"
                                    style={{ width: `${row.percent}%` }}
                                />
                            </div>
                        )}
                    </div>
                ))}
                {balance && (
                    <span className="settings-row-desc break-words">
                        {t("modelAccounts.balance", { amount: balance })}
                    </span>
                )}
            </div>
            {trailing && <span className="shrink-0 text-xs text-muted">{trailing}</span>}
        </div>
    );
}

function OrderedEntries({ entries }: { entries: ThinksWithEntry[] }) {
    const { t } = useTranslation("settings");
    return (
        <ol data-testid="thinks-with" className="flex flex-col">
            {entries.map((entry, index) => (
                <li
                    key={entry.key}
                    className="flex items-baseline justify-between gap-3 border-b border-border py-2 text-xs last:border-b-0"
                >
                    <span className="flex min-w-0 flex-wrap items-baseline gap-x-2">
                        {index === 0 && (
                            <CheckCircle2 aria-hidden className="size-3.5 translate-y-0.5 text-accent-2" />
                        )}
                        <span className="font-medium text-ink">{entry.name}</span>
                        {entry.detail && <span className="break-all text-muted">{entry.detail}</span>}
                    </span>
                    <span className="shrink-0 text-muted">
                        {index === 0 ? t("modelAccounts.thinksWithPrimary") : t("modelAccounts.thinksWithBackup")}
                    </span>
                </li>
            ))}
        </ol>
    );
}

export function ModelAccountsSettings({ ownKeyForm }: { ownKeyForm: ReactNode }) {
    const { t, i18n } = useTranslation(["settings", "onboarding"]);
    const devTools = useSettingsDevTools();
    const locale = i18n.resolvedLanguage ?? i18n.language;
    const tOnboarding = i18n.getFixedT(locale, "onboarding");

    const [status, setStatus] = useState<ModelAccountsStatus | null>(null);
    const [errors, setErrors] = useState<Partial<Record<ErrorScope, string>>>({});
    const [busy, setBusy] = useState<BusyAction | null>(null);
    const [consentDeferred, setConsentDeferred] = useState(false);
    const [withdrawOpen, setWithdrawOpen] = useState(false);
    const [now, setNow] = useState(() => Date.now());

    const mounted = useRef(false);
    // Bumped by every read or write; a read whose version is stale when it resolves is dropped.
    const readVersion = useRef(0);
    const busyRef = useRef(false);

    const setError = useCallback((scope: ErrorScope, message?: string) => {
        setErrors((current) => ({ ...current, [scope]: message }));
    }, []);

    const load = useCallback(async () => {
        const version = ++readVersion.current;
        try {
            const next = await GetModelAccountsStatus();
            if (mounted.current && version === readVersion.current) {
                setStatus(next);
                setError("load");
            }
        } catch {
            if (mounted.current && version === readVersion.current) {
                setError("load", t("modelAccounts.loadFailed"));
            }
        }
    }, [setError, t]);

    useEffect(() => {
        mounted.current = true;
        void load();
        const unsubscribeUpdated = EventsOn("model-accounts:updated", () => {
            void load();
        });
        // A key saved under Advanced, or a mode change, reshapes the whole page.
        const unsubscribeConfig = EventsOn("ai:config-changed", () => {
            void load();
        });
        return () => {
            mounted.current = false;
            readVersion.current += 1;
            unsubscribeUpdated();
            unsubscribeConfig();
        };
    }, [load]);

    useEffect(() => {
        // A local clock, stopped while the window is hidden.
        return activeInterval(() => setNow(Date.now()), CLOCK_TICK_MS);
    }, []);

    const runStatusAction = async (
        kind: BusyAction,
        scope: ErrorScope,
        failure: string,
        call: () => Promise<ModelAccountsStatus>,
    ) => {
        if (busyRef.current) return;
        busyRef.current = true;
        readVersion.current += 1;
        setBusy(kind);
        setError(scope);
        try {
            const next = await call();
            if (mounted.current) {
                setStatus(next);
                setError("load");
            }
        } catch {
            if (mounted.current) setError(scope, failure);
        } finally {
            busyRef.current = false;
            if (mounted.current) setBusy(null);
        }
    };

    const chooseMode = async (next: "managed" | "advanced") => {
        if (status === null || busyRef.current) return;
        busyRef.current = true;
        readVersion.current += 1;
        setBusy("mode");
        setError("mode");
        try {
            await SetAIProviderMode(next);
            await load();
        } catch {
            if (mounted.current) setError("mode", t("modelAccounts.modeFailed"));
        } finally {
            busyRef.current = false;
            if (mounted.current) setBusy(null);
        }
    };

    const accept = () => {
        void runStatusAction("accept", "accept", t("modelAccounts.acceptFailed"), () =>
            AcceptManagedModelConsent(CONSENT_VERSION),
        );
    };

    const revoke = () => {
        void runStatusAction("revoke", "revoke", t("modelAccounts.revokeFailed"), () =>
            RevokeManagedModelConsent(),
        );
    };

    const refresh = () => {
        void runStatusAction("refresh", "refresh", t("modelAccounts.refreshFailed"), () =>
            RefreshModelAccounts(),
        );
    };

    const busyNow = busy !== null;
    const managed = status !== null && status.mode === "managed";
    // The user's own API key, local model or CLI is in use (advanced or external mode).
    const ownKeyOn = status !== null && !managed;
    const external = status !== null && status.mode === "external";
    const install = status?.install;
    const userMagpie = install?.kind === "user";
    const accounts = status?.accounts ?? [];
    const groups = status?.groups ?? [];
    const activeGroupId = install?.activeGroup ?? "";
    const consentOpen =
        status !== null &&
        status.consent.required &&
        !status.consent.accepted &&
        !consentDeferred;
    // Signed-in accounts serve requests in managed mode and, for Isoform's own loop, in the
    // external CLI modes, so the agreement line is accurate in both.
    const consentInUse =
        status !== null && (managed || external) && status.consent.accepted;
    const errorText = status?.error?.trim() ?? "";
    const sidecarState = status !== null ? sidecarStateOf(status.sidecar, managed) : null;

    // The bundled copy reports Isoform's group among its own plans; the user's
    // magpie reports all of its groups on the install view.
    const findGroup = (id: string): ModelGroupView | undefined =>
        id === ""
            ? undefined
            : (groups.find((group) => group.id === id) ??
              (install?.groups ?? []).find((group) => group.id === id));
    const activeGroup = findGroup(activeGroupId);

    const assistantName = (id: AssistantId): string =>
        id === "claude" ? t("onboarding:agent.names.claude") : t("onboarding:agent.names.codex");

    const accountFor = (prefix: string): ModelAccountView | undefined =>
        accounts.find((item) => item.on && item.provider.trim().toLowerCase() === prefix);

    const planOf = (account: ModelAccountView | undefined): string | null =>
        account?.plan ? planDisplayName(account.plan, (plan) => t("modelAccounts.planName", { plan })) : null;

    const accountTitle = (account: ModelAccountView): string => {
        const id = account.provider.trim().toLowerCase();
        if (isAssistantId(id)) return assistantName(id);
        return account.provider || account.name;
    };

    // Only show a usage account when the provider has a recognizable name.
    // The gateway can return internal identifiers in either provider or name.
    const usageAccountTitle = (account: ModelAccountView): string | null => {
        const provider = account.provider.trim();
        const id = provider.toLowerCase();
        if (isAssistantId(id)) return assistantName(id);
        if (id === "kimi-code") return t("modelAccounts.kimiCode");
        return /\s/.test(provider) ? provider : null;
    };
    const usageAccounts = accounts.flatMap((account) => {
        const title = usageAccountTitle(account);
        return title ? [{ account, title }] : [];
    });
    const hasSources = usageAccounts.length > 0 || Boolean(status?.ollama) || Boolean(status?.byokRegistered);

    /** The plain name of a routing member: the assistant, local models or the user's key. */
    const sourceName = (prefix: string, member: string): string => {
        if (isAssistantId(prefix)) return assistantName(prefix);
        if (prefix === "ollama") return t("modelAccounts.localModelsName");
        if (BYOK_MEMBER_IDS.has(prefix)) return t("modelAccounts.yourKeyName");
        const account = accounts.find((item) => item.name === member || item.provider === member);
        if (account) return accountTitle(account);
        return titleWords(prefix);
    };

    const memberEntry = (member: string, index: number): ThinksWithEntry => {
        const { prefix, model } = splitMember(member);
        const detail = [model, planOf(accountFor(prefix))].filter(Boolean).join(" · ");
        return { key: `${index}:${member}`, name: sourceName(prefix, member), detail: detail || null };
    };

    // The line form used in "Who handles what": lower case inside a sentence.
    const memberLabel = (member: string): string => {
        const { prefix, model } = splitMember(member);
        let name: string;
        if (prefix === "ollama") name = t("modelAccounts.memberLocal");
        else if (BYOK_MEMBER_IDS.has(prefix)) name = t("modelAccounts.memberYourKey");
        else if (isAssistantId(prefix)) name = assistantName(prefix);
        else {
            const account = accounts.find((item) => item.name === member || item.provider === member);
            name = account ? [account.provider, account.plan].filter(Boolean).join(" ") : prefix;
        }
        return model ? t("modelAccounts.memberWithModel", { name, model }) : name;
    };

    const memberChain = (members: string[]): string => {
        if (members.length === 0) return t("modelAccounts.routingEmpty");
        return members
            .map((member, index) => {
                const label = memberLabel(member);
                return index === 0 ? label : t("modelAccounts.routingThen", { member: label });
            })
            .join(t("modelAccounts.routingSeparator"));
    };

    const workName = (key: RoutingKey): string => {
        switch (key) {
            case "chat":
                return t("modelAccounts.routingChat");
            case "background":
                return t("modelAccounts.routingBackground");
            case "light":
                return t("modelAccounts.routingLight");
            default:
                return key;
        }
    };

    // Mirrors magpieRouteTargets (magpie_api.go): the user's magpie routes every
    // kind of work through its active group; the bundled copy routes chat and
    // background work through Isoform's group when it exists and quick tasks
    // through yansu-light.
    const routeMembers = (key: RoutingKey): { group: ModelGroupView | undefined; members: string[] } => {
        if (userMagpie) {
            if (activeGroup) return { group: activeGroup, members: activeGroup.members };
            const model = install?.agentModels?.claude || install?.agentModels?.codex || "";
            return { group: undefined, members: model ? [model] : [] };
        }
        const id = activeGroupId === ISOFORM_GROUP_ID && key !== "light" ? ISOFORM_GROUP_ID : `yansu-${key}`;
        const group = findGroup(id) ?? groups.find((item) => item.id === key);
        return { group, members: group?.members ?? [] };
    };

    const windowLabel = (name: string): string | null => {
        const limit = usageWindowLimit(name, t);
        if (limit) return limit.charAt(0).toUpperCase() + limit.slice(1);
        switch (name.split("·")[0].trim().toLowerCase()) {
            case "kimi-code": return t("modelAccounts.kimiCode");
            default: return null;
        }
    };

    const windowRowsOf = (account: ModelAccountView): WindowRow[] =>
        (account.windows ?? []).flatMap((quota, index) => {
            const label = windowLabel(quota.name);
            if (!label) return [];
            const resetAt = parseTimestamp(quota.resetsAt);
            const refreshing = resetAt !== null && resetAt <= now;
            return [{
                key: `${index}:${quota.name}`,
                label,
                percent: refreshing ? null : remainingPercent(quota),
                resets: refreshing ? null : formatReset(quota.resetsAt, now, locale),
                refreshing,
            }];
        });

    const trailingOf = (account: ModelAccountView): string | null => {
        if (!account.on) return t("modelAccounts.off");
        // Signed-in accounts are only in use in managed mode.
        if (managed && (account.windows ?? []).length === 0) return t("modelAccounts.inUse");
        return null;
    };

    const sidecarLabel = (state: SidecarState): string => {
        switch (state) {
            case "ready":
                return t("modelAccounts.sidecarReady");
            case "degraded":
                return t("modelAccounts.sidecarDegraded");
            case "unavailable":
                return t("modelAccounts.sidecarUnavailable");
            case "inactive":
                return t("modelAccounts.sidecarInactive");
            default:
                return t("modelAccounts.sidecarStarting");
        }
    };

    // One plain line for the first thing that is wrong, in order of how much it blocks.
    let problem: Problem | null = null;
    if (errors.load) {
        problem = { message: errors.load, busy: false, retry: () => void load() };
    } else if (managed && (sidecarState === "degraded" || sidecarState === "unavailable")) {
        problem = { message: t("modelAccounts.problemGateway"), busy: busy === "refresh", retry: refresh };
    } else if (errorText !== "") {
        problem = { message: t("modelAccounts.statusErrorLead"), busy: busy === "refresh", retry: refresh };
    }

    // ── Thinks with ──

    let thinksWithBody: ReactNode;
    let thinksWithEntries: ThinksWithEntry[] = [];
    if (ownKeyOn) {
        thinksWithBody = (
            <div className="flex flex-col gap-1">
                <p className="settings-row-desc">{t("modelAccounts.thinksWithOwnKey")}</p>
                {!userMagpie && (
                    <p className="text-xs text-muted">{t("modelAccounts.thinksWithOwnKeyReason")}</p>
                )}
            </div>
        );
    } else if (activeGroupId !== "") {
        // The user's own magpie: its group, named, since that is where they
        // change the order. (Isoform's own gateway uses the choice card.)
        thinksWithEntries = (activeGroup?.members ?? []).map(memberEntry);
        const groupLabel = modelGroupDisplayName(activeGroup ?? { id: activeGroupId, name: "" }, tOnboarding);
        thinksWithBody = (
            <div className="flex flex-col gap-2">
                <p className="settings-row-desc">{t("modelAccounts.thinksWithGroupUser", { group: groupLabel })}</p>
                {thinksWithEntries.length > 0 ? (
                    <OrderedEntries entries={thinksWithEntries} />
                ) : (
                    <p className="text-xs text-muted">{t("modelAccounts.thinksWithGroupEmpty")}</p>
                )}
            </div>
        );
    } else {
        // No group in the user's magpie yet: Isoform asks the model each assistant is set to.
        thinksWithEntries = (["claude", "codex"] as const).flatMap((id) => {
            const model = install?.agentModels?.[id];
            return model ? [{ key: id, name: assistantName(id), detail: splitMember(model).model ?? model }] : [];
        });
        thinksWithBody = (
            <div className="flex flex-col gap-2">
                <p className="settings-row-desc">{t("modelAccounts.thinksWithAgentModels")}</p>
                {thinksWithEntries.length > 0 && <OrderedEntries entries={thinksWithEntries} />}
            </div>
        );
    }

    const detailRows: Array<[string, string]> = [];
    if (status !== null) {
        const version = (install?.version || status.sidecar.version || "").trim();
        detailRows.push([
            t("modelAccounts.detailsGateway"),
            (userMagpie
                ? t("modelAccounts.detailsGatewayUser", { version })
                : t("modelAccounts.detailsGatewayBundled", { version })
            ).trim(),
        ]);
        if (sidecarState) detailRows.push([t("modelAccounts.detailsState"), sidecarLabel(sidecarState)]);
        detailRows.push([t("modelAccounts.detailsGroup"), activeGroupId || t("modelAccounts.detailsNone")]);
        if (install?.serverUrl) detailRows.push([t("modelAccounts.detailsAddress"), install.serverUrl]);
        const program = install?.binaryPath || status.sidecar.binaryPath;
        if (program) detailRows.push([t("modelAccounts.detailsProgram"), program]);
        if (install?.configDir) detailRows.push([t("modelAccounts.detailsConfig"), install.configDir]);
        const lastErrors = [...new Set([(status.sidecar.lastError ?? "").trim(), errorText].filter(Boolean))];
        detailRows.push([
            t("modelAccounts.detailsLastError"),
            lastErrors.length > 0 ? lastErrors.join("\n") : t("modelAccounts.detailsNone"),
        ]);
    }

    // Isoform runs the gateway: who answers, and each agent's model and
    // effort, are chosen here (assistant-choice.tsx). The user's own key or
    // magpie keeps the order view below.
    const choosesHere = !ownKeyOn;
    const researchLine = status?.researchVia ? (
        <p className="settings-row-desc pt-3" data-testid="research-route">
            {status.researchVia === "claude_code"
                ? t("modelAccounts.searchClaudeCode")
                : status.researchVia === "opencli"
                  ? t("modelAccounts.searchOpencli")
                  : t("modelAccounts.searchNone")}
        </p>
    ) : null;

    const copyDetails = async () => {
        const text = detailRows.map(([label, value]) => `${label}: ${value}`).join("\n");
        try {
            await navigator.clipboard.writeText(text);
            toast.success(t("modelAccounts.detailsCopied"));
        } catch {
            // Clipboard blocked: the details stay on screen to copy by hand.
        }
    };

    return (
        <>
            {problem && <ProblemLine problem={problem} retryLabel={t("modelAccounts.retry")} />}

            {status === null ? (
                !errors.load && (
                    <p role="status" className="settings-row-desc">
                        {t("modelAccounts.loading")}
                    </p>
                )
            ) : (
                <>
                    {consentOpen && (
                        <SettingsCard
                            title={t("modelAccounts.consentTitle")}
                            description={t("modelAccounts.consentLead")}
                        >
                            <div className="flex flex-col gap-3">
                                <p className="settings-row-desc">{t("modelAccounts.consentTerms")}</p>
                                <p className="settings-row-desc">{t("modelAccounts.consentKeychain")}</p>
                                <p className="settings-row-desc">{t("modelAccounts.consentPrivacy")}</p>
                                <div className="flex flex-wrap items-center gap-3 pt-1">
                                    <Button type="button" size="sm" disabled={busyNow} onClick={accept}>
                                        {busy === "accept" && <Loader2 className="size-3.5 animate-spin" />}
                                        {t("modelAccounts.consentAccept")}
                                    </Button>
                                    <Button
                                        type="button"
                                        variant="outline"
                                        size="sm"
                                        disabled={busyNow}
                                        onClick={() => setConsentDeferred(true)}
                                    >
                                        {t("modelAccounts.consentDecline")}
                                    </Button>
                                </div>
                                {errors.accept && (
                                    <p role="alert" className="text-xs text-error">
                                        {errors.accept}
                                    </p>
                                )}
                            </div>
                        </SettingsCard>
                    )}

                    {choosesHere ? (
                        <>
                            <AssistantChoiceCard footer={researchLine} profileModels />
                            <MagpieProfileCard />
                        </>
                    ) : (
                        <SettingsCard
                            title={t("modelAccounts.thinksWithTitle")}
                            description={t("modelAccounts.thinksWithDescription")}
                        >
                            {thinksWithBody}
                            {!ownKeyOn && <AssistantHealthLines />}
                            {researchLine}
                        </SettingsCard>
                    )}

                    <SettingsCard
                        title={t("modelAccounts.usageTitle")}
                        description={t("modelAccounts.usageDescription")}
                        headerRight={
                            <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                disabled={busyNow}
                                onClick={refresh}
                            >
                                {busy === "refresh" ? (
                                    <Loader2 className="size-3.5 animate-spin" />
                                ) : (
                                    <RefreshCw className="size-3.5" />
                                )}
                                {t("modelAccounts.refresh")}
                            </Button>
                        }
                    >
                        {errors.refresh && (
                            <p role="alert" className="pb-2 text-xs text-error">
                                {errors.refresh}
                            </p>
                        )}
                        {hasSources ? (
                            <div className="flex flex-col">
                                {usageAccounts.map(({ account, title }, index) => (
                                    <AccountRow
                                        key={`${index}:${account.name}`}
                                        title={title}
                                        detail={accountDetail(account, (plan) => t("modelAccounts.planName", { plan }))}
                                        trailing={trailingOf(account)}
                                        windows={windowRowsOf(account)}
                                        balance={account.balance || null}
                                    />
                                ))}
                                {status.ollama && (
                                    <AccountRow
                                        title={t("modelAccounts.localModelsName")}
                                        detail={t("modelAccounts.localModelsDescription")}
                                        trailing={t("modelAccounts.inUse")}
                                        windows={[]}
                                        balance={null}
                                    />
                                )}
                                {status.byokRegistered && (
                                    <AccountRow
                                        title={t("modelAccounts.yourKeyName")}
                                        detail={t("modelAccounts.yourKeyDescription")}
                                        trailing={t("modelAccounts.inUse")}
                                        windows={[]}
                                        balance={null}
                                    />
                                )}
                            </div>
                        ) : (
                            !errorText && <p className="settings-row-desc py-2">{t("modelAccounts.emptyBody")}</p>
                        )}
                    </SettingsCard>
                </>
            )}

            <ToolsOnThisMacCard gatewayInUse={status === null || managed} />

            {(status !== null || errors.load) && (
                <SettingsAdvanced pageId={PAGE_ID} hint={t("modelAccounts.advancedHint")} defaultOpen={ownKeyOn}>
                    <SettingsCard>
                        <SettingsRow
                            title={t("modelAccounts.ownKeyTitle")}
                            description={t("modelAccounts.ownKeyDescription")}
                            control={
                                status !== null ? (
                                    <Switch
                                        aria-label={t("modelAccounts.ownKeyTitle")}
                                        checked={ownKeyOn}
                                        disabled={busyNow}
                                        onCheckedChange={(next) => void chooseMode(next ? "advanced" : "managed")}
                                    />
                                ) : undefined
                            }
                        />
                        {errors.mode && (
                            <p role="alert" className="pb-3 text-xs text-error">
                                {errors.mode}
                            </p>
                        )}
                        {/* The form is the way back in when the status cannot be read. */}
                        {(ownKeyOn || status === null) && <div className="pt-4">{ownKeyForm}</div>}
                    </SettingsCard>

                    {managed && (
                        <SettingsCard
                            title={t("modelAccounts.routingTitle")}
                            description={t("modelAccounts.routingDescription")}
                        >
                            <div className="flex flex-col gap-1.5">
                                {ROUTING_KEYS.map((key) => {
                                    const route = routeMembers(key);
                                    return (
                                        <p key={key} className="text-xs text-ink">
                                            {t("modelAccounts.routingLine", {
                                                group: workName(key),
                                                members: memberChain(route.members),
                                            })}
                                            {route.group && (
                                                <span className="ml-2 text-muted">
                                                    {t("modelAccounts.routingVia", {
                                                        group: modelGroupDisplayName(route.group, tOnboarding),
                                                    })}
                                                </span>
                                            )}
                                        </p>
                                    );
                                })}
                            </div>
                        </SettingsCard>
                    )}

                    {consentInUse && (
                        <SettingsCard
                            title={t("modelAccounts.signedInTitle")}
                            description={t("modelAccounts.consentAccepted")}
                        >
                            <div className="flex flex-wrap items-center gap-3">
                                <Button
                                    type="button"
                                    variant="outline"
                                    size="sm"
                                    disabled={busyNow}
                                    onClick={() => setWithdrawOpen(true)}
                                >
                                    {busy === "revoke" && <Loader2 className="size-3.5 animate-spin" />}
                                    {t("modelAccounts.consentStop")}
                                </Button>
                                {errors.revoke && (
                                    <p role="alert" className="w-full text-xs text-error">
                                        {errors.revoke}
                                    </p>
                                )}
                            </div>
                        </SettingsCard>
                    )}

                    {devTools && status !== null && (
                        <SettingsCard
                            title={t("modelAccounts.detailsTitle")}
                            description={t("modelAccounts.detailsDescription")}
                            headerRight={
                                <Button type="button" variant="outline" size="sm" onClick={() => void copyDetails()}>
                                    <Copy className="size-3.5" />
                                    {t("modelAccounts.detailsCopy")}
                                </Button>
                            }
                        >
                            <dl
                                data-testid="assistant-status-details"
                                className="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-xs"
                            >
                                {detailRows.map(([label, value]) => (
                                    <Fragment key={label}>
                                        <dt className="text-muted">{label}</dt>
                                        <dd className="min-w-0 whitespace-pre-wrap break-words text-ink">{value}</dd>
                                    </Fragment>
                                ))}
                            </dl>
                        </SettingsCard>
                    )}
                </SettingsAdvanced>
            )}


            <ConfirmDialog
                open={withdrawOpen}
                onOpenChange={setWithdrawOpen}
                title={t("modelAccounts.stopConfirmTitle")}
                description={t("modelAccounts.stopConfirmBody")}
                confirmLabel={t("modelAccounts.stopConfirm")}
                onConfirm={revoke}
            />
        </>
    );
}
