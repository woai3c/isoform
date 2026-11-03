import i18n from "@/i18n";
import type { AgentEvent } from "../api/types";
import type { StreamItem } from "@/types";

import type { PermissionOption } from "../api/types";
export type { PermissionOption };

/** Pending permission state — surfaced as a card below the list. */
export interface PermissionState {
    eventId?: string;
    toolName?: string;
    toolInput?: unknown;
    toolUseId?: string;
    options: PermissionOption[];
    sessionId?: string;
}

/**
 * StreamState is the spotlight's chat-render input. Uses the same
 * StreamItem shape the main UI's chat surface consumes (`@/types`),
 * so the imported components (GroupedStreamList, ProcessingBlock,
 * StreamCard) can render directly without any adaptation layer.
 *
 * Permission requests live outside `items` because the main UI's
 * StreamItem doesn't have a permission kind — the main UI handles
 * those via Session-level state, which we don't have here. We render
 * a separate PermissionQuestionCard in ConversationPanel when
 * `permission` is non-null.
 */
export interface StreamState {
    items: StreamItem[];
    /** True from run_created until close. Drives the typing dots. */
    streaming: boolean;
    /** Last seen run id; needed for the Cancel button. */
    runId: string;
    /** Last seen Claude session id; persisted for resume. */
    sessionId: string;
    /** Latest auth/agent error surfaced via the error event types. */
    error: string;
    /** Pending permission_request payload, if any. */
    permission: PermissionState | null;
    /** ms timestamp when streaming flipped on; drives elapsed timer. */
    streamingStartedAt: number;
}

export const initialStreamState: StreamState = {
    items: [],
    streaming: false,
    runId: "",
    sessionId: "",
    error: "",
    permission: null,
    streamingStartedAt: 0,
};

let counter = 0;
function nextId(prefix: string): string {
    counter += 1;
    return `${prefix}-${Date.now().toString(36)}-${counter}`;
}

/** Append item, replacing the trailing one when ids match. */
function appendOrReplace(items: StreamItem[], item: StreamItem): StreamItem[] {
    const idx = items.findIndex((it) => it.id === item.id);
    if (idx === -1) return [...items, item];
    const next = items.slice();
    next[idx] = item;
    return next;
}

/**
 * processAgentEvent folds a single AgentEvent into StreamState. Pure
 * — does not write to React, does not touch the DB. Mirrors the
 * case-by-case logic in main UI's useAgentStreaming so the items
 * collection ends up in the shape the imported renderer expects.
 *
 * Behavior intent:
 *  - assistant / thinking deltas extend the trailing assistant /
 *    thinking item when its runId matches; otherwise start a new one.
 *  - tool_use creates a new tool item; tool_result attaches as a
 *    `result` kind item paired via toolUseId.
 *  - permission_request stashes options on `permission` (rendered
 *    below the list as a separate card).
 *  - compact_* append a single boundary marker.
 *  - run_created marks streaming on; done/close/error clear it.
 */
export function processAgentEvent(state: StreamState, evt: AgentEvent): StreamState {
    switch (evt.type) {
        case "run_created":
            return {
                ...state,
                streaming: true,
                streamingStartedAt: state.streamingStartedAt || Date.now(),
                runId: evt.run_id ?? state.runId,
                sessionId: evt.session_id ?? state.sessionId,
                error: "",
            };

        case "assistant": {
            const content = evt.content ?? "";
            if (!content) return state;
            const last = state.items[state.items.length - 1];
            if (
                last &&
                last.kind === "assistant" &&
                last.runId === (evt.run_id ?? state.runId)
            ) {
                const merged: StreamItem = {
                    ...last,
                    content: (last.content ?? "") + content,
                };
                return { ...state, items: appendOrReplace(state.items, merged) };
            }
            return {
                ...state,
                items: [
                    ...state.items,
                    {
                        id: nextId("assistant"),
                        kind: "assistant",
                        content,
                        runId: evt.run_id ?? state.runId,
                        sessionId: evt.session_id ?? state.sessionId,
                        sortOrder: state.items.length,
                    },
                ],
            };
        }

        case "thinking": {
            const content = evt.content ?? "";
            if (!content) return state;
            const last = state.items[state.items.length - 1];
            if (
                last &&
                last.kind === "thinking" &&
                last.runId === (evt.run_id ?? state.runId)
            ) {
                const merged: StreamItem = {
                    ...last,
                    content: (last.content ?? "") + content,
                };
                return { ...state, items: appendOrReplace(state.items, merged) };
            }
            return {
                ...state,
                items: [
                    ...state.items,
                    {
                        id: nextId("thinking"),
                        kind: "thinking",
                        content,
                        runId: evt.run_id ?? state.runId,
                        sessionId: evt.session_id ?? state.sessionId,
                        sortOrder: state.items.length,
                    },
                ],
            };
        }

        case "tool_use":
            return {
                ...state,
                items: [
                    ...state.items,
                    {
                        id: nextId("tool"),
                        kind: "tool",
                        toolName: evt.tool_name ?? i18n.t("chat:quickChat.tool"),
                        toolInput: evt.tool_input,
                        toolUseId: evt.tool_use_id,
                        runId: evt.run_id ?? state.runId,
                        sessionId: evt.session_id ?? state.sessionId,
                        sortOrder: state.items.length,
                    },
                ],
            };

        case "tool_result":
            return {
                ...state,
                permission: evt.tool_use_id && evt.tool_use_id === state.permission?.toolUseId ? null : state.permission,
                items: [
                    ...state.items,
                    {
                        id: nextId("result"),
                        kind: "result",
                        content: evt.content ?? i18n.t("chat:quickChat.resultReceived"),
                        toolUseId: evt.tool_use_id,
                        toolName: evt.tool_name,
                        runId: evt.run_id ?? state.runId,
                        sessionId: evt.session_id ?? state.sessionId,
                        sortOrder: state.items.length,
                    },
                ],
            };

        case "permission_request":
            return {
                ...state,
                permission: {
                    eventId: evt.event_id,
                    toolName: evt.tool_name,
                    toolInput: evt.tool_input,
                    toolUseId: evt.tool_use_id,
                    options: evt.permission_options ?? [],
                    sessionId: evt.session_id ?? state.sessionId,
                },
            };

        case "compact_start":
        case "compact_end":
        case "compact_error":
            return {
                ...state,
                items: [
                    ...state.items,
                    {
                        id: nextId("compact"),
                        kind: "compact_boundary",
                        content:
                            evt.type === "compact_error"
                                ? (evt.error
                                    ? i18n.t("chat:quickChat.compactionFailedDetail", { error: evt.error })
                                    : i18n.t("chat:quickChat.compactionFailed"))
                                : evt.type === "compact_start"
                                  ? i18n.t("chat:quickChat.compacting")
                                  : i18n.t("chat:quickChat.compacted"),
                        runId: evt.run_id ?? state.runId,
                        sortOrder: state.items.length,
                    },
                ],
            };

        case "auth_error":
        case "error":
            return {
                ...state,
                streaming: false,
                permission: null,
                error: evt.error ?? i18n.t("chat:quickChat.streamError"),
                items: [
                    ...state.items,
                    {
                        id: nextId("error"),
                        kind: "error",
                        content: evt.error ?? i18n.t("chat:quickChat.streamError"),
                        runId: evt.run_id ?? state.runId,
                        sortOrder: state.items.length,
                    },
                ],
            };

        case "done":
        case "close":
            return { ...state, streaming: false, permission: null };

        default:
            return state;
    }
}

/**
 * editAndTruncate replaces the content of the user item at `itemId`
 * with `newContent` AND drops everything after it (assistant turn,
 * tool calls, results) so the conversation is ready to be replayed
 * from that point. Returns the same state when itemId isn't found.
 *
 * Mirrors what main UI does on edit/retry: trim items and re-submit;
 * the next agent turn streams fresh into the trimmed list.
 */
export function editAndTruncate(
    state: StreamState,
    itemId: string,
    newContent: string,
): StreamState {
    const idx = state.items.findIndex((it) => it.id === itemId);
    if (idx === -1) return state;
    const target = state.items[idx];
    if (target.kind !== "user") return state;
    const updated: StreamItem = { ...target, content: newContent };
    return {
        ...state,
        items: [...state.items.slice(0, idx), updated],
        permission: null,
        streaming: false,
    };
}

/** Helper for the initial user bubble: append a synthetic user item.
 *  The sortOrder is essential — main UI's GroupedStreamList only enables
 *  edit/retry buttons when `userItem.sortOrder !== undefined`. */
export function appendUserMessage(state: StreamState, prompt: string, id?: string): StreamState {
    return {
        ...state,
        streaming: true,
        streamingStartedAt: Date.now(),
        items: [
            ...state.items,
            {
                id: id ?? nextId("user"),
                kind: "user",
                content: prompt,
                sortOrder: state.items.length,
            },
        ],
    };
}

/** Clear pending permission state (e.g. after the user picks an option). */
export function clearPermission(state: StreamState, eventId?: string): StreamState {
    if (eventId && state.permission?.eventId !== eventId) return state;
    return { ...state, permission: null };
}
