import { useState, useRef, useEffect } from 'react';
import { Plus, Minus, X, Eraser } from 'lucide-react';
import { SearchIcon } from '@/assets/icons/SearchIcon';
import { TerminalInstance } from './terminal-instance';
import type { TerminalInstanceHandle } from './terminal-instance';
import type { TerminalTheme } from '@/themes/types';

export interface TerminalTab {
    id: string;
    wsPort: number;
    label: string;
    envLabel?: string;
}

interface TerminalPanelProps {
    /** Tabs for the active project (shown in the tab bar). */
    tabs: TerminalTab[];
    /** All tabs across all projects (rendered to preserve history). */
    allTabs: TerminalTab[];
    activeTabId: string | null;
    height: number;
    collapsed: boolean;
    onCreateTab: () => void;
    onCloseTab: (id: string) => void;
    onSelectTab: (id: string) => void;
    onToggleCollapse: () => void;
    onRenameTab?: (id: string, label: string) => void;
    onRegisterApi?: (id: string, api: TerminalInstanceHandle | null) => void;
    terminalTheme: TerminalTheme;
}

const TAB_BAR_HEIGHT = 36;

export function TerminalPanel({
    tabs,
    allTabs,
    activeTabId,
    height,
    collapsed,
    onCreateTab,
    onCloseTab,
    onSelectTab,
    onToggleCollapse,
    onRenameTab,
    onRegisterApi,
    terminalTheme,
}: TerminalPanelProps) {
    const [editingTabId, setEditingTabId] = useState<string | null>(null);
    const [editValue, setEditValue] = useState('');
    const editInputRef = useRef<HTMLInputElement>(null);
    const apisRef = useRef(new Map<string, TerminalInstanceHandle>());

    // Focus edit input when entering rename mode
    useEffect(() => {
        if (editingTabId) editInputRef.current?.select();
    }, [editingTabId]);

    const commitRename = (id: string) => {
        const trimmed = editValue.trim();
        if (trimmed && onRenameTab) onRenameTab(id, trimmed);
        setEditingTabId(null);
    };

    const handleClearActive = () => {
        if (activeTabId) apisRef.current.get(activeTabId)?.clear();
    };

    const handleSearchActive = () => {
        if (activeTabId) apisRef.current.get(activeTabId)?.toggleSearch();
    };

    const handleRegister = (id: string, api: TerminalInstanceHandle | null) => {
        if (api) {
            apisRef.current.set(id, api);
        } else {
            apisRef.current.delete(id);
        }
        onRegisterApi?.(id, api);
    };

    // When active project has no tabs, hide the panel but keep instances mounted
    const hasActiveTabs = tabs.length > 0;
    const panelHeight = !hasActiveTabs ? 0 : collapsed ? TAB_BAR_HEIGHT : height;

    return (
        <div
            className="shrink-0 flex flex-col overflow-hidden bg-background border-t border-border"
            style={{ height: panelHeight }}
        >
            {/* Tab bar — only shown when active project has terminals */}
            {hasActiveTabs && (
                <div
                    className="flex items-center h-9 px-1 shrink-0 gap-0.5"
                    style={{ '--wails-draggable': 'no-drag' } as React.CSSProperties}
                >
                    {tabs.map((tab) => (
                        <button
                            key={tab.id}
                            className={`group flex items-center gap-1.5 px-3 h-7 rounded-md text-xs transition-colors ${
                                tab.id === activeTabId
                                    ? 'bg-ink/[0.08] text-ink font-medium'
                                    : 'text-muted hover:text-ink hover:bg-ink/[0.04]'
                            }`}
                            onClick={() => onSelectTab(tab.id)}
                            onDoubleClick={() => {
                                setEditingTabId(tab.id);
                                setEditValue(tab.label);
                            }}
                        >
                            {editingTabId === tab.id ? (
                                <input
                                    ref={editInputRef}
                                    className="w-20 h-5 px-1 text-xs bg-transparent border border-border rounded focus:outline-none focus:border-accent"
                                    value={editValue}
                                    onChange={(e) => setEditValue(e.target.value)}
                                    onBlur={() => commitRename(tab.id)}
                                    onKeyDown={(e) => {
                                        if (e.key === 'Enter') commitRename(tab.id);
                                        if (e.key === 'Escape') setEditingTabId(null);
                                        e.stopPropagation();
                                    }}
                                    onClick={(e) => e.stopPropagation()}
                                />
                            ) : (
                                <span>
                                    {tab.label}
                                    {tab.envLabel && (
                                        <span className="ml-1 text-muted font-normal">({tab.envLabel})</span>
                                    )}
                                </span>
                            )}
                            <span
                                className="opacity-0 group-hover:opacity-100 transition-opacity p-0.5 rounded hover:bg-border"
                                onClick={(e) => {
                                    e.stopPropagation();
                                    onCloseTab(tab.id);
                                }}
                            >
                                <X className="size-3" />
                            </span>
                        </button>
                    ))}

                    <button
                        className="flex items-center justify-center size-7 rounded-md text-muted hover:text-ink hover:bg-border/50 transition-colors"
                        onClick={onCreateTab}
                        title="New terminal"
                    >
                        <Plus className="size-3.5" />
                    </button>

                    <div className="flex-1" />

                    <button
                        className="flex items-center justify-center size-7 rounded-md text-muted hover:text-ink hover:bg-border/50 transition-colors"
                        onClick={handleSearchActive}
                        title="Search in terminal"
                    >
                        <SearchIcon className="size-3.5" />
                    </button>

                    <button
                        className="flex items-center justify-center size-7 rounded-md text-muted hover:text-ink hover:bg-border/50 transition-colors"
                        onClick={handleClearActive}
                        title="Clear terminal"
                    >
                        <Eraser className="size-3.5" />
                    </button>

                    <button
                        className="flex items-center justify-center size-7 rounded-md text-muted hover:text-ink hover:bg-border/50 transition-colors"
                        onClick={onToggleCollapse}
                        title={collapsed ? 'Expand terminal' : 'Collapse terminal'}
                    >
                        <Minus className="size-3.5" />
                    </button>
                </div>
            )}

            {/* Terminal instances — render ALL tabs (across projects) to preserve history */}
            <div className="flex-1 min-h-0 overflow-hidden">
                {allTabs.map((tab) => (
                    <TerminalInstance
                        key={tab.id}
                        terminalId={tab.id}
                        wsPort={tab.wsPort}
                        visible={tab.id === activeTabId && !collapsed}
                        terminalTheme={terminalTheme}
                        onApi={(api) => handleRegister(tab.id, api)}
                    />
                ))}
            </div>
        </div>
    );
}
