import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { QRCodeSVG } from "qrcode.react";
import { activeInterval } from "@/lib/app-activity";
import { RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
    GetWeChatStatus,
    ConnectWeChat,
    DisconnectWeChat,
    StartWeChatLogin,
    PollWeChatLogin,
} from "../../../../../wailsjs/go/main/App";
import { EventsOn } from "../../../../../wailsjs/runtime/runtime";
import { WeChatIcon } from "@/components/icons/app-icons";
import { SettingsFailureLine, useSettingsFailure } from "@/components/settings/settings-failure";
import { SettingsCard } from "@/components/settings/settings-primitives";
import type { ChatAppStatus } from "./chat-app-status";
import { DisconnectButton } from "./disconnect-button";
import { PairingCodeBlock } from "./pairing-code-block";

// ── WeChat settings ──
// QR-code login for the WeChat bot. Rendered as a drill-in on the Chat apps
// settings page. The login QR code is drawn locally (qrcode.react, as on the
// Feishu setup), so the login link is never sent to a third-party image
// service.
export function WeChatSettings({ onStatusChange }: { onStatusChange: (status: ChatAppStatus) => void }) {
    const { t } = useTranslation('settings');
    const { failure: changeFailure, fail: failChange } = useSettingsFailure();
    const [status, setStatus] = useState<{
        connected: boolean;
        bot_id?: string;
        online: boolean;
        paired?: boolean;
        pairing_code?: string;
    }>({ connected: false, online: false });
    const [loading, setLoading] = useState(true);
    const [qrData, setQrData] = useState('');
    const [qrCode, setQrCode] = useState('');
    const [scanning, setScanning] = useState(false);
    const [error, setError] = useState('');
    const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

    const fetchStatus = useCallback(async () => {
        try {
            const s = await GetWeChatStatus();
            setStatus(s);
            return s;
        } catch {
            return null;
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        fetchStatus();
    }, [fetchStatus]);

    // Report only once the first status check is back, so the page never
    // treats the placeholder state as "not set up".
    useEffect(() => {
        if (loading) return;
        onStatusChange({ configured: status.connected, online: status.connected && status.online });
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [loading, status.connected, status.online]);

    // Poll status every 5s while connected and the window is looked at.
    useEffect(() => {
        if (!status.connected) return;
        return activeInterval(() => { fetchStatus(); }, 5000);
    }, [status.connected, fetchStatus]);

    // Auto-start monitor on page load if connected but not online.
    useEffect(() => {
        if (loading || !status.connected || status.online) return;
        ConnectWeChat();
    }, [loading, status.connected, status.online]);

    // Listen for session expired event from backend.
    useEffect(() => {
        const unsub = EventsOn("wechat:session_expired", () => {
            setStatus({ connected: false, online: false });
            setError(t('connectors.wechat.errors.sessionExpired'));
        });
        return () => { unsub(); };
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    // Poll QR code login status.
    useEffect(() => {
        if (!scanning || !qrCode) return;
        pollRef.current = setInterval(async () => {
            try {
                const result = await PollWeChatLogin(qrCode);
                if (result.status === 'confirmed') {
                    setScanning(false);
                    setQrData('');
                    if (pollRef.current) clearInterval(pollRef.current);
                    fetchStatus();
                } else if (result.status === 'expired') {
                    setScanning(false);
                    setQrData('');
                    setError(t('connectors.wechat.errors.qrExpired'));
                    if (pollRef.current) clearInterval(pollRef.current);
                } else if (result.status === 'error') {
                    setScanning(false);
                    setError(result.error || t('connectors.wechat.errors.loginFailed'));
                    if (pollRef.current) clearInterval(pollRef.current);
                }
            } catch {
                // ignore transient errors
            }
        }, 2000);
        return () => {
            if (pollRef.current) clearInterval(pollRef.current);
        };
    }, [scanning, qrCode, fetchStatus]);

    const handleConnect = async () => {
        setError('');
        setScanning(true);
        try {
            const qr = await StartWeChatLogin();
            if (qr.error) {
                setError(qr.error);
                setScanning(false);
                return;
            }
            setQrData(qr.qr_data);
            setQrCode(qr.qr_code);
        } catch (err) {
            setError(err instanceof Error ? err.message : String(err));
            setScanning(false);
        }
    };

    const handleDisconnect = async () => {
        try {
            await DisconnectWeChat();
            setStatus({ connected: false, online: false });
        } catch {
            failChange(t('connectors.common.couldntDisconnect', { app: t('connectors.wechat.title') }), () => void handleDisconnect());
        }
    };

    if (loading) {
        return (
            <SettingsCard title={t('connectors.wechat.title')}>
                <p className="text-sm text-muted">{t('connectors.common.loading')}</p>
            </SettingsCard>
        );
    }

    if (!status.connected) {
        return (
            <SettingsCard title={t('connectors.wechat.title')}>
                <div className="flex flex-col gap-3">
                    <p className="text-sm text-muted">
                        {t('connectors.wechat.description')}
                    </p>
                    {!scanning ? (
                        <Button variant="connect" size="sm" className="w-fit" onClick={handleConnect}>
                            <WeChatIcon className="h-3.5 w-3.5 mr-1.5" />
                            {t('connectors.wechat.connectButton')}
                        </Button>
                    ) : qrData ? (
                        <div className="flex flex-col items-center gap-3 py-2">
                            <div className="rounded-md bg-panel p-3">
                                <QRCodeSVG value={qrData} size={200} title={t('connectors.wechat.qrAlt')} />
                            </div>
                            <p className="text-xs text-muted flex items-center gap-2">
                                <RefreshCw className="h-3.5 w-3.5 animate-spin" />
                                {t('connectors.wechat.scanHint')}
                            </p>
                            <Button variant="ghost" size="sm" onClick={() => { setScanning(false); setQrData(''); }}>
                                {t('connectors.common.cancel')}
                            </Button>
                        </div>
                    ) : (
                        <div className="flex items-center gap-2 py-2">
                            <RefreshCw className="h-4 w-4 animate-spin text-muted" />
                            <span className="text-sm text-muted">{t('connectors.wechat.loadingQr')}</span>
                        </div>
                    )}
                    {error && <p className="text-xs text-copper">{error}</p>}
                </div>
            </SettingsCard>
        );
    }

    return (
        <SettingsCard title={t('connectors.wechat.title')} headerRight={
            <div className="flex items-center gap-1.5">
                <div className={`w-2 h-2 rounded-full ${status.online ? 'bg-success' : 'bg-muted'}`} />
                <span className="text-xs text-muted">{status.online ? t('connectors.common.online') : t('connectors.common.offline')}</span>
            </div>
        }>
            <div className="flex flex-col gap-4">
                <p className="text-sm font-medium text-ink">
                    {status.bot_id ? t('connectors.wechat.botLabel', { botId: status.bot_id }) : t('connectors.wechat.connectedFallback')}
                </p>
                <PairingCodeBlock
                    pairingCode={status.pairing_code}
                    title={t('connectors.common.pairing.title')}
                    hint={t('connectors.wechat.pairHint')}
                    idleNote={status.paired ? t('connectors.common.pairing.paired') : null}
                />
                <div className="flex items-center gap-2">
                    <DisconnectButton appName={t('connectors.wechat.title')} onConfirm={handleDisconnect} />
                </div>
            </div>
            <SettingsFailureLine failure={changeFailure} />
        </SettingsCard>
    );
}
