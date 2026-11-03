export type ThemeId = 'paper' | 'onedark';

/** What the user picked: a fixed theme, or 'system' to follow the OS
 *  light / dark setting (resolved to paper or onedark). */
export type ThemePreference = ThemeId | 'system';

export interface TerminalTheme {
    background: string;
    foreground: string;
    cursor: string;
    selectionBackground: string;
    black: string;
    red: string;
    green: string;
    yellow: string;
    blue: string;
    magenta: string;
    cyan: string;
    white: string;
    brightBlack: string;
    brightRed: string;
    brightGreen: string;
    brightYellow: string;
    brightBlue: string;
    brightMagenta: string;
    brightCyan: string;
    brightWhite: string;
}

export interface ThemeDefinition {
    id: ThemeId;
    label: string;
    isDark: boolean;
    terminal: TerminalTheme;
}
