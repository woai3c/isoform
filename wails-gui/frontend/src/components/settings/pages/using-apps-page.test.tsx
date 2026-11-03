import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import pageSource from "./using-apps-page.tsx?raw";
import { UsingAppsPage } from "./using-apps-page";

const mocks = vi.hoisted(() => ({
    getSnapshot: vi.fn(),
    getCursor: vi.fn(),
    setCursor: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    GetComputerUseAppAccessSnapshot: mocks.getSnapshot,
    GetCuaCursorOverlayEnabled: mocks.getCursor,
    SetCuaCursorOverlayEnabled: mocks.setCursor,
}));

vi.mock("../../../../wailsjs/runtime/runtime", () => ({
    BrowserOpenURL: vi.fn(),
    EventsOn: () => () => {},
}));

vi.mock("@/components/enable-computer-use-dialog", () => ({
    EnableComputerUseDialog: ({ open }: { open: boolean }) =>
        open ? <div role="dialog">Permission setup</div> : null,
}));

function readySnapshot() {
    return {
        policyVersion: "computer-use-app-access-v1",
        mode: "computer_use_only",
        oauthFallback: false,
        available: true,
        accessibility: true,
        screenRecording: true,
        screenLocked: false,
        status: "ready",
        statusReason: "",
        apps: [],
    };
}

describe("UsingAppsPage", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        window.localStorage.clear();
        await i18n.changeLanguage("en");
        mocks.getCursor.mockResolvedValue(true);
        mocks.setCursor.mockResolvedValue(undefined);
        mocks.getSnapshot.mockResolvedValue({
            policyVersion: "computer-use-app-access-v1",
            mode: "computer_use_only",
            oauthFallback: false,
            available: true,
            accessibility: false,
            screenRecording: false,
            screenLocked: false,
            status: "permission_required",
            statusReason: "Enable Accessibility for Isoform before it can inspect or operate apps.",
            apps: [
                { id: "linear", label: "Linear", category: "planning", entryUrl: "https://linear.app/" },
            ],
        });
    });

    it("opens with readiness, one setup control and the cursor switch; the app list waits under Advanced", async () => {
        render(<UsingAppsPage />);

        expect(screen.getByRole("heading", { name: "Let Isoform use your apps" })).toBeInTheDocument();
        expect(await screen.findByText("Needs setup")).toBeInTheDocument();
        expect(screen.getAllByRole("button", { name: /Set up|Review permissions/ })).toHaveLength(1);

        // The cursor only shows while Isoform uses your apps, so it waits for setup.
        const cursor = screen.getByRole("switch", { name: "Show Isoform's cursor while it works" });
        await waitFor(() =>
            expect(screen.getByText("Set up Let Isoform use your apps first.")).toBeInTheDocument(),
        );
        expect(cursor).toBeDisabled();

        const advanced = screen.getByRole("button", { name: /Advanced/ });
        expect(advanced).toHaveTextContent("Apps Isoform knows how to use");
        expect(screen.queryByText("Linear")).not.toBeInTheDocument();

        fireEvent.click(advanced);
        expect(screen.getByText("Linear")).toBeInTheDocument();
        expect(screen.getByText("Planning")).toBeInTheDocument();
    });

    it("turns the cursor off and back on when the save fails", async () => {
        mocks.getSnapshot.mockResolvedValue(readySnapshot());
        mocks.setCursor.mockRejectedValueOnce(new Error("driver busy"));
        render(<UsingAppsPage />);

        const cursor = screen.getByRole("switch", { name: "Show Isoform's cursor while it works" });
        await waitFor(() => expect(cursor).toBeEnabled());
        fireEvent.click(cursor);

        expect(mocks.setCursor).toHaveBeenCalledWith(false);
        await waitFor(() => expect(cursor).toHaveAttribute("aria-checked", "true"));
    });

    it("lets the cursor switch be used once access is ready", async () => {
        mocks.getSnapshot.mockResolvedValue({ ...readySnapshot(), screenRecording: false, status: "read_only_ready" });
        render(<UsingAppsPage />);

        expect(await screen.findByText("Ready, with limits")).toBeInTheDocument();
        const cursor = screen.getByRole("switch", { name: "Show Isoform's cursor while it works" });
        await waitFor(() => expect(cursor).toBeEnabled());
        expect(screen.queryByText("Set up Let Isoform use your apps first.")).not.toBeInTheDocument();
        expect(
            screen.getByText(
                "Shows a second pointer and click marks wherever Isoform clicks or types, so you can follow along. It never moves your own mouse.",
            ),
        ).toBeInTheDocument();
    });

    it("keeps the guided setup in one place", () => {
        expect(pageSource).not.toMatch(/ComputerUseSetupCard|EnableComputerUseDialog/);
    });
});
