import { useEffect, useState } from 'react';

const VIM_MODE_STORAGE_KEY = 'yansu.vim-mode';
const GROUP_DISPLAY_MODE_KEY = 'yansu.group-display-mode';

export type GroupDisplayMode = 'flat' | 'nested';

export function useSettings() {
    const [settingsOpen, setSettingsOpen] = useState(false);
    const [vimMode, setVimMode] = useState(() => {
        try {
            return localStorage.getItem(VIM_MODE_STORAGE_KEY) === 'true';
        } catch {
            return false;
        }
    });
    const [groupDisplayMode, setGroupDisplayMode] = useState<GroupDisplayMode>(() => {
        try {
            const stored = localStorage.getItem(GROUP_DISPLAY_MODE_KEY);
            return stored === 'flat' ? 'flat' : 'nested';
        } catch {
            return 'nested';
        }
    });

    useEffect(() => {
        try {
            localStorage.setItem(VIM_MODE_STORAGE_KEY, String(vimMode));
        } catch {
            // ignore
        }
    }, [vimMode]);

    useEffect(() => {
        try {
            localStorage.setItem(GROUP_DISPLAY_MODE_KEY, groupDisplayMode);
        } catch {
            // ignore
        }
    }, [groupDisplayMode]);

    return {
        settingsOpen,
        setSettingsOpen,
        vimMode,
        setVimMode,
        groupDisplayMode,
        setGroupDisplayMode,
    };
}
