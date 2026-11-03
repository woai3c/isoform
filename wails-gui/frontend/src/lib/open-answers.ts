export type OpenAnswerSettles = "result" | "reply" | "status";
export type OpenAnswerDelivery = "around" | "settled_only" | "brief";
export type OpenAnswerState = "open" | "settled" | "saw_it" | "gave_up" | "cancelled" | "ask";

export interface OpenAnswer {
    id: string;
    sessionId: string;
    messageId: string;
    question: string;
    topic?: string;
    kind: string;
    settles: OpenAnswerSettles;
    source?: string;
    windowStart?: string;
    windowEnd?: string;
    watchedEvent?: { who?: string; label: string };
    delivery: OpenAnswerDelivery;
    expiresAt: string;
    posts: number;
    state: OpenAnswerState;
    sawAt?: string;
    createdAt: string;
    learned?: boolean;
}

const settles = new Set<OpenAnswerSettles>(["result", "reply", "status"]);
const deliveries = new Set<OpenAnswerDelivery>(["around", "settled_only", "brief"]);
const states = new Set<OpenAnswerState>(["open", "settled", "saw_it", "gave_up", "cancelled", "ask"]);

function isString(value: unknown): value is string {
    return typeof value === "string";
}

function validDate(value: unknown): value is string {
    return isString(value) &&
        /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(value) &&
        !Number.isNaN(new Date(value).getTime());
}

/** Converts the flat Wails value to a trusted OpenAnswer record, dropping malformed payloads. */
export function parseOpenAnswer(value: unknown): OpenAnswer | undefined {
    if (typeof value === "string") {
        try {
            value = JSON.parse(value);
        } catch {
            return undefined;
        }
    }
    if (!value || typeof value !== "object") return undefined;
    const record = value as Record<string, unknown>;
    if (
        !isString(record.id) || !record.id ||
        !isString(record.sessionId) || !record.sessionId ||
        !isString(record.messageId) || !record.messageId ||
        !isString(record.question) || !record.question.trim() ||
        !isString(record.kind) || !record.kind ||
        !settles.has(record.settles as OpenAnswerSettles) ||
        !deliveries.has(record.delivery as OpenAnswerDelivery) ||
        !validDate(record.expiresAt) ||
        !Number.isInteger(record.posts) || (record.posts as number) < 0 ||
        !states.has(record.state as OpenAnswerState) ||
        !validDate(record.createdAt)
    ) return undefined;
    if (record.windowStart !== undefined && !validDate(record.windowStart)) return undefined;
    if (record.windowEnd !== undefined && !validDate(record.windowEnd)) return undefined;
    if (record.sawAt !== undefined && !validDate(record.sawAt)) return undefined;
    if (record.learned !== undefined && typeof record.learned !== "boolean") return undefined;
    if (record.topic !== undefined && !isString(record.topic)) return undefined;
    if (record.source !== undefined && !isString(record.source)) return undefined;
    let watchedEvent: OpenAnswer["watchedEvent"];
    if (record.watchedEvent !== undefined) {
        if (!record.watchedEvent || typeof record.watchedEvent !== "object") return undefined;
        const event = record.watchedEvent as Record<string, unknown>;
        if (!isString(event.label) || !event.label) return undefined;
        if (event.who !== undefined && !isString(event.who)) return undefined;
        watchedEvent = {
            label: event.label,
            ...(event.who ? { who: event.who } : {}),
        };
    }
    return {
        id: record.id,
        sessionId: record.sessionId,
        messageId: record.messageId,
        question: record.question,
        ...(record.topic !== undefined ? { topic: record.topic } : {}),
        kind: record.kind,
        settles: record.settles as OpenAnswerSettles,
        ...(record.source !== undefined ? { source: record.source } : {}),
        ...(record.windowStart !== undefined ? { windowStart: record.windowStart } : {}),
        ...(record.windowEnd !== undefined ? { windowEnd: record.windowEnd } : {}),
        ...(watchedEvent ? { watchedEvent } : {}),
        delivery: record.delivery as OpenAnswerDelivery,
        expiresAt: record.expiresAt,
        posts: record.posts as number,
        state: record.state as OpenAnswerState,
        ...(record.sawAt !== undefined ? { sawAt: record.sawAt } : {}),
        createdAt: record.createdAt,
        ...(record.learned !== undefined ? { learned: record.learned } : {}),
    };
}
