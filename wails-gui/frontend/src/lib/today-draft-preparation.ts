import type { TFunction } from 'i18next';
import { TODAY_CHAT_PAUSED_REF, type TodayChatContextRef, type TodayChatDraft, type TodayChatPreview, type TodayChatStartOptions } from './today-chat-navigation';
import { formatTodayChatPrefill } from './today-chat-prefill';
import { isFreshTodayChatDraft, peekTodayChatDraft, prepareTodayChatDraftNow } from './today-chat-prepared';

/**
 * A box a Today action fills: the main chat's composer and the panel's box
 * on Today share one preparation (prepareTodayDraft) through this. The box
 * owns its words, the reference Go checks again at Send, and the context
 * line; the flow only tells it what to show.
 */
export interface TodayDraftBox {
    /** Replace the words, the reference and the context line at once. */
    fill(text: string, context: TodayChatContextRef | undefined, preview: TodayChatPreview): void;
    /** Change the live draft: `context` only when the key is present (it may
     *  be set to none), `text` only when given. */
    update(patch: { context?: TodayChatContextRef | undefined; preview?: TodayChatPreview; text?: string }): void;
    /** Drop the draft: empty box, no reference, no context line. */
    clear(): void;
    /** The box still holds the draft fill() made, in the place it was made
     *  for; otherwise the result is dropped. */
    stillApplies(): boolean;
    /** The words are as fill() left them. */
    unedited(): boolean;
    /** The card left the brief and its untouched words were cleared. */
    onCardGone(): void;
}

/** The words a prepared draft shows: the computer's own message for a card,
 *  else the item's prefill. */
export function todayDraftText(prepared: TodayChatDraft, t: TFunction<'today'>, language: string): string {
    return prepared.message?.trim() ? prepared.message : formatTodayChatPrefill(prepared.prefill, t, language);
}

/** The reference Go checks again at Send: the prepared facts it was read
 *  with travel with it, so a change in between is refused. */
export function todayDraftContext(context: TodayChatContextRef, prepared: TodayChatDraft): TodayChatContextRef {
    return {
        ...context,
        generatedTitle: prepared.prefill.title,
        generatedFingerprint: prepared.fingerprint,
        ...(prepared.prefill.people?.length ? { generatedPeople: prepared.prefill.people } : {}),
        ...(prepared.message?.trim() ? { generatedMessage: prepared.message } : {}),
    };
}

/**
 * Put a Today action's text in a box at once and attach its context. The
 * draft Today prepared before the click is used directly when fresh; an
 * older one fills the box at once and is checked again before Send. Without
 * a prepared draft the card's own prompt fills the box while the item is
 * prepared. If the item cannot be prepared, the card's prompt stays without
 * context and with a quiet note, never an empty box. A brief card (backlog
 * item 64) works the same way: its prepared words fill the box at once and
 * nothing is sent; the computer's current words replace them while
 * untouched, and Send reads the card's sources again.
 */
export async function prepareTodayDraft(
    box: TodayDraftBox,
    cardPrompt: string,
    context: TodayChatContextRef,
    options: TodayChatStartOptions,
    t: TFunction<'today'>,
    language: string,
): Promise<void> {
    const textFor = (prepared: TodayChatDraft) => todayDraftText(prepared, t, language);
    const contextFor = (prepared: TodayChatDraft) => todayDraftContext(context, prepared);
    if (options.paused && context.sourceKind !== 'brief_card') {
        // Observation is paused: the card's own words, nothing read, and a
        // note in place of the context line. A paused draft never sends
        // itself.
        box.fill(cardPrompt, TODAY_CHAT_PAUSED_REF, { status: 'paused' });
        return;
    }
    // A pre-pause draft may contain more than the brief. Ask Go for the
    // restricted, revalidated brief context on every paused card click.
    const cached = options.paused ? undefined : peekTodayChatDraft(context);
    if (cached?.status === 'ready' && isFreshTodayChatDraft(cached)) {
        box.fill(textFor(cached.draft), contextFor(cached.draft), { status: 'ready', counts: cached.draft.counts });
        return;
    }
    const usedCached = cached?.status === 'ready';
    if (usedCached) {
        box.fill(textFor(cached.draft), contextFor(cached.draft), { status: 'loading' });
    } else {
        box.fill(cardPrompt, context, { status: 'loading' });
    }
    const result = await prepareTodayChatDraftNow(context);
    if (!box.stillApplies()) return;
    if (result.status === 'ready') {
        let prepared = contextFor(result.draft);
        // Card words the user already edited keep the words they were
        // shown: sent back unchanged once stale, those are refused.
        if (context.sourceKind === 'brief_card' && !box.unedited()) prepared = { ...prepared, generatedMessage: context.generatedMessage };
        // Typing while sources load is user intent, even if it happens
        // before the first generated word reaches the box.
        box.update({ context: prepared, preview: { status: 'ready', counts: result.draft.counts }, ...(box.unedited() ? { text: textFor(result.draft) } : {}) });
        return;
    }
    if (usedCached && !result.unavailable) {
        // The check itself could not run, for example a timeout. Keep the
        // prepared draft: Send repeats every read-time gate in any case.
        box.update({ preview: { status: 'ready', counts: cached.draft.counts } });
        return;
    }
    if (context.sourceKind === 'brief_card') {
        // The card left the brief, could not be checked, or observation was
        // paused: its words may no longer stand. Untouched, they leave with
        // a quiet note; otherwise the card stays attached and Send asks Go,
        // which refuses rather than send them unchecked.
        if (result.paused) {
            box.update({ preview: { status: 'paused' } });
            return;
        }
        if (result.unavailable && box.unedited()) {
            box.clear();
            box.onCardGone();
            return;
        }
        box.update({ preview: { status: 'unavailable' } });
        return;
    }
    if (usedCached && !box.unedited()) {
        // The user edited a draft that named the item, and the item can no
        // longer be read. Keep its reference: Send then asks Go, which
        // refuses rather than letting the old title through unchecked.
        box.update({ preview: { status: 'unavailable' } });
        return;
    }
    if (result.paused) {
        // Observation was paused after Today rendered: as for a paused click.
        box.update({ context: TODAY_CHAT_PAUSED_REF, preview: { status: 'paused' }, ...(usedCached ? { text: cardPrompt } : {}) });
        return;
    }
    // The item's own context is not available now. Keep the card's own
    // prompt without it, with a quiet note. A prepared draft named the item;
    // the card's own prompt does not.
    box.update({ context: undefined, preview: { status: 'fallback' }, ...(usedCached ? { text: cardPrompt } : {}) });
}

/** Mirrors mobileBriefCardLabelVerbs in mobile_brief_cards.go; the parity test
 *  checks the complete set so backend-approved requests keep their labels. */
export const BRIEF_CARD_LABEL_VERBS = [
    'add', 'answer', 'ask', 'book', 'call', 'check', 'confirm', 'continue',
    'decide', 'draft', 'email', 'file', 'find', 'finish', 'fix', 'follow', 'message',
    'note', 'open', 'order', 'pay', 'pick', 'plan', 'prep', 'prepare', 'reply', 'review',
    'schedule', 'send', 'set', 'share', 'start', 'unblock', 'update', 'write',
] as const;
