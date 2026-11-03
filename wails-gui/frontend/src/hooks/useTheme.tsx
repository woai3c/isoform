import {
    createContext,
    useContext,
    useEffect,
    useState,
    useCallback,
    useMemo,
    type ReactNode,
} from 'react';
import type {
    ThemeId,
    ThemePreference,
    ThemeDefinition,
    TerminalTheme,
} from '@/themes/types';
import { getTheme, DEFAULT_LIGHT_THEME, DEFAULT_DARK_THEME } from '@/themes/registry';
import {
    GetThemePreference,
    SetThemePreference as GoSetThemePreference,
} from '../../wailsjs/go/main/App';

const THEME_STORAGE_KEY = 'yansu.theme';
const DARK_SCHEME_QUERY = '(prefers-color-scheme: dark)';

// ── Context ──

interface ThemeContextValue {
    preference: ThemePreference;
    themeId: ThemeId;
    theme: ThemeDefinition;
    terminalTheme: TerminalTheme;
    setPreference: (pref: ThemePreference) => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

// ── Helpers ──

function isThemeId(value: unknown): value is ThemeId {
    return value === 'paper' || value === 'onedark';
}

function readPreference(): ThemePreference {
    try {
        const stored = localStorage.getItem(THEME_STORAGE_KEY);
        if (isThemeId(stored) || stored === 'system') {
            return stored;
        }
    } catch {
        // ignore
    }
    return DEFAULT_LIGHT_THEME;
}

// matchMedia is missing in some test environments; treat that as light.
function darkSchemeQuery(): MediaQueryList | null {
    if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') {
        return null;
    }
    return window.matchMedia(DARK_SCHEME_QUERY);
}

function readSystemPrefersDark(): boolean {
    return darkSchemeQuery()?.matches ?? false;
}

function applyThemeAttribute(themeId: ThemeId): void {
    document.documentElement.setAttribute('data-theme', themeId);
}

// ── Provider ──

export function ThemeProvider({ children }: { children: ReactNode }) {
    const [preference, setPreferenceState] =
        useState<ThemePreference>(readPreference);
    const [systemPrefersDark, setSystemPrefersDark] = useState<boolean>(
        readSystemPrefersDark,
    );

    const themeId: ThemeId =
        preference === 'system'
            ? systemPrefersDark
                ? DEFAULT_DARK_THEME
                : DEFAULT_LIGHT_THEME
            : preference;

    const theme = useMemo(() => getTheme(themeId), [themeId]);

    // Apply data-theme attribute to <html>
    useEffect(() => {
        applyThemeAttribute(themeId);
    }, [themeId]);

    // Follow the OS light / dark setting while the preference is 'system'.
    useEffect(() => {
        if (preference !== 'system') return;
        const query = darkSchemeQuery();
        if (!query) return;
        const onChange = (e: MediaQueryListEvent) => setSystemPrefersDark(e.matches);
        setSystemPrefersDark(query.matches);
        query.addEventListener('change', onChange);
        return () => query.removeEventListener('change', onChange);
    }, [preference]);

    // The Go side only knows concrete themes (it serves them to the
    // spotlight helper and the toast windows), so while following the OS
    // we mirror the resolved theme each time it changes.
    useEffect(() => {
        if (preference !== 'system') return;
        void GoSetThemePreference(themeId).catch(() => {
            /* Wails binding not available (browser-mode dev). */
        });
    }, [preference, themeId]);

    // On mount, ask the Go side for the persisted theme. Go is the
    // authoritative source for cross-process consumers (the spotlight
    // helper queries it via the host bridge), so if disk has a value
    // and localStorage diverges (e.g. a previous session crashed
    // before localStorage flushed), trust the disk value. Falls
    // through to the localStorage / default value when Go has nothing
    // recorded yet (first run). Go stores only the resolved theme, so a
    // 'system' preference is kept as is.
    useEffect(() => {
        let cancelled = false;
        void GetThemePreference()
            .then((stored) => {
                if (cancelled) return;
                if (isThemeId(stored)) {
                    setPreferenceState((prev) => (prev === 'system' ? prev : stored));
                }
            })
            .catch(() => {
                /* Wails binding not available (browser-mode dev) — keep
                   the localStorage / default fallback. */
            });
        return () => {
            cancelled = true;
        };
    }, []);

    // Persist preference. Writes to BOTH localStorage (fast, synchronous,
    // survives the next React mount; index.html reads it before first
    // paint) AND the Go side (file-backed, exposed to the spotlight helper
    // via /api/spotlight/theme). 'system' reaches Go through the mirror
    // effect above as the resolved theme.
    const setPreference = useCallback((pref: ThemePreference) => {
        setPreferenceState(pref);
        try {
            localStorage.setItem(THEME_STORAGE_KEY, pref);
        } catch {
            // ignore
        }
        if (pref === 'system') return;
        void GoSetThemePreference(pref).catch(() => {
            /* Wails binding not available (browser-mode dev) — the
               localStorage write above is enough for the main UI; only
               the spotlight loses sync, which is acceptable in dev. */
        });
    }, []);

    const value = useMemo<ThemeContextValue>(
        () => ({
            preference,
            themeId,
            theme,
            terminalTheme: theme.terminal,
            setPreference,
        }),
        [preference, themeId, theme, setPreference]
    );

    return (
        <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
    );
}

// ── Hook ──

export function useTheme(): ThemeContextValue {
    const ctx = useContext(ThemeContext);
    if (!ctx) {
        throw new Error('useTheme must be used within a ThemeProvider');
    }
    return ctx;
}
