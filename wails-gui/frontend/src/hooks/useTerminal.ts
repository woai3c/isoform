import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { CreateTerminal, CloseTerminal } from '../../wailsjs/go/main/App';
import { useResizable } from './useResizable';
import type { TerminalTab } from '@/components/terminal-panel';
import type { TerminalInstanceHandle } from '@/components/terminal-instance';
import type { RemoteEnv } from '@/types';
import { terminal } from '../../wailsjs/go/models';
import { toast } from 'sonner';

/** Per-workspace terminal state. */
interface WorkspaceTerminalState {
    tabs: TerminalTab[];
    activeId: string | null;
    count: number;
}

/** Build a composite key for per-workspace terminal isolation. */
function wsKey(projectId: string, workspaceId: string): string {
    return `${projectId}:${workspaceId}`;
}

interface UseTerminalDeps {
    activeProjectId: string | null;
    activeWorkspaceId: string | null;
    /** Effective working directory — worktree path for non-default workspaces, project path otherwise. */
    activeWorkDir: string | undefined;
    layoutRef: React.RefObject<HTMLDivElement | null>;
    /** Terminal theme colors for PTY query responses (OSC 10/11). */
    terminalFg?: string;
    terminalBg?: string;
    /** Remote execution environment (WSL or SSH) for the active workspace. */
    remoteEnv?: RemoteEnv | null;
}

/** Convert frontend RemoteEnv to the Wails model format, or null for local. */
function toTerminalRemoteEnv(env?: RemoteEnv | null): terminal.RemoteEnv | null {
    if (!env || env.type === 'local') return null;
    const model = new terminal.RemoteEnv();
    model.type = env.type;
    model.distro = env.distro;
    model.sshConfigId = env.sshConfigId;
    return model;
}

export function useTerminal({ activeProjectId, activeWorkspaceId, activeWorkDir, layoutRef, terminalFg, terminalBg, remoteEnv }: UseTerminalDeps) {
    // Per-workspace terminal state keyed by "projectId:workspaceId"
    const [tabsByWorkspace, setTabsByWorkspace] = useState<Record<string, WorkspaceTerminalState>>({});
    const [terminalCollapsed, setTerminalCollapsed] = useState(true);

    const {
        size: terminalHeight,
        isResizing: isResizingTerminal,
        startResize: startTerminalResize,
    } = useResizable({
        anchor: 'bottom',
        defaultSize: 280,
        minSize: 120,
        maxSize: 600,
        storageKey: 'yansu.terminal-height',
        layoutRef,
    });

    const terminalApisRef = useRef(new Map<string, TerminalInstanceHandle>());

    // Reverse lookup: terminalId → composite key (for terminal:exit routing)
    const terminalToKeyRef = useRef(new Map<string, string>());

    // ── Derived state for active workspace ──
    const currentKey = activeProjectId && activeWorkspaceId ? wsKey(activeProjectId, activeWorkspaceId) : null;
    const workspaceState = currentKey ? tabsByWorkspace[currentKey] : undefined;
    const terminalTabs = workspaceState?.tabs ?? [];
    const activeTerminalId = workspaceState?.activeId ?? null;

    // All tabs across all workspaces (for keeping TerminalInstance mounted)
    const allTerminalTabs = useMemo(() => {
        const all: TerminalTab[] = [];
        for (const state of Object.values(tabsByWorkspace)) {
            all.push(...state.tabs);
        }
        return all;
    }, [tabsByWorkspace]);

    // Helper to update a specific workspace's terminal state
    const updateWorkspaceState = useCallback(
        (key: string, updater: (prev: WorkspaceTerminalState) => WorkspaceTerminalState) => {
            setTabsByWorkspace((prev) => {
                const current = prev[key] ?? { tabs: [], activeId: null, count: 0 };
                const next = updater(current);
                // Clean up empty entries
                if (next.tabs.length === 0) {
                    const { [key]: _, ...rest } = prev;
                    return rest;
                }
                return { ...prev, [key]: next };
            });
        },
        []
    );

    // Terminal exit event — route to the correct workspace
    useEffect(() => {
        const unsub = EventsOn('terminal:exit', (id: string) => {
            terminalApisRef.current.delete(id);
            const key = terminalToKeyRef.current.get(id);
            terminalToKeyRef.current.delete(id);

            if (!key) return;

            setTabsByWorkspace((prev) => {
                const state = prev[key];
                if (!state) return prev;

                const remaining = state.tabs.filter((t) => t.id !== id);
                if (remaining.length === 0) {
                    const { [key]: _, ...rest } = prev;
                    return rest;
                }

                return {
                    ...prev,
                    [key]: {
                        ...state,
                        tabs: remaining,
                        activeId: state.activeId === id ? remaining[0].id : state.activeId,
                    },
                };
            });
        });
        return () => unsub();
    }, []);

    // Auto-collapse when active project has no terminals
    useEffect(() => {
        if (terminalTabs.length === 0) {
            setTerminalCollapsed(true);
        }
    }, [terminalTabs.length]);

    // ── API registration (called by TerminalPanel → TerminalInstance) ──
    const registerTerminalApi = useCallback(
        (id: string, api: TerminalInstanceHandle | null) => {
            if (api) {
                terminalApisRef.current.set(id, api);
            } else {
                terminalApisRef.current.delete(id);
            }
        },
        []
    );

    // ── Handlers ──

    const handleCreateTerminal = useCallback(async () => {
        if (!currentKey) return;
        const workDir = activeWorkDir || '';
        try {
            const info = await CreateTerminal(workDir, terminalFg || '', terminalBg || '', toTerminalRemoteEnv(remoteEnv) as any);
            terminalToKeyRef.current.set(info.id, currentKey);

            setTabsByWorkspace((prev) => {
                const state = prev[currentKey] ?? { tabs: [], activeId: null, count: 0 };
                const nextCount = state.count + 1;
                const envLabel = remoteEnv?.type === 'wsl'
                    ? `WSL${remoteEnv.distro ? ': ' + remoteEnv.distro : ''}`
                    : remoteEnv?.type === 'ssh'
                        ? `SSH${remoteEnv.sshConfigId ? ': ' + remoteEnv.sshConfigId : ''}`
                        : undefined;
                const tab: TerminalTab = {
                    id: info.id,
                    wsPort: info.wsPort,
                    label: `Terminal ${nextCount}`,
                    envLabel,
                };
                return {
                    ...prev,
                    [currentKey]: {
                        tabs: [...state.tabs, tab],
                        activeId: info.id,
                        count: nextCount,
                    },
                };
            });
            setTerminalCollapsed(false);
        } catch (err) {
            const message = err instanceof Error ? err.message : String(err);
            toast.error(`Failed to create terminal: ${message}`);
        }
    }, [currentKey, activeWorkDir, terminalFg, terminalBg, remoteEnv]);

    const handleCloseTerminal = useCallback(async (id: string) => {
        try {
            await CloseTerminal(id);
        } catch {
            // ignore — terminal may already be gone
        }
        terminalApisRef.current.delete(id);
        const key = terminalToKeyRef.current.get(id);
        terminalToKeyRef.current.delete(id);

        if (!key) return;

        setTabsByWorkspace((prev) => {
            const state = prev[key];
            if (!state) return prev;

            const remaining = state.tabs.filter((t) => t.id !== id);
            if (remaining.length === 0) {
                const { [key]: _, ...rest } = prev;
                return rest;
            }

            return {
                ...prev,
                [key]: {
                    ...state,
                    tabs: remaining,
                    activeId: state.activeId === id ? remaining[0].id : state.activeId,
                },
            };
        });
    }, []);

    const handleToggleTerminal = useCallback(() => {
        if (terminalTabs.length === 0) {
            void handleCreateTerminal();
        } else {
            setTerminalCollapsed((v) => !v);
        }
    }, [terminalTabs.length, handleCreateTerminal]);

    const handleRenameTerminal = useCallback((id: string, label: string) => {
        const key = terminalToKeyRef.current.get(id);
        if (!key) return;
        updateWorkspaceState(key, (state) => ({
            ...state,
            tabs: state.tabs.map((t) => (t.id === id ? { ...t, label } : t)),
        }));
    }, [updateWorkspaceState]);

    /** Close the currently active terminal tab (⌘W). */
    const handleCloseActiveTerminal = useCallback(() => {
        if (activeTerminalId) {
            void handleCloseTerminal(activeTerminalId);
        }
    }, [activeTerminalId, handleCloseTerminal]);

    /** Switch to terminal tab by 0-based index (⌘1-9). */
    const handleSwitchTerminalByIndex = useCallback(
        (index: number) => {
            if (!currentKey || index >= terminalTabs.length) return;
            updateWorkspaceState(currentKey, (state) => ({
                ...state,
                activeId: state.tabs[index].id,
            }));
            setTerminalCollapsed(false);
        },
        [currentKey, terminalTabs, updateWorkspaceState]
    );

    const setActiveTerminalId = useCallback(
        (id: string) => {
            if (!currentKey) return;
            updateWorkspaceState(currentKey, (state) => ({
                ...state,
                activeId: id,
            }));
        },
        [currentKey, updateWorkspaceState]
    );

    /** Close all terminals belonging to a project (all workspaces). */
    const handleCloseProjectTerminals = useCallback(
        (projectId: string) => {
            setTabsByWorkspace((prev) => {
                const prefix = `${projectId}:`;
                const next = { ...prev };
                let changed = false;
                for (const [key, state] of Object.entries(prev)) {
                    if (key.startsWith(prefix)) {
                        for (const tab of state.tabs) {
                            terminalApisRef.current.delete(tab.id);
                            terminalToKeyRef.current.delete(tab.id);
                            CloseTerminal(tab.id).catch(() => {});
                        }
                        delete next[key];
                        changed = true;
                    }
                }
                return changed ? next : prev;
            });
        },
        []
    );

    /** Close all terminals belonging to a specific workspace. */
    const handleCloseWorkspaceTerminals = useCallback(
        (projectId: string, workspaceId: string) => {
            const key = wsKey(projectId, workspaceId);
            setTabsByWorkspace((prev) => {
                const state = prev[key];
                if (!state) return prev;

                for (const tab of state.tabs) {
                    terminalApisRef.current.delete(tab.id);
                    terminalToKeyRef.current.delete(tab.id);
                    CloseTerminal(tab.id).catch(() => {});
                }

                const { [key]: _, ...rest } = prev;
                return rest;
            });
        },
        []
    );

    return {
        terminalTabs,
        allTerminalTabs,
        activeTerminalId,
        setActiveTerminalId,
        terminalCollapsed,
        setTerminalCollapsed,
        terminalHeight,
        isResizingTerminal,
        startTerminalResize,
        handleCreateTerminal,
        handleCloseTerminal,
        handleToggleTerminal,
        handleRenameTerminal,
        handleCloseActiveTerminal,
        handleSwitchTerminalByIndex,
        handleCloseProjectTerminals,
        handleCloseWorkspaceTerminals,
        registerTerminalApi,
    };
}
