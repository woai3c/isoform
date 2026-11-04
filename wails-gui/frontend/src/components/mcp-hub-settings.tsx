import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import {
    ChevronDown,
    Download,
    FolderOpen,
    KeyRound,
    Loader2,
    Plus,
    RefreshCw,
    Search,
    ShieldCheck,
    Sparkles,
    StopCircle,
    Trash2,
    X,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Switch } from "@/components/ui/switch";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { SettingsCard } from "@/components/settings/settings-primitives";
import { SettingsAdvanced } from "@/components/settings/settings-advanced";
import {
    AddMcpServerManual,
    ConnectMcpServerOAuth,
    DiscoverLocalMcpServers,
    ImportLocalMcpServer,
    InstallMcpFromRegistry,
    ListMcpServers,
    OpenMcpServerFolder,
    RefreshMcpRegistry,
    RestartMcpServer,
    SearchMcpRegistry,
    SetMcpServerEnabled,
    SetMcpServerTrusted,
    StopMcpServer,
    TailMcpServerLog,
    UninstallMcpServer,
} from "../../wailsjs/go/main/App";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import { main } from "../../wailsjs/go/models";

// ── Tool servers (Settings page id "mcp") ──
//
// Two tabs: Installed (what you have, with on/off) and Add (import from other
// apps on this computer, or browse the server list). Everything technical, adding
// a server by hand and each server's command, tools and logs, sits in the
// page's one Advanced section at the bottom.

type McpServer = main.McpServerView;
type DiscoveredServer = main.DiscoveredMcpServer;
type DiscoveredSource = main.DiscoveredMcpSource;
type DiscoveryReport = main.DiscoveryReport;
type RegistryEntry = main.McpRegistryEntry;

type HubTab = "installed" | "add";

const HEALTH_STATES = ["healthy", "unhealthy", "unknown"] as const;
type HealthState = (typeof HEALTH_STATES)[number];

const HEALTH_DOT_CLASS: Record<HealthState, string> = {
    healthy: "bg-success",
    unhealthy: "bg-error",
    unknown: "bg-muted",
};

/** The backend reports "healthy", "unhealthy" or "unknown"; anything else is
 *  treated as unknown so the label is always a translated word. */
function healthState(value: string | undefined): HealthState {
    return (HEALTH_STATES as readonly string[]).includes(value ?? "")
        ? (value as HealthState)
        : "unknown";
}

const OAUTH_STATES = [
    "authorized",
    "refreshable",
    "authorization_required",
    "expired",
    "token_error",
] as const;
type OAuthState = (typeof OAUTH_STATES)[number];

/** Sign-in status from mcpOAuthStatus (mcp_oauth.go); empty or unknown values
 *  mean the server still needs a sign-in. */
function oauthState(value: string | undefined): OAuthState {
    return (OAUTH_STATES as readonly string[]).includes(value ?? "")
        ? (value as OAuthState)
        : "authorization_required";
}

function isSignedIn(state: OAuthState): boolean {
    return state === "authorized" || state === "refreshable";
}

function errorMessage(err: unknown): string {
    return err instanceof Error ? err.message : String(err);
}

interface NewServerForm {
    id: string;
    name: string;
    transport: "stdio" | "http";
    command: string;
    argsRaw: string;
    workDir: string;
    envRaw: string;
    url: string;
    authMode: "none" | "oauth";
    oauthClientID: string;
    oauthAuthURL: string;
    oauthTokenURL: string;
    oauthScopesRaw: string;
    oauthResource: string;
    autoStart: boolean;
}

const blankForm: NewServerForm = {
    id: "",
    name: "",
    transport: "stdio",
    command: "",
    argsRaw: "",
    workDir: "",
    envRaw: "",
    url: "",
    authMode: "none",
    oauthClientID: "",
    oauthAuthURL: "",
    oauthTokenURL: "",
    oauthScopesRaw: "",
    oauthResource: "",
    autoStart: true,
};

function parseArgs(raw: string): string[] {
    // Whitespace-split is enough for the common case (npx -y @x/y /tmp).
    // Arguments that contain spaces can be edited in state.json directly;
    // keeping this simple avoids re-implementing a shell tokeniser in the
    // frontend.
    return raw
        .split(/\s+/)
        .map((piece) => piece.trim())
        .filter((piece) => piece.length > 0);
}

// githubRawReadmeURL turns a github.com/<owner>/<repo>(/...) URL into
// the canonical raw README URL using the `HEAD` ref placeholder.
//
// Why HEAD? `main` is the modern default but plenty of older repos
// (e.g. upstash/context7) still use `master`. HEAD resolves to whatever
// the repo's actual default branch is, so we never 404 on a guessed
// branch name. Returns "" for non-github URLs (gitlab/bitbucket etc.)
// — those paths fall back to a regular WebFetch on the source page.
function githubRawReadmeURL(sourceURL: string): string {
    try {
        const url = new URL(sourceURL);
        const host = url.hostname.toLowerCase();
        if (host !== "github.com" && host !== "www.github.com") return "";
        const parts = url.pathname.split("/").filter(Boolean);
        if (parts.length < 2) return "";
        const [owner, repo] = parts;
        return `https://raw.githubusercontent.com/${owner}/${repo}/HEAD/README.md`;
    } catch {
        return "";
    }
}

function parseEnv(raw: string): Record<string, string> {
    const env: Record<string, string> = {};
    for (const line of raw.split(/\r?\n/)) {
        const trimmed = line.trim();
        if (!trimmed || trimmed.startsWith("#")) continue;
        const eq = trimmed.indexOf("=");
        if (eq === -1) continue;
        const key = trimmed.slice(0, eq).trim();
        const value = trimmed.slice(eq + 1);
        if (key) env[key] = value;
    }
    return env;
}

function parseScopes(raw: string): string[] {
    return raw
        .split(/\s+/)
        .map((piece) => piece.trim())
        .filter((piece) => piece.length > 0);
}

export function McpHubSettings() {
    const { t } = useTranslation("settings");
    const [activeTab, setActiveTab] = useState<HubTab>("installed");
    const [servers, setServers] = useState<McpServer[]>([]);
    const [loading, setLoading] = useState(true);
    const [adding, setAdding] = useState(false);
    const [updating, setUpdating] = useState<string | null>(null);
    const [expanded, setExpanded] = useState<Record<string, boolean>>({});
    const [form, setForm] = useState<NewServerForm>(blankForm);
    const [fieldErrors, setFieldErrors] = useState<ManualFieldErrors>({});
    useEffect(() => setFieldErrors({}), [form.id, form.command, form.url, form.transport]);
    const [pendingUninstall, setPendingUninstall] = useState<McpServer | null>(null);

    const refresh = useCallback(async () => {
        setLoading(true);
        try {
            const list = await ListMcpServers();
            setServers(list);
        } catch (err) {
            toast.error(t("mcp.errors.loadFailed", { error: errorMessage(err) }));
        } finally {
            setLoading(false);
        }
    }, [t]);

    useEffect(() => {
        let mounted = true;
        ListMcpServers()
            .then((list) => mounted && setServers(list))
            .catch((err) => toast.error(t("mcp.errors.loadFailed", { error: errorMessage(err) })))
            .finally(() => mounted && setLoading(false));
        return () => {
            mounted = false;
        };
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    // Track which tabs have been visited so we mount them lazily (on first
    // visit) but keep them alive afterwards. This avoids re-fetching data
    // from the Go backend on every tab switch — each Wails binding call is
    // a CGO round-trip that blocks the WebView JS thread.
    const visitedTabs = useRef(new Set<HubTab>(["installed"]));
    const handleTabChange = useCallback((next: HubTab) => {
        visitedTabs.current.add(next);
        setActiveTab(next);
    }, []);

    const handleAdd = useCallback(async () => {
        const trimmedID = form.id.trim();
        const trimmedCmd = form.command.trim();
        const trimmedURL = form.url.trim();
        const errors: ManualFieldErrors = {
            id: trimmedID ? undefined : t("mcp.manual.nameRequired"),
            command: form.transport === "stdio" && !trimmedCmd ? t("mcp.manual.commandRequired") : undefined,
            url: form.transport === "http" && !trimmedURL ? t("mcp.manual.urlRequired") : undefined,
        };
        if (errors.id || errors.command || errors.url) {
            setFieldErrors(errors);
            return;
        }
        setAdding(true);
        try {
            const created = await AddMcpServerManual(main.ManualServerInput.createFrom({
                id: trimmedID,
                name: form.name.trim() || trimmedID,
                description: "",
                transport: form.transport,
                command: form.transport === "stdio" ? trimmedCmd : "",
                args: form.transport === "stdio" ? parseArgs(form.argsRaw) : [],
                work_dir: form.transport === "stdio" ? form.workDir.trim() : "",
                env: form.transport === "stdio" ? parseEnv(form.envRaw) : {},
                secret_env: [],
                url: form.transport === "http" ? trimmedURL : "",
                auth_mode: form.transport === "http" ? form.authMode : "none",
                oauth: form.transport === "http" && form.authMode === "oauth"
                    ? {
                          auth_url: form.oauthAuthURL.trim(),
                          token_url: form.oauthTokenURL.trim(),
                          client_id: form.oauthClientID.trim(),
                          scopes: parseScopes(form.oauthScopesRaw),
                          resource: form.oauthResource.trim(),
                          issuer: "",
                      }
                    : {
                          auth_url: "",
                          token_url: "",
                          client_id: "",
                          scopes: [],
                          resource: "",
                          issuer: "",
                      },
                auto_start: form.autoStart,
            }));
            setServers((current) => {
                const next = current.filter((s) => s.id !== created.id);
                return [...next, created].sort((a, b) => a.id.localeCompare(b.id));
            });
            setForm(blankForm);
            toast.success(t("mcp.manual.addedToast", { id: created.id }));
            handleTabChange("installed");
        } catch (err) {
            toast.error(t("mcp.manual.addFailed", { error: errorMessage(err) }));
        } finally {
            setAdding(false);
        }
    }, [form, handleTabChange, t]);

    const replaceServer = useCallback((server: McpServer) => {
        setServers((current) => current.map((s) => (s.id === server.id ? server : s)));
    }, []);

    const handleEnabledChange = useCallback(
        async (server: McpServer, enabled: boolean) => {
            setUpdating(server.id);
            try {
                const updated = await SetMcpServerEnabled(server.id, enabled);
                replaceServer(updated);
            } catch (err) {
                const key = enabled ? "mcp.errors.turnOnFailed" : "mcp.errors.turnOffFailed";
                toast.error(t(key, { name: server.name, error: errorMessage(err) }));
            } finally {
                setUpdating(null);
            }
        },
        [replaceServer, t],
    );

    const handleTrustedChange = useCallback(
        async (server: McpServer, trusted: boolean) => {
            setUpdating(server.id);
            try {
                const updated = await SetMcpServerTrusted(server.id, trusted);
                replaceServer(updated);
            } catch (err) {
                toast.error(t("mcp.errors.trustFailed", { name: server.name, error: errorMessage(err) }));
            } finally {
                setUpdating(null);
            }
        },
        [replaceServer, t],
    );

    const handleRestart = useCallback(
        async (server: McpServer) => {
            setUpdating(server.id);
            try {
                const updated = await RestartMcpServer(server.id);
                replaceServer(updated);
                toast.success(t("mcp.installed.restarted", { name: server.name }));
            } catch (err) {
                toast.error(t("mcp.errors.restartFailed", { error: errorMessage(err) }));
            } finally {
                setUpdating(null);
            }
        },
        [replaceServer, t],
    );

    const handleAuthorizeOAuth = useCallback(
        async (server: McpServer) => {
            setUpdating(server.id);
            try {
                const updated = await ConnectMcpServerOAuth(server.id);
                replaceServer(updated);
                toast.success(t("mcp.oauth.authorized_toast", { name: server.name }));
            } catch (err) {
                toast.error(t("mcp.oauth.authFailed", { error: errorMessage(err) }));
            } finally {
                setUpdating(null);
            }
        },
        [replaceServer, t],
    );

    const handleStop = useCallback(
        async (server: McpServer) => {
            setUpdating(server.id);
            try {
                await StopMcpServer(server.id);
                // Stop doesn't change the persisted Enabled flag; just
                // refresh from backend so health reflects the new state.
                await refresh();
            } catch (err) {
                toast.error(t("mcp.errors.stopFailed", { error: errorMessage(err) }));
            } finally {
                setUpdating(null);
            }
        },
        [refresh, t],
    );

    const handleUninstall = useCallback(
        async (server: McpServer) => {
            setUpdating(server.id);
            try {
                await UninstallMcpServer(server.id);
                setServers((current) => current.filter((s) => s.id !== server.id));
                setExpanded((current) => {
                    const next = { ...current };
                    delete next[server.id];
                    return next;
                });
                toast.success(t("mcp.installed.uninstalled", { name: server.name }));
            } catch (err) {
                toast.error(t("mcp.errors.uninstallFailed", { error: errorMessage(err) }));
            } finally {
                setUpdating(null);
            }
        },
        [t],
    );

    const handleOpenFolder = useCallback(async (server: McpServer) => {
        try {
            await OpenMcpServerFolder(server.id);
        } catch (err) {
            toast.error(t("mcp.errors.openFolderFailed", { error: errorMessage(err) }));
        }
    }, [t]);

    const handleAfterImport = useCallback(
        (created: McpServer) => {
            setServers((current) => {
                const next = current.filter((s) => s.id !== created.id);
                return [...next, created].sort((a, b) => a.id.localeCompare(b.id));
            });
            handleTabChange("installed");
        },
        [handleTabChange],
    );

    // handleAskAIInstall fires the agent-native install flow: compose a
    // prompt that walks the agent through reading the repo's README,
    // picking the install command, running `yansu mcp install`, and
    // verifying health. We dispatch a window CustomEvent that App.tsx
    // listens for — it closes Settings, opens a fresh general chat,
    // and submits the prompt so the user just watches the agent work.
    // The prompt is written in the person's language; the commands in it
    // are literal and interpolated unchanged.
    //
    // We pre-compute the raw README URL using `/HEAD/` instead of a
    // guessed branch name. Agents otherwise default to `main` and 404
    // on repos that use `master` (e.g. upstash/context7). HEAD always
    // resolves to the repo's default branch on raw.githubusercontent.
    const handleAskAIInstall = useCallback((entry: RegistryEntry) => {
        const suggestedId = entry.slug
            .replace(/^tp-/, "")
            .replace(/[^A-Za-z0-9-]+/g, "-")
            .replace(/-+/g, "-")
            .replace(/^-|-$/g, "")
            .slice(0, 31);
        const readmeRawURL = githubRawReadmeURL(entry.source_url);
        const fetchHints = readmeRawURL
            ? [
                  t("mcp.installPrompt.fetchFirst", { readme: readmeRawURL }),
                  t("mcp.installPrompt.fetchFallback", { url: entry.source_url }),
              ]
            : [t("mcp.installPrompt.fetchOnly", { url: entry.source_url })];
        const prompt = [
            t("mcp.installPrompt.intro", { name: entry.name, url: entry.source_url }),
            "",
            t("mcp.installPrompt.stepsTitle"),
            t("mcp.installPrompt.step1"),
            ...fetchHints,
            t("mcp.installPrompt.step1Hint"),
            t("mcp.installPrompt.step2"),
            t("mcp.installPrompt.step3", { id: suggestedId }),
            t("mcp.installPrompt.step4", { id: suggestedId }),
            "",
            t("mcp.installPrompt.closing"),
        ].join("\n");
        window.dispatchEvent(
            new CustomEvent("mcp:install-via-ai", {
                detail: { prompt, display: t("mcp.installPrompt.visible", { name: entry.name, url: entry.source_url }) },
            }),
        );
    }, [t]);

    const sortedServers = useMemo(
        () => [...servers].sort((a, b) => a.id.localeCompare(b.id)),
        [servers],
    );

    const tabs = useMemo(
        () => [
            { id: "installed" as const, label: t("mcp.tabs.installed") },
            { id: "add" as const, label: t("mcp.tabs.add") },
        ],
        [t],
    );

    return (
        <div className="space-y-6">
            <HubTabs
                label={t("mcp.tabs.label")}
                items={tabs}
                active={activeTab}
                onChange={handleTabChange}
            />

            <div className={activeTab === "installed" ? undefined : "hidden"}>
                <InstalledServersCard
                    servers={sortedServers}
                    loading={loading}
                    updating={updating}
                    onRefresh={refresh}
                    onAdd={() => handleTabChange("add")}
                    onEnabledChange={handleEnabledChange}
                    onAuthorizeOAuth={handleAuthorizeOAuth}
                    onUninstall={setPendingUninstall}
                />
            </div>

            {visitedTabs.current.has("add") && (
                <div className={activeTab === "add" ? "space-y-6" : "hidden"}>
                    <DiscoverServersCard
                        installedIds={new Set(servers.map((s) => s.id))}
                        onImported={handleAfterImport}
                    />
                    <RegistryServersCard
                        onImported={handleAfterImport}
                        onAskAIInstall={handleAskAIInstall}
                    />
                </div>
            )}

            <SettingsAdvanced pageId="mcp" hint={t("mcp.advancedHint")}>
                <ManualAddCard
                    form={form}
                    errors={fieldErrors}
                    setForm={setForm}
                    adding={adding}
                    onAdd={handleAdd}
                />
                <ServerDetailsCard
                    servers={sortedServers}
                    updating={updating}
                    expanded={expanded}
                    onExpandedChange={(id, open) =>
                        setExpanded((current) => ({ ...current, [id]: open }))
                    }
                    onTrustedChange={handleTrustedChange}
                    onRestart={handleRestart}
                    onStop={handleStop}
                    onOpenFolder={handleOpenFolder}
                />
            </SettingsAdvanced>

            <ConfirmDialog
                open={pendingUninstall !== null}
                onOpenChange={(open) => {
                    if (!open) setPendingUninstall(null);
                }}
                title={t("mcp.installed.uninstallConfirm.title", { name: pendingUninstall?.name ?? "" })}
                description={t("mcp.installed.uninstallConfirm.description")}
                confirmLabel={t("mcp.installed.uninstallConfirm.confirm")}
                variant="danger"
                onConfirm={() => {
                    if (pendingUninstall) void handleUninstall(pendingUninstall);
                }}
            />
        </div>
    );
}

// ── Tabs ──
//
// Underlined tab strip shared by the Skills and Tool servers pages.

export interface HubTabsProps<T extends string> {
    /** Accessible name of the tab list. */
    label: string;
    items: ReadonlyArray<{ id: T; label: string }>;
    active: T;
    onChange: (next: T) => void;
}

export function HubTabs<T extends string>({ label, items, active, onChange }: HubTabsProps<T>) {
    return (
        <div role="tablist" aria-label={label} className="flex items-center gap-1 border-b border-border">
            {items.map((item) => {
                const isActive = active === item.id;
                return (
                    <button
                        key={item.id}
                        type="button"
                        role="tab"
                        aria-selected={isActive}
                        onClick={() => onChange(item.id)}
                        className={
                            "px-3 py-2 text-sm rounded-t-md transition-colors " +
                            (isActive
                                ? "text-ink border-b-2 border-accent-2 -mb-px"
                                : "text-muted hover:text-ink")
                        }
                    >
                        {item.label}
                    </button>
                );
            })}
        </div>
    );
}

// ── Installed tab ──

interface InstalledServersCardProps {
    servers: McpServer[];
    loading: boolean;
    updating: string | null;
    onRefresh: () => void;
    onAdd: () => void;
    onEnabledChange: (server: McpServer, enabled: boolean) => void;
    onAuthorizeOAuth: (server: McpServer) => void;
    onUninstall: (server: McpServer) => void;
}

function InstalledServersCard({
    servers,
    loading,
    updating,
    onRefresh,
    onAdd,
    onEnabledChange,
    onAuthorizeOAuth,
    onUninstall,
}: InstalledServersCardProps) {
    const { t } = useTranslation("settings");
    return (
        <SettingsCard
            title={t("mcp.installed.title")}
            description={t("mcp.installed.description")}
            headerRight={
                <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={onRefresh}
                    disabled={loading}
                >
                    {loading ? (
                        <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    ) : (
                        <RefreshCw className="h-3.5 w-3.5" />
                    )}
                    {t("mcp.installed.refresh")}
                </Button>
            }
        >
            {loading ? (
                <div className="py-8 flex items-center justify-center text-sm text-muted">
                    <Loader2 className="h-4 w-4 animate-spin mr-2" />
                    {t("mcp.installed.loading")}
                </div>
            ) : servers.length === 0 ? (
                <div className="py-6 flex flex-col items-start gap-3">
                    <p className="text-sm text-muted">{t("mcp.installed.empty")}</p>
                    <Button type="button" variant="outline" size="sm" onClick={onAdd}>
                        <Plus className="h-3.5 w-3.5" />
                        {t("mcp.installed.emptyAction")}
                    </Button>
                </div>
            ) : (
                <div className="divide-y divide-border">
                    {servers.map((server) => (
                        <McpServerRow
                            key={server.id}
                            server={server}
                            updating={updating === server.id}
                            onEnabledChange={onEnabledChange}
                            onAuthorizeOAuth={onAuthorizeOAuth}
                            onUninstall={onUninstall}
                        />
                    ))}
                </div>
            )}
        </SettingsCard>
    );
}

interface McpServerRowProps {
    server: McpServer;
    updating: boolean;
    onEnabledChange: (server: McpServer, enabled: boolean) => void;
    onAuthorizeOAuth: (server: McpServer) => void;
    onUninstall: (server: McpServer) => void;
}

function McpServerRow({
    server,
    updating,
    onEnabledChange,
    onAuthorizeOAuth,
    onUninstall,
}: McpServerRowProps) {
    const { t } = useTranslation("settings");
    const health = healthState(server.health);
    const usesOAuth = server.auth_mode === "oauth";
    const signIn = oauthState(server.oauth_status);
    const toolCount = server.tools?.length ?? 0;
    const crashed = server.enabled && health === "unhealthy";
    return (
        <div className="py-3 flex items-start gap-4">
            <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2 min-w-0">
                    <span
                        aria-hidden
                        className={`h-2 w-2 rounded-full shrink-0 ${HEALTH_DOT_CLASS[health]}`}
                    />
                    <span className="settings-row-title truncate">{server.name}</span>
                    {toolCount > 0 && (
                        <span className="text-[11px] text-muted shrink-0">
                            {t("mcp.installed.toolCount", { count: toolCount })}
                        </span>
                    )}
                </div>
                <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] mt-0.5">
                    <span className="text-muted">{t(`mcp.health.${health}`)}</span>
                    {usesOAuth && (
                        <span className={isSignedIn(signIn) ? "text-success" : "text-warning"}>
                            {t(`mcp.oauth.status.${signIn}`)}
                        </span>
                    )}
                </div>
                {server.description && (
                    <p className="settings-row-desc line-clamp-2">{server.description}</p>
                )}
                {crashed && (
                    <p className="text-[11px] text-warning mt-1">{t("mcp.installed.crashedHint")}</p>
                )}
            </div>
            <div className="flex items-center gap-2 shrink-0">
                {updating && <Loader2 className="h-3.5 w-3.5 animate-spin text-muted" />}
                {usesOAuth && (
                    <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        onClick={() => onAuthorizeOAuth(server)}
                        disabled={updating}
                    >
                        <KeyRound className="h-3.5 w-3.5" />
                        {isSignedIn(signIn) ? t("mcp.oauth.reauthorize") : t("mcp.oauth.authorize")}
                    </Button>
                )}
                <Switch
                    checked={server.enabled}
                    onCheckedChange={(checked) => onEnabledChange(server, checked)}
                    disabled={updating}
                    aria-label={t("mcp.installed.toggleAria", { name: server.name })}
                />
                <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    onClick={() => onUninstall(server)}
                    disabled={updating}
                    aria-label={t("mcp.installed.uninstallAria", { name: server.name })}
                    title={t("mcp.installed.uninstall")}
                >
                    <Trash2 className="h-3.5 w-3.5" />
                </Button>
            </div>
        </div>
    );
}

// ── Add tab: import from other apps on this computer ──

interface DiscoverServersCardProps {
    installedIds: Set<string>;
    onImported: (server: McpServer) => void;
}

function DiscoverServersCard({ installedIds, onImported }: DiscoverServersCardProps) {
    const { t } = useTranslation("settings");
    const [report, setReport] = useState<DiscoveryReport | null>(null);
    const [loading, setLoading] = useState(true);
    const [importing, setImporting] = useState<string | null>(null);
    const [filter, setFilter] = useState("");

    const refresh = useCallback(async () => {
        setLoading(true);
        try {
            const result = await DiscoverLocalMcpServers();
            setReport(result);
        } catch (err) {
            toast.error(t("mcp.discover.discoveryFailed", { error: errorMessage(err) }));
        } finally {
            setLoading(false);
        }
    }, [t]);

    useEffect(() => {
        void refresh();
    }, [refresh]);

    const handleImport = useCallback(
        async (server: DiscoveredServer) => {
            setImporting(server.id);
            try {
                const created = await ImportLocalMcpServer(server.source_id, server.name, true);
                toast.success(t("mcp.discover.importedToast", { name: server.name, source: server.source_name }));
                onImported(created);
                await refresh();
            } catch (err) {
                toast.error(t("mcp.discover.importFailed", { error: errorMessage(err) }));
            } finally {
                setImporting(null);
            }
        },
        [onImported, refresh, t],
    );

    const filtered = useMemo(() => {
        if (!report) return [] as DiscoveredServer[];
        const q = filter.trim().toLowerCase();
        if (!q) return report.servers;
        return report.servers.filter((srv) => {
            const haystack = [
                srv.name,
                srv.source_id,
                srv.source_name,
                srv.command ?? "",
                srv.url ?? "",
                ...(srv.args ?? []),
            ]
                .join("\n")
                .toLowerCase();
            return haystack.includes(q);
        });
    }, [report, filter]);

    const grouped = useMemo(() => {
        const out = new Map<string, { source: DiscoveredSource; rows: DiscoveredServer[] }>();
        if (!report) return out;
        for (const src of report.sources) {
            out.set(src.id, { source: src, rows: [] });
        }
        for (const row of filtered) {
            const bucket = out.get(row.source_id);
            if (bucket) {
                bucket.rows.push(row);
            }
        }
        return out;
    }, [report, filtered]);

    return (
        <SettingsCard
            title={t("mcp.discover.title")}
            description={t("mcp.discover.description")}
            headerRight={
                <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={refresh}
                    disabled={loading}
                >
                    {loading ? (
                        <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    ) : (
                        <RefreshCw className="h-3.5 w-3.5" />
                    )}
                    {t("mcp.discover.rescan")}
                </Button>
            }
        >
            <div className="relative mb-3">
                <Search className="h-3.5 w-3.5 absolute left-3 top-1/2 -translate-y-1/2 text-muted" />
                <input
                    type="text"
                    value={filter}
                    onChange={(event) => setFilter(event.target.value)}
                    placeholder={t("mcp.discover.filterPlaceholder")}
                    aria-label={t("mcp.discover.filterPlaceholder")}
                    className="h-9 w-full rounded-md border border-border bg-background pl-9 pr-3 text-sm text-ink placeholder:text-muted/60 focus:outline-none focus:ring-1 focus:ring-accent-2/40"
                />
            </div>

            {loading && !report ? (
                <div className="py-8 flex items-center justify-center text-sm text-muted">
                    <Loader2 className="h-4 w-4 animate-spin mr-2" />
                    {t("mcp.discover.scanning")}
                </div>
            ) : (
                <div className="space-y-4">
                    {report && !Array.from(grouped.values()).some(({ rows }) => rows.length > 0) && (
                        <p className="py-6 text-center text-sm text-muted">{t("mcp.discover.noneFound")}</p>
                    )}
                    {Array.from(grouped.values()).filter(({ rows }) => rows.length > 0).map(({ source, rows }) => (
                        <DiscoverySourceBlock
                            key={source.id}
                            source={source}
                            rows={rows}
                            installedIds={installedIds}
                            importing={importing}
                            onImport={handleImport}
                        />
                    ))}
                </div>
            )}
        </SettingsCard>
    );
}

interface DiscoverySourceBlockProps {
    source: DiscoveredSource;
    rows: DiscoveredServer[];
    installedIds: Set<string>;
    importing: string | null;
    onImport: (server: DiscoveredServer) => void;
}

function DiscoverySourceBlock({
    source,
    rows,
    installedIds,
    importing,
    onImport,
}: DiscoverySourceBlockProps) {
    const { t } = useTranslation("settings");
    const status = source.found
        ? t("mcp.discover.serverCount", { count: source.count })
        : source.error
          ? t("mcp.discover.parseError")
          : t("mcp.discover.notFound");
    return (
        <div className="rounded-md border border-border bg-muted/10">
            <div className="flex items-center justify-between gap-3 px-3 py-2 border-b border-border">
                <p
                    className="min-w-0 truncate text-sm font-medium text-ink"
                    title={source.path || t("mcp.discover.notAvailable")}
                >
                    {source.name}
                </p>
                <span className="text-[11px] text-muted shrink-0" title={source.error || undefined}>
                    {status}
                </span>
            </div>
            {rows.length === 0 ? (
                <p className="text-[11px] text-muted px-3 py-3">
                    {source.found ? t("mcp.discover.noServers") : t("mcp.discover.nothingToImport")}
                </p>
            ) : (
                <ul className="divide-y divide-border">
                    {rows.map((row) => (
                        <DiscoveryServerRow
                            key={row.id}
                            row={row}
                            isImporting={importing === row.id}
                            installedIds={installedIds}
                            onImport={onImport}
                        />
                    ))}
                </ul>
            )}
        </div>
    );
}

interface DiscoveryServerRowProps {
    row: DiscoveredServer;
    isImporting: boolean;
    installedIds: Set<string>;
    onImport: (server: DiscoveredServer) => void;
}

function DiscoveryServerRow({ row, isImporting, installedIds, onImport }: DiscoveryServerRowProps) {
    const { t } = useTranslation("settings");
    const alreadyImported = row.installed || (row.installed_id ? installedIds.has(row.installed_id) : false);
    // The command or URL is shown on hover only; the name is what people
    // recognise from the other app.
    const summary = row.transport === "stdio"
        ? `${row.command ?? ""} ${(row.args ?? []).join(" ")}`.trim()
        : row.url ?? "";
    return (
        <li className="px-3 py-2 flex items-center gap-3">
            <div className="min-w-0 flex-1 flex items-center gap-2">
                <span className="text-sm text-ink truncate" title={summary || undefined}>
                    {row.name}
                </span>
                {alreadyImported && (
                    <span className="text-[11px] text-success shrink-0">
                        {t("mcp.discover.imported")}
                    </span>
                )}
            </div>
            <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => onImport(row)}
                disabled={isImporting || alreadyImported}
            >
                {isImporting ? (
                    <Loader2 className="h-3.5 w-3.5 animate-spin" />
                ) : (
                    <Download className="h-3.5 w-3.5" />
                )}
                {alreadyImported ? t("mcp.discover.imported") : t("mcp.discover.import")}
            </Button>
        </li>
    );
}

// ── Add tab: browse the server list ──

interface RegistryServersCardProps {
    onImported: (server: McpServer) => void;
    onAskAIInstall: (entry: RegistryEntry) => void;
}

function RegistryServersCard({ onImported, onAskAIInstall }: RegistryServersCardProps) {
    const { t } = useTranslation("settings");
    const [entries, setEntries] = useState<RegistryEntry[]>([]);
    const [loading, setLoading] = useState(true);
    const [refreshing, setRefreshing] = useState(false);
    const [installing, setInstalling] = useState<string | null>(null);
    const [query, setQuery] = useState("");
    const [error, setError] = useState<string | null>(null);

    const search = useCallback(async (q: string) => {
        setLoading(true);
        setError(null);
        try {
            const list = await SearchMcpRegistry(q);
            setEntries(list ?? []);
        } catch (err) {
            setError(errorMessage(err));
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        void search("");
    }, [search]);

    const handleRefresh = useCallback(async () => {
        setRefreshing(true);
        setError(null);
        try {
            const list = await RefreshMcpRegistry();
            setEntries(list ?? []);
            toast.success(t("mcp.registry.catalogRefreshed"));
        } catch (err) {
            const message = errorMessage(err);
            setError(message);
            toast.error(t("mcp.registry.refreshFailed", { error: message }));
        } finally {
            setRefreshing(false);
        }
    }, [t]);

    const handleInstall = useCallback(
        async (entry: RegistryEntry) => {
            if (!entry.installable) {
                toast.error(t("mcp.registry.thirdPartyHint", { name: entry.name, url: entry.source_url }));
                return;
            }
            setInstalling(entry.slug);
            try {
                const created = await InstallMcpFromRegistry(entry.slug, true);
                toast.success(t("mcp.registry.installed", { name: entry.name }));
                onImported(created);
            } catch (err) {
                toast.error(t("mcp.registry.installFailed", { error: errorMessage(err) }));
            } finally {
                setInstalling(null);
            }
        },
        [onImported, t],
    );

    const filtered = useMemo(() => {
        if (!query.trim()) return entries;
        const q = query.trim().toLowerCase();
        return entries.filter((entry) =>
            (entry.name + "\n" + entry.slug + "\n" + (entry.description ?? "") + "\n" + entry.section)
                .toLowerCase()
                .includes(q),
        );
    }, [entries, query]);

    const reference = useMemo(
        () => filtered.filter((e) => e.kind === "reference"),
        [filtered],
    );
    const thirdParty = useMemo(
        () => filtered.filter((e) => e.kind === "third-party"),
        [filtered],
    );

    return (
        <SettingsCard
            title={t("mcp.registry.title")}
            description={t("mcp.registry.description")}
            headerRight={
                <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    onClick={handleRefresh}
                    disabled={refreshing}
                >
                    {refreshing ? (
                        <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    ) : (
                        <RefreshCw className="h-3.5 w-3.5" />
                    )}
                    {t("mcp.registry.refresh")}
                </Button>
            }
        >
            <div className="relative mb-3">
                <Search className="h-3.5 w-3.5 absolute left-3 top-1/2 -translate-y-1/2 text-muted" />
                <input
                    type="text"
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                    placeholder={t("mcp.registry.filterPlaceholder")}
                    aria-label={t("mcp.registry.filterPlaceholder")}
                    className="h-9 w-full rounded-md border border-border bg-background pl-9 pr-3 text-sm text-ink placeholder:text-muted/60 focus:outline-none focus:ring-1 focus:ring-accent-2/40"
                />
            </div>

            {loading && entries.length === 0 ? (
                <div className="py-8 flex items-center justify-center text-sm text-muted">
                    <Loader2 className="h-4 w-4 animate-spin mr-2" />
                    {t("mcp.registry.loadingCatalog")}
                </div>
            ) : error ? (
                <p className="text-sm text-warning">{t("mcp.registry.loadFailed", { error })}</p>
            ) : (
                <div className="space-y-4">
                    {reference.length > 0 && (
                        <RegistrySection
                            title={t("mcp.registry.referenceTitle")}
                            description={t("mcp.registry.referenceDescription")}
                            entries={reference}
                            installing={installing}
                            onInstall={handleInstall}
                            onAskAIInstall={onAskAIInstall}
                        />
                    )}
                    {thirdParty.length > 0 && (
                        <RegistrySection
                            title={t("mcp.registry.thirdPartyTitle")}
                            description={t("mcp.registry.thirdPartyDescription")}
                            entries={thirdParty}
                            installing={installing}
                            onInstall={handleInstall}
                            onAskAIInstall={onAskAIInstall}
                        />
                    )}
                    {reference.length === 0 && thirdParty.length === 0 && (
                        <p className="py-4 text-sm text-muted">
                            {t("mcp.registry.noMatch")}
                        </p>
                    )}
                </div>
            )}
        </SettingsCard>
    );
}

interface RegistrySectionProps {
    title: string;
    description: string;
    entries: RegistryEntry[];
    installing: string | null;
    onInstall: (entry: RegistryEntry) => void;
    onAskAIInstall: (entry: RegistryEntry) => void;
}

function RegistrySection({
    title,
    description,
    entries,
    installing,
    onInstall,
    onAskAIInstall,
}: RegistrySectionProps) {
    return (
        <div className="rounded-md border border-border bg-muted/10">
            <div className="px-3 py-2 border-b border-border">
                <p className="text-sm font-medium text-ink">{title}</p>
                <p className="text-[11px] text-muted">{description}</p>
            </div>
            <ul className="divide-y divide-border">
                {entries.map((entry) => (
                    <RegistryEntryRow
                        key={entry.slug}
                        entry={entry}
                        installing={installing === entry.slug}
                        onInstall={onInstall}
                        onAskAIInstall={onAskAIInstall}
                    />
                ))}
            </ul>
        </div>
    );
}

interface RegistryEntryRowProps {
    entry: RegistryEntry;
    installing: boolean;
    onInstall: (entry: RegistryEntry) => void;
    onAskAIInstall: (entry: RegistryEntry) => void;
}

function RegistryEntryRow({ entry, installing, onInstall, onAskAIInstall }: RegistryEntryRowProps) {
    const { t } = useTranslation("settings");
    return (
        <li className="px-3 py-2 flex items-start gap-3">
            <div className="min-w-0 flex-1">
                <span className="block text-sm text-ink truncate">{entry.name}</span>
                {entry.description && (
                    <p className="text-[11px] text-muted line-clamp-2">
                        {entry.description}
                    </p>
                )}
                {entry.source_url && (
                    <button
                        type="button"
                        onClick={() => BrowserOpenURL(entry.source_url)}
                        className="text-left text-[11px] text-accent-2 hover:underline truncate max-w-full"
                        title={entry.source_url}
                    >
                        {entry.source_url}
                    </button>
                )}
            </div>
            {entry.installable ? (
                <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    onClick={() => onInstall(entry)}
                    disabled={installing}
                >
                    {installing ? (
                        <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    ) : (
                        <Download className="h-3.5 w-3.5" />
                    )}
                    {t("mcp.registry.install")}
                </Button>
            ) : (
                <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    onClick={() => onAskAIInstall(entry)}
                >
                    <Sparkles className="h-3.5 w-3.5" />
                    {t("mcp.registry.installViaAI")}
                </Button>
            )}
        </li>
    );
}

// ── Advanced: add a server manually ──

/** A folded group inside a card, styled like the page's Advanced trigger. */
function FoldedSection({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
    const [open, setOpen] = useState(false);
    return (
        <Collapsible open={open} onOpenChange={setOpen} className="mt-4 flex flex-col">
            <CollapsibleTrigger className="flex items-center gap-1.5 self-start text-left text-xs text-muted hover:text-ink transition-colors">
                <ChevronDown
                    aria-hidden
                    className={`h-3 w-3 shrink-0 transition-transform ${open ? "rotate-180" : ""}`}
                />
                <span className="font-medium">{title}</span>
                {hint && <span className="text-muted">{hint}</span>}
            </CollapsibleTrigger>
            <CollapsibleContent className="pt-3">{children}</CollapsibleContent>
        </Collapsible>
    );
}

interface ManualFieldErrors {
    id?: string;
    command?: string;
    url?: string;
}

interface ManualAddCardProps {
    form: NewServerForm;
    errors: ManualFieldErrors;
    setForm: (next: NewServerForm | ((prev: NewServerForm) => NewServerForm)) => void;
    adding: boolean;
    onAdd: () => void;
}

function ManualAddCard({ form, errors, setForm, adding, onAdd }: ManualAddCardProps) {
    const { t } = useTranslation("settings");
    const isHTTP = form.transport === "http";
    return (
        <SettingsCard title={t("mcp.manual.title")} description={t("mcp.manual.description")}>
            <div className="mb-4">
                <label className="block text-xs text-muted mb-1">{t("mcp.manual.transport")}</label>
                <div className="inline-flex rounded-md border border-border bg-muted/20 p-0.5">
                    {(["stdio", "http"] as const).map((transport) => (
                        <button
                            key={transport}
                            type="button"
                            aria-pressed={form.transport === transport}
                            onClick={() =>
                                setForm((f) => ({
                                    ...f,
                                    transport,
                                    authMode: transport === "http" ? f.authMode : "none",
                                }))
                            }
                            className={
                                "px-3 py-1.5 text-xs rounded transition-colors " +
                                (form.transport === transport
                                    ? "bg-background text-ink shadow-sm"
                                    : "text-muted hover:text-ink")
                            }
                        >
                            {transport === "stdio" ? t("mcp.manual.localStdio") : t("mcp.manual.remoteHTTP")}
                        </button>
                    ))}
                </div>
            </div>

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                <ManualField
                    label={t("mcp.manual.idLabel")}
                    placeholder={isHTTP ? t("mcp.manual.idPlaceholderRemote") : t("mcp.manual.idPlaceholderLocal")}
                    value={form.id}
                    onChange={(value) => setForm((f) => ({ ...f, id: value }))}
                    helper={t("mcp.manual.idHelper")}
                    error={errors.id}
                />
                <ManualField
                    label={t("mcp.manual.displayNameLabel")}
                    placeholder={isHTTP ? t("mcp.manual.namePlaceholderRemote") : t("mcp.manual.namePlaceholderLocal")}
                    value={form.name}
                    onChange={(value) => setForm((f) => ({ ...f, name: value }))}
                    helper={t("mcp.manual.displayNameHelper")}
                />
                {isHTTP ? (
                    <ManualField
                        label={t("mcp.manual.urlLabel")}
                        placeholder={t("mcp.manual.urlPlaceholder")}
                        value={form.url}
                        onChange={(value) => setForm((f) => ({ ...f, url: value }))}
                        helper={t("mcp.manual.urlHelper")}
                        error={errors.url}
                    />
                ) : (
                    <>
                        <ManualField
                            label={t("mcp.manual.commandLabel")}
                            placeholder={t("mcp.manual.commandPlaceholder")}
                            value={form.command}
                            onChange={(value) => setForm((f) => ({ ...f, command: value }))}
                            helper={t("mcp.manual.commandHelper")}
                            error={errors.command}
                        />
                        <ManualField
                            label={t("mcp.manual.argsLabel")}
                            placeholder={t("mcp.manual.argsPlaceholder")}
                            value={form.argsRaw}
                            onChange={(value) => setForm((f) => ({ ...f, argsRaw: value }))}
                            helper={t("mcp.manual.argsHelper")}
                        />
                    </>
                )}
            </div>

            {isHTTP && (
                <FoldedSection title={t("mcp.manual.signInTitle")} hint={t("mcp.manual.signInHint")}>
                    <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                        <ManualField
                            label={t("mcp.manual.authLabel")}
                            custom={
                                <div className="inline-flex rounded-md border border-border bg-muted/20 p-0.5">
                                    {(["none", "oauth"] as const).map((mode) => (
                                        <button
                                            key={mode}
                                            type="button"
                                            aria-pressed={form.authMode === mode}
                                            onClick={() => setForm((f) => ({ ...f, authMode: mode }))}
                                            className={
                                                "px-3 py-1.5 text-xs rounded transition-colors " +
                                                (form.authMode === mode
                                                    ? "bg-background text-ink shadow-sm"
                                                    : "text-muted hover:text-ink")
                                            }
                                        >
                                            {mode === "none" ? t("mcp.manual.authNone") : t("mcp.manual.authOAuth")}
                                        </button>
                                    ))}
                                </div>
                            }
                            helper={t("mcp.manual.authHelper")}
                        />
                        {form.authMode === "oauth" && (
                            <>
                                <ManualField
                                    label={t("mcp.manual.clientIdLabel")}
                                    placeholder={t("mcp.manual.clientIdPlaceholder")}
                                    value={form.oauthClientID}
                                    onChange={(value) => setForm((f) => ({ ...f, oauthClientID: value }))}
                                    helper={t("mcp.manual.clientIdHelper")}
                                />
                                <ManualField
                                    label={t("mcp.manual.scopesLabel")}
                                    placeholder={t("mcp.manual.scopesPlaceholder")}
                                    value={form.oauthScopesRaw}
                                    onChange={(value) => setForm((f) => ({ ...f, oauthScopesRaw: value }))}
                                    helper={t("mcp.manual.scopesHelper")}
                                />
                                <ManualField
                                    label={t("mcp.manual.authEndpointLabel")}
                                    placeholder={t("mcp.manual.authEndpointPlaceholder")}
                                    value={form.oauthAuthURL}
                                    onChange={(value) => setForm((f) => ({ ...f, oauthAuthURL: value }))}
                                    helper={t("mcp.manual.authEndpointHelper")}
                                />
                                <ManualField
                                    label={t("mcp.manual.tokenEndpointLabel")}
                                    placeholder={t("mcp.manual.tokenEndpointPlaceholder")}
                                    value={form.oauthTokenURL}
                                    onChange={(value) => setForm((f) => ({ ...f, oauthTokenURL: value }))}
                                    helper={t("mcp.manual.tokenEndpointHelper")}
                                />
                                <ManualField
                                    label={t("mcp.manual.resourceLabel")}
                                    placeholder={t("mcp.manual.resourcePlaceholder")}
                                    value={form.oauthResource}
                                    onChange={(value) => setForm((f) => ({ ...f, oauthResource: value }))}
                                    helper={t("mcp.manual.resourceHelper")}
                                />
                            </>
                        )}
                    </div>
                </FoldedSection>
            )}

            <FoldedSection
                title={t("mcp.manual.moreOptionsTitle")}
                hint={isHTTP ? t("mcp.manual.moreOptionsHintRemote") : t("mcp.manual.moreOptionsHintLocal")}
            >
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                    {!isHTTP && (
                        <ManualField
                            label={t("mcp.manual.workDirLabel")}
                            placeholder={t("mcp.manual.workDirHelper")}
                            value={form.workDir}
                            onChange={(value) => setForm((f) => ({ ...f, workDir: value }))}
                            helper={t("mcp.manual.workDirHelper")}
                        />
                    )}
                    <ManualField
                        label={t("mcp.manual.autoStartLabel")}
                        custom={
                            <label className="flex items-center gap-2 mt-1 text-sm text-ink">
                                <Switch
                                    checked={form.autoStart}
                                    onCheckedChange={(checked) =>
                                        setForm((f) => ({ ...f, autoStart: checked }))
                                    }
                                />
                                <span className="text-muted">{t("mcp.manual.autoStartDesc")}</span>
                            </label>
                        }
                    />
                </div>
                {!isHTTP && (
                    <div className="mt-3">
                        <label className="block text-xs text-muted mb-1">
                            {t("mcp.manual.envLabel")}
                        </label>
                        <textarea
                            rows={3}
                            value={form.envRaw}
                            onChange={(event) => setForm((f) => ({ ...f, envRaw: event.target.value }))}
                            placeholder={t("mcp.manual.envPlaceholder")}
                            className="w-full rounded-md border border-border bg-background px-3 py-2 text-xs font-mono text-ink placeholder:text-muted/60 focus:outline-none focus:ring-1 focus:ring-accent-2/40"
                        />
                    </div>
                )}
            </FoldedSection>

            <div className="mt-4 flex justify-end gap-2">
                <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => setForm(blankForm)}
                    disabled={adding}
                >
                    <X className="h-3.5 w-3.5" />
                    {t("mcp.manual.clear")}
                </Button>
                <Button
                    type="button"
                    size="sm"
                    onClick={onAdd}
                    disabled={adding}
                >
                    {adding ? (
                        <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    ) : (
                        <Plus className="h-3.5 w-3.5" />
                    )}
                    {t("mcp.manual.addServer")}
                </Button>
            </div>
        </SettingsCard>
    );
}

interface ManualFieldProps {
    label: string;
    placeholder?: string;
    value?: string;
    onChange?: (value: string) => void;
    helper?: string;
    error?: string;
    custom?: React.ReactNode;
}

function ManualField({ label, placeholder, value, onChange, helper, error, custom }: ManualFieldProps) {
    return (
        <div>
            {custom ? (
                <>
                    <span className="block text-xs text-muted mb-1">{label}</span>
                    {custom}
                </>
            ) : (
                <label className="block text-xs text-muted">
                    {label}
                    <input
                        type="text"
                        value={value ?? ""}
                        onChange={(event) => onChange?.(event.target.value)}
                        placeholder={placeholder}
                        className="mt-1 h-9 w-full rounded-md border border-border bg-background px-3 text-sm text-ink placeholder:text-muted/60 focus:outline-none focus:ring-1 focus:ring-accent-2/40"
                    />
                </label>
            )}
            {error ? (
                <p role="alert" className="text-[11px] text-copper mt-1">{error}</p>
            ) : (
                helper && <p className="text-[11px] text-muted mt-1">{helper}</p>
            )}
        </div>
    );
}

// ── Advanced: per-server details ──

interface ServerDetailsCardProps {
    servers: McpServer[];
    updating: string | null;
    expanded: Record<string, boolean>;
    onExpandedChange: (id: string, open: boolean) => void;
    onTrustedChange: (server: McpServer, trusted: boolean) => void;
    onRestart: (server: McpServer) => void;
    onStop: (server: McpServer) => void;
    onOpenFolder: (server: McpServer) => void;
}

function ServerDetailsCard({
    servers,
    updating,
    expanded,
    onExpandedChange,
    onTrustedChange,
    onRestart,
    onStop,
    onOpenFolder,
}: ServerDetailsCardProps) {
    const { t } = useTranslation("settings");
    return (
        <SettingsCard title={t("mcp.details.title")} description={t("mcp.details.description")}>
            {servers.length === 0 ? (
                <p className="text-sm text-muted">{t("mcp.details.empty")}</p>
            ) : (
                <div className="divide-y divide-border">
                    {servers.map((server) => (
                        <ServerDetailsRow
                            key={server.id}
                            server={server}
                            expanded={Boolean(expanded[server.id])}
                            updating={updating === server.id}
                            onExpandedChange={(open) => onExpandedChange(server.id, open)}
                            onTrustedChange={onTrustedChange}
                            onRestart={onRestart}
                            onStop={onStop}
                            onOpenFolder={onOpenFolder}
                        />
                    ))}
                </div>
            )}
        </SettingsCard>
    );
}

interface ServerDetailsRowProps {
    server: McpServer;
    expanded: boolean;
    updating: boolean;
    onExpandedChange: (open: boolean) => void;
    onTrustedChange: (server: McpServer, trusted: boolean) => void;
    onRestart: (server: McpServer) => void;
    onStop: (server: McpServer) => void;
    onOpenFolder: (server: McpServer) => void;
}

function ServerDetailsRow({
    server,
    expanded,
    updating,
    onExpandedChange,
    onTrustedChange,
    onRestart,
    onStop,
    onOpenFolder,
}: ServerDetailsRowProps) {
    const { t } = useTranslation("settings");
    const health = healthState(server.health);
    const usesOAuth = server.auth_mode === "oauth";
    const [logs, setLogs] = useState<string[]>([]);
    const [logsError, setLogsError] = useState<string | null>(null);
    const [logsLoading, setLogsLoading] = useState(false);

    const loadLogs = useCallback(async () => {
        setLogsLoading(true);
        try {
            const lines = await TailMcpServerLog(server.id, 50);
            setLogs(lines ?? []);
            setLogsError(null);
        } catch (err) {
            // Don't toast — logs are informational and a stopped server
            // legitimately has no log stream to read from.
            setLogs([]);
            setLogsError(errorMessage(err));
        } finally {
            setLogsLoading(false);
        }
    }, [server.id]);

    useEffect(() => {
        if (expanded) {
            void loadLogs();
        }
    }, [expanded, loadLogs]);

    return (
        <Collapsible open={expanded} onOpenChange={onExpandedChange}>
            <div className="py-3">
                <CollapsibleTrigger asChild>
                    <button
                        type="button"
                        className="w-full flex items-center gap-2 text-left"
                        aria-label={t("mcp.details.toggleAria", { name: server.name })}
                    >
                        <span
                            aria-hidden
                            className={`h-2 w-2 rounded-full shrink-0 ${HEALTH_DOT_CLASS[health]}`}
                        />
                        <span className="settings-row-title truncate">{server.name}</span>
                        <span className="text-[11px] text-muted font-mono truncate">{server.id}</span>
                        <span className="ml-auto text-[11px] text-muted shrink-0">{t(`mcp.health.${health}`)}</span>
                        <ChevronDown
                            aria-hidden
                            className={`h-4 w-4 shrink-0 text-muted transition-transform ${expanded ? "rotate-180" : ""}`}
                        />
                    </button>
                </CollapsibleTrigger>

                <CollapsibleContent>
                    <div className="mt-3 rounded-md border border-border bg-muted/20 p-3 space-y-3">
                        {server.transport === "http" ? (
                            <div>
                                <p className="text-xs text-muted mb-1">{t("mcp.installed.endpoint")}</p>
                                <p className="font-mono text-xs text-ink break-all">{server.url}</p>
                                {usesOAuth && (
                                    <p className="text-[11px] text-muted mt-1">
                                        {t("mcp.details.signIn", {
                                            status: t(`mcp.oauth.status.${oauthState(server.oauth_status)}`),
                                        })}
                                    </p>
                                )}
                            </div>
                        ) : (
                            <div>
                                <p className="text-xs text-muted mb-1">{t("mcp.installed.command")}</p>
                                <p className="font-mono text-xs text-ink break-all">
                                    {server.command}{" "}
                                    {server.args && server.args.length > 0
                                        ? server.args.join(" ")
                                        : ""}
                                </p>
                            </div>
                        )}

                        {server.last_error && health !== "healthy" && (
                            <div>
                                <p className="text-xs text-muted mb-1">{t("mcp.details.lastError")}</p>
                                <p className="text-[11px] text-warning break-words">{server.last_error}</p>
                            </div>
                        )}

                        {server.tools && server.tools.length > 0 && (
                            <div>
                                <p className="text-xs text-muted mb-1">{t("mcp.installed.tools")}</p>
                                <ul className="space-y-1">
                                    {server.tools.map((tool) => (
                                        <li key={tool.name} className="text-xs">
                                            <span className="font-mono text-ink">{tool.name}</span>
                                            {tool.description && (
                                                <span className="text-muted"> — {tool.description}</span>
                                            )}
                                        </li>
                                    ))}
                                </ul>
                            </div>
                        )}

                        <div>
                            <div className="flex items-center justify-between">
                                <p className="text-xs text-muted">{t("mcp.installed.recentStderr")}</p>
                                <Button
                                    type="button"
                                    variant="ghost"
                                    size="sm"
                                    onClick={loadLogs}
                                    disabled={logsLoading}
                                    aria-label={t("mcp.details.reloadLogs")}
                                    title={t("mcp.details.reloadLogs")}
                                >
                                    {logsLoading ? (
                                        <Loader2 className="h-3 w-3 animate-spin" />
                                    ) : (
                                        <RefreshCw className="h-3 w-3" />
                                    )}
                                </Button>
                            </div>
                            {logsError ? (
                                <p className="text-[11px] text-muted">
                                    {t("mcp.details.logsUnavailable", { error: logsError })}
                                </p>
                            ) : logs.length === 0 ? (
                                <p className="text-[11px] text-muted">
                                    {logsLoading ? t("mcp.installed.loadingLogs") : t("mcp.installed.noLogs")}
                                </p>
                            ) : (
                                <pre className="mt-1 max-h-40 overflow-auto rounded bg-background/60 p-2 text-[11px] font-mono text-muted whitespace-pre-wrap break-all">
                                    {logs.join("\n")}
                                </pre>
                            )}
                        </div>

                        <div className="flex items-start justify-between gap-3 rounded-md border border-border bg-background/40 p-3">
                            <div className="min-w-0">
                                <div className="flex items-center gap-1.5">
                                    <ShieldCheck className="h-3.5 w-3.5 text-muted" />
                                    <p className="text-xs font-medium text-ink">
                                        {t("mcp.trusted.label")}
                                    </p>
                                </div>
                                <p className="mt-0.5 text-[11px] text-muted">
                                    {t("mcp.trusted.description")}
                                </p>
                            </div>
                            <Switch
                                checked={Boolean(server.trusted)}
                                onCheckedChange={(checked) => onTrustedChange(server, checked)}
                                disabled={updating}
                                aria-label={t("mcp.trusted.label")}
                            />
                        </div>

                        <div className="flex flex-wrap items-center justify-end gap-2 pt-1">
                            {!server.enabled && (
                                <p className="mr-auto text-[11px] text-muted">{t("mcp.details.offReason")}</p>
                            )}
                            <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                onClick={() => onOpenFolder(server)}
                            >
                                <FolderOpen className="h-3.5 w-3.5" />
                                {t("mcp.installed.folder")}
                            </Button>
                            <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                onClick={() => onStop(server)}
                                disabled={updating || health !== "healthy"}
                            >
                                <StopCircle className="h-3.5 w-3.5" />
                                {t("mcp.installed.stop")}
                            </Button>
                            <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                onClick={() => onRestart(server)}
                                disabled={updating || !server.enabled}
                            >
                                <RefreshCw className="h-3.5 w-3.5" />
                                {t("mcp.installed.restart")}
                            </Button>
                        </div>
                    </div>
                </CollapsibleContent>
            </div>
        </Collapsible>
    );
}
