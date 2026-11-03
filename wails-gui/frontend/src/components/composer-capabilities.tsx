import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ChatFailureLine } from '@/components/chat-failure-line';
import { OpenPermissionSettings, RequestPermission } from '../../wailsjs/go/main/App';
import type {
    ComposerCapability,
    CapabilityTopic,
} from '../hooks/useComposerCapabilities';

interface ComposerCapabilitiesProps {
    capabilities: ComposerCapability[];
    onConnect: (topic: CapabilityTopic) => void;
}

interface CapabilityChipProps {
    capability: ComposerCapability;
    onConnect: (topic: CapabilityTopic) => void;
    onFailed?: (failed: boolean) => void;
}

/** Only an account sign-in opens Settings; every other chip asks macOS. */
const ACCOUNT_TOPICS: ReadonlySet<CapabilityTopic> = new Set<CapabilityTopic>(['feishu-agent']);
const COMPUTER_USE_PERMISSION = 'accessibility';

function CapabilityChip({ capability, onConnect, onFailed }: CapabilityChipProps) {
    const { t } = useTranslation('app');
    const asksMac = !capability.connected && capability.needsPermission !== false && !ACCOUNT_TOPICS.has(capability.topic);
    const hint = asksMac ? t('composer.capabilities.turnOn', { app: capability.label }) : capability.tooltip ?? capability.label;
    const click = async () => {
        if (!asksMac) {
            onConnect(capability.topic);
            return;
        }
        // Chat K4: "Turn on Google" asks macOS directly, as Today's brief does.
        onFailed?.(false);
        try {
            if (!(await RequestPermission(COMPUTER_USE_PERMISSION))) await OpenPermissionSettings(COMPUTER_USE_PERMISSION);
        } catch {
            onFailed?.(true);
        }
    };
    return (
        <button
            type="button"
            onClick={() => void click()}
            title={hint}
            aria-label={hint}
            data-capability={capability.topic}
            className={`flex h-6 w-6 items-center justify-center rounded-full bg-panel-warm transition hover:opacity-100 dark:bg-panel ${
                capability.connected ? '' : 'opacity-50 grayscale hover:grayscale-0'
            }`}
        >
            <capability.Icon className="h-3.5 w-3.5" />
        </button>
    );
}

/**
 * A single row shown below the welcome-screen composer that surfaces the
 * Computer Use app capabilities: unavailable ones on the left and ready ones
 * on the right. A missing one names what it turns on and asks macOS
 * directly (Chat K4); a ready one or an account sign-in opens Settings.
 */
export function ComposerCapabilities({ capabilities, onConnect }: ComposerCapabilitiesProps) {
    const { t } = useTranslation('app');
    const { t: tChat } = useTranslation('chat');
    const [failed, setFailed] = useState(false);
    const unconnected = capabilities.filter((c) => !c.connected);
    const connected = capabilities.filter((c) => c.connected);

    if (unconnected.length === 0 && connected.length === 0) {
        return null;
    }

    return (
        <>
        <div className="mx-auto mt-2.5 flex w-full items-center justify-between gap-4 px-2 text-xs text-muted">
            {unconnected.length > 0 ? (
                <div className="flex min-w-0 items-center gap-2">
                    <span className="shrink-0">{t('composer.capabilities.canDoMore')}</span>
                    <div className="flex items-center gap-1">
                        {unconnected.map((cap) => (
                            <CapabilityChip key={cap.topic} capability={cap} onConnect={onConnect} onFailed={setFailed} />
                        ))}
                    </div>
                </div>
            ) : (
                <span />
            )}
            {connected.length > 0 && (
                <div className="flex min-w-0 items-center gap-2">
                    <span className="shrink-0">{t('composer.capabilities.connected')}</span>
                    <div className="flex items-center gap-1">
                        {connected.map((cap) => (
                            <CapabilityChip key={cap.topic} capability={cap} onConnect={onConnect} />
                        ))}
                    </div>
                </div>
            )}
        </div>
        {failed && (
            <ChatFailureLine
                testId="system-settings"
                className="mx-auto mt-1.5 w-full px-2 text-xs"
                text={tChat('failure.systemSettings')}
                onRetry={async () => {
                    try { await OpenPermissionSettings(COMPUTER_USE_PERMISSION); setFailed(false); return true; } catch { return false; }
                }}
                after={<span className="block">{tChat('failure.systemSettingsPath', { name: t('composer.capabilities.accessibility') })}</span>}
            />
        )}
        </>
    );
}
