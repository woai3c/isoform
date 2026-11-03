import { describe, expect, it, vi } from "vitest";
import { measureToday } from "./today-telemetry";

const record = vi.hoisted(() => vi.fn(async () => undefined));
vi.mock("@/lib/wails-shim-app", () => ({ RecordTodayAction: record }));

describe("Today action measurement", () => {
    it("records one content-free event for a click", () => {
        record.mockClear();
        const privateTitle = "Private brief title";
        expect(measureToday("T50", "open", "opened", () => privateTitle)).toBe(privateTitle);
        expect(record).toHaveBeenCalledTimes(1);
        expect(record).toHaveBeenCalledWith("today", "T50", "open", "opened", expect.any(Number));
        expect(JSON.stringify(record.mock.calls)).not.toContain(privateTitle);
    });
    it("records a failed result without copying its content", async () => {
        record.mockClear();
        await measureToday("T70", "do", "done", async () => false);
        expect(record).toHaveBeenCalledTimes(1);
        expect(record).toHaveBeenCalledWith("today", "T70", "do", "failed", expect.any(Number));
    });
    it("records failure once when an action rejects", async () => {
        record.mockClear();
        await expect(measureToday("T40", "open", "opened", async () => { throw new Error("private words"); })).rejects.toThrow();
        expect(record).toHaveBeenCalledTimes(1);
        expect(record).toHaveBeenCalledWith("today", "T40", "open", "failed", expect.any(Number));
        expect(JSON.stringify(record.mock.calls)).not.toContain("private words");
    });
});
