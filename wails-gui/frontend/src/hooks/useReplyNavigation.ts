import { useCallback, useEffect, useRef, useState, type RefObject } from 'react';

interface ReplyRow {
    id: string;
    kind: string;
    content?: string;
    message?: { kind: string; content?: string };
}

/** The current answer belongs to the latest user turn, not older history. */
function currentAnswer(items: readonly ReplyRow[] | null) {
    for (let index = (items?.length ?? 0) - 1; index >= 0; index--) {
        const item = items![index];
        const kind = item.message?.kind ?? item.kind;
        if (kind === 'user') break;
        if (kind === 'assistant') return { id: item.id, content: item.message?.content ?? item.content ?? '' };
    }
    return null;
}

/** Tracks appended answers separately from history and streaming text. The
 * reader keeps their place until they return to the bottom or explicitly jump. */
export function useReplyNavigation(
    items: readonly ReplyRow[] | null,
    root: RefObject<HTMLDivElement>,
    { enabled = true, viewportSelector, bottom, streaming = false }: {
        enabled?: boolean;
        streaming?: boolean;
        viewportSelector?: string;
        bottom?: RefObject<HTMLDivElement>;
    } = {},
) {
    const userScrolledRef = useRef(false);
    const lastItemId = useRef(items?.at(-1)?.id);
    const unseen = useRef(new Set<string>());
    // Acknowledged replies must not reappear on their next streaming flush.
    const announced = useRef(new Set<string>());
    const previousAnswer = useRef(currentAnswer(items));
    const wasStreaming = useRef(streaming);
    const [count, setCount] = useState(0);
    const [farUp, setFarUp] = useState(false);
    const viewport = useCallback(() => viewportSelector
        ? root.current?.querySelector<HTMLElement>(viewportSelector)
        : root.current, [root, viewportSelector]);
    const clear = useCallback(() => {
        unseen.current.clear();
        setCount(0);
        setFarUp(false);
    }, []);
    const jumpToLatest = useCallback(() => {
        userScrolledRef.current = false;
        clear();
        if (bottom?.current) bottom.current.scrollIntoView({ block: 'end', behavior: 'instant' as ScrollBehavior });
        else {
            const list = viewport();
            if (list) list.scrollTop = list.scrollHeight;
        }
    }, [bottom, clear, viewport]);

    useEffect(() => {
        const previous = lastItemId.current;
        const index = items?.findIndex(item => item.id === previous) ?? -1;
        const answer = currentAnswer(items);
        if (!enabled || (previous !== undefined && index < 0)) {
            userScrolledRef.current = false;
            announced.current.clear();
            clear();
        } else if (userScrolledRef.current && index >= 0) {
            const add = (id: string) => {
                if (announced.current.has(id)) return;
                announced.current.add(id);
                unseen.current.add(id);
            };
            for (const item of items?.slice(index + 1) ?? []) {
                if (item.kind === 'assistant' || item.message?.kind === 'assistant') add(item.id);
            }
            const grew = answer && answer.id === previousAnswer.current?.id
                && answer.content.length > previousAnswer.current.content.length;
            const finished = wasStreaming.current && !streaming;
            if (answer && (grew || finished)) add(answer.id);
            setCount(unseen.current.size);
        }
        lastItemId.current = items?.at(-1)?.id;
        previousAnswer.current = answer;
        wasStreaming.current = streaming;
    }, [items, enabled, streaming, clear]);

    useEffect(() => {
        if (!enabled) return;
        const list = viewport();
        if (!list) return;
        const onScroll = () => {
            const distance = list.scrollHeight - list.scrollTop - list.clientHeight;
            userScrolledRef.current = distance >= 80;
            if (!userScrolledRef.current) clear();
            else setFarUp(distance > list.clientHeight);
        };
        list.addEventListener('scroll', onScroll, { passive: true });
        return () => list.removeEventListener('scroll', onScroll);
    }, [viewport, clear, enabled]);

    return { count, visible: enabled && (count > 0 || farUp), jumpToLatest, userScrolledRef };
}
