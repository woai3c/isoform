import { useEffect, useRef, useState, useCallback } from 'react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { AttachAddon } from '@xterm/addon-attach';
import { WebglAddon } from '@xterm/addon-webgl';
import { Unicode11Addon } from '@xterm/addon-unicode11';
import { SearchAddon } from '@xterm/addon-search';
import { ChevronUp, ChevronDown, X } from 'lucide-react';
import '@xterm/xterm/css/xterm.css';
import { ResizeTerminal } from '../../wailsjs/go/main/App';
import type { TerminalTheme } from '@/themes/types';

/** Imperative API exposed via the onApi callback. */
export interface TerminalInstanceHandle {
    clear: () => void;
    toggleSearch: () => void;
    focus: () => void;
}

interface TerminalInstanceProps {
    terminalId: string;
    wsPort: number;
    visible: boolean;
    terminalTheme: TerminalTheme;
    /** Called when the xterm instance is ready (or null on unmount). */
    onApi?: (api: TerminalInstanceHandle | null) => void;
}

export function TerminalInstance({ terminalId, wsPort, visible, terminalTheme, onApi }: TerminalInstanceProps) {
    const containerRef = useRef<HTMLDivElement>(null);
    const termRef = useRef<Terminal | null>(null);
    const fitRef = useRef<FitAddon | null>(null);
    const wsRef = useRef<WebSocket | null>(null);
    const searchRef = useRef<SearchAddon | null>(null);
    const searchInputRef = useRef<HTMLInputElement>(null);

    const [showSearch, setShowSearch] = useState(false);
    const [searchQuery, setSearchQuery] = useState('');

    // ── Search helpers ──
    const findNext = useCallback(() => {
        if (searchRef.current && searchQuery) searchRef.current.findNext(searchQuery);
    }, [searchQuery]);

    const findPrevious = useCallback(() => {
        if (searchRef.current && searchQuery) searchRef.current.findPrevious(searchQuery);
    }, [searchQuery]);

    const closeSearch = useCallback(() => {
        searchRef.current?.clearDecorations();
        setShowSearch(false);
        setSearchQuery('');
        termRef.current?.focus();
    }, []);

    // ── xterm lifecycle ──
    useEffect(() => {
        if (!containerRef.current) return;

        const term = new Terminal({
            cursorBlink: true,
            fontSize: 13,
            fontFamily: "Menlo, Monaco, 'Courier New', monospace",
            theme: terminalTheme,
            allowProposedApi: true,
        });

        const fitAddon = new FitAddon();
        term.loadAddon(fitAddon);
        term.open(containerRef.current);

        // Unicode11 addon for correct wide character (emoji, CJK) width calculation
        const unicode11 = new Unicode11Addon();
        term.loadAddon(unicode11);
        term.unicode.activeVersion = '11';

        // Search addon
        const searchAddon = new SearchAddon();
        term.loadAddon(searchAddon);
        searchRef.current = searchAddon;

        // WebGL renderer for better Nerd Font glyph rendering
        try {
            const webgl = new WebglAddon();
            webgl.onContextLoss(() => {
                webgl.dispose();
            });
            term.loadAddon(webgl);
        } catch (e) {
            console.warn('WebGL renderer not available, using canvas:', e);
        }

        termRef.current = term;
        fitRef.current = fitAddon;

        // Initial fit
        fitAddon.fit();

        // Connect WebSocket
        const ws = new WebSocket(`ws://127.0.0.1:${wsPort}/terminal/${terminalId}`);
        ws.binaryType = 'arraybuffer';
        wsRef.current = ws;

        ws.onopen = () => {
            const attachAddon = new AttachAddon(ws);
            term.loadAddon(attachAddon);
            ResizeTerminal(terminalId, term.rows, term.cols).catch(() => {});
        };

        ws.onclose = () => {
            term.write('\r\n\x1b[90m[Terminal disconnected]\x1b[0m\r\n');
        };

        // Observe container resizes (debounced)
        let resizeTimer: ReturnType<typeof setTimeout>;
        const resizeObserver = new ResizeObserver(() => {
            clearTimeout(resizeTimer);
            resizeTimer = setTimeout(() => {
                fitAddon.fit();
                ResizeTerminal(terminalId, term.rows, term.cols).catch(() => {});
            }, 100);
        });
        resizeObserver.observe(containerRef.current);

        // Register API handle
        onApi?.({
            clear: () => term.clear(),
            toggleSearch: () => {
                setShowSearch((v) => {
                    if (!v) {
                        // Will focus the input after render via the effect below
                        return true;
                    }
                    searchAddon.clearDecorations();
                    setSearchQuery('');
                    term.focus();
                    return false;
                });
            },
            focus: () => term.focus(),
        });

        return () => {
            onApi?.(null);
            clearTimeout(resizeTimer);
            resizeObserver.disconnect();
            ws.close();
            term.dispose();
            termRef.current = null;
            fitRef.current = null;
            wsRef.current = null;
            searchRef.current = null;
        };
    }, [terminalId, wsPort]); // eslint-disable-line react-hooks/exhaustive-deps

    // Update terminal theme when it changes (hot-reload)
    useEffect(() => {
        if (termRef.current) {
            termRef.current.options.theme = terminalTheme;
        }
    }, [terminalTheme]);

    // Re-fit when visibility changes
    useEffect(() => {
        if (visible && fitRef.current) {
            const timer = setTimeout(() => {
                fitRef.current?.fit();
                if (termRef.current) {
                    ResizeTerminal(terminalId, termRef.current.rows, termRef.current.cols).catch(() => {});
                }
            }, 50);
            return () => clearTimeout(timer);
        }
    }, [visible, terminalId]);

    // Auto-focus search input when search bar appears
    useEffect(() => {
        if (showSearch) {
            searchInputRef.current?.focus();
        }
    }, [showSearch]);

    // Live search as user types
    useEffect(() => {
        if (showSearch && searchQuery && searchRef.current) {
            searchRef.current.findNext(searchQuery);
        }
    }, [searchQuery, showSearch]);

    return (
        <div
            className="relative h-full w-full"
            style={{ display: visible ? 'block' : 'none' }}
        >
            {/* Search overlay */}
            {showSearch && (
                <div className="absolute top-1 right-3 z-10 flex items-center gap-0.5 px-2 py-1 bg-panel border border-border rounded-lg shadow-sm">
                    <input
                        ref={searchInputRef}
                        className="w-40 h-5 px-1.5 text-xs bg-transparent border border-border rounded focus:outline-none focus:border-accent"
                        placeholder="Search..."
                        value={searchQuery}
                        onChange={(e) => setSearchQuery(e.target.value)}
                        onKeyDown={(e) => {
                            if (e.key === 'Enter' && e.shiftKey) {
                                e.preventDefault();
                                findPrevious();
                            } else if (e.key === 'Enter') {
                                e.preventDefault();
                                findNext();
                            } else if (e.key === 'Escape') {
                                closeSearch();
                            }
                        }}
                    />
                    <button
                        className="p-0.5 rounded text-muted hover:text-ink transition-colors"
                        onClick={findPrevious}
                        title="Previous (Shift+Enter)"
                    >
                        <ChevronUp className="size-3.5" />
                    </button>
                    <button
                        className="p-0.5 rounded text-muted hover:text-ink transition-colors"
                        onClick={findNext}
                        title="Next (Enter)"
                    >
                        <ChevronDown className="size-3.5" />
                    </button>
                    <button
                        className="p-0.5 rounded text-muted hover:text-ink transition-colors"
                        onClick={closeSearch}
                        title="Close (Escape)"
                    >
                        <X className="size-3.5" />
                    </button>
                </div>
            )}

            {/* xterm container */}
            <div ref={containerRef} className="h-full w-full" />
        </div>
    );
}
