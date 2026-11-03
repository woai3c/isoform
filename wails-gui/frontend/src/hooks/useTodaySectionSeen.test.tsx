import { render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useTodaySectionSeen, type TodaySection } from "./useTodaySectionSeen";

const record = vi.hoisted(() => vi.fn(async () => undefined));
vi.mock("@/lib/wails-shim-app", () => ({ RecordTodaySectionSeen: record }));

function Harness({ active, rows, section = "before_you_talk", shown = true, ready = true }: { active: boolean; rows: number; section?: TodaySection; shown?: boolean; ready?: boolean }) {
    const ref = useTodaySectionSeen(active, section, rows, ready);
    return shown ? <section ref={ref} /> : null;
}

afterEach(() => record.mockClear());
describe("useTodaySectionSeen", () => {
    it("records mounted sections once per visit, including zero rows", () => {
        const { rerender } = render(<Harness active rows={0} />);
        expect(record).toHaveBeenCalledWith("before_you_talk", 0);
        expect(record).toHaveBeenCalledTimes(1);
        rerender(<Harness active rows={2} />);
        expect(record).toHaveBeenCalledTimes(1);
        rerender(<Harness active={false} rows={2} />);
        rerender(<Harness active rows={2} />);
        expect(record).toHaveBeenLastCalledWith("before_you_talk", 2);
    });
    it.each(["brief", "before_you_talk", "needs_you", "done_for_you", "your_topics", "your_week", "getting_to_know_you", "your_projects"] as TodaySection[])("records %s with its row count only", (section) => {
        render(<Harness active rows={3} section={section} />);
        expect(record).toHaveBeenCalledWith(section, 3);
        expect(record).toHaveBeenCalledTimes(1);
    });
    it("waits for data and does not record again on refresh", () => {
        const view = render(<Harness active rows={0} ready={false} />);
        expect(record).not.toHaveBeenCalled();
        view.rerender(<Harness active rows={2} />);
        expect(record).toHaveBeenCalledWith("before_you_talk", 2);
        view.rerender(<Harness active rows={2} ready={false} />);
        view.rerender(<Harness active rows={3} />);
        expect(record).toHaveBeenCalledTimes(1);
    });
    it("does not record a section that is not shown", () => {
        render(<Harness active rows={0} shown={false} />);
        expect(record).not.toHaveBeenCalled();
    });
});
