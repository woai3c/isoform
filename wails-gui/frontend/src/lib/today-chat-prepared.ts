import { PrepareTodayChatDraft, PrepareTodayChatDrafts } from '../../wailsjs/go/main/App';
import type { TodayChatContextRef, TodayChatDraft } from './today-chat-navigation';

/**
 * Today prepares every chat action before the user clicks one: the draft
 * text facts, the context counts and the tokens Go revalidates at Send.
 * Clicking reads this cache synchronously, so the composer fills at once.
 * Nothing here is a privacy gate; Send repeats every read-time check.
 */

/** A prepared draft is used as-is for this long. An older one still fills the
 *  composer at once, but is checked again before it can be sent. */
export const TODAY_DRAFT_FRESH_MS = 60_000;

export type SettledTodayDraft =
    | { status: 'ready'; draft: TodayChatDraft; preparedAt: number }
    /** paused: observation was paused, so nothing was read. */
    | { status: 'failed'; unavailable: boolean; paused?: boolean; preparedAt: number };

export type PreparedTodayDraft = { status: 'pending'; settled: Promise<void> } | SettledTodayDraft;

interface Entry {
    group: string;
    /** The item and brief version the draft was prepared for. */
    signature: string;
    state: PreparedTodayDraft;
    /** Guards against an older batch overwriting a newer one. */
    batch: number;
}

const entries = new Map<string, Entry>();
let batches = 0;

export function todayChatRefKey(ref: TodayChatContextRef): string {
    return JSON.stringify([ref.sourceKind, ref.sourceId, ref.briefId ?? '', ...(ref.dayStartMs ? [ref.dayStartMs] : [])]);
}

export interface TodayDraftWarmItem {
    ref: TodayChatContextRef;
    /** Changes whenever the item or its brief changes. */
    signature: string;
}

/**
 * Prepare a group of Today actions in one read. An action whose signature
 * changed is prepared again; actions the group no longer shows are dropped.
 * A failed action is not retried here: a click prepares it again.
 */
export function warmTodayChatDrafts(group: string, items: TodayDraftWarmItem[]): void {
    const shown = new Set<string>();
    const pending: { key: string; ref: TodayChatContextRef; signature: string }[] = [];
    for (const { ref, signature } of items) {
        const key = todayChatRefKey(ref);
        if (shown.has(key)) continue;
        shown.add(key);
        const existing = entries.get(key);
        if (existing && existing.signature === signature) {
            existing.group = group;
            continue;
        }
        pending.push({ key, ref, signature });
    }
    for (const [key, entry] of entries) {
        if (entry.group === group && !shown.has(key)) entries.delete(key);
    }
    if (pending.length === 0) return;
    const batch = ++batches;
    // A draft is as old as the read it came from, not its arrival.
    const startedAt = Date.now();
    let settle!: () => void;
    const settled = new Promise<void>((resolve) => { settle = resolve; });
    for (const { key, signature } of pending) {
        entries.set(key, { group, signature, batch, state: { status: 'pending', settled } });
    }
    const finish = (stateAt: (index: number) => SettledTodayDraft) => {
        pending.forEach(({ key }, index) => {
            const entry = entries.get(key);
            if (entry?.batch === batch && entry.state.status === 'pending') entry.state = stateAt(index);
        });
        settle();
    };
    Promise.resolve()
        .then(() => PrepareTodayChatDrafts(pending.map(({ ref }) => ref)))
        .then((results) => {
            const preparedAt = startedAt;
            finish((index) => {
                const result = results?.[index];
                if (result?.draft) return { status: 'ready', draft: result.draft, preparedAt };
                const paused = result?.error === 'paused';
                return { status: 'failed', unavailable: paused || result?.error === 'unavailable', ...(paused ? { paused } : {}), preparedAt };
            });
        })
        .catch(() => {
            finish(() => ({ status: 'failed', unavailable: false, preparedAt: startedAt }));
        });
}

export function peekTodayChatDraft(ref: TodayChatContextRef): PreparedTodayDraft | undefined {
    return entries.get(todayChatRefKey(ref))?.state;
}

export function isFreshTodayChatDraft(state: PreparedTodayDraft | undefined, now = Date.now()): boolean {
    return state?.status === 'ready' && now - state.preparedAt < TODAY_DRAFT_FRESH_MS;
}

/** Prepare one action now (a click on a stale, failed or unknown draft) and
 *  keep the result for the next click. A pending batch is not waited for: it
 *  may hold many slower actions, while this reads one. */
export async function prepareTodayChatDraftNow(ref: TodayChatContextRef): Promise<SettledTodayDraft> {
    const key = todayChatRefKey(ref);
    const before = entries.get(key);
    const startedAt = Date.now();
    let state: SettledTodayDraft;
    try {
        state = { status: 'ready', draft: await PrepareTodayChatDraft(ref), preparedAt: startedAt };
    } catch (err) {
        const message = err instanceof Error ? err.message : String(err);
        const paused = message.includes('TODAY_CHAT_OBSERVATION_PAUSED');
        state = { status: 'failed', unavailable: paused || message.includes('TODAY_CHAT_CONTEXT_UNAVAILABLE'), ...(paused ? { paused } : {}), preparedAt: startedAt };
    }
    // Keep the result for the next click unless Today has since started
    // preparing this action again for a changed item or brief. A batch still
    // running for the same action settles the entry when it finishes.
    const entry = entries.get(key);
    if (entry && entry === before && entry.state.status !== 'pending') entry.state = state;
    return state;
}

/** Test hook: forget every prepared draft. */
export function resetTodayChatDrafts(): void {
    entries.clear();
}
