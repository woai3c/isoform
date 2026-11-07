import {
    act,
    fireEvent,
    render,
    screen,
    waitFor,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
const mocks = vi.hoisted(() => ({
    restart: vi.fn(),
    check: vi.fn(),
    download: vi.fn(),
    later: vi.fn(),
}));
vi.mock("@/lib/wails-shim-app", () => ({
    RestartToUpdate: mocks.restart,
    CheckForUpdates: mocks.check,
    DownloadUpdate: mocks.download,
    DismissUpdateUntilNextLaunch: mocks.later,
}));
import i18n from "@/i18n";
import { UpdateReadyCard } from "./update-ready-card";

describe("Update ready sidebar card", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        await i18n.changeLanguage("en");
        mocks.restart.mockResolvedValue(undefined);
        mocks.later.mockResolvedValue(undefined);
    });
    it("restarts only when Restart now is clicked", async () => {
        render(<UpdateReadyCard version="0.1.302" onLater={vi.fn()} />);
        expect(screen.getByText("Isoform 0.1.302 is ready")).toBeInTheDocument();
        expect(mocks.restart).not.toHaveBeenCalled();
        fireEvent.click(screen.getByRole("button", { name: "Restart now" }));
        await waitFor(() => expect(mocks.restart).toHaveBeenCalledOnce());
        expect(mocks.later).not.toHaveBeenCalled();
    });
    it("shows Restarting from the click until the app quits", async () => {
        let accept: () => void = () => {};
        mocks.restart.mockImplementationOnce(
            () =>
                new Promise<void>((resolve) => {
                    accept = resolve;
                }),
        );
        render(<UpdateReadyCard version="0.1.302" onLater={vi.fn()} />);
        fireEvent.click(screen.getByRole("button", { name: "Restart now" }));
        expect(
            screen.getByRole("button", { name: "Restarting…" }),
        ).toBeDisabled();
        expect(screen.getByRole("button", { name: "Later" })).toBeDisabled();
        await act(async () => {
            accept();
        });
        expect(
            screen.getByRole("button", { name: "Restarting…" }),
        ).toBeDisabled();
    });
    it("Later hides only after the backend accepts the session dismissal", async () => {
        const hide = vi.fn();
        render(<UpdateReadyCard version="0.1.302" onLater={hide} />);
        fireEvent.click(screen.getByRole("button", { name: "Later" }));
        await waitFor(() => expect(hide).toHaveBeenCalledOnce());
        expect(mocks.later).toHaveBeenCalledOnce();
        expect(mocks.restart).not.toHaveBeenCalled();
    });
    it("keeps a rejected install visible after the backend clears ready, and downloads again without restarting", async () => {
        let rejectInstall: (error: Error) => void = () => {};
        mocks.restart.mockImplementationOnce(
            () =>
                new Promise<void>((_resolve, reject) => {
                    rejectInstall = reject;
                }),
        );
        mocks.check.mockResolvedValue({ available: true });
        mocks.download.mockResolvedValue(undefined);
        const view = render(
            <UpdateReadyCard version="0.1.302" onLater={vi.fn()} />,
        );
        fireEvent.click(screen.getByRole("button", { name: "Restart now" }));
        view.rerender(
            <UpdateReadyCard version="" visible={false} onLater={vi.fn()} />,
        );
        await act(async () => {
            rejectInstall(new Error("checksum"));
        });
        expect(
            screen.getByRole("region", { name: "Update could not finish" }),
        ).toBeInTheDocument();
        expect(
            screen.queryByRole("button", { name: "Restart now" }),
        ).not.toBeInTheDocument();
        fireEvent.click(
            screen.getByRole("button", { name: "Download update" }),
        );
        await waitFor(() => expect(mocks.download).toHaveBeenCalledOnce());
        expect(mocks.check).toHaveBeenCalledOnce();
        expect(mocks.restart).toHaveBeenCalledOnce();
    });
    it("keeps the card when Later cannot be recorded", async () => {
        mocks.later.mockRejectedValueOnce(new Error("unavailable"));
        const hide = vi.fn();
        render(<UpdateReadyCard version="0.1.302" onLater={hide} />);
        fireEvent.click(screen.getByRole("button", { name: "Later" }));
        expect(await screen.findByRole("alert")).toBeInTheDocument();
        expect(hide).not.toHaveBeenCalled();
    });
    it("keeps the card recoverable when restart fails", async () => {
        mocks.restart.mockRejectedValue(new Error("checksum"));
        render(<UpdateReadyCard version="0.1.302" onLater={vi.fn()} />);
        fireEvent.click(screen.getByRole("button", { name: "Restart now" }));
        expect(await screen.findByRole("alert")).toBeInTheDocument();
        expect(
            screen.getByRole("button", { name: "Restart now" }),
        ).toBeEnabled();
    });
    it("translates the complete card", async () => {
        await i18n.changeLanguage("zh-CN");
        render(<UpdateReadyCard version="0.1.302" onLater={vi.fn()} />);
        expect(screen.getByText("Isoform 0.1.302 已准备好")).toBeInTheDocument();
        expect(
            screen.getByRole("button", { name: "稍后" }),
        ).toBeInTheDocument();
    });
});
