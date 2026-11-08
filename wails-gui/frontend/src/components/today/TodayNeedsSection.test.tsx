import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { createInstance } from "i18next";
import { I18nextProvider } from "react-i18next";
import { afterEach, expect, it, vi } from "vitest";
import en from "@/i18n/locales/en/today.json";
import zh from "@/i18n/locales/zh-CN/today.json";
import type { TodayNeed } from "@/lib/wails-shim-app";
import { TodayNeedsSection } from "./TodayNeedsSection";

const record = vi.hoisted(() => vi.fn(async () => undefined));
const action = vi.hoisted(() => vi.fn(async () => undefined));
vi.mock("@/lib/wails-shim-app", () => ({ RecordTodaySectionSeen: record, RecordTodayAction: action }));
afterEach(() => { vi.unstubAllGlobals(); record.mockClear(); action.mockClear(); });

const item = (id: string, kind = "review"): TodayNeed => ({ id, kind, sentence: `Sentence ${id}`, source: "Mail", createdAt: 0, targetKind: "chat_item", targetId: `row-${id}` });
async function show(items: TodayNeed[], language = "en") {
    const i18n = createInstance();
    await i18n.init({ lng: language, resources: { en: { today: en }, "zh-CN": { today: zh } }, defaultNS: "today" });
    const open = vi.fn(); const tasks = vi.fn();
    const view = render(<I18nextProvider i18n={i18n}><TodayNeedsSection items={items} onOpen={open} onOpenTasks={tasks} /></I18nextProvider>);
    return { ...view, open, tasks, i18n };
}

it("hides an empty section", async () => {
    const { container } = await show([]);
    expect(container).toBeEmptyDOMElement();
});

it("shows the first three in order, the remainder, and opens the exact item", async () => {
    const rows = [item("one"), item("two", "work_blocked"), item("three"), item("four")];
    const { open, tasks } = await show(rows);
    const section = screen.getByRole("region", { name: "Needs you" });
    expect(within(section).queryByText("Sentence four")).not.toBeInTheDocument();
    expect(within(section).getAllByText(/^Sentence/).map((element) => element.textContent)).toEqual(["Sentence one", "Sentence two", "Sentence three"]);
    expect(within(section).getAllByText("From Mail")).toHaveLength(3);
    fireEvent.click(within(section).getByRole("button", { name: "Answer" }));
    expect(open).toHaveBeenCalledWith(rows[1], 1);
    fireEvent.click(within(section).getByRole("button", { name: "1 more in Tasks" }));
    expect(tasks).toHaveBeenCalledOnce();
});

it("uses the specified Chinese section and count words", async () => {
    await show([item("one"), item("two"), item("three"), item("four")], "zh-CN");
    expect(screen.getByRole("region", { name: "需要你" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "任务里还有 1 项" })).toBeInTheDocument();
});

it.each([["en", "Find time"], ["zh-CN", "找个时间"]])("offers Find time for a slipping commitment in %s", async (language, label) => {
    const row = item("commitment", "commitment_slipping");
    const { open } = await show([row], language);
    const button = screen.getByRole("button", { name: label });
    expect(button.closest(".rounded-xl")).toHaveClass("bg-panel", "border");
    fireEvent.click(button);
    expect(open).toHaveBeenCalledWith(row, 0);
});

it("changes a source sentence with the selected language", async () => {
    const row = { ...item("one"), sentenceLocalized: { en: "Ready to review", "zh-CN": "可以查看" } };
    const { i18n } = await show([row]);
    expect(screen.getByText("Ready to review")).toBeInTheDocument();
    await act(async () => { await i18n.changeLanguage("zh-CN"); });
    expect(screen.getByText("可以查看")).toBeInTheDocument();
});

it("records only the section and visible row count when it is seen", async () => {
    await showSection();
    expect(record).toHaveBeenCalledWith("needs_you", 3);
    expect(record).toHaveBeenCalledTimes(1);
});

async function showSection() {
    await show([item("one"), item("two"), item("three"), item("four")]);
}


it("keeps the proven handled reply in the row in English and Chinese", async () => {
    const i18n = createInstance();
    await i18n.init({ lng: "en", resources: { en: { today: en }, "zh-CN": { today: zh } }, defaultNS: "today" });
    const receipt = { item: item("one", "waiting_on_reply"), index: 0, result: { kind: "you_replied", person: "Dana", at: new Date(2026, 9, 30, 10, 12).getTime() } };
    render(<I18nextProvider i18n={i18n}><TodayNeedsSection items={[]} receipts={[receipt]} onOpen={vi.fn()} onOpenTasks={vi.fn()} /></I18nextProvider>);
    expect(screen.getByRole("status")).toHaveTextContent("Already done: you replied to Dana at 10:12 AM.");
    expect(screen.queryByRole("button", { name: "Review" })).toBeNull();
    await act(async () => { await i18n.changeLanguage("zh-CN"); });
    expect(screen.getByRole("status")).toHaveTextContent("已经做了：你");
    expect(screen.getByRole("status")).toHaveTextContent("回复了 Dana");
});


it("says your reply for a proven reply with no usable name", async () => {
    const i18n = createInstance();
    await i18n.init({ lng: "en", resources: { en: { today: en }, "zh-CN": { today: zh } }, defaultNS: "today" });
    const receipt = { item: item("one", "waiting_on_reply"), index: 0, result: { kind: "you_replied", at: new Date(2026, 9, 30, 10, 12).getTime() } };
    render(<I18nextProvider i18n={i18n}><TodayNeedsSection items={[]} receipts={[receipt]} onOpen={vi.fn()} onOpenTasks={vi.fn()} /></I18nextProvider>);
    expect(screen.getByRole("status")).toHaveTextContent("Already done: you replied at 10:12 AM.");
    await act(async () => { await i18n.changeLanguage("zh-CN"); });
    expect(screen.getByRole("status")).toHaveTextContent("已经做了：你");
    expect(screen.getByRole("status")).toHaveTextContent("回复了。");
});


it("counts only current task rows when a handled receipt occupies a preview slot", async () => {
    const i18n = createInstance();
    await i18n.init({ lng: "en", resources: { en: { today: en } }, defaultNS: "today" });
    const receipt = { item: item("handled", "waiting_on_reply"), index: 0, result: { kind: "you_replied", person: "Dana", at: new Date(2026, 9, 30, 10, 12).getTime() } };
    const tasks = vi.fn();
    render(<I18nextProvider i18n={i18n}><TodayNeedsSection items={[item("one"), item("two"), item("three")]} receipts={[receipt]} onOpen={vi.fn()} onOpenTasks={tasks} /></I18nextProvider>);
    expect(screen.getByRole("status")).toHaveTextContent("Already done");
    expect(screen.getByText("Sentence one")).toBeInTheDocument();
    expect(screen.getByText("Sentence two")).toBeInTheDocument();
    expect(screen.queryByText("Sentence three")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "1 more in Tasks" }));
    expect(tasks).toHaveBeenCalledOnce();
});


it("does not count a receipt as a shown task while the Tasks list is refreshing", async () => {
    const i18n = createInstance();
    await i18n.init({ lng: "en", resources: { en: { today: en } }, defaultNS: "today" });
    const handled = item("handled", "waiting_on_reply");
    const receipt = { item: handled, index: 0, result: { kind: "you_replied", person: "Dana", at: new Date(2026, 9, 30, 10, 12).getTime() } };
    render(<I18nextProvider i18n={i18n}><TodayNeedsSection items={[handled, item("one"), item("two")]} receipts={[receipt]} onOpen={vi.fn()} onOpenTasks={vi.fn()} /></I18nextProvider>);
    expect(screen.getByRole("button", { name: "1 more in Tasks" })).toBeInTheDocument();
});


it("keeps a receipt in the resolved task's original slot", async () => {
    const i18n = createInstance();
    await i18n.init({ lng: "en", resources: { en: { today: en } }, defaultNS: "today" });
    const receipt = { item: item("handled", "waiting_on_reply"), index: 1, result: { kind: "you_replied", person: "Dana", at: new Date(2026, 9, 30, 10, 12).getTime() } };
    render(<I18nextProvider i18n={i18n}><TodayNeedsSection items={[item("one"), item("two"), item("three")]} receipts={[receipt]} onOpen={vi.fn()} onOpenTasks={vi.fn()} /></I18nextProvider>);
    const section = screen.getByRole("region", { name: "Needs you" });
    const lines = within(section).getByText("Sentence one").closest(".divide-y")!;
    expect(Array.from(lines.children).map((row) => row.textContent)).toEqual([expect.stringContaining("Sentence one"), expect.stringContaining("Already done"), expect.stringContaining("Sentence two")]);
    expect(within(section).getByRole("button", { name: "1 more in Tasks" })).toBeInTheDocument();
});


it("measures each Needs you action once without row content", async () => {
    await show([item("private-name"), item("two"), item("three"), item("four")]);
    fireEvent.click(screen.getAllByRole("button", { name: "Review" })[0]);
    fireEvent.click(screen.getByRole("button", { name: "1 more in Tasks" }));
    expect(action).toHaveBeenCalledTimes(2);
    expect(action).toHaveBeenNthCalledWith(1, "today", "D01", "open", "opened", expect.any(Number));
    expect(action).toHaveBeenNthCalledWith(2, "today", "T17", "open", "opened", expect.any(Number));
    expect(JSON.stringify(action.mock.calls)).not.toContain("private-name");
});


it("keeps multiple resolved receipts in their slots until the next brief", async () => {
    const i18n = createInstance();
    await i18n.init({ lng: "en", resources: { en: { today: en } }, defaultNS: "today" });
    const result = { kind: "you_replied", person: "Dana", at: new Date(2026, 9, 30, 10, 12).getTime() };
    const receipts = [
        { item: item("handled-one", "waiting_on_reply"), index: 0, result },
        { item: item("handled-two", "waiting_on_reply"), index: 2, result },
    ];
    render(<I18nextProvider i18n={i18n}><TodayNeedsSection items={[item("one"), item("two"), item("three")]} receipts={receipts} onOpen={vi.fn()} onOpenTasks={vi.fn()} /></I18nextProvider>);
    const section = screen.getByRole("region", { name: "Needs you" });
    const rows = within(section).getByText("Sentence one").closest(".divide-y")!;
    expect(Array.from(rows.children).map((row) => row.textContent)).toEqual([expect.stringContaining("Already done"), expect.stringContaining("Sentence one"), expect.stringContaining("Already done")]);
    expect(within(section).getByRole("button", { name: "2 more in Tasks" })).toBeInTheDocument();
});


it.each([["en", "This doesn't need you anymore."], ["zh-CN", "这件事已经不需要你处理了。"]])("shows a confirmed resolution without a button in %s", async (language, message) => {
    const i18n = createInstance();
    await i18n.init({ lng: language, resources: { en: { today: en }, "zh-CN": { today: zh } }, defaultNS: "today" });
    const receipt = { item: item("handled", "waiting_on_reply"), index: 0, result: { kind: "resolved" } };
    render(<I18nextProvider i18n={i18n}><TodayNeedsSection items={[]} receipts={[receipt]} onOpen={vi.fn()} onOpenTasks={vi.fn()} /></I18nextProvider>);
    expect(screen.getByRole("status")).toHaveTextContent(message);
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
});


it("measures a retry without recording source content", async () => {
    const i18n = createInstance();
    await i18n.init({ lng: "en", resources: { en: { today: en } }, defaultNS: "today" });
    const row = item("private-id", "waiting_on_reply");
    const open = vi.fn();
    render(<I18nextProvider i18n={i18n}><TodayNeedsSection items={[row]} failures={{ [row.id]: 1 }} onOpen={open} onOpenTasks={vi.fn()} /></I18nextProvider>);
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(open).toHaveBeenCalledWith(row, 0);
    expect(action).toHaveBeenCalledWith("today", "D01", "open", "opened", expect.any(Number));
    expect(JSON.stringify(action.mock.calls)).not.toContain(row.id);
});
