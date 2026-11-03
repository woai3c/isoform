import { describe, expect, it } from "vitest";
import type { StreamItem } from "@/types";
import { extractLatestTurnDeliverables } from "./agent-deliverables";

function item(kind: StreamItem["kind"], content: string): StreamItem {
    return { id: `${kind}-${content}`, kind, content };
}

describe("extractLatestTurnDeliverables", () => {
    it("does not reuse deliverables from an earlier turn", () => {
        const result = extractLatestTurnDeliverables([
            item("user", "first request"),
            item(
                "assistant",
                "done\n<deliverables>/tmp/old.pdf</deliverables>",
            ),
            item("user", "follow-up without a file"),
            item("assistant", "text-only response"),
        ]);

        expect(result).toBeNull();
    });

    it("returns deliverables from the latest turn", () => {
        const result = extractLatestTurnDeliverables([
            item("user", "request"),
            item(
                "assistant",
                "Created the report.\n<deliverables>\n/tmp/report.pdf\n</deliverables>",
            ),
        ]);

        expect(result).toEqual({
            content: "Created the report.",
            deliverables: [{ path: "/tmp/report.pdf", description: "" }],
        });
    });

    it("parses per-file descriptions after the first pipe", () => {
        const result = extractLatestTurnDeliverables([
            item("user", "request"),
            item(
                "assistant",
                "Done.\n<deliverables>\n/tmp/report.pdf | Quarterly report | final version\n/tmp/data.csv\n</deliverables>",
            ),
        ]);

        expect(result).toEqual({
            content: "Done.",
            deliverables: [
                {
                    path: "/tmp/report.pdf",
                    description: "Quarterly report | final version",
                },
                { path: "/tmp/data.csv", description: "" },
            ],
        });
    });

    it("caps overlong descriptions like the backend does", () => {
        const long = "x".repeat(250);
        const result = extractLatestTurnDeliverables([
            item("user", "request"),
            item(
                "assistant",
                `Done.\n<deliverables>\n/tmp/a.md | ${long}\n</deliverables>`,
            ),
        ]);

        expect(result?.deliverables).toEqual([
            { path: "/tmp/a.md", description: `${"x".repeat(200)}…` },
        ]);
    });

    it("strips inline markdown from descriptions like the backend does", () => {
        const result = extractLatestTurnDeliverables([
            item("user", "request"),
            item(
                "assistant",
                "Done.\n<deliverables>\n/tmp/a.md | **Final** report with [sources](https://example.com)\n</deliverables>",
            ),
        ]);

        expect(result?.deliverables).toEqual([
            { path: "/tmp/a.md", description: "Final report with sources" },
        ]);
    });

    it("drops separator-only lines without a path", () => {
        const result = extractLatestTurnDeliverables([
            item("user", "request"),
            item(
                "assistant",
                "Done.\n<deliverables>\n| dangling description\n/tmp/a.md\n</deliverables>",
            ),
        ]);

        expect(result?.deliverables).toEqual([
            { path: "/tmp/a.md", description: "" },
        ]);
    });
});
