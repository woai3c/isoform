import type { StreamItem } from "@/types";

const AGENT_DELIVERABLES_RE =
    /<deliverables>\s*([\s\S]*?)\s*<\/deliverables>/gi;
const AGENT_DELIVERY_TARGET_RE =
    /<delivery_target>[\s\S]*?<\/delivery_target>/gi;
const MAX_AGENT_DELIVERABLES = 10;
// Mirrors Go's maxDeliverableDescriptionRunes (counted in code points): the
// description renders as a one-line card subtitle, so anything longer is
// agent rambling. The backend re-applies the same cap when persisting.
const MAX_DELIVERABLE_DESCRIPTION_CHARS = 200;

// Mirrors stripInlineMarkdown in wails-gui/deliverables_hub.go (the backend
// re-applies it defensively when persisting and when reading stored rows) —
// keep the two in sync. The parity tests in agent-deliverables.test.ts and
// deliverables_test.go share the same input/expectation pair.
function stripInlineMarkdown(line: string): string {
    line = line
        .replace(/!\[([^\]]*)\]\([^)]*\)/g, "$1")
        .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
        .replace(/\*\*([^*]+)\*\*/g, "$1")
        .replace(/__([^_]+)__/g, "$1")
        .replace(/\*([^*]+)\*/g, "$1")
        .replace(/`([^`]+)`/g, "$1")
        .replace(/~~([^~]+)~~/g, "$1");
    for (;;) {
        const stripped = line.replace(
            /^(?:>\s?|[-*+]\s+|\d{1,3}[.)]\s+)/,
            "",
        );
        if (stripped === line) break;
        line = stripped;
    }
    return line.trim();
}

/** One declared deliverable file. Each <deliverables> line is either a bare
 *  path (legacy) or "path | one-sentence description"; the split happens on
 *  the FIRST pipe only so descriptions containing "|" stay intact. */
export interface AgentDeliverable {
    path: string;
    description: string; // "" when the agent declared no description
}

function parseDeliverableLine(line: string): AgentDeliverable | null {
    const trimmed = line.trim();
    if (!trimmed) return null;
    const sep = trimmed.indexOf("|");
    if (sep === -1) return { path: trimmed, description: "" };
    const path = trimmed.slice(0, sep).trim();
    if (!path) return null;
    let description = stripInlineMarkdown(trimmed.slice(sep + 1).trim());
    const chars = Array.from(description);
    if (chars.length > MAX_DELIVERABLE_DESCRIPTION_CHARS) {
        description =
            chars.slice(0, MAX_DELIVERABLE_DESCRIPTION_CHARS).join("") + "…";
    }
    return { path, description };
}

export function extractAgentBlocks(content: string): {
    content: string;
    deliverables: AgentDeliverable[];
} {
    const deliverables: AgentDeliverable[] = [];
    let cleaned = content.replace(
        AGENT_DELIVERABLES_RE,
        (_full, inner: string) => {
            for (const line of inner.split("\n")) {
                const file = parseDeliverableLine(line);
                if (file && deliverables.length < MAX_AGENT_DELIVERABLES) {
                    deliverables.push(file);
                }
            }
            return "";
        },
    );
    cleaned = cleaned.replace(AGENT_DELIVERY_TARGET_RE, "").trim();
    return { content: cleaned, deliverables };
}

export function extractLatestTurnDeliverables(
    items: StreamItem[],
): { content: string; deliverables: AgentDeliverable[] } | null {
    for (let index = items.length - 1; index >= 0; index--) {
        const item = items[index];
        if (item.kind === "user") break;
        if (item.kind !== "assistant" && item.kind !== "result") continue;

        const parsed = extractAgentBlocks(item.content ?? "");
        if (parsed.deliverables.length > 0) return parsed;
    }
    return null;
}
