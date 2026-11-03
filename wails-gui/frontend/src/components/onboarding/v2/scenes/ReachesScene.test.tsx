import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { ReachesScene } from "./ReachesScene";

const mocks = vi.hoisted(() => ({ permissions: vi.fn(), request: vi.fn(), open: vi.fn(), browserOpen: vi.fn(), copy: vi.fn() }));
vi.mock("@/lib/wails-shim-app", () => ({
    GetSetupPermissions: mocks.permissions,
    RequestPermission: mocks.request,
    OpenPermissionSettings: mocks.open,
    GetMobileConnectionStatus: vi.fn().mockResolvedValue({ enabled: true, devices: [] }),
    GenerateMobileConnectionCode: vi.fn().mockResolvedValue({ enabled: true, devices: [], connectionCode: "isoform://pair#test" }),
    EnableMobileConnectionUsingRelay: vi.fn(),
    ConfirmMobilePairing: vi.fn(),
    NativeClipboardSetText: vi.fn(),
}));
vi.mock("../../../../../wailsjs/runtime/runtime", () => ({
    BrowserOpenURL: mocks.browserOpen,
    ClipboardSetText: mocks.copy,
    EventsOn: vi.fn(() => () => {}),
}));

beforeEach(async () => {
    vi.clearAllMocks();
    await i18n.changeLanguage("en");
    mocks.permissions.mockResolvedValue({ notificationGranted: false });
    mocks.request.mockResolvedValue(false);
    mocks.open.mockResolvedValue(undefined);
});

it("keeps denied notifications off and opens System Settings", async () => {
    const next = vi.fn();
    render(<ReachesScene onNext={next} reduced enter="" />);
    const button = await screen.findByTestId("setup-v2-notifications");
    await waitFor(() => expect(mocks.permissions).toHaveBeenCalled());
    fireEvent.click(button);
    expect(await screen.findByText("Notifications are off. Turn them on in System Settings.")).toBeInTheDocument();
    expect(mocks.open).toHaveBeenCalledWith("notifications");
    expect(button).not.toHaveTextContent("Notifications on");
    expect(next).not.toHaveBeenCalled();
});

it("reports request failure without claiming notifications are on", async () => {
    mocks.request.mockRejectedValueOnce(new Error("failed"));
    const next = vi.fn();
    render(<ReachesScene onNext={next} reduced enter="" />);
    await waitFor(() => expect(mocks.permissions).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId("setup-v2-notifications"));
    expect(await screen.findByText("Could not check or turn on notifications. Try again.")).toBeInTheDocument();
    expect(next).not.toHaveBeenCalled();
});

it("shows on only after the real authorization is granted", async () => {
    mocks.permissions.mockResolvedValue({ notificationGranted: true });
    render(<ReachesScene onNext={vi.fn()} reduced enter="" />);
    await waitFor(() => expect(screen.getByTestId("setup-v2-notifications")).toHaveTextContent("Notifications on"));
});

it("opens the App Store and keeps the pairing code separate", () => {
    render(<ReachesScene onNext={() => {}} reduced still="reaches" enter="" />);
    fireEvent.click(screen.getByRole("button", { name: "Open App Store" }));
    expect(mocks.browserOpen).toHaveBeenCalledWith("https://apps.apple.com/app/isoform/id6827347350");
    expect(screen.getByRole("img", { name: "Phone pairing code" })).toBeInTheDocument();
    expect(screen.getByText("Download Isoform, then scan this code in the app to pair with this computer.")).toBeInTheDocument();
});

it("shows the App Store and notification messages in Chinese", async () => {
    await i18n.changeLanguage("zh-CN");
    render(<ReachesScene onNext={vi.fn()} reduced enter="" />);
    await waitFor(() => expect(mocks.permissions).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId("setup-v2-notifications"));
    expect(await screen.findByText("通知已关闭。请在系统设置中打开。")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "打开 App Store" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "手机配对二维码" })).toBeInTheDocument();
});

it("retries a failed notification request", async () => {
    mocks.request.mockRejectedValueOnce(new Error("failed"));
    render(<ReachesScene onNext={vi.fn()} reduced enter="" />);
    await waitFor(() => expect(mocks.permissions).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId("setup-v2-notifications"));
    await screen.findByText("Could not check or turn on notifications. Try again.");
    fireEvent.click(screen.getByTestId("setup-v2-notifications"));
    expect(await screen.findByText("Notifications are off. Turn them on in System Settings.")).toBeInTheDocument();
    expect(mocks.request).toHaveBeenCalledTimes(2);
});
