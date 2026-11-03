import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { isMac } from '@/lib/platform';

const STORAGE_KEY = 'yansu.devtools-unlocked';

// '1' and '0' are explicit choices made with the shortcut. Nothing stored
// means the build default applies.
function readStored(): boolean | null {
    try {
        const raw = localStorage.getItem(STORAGE_KEY);
        if (raw === '1') return true;
        if (raw === '0') return false;
    } catch {
        // Storage unavailable: fall back to the build default.
    }
    return null;
}

function writeStored(enabled: boolean): void {
    try {
        localStorage.setItem(STORAGE_KEY, enabled ? '1' : '0');
    } catch {
        // ignore quota / disabled storage; the in-memory switch still applies
    }
}

// useDevToolsUnlock is the on/off switch for the Developer tools group in
// Settings. There is no visible switch for release users: Cmd+Shift+Option+D
// (Ctrl+Shift+Alt+D on Windows/Linux) flips it, and pressing it again turns
// the tools off. `defaultEnabled` is the state before the shortcut is ever
// used (on in staging and dev builds). Parallels useEnvironmentSwitch's
// Cmd+Shift+Option+E pattern but is UI-only: no backend side effects, so the
// current env/session/agent state stays intact while inspecting diagnostic
// panels.
export function useDevToolsUnlock(defaultEnabled = false): boolean {
    const { t } = useTranslation('settings');
    const [stored, setStored] = useState<boolean | null>(readStored);
    const enabled = stored ?? defaultEnabled;

    const toggle = useCallback(() => {
        const next = !enabled;
        setStored(next);
        writeStored(next);
        if (next) {
            toast.success(t('devTools.toggle.onTitle'), {
                description: t('devTools.toggle.onDescription'),
            });
        } else {
            toast.success(t('devTools.toggle.offTitle'), {
                description: t('devTools.toggle.offDescription'),
            });
        }
    }, [enabled, t]);

    useEffect(() => {
        const onKeyDown = (e: KeyboardEvent) => {
            const primaryMod = isMac ? e.metaKey : e.ctrlKey;
            // On macOS, Option + letter produces a dead-key / accented char
            // (e.g. Shift+Option+D yields 'Î'), so `e.key === 'd'` fails under
            // WKWebView. `e.code` is the layout-independent physical key name
            // and matches regardless of modifiers — preferred for Option-based
            // shortcuts.
            const isDKey = e.code === 'KeyD' || e.key === 'd' || e.key === 'D';
            if (primaryMod && e.shiftKey && e.altKey && isDKey) {
                e.preventDefault();
                // Holding the keys auto-repeats; one press is one flip.
                if (e.repeat) return;
                toggle();
            }
        };
        window.addEventListener('keydown', onKeyDown);
        return () => window.removeEventListener('keydown', onKeyDown);
    }, [toggle]);

    return enabled;
}
