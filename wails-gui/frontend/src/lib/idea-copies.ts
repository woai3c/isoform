/**
 * One card per idea while saved copies wait for the data merge (design review
 * v1.0.2 W40, Y33). Copies that say the same thing show as one card, the newest.
 *
 * Copies are paraphrases, so titles are compared by their words, not their
 * exact text. Two titles are one idea when they have the same words, or when
 * they share at least three distinct words and the words they share carry at
 * least 35% of their weight.
 * A word weighs more the rarer it is across the list (inverse document
 * frequency), so "commit" or "public" alone never joins two ideas. Negations
 * must agree, so "Never merges" and "Merges" stay apart. The threshold comes
 * from the saved copies of one belief in the launch walk: at 0.35 they sort
 * into one group; below 0.3 unrelated beliefs start to join.
 *
 * Forget and Delete on a group reach every copy, so the pages show the copies
 * before they act; see the forget dialog and the know-how delete dialog.
 */

const STOP_WORDS = new Set([
    'the', 'a', 'an', 'to', 'of', 'and', 'or', 'in', 'on', 'for', 'with', 'by', 'is', 'it', 'that', 'as', 'be', 'at',
    'this', 'from', 'you', 'your', 'wants', 'want', 'prefers', 'prefer', 'rather', 'than', 'does', 'do', 'their', 'its',
    'only', 'so', 'into', 'all', 'are', 'when', 'after', 'before', 'every', 'each',
    // The framing of a belief written as "You care about …" or "You're careful …" (Y28).
    'care', 'about', 'careful',
]);
/** Words that flip a title's meaning; a title with one never joins a title without it. */
const NEGATIONS = new Set(['never', 'no', 'nothing', 'without', "don't", 'dont']);
const SIMILARITY_THRESHOLD = 0.35;
const MIN_SHARED_WORDS = 3;

/** The words that decide whether two titles are one idea: lower case, stemmed, stop words removed. */
export function ideaWords(title: string): Set<string> {
    const words = new Set<string>();
    for (const raw of title.normalize('NFKC').toLocaleLowerCase().split(/[^\p{L}\p{N}']+/u)) {
        const word = raw.replace(/^'+|'+$/g, '');
        if (!word || STOP_WORDS.has(word)) continue;
        if (NEGATIONS.has(word)) {
            words.add(`!${word}`);
            continue;
        }
        words.add(word.length > 3 && word.endsWith('s') ? word.slice(0, -1) : word);
    }
    return words;
}

/** A title's words, prepared once: its plain words, its negations and its total weight. */
interface Prepared {
    words: Set<string>;
    negations: string;
    weight: number;
}

function prepare(words: Set<string>, weight: Map<string, number>): Prepared {
    const plain = new Set<string>();
    const negations: string[] = [];
    let total = 0;
    for (const word of words) {
        if (word.startsWith('!')) {
            negations.push(word);
            continue;
        }
        plain.add(word);
        total += weight.get(word) ?? 1;
    }
    return { words: plain, negations: negations.sort().join(' '), weight: total };
}

/** Whether two prepared titles are one idea, weighted by the list's corpus. */
function sameIdea(a: Prepared, b: Prepared, weight: Map<string, number>): boolean {
    if (a.negations !== b.negations) return false;
    const [small, large] = a.words.size <= b.words.size ? [a, b] : [b, a];
    let shared = 0;
    let sharedWeight = 0;
    for (const word of small.words) {
        if (!large.words.has(word)) continue;
        shared++;
        sharedWeight += weight.get(word) ?? 1;
    }
    // The same words are the same idea, however few there are.
    if (shared === a.words.size && shared === b.words.size) return true;
    if (shared < MIN_SHARED_WORDS) return false;
    const unionWeight = a.weight + b.weight - sharedWeight;
    return unionWeight > 0 && sharedWeight / unionWeight >= SIMILARITY_THRESHOLD;
}

export interface IdeaGroup<T> {
    /** The idea's words, joined: a stable key for the group's head. */
    key: string;
    /** Every copy of the idea, newest first. copies[0] is the card's head. */
    copies: T[];
}

/**
 * Groups items by idea. Items are taken newest first; each joins the first
 * group whose head says the same thing, else heads a group of its own. Groups
 * keep the order in which their first item appears in the input.
 * An item whose title has no words stays a group of its own.
 */
export function groupIdeas<T>(
    items: readonly T[],
    titleOf: (item: T) => string,
    savedAtOf: (item: T) => number,
    /**
     * The meaning group of each item (from the app's embedder), aligned with
     * items. When given, two items join one idea when their meaning groups match
     * or their words match (launch walk Y33 and W40: a paraphrase can share no
     * words, and a short title can share words without sharing a meaning).
     */
    meanings?: readonly number[] | null,
): IdeaGroup<T>[] {
    const order = items.map((_, index) => index).sort((a, b) => savedAtOf(items[b]) - savedAtOf(items[a]) || a - b);
    const sets = items.map((item) => ideaWords(titleOf(item)));
    const wordGroups = wordMemberGroups(sets, order);
    const hasMeanings = !!meanings && meanings.length === items.length;
    if (!hasMeanings) return toIdeaGroups(items, sets, order, wordGroups);
    const meaningGroups = new Map<string, number[]>();
    for (const index of order) {
        const negations = [...sets[index]].filter((word) => word.startsWith('!')).sort().join(' ');
        const key = `${meanings[index]}|${negations}`;
        const list = meaningGroups.get(key);
        if (list) list.push(index);
        else meaningGroups.set(key, [index]);
    }
    // Union-find over both partitions: items linked by either join one idea.
    const parent = items.map((_, index) => index);
    const find = (x: number): number => {
        while (parent[x] !== x) {
            parent[x] = parent[parent[x]];
            x = parent[x];
        }
        return x;
    };
    const link = (members: number[]) => {
        for (let k = 1; k < members.length; k++) parent[find(members[k])] = find(members[0]);
    };
    for (const members of wordGroups) link(members);
    for (const members of meaningGroups.values()) link(members);
    const byRoot = new Map<number, number[]>();
    for (const index of order) {
        const root = find(index);
        const list = byRoot.get(root);
        if (list) list.push(index);
        else byRoot.set(root, [index]);
    }
    return toIdeaGroups(items, sets, order, [...byRoot.values()]);
}

/** The groups of items whose words make them one idea, newest first. */
function wordMemberGroups(sets: Set<string>[], order: number[]): number[][] {
    const n = sets.length;
    // Document frequency and smoothed inverse document frequency across the list.
    const df = new Map<string, number>();
    for (const words of sets) {
        for (const word of words) if (!word.startsWith('!')) df.set(word, (df.get(word) ?? 0) + 1);
    }
    const weight = new Map<string, number>();
    for (const [word, count] of df) weight.set(word, Math.log((1 + n) / (1 + count)) + 1);
    // On a long list, a word in a fifth of the titles or more is too common to
    // find candidates by; it still counts toward similarity.
    const commonCutoff = n >= 200 ? n * 0.2 : Infinity;
    const prepared = sets.map((words) => prepare(words, weight));

    const headsByWord = new Map<string, number[]>();
    const heads: { index: number; members: number[] }[] = [];
    for (const index of order) {
        const item = prepared[index];
        const candidates = new Set<number>();
        for (const word of item.words) {
            if ((df.get(word) ?? 0) > commonCutoff) continue;
            for (const headNumber of headsByWord.get(word) ?? []) candidates.add(headNumber);
        }
        let joined: number | null = null;
        for (const headNumber of [...candidates].sort((x, y) => x - y)) {
            if (sameIdea(prepared[heads[headNumber].index], item, weight)) {
                joined = headNumber;
                break;
            }
        }
        if (joined !== null) {
            heads[joined].members.push(index);
            continue;
        }
        const headNumber = heads.length;
        heads.push({ index, members: [index] });
        for (const word of item.words) {
            if ((df.get(word) ?? 0) > commonCutoff) continue;
            const list = headsByWord.get(word);
            if (list) list.push(headNumber);
            else headsByWord.set(word, [headNumber]);
        }
    }
    return heads.map((head) => head.members);
}

/** Turns member groups into idea groups: copies newest first, groups in first-seen order. */
function toIdeaGroups<T>(items: readonly T[], sets: Set<string>[], order: number[], memberGroups: number[][]): IdeaGroup<T>[] {
    const position = new Map<number, number>();
    order.forEach((index, at) => position.set(index, at));
    return memberGroups
        .map((members) => {
            const sorted = [...members].sort((a, b) => (position.get(a) ?? 0) - (position.get(b) ?? 0));
            return {
                firstSeen: Math.min(...sorted),
                key: [...sets[sorted[0]]].filter((word) => !word.startsWith('!')).sort().join(' '),
                copies: sorted.map((index) => items[index]),
            };
        })
        .sort((a, b) => a.firstSeen - b.firstSeen)
        .map(({ key, copies }) => ({ key, copies }));
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
