import { useSyncExternalStore } from 'react';
import { GetMainChatWaiting } from '@/lib/wails-shim-app';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { onPagePrivacyWithdrawal } from './page-privacy-withdrawal';

export type MainChatWaitingState = Omit<
    import('../../wailsjs/go/models').main.MainChatWaitingState,
    'convertValues'
> & {
    day: string;
    conversations: number;
    finished: number;
    routine: number;
};

const EMPTY_STATE: MainChatWaitingState = {
    sessionId: '',
    day: '',
    count: 0,
    conversations: 0,
    finished: 0,
    routine: 0,
    items: [],
    revision: 0,
    ready: false,
};

function normalizedCount(value: unknown): number {
    return typeof value === 'number' && Number.isFinite(value) ? Math.max(0, Math.floor(value)) : 0;
}

function normalizeWaitingState(value: unknown): MainChatWaitingState {
    const wire = (value ?? {}) as Record<string, unknown>;
    return {
        sessionId: typeof wire.sessionId === 'string' ? wire.sessionId : '',
        day: typeof wire.day === 'string' ? wire.day : '',
        count: normalizedCount(wire.count),
        conversations: normalizedCount(wire.conversations),
        finished: normalizedCount(wire.finished),
        routine: normalizedCount(wire.routine),
        items: Array.isArray(wire.items) ? wire.items as MainChatWaitingState['items'] : [],
        revision: normalizedCount(wire.revision),
        ready: wire.ready === true,
    };
}

let state = EMPTY_STATE;
let generation = 0;
let started = false;
let privacyFence = false;
let pendingEvent: MainChatWaitingState | null = null;
const listeners = new Set<() => void>();
const cleanups: Array<() => void> = [];

function publish(next: MainChatWaitingState) {
    state = next;
    for (const listener of [...listeners]) listener();
}

function accept(next: MainChatWaitingState, requestGeneration: number) {
    if (requestGeneration !== generation || next.revision < state.revision) return;
    publish(next);
}

async function refresh(requestGeneration: number) {
    try {
        const next = normalizeWaitingState(await GetMainChatWaiting());
        if (requestGeneration !== generation) return;
        if (next.revision >= state.revision) publish(next);
        privacyFence = false;
    } catch {
        if (requestGeneration !== generation) return;
        privacyFence = false;
        publish({ ...state, ready: false, count: 0, items: [] });
    }
    const queued = pendingEvent;
    pendingEvent = null;
    if (queued && queued.revision > state.revision) accept(queued, requestGeneration);
}

function clearAndRefresh() {
    generation++;
    privacyFence = true;
    pendingEvent = null;
    publish({ ...state, count: 0, items: [], ready: false });
    void refresh(generation);
}

function start() {
    if (started) return;
    started = true;
    cleanups.push(EventsOn('main-chat:waiting-changed', (value: MainChatWaitingState) => {
        const next = normalizeWaitingState(value);
        if (privacyFence) {
            if (!pendingEvent || next.revision > pendingEvent.revision) pendingEvent = next;
        } else if (next.revision > state.revision) accept(next, generation);
    }));
    cleanups.push(onPagePrivacyWithdrawal(clearAndRefresh));
    cleanups.push(EventsOn('session:deleted', clearAndRefresh));
    void refresh(generation);
}

function subscribe(listener: () => void) {
    listeners.add(listener);
    start();
    return () => listeners.delete(listener);
}

function getSnapshot() {
    return state;
}

export function useMainChatWaiting() {
    return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}

export function resetMainChatWaitingStore() {
    cleanups.splice(0).forEach((off) => off());
    generation++;
    privacyFence = false;
    pendingEvent = null;
    started = false;
    state = EMPTY_STATE;
    listeners.clear();
}
