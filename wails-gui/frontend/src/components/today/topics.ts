import briefCopyPatterns from "@/lib/brief-copy-patterns.json";

const privacyMask = new RegExp(briefCopyPatterns.privacyMask, "i");

/** A topic on Today (RC 1): a piece of work or a subject the user follows,
 *  from the topic registry (A1, `GetTodayTopics`). */
export interface TodayTopic {
    id: string;
    name: string;
    kind: "work" | "followed";
    /** Why the topic shows: a work topic's state, or a followed topic's
     *  new stories. */
    status?: "waiting" | "waitingOnThem" | "today" | "moving" | "news";
    /** New stories on a followed topic. */
    newCount?: number;
    latestFind?: TopicMoment;
    /** What happened last, and what comes next; at is RFC 3339. */
    latest?: TopicMoment;
    next?: TopicMoment;
    /** Up to three names, and how many people in all. */
    people?: { names: string[]; count: number };
    /** When the user last spent time on a followed topic, for its why line.
     *  The registry sends followed topics only with a reason (item 61);
     *  Closed Eyes withholds the time. */
    lastSeenAt?: string;
    pinned: boolean;
    /** Local days (YYYY-MM-DD) the person worked on it, newest first;
     *  absent from a registry snapshot made before they were recorded. */
    activityDays?: string[];
}

export interface TopicMoment {
    text: string;
    at?: string;
}

/** The topic the panel opens on: the first one waiting on the user, else the
 *  first one listed. */
export function firstTopic(topics: readonly TodayTopic[]): TodayTopic | null {
    return topics.find((topic) => topic.status === "waiting") ?? topics[0] ?? null;
}

/** Pinning a topic tells every reader of topics on this window. */
export const SIDEBAR_TOPICS_CHANGED = "isoform:sidebar-topics-changed";

/** The pinned panel's narrowest and widest widths (Claude Design,
 *  2026-10-22): 360 px where Today is just wide enough, 420 px on wider
 *  windows. */
export const TOPIC_PANEL_MIN_WIDTH = 360;
export const TOPIC_PANEL_WIDTH = 420;

/** The page column beside the pinned panel never goes below this. */
export const TODAY_PAGE_MIN_WIDTH = 560;

/** Today keeps the panel beside the page while the space next to the
 *  sidebar (Today's own width, not the window's) is at least this. Narrower,
 *  the panel is a Sheet from the right edge, opened by the Chat button, a
 *  topic or a chip. A folded sidebar counts: the panel can pin on a narrower
 *  window. */
export const TOPIC_PANEL_PINNED_MIN_WIDTH = TODAY_PAGE_MIN_WIDTH + TOPIC_PANEL_MIN_WIDTH;

/** The pinned panel's width for the space next to the sidebar: three
 *  eighths of it, never under 360 px, never over 420 px, and never leaving
 *  the page under 560 px. At 920 px that is 360 (page 560); at 1010 px,
 *  about 380 (page 630); from 1120 px up, 420. */
export function topicPanelWidth(space: number): number {
    const share = Math.round(space * 3 / 8);
    return Math.max(TOPIC_PANEL_MIN_WIDTH, Math.min(TOPIC_PANEL_WIDTH, share, space - TODAY_PAGE_MIN_WIDTH));
}

/** One row of the main chat filtered to a topic (ListMainChatTopicItems,
 *  docs/rc1-chat-topics-api.md). */
export interface TopicChatItem {
    /** "message:<stream id>" or "event:<Activity id>". */
    id: string;
    kind: "message" | "event";
    /** Unix milliseconds. */
    at: number;
    /** An event's role code; "user" is the only one today. */
    actor?: string;
    /** An event's current, redacted Activity title. */
    text?: string;
    message?: {
        id: string;
        kind: string;
        content?: string;
        sortOrder: number;
    };
}

export interface TopicChatPage {
    sessionId: string;
    topicId: string;
    items: TopicChatItem[];
    hasMore: boolean;
    nextCursor: string;
}

/** Withhold redacted titles (including truncated placeholders) and the chat
 * itself. The registry does not provide an app for a trustworthy fallback. */
export function isDisplayTopic<T extends { name?: string }>(topic: T): topic is T & { name: string } {
    const name = topic.name?.trim();
    if (!name) return false;
    return !privacyMask.test(name) && !/\[redact/i.test(name) && !/^(?:(?:isoform|yansu)\s+)?(?:main\s+)?chat$/i.test(name);
}

/** Waiting topics lead, then the latest observed work, with stable ties. */
export function orderedDisplayTopics(topics: readonly TodayTopic[]): TodayTopic[] {
    const recent = (topic: TodayTopic) => Date.parse(topic.latest?.at ?? topic.lastSeenAt ?? '') || 0;
    return topics.filter(isDisplayTopic).sort((a, b) => Number(b.status === 'waiting') - Number(a.status === 'waiting') || recent(b) - recent(a));
}

/** The most topic cards Today shows, and Chat's pills (today-review v1.1.4 #8). */
export const TOPIC_CARDS_MAX = 6;
/** A card's one line, including its time, stays within this many characters. */
export const TOPIC_CARD_LINE_MAX = 50;

const DOMAIN_NAME = /^(?:https?:\/\/)?(?:[a-z0-9-]+\.)+[a-z]{2,}(?:\/\S*)?$/i;
const WINDOW_TITLE = /\s[-–—|·•:]\s|\s[|·•]|[|·•]\s/;

/** A topic's name is the thing, as a person would say it, in one to four
 *  words: never a domain, a window title or a log line. A name Isoform
 *  couldn't write ("msg") isn't a topic. */
export function isTopicCardName(name: string): boolean {
    const text = name.trim();
    if (!text || DOMAIN_NAME.test(text) || WINDOW_TITLE.test(text) || /[\n\r]/.test(text)) return false;
    if (/[\u3400-\u9fff]/.test(text)) return [...text.replace(/\s+/g, "")].length <= 10;
    const words = text.split(/\s+/);
    if (words.length > 4) return false;
    // One short lowercase token is a code, not a name.
    return !(words.length === 1 && /^[a-z0-9_]{1,3}$/.test(text));
}

function localDay(date: Date): string {
    return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`;
}

/** Days with activity in the seven days up to now. */
export function topicActiveDays(topic: Pick<TodayTopic, "activityDays">, now: Date): number {
    const days = new Set<string>();
    for (let back = 0; back < 7; back++) {
        const day = new Date(now.getFullYear(), now.getMonth(), now.getDate() - back);
        days.add(localDay(day));
    }
    return new Set((topic.activityDays ?? []).filter((day) => days.has(day))).size;
}

/** Which topics get a card, and a pill in Chat: pinned ones first, then
 *  work on at least two different days in the last seven (a followed topic
 *  counts with something new), at most six, each with a real name. */
export function topicCards<T extends TodayTopic>(topics: readonly T[], now: Date): T[] {
    const named = topics.filter((topic) => isDisplayTopic(topic) && isTopicCardName(topic.name));
    const shown = (topic: T) => topic.activityDays === undefined
        || topicActiveDays(topic, now) >= 2
        || topic.kind === "followed" && Boolean(topic.latestFind?.text.trim());
    return [...named.filter((topic) => topic.pinned), ...named.filter((topic) => !topic.pinned && shown(topic))].slice(0, TOPIC_CARDS_MAX);
}
