import { useCallback, useEffect, useRef, useState } from 'react';
import { FindStreamItemCursor } from '@/lib/wails-shim-app';
import { loadStreamItemsPageWithOpenAnswerDelivery } from '@/lib/open-answer-delivery';
import { fromStreamItemData } from '@/lib/stream-helpers';
import type { Project, Session, StreamItem, StreamItemData } from '@/types';

/** Rows loaded above the found message, so it does not sit at the very top. */
const CONTEXT_ABOVE = 10;
/** The most rows one load asks for; an old message takes several loads. */
export const JUMP_PAGE_LIMIT = 200;

export interface ChatMessageFocus {
    sessionId: string;
    itemId: string;
    /** Bumps on every jump, so asking for the same message scrolls again. */
    signal: number;
}

/** One jump in flight. A newer jump, or leaving the chat, replaces it. */
interface Jump {
    sessionId: string;
    itemId: string;
    /** FindStreamItemCursor's exclusive bound (the item's sort order + 1);
     *  null until it answers. */
    cursor: number | null;
    /** The chat has been the open one since the jump started. */
    sawTarget: boolean;
    loading: boolean;
}

interface ChatMessageJumpOptions {
    projects: Project[];
    activeSession: Session | null | undefined;
    updateProject: (id: string, update: (project: Project) => Project) => void;
    /** Opens the chat (App's session switch, which also shows the chat view). */
    openSession: (projectId: string, sessionId: string) => void;
    /** The message is gone or hidden: say so. */
    onFailed: () => void;
}

function projectIdOf(projects: Project[], sessionId: string): string | null {
    for (const project of projects) {
        for (const workspace of project.workspaces) {
            if (workspace.sessions.some((session) => session.localId === sessionId)) return project.id;
        }
    }
    return null;
}

/** The next older page to load toward the item at `cursor - 1`, with no gap
 *  after what is loaded: at most JUMP_PAGE_LIMIT rows. Null when the item
 *  should already be loaded or nothing older is left. */
export function olderPageThrough(session: Pick<Session, 'nextCursor' | 'hasMore'>, cursor: number): { before: number; limit: number } | null {
    if (!session.hasMore || cursor > session.nextCursor) return null;
    // Sort orders can be sparse, so this many rows always reach the item.
    const needed = session.nextCursor - cursor + 1 + CONTEXT_ABOVE;
    return { before: session.nextCursor, limit: Math.min(needed, JUMP_PAGE_LIMIT) };
}

/**
 * Opens a chat at one message, for Search's chat results. The chat opens at
 * once; the message's cursor (FindStreamItemCursor) says how far back to
 * load; older pages are prepended with no gap until the message is in; the
 * returned focus tells the chat view which message to scroll to. Leaving the
 * chat, or a newer jump, drops the one in flight.
 */
export function useChatMessageJump({ projects, activeSession, updateProject, openSession, onFailed }: ChatMessageJumpOptions) {
    const [focus, setFocus] = useState<ChatMessageFocus | null>(null);
    // Bumped when a jump's cursor arrives, so the load effect runs.
    const [tick, setTick] = useState(0);
    const job = useRef<Jump | null>(null);
    const signal = useRef(0);
    const activeId = activeSession?.localId;
    const latest = useRef({ projects, openSession, onFailed, activeId });
    latest.current = { projects, openSession, onFailed, activeId };

    const fail = useCallback((jump: Jump) => {
        if (job.current !== jump) return;
        job.current = null;
        latest.current.onFailed();
    }, []);

    const jump = useCallback(async (sessionId: string, itemId: string) => {
        const projectId = projectIdOf(latest.current.projects, sessionId);
        if (!projectId) {
            job.current = null;
            latest.current.onFailed();
            return;
        }
        const current: Jump = {
            sessionId,
            itemId,
            cursor: null,
            sawTarget: latest.current.activeId === sessionId,
            loading: false,
        };
        job.current = current;
        latest.current.openSession(projectId, sessionId);
        let cursor: number;
        try {
            cursor = await FindStreamItemCursor(sessionId, itemId);
        } catch {
            fail(current);
            return;
        }
        if (job.current !== current) return;
        current.cursor = cursor;
        setTick((n) => n + 1);
    }, [fail]);

    // The person opened another chat before the jump finished: drop it, so
    // coming back later does not scroll to the old message.
    useEffect(() => {
        const current = job.current;
        if (!current) return;
        if (activeId === current.sessionId) current.sawTarget = true;
        else if (current.sawTarget) job.current = null;
    }, [activeId]);

    useEffect(() => {
        const current = job.current;
        if (!current || current.cursor === null || current.loading) return;
        if (!activeSession || activeSession.localId !== current.sessionId) return;
        if (!activeSession.itemsLoaded || activeSession.itemsLoading || activeSession.loadingMore) return;
        if (activeSession.items.some((item) => item.id === current.itemId)) {
            job.current = null;
            signal.current += 1;
            setFocus({ sessionId: current.sessionId, itemId: current.itemId, signal: signal.current });
            return;
        }
        const page = olderPageThrough(activeSession, current.cursor);
        const projectId = projectIdOf(latest.current.projects, current.sessionId);
        if (!page || !projectId) {
            fail(current);
            return;
        }
        current.loading = true;
        loadStreamItemsPageWithOpenAnswerDelivery(current.sessionId, page.before, page.limit)
            .then((loaded: { items?: StreamItemData[] | null; hasMore: boolean; nextCursor: number }) => {
                current.loading = false;
                if (job.current !== current) return;
                if (loaded.hasMore && loaded.nextCursor >= page.before) {
                    // No progress: never ask for the same page again.
                    fail(current);
                    return;
                }
                const older: StreamItem[] = (loaded.items ?? []).map(fromStreamItemData);
                // The effect runs again with these items: it focuses the
                // message, loads the next page, or reports it gone.
                updateProject(projectId, (project) => ({
                    ...project,
                    workspaces: project.workspaces.map((workspace) => ({
                        ...workspace,
                        sessions: workspace.sessions.map((session) => {
                            if (session.localId !== current.sessionId) return session;
                            const known = new Set(session.items.map((item) => item.id));
                            const unique = older.filter((item) => !known.has(item.id));
                            return {
                                ...session,
                                items: [...unique, ...session.items],
                                streamItemCount: Math.max(session.streamItemCount ?? 0, unique.length + session.items.length),
                                hasMore: loaded.hasMore,
                                nextCursor: loaded.nextCursor,
                            };
                        }),
                    })),
                }));
            })
            .catch(() => {
                current.loading = false;
                fail(current);
            });
    }, [tick, activeSession, updateProject, fail]);

    // The chat view scrolled there: forget it, so the next visit to the chat
    // opens at the bottom as usual.
    const clearFocus = useCallback(() => setFocus(null), []);

    return { jump, focus, clearFocus };
}
