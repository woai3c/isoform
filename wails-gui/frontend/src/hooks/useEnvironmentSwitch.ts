import { useCallback, useEffect, useRef, useState } from 'react';
import { GetEnvironment, SwitchEnvironment } from '../../wailsjs/go/main/App';
import { isMac } from '@/lib/platform';

const RAPID_CLICK_COUNT = 5;
const RAPID_CLICK_WINDOW_MS = 2000;

export interface EnvironmentState {
    environment: 'production' | 'staging';
    isStaging: boolean;
    showStagingBadge: boolean;
    isDev: boolean;
    loading: boolean;
    showConfirmDialog: boolean;
    error: string | null;
}

export function useEnvironmentSwitch(onEnvironmentChanged?: () => void) {
    const [state, setState] = useState<EnvironmentState>({
        environment: 'production',
        isStaging: false,
        showStagingBadge: false,
        isDev: false,
        loading: false,
        showConfirmDialog: false,
        error: null,
    });

    const clickTimestamps = useRef<number[]>([]);

    useEffect(() => {
        GetEnvironment()
            .then((info) => {
                setState((prev) => ({
                    ...prev,
                    environment: info.is_staging ? 'staging' : 'production',
                    isStaging: info.is_staging,
                    showStagingBadge: info.is_staging && !info.is_release,
                    isDev: info.is_dev,
                }));
            })
            .catch((err) => {
                console.error('[env] failed to load environment state:', err);
            });
    }, []);

    // Keyboard shortcut: Cmd+Shift+Option+E (Mac) / Ctrl+Shift+Alt+E (Windows/Linux)
    // Match on e.code (layout-independent physical key) because macOS WKWebView
    // reports e.key as a dead-key / accented char under Option (Shift+Option+E
    // → '´'), making `e.key === 'e'` fail. Keep e.key as a fallback for layouts
    // where e.code isn't populated.
    useEffect(() => {
        const onKeyDown = (e: KeyboardEvent) => {
            const primaryMod = isMac ? e.metaKey : e.ctrlKey;
            const isEKey = e.code === 'KeyE' || e.key === 'e' || e.key === 'E';
            if (primaryMod && e.shiftKey && e.altKey && isEKey) {
                e.preventDefault();
                setState((prev) => ({ ...prev, showConfirmDialog: true }));
            }
        };
        window.addEventListener('keydown', onKeyDown);
        return () => window.removeEventListener('keydown', onKeyDown);
    }, []);

    // Logo rapid-click handler: call this from the logo's onClick
    const handleLogoClick = useCallback(() => {
        const now = Date.now();
        const timestamps = clickTimestamps.current;
        timestamps.push(now);

        // Keep only clicks within the window
        const cutoff = now - RAPID_CLICK_WINDOW_MS;
        while (timestamps.length > 0 && timestamps[0] < cutoff) {
            timestamps.shift();
        }

        if (timestamps.length >= RAPID_CLICK_COUNT) {
            timestamps.length = 0;
            setState((prev) => ({ ...prev, showConfirmDialog: true }));
        }
    }, []);

    const confirmSwitch = useCallback(async () => {
        setState((prev) => ({ ...prev, loading: true, error: null }));
        try {
            const info = await SwitchEnvironment();
            setState({
                environment: info.is_staging ? 'staging' : 'production',
                isStaging: info.is_staging,
                showStagingBadge: info.is_staging && !info.is_release,
                isDev: info.is_dev,
                loading: false,
                showConfirmDialog: false,
                error: null,
            });
            onEnvironmentChanged?.();
        } catch (err) {
            const message = err instanceof Error ? err.message : String(err);
            console.error('[env] failed to switch environment:', message);
            setState((prev) => ({
                ...prev,
                loading: false,
                error: message,
            }));
        }
    }, [onEnvironmentChanged]);

    const cancelSwitch = useCallback(() => {
        setState((prev) => ({ ...prev, showConfirmDialog: false }));
    }, []);

    return {
        ...state,
        handleLogoClick,
        confirmSwitch,
        cancelSwitch,
    };
}
