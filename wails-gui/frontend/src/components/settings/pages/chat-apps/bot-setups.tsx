import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Loader2, EyeOff, Eye } from "lucide-react";
import { activeInterval } from "@/lib/app-activity";
import { Button } from "@/components/ui/button";
import { Avatar } from "@/components/ui/avatar";
import { SettingsFailureLine, useSettingsFailure } from "@/components/settings/settings-failure";
import {
    DisconnectSlack,
    GetSlackStatus,
    GetDiscordStatus,
    UnbindDiscordAccount,
    GetTelegramStatus,
    DisconnectTelegram,
} from "../../../../../wailsjs/go/main/App";
import { BrowserOpenURL } from "../../../../../wailsjs/runtime/runtime";
import {
    TestTelegramConnection,
    SaveTelegramLocalCredentials,
    TestSlackConnection,
    SaveSlackLocalCredentials,
    TestDiscordConnection,
    SaveDiscordLocalCredentials,
    GetDiscordInviteURL,
    GetTelegramArchived,
    ReuseTelegramLocal,
    DiscardTelegramArchived,
    GetSlackArchived,
    ReuseSlackLocal,
    DiscardSlackArchived,
    GetDiscordArchived,
    ReuseDiscordLocal,
    DiscardDiscordArchived,
} from "@/lib/wails-shim-app";
import { ConfirmDialog } from "@/components/confirm-dialog";
import { SettingsCard, SettingsRow } from "@/components/settings/settings-primitives";
import { SettingsAdvanced } from "@/components/settings/settings-advanced";
import type { ChatAppStatus } from "./chat-app-status";
import { DisconnectButton } from "./disconnect-button";
import { PairingCodeBlock } from "./pairing-code-block";

// Setup flows for the chat apps that run on a bot the user creates (Slack,
// Discord, Telegram). Isoform connects to each platform directly from this
// computer; there is no Isoform backend relay. Rendered as drill-ins on the
// Chat apps settings page. Each flow keeps a plain intro, the token fields and
// Test/Save in its card; the step-by-step setup (before it is connected) and
// whether the receiver is online (after) sit under the page's Advanced, as on
// the Feishu setup.

// TokenInput is a password field with a show/hide eye toggle, used by the
// standalone connector wizards so users can verify a pasted bot token/key.
function TokenInput({ value, onChange, placeholder }: { value: string; onChange: (v: string) => void; placeholder?: string }) {
    const { t } = useTranslation('settings');
    const [show, setShow] = useState(false);
    return (
        <div className="relative">
            <input
                type={show ? "text" : "password"}
                value={value}
                onChange={(e) => onChange(e.target.value)}
                placeholder={placeholder}
                className="w-full px-3 py-2 pr-9 rounded-md border border-border bg-transparent text-xs"
            />
            <button
                type="button"
                onClick={() => setShow((s) => !s)}
                aria-label={show ? t('connectors.common.hideToken') : t('connectors.common.showToken')}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-muted hover:text-ink transition-colors"
            >
                {show ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
            </button>
        </div>
    );
}

// StandaloneReuseCard offers reconnecting a previously-disconnected local IM bot
// without re-pasting its token. Shared by the Slack/Discord/Telegram wizards so
// the reuse affordance stays consistent with Feishu/WeChat. "Create new"
// deletes the saved token for good, so it asks first.
function StandaloneReuseCard({
    appName,
    label,
    loading,
    onReuse,
    onCreateNew,
}: {
    appName: string;
    label: string;
    loading: boolean;
    onReuse: () => void;
    onCreateNew: () => void;
}) {
    const { t } = useTranslation('settings');
    const [confirmOpen, setConfirmOpen] = useState(false);
    return (
        <div className="flex flex-col gap-3 rounded-md border border-border bg-background p-4">
            <div className="flex flex-col gap-1">
                <p className="text-sm font-medium text-ink">{t('connectors.common.reuse.title')}</p>
                <p className="text-xs text-muted">{t('connectors.common.reuse.description')}</p>
            </div>
            {label && (
                <div className="flex items-center gap-3 rounded-md border border-border bg-panel/30 px-3 py-2">
                    <Avatar fallback={label} size="sm" />
                    <span className="truncate text-sm font-medium text-ink">{label}</span>
                </div>
            )}
            <div className="flex items-center gap-2">
                <Button variant="connect" size="sm" className="w-fit transition-colors" disabled={loading} onClick={onReuse}>
                    {loading && <Loader2 className="mr-1.5 h-3.5 w-3.5 animate-spin" />}
                    {t('connectors.common.reuse.reuseButton')}
                </Button>
                <Button variant="outline" size="sm" className="w-fit transition-colors" disabled={loading} onClick={() => setConfirmOpen(true)}>
                    {t('connectors.common.reuse.newButton')}
                </Button>
            </div>
            <ConfirmDialog
                open={confirmOpen}
                onOpenChange={setConfirmOpen}
                title={t('chatApps.discardConfirm.title', { app: appName })}
                description={t('chatApps.discardConfirm.description', { app: appName })}
                confirmLabel={t('chatApps.discardConfirm.confirm')}
                variant="danger"
                onConfirm={onCreateNew}
            />
        </div>
    );
}

// BotSetupSteps is the Advanced section of a bot that is not connected yet:
// the step-by-step setup in the platform's own settings.
function BotSetupSteps({ description, steps }: { description?: string; steps: string[] }) {
    const { t } = useTranslation('settings');
    return (
        <SettingsAdvanced pageId="chat-apps" hint={t('chatApps.advanced.setupHint')}>
            <SettingsCard title={t('chatApps.setupSteps.title')} description={description}>
                <ol className="text-xs text-muted leading-relaxed list-decimal pl-4 flex flex-col gap-1">
                    {steps.map((step) => (
                        <li key={step}>{step}</li>
                    ))}
                </ol>
            </SettingsCard>
        </SettingsAdvanced>
    );
}

// BotConnectionDetails is the Advanced section of a connected bot: whether
// Isoform on this computer is receiving its messages right now.
function BotConnectionDetails({ appName, online }: { appName: string; online: boolean }) {
    const { t } = useTranslation('settings');
    return (
        <SettingsAdvanced pageId="chat-apps" hint={t('chatApps.advanced.connectedHint')}>
            <SettingsCard title={t('chatApps.connectionDetails.title')}>
                <SettingsRow
                    title={t('chatApps.connectionDetails.receivingTitle')}
                    description={t('chatApps.connectionDetails.receivingDescription', { app: appName })}
                    control={
                        <span className="flex items-center gap-1.5">
                            <span aria-hidden className={`w-2 h-2 rounded-full ${online ? 'bg-success' : 'bg-muted'}`} />
                            <span className="text-xs text-muted">
                                {online ? t('connectors.common.online') : t('connectors.common.offline')}
                            </span>
                        </span>
                    }
                />
            </SettingsCard>
        </SettingsAdvanced>
    );
}

// SlackStandaloneSetup is the standalone Slack flow: the user creates their own
// Slack app (Socket Mode) and pastes the bot + app tokens. Isoform connects
// directly via Socket Mode — no Isoform backend relay.
export function SlackStandaloneSetup({ onStatusChange }: { onStatusChange: (status: ChatAppStatus) => void }) {
    const { t } = useTranslation('settings');
    const { failure, fail, clear } = useSettingsFailure();
    const [tokenNote, setTokenNote] = useState<string | null>(null);
    const [status, setStatus] = useState<{ connected: boolean; team_name?: string; slack_user_name?: string; daemon_online: boolean; paired?: boolean; pairing_code?: string }>({ connected: false, daemon_online: false });
    const [botToken, setBotToken] = useState("");
    const [appToken, setAppToken] = useState("");
    const [testing, setTesting] = useState(false);
    const [saving, setSaving] = useState(false);
    const [archivedLabel, setArchivedLabel] = useState<string | null>(null);
    const [forceNew, setForceNew] = useState(false);
    const [reuseLoading, setReuseLoading] = useState(false);

    const refresh = useCallback(async () => {
        try {
            const s = await GetSlackStatus();
            setStatus(s);
            onStatusChange({ configured: s.connected, online: s.connected && s.daemon_online });
        } catch { /* ignore */ }
    }, [onStatusChange]);

    useEffect(() => {
        refresh();
        return activeInterval(refresh, 5000);
    }, [refresh]);

    useEffect(() => {
        GetSlackArchived()
            .then((a) => setArchivedLabel(a.has ? a.label : null))
            .catch(() => setArchivedLabel(null));
    }, []);

    const handleReuse = async () => {
        setReuseLoading(true);
        try {
            await ReuseSlackLocal();
            setArchivedLabel(null);
            await refresh();
        } catch (err) {
            fail(t('connectors.common.couldntConnect', { app: t('connectors.slack.title') }), handleReuse);
        } finally {
            setReuseLoading(false);
        }
    };

    const handleCreateNew = async () => {
        try {
            await DiscardSlackArchived();
        } catch {
            /* ignore */
        }
        setArchivedLabel(null);
        setForceNew(true);
    };

    const handleTest = async () => {
        setTesting(true);
        clear();
        setTokenNote(null);
        try {
            const res = await TestSlackConnection(botToken.trim(), appToken.trim());
            if (res.success) setTokenNote(t('connectors.common.tokenWorks', { team: res.team_name ?? "" }));
            else fail(t('connectors.common.tokenDidntWork'), handleTest);
        } catch (err) { fail(t('connectors.common.tokenDidntWork'), handleTest); }
        finally { setTesting(false); }
    };

    const handleSave = async () => {
        setSaving(true);
        clear();
        setTokenNote(null);
        try {
            await SaveSlackLocalCredentials(botToken.trim(), appToken.trim());
            setBotToken(""); setAppToken("");
            await refresh();
        } catch (err) { fail(t('connectors.common.couldntConnect', { app: t('connectors.slack.title') }), handleSave); }
        finally { setSaving(false); }
    };

    const handleDisconnect = async () => {
        try {
            await DisconnectSlack();
            await refresh();
            const a = await GetSlackArchived();
            setArchivedLabel(a.has ? a.label : null);
            setForceNew(false);
        }
        catch (err) { fail(t('connectors.common.couldntDisconnect', { app: t('connectors.slack.title') }), handleDisconnect); }
    };

    if (status.connected) {
        return (
            <>
                <SettingsCard title={t('connectors.slack.title')}>
                    <div className="flex flex-col gap-4">
                        <div className="flex flex-col gap-0.5">
                            {status.slack_user_name && (
                                <p className="text-sm font-medium text-ink">{t('connectors.slack.standalone.connectedAs', { name: status.slack_user_name })}</p>
                            )}
                            <p className={status.slack_user_name ? "text-xs text-muted" : "text-sm font-medium text-ink"}>{t('connectors.slack.standalone.connectedTo', { team: status.team_name ?? "" })}</p>
                        </div>
                        <PairingCodeBlock
                            pairingCode={status.pairing_code}
                            title={t('connectors.common.pairing.title')}
                            hint={t('connectors.slack.standalone.pairHint')}
                            idleNote={status.paired ? t('connectors.common.pairing.paired') : null}
                        />
                        <div className="flex items-center gap-2">
                            <DisconnectButton appName={t('connectors.slack.title')} onConfirm={handleDisconnect} />
                        </div>
                        <SettingsFailureLine failure={failure} />
                    </div>
                </SettingsCard>
                <BotConnectionDetails appName={t('connectors.slack.title')} online={status.daemon_online} />
            </>
        );
    }

    if (archivedLabel && !forceNew) {
        return (
            <SettingsCard title={t('connectors.slack.title')}>
                <StandaloneReuseCard
                    appName={t('connectors.slack.title')}
                    label={archivedLabel}
                    loading={reuseLoading}
                    onReuse={handleReuse}
                    onCreateNew={handleCreateNew}
                />
                <SettingsFailureLine failure={failure} className="mt-2" />
            </SettingsCard>
        );
    }

    return (
        <>
            <SettingsCard
                title={t('connectors.slack.title')}
                description={t('connectors.slack.standalone.intro')}
            >
                <div className="flex flex-col gap-3">
                    <Button variant="connect" size="sm" className="w-fit" onClick={() => BrowserOpenURL("https://api.slack.com/apps")}>
                        {t('connectors.slack.standalone.openSlackApps')}
                    </Button>
                    <label className="text-xs font-medium text-ink">{t('connectors.slack.standalone.botTokenLabel')}</label>
                    <TokenInput value={botToken} onChange={setBotToken} placeholder={t('connectors.slack.standalone.botTokenPlaceholder')} />
                    <label className="text-xs font-medium text-ink">{t('connectors.slack.standalone.appTokenLabel')}</label>
                    <TokenInput value={appToken} onChange={setAppToken} placeholder={t('connectors.slack.standalone.appTokenPlaceholder')} />
                    <div className="flex items-center gap-2">
                        <Button variant="outline" size="sm" className="transition-colors" disabled={!botToken.trim() || !appToken.trim() || testing} onClick={handleTest}>{testing ? t('connectors.slack.standalone.testing') : t('connectors.slack.standalone.test')}</Button>
                        <Button size="sm" className="transition-colors" disabled={!botToken.trim() || !appToken.trim() || saving} onClick={handleSave}>{saving ? t('connectors.slack.standalone.saving') : t('connectors.slack.standalone.save')}</Button>
                    </div>
                    <SettingsFailureLine failure={failure} />
                    {tokenNote && <p className="text-[12px] text-muted">{tokenNote}</p>}
                </div>
            </SettingsCard>
            <BotSetupSteps
                description={t('connectors.slack.standalone.stepsDescription')}
                steps={[
                    t('connectors.slack.standalone.step1'),
                    t('connectors.slack.standalone.step2'),
                    t('connectors.slack.standalone.step3'),
                    t('connectors.slack.standalone.step4'),
                    t('connectors.slack.standalone.step5'),
                    t('connectors.slack.standalone.step6'),
                    t('connectors.slack.standalone.step7'),
                ]}
            />
        </>
    );
}

// DiscordStandaloneSetup is the standalone Discord flow: the user creates their
// own Discord bot, pastes the token, and adds it to a server via the
// auto-generated invite URL. Isoform connects directly to the gateway.
export function DiscordStandaloneSetup({ onStatusChange }: { onStatusChange: (status: ChatAppStatus) => void }) {
    const { t } = useTranslation('settings');
    const { failure, fail, clear } = useSettingsFailure();
    const [tokenNote, setTokenNote] = useState<string | null>(null);
    const [status, setStatus] = useState<{ bound: boolean; discord_user?: string; daemon_online: boolean; paired?: boolean; pairing_code?: string }>({ bound: false, daemon_online: false });
    const [token, setToken] = useState("");
    const [testing, setTesting] = useState(false);
    const [saving, setSaving] = useState(false);
    const [inviteURL, setInviteURL] = useState("");
    const [archivedLabel, setArchivedLabel] = useState<string | null>(null);
    const [forceNew, setForceNew] = useState(false);
    const [reuseLoading, setReuseLoading] = useState(false);

    const refresh = useCallback(async () => {
        try {
            const s = await GetDiscordStatus();
            setStatus(s);
            onStatusChange({ configured: s.bound, online: s.bound && s.daemon_online });
        } catch { /* ignore */ }
    }, [onStatusChange]);

    useEffect(() => {
        refresh();
        return activeInterval(refresh, 5000);
    }, [refresh]);

    useEffect(() => {
        GetDiscordArchived()
            .then((a) => setArchivedLabel(a.has ? a.label : null))
            .catch(() => setArchivedLabel(null));
    }, []);

    const handleReuse = async () => {
        setReuseLoading(true);
        try {
            await ReuseDiscordLocal();
            const url = await GetDiscordInviteURL();
            setInviteURL(url);
            setArchivedLabel(null);
            await refresh();
        } catch (err) {
            fail(t('connectors.common.couldntConnect', { app: t('connectors.discord.title') }), handleReuse);
        } finally {
            setReuseLoading(false);
        }
    };

    const handleCreateNew = async () => {
        try {
            await DiscardDiscordArchived();
        } catch {
            /* ignore */
        }
        setArchivedLabel(null);
        setForceNew(true);
    };

    const handleTest = async () => {
        setTesting(true);
        clear();
        setTokenNote(null);
        try {
            const res = await TestDiscordConnection(token.trim());
            if (res.success) {
                setInviteURL(res.invite_url ?? "");
                setTokenNote(t('connectors.common.tokenWorks', { team: res.bot_username ?? "" }));
            } else fail(t('connectors.common.tokenDidntWork'), handleTest);
        } catch (err) { fail(t('connectors.common.tokenDidntWork'), handleTest); }
        finally { setTesting(false); }
    };

    const handleSave = async () => {
        setSaving(true);
        clear();
        setTokenNote(null);
        try {
            await SaveDiscordLocalCredentials(token.trim());
            const url = await GetDiscordInviteURL();
            setInviteURL(url);
            setToken("");
            await refresh();
        } catch (err) { fail(t('connectors.common.couldntConnect', { app: t('connectors.discord.title') }), handleSave); }
        finally { setSaving(false); }
    };

    const handleDisconnect = async () => {
        try {
            await UnbindDiscordAccount();
            setInviteURL("");
            await refresh();
            const a = await GetDiscordArchived();
            setArchivedLabel(a.has ? a.label : null);
            setForceNew(false);
        }
        catch (err) { fail(t('connectors.common.couldntDisconnect', { app: t('connectors.discord.title') }), handleDisconnect); }
    };

    if (status.bound) {
        return (
            <>
                <SettingsCard title={t('connectors.discord.title')}>
                    <div className="flex flex-col gap-4">
                        <p className="text-sm font-medium text-ink">{t('connectors.discord.standalone.connectedAs', { username: status.discord_user ?? "" })}</p>
                        <PairingCodeBlock
                            pairingCode={status.pairing_code}
                            title={t('connectors.common.pairing.title')}
                            hint={t('connectors.discord.standalone.pairHint')}
                            idleNote={status.paired ? t('connectors.common.pairing.paired') : null}
                        />
                        <div className="flex items-center gap-2">
                            <DisconnectButton appName={t('connectors.discord.title')} onConfirm={handleDisconnect} />
                            {inviteURL && (
                                <Button variant="connect" size="sm" onClick={() => BrowserOpenURL(inviteURL)}>
                                    {t('connectors.discord.standalone.addToServer')}
                                </Button>
                            )}
                        </div>
                        <SettingsFailureLine failure={failure} />
                    </div>
                </SettingsCard>
                <BotConnectionDetails appName={t('connectors.discord.title')} online={status.daemon_online} />
            </>
        );
    }

    if (archivedLabel && !forceNew) {
        return (
            <SettingsCard title={t('connectors.discord.title')}>
                <StandaloneReuseCard
                    appName={t('connectors.discord.title')}
                    label={archivedLabel}
                    loading={reuseLoading}
                    onReuse={handleReuse}
                    onCreateNew={handleCreateNew}
                />
                <SettingsFailureLine failure={failure} className="mt-2" />
            </SettingsCard>
        );
    }

    return (
        <>
            <SettingsCard
                title={t('connectors.discord.title')}
                description={t('connectors.discord.standalone.intro')}
            >
                <div className="flex flex-col gap-3">
                    <Button variant="connect" size="sm" className="w-fit" onClick={() => BrowserOpenURL("https://discord.com/developers/applications")}>
                        {t('connectors.discord.standalone.openPortal')}
                    </Button>
                    <label className="text-xs font-medium text-ink">{t('connectors.discord.standalone.tokenLabel')}</label>
                    <TokenInput value={token} onChange={setToken} placeholder={t('connectors.discord.standalone.tokenPlaceholder')} />
                    {inviteURL && (
                        <Button variant="connect" size="sm" className="w-fit" onClick={() => BrowserOpenURL(inviteURL)}>
                            {t('connectors.discord.standalone.addToServer')}
                        </Button>
                    )}
                    <div className="flex items-center gap-2">
                        <Button variant="outline" size="sm" className="transition-colors" disabled={!token.trim() || testing} onClick={handleTest}>{testing ? t('connectors.discord.standalone.testing') : t('connectors.discord.standalone.test')}</Button>
                        <Button size="sm" className="transition-colors" disabled={!token.trim() || saving} onClick={handleSave}>{saving ? t('connectors.discord.standalone.saving') : t('connectors.discord.standalone.save')}</Button>
                    </div>
                    <SettingsFailureLine failure={failure} />
                    {tokenNote && <p className="text-[12px] text-muted">{tokenNote}</p>}
                </div>
            </SettingsCard>
            <BotSetupSteps
                steps={[
                    t('connectors.discord.standalone.step1'),
                    t('connectors.discord.standalone.step2'),
                    t('connectors.discord.standalone.step3'),
                ]}
            />
        </>
    );
}

// ── Telegram settings ──

// TelegramStandaloneSetup is the standalone (no-backend) Telegram flow: the user
// creates their own bot via @BotFather and pastes the token. Isoform talks to
// api.telegram.org directly. Replaces the backend-relay connect flow.
export function TelegramStandaloneSetup({ onStatusChange }: { onStatusChange: (status: ChatAppStatus) => void }) {
    const { t } = useTranslation('settings');
    const { failure, fail, clear } = useSettingsFailure();
    const [tokenNote, setTokenNote] = useState<string | null>(null);
    const [status, setStatus] = useState<{
        connected: boolean;
        telegram_user_name?: string;
        daemon_online: boolean;
        paired?: boolean;
        pairing_code?: string;
    }>({ connected: false, daemon_online: false });
    const [token, setToken] = useState("");
    const [testing, setTesting] = useState(false);
    const [saving, setSaving] = useState(false);
    const [testedUsername, setTestedUsername] = useState<string | null>(null);
    const [archivedLabel, setArchivedLabel] = useState<string | null>(null);
    const [forceNew, setForceNew] = useState(false);
    const [reuseLoading, setReuseLoading] = useState(false);

    const refresh = useCallback(async () => {
        try {
            const s = await GetTelegramStatus();
            setStatus(s);
            onStatusChange({ configured: s.connected, online: s.connected && s.daemon_online });
        } catch {
            /* ignore */
        }
    }, [onStatusChange]);

    useEffect(() => {
        refresh();
        return activeInterval(refresh, 5000);
    }, [refresh]);

    useEffect(() => {
        GetTelegramArchived()
            .then((a) => setArchivedLabel(a.has ? a.label : null))
            .catch(() => setArchivedLabel(null));
    }, []);

    const handleReuse = async () => {
        setReuseLoading(true);
        try {
            await ReuseTelegramLocal();
            setArchivedLabel(null);
            await refresh();
        } catch (err) {
            fail(t('connectors.common.couldntConnect', { app: t('connectors.telegram.title') }), handleReuse);
        } finally {
            setReuseLoading(false);
        }
    };

    const handleCreateNew = async () => {
        try {
            await DiscardTelegramArchived();
        } catch {
            /* ignore */
        }
        setArchivedLabel(null);
        setForceNew(true);
    };

    const handleTest = async () => {
        setTesting(true);
        clear();
        setTokenNote(null);
        setTestedUsername(null);
        try {
            const res = await TestTelegramConnection(token.trim());
            if (res.success) {
                setTestedUsername(res.bot_username ?? "");
                setTokenNote(t('connectors.common.tokenWorks', { team: res.bot_username ?? "" }));
            } else {
                fail(t('connectors.common.tokenDidntWork'), handleTest);
            }
        } catch (err) {
            fail(t('connectors.common.tokenDidntWork'), handleTest);
        } finally {
            setTesting(false);
        }
    };

    const handleSave = async () => {
        setSaving(true);
        clear();
        setTokenNote(null);
        try {
            await SaveTelegramLocalCredentials(token.trim());
            setToken("");
            setTestedUsername(null);
            await refresh();
        } catch (err) {
            fail(t('connectors.common.couldntConnect', { app: t('connectors.telegram.title') }), handleSave);
        } finally {
            setSaving(false);
        }
    };

    const handleDisconnect = async () => {
        try {
            await DisconnectTelegram();
            await refresh();
            const a = await GetTelegramArchived();
            setArchivedLabel(a.has ? a.label : null);
            setForceNew(false);
        } catch (err) {
            fail(t('connectors.common.couldntDisconnect', { app: t('connectors.telegram.title') }), handleDisconnect);
        }
    };

    if (status.connected) {
        const pairingCode = status.paired ? "" : status.pairing_code ?? "";
        // A t.me link with a start parameter sends "/start <code>" when the
        // user taps Start, which pairs the bot in one step.
        const botURL = pairingCode
            ? `https://t.me/${status.telegram_user_name}?start=${encodeURIComponent(pairingCode)}`
            : `https://t.me/${status.telegram_user_name}`;
        return (
            <>
                <SettingsCard title={t('connectors.telegram.title')}>
                    <div className="flex flex-col gap-4">
                        <p className="text-sm font-medium text-ink">
                            {t('connectors.telegram.standalone.connectedAs', { username: status.telegram_user_name ?? "" })}
                        </p>
                        <PairingCodeBlock
                            pairingCode={pairingCode}
                            title={t('connectors.telegram.standalone.pairTitle')}
                            hint={t('connectors.telegram.standalone.pairHint')}
                            idleNote={status.paired ? t('connectors.telegram.standalone.paired') : t('connectors.telegram.standalone.activateHint')}
                        />
                        <div className="flex items-center gap-2">
                            {status.telegram_user_name && (
                                <Button
                                    variant="connect"
                                    size="sm"
                                    className="transition-colors"
                                    onClick={() => BrowserOpenURL(botURL)}
                                >
                                    {pairingCode ? t('connectors.telegram.standalone.openBotToPair') : t('connectors.telegram.standalone.openBot')}
                                </Button>
                            )}
                            <DisconnectButton appName={t('connectors.telegram.title')} onConfirm={handleDisconnect} />
                        </div>
                        <SettingsFailureLine failure={failure} />
                    </div>
                </SettingsCard>
                <BotConnectionDetails appName={t('connectors.telegram.title')} online={status.daemon_online} />
            </>
        );
    }

    if (archivedLabel && !forceNew) {
        return (
            <SettingsCard title={t('connectors.telegram.title')}>
                <StandaloneReuseCard
                    appName={t('connectors.telegram.title')}
                    label={archivedLabel}
                    loading={reuseLoading}
                    onReuse={handleReuse}
                    onCreateNew={handleCreateNew}
                />
                <SettingsFailureLine failure={failure} className="mt-2" />
            </SettingsCard>
        );
    }

    return (
        <>
            <SettingsCard
                title={t('connectors.telegram.title')}
                description={t('connectors.telegram.standalone.intro')}
            >
                <div className="flex flex-col gap-3">
                    <Button variant="connect" size="sm" className="w-fit" onClick={() => BrowserOpenURL("https://t.me/BotFather")}>
                        {t('connectors.telegram.standalone.openBotFather')}
                    </Button>
                    <label className="text-xs font-medium text-ink">
                        {t('connectors.telegram.standalone.tokenLabel')}
                    </label>
                    <TokenInput
                        value={token}
                        onChange={setToken}
                        placeholder={t('connectors.telegram.standalone.tokenPlaceholder')}
                    />
                    {testedUsername !== null && (
                        <span className="text-xs text-success">
                            {t('connectors.telegram.standalone.testOk', { username: testedUsername })}
                        </span>
                    )}
                    <div className="flex items-center gap-2">
                        <Button variant="outline" size="sm" className="transition-colors" disabled={!token.trim() || testing} onClick={handleTest}>
                            {testing ? t('connectors.telegram.standalone.testing') : t('connectors.telegram.standalone.test')}
                        </Button>
                        <Button size="sm" className="transition-colors" disabled={!token.trim() || saving} onClick={handleSave}>
                            {saving ? t('connectors.telegram.standalone.saving') : t('connectors.telegram.standalone.save')}
                        </Button>
                    </div>
                    <SettingsFailureLine failure={failure} />
                    {tokenNote && <p className="text-[12px] text-muted">{tokenNote}</p>}
                </div>
            </SettingsCard>
            <BotSetupSteps
                steps={[
                    t('connectors.telegram.standalone.step1'),
                    t('connectors.telegram.standalone.step2'),
                    t('connectors.telegram.standalone.step3'),
                ]}
            />
        </>
    );
}
