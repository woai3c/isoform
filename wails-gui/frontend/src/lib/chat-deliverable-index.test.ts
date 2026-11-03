import { expect, it } from "vitest";
import type { Project } from "@/types";
import { completedChatDeliverables } from "./chat-deliverable-index";

it("records a completed background session even when another session is active", () => {
    const background = { localId: "background", label: "Report", streaming: true, items: [
        { id: "u", kind: "user", content: "Write" },
        { id: "a", kind: "assistant", content: "Done.\n<deliverables>\n/tmp/report.md | Report\n</deliverables>" },
    ] };
    const projects = [{ activeSessionId: "active", workspaces: [{ sessions: [background, { localId: "active", streaming: false, items: [] }] }] }] as unknown as Project[];
    const previous = new Map<string, boolean>();
    expect(completedChatDeliverables(projects, previous)).toEqual([]);
    background.streaming = false;
    expect(completedChatDeliverables(projects, previous)).toEqual([{ sessionId: "background", title: "Report", summary: "Done.", files: [{ path: "/tmp/report.md", description: "Report" }] }]);
    expect(completedChatDeliverables(projects, previous)).toEqual([]);
});
