import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { RelaunchNotice, useScreenRecordingRelaunch } from "./relaunch-notice";

const mocks = vi.hoisted(() => ({
    handlers: new Map<string, Set<() => void>>(),
    needsRelaunch: vi.fn(),
    relaunch: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    GetScreenRecordingRelaunch: mocks.needsRelaunch,
    RelaunchApp: mocks.relaunch,
}));

vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: (name: string, handler: () => void) => {
        const set = mocks.handlers.get(name) ?? new Set();
        set.add(handler);
        mocks.handlers.set(name, set);
        return () => set.delete(handler);
    },
}));

function Probe() {
    const app = useScreenRecordingRelaunch();
    return app ? <RelaunchNotice app={app} /> : <span>no-notice</span>;
}

describe("RelaunchNotice", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        mocks.handlers.clear();
        await i18n.changeLanguage("en");
    });

    it("appears once Screen Recording is turned on during this run", async () => {
        mocks.needsRelaunch.mockResolvedValue({ needed: false, app: "Isoform" });
        render(<Probe />);
        expect(await screen.findByText("no-notice")).toBeTruthy();

        mocks.needsRelaunch.mockResolvedValue({ needed: true, app: "Isoform" });
        await act(async () => {
            for (const handler of mocks.handlers.get("permissions:changed") ?? []) handler();
        });
        expect(await screen.findByRole("button", { name: "Quit and reopen" })).toBeTruthy();
        expect(screen.getByText("Reopen Isoform to finish turning on Screen Recording")).toBeTruthy();
    });

    it("quits and reopens on the tap, and says so when it cannot", async () => {
        mocks.relaunch.mockRejectedValueOnce(new Error("not running from an app bundle"));
        render(<RelaunchNotice app="Yansu" />);
        fireEvent.click(screen.getByRole("button", { name: "Quit and reopen" }));
        expect(mocks.relaunch).toHaveBeenCalledTimes(1);
        expect(
            await screen.findByText("Yansu could not reopen itself. Quit it from the menu bar, then open it again."),
        ).toBeTruthy();
    });

    it("stops saying it is reopening when the reopen gives up", async () => {
        mocks.relaunch.mockResolvedValueOnce(undefined);
        render(<RelaunchNotice app="Isoform" />);
        fireEvent.click(screen.getByRole("button", { name: "Quit and reopen" }));
        expect(await screen.findByRole("button", { name: "Reopening…" })).toBeTruthy();
        await act(async () => {
            for (const handler of mocks.handlers.get("relaunch:failed") ?? []) handler();
        });
        expect(screen.getByRole("button", { name: "Quit and reopen" })).toBeTruthy();
        expect(
            screen.getByText("Isoform could not reopen itself. Quit it from the menu bar, then open it again."),
        ).toBeTruthy();
    });
});
