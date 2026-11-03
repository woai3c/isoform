import { paperTheme } from './paper';
import { onedarkTheme } from './onedark';
import type { ThemeId, ThemeDefinition } from './types';

export const themes: Record<ThemeId, ThemeDefinition> = {
    paper: paperTheme,
    onedark: onedarkTheme,
};

export const themeList: ThemeDefinition[] = Object.values(themes);

export const DEFAULT_LIGHT_THEME: ThemeId = 'paper';
export const DEFAULT_DARK_THEME: ThemeId = 'onedark';

export function getTheme(id: ThemeId): ThemeDefinition {
    return themes[id] ?? themes.paper;
}
