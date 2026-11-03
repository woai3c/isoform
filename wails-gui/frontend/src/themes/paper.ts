import { violetTerminalTheme } from './violet';
import type { ThemeDefinition } from './types';

export const paperTheme: ThemeDefinition = {
    id: 'paper',
    label: 'Light',
    isDark: false,
    terminal: violetTerminalTheme('paper'),
};
