/** A reference to a stored Today item, resolved and privacy-filtered by Go.
 * The generated title token is prepared by Go and revalidated at send time. */
export interface TodayChatContextRef {
    /** Content-free product-map identifier for an immediate chip send. */
    telemetryAction?: string;
    /** Optional Activity civil-day selection, revalidated by Go. */
    dayStartMs?: number;
    sourceKind: string;
    sourceId: string;
    briefId?: string;
    /** The prepared title token, revalidated by Go at send time. */
    generatedTitle?: string;
    /** Prepared attendee names are also revalidated by Go before sending. */
    generatedPeople?: string[];
    /** Includes all generated source facts, so stale dates cannot be sent. */
    generatedFingerprint?: string;
    /** A brief card's words as shown; only refuses them sent unedited once
     *  they no longer stand. */
    generatedMessage?: string;
    /** Stable digest of the selected brief paragraph, validated by Go. */
    paragraphDigest?: string;
    meetingPrep?: boolean;
    meetingNote?: boolean;
    briefDraft?: boolean;
    meetingStartMs?: number;
}

export interface TodayContextCounts {
    memories: number;
    activities: number;
    people: number;
}

export interface TodayChatPrefill {
    kind: string;
    title: string;
    durationMinutes?: number;
    occurredAt?: number;
    startsAt?: number;
    people?: string[];
    waitingSince?: number;
}

export interface TodayChatDraft {
    prefill: TodayChatPrefill;
    counts: TodayContextCounts;
    fingerprint: string;
    /** A brief card's exact words, from the computer; sent as they are. */
    message?: string;
}

/** "Talk about this brief": Isoform's opener is already in the main chat, and
 *  the brief goes with the user's answer. */
export interface TodayBriefOpened {
    ref: TodayChatContextRef;
    draft: TodayChatDraft;
}

/** A brief card's reference: the computer finds the card in the brief as it
 *  delivers it now. message is the card's words as Today showed them: sent
 *  unedited after they stopped standing, they are refused. */
export function todayBriefCardRef(briefId: string, cardId: string, message?: string): TodayChatContextRef {
    return { sourceKind: "brief_card", sourceId: cardId, briefId, ...(message ? { generatedMessage: message } : {}) };
}

/** One action prepared in a batch. error is "unavailable" when the item
 *  cannot be read now, "paused" while observation is paused, "failed" for
 *  anything else. */
export interface TodayChatDraftResult {
    draft?: TodayChatDraft;
    error?: "unavailable" | "paused" | "failed" | string;
}

/** "fallback": the item's own context could not be prepared, so the draft
 *  carries whatever context is available (counts present when it does).
 *  "paused": observation is paused, so the draft carries no observed context. */
export type TodayChatPreview =
    | { status: "loading" | "unavailable" | "paused" }
    | { status: "ready"; counts: TodayContextCounts }
    | { status: "fallback"; counts?: TodayContextCounts };

/** The only reference a draft carries while observation is paused: Go reads
 *  nothing for it and tells the model that observation is paused. */
export const TODAY_CHAT_PAUSED_REF: TodayChatContextRef = { sourceKind: "paused", sourceId: "observation" };

/** How a Today action opens the main chat with its words in the box: news,
 *  the day's summary and the brief's cards (todayBriefCardRef). */
export interface TodayChatStartOptions {
    /** Observation is paused: use the card's own text and read nothing. */
    paused?: boolean;
}

interface TodayChatNavigation {
    openMainChat: () => string | null | undefined | Promise<string | null | undefined>;
    prefill: (sessionId: string, prompt: string, context: TodayChatContextRef, options?: TodayChatStartOptions) => void | Promise<void>;
    focus: () => void;
    isCurrent?: () => boolean;
}

/** Wait for the main session before attaching the draft to it. Opening a
 *  Today item never creates a side chat or sends anything by itself. */
export async function openTodayMainChat(
    prompt: string,
    context: TodayChatContextRef,
    navigation: TodayChatNavigation,
    options?: TodayChatStartOptions,
): Promise<boolean> {
    if (!prompt.trim()) return false;
    const sessionId = await navigation.openMainChat();
    if (!sessionId) return false;
    if (navigation.isCurrent && !navigation.isCurrent()) return true;
    const prefilled = options ? navigation.prefill(sessionId, prompt, context, options) : navigation.prefill(sessionId, prompt, context);
    // The draft is in the composer before preparation finishes; focus it now.
    if (!navigation.isCurrent || navigation.isCurrent()) navigation.focus();
    await prefilled;
    return true;
}

/** Owns the one visible brief-opening request. A cancelled or completed old
 * request cannot release the slot of a newer request. */
export class TodayBriefRequestGate {
    private current: AbortController | null = null;

    get pending(): boolean { return this.current !== null; }

    begin(): AbortController | null {
        if (this.current) return null;
        this.current = new AbortController();
        return this.current;
    }

    cancel(): void {
        const current = this.current;
        this.current = null;
        current?.abort();
    }

    finish(request: AbortController): void {
        if (this.current === request) this.current = null;
    }
}

/** Open Chat immediately while the brief is checked. A deadline also covers
 * a stalled Wails promise or main-session load; late results cannot attach. */
export async function openTodayBriefChat(
    prepare: () => Promise<TodayBriefOpened>,
    navigation: {
        openMainChat: (isCurrent: () => boolean) => Promise<string | null>;
        draft: { select: (sessionId: string) => void; finish: (opened?: TodayBriefOpened) => void };
        focus: () => void;
        isCurrent: () => boolean;
        signal?: AbortSignal;
    },
    timeoutMs = 12000,
): Promise<boolean> {
    let selectionExpired = false;
    let finished = false;
    const isCurrent = () => !selectionExpired && !navigation.signal?.aborted && navigation.isCurrent();
    const bounded = async <T>(operation: Promise<T>, onTimeout?: () => void): Promise<T> => {
        let timer: ReturnType<typeof setTimeout> | undefined;
        let abort: (() => void) | undefined;
        try {
            const cancelled = new Promise<never>((_, reject) => {
                abort = () => reject(new Error('cancelled'));
                if (navigation.signal?.aborted) abort();
                else navigation.signal?.addEventListener('abort', abort, { once: true });
            });
            return await Promise.race([
                operation,
                cancelled,
                new Promise<never>((_, reject) => { timer = setTimeout(() => { onTimeout?.(); reject(new Error('timeout')); }, timeoutMs); }),
            ]);
        } finally {
            if (timer !== undefined) clearTimeout(timer);
            if (abort) navigation.signal?.removeEventListener('abort', abort);
        }
    };
    try {
        const sessionId = await bounded(navigation.openMainChat(isCurrent), () => { selectionExpired = true; });
        if (!isCurrent()) return true;
        if (!sessionId) return false;
        navigation.draft.select(sessionId);
        navigation.focus();
        const opened = await bounded(prepare());
        if (!isCurrent()) return true;
        navigation.draft.finish(opened);
        finished = true;
        return true;
    } catch {
        return !!navigation.signal?.aborted;
    } finally {
        if (!finished) navigation.draft.finish();
    }
}
