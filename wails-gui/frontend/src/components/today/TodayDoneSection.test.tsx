import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { createInstance } from "i18next";
import { I18nextProvider } from "react-i18next";
import { expect, it, vi } from "vitest";
import en from "@/i18n/locales/en/today.json";
import zh from "@/i18n/locales/zh-CN/today.json";
import type { TodayDone } from "@/lib/wails-shim-app";
import { TodayDoneSection } from "./TodayDoneSection";
const action = vi.hoisted(() => vi.fn(async () => undefined));
vi.mock("@/lib/wails-shim-app", () => ({ RecordTodaySectionSeen: vi.fn(async () => undefined), RecordTodayAction: action }));

const item = (id: string, kind = "work_finished"): TodayDone => ({ id, kind, sentence: `Finished ${id}`, at: 0, targetKind: "chat_item", targetId: `row-${id}` });
async function show(items: TodayDone[], language = "en") {
    const i18n = createInstance();
    await i18n.init({ lng: language, resources: { en: { today: en }, "zh-CN": { today: zh } }, defaultNS: "today" });
    const open = vi.fn(); const tasks = vi.fn();
    const view = render(<I18nextProvider i18n={i18n}><TodayDoneSection items={items} onOpen={open} onOpenTasks={tasks} /></I18nextProvider>);
    return { ...view, open, tasks, i18n };
}
it("hides when empty", async () => { expect((await show([])).container).toBeEmptyDOMElement(); });
it("shows three finished rows, opens an answer from its row, and reports the remainder", async () => {
    const rows = [item("one", "answer_ready"), item("two"), item("three"), item("four")];
    const { open, tasks } = await show(rows);
    const section = screen.getByRole("region", { name: "Done for you" });
    expect(within(section).getByRole("button", { name: "Finished one" }).closest(".rounded-xl")).toHaveClass("bg-panel", "border");
    expect(within(section).queryByText("Finished four")).toBeNull();
    expect(within(section).getAllByRole("button", { name: "Review" })).toHaveLength(2);
    fireEvent.click(within(section).getByRole("button", { name: "Finished one" }));
    expect(open).toHaveBeenCalledWith(rows[0]);
    fireEvent.click(within(section).getByRole("button", { name: "1 more in Tasks" }));
    expect(tasks).toHaveBeenCalledOnce();
});
it("uses the specified Chinese words", async () => {
    await show([item("one"), item("two"), item("three"), item("four")], "zh-CN");
    expect(screen.getByRole("region", { name: "替你做完的" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "任务里还有 1 项" })).toBeInTheDocument();
});
it("changes a result sentence with the selected language", async () => {
    const row = { ...item("one"), sentenceLocalized: { en: "Routine finished", "zh-CN": "例行工作已完成" } };
    const { i18n } = await show([row]);
    expect(screen.getByText("Routine finished")).toBeInTheDocument();
    await act(async () => { await i18n.changeLanguage("zh-CN"); });
    expect(screen.getByText("例行工作已完成")).toBeInTheDocument();
});


it("measures an opened Done row once without its sentence", async () => {
    action.mockClear();
    await show([item("private-name", "answer_ready")]);
    fireEvent.click(screen.getByRole("button", { name: "Finished private-name" }));
    expect(action).toHaveBeenCalledTimes(1);
    expect(action).toHaveBeenCalledWith("today", "T21", "open", "opened", expect.any(Number));
    expect(JSON.stringify(action.mock.calls)).not.toContain("private-name");
});
