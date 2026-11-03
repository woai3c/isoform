// Browser fixture for the windowed chat stream: real layout, real
// react-virtuoso, synthetic rows. Verifies scroll anchoring on prepend and
// that appends never move the reading position.
import React, { useMemo, useState } from 'react';
import { createRoot } from 'react-dom/client';
import i18n from '@/i18n';
import '@/index.css';
import { StreamBody } from '@/components/stream-body';
import type { StreamItem } from '@/types';

const noop = () => {};
// In-memory reads before React mounts; the fixture never contacts the
// running desktop, its browser bridge, or its persisted state.
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
    const [start, setStart] = useState(1000);
    const [count, setCount] = useState(200);
    const items = useMemo(() => makeTurns(start, count), [start, count]);
    return (
        <div className="flex h-screen flex-col">
            <div className="flex gap-2 p-2">
                <button data-testid="prepend" onClick={() => { setStart((s) => s - 50); setCount((c) => c + 50); }}>
                    prepend
                </button>
                <button data-testid="append" onClick={() => setCount((c) => c + 50)}>
                    append
                </button>
            </div>
            <StreamBody items={items} streaming={false} hasProject sessionLocalId="e2e-stream-window" />
        </div>
    );
}

async function mount() {
    await i18n.changeLanguage('en');
    createRoot(document.getElementById('root')!).render(<Harness />);
}
void mount();
