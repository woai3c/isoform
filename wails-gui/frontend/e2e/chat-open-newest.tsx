// Browser fixture for R36: the chat stream must open on its newest item
// whether it mounts with the full history, grows from a short snapshot, or
// remounts after the page was left. Real layout, real react-virtuoso.
import React, { useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';
import i18n from '@/i18n';
import '@/index.css';
import { StreamBody } from '@/components/stream-body';
import type { StreamItem } from '@/types';
import { RetainedPageActiveContext } from '@/lib/retained-page-active';

const noop = () => {};
(window as any).go = { main: { App: new Proxy({}, { get: () => async () => null }) } };
(window as any).runtime = { EventsOn: () => noop };

function makeTurns(startIndex: number, pairs: number): StreamItem[] {
    const items: StreamItem[] = [];
    for (let i = startIndex; i < startIndex + pairs; i++) {
        items.push({ id: `u-${i}`, kind: 'user', content: `Question ${i}`, sortOrder: i * 2 });
        items.push({ id: `a-${i}`, kind: 'assistant', content: `Answer ${i} with **markdown**`, sortOrder: i * 2 + 1 });
    }
    return items;
}

function Harness() {
    const [items, setItems] = useState<StreamItem[]>(() => makeTurns(1190, 10));
    const [mounted, setMounted] = useState(true);
    const [session, setSession] = useState('e2e-open-newest');
    const [hidden, setHidden] = useState(false);
    const count = useMemo(() => items.length, [items]);
    return (
        <div className="flex h-screen flex-col">
            <div className="flex gap-2 p-2">
                <button data-testid="grow" onClick={() => setItems(makeTurns(1000, 200))}>grow</button>
                <button data-testid="grow-newer" onClick={() => setItems(makeTurns(1000, 250))}>grow-newer</button>
                <button data-testid="unmount" onClick={() => setMounted(false)}>unmount</button>
                <button data-testid="remount" onClick={() => setMounted(true)}>remount</button>
                <button data-testid="other-session" onClick={() => setSession((s) => s + '-b')}>other</button>
                <button data-testid="hide" onClick={() => setHidden(true)}>hide</button>
                <button data-testid="show" onClick={() => setHidden(false)}>show</button>
                <span data-testid="count">{count}</span>
            </div>
            {mounted && (
                // Like a RetainedPage: hidden with display none, its activity reported to the pane.
                <RetainedPageActiveContext.Provider value={!hidden}>
                    <div style={{ display: hidden ? 'none' : 'flex', flex: 1, minHeight: 0, flexDirection: 'column' }}>
                        <StreamBody items={items} streaming={false} hasProject sessionLocalId={session} />
                    </div>
                </RetainedPageActiveContext.Provider>
            )}
        </div>
    );
}

async function mount() {
    const theme = new URLSearchParams(location.search).get('theme');
    if (theme) document.documentElement.dataset.theme = theme;
    await i18n.changeLanguage('en');
    createRoot(document.getElementById('root')!).render(<Harness />);
}
void mount();
