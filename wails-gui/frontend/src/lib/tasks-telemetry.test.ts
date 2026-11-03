import { renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { measureTasks, useTasksSectionSeen } from "./tasks-telemetry";

const record = vi.hoisted(() => ({ action: vi.fn(async () => undefined), seen: vi.fn(async () => undefined) }));
vi.mock("@/lib/wails-shim-app", () => ({ RecordTasksAction: record.action, RecordTasksSectionSeen: record.seen }));

describe("Tasks measurement (K8)", () => {
    it("records one content-free tasks.action for a click", () => {
        record.action.mockClear();
        const privateTitle = "Private task title";
        expect(measureTasks("T26", "open", "opened", () => privateTitle)).toBe(privateTitle);
        expect(record.action).toHaveBeenCalledTimes(1);
        expect(record.action).toHaveBeenCalledWith("T26", "open", "opened", expect.any(Number));
        expect(JSON.stringify(record.action.mock.calls)).not.toContain(privateTitle);
    });
    it("records a failure once, without its words", async () => {
        record.action.mockClear();
        await expect(measureTasks("C08", "do", "done", async () => { throw new Error("private words"); })).rejects.toThrow();
        expect(record.action).toHaveBeenCalledTimes(1);
        expect(record.action).toHaveBeenCalledWith("C08", "do", "failed", expect.any(Number));
        expect(JSON.stringify(record.action.mock.calls)).not.toContain("private words");
    });
    it("never lets measurement break the action", () => {
        record.action.mockImplementationOnce(() => { throw new Error("binding gone"); });
        expect(measureTasks("T20", "do", "done", () => 3)).toBe(3);
    });
    it("records a section once, with its row count only", () => {
        record.seen.mockClear();
        const hook = renderHook(({ rows, shown }) => useTasksSectionSeen("scheduled", rows, shown), { initialProps: { rows: 0, shown: false } });
        expect(record.seen).not.toHaveBeenCalled();
        hook.rerender({ rows: 4, shown: true });
        hook.rerender({ rows: 5, shown: true });
        expect(record.seen).toHaveBeenCalledTimes(1);
        expect(record.seen).toHaveBeenCalledWith("scheduled", 4);
    });
});
