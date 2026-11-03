const ua = navigator.platform.toUpperCase();

export const isMac = ua.includes('MAC');
export const isWindows = ua.includes('WIN');
export const isLinux = ua.includes('LINUX');

/**
 * User-facing OS name derived from the browser's `navigator.platform`.
 * Falls back to "your system" when the platform is unrecognised.
 */
export function getPlatformDisplayName(): string {
    if (isMac) return 'macOS';
    if (isWindows) return 'Windows';
    if (isLinux) return 'Linux';
    return 'your system';
}

/**
 * Returns the platform-native file manager name for use in UI strings.
 * Pass the current i18n language to get the localised name.
 */
export function getFileManagerName(lang?: string): string {
    const zh = lang?.startsWith('zh');
    if (isMac) return zh ? '访达' : 'Finder';
    if (isWindows) return zh ? '文件资源管理器' : 'File Explorer';
    return zh ? '文件管理器' : 'File Manager';
}

/**
 * Maps a Go `runtime.GOOS`-style platform string (e.g. "darwin", "windows",
 * "linux") to a user-facing display name.
 */
export function platformToDisplayName(platform: string): string {
    switch (platform) {
        case 'darwin':
            return 'macOS';
        case 'windows':
            return 'Windows';
        case 'linux':
            return 'Linux';
        default:
            return 'this platform';
    }
}
