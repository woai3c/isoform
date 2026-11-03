import { useCallback, useEffect, useState } from 'react';
import type { ComponentType } from 'react';
import { useTranslation } from 'react-i18next';
import { GetComputerUseAppAccessSnapshot } from '@/lib/wails-shim-app';
import {
    GoogleIcon,
    SlackIcon,
    NotionIcon,
    GitHubIcon,
    LinearIcon,
    FeishuIcon,
} from '../components/icons/app-icons';

/**
 * The settings topic used by openSettings() to deep-link into a connection.
 * Legacy per-app topics are retained as deep-link aliases. Settings resolves
 * all of them to the single Computer Use app-access page.
 */
export type CapabilityTopic = 'google' | 'slackbot' | 'notion' | 'github' | 'linear' | 'feishu-agent';

export interface ComposerCapability {
    topic: CapabilityTopic;
    label: string;
    /** Hover/aria text. Defaults to `label`; set when the chip needs to convey
     *  more than the bare brand name (e.g. Feishu → "Feishu agent capability"). */
    tooltip?: string;
    Icon: ComponentType<{ className?: string }>;
    connected: boolean;
    /** False when macOS permission is already granted (e.g. the Mac is only locked). */
    needsPermission?: boolean;
}

interface ComputerUseCapabilityDef {
    topic: CapabilityTopic;
    label: string;
    Icon: ComponentType<{ className?: string }>;
}

const COMPUTER_USE_DEFS: ReadonlyArray<ComputerUseCapabilityDef> = [
    { topic: 'google', label: 'Google', Icon: GoogleIcon },
    { topic: 'slackbot', label: 'Slack', Icon: SlackIcon },
    { topic: 'notion', label: 'Notion', Icon: NotionIcon },
    { topic: 'github', label: 'GitHub', Icon: GitHubIcon },
    { topic: 'linear', label: 'Linear', Icon: LinearIcon },
];

/**
 * Returns app capabilities with one shared readiness signal: Computer Use can
 * inspect the existing signed-in session and the Mac is unlocked. There is no
 * provider connection state and no OAuth fallback.
 */
export function useComposerCapabilities(): {
    capabilities: ComposerCapability[];
    refresh: () => void;
} {
    const { t } = useTranslation('app');
    const [capabilities, setCapabilities] = useState<ComposerCapability[]>([]);

    const refresh = useCallback(() => {
        GetComputerUseAppAccessSnapshot()
            .then((snapshot) => {
                const ready =
                    snapshot.available &&
                    snapshot.accessibility &&
                    !snapshot.screenLocked;
                setCapabilities([
                    ...COMPUTER_USE_DEFS.map((def) => ({
                        topic: def.topic,
                        label: def.label,
                        tooltip: `${def.label} via Computer Use`,
                        Icon: def.Icon,
                        connected: ready,
                        needsPermission: !snapshot.accessibility,
                    })),
                    {
                        topic: 'feishu-agent' as const,
                        label: 'Feishu',
                        tooltip: t('composer.capabilities.feishuTooltip'),
                        Icon: FeishuIcon,
                        connected: ready,
                    },
                ]);
            })
            .catch(() => setCapabilities([]));
    }, [t]);

    useEffect(() => {
        refresh();
        // Permission changes happen in System Settings; refresh on focus.
        window.addEventListener('focus', refresh);
        // Also poll so lock/unlock and permission changes converge in place.
        const id = window.setInterval(refresh, 15_000);
        return () => {
            window.removeEventListener('focus', refresh);
            window.clearInterval(id);
        };
    }, [refresh]);

    return { capabilities, refresh };
}
