import { useCallback, useEffect, useState } from 'react';
import {
    GetSlackStatus,
    GetDiscordStatus,
    GetTelegramStatus,
    GetFeishuStatus,
    GetWeChatStatus,
} from '../../wailsjs/go/main/App';

/**
 * Returns a Set of platform IDs that are currently connected and online,
 * plus a `refresh` function to re-fetch on demand (e.g. when entering edit mode).
 */
export function useConnectedPlatforms(): { connected: Set<string>; refresh: () => void } {
    const [connected, setConnected] = useState<Set<string>>(new Set());

    const refresh = useCallback(() => {
        Promise.allSettled([
            GetSlackStatus().then((s) => ['slack', s.connected && s.daemon_online] as const),
            GetDiscordStatus().then((s) => ['discord', s.bound && s.daemon_online] as const),
            GetTelegramStatus().then((s) => ['telegram', s.connected && s.daemon_online] as const),
            GetFeishuStatus('').then((s) => ['feishu', s.connected && s.daemon_online] as const),
            GetWeChatStatus().then((s) => ['wechat', s.connected && s.online] as const),
        ]).then((results) => {
            const platforms = new Set<string>();
            for (const result of results) {
                if (result.status === 'fulfilled') {
                    const [id, active] = result.value;
                    if (active) platforms.add(id);
                }
            }
            setConnected(platforms);
        });
    }, []);

    useEffect(() => { refresh(); }, [refresh]);

    return { connected, refresh };
}
