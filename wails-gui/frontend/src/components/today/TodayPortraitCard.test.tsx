import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { TodayPortraitCard } from "./TodayPortraitCard";

vi.mock("@/lib/wails-shim-app", () => ({ GetPortraitTodayCard: vi.fn(), DismissPortraitTodayCard: vi.fn() }));

const card = {
    renamed: false,
    name: ["blueprint", "builder"],
    sides: [0.24, 0.2, 0.16, 0.38],
    sentence: [
        { text: "You ", confirmed: false },
        { text: "plan the whole thing", memoryIds: ["plan"], confirmed: false },
        { text: ", then build the first version yourself.", confirmed: false },
    ],
};

describe("Today portrait card", () => {
    beforeEach(async () => {
        await i18n.changeLanguage("en");
    });

    it("shows the name and sentence 1; Read it opens About you and hides the card", async () => {
        const onRead = vi.fn();
        const onDismiss = vi.fn();
        render(<TodayPortraitCard card={card} onRead={onRead} onDismiss={onDismiss} />);
        expect(screen.getByText("Your portrait is ready")).toBeInTheDocument();
        expect(screen.getByText("The Blueprint Builder")).toBeInTheDocument();
        expect(screen.getByText("You plan the whole thing, then build the first version yourself.")).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Read it" }));
        expect(onDismiss).toHaveBeenCalled();
        await waitFor(() => expect(onRead).toHaveBeenCalled());
    });

    it("says the name is new when it changed, and Not now only hides it", () => {
        const onRead = vi.fn();
        const onDismiss = vi.fn();
        render(<TodayPortraitCard card={{ ...card, renamed: true, name: ["curious", "connector"] }} onRead={onRead} onDismiss={onDismiss} />);
        expect(screen.getByText("Your portrait has a new name")).toBeInTheDocument();
        expect(screen.getByText("The Curious Connector")).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Not now" }));
        expect(onDismiss).toHaveBeenCalled();
        expect(onRead).not.toHaveBeenCalled();
    });

    it("keeps the card and says when Not now could not be saved", async () => {
        const onDismiss = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce(undefined);
        render(<TodayPortraitCard card={card} onRead={vi.fn()} onDismiss={onDismiss} />);
        fireEvent.click(screen.getByRole("button", { name: "Not now" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("Didn't save. Try again");
        expect(screen.getByTestId("today-portrait-card")).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Not now" }));
        await waitFor(() => expect(onDismiss).toHaveBeenCalledTimes(2));
        await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
    });

    it("reads in zh-CN", async () => {
        await i18n.changeLanguage("zh-CN");
        render(<TodayPortraitCard card={card} onRead={vi.fn()} onDismiss={vi.fn()} />);
        expect(screen.getByText("你的画像写好了")).toBeInTheDocument();
        expect(screen.getByText("蓝图型建造者")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "暂不" })).toBeInTheDocument();
    });
});


it("uses the same Chinese Not now wording as calendar holds", async () => {
    const today = (await import("@/i18n/locales/zh-CN/today.json")).default;
    expect(today.portraitCard.notNow).toBe("暂不");
    expect(today.holds.notNow).toBe(today.portraitCard.notNow);
});
