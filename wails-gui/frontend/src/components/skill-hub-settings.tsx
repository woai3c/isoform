import { useCallback, useEffect, useMemo, useState } from "react";
import i18n from "@/i18n";
import { SettingsFailureLine, SettingsPlaceholderRows, useSettingsFailure } from "@/components/settings/settings-failure";
import { Trans, useTranslation } from "react-i18next";
import {
    ChevronDown,
    Download,
    ExternalLink,
    FolderOpen,
    FolderInput,
    Github,
    Loader2,
    Plus,
    RefreshCw,
    Search,
    Trash2,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import { Switch } from "@/components/ui/switch";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { HubTabs } from "@/components/mcp-hub-settings";
import { SettingsCard } from "@/components/settings/settings-primitives";
import { SettingsAdvanced } from "@/components/settings/settings-advanced";
import {
    InstallClawHubSkill,
    InstallSkillFromAgentDir,
    InstallSkillFromGitHub,
    InstallSkillFromLocal,
    DiscoverAgentSkills,
    ListSkillHubInstalledSkills,
    OpenSkillHubSkillFolder,
    SearchAgentSkills,
    SearchClawHubSkills,
    SelectLocalSkillDir,
    SetSkillHubSkillDomains,
    SetSkillHubSkillEnabled,
    UninstallSkillHubSkill,
} from "../../wailsjs/go/main/App";
import { BrowserOpenURL } from "../../wailsjs/runtime/runtime";
import type { main } from "../../wailsjs/go/models";

// ── Skills (Settings page id "skills") ──
//
// Two tabs: Installed (your skills, with on/off and where each is used) and
// Add (the skill library, and skills found in your other AI tools). Installing
// from GitHub or a folder, and where each skill is stored, sit in the page's
// one Advanced section at the bottom.

type InstalledSkill = main.SkillHubInstalledSkill;
type CatalogSkill = main.SkillHubCatalogSkill;
type DiscoveredSkill = main.SkillHubDiscoveredSkill;

type SkillTab = "installed" | "add";

const SOURCE_LABEL_KEYS = {
    clawhub: "clawhub",
    github: "github",
    local: "local",
    "agent-dirs": "agentDirs",
} as const;

type SourceLabelKey = (typeof SOURCE_LABEL_KEYS)[keyof typeof SOURCE_LABEL_KEYS];

function sourceLabelKey(source: string | undefined): SourceLabelKey | null {
    if (!source) return null;
    return (SOURCE_LABEL_KEYS as Record<string, SourceLabelKey>)[source] ?? null;
}

const SKILL_DOMAINS = [
    { id: "chat" },
    { id: "project" },
    { id: "automation" },
] as const;

type SecurityKey = "clean" | "suspicious" | "malicious" | "none" | "unknown";

/** The library's safety review (moderationStatus in skill_hub.go): "clean",
 *  "suspicious", "malicious", another verdict, or empty for skills that were
 *  not installed from the library. */
function securityKey(status: string | undefined): SecurityKey {
    if (!status) return "none";
    if (status === "clean" || status === "suspicious" || status === "malicious") return status;
    return "unknown";
}

/** A status worth flagging on the row itself: anything the review did not
 *  clear. Skills that were never reviewed are not flagged. */
function isFlagged(key: SecurityKey): boolean {
    return key === "suspicious" || key === "malicious" || key === "unknown";
}

function upsertSkill(current: InstalledSkill[], skill: InstalledSkill): InstalledSkill[] {
    const next = current.filter((item) => item.id !== skill.id);
    return [...next, skill].sort((a, b) => a.name.localeCompare(b.name));
}


// settings-review ST65: one line a person reads. A skill's description is
// written for the AI; show its first sentence only when it reads as a
// summary in the app's language, else the name stands alone. Never cut.
const SKILL_SUMMARY_MAX = 120;
export function skillSummaryLine(description: string | undefined, language: string): string | null {
    const text = (description ?? "").replace(/\s+/g, " ").trim();
    if (!text) return null;
    const first = (text.match(/^.+?[.!?。！？](?=\s|$)/)?.[0] ?? text).trim();
    if (first.length > SKILL_SUMMARY_MAX) return null;
    if (/\b(IMPORTANT|MUST|ALWAYS|NEVER|NOTE)\b/.test(text)) return null;
    if (/^(use|invoke|call|trigger|run)\b/i.test(first)) return null;
    if (/\b(the (ai|agent|assistant|model|llm)|you should|when the user)\b/i.test(first)) return null;
    const cjk = /[\u3400-\u9fff]/.test(first);
    if (!language.startsWith("zh") && cjk) return null;
    return first;
}

export function SkillHubSettings() {
    const { t } = useTranslation('settings');
    const { failure: changeFailure, fail: failChange } = useSettingsFailure();
    const [loadFailed, setLoadFailed] = useState(false);
    const [activeTab, setActiveTab] = useState<SkillTab>("installed");
    const [installed, setInstalled] = useState<InstalledSkill[]>([]);
    const [results, setResults] = useState<CatalogSkill[]>([]);
    const [discovered, setDiscovered] = useState<DiscoveredSkill[]>([]);
    const [query, setQuery] = useState("");
    const [discoverQuery, setDiscoverQuery] = useState("");
    const [githubRef, setGithubRef] = useState("");
    const [installingGitHub, setInstallingGitHub] = useState(false);
    const [installingLocal, setInstallingLocal] = useState(false);
    const [loading, setLoading] = useState(true);
    const [searching, setSearching] = useState(false);
    const [discovering, setDiscovering] = useState(false);
    const [didDiscover, setDidDiscover] = useState(false);
    const [installing, setInstalling] = useState<string | null>(null);
    const [installingDiscovered, setInstallingDiscovered] = useState<string | null>(null);
    const [updating, setUpdating] = useState<string | null>(null);
    const [expanded, setExpanded] = useState<Record<string, boolean>>({});
    const [pendingUninstall, setPendingUninstall] = useState<InstalledSkill | null>(null);

    const installedBySlug = useMemo(() => {
        const map = new Map<string, InstalledSkill>();
        installed.forEach((skill) => map.set(skill.slug, skill));
        return map;
    }, [installed]);

    const installedBySourceURL = useMemo(() => {
        const map = new Map<string, InstalledSkill>();
        installed.forEach((skill) => {
            if (skill.source_url) map.set(skill.source_url, skill);
        });
        return map;
    }, [installed]);

    const refreshInstalled = useCallback(async () => {
        setLoading(true);
        try {
            const skills = await ListSkillHubInstalledSkills();
            setInstalled(skills ?? []);
            setLoadFailed(false);
        } catch {
            setLoadFailed(true);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        let mounted = true;
        setLoading(true);
        ListSkillHubInstalledSkills()
            .then((skills) => {
                if (mounted) setInstalled(skills ?? []);
            })
            .catch(() => {
                if (mounted) setLoadFailed(true);
            })
            .finally(() => {
                if (mounted) setLoading(false);
            });
        return () => {
            mounted = false;
        };
    }, []);

    const handleSearch = useCallback(async () => {
        setSearching(true);
        try {
            const skills = await SearchClawHubSkills(query.trim());
            setResults(skills);
        } catch {
            failChange(t('skillHub.toasts.searchFailed'), () => void handleSearch());
        } finally {
            setSearching(false);
        }
    }, [query, t]);

    const handleInstall = useCallback(
        async (ref: string) => {
            const target = ref.trim();
            if (!target) return;
            setInstalling(target);
            try {
                const skill = await InstallClawHubSkill(target);
                setInstalled((current) => upsertSkill(current, skill));
                setExpanded((current) => ({ ...current, [skill.id]: false }));
                if (!query.trim()) setQuery(skill.slug);
            } catch {
                failChange(t('skillHub.toasts.installFailed', { name: target }), () => void handleInstall(target));
            } finally {
                setInstalling(null);
            }
        },
        [query, t],
    );

    const handleInstallFromGitHub = useCallback(async () => {
        const target = githubRef.trim();
        if (!target) return;
        setInstallingGitHub(true);
        try {
            const skill = await InstallSkillFromGitHub(target);
            setInstalled((current) => upsertSkill(current, skill));
            setExpanded((current) => ({ ...current, [skill.id]: false }));
            setGithubRef("");
        } catch {
            failChange(t('skillHub.toasts.githubInstallFailed', { name: target }), () => void handleInstallFromGitHub());
        } finally {
            setInstallingGitHub(false);
        }
    }, [githubRef, t]);

    const handleInstallFromLocal = useCallback(async () => {
        setInstallingLocal(true);
        try {
            const dir = await SelectLocalSkillDir();
            if (!dir) return;
            const skill = await InstallSkillFromLocal(dir);
            setInstalled((current) => upsertSkill(current, skill));
            setExpanded((current) => ({ ...current, [skill.id]: false }));
        } catch {
            failChange(t('skillHub.toasts.localInstallFailed'), () => void handleInstallFromLocal());
        } finally {
            setInstallingLocal(false);
        }
    }, [t]);

    const handleDiscoverAgentSkills = useCallback(async () => {
        setDiscovering(true);
        try {
            const skills = discoverQuery.trim()
                ? await SearchAgentSkills(discoverQuery.trim())
                : await DiscoverAgentSkills();
            setDiscovered(skills);
            setDidDiscover(true);
        } catch {
            failChange(t('skillHub.discover.discoveryFailed'), () => void handleDiscoverAgentSkills());
        } finally {
            setDiscovering(false);
        }
    }, [discoverQuery, t]);

    const handleInstallDiscovered = useCallback(async (skill: DiscoveredSkill) => {
        setInstallingDiscovered(skill.id);
        try {
            const installedSkill = await InstallSkillFromAgentDir(skill.id);
            setInstalled((current) => upsertSkill(current, installedSkill));
            setDiscovered((current) =>
                current.map((item) =>
                    item.id === skill.id
                        ? { ...item, installed: true, installed_id: installedSkill.id }
                        : item,
                ),
            );
            setExpanded((current) => ({ ...current, [installedSkill.id]: false }));
        } catch {
            failChange(t('skillHub.discover.installFailed', { name: skill.name }), () => void handleInstallDiscovered(skill));
        } finally {
            setInstallingDiscovered(null);
        }
    }, [t]);

    const replaceInstalled = useCallback((skill: InstalledSkill) => {
        setInstalled((current) => current.map((item) => (item.id === skill.id ? skill : item)));
    }, []);

    const handleEnabledChange = useCallback(
        async (skill: InstalledSkill, enabled: boolean) => {
            setUpdating(skill.id);
            try {
                const updated = await SetSkillHubSkillEnabled(skill.id, enabled);
                replaceInstalled(updated);
            } catch {
                failChange(t('skillHub.toasts.updateFailed', { name: skill.name }), () => void handleEnabledChange(skill, enabled));
            } finally {
                setUpdating(null);
            }
        },
        [replaceInstalled, t],
    );

    const handleDomainChange = useCallback(
        async (skill: InstalledSkill, domain: string, enabled: boolean) => {
            const domains = { ...(skill.domains ?? {}), [domain]: enabled };
            setUpdating(skill.id);
            try {
                const updated = await SetSkillHubSkillDomains(skill.id, domains);
                replaceInstalled(updated);
            } catch {
                failChange(t('skillHub.toasts.updateAreasFailed', { name: skill.name }), () => void handleDomainChange(skill, domain, enabled));
            } finally {
                setUpdating(null);
            }
        },
        [replaceInstalled, t],
    );

    const handleUninstall = useCallback(
        async (skill: InstalledSkill) => {
            setUpdating(skill.id);
            try {
                await UninstallSkillHubSkill(skill.id);
                setInstalled((current) => current.filter((item) => item.id !== skill.id));
                setExpanded((current) => {
                    const next = { ...current };
                    delete next[skill.id];
                    return next;
                });
            } catch {
                failChange(t('skillHub.toasts.uninstallFailed', { name: skill.name }), () => void handleUninstall(skill));
            } finally {
                setUpdating(null);
            }
        },
        [t],
    );

    const handleOpenFolder = useCallback(async (skill: InstalledSkill) => {
        try {
            await OpenSkillHubSkillFolder(skill.id);
        } catch {
            failChange(t('skillHub.toasts.openFolderFailed'), () => void handleOpenFolder(skill));
        }
    }, [t]);

    const tabs = useMemo(
        () => [
            { id: "installed" as const, label: t('skillHub.tabs.installed') },
            { id: "add" as const, label: t('skillHub.tabs.add') },
        ],
        [t],
    );

    return (
        <div className="space-y-6">
            <HubTabs
                label={t('skillHub.tabs.label')}
                items={tabs}
                active={activeTab}
                onChange={setActiveTab}
            />

            <div className={activeTab === "installed" ? undefined : "hidden"}>
                <SettingsCard
                    title={t('skillHub.installed.title')}
                    description={t('skillHub.installed.description')}
                    headerRight={
                        <Button type="button" variant="outline" size="sm" onClick={refreshInstalled} disabled={loading}>
                            {loading ? (
                                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                            ) : (
                                <RefreshCw className="h-3.5 w-3.5" />
                            )}
                            {t('skillHub.installed.refresh')}
                        </Button>
                    }
                >
                    {loadFailed && installed.length === 0 ? (
                        <SettingsFailureLine
                            className="py-4"
                            failure={{ text: t('skillHub.toasts.loadFailed'), retry: () => void refreshInstalled() }}
                        />
                    ) : loading ? (
                        <SettingsPlaceholderRows rows={3} />
                    ) : installed.length === 0 ? (
                        <div className="py-6 flex flex-col items-start gap-3">
                            <p className="text-sm text-muted">{t('skillHub.installed.empty')}</p>
                            <Button type="button" variant="outline" size="sm" onClick={() => setActiveTab("add")}>
                                <Plus className="h-3.5 w-3.5" />
                                {t('skillHub.installed.emptyAction')}
                            </Button>
                        </div>
                    ) : (
                        <div className="divide-y divide-border">
                            {installed.map((skill) => (
                                <InstalledSkillRow
                                    key={skill.id}
                                    skill={skill}
                                    expanded={Boolean(expanded[skill.id])}
                                    updating={updating === skill.id}
                                    onExpandedChange={(open) =>
                                        setExpanded((current) => ({ ...current, [skill.id]: open }))
                                    }
                                    onEnabledChange={handleEnabledChange}
                                    onDomainChange={handleDomainChange}
                                    onUninstall={setPendingUninstall}
                                />
                            ))}
                        </div>
                    )}
                </SettingsCard>
            </div>

            <div className={activeTab === "add" ? "space-y-6" : "hidden"}>
                <SettingsCard
                    title={t('skillHub.clawhub.cardTitle')}
                    description={t('skillHub.clawhub.cardDescription')}
                >
                    <div className="flex gap-2">
                        <input
                            type="search"
                            value={query}
                            onChange={(event) => {
                                const next = event.target.value;
                                setQuery(next);
                                if (!next.trim()) {
                                    setResults([]);
                                }
                            }}
                            onKeyDown={(event) => {
                                if (event.key === "Enter") {
                                    event.preventDefault();
                                    void handleSearch();
                                }
                            }}
                            placeholder={t('skillHub.clawhub.searchPlaceholder')}
                            aria-label={t('skillHub.clawhub.cardTitle')}
                            className="h-9 min-w-0 flex-1 rounded-md border border-border bg-background px-3 text-sm text-ink placeholder:text-muted/60 focus:outline-none focus:ring-1 focus:ring-accent-2/40"
                        />
                        <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            onClick={handleSearch}
                            disabled={searching}
                        >
                            {searching ? (
                                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                            ) : (
                                <Search className="h-3.5 w-3.5" />
                            )}
                            {query.trim() ? t('skillHub.clawhub.search') : t('skillHub.clawhub.browse')}
                        </Button>
                        <Button
                            type="button"
                            size="sm"
                            onClick={() => handleInstall(query)}
                            disabled={!query.trim() || installing === query.trim()}
                        >
                            {installing === query.trim() ? (
                                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                            ) : (
                                <Download className="h-3.5 w-3.5" />
                            )}
                            {t('skillHub.clawhub.install')}
                        </Button>
                    </div>

                    {results.length > 0 && (
                        <div className="mt-4 divide-y divide-border">
                            {results.map((skill) => {
                                const alreadyInstalled = installedBySlug.has(skill.slug);
                                const detailUrl = skill.source_url;
                                const security = securityKey(skill.security_status);
                                return (
                                    <div key={skill.slug} className="group py-3 flex items-center gap-4">
                                        <div className="min-w-0 flex-1">
                                            <div className="flex items-center gap-2 min-w-0">
                                                <span className="settings-row-title truncate">{skill.name}</span>
                                                {detailUrl && (
                                                    <button
                                                        type="button"
                                                        onClick={() => BrowserOpenURL(detailUrl)}
                                                        title={t('skillHub.clawhub.openOnTitle', { url: detailUrl })}
                                                        aria-label={t('skillHub.clawhub.openOnAria')}
                                                        className="invisible group-hover:visible shrink-0 text-muted hover:text-ink rounded -m-1 p-1"
                                                    >
                                                        <ExternalLink className="h-3 w-3" />
                                                    </button>
                                                )}
                                                {skill.version && (
                                                    <span className="text-[11px] text-muted shrink-0">{t('skillHub.installed.version', { version: skill.version })}</span>
                                                )}
                                                {isFlagged(security) && (
                                                    <span className="text-[11px] text-warning shrink-0">
                                                        {t(`skillHub.security.${security}`)}
                                                    </span>
                                                )}
                                            </div>
                                            {skillSummaryLine(skill.description, i18n.language) && (
<p className="settings-row-desc">{skillSummaryLine(skill.description, i18n.language)}</p>
)}
                                        </div>
                                        <Button
                                            type="button"
                                            size="sm"
                                            variant={alreadyInstalled ? "outline" : "secondary"}
                                            disabled={alreadyInstalled || installing === skill.slug}
                                            onClick={() => handleInstall(skill.slug)}
                                        >
                                            {installing === skill.slug ? (
                                                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                                            ) : (
                                                <Download className="h-3.5 w-3.5" />
                                            )}
                                            {alreadyInstalled ? t('skillHub.clawhub.installed') : t('skillHub.clawhub.install')}
                                        </Button>
                                    </div>
                                );
                            })}
                        </div>
                    )}
                </SettingsCard>

                <SettingsCard
                    title={t('skillHub.discover.title')}
                    description={t('skillHub.discover.description')}
                >
                    <div className="flex gap-2">
                        <input
                            type="search"
                            value={discoverQuery}
                            onChange={(event) => setDiscoverQuery(event.target.value)}
                            onKeyDown={(event) => {
                                if (event.key === "Enter") {
                                    event.preventDefault();
                                    void handleDiscoverAgentSkills();
                                }
                            }}
                            placeholder={t('skillHub.discover.searchPlaceholder')}
                            aria-label={t('skillHub.discover.searchPlaceholder')}
                            className="h-9 min-w-0 flex-1 rounded-md border border-border bg-background px-3 text-sm text-ink placeholder:text-muted/60 focus:outline-none focus:ring-1 focus:ring-accent-2/40"
                        />
                        <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            onClick={handleDiscoverAgentSkills}
                            disabled={discovering}
                        >
                            {discovering ? (
                                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                            ) : (
                                <Search className="h-3.5 w-3.5" />
                            )}
                            {discoverQuery.trim() ? t('skillHub.discover.search') : t('skillHub.discover.scan')}
                        </Button>
                    </div>

                    {didDiscover && discovered.length === 0 ? (
                        <div className="pt-6 pb-2 text-sm text-muted">
                            {t('skillHub.discover.noMatch')}
                        </div>
                    ) : discovered.length > 0 ? (
                        <div className="mt-4 divide-y divide-border">
                            {discovered.map((skill) => {
                                const alreadyInstalled =
                                    skill.installed || installedBySourceURL.has(skill.source_url);
                                return (
                                    <div key={skill.id} className="py-3 flex items-center gap-4">
                                        <div className="min-w-0 flex-1">
                                            <div className="flex items-center gap-2 min-w-0">
                                                <span className="settings-row-title truncate" title={skill.path}>{skill.name}</span>
                                                <span className="text-[11px] text-muted shrink-0">{skill.agent_name}</span>
                                            </div>
                                            {skillSummaryLine(skill.description, i18n.language) && (
<p className="settings-row-desc">{skillSummaryLine(skill.description, i18n.language)}</p>
)}
                                        </div>
                                        <Button
                                            type="button"
                                            size="sm"
                                            variant={alreadyInstalled ? "outline" : "secondary"}
                                            disabled={alreadyInstalled || installingDiscovered === skill.id}
                                            onClick={() => handleInstallDiscovered(skill)}
                                        >
                                            {installingDiscovered === skill.id ? (
                                                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                                            ) : (
                                                <Download className="h-3.5 w-3.5" />
                                            )}
                                            {alreadyInstalled ? t('skillHub.discover.installed') : t('skillHub.discover.install')}
                                        </Button>
                                    </div>
                                );
                            })}
                        </div>
                    ) : null}
                </SettingsCard>
            </div>

            <SettingsAdvanced pageId="skills" hint={t('skillHub.advancedHint')}>
                <SettingsCard title={t('skillHub.github.cardTitle')}>
                    <p className="settings-card-desc mb-3">
                        <Trans
                            i18nKey="skillHub.github.cardDescription"
                            ns="settings"
                            components={[
                                <code key="0" />,
                                <code key="1" className="ml-1" />,
                                <code key="2" className="ml-1" />,
                            ]}
                        />
                    </p>
                    <p className="mb-4 rounded-md border border-warning-border bg-warning-bg px-3 py-2 text-xs text-warning">
                        {t('skillHub.github.warning')}
                    </p>
                    <div className="flex gap-2">
                        <input
                            type="text"
                            value={githubRef}
                            onChange={(event) => setGithubRef(event.target.value)}
                            onKeyDown={(event) => {
                                if (event.key === "Enter") {
                                    event.preventDefault();
                                    void handleInstallFromGitHub();
                                }
                            }}
                            placeholder={t('skillHub.github.placeholder')}
                            aria-label={t('skillHub.github.cardTitle')}
                            className="h-9 min-w-0 flex-1 rounded-md border border-border bg-background px-3 text-sm text-ink placeholder:text-muted/60 focus:outline-none focus:ring-1 focus:ring-accent-2/40"
                        />
                        <Button
                            type="button"
                            size="sm"
                            onClick={handleInstallFromGitHub}
                            disabled={!githubRef.trim() || installingGitHub}
                        >
                            {installingGitHub ? (
                                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                            ) : (
                                <Github className="h-3.5 w-3.5" />
                            )}
                            {t('skillHub.github.install')}
                        </Button>
                    </div>
                </SettingsCard>

                <SettingsCard
                    title={t('skillHub.folder.cardTitle')}
                    headerRight={
                        <Button
                            type="button"
                            size="sm"
                            onClick={handleInstallFromLocal}
                            disabled={installingLocal}
                        >
                            {installingLocal ? (
                                <Loader2 className="h-3.5 w-3.5 animate-spin" />
                            ) : (
                                <FolderInput className="h-3.5 w-3.5" />
                            )}
                            {t('skillHub.folder.chooseButton')}
                        </Button>
                    }
                >
                    <p className="settings-card-desc">
                        <Trans
                            i18nKey="skillHub.folder.cardDescription"
                            ns="settings"
                            components={[<code key="0" />]}
                        />
                    </p>
                </SettingsCard>

                <SkillFilesCard skills={installed} onOpenFolder={handleOpenFolder} />
            </SettingsAdvanced>

            <ConfirmDialog
                open={pendingUninstall !== null}
                onOpenChange={(open) => {
                    if (!open) setPendingUninstall(null);
                }}
                title={t('skillHub.installed.uninstallConfirm.title', { name: pendingUninstall?.name ?? "" })}
                description={t('skillHub.installed.uninstallConfirm.description')}
                confirmLabel={t('skillHub.installed.uninstallConfirm.confirm')}
                variant="danger"
                onConfirm={() => {
                    if (pendingUninstall) void handleUninstall(pendingUninstall);
                }}
            />
            <SettingsFailureLine failure={changeFailure} />
        </div>
    );
}

function InstalledSkillRow({
    skill,
    expanded,
    updating,
    onExpandedChange,
    onEnabledChange,
    onDomainChange,
    onUninstall,
}: {
    skill: InstalledSkill;
    expanded: boolean;
    updating: boolean;
    onExpandedChange: (open: boolean) => void;
    onEnabledChange: (skill: InstalledSkill, enabled: boolean) => void;
    onDomainChange: (skill: InstalledSkill, domain: string, enabled: boolean) => void;
    onUninstall: (skill: InstalledSkill) => void;
}) {
    const { t } = useTranslation('settings');
    const security = securityKey(skill.security_status);
    return (
        <Collapsible open={expanded} onOpenChange={onExpandedChange}>
            <div className="py-3">
                <div className="flex items-start gap-4">
                    <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2 min-w-0">
                            <span className="settings-row-title truncate">{skill.name}</span>
                            {isFlagged(security) && (
                                <span className="text-[11px] text-warning shrink-0">
                                    {t(`skillHub.security.${security}`)}
                                </span>
                            )}
                        </div>
                        {skillSummaryLine(skill.description, i18n.language) && (
<p className="settings-row-desc">{skillSummaryLine(skill.description, i18n.language)}</p>
)}
                    </div>
                    <div className="flex items-center gap-2 shrink-0">
                        {updating && <Loader2 className="h-3.5 w-3.5 animate-spin text-muted" />}
                        <Switch
                            checked={skill.enabled}
                            onCheckedChange={(checked) => onEnabledChange(skill, checked)}
                            disabled={updating}
                            aria-label={t('skillHub.installed.toggleAria', { name: skill.name })}
                        />
                        {/* Same place and form as Uninstall on Tool servers. */}
                        <Button
                            type="button"
                            variant="ghost"
                            size="icon"
                            onClick={() => onUninstall(skill)}
                            disabled={updating}
                            aria-label={t('skillHub.installed.uninstallAria', { name: skill.name })}
                            title={t('skillHub.installed.uninstall')}
                        >
                            <Trash2 className="h-3.5 w-3.5" />
                        </Button>
                        <CollapsibleTrigger asChild>
                            <Button
                                type="button"
                                variant="ghost"
                                size="icon"
                                aria-label={t('skillHub.installed.detailsAria', { name: skill.name })}
                            >
                                <ChevronDown
                                    className={`h-4 w-4 transition-transform ${expanded ? "rotate-180" : ""}`}
                                />
                            </Button>
                        </CollapsibleTrigger>
                    </div>
                </div>

                <CollapsibleContent>
                    <div className="mt-3 rounded-md border border-border bg-muted/20 p-3">
                        <p className="text-xs font-medium text-ink">{t('skillHub.installed.useIn')}</p>
                        <p className="mt-0.5 text-[11px] text-muted">
                            {skill.enabled
                                ? t('skillHub.installed.useInHint')
                                : t('skillHub.installed.useInOffReason')}
                        </p>
                        <div className="mt-2 grid grid-cols-3 gap-2">
                            {SKILL_DOMAINS.map((domain) => (
                                <label
                                    key={domain.id}
                                    className={`flex items-center gap-2 text-xs ${skill.enabled ? "text-ink" : "text-muted"}`}
                                >
                                    <Checkbox
                                        checked={skill.domains?.[domain.id] ?? true}
                                        disabled={updating || !skill.enabled}
                                        onCheckedChange={(checked) =>
                                            onDomainChange(skill, domain.id, checked === true)
                                        }
                                    />
                                    <span>{t(`skillHub.domains.${domain.id}`)}</span>
                                </label>
                            ))}
                        </div>
                    </div>
                </CollapsibleContent>
            </div>
        </Collapsible>
    );
}

// ── Advanced: where each skill is stored ──

function SkillFilesCard({
    skills,
    onOpenFolder,
}: {
    skills: InstalledSkill[];
    onOpenFolder: (skill: InstalledSkill) => void;
}) {
    const { t } = useTranslation('settings');
    return (
        <SettingsCard title={t('skillHub.files.title')} description={t('skillHub.files.description')}>
            {skills.length === 0 ? (
                <p className="text-sm text-muted">{t('skillHub.files.empty')}</p>
            ) : (
                <div className="divide-y divide-border">
                    {skills.map((skill) => {
                        const source = sourceLabelKey(skill.source);
                        const security = securityKey(skill.security_status);
                        return (
                            <div key={skill.id} className="py-3 flex items-center gap-4">
                                <div className="min-w-0 flex-1">
                                    <div className="flex items-center gap-2 min-w-0">
                                        <span className="settings-row-title truncate">{skill.name}</span>
                                        {skill.version && (
                                            <span className="text-[11px] text-muted shrink-0">
                                                {t('skillHub.installed.version', { version: skill.version })}
                                            </span>
                                        )}
                                        {source && (
                                            <span className="text-[11px] text-muted shrink-0">
                                                {t(`skillHub.sourceLabels.${source}`)}
                                            </span>
                                        )}
                                    </div>
                                    <p className={`text-[11px] ${isFlagged(security) ? "text-warning" : "text-muted"}`}>
                                        {t('skillHub.files.safety', { status: t(`skillHub.security.${security}`) })}
                                    </p>
                                    {skill.path && (
                                        <p className="truncate font-mono text-[11px] text-muted" title={skill.path}>
                                            {skill.path}
                                        </p>
                                    )}
                                </div>
                                <Button
                                    type="button"
                                    variant="outline"
                                    size="sm"
                                    onClick={() => onOpenFolder(skill)}
                                >
                                    <FolderOpen className="h-3.5 w-3.5" />
                                    {t('skillHub.files.openFolder')}
                                </Button>
                            </div>
                        );
                    })}
                </div>
            )}
        </SettingsCard>
    );
}
