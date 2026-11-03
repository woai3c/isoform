import { fireEvent, render, screen, within } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { WhatIsoformSeesSheet } from "./what-isoform-sees";

const mocks = vi.hoisted(() => ({ get: vi.fn(), emit: vi.fn() }));
vi.mock("@/lib/wails-shim-app", () => ({ GetWhatIsoformSees: mocks.get }));
vi.mock("@/lib/wails-shim-runtime", () => ({ EventsEmit: mocks.emit }));

beforeEach(() => {
    vi.clearAllMocks();
    mocks.get.mockResolvedValue({ assistant: "claude_code", sources: [
        { job: "mail", on: false }, { job: "calendar", on: true }, { job: "calls", on: true },
        { job: "screen", on: true }, { job: "assistant", on: true }, { job: "contacts", on: false },
    ] });
});

it.each([
    ["en", "What Isoform sees", "Mail", "Can't see your mail yet, so there are no drafts.", "Turn on", "What leaves your computer"],
    ["zh-CN", "Isoform 能看到什么", "邮件", "还看不到你的邮件，所以没有草稿。", "打开", "什么会离开你的电脑"],
])("renders the phone's jobs and copy in %s and opens the mail control", async (language, title, mail, detail, turnOn, leaves) => {
    await i18n.changeLanguage(language);
    const onOpenChange = vi.fn();
    render(<WhatIsoformSeesSheet open onOpenChange={onOpenChange} />);
    const dialog = await screen.findByRole("dialog", { name: title });
    expect(await within(dialog).findByText(detail)).toBeInTheDocument();
    expect(within(dialog).getByText(leaves)).toBeInTheDocument();
    const row = within(dialog).getByText(mail).parentElement!.parentElement!;
    fireEvent.click(within(row).getByRole("button", { name: turnOn }));
    expect(onOpenChange).toHaveBeenCalledWith(false);
    expect(mocks.emit).toHaveBeenCalledWith("settings:open", "activity", { anchor: "sees-mail" });
});

it("shows a failure instead of claiming permission state when the read fails", async () => {
    await i18n.changeLanguage("en");
    mocks.get.mockRejectedValueOnce(new Error("unavailable"));
    render(<WhatIsoformSeesSheet open onOpenChange={vi.fn()} />);
    expect(await screen.findByRole("alert")).toHaveTextContent("Could not load what Isoform sees.");
    expect(screen.queryByText("On")).not.toBeInTheDocument();
});
