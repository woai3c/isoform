/**
 * One card per idea while saved copies wait for the data merge (design review
 * v1.0.2 W40, Y33). Copies whose titles match show as one card, the newest.
 *
 * The match is the title's words in lower case, with punctuation and
 * whitespace dropped and word order ignored. It is deliberately narrower than
 * the Go lesson matcher: a forget applies to every copy in a group, so a looser
 * match could forget a different idea.
 */

/** The normalized title key: equal keys are the same idea. Empty when the title has no words. */
export function ideaKey(title: string): string {
    return title
        .normalize('NFKC')
        .toLocaleLowerCase()
        .split(/[^\p{L}\p{N}]+/u)
        .filter(Boolean)
        .sort()
        .join(' ');
}

export interface IdeaGroup<T> {
    key: string;
    /** Every copy of the idea, newest first. copies[0] is the card's head. */
    copies: T[];
}

/**
 * Groups items by idea, keeping the order in which each idea first appears.
 * An item whose title has no words stays a group of its own.
 */
export function groupIdeas<T>(
    items: readonly T[],
    titleOf: (item: T) => string,
    savedAtOf: (item: T) => number,
): IdeaGroup<T>[] {
    const groups = new Map<string, T[]>();
    items.forEach((item, index) => {
        const key = ideaKey(titleOf(item)) || `\u0000${index}`;
        const list = groups.get(key);
        if (list) list.push(item);
        else groups.set(key, [item]);
    });
    return [...groups].map(([key, copies]) => ({
        key,
        copies: [...copies].sort((a, b) => savedAtOf(b) - savedAtOf(a)),
    }));
}

/** The copy a card shows: the newest one the view can show, or none. */
export function ideaHead<T>(group: IdeaGroup<T>, visible: (item: T) => boolean = () => true): T | undefined {
    return group.copies.find(visible);
}

/** Every id in the groups, for an action that must reach each copy. */
export function ideaCopyIds<T>(group: IdeaGroup<T>, idOf: (item: T) => string): string[] {
    return group.copies.map(idOf);
}

/** Items from several copies, kept once each by key, in first-seen order. */
export function uniqueBy<T>(items: readonly T[], keyOf: (item: T) => string): T[] {
    const seen = new Map<string, T>();
    for (const item of items) {
        const key = keyOf(item);
        if (!seen.has(key)) seen.set(key, item);
    }
    return [...seen.values()];
}
