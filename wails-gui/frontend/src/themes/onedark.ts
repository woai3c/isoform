import { violetTerminalTheme } from './violet';
import type { ThemeDefinition } from './types';

export const onedarkTheme: ThemeDefinition = {
    id: 'onedark',
    label: 'Dark',
    isDark: true,
    terminal: violetTerminalTheme('onedark'),
};
