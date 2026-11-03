import tokens from '../../../../docs/design-system/tokens.json';
import type { TerminalTheme, ThemeId } from './types';

// xterm paints a canvas and needs resolved colors, not CSS var() references.
// Read the same source as the CSS palette so terminal chrome follows brand violet.
export function violetTerminalTheme(theme: ThemeId): TerminalTheme {
    const color = (name: string): string => {
        const token = tokens.color.tokens.find((entry) => entry.name === `color-${name}`);
        if (!token) throw new Error(`Missing brand violet token: ${name}`);
        return token.value[theme];
    };
    return {
        background: color('background'),
        foreground: color('ink'),
        cursor: color('ink'),
        selectionBackground: color('selection-bg'),
        black: color('ink'),
        red: color('error'),
        green: color('success'),
        yellow: color('warning'),
        blue: color('accent'),
        magenta: color('muted'),
        cyan: color('accent'),
        white: color('ink'),
        brightBlack: color('muted'),
        brightRed: color('error'),
        brightGreen: color('success'),
        brightYellow: color('warning'),
        brightBlue: color('accent'),
        brightMagenta: color('muted'),
        brightCyan: color('accent'),
        brightWhite: color('ink'),
    };
}
