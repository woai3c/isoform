import type { Project } from "@/types";
import { extractLatestTurnDeliverables } from "@/lib/agent-deliverables";

export function completedChatDeliverables(projects: Project[], previous: Map<string, boolean>) {
    const results: { sessionId: string; title: string; summary: string; files: { path: string; description: string }[] }[] = [];
    for (const project of projects) for (const workspace of project.workspaces) for (const session of workspace.sessions) {
        const wasStreaming = previous.get(session.localId) ?? false;
        previous.set(session.localId, !!session.streaming);
        if (!wasStreaming || session.streaming) continue;
        const parsed = extractLatestTurnDeliverables(session.items ?? []);
        if (!parsed) continue;
        results.push({
            sessionId: session.localId,
            title: session.label?.trim() || "Chat",
            summary: parsed.content.split("\n").map((line) => line.trim()).find(Boolean)?.slice(0, 200) ?? "",
            files: parsed.deliverables,
        });
    }
    return results;
}
