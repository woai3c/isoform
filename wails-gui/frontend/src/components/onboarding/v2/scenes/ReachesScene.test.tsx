import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { ReachesScene } from "./ReachesScene";

const mocks = vi.hoisted(() => ({ permissions: vi.fn(), request: vi.fn(), open: vi.fn(), browserOpen: vi.fn(), copy: vi.fn(), generate: vi.fn() }));
vi.mock("@/lib/wails-shim-app", () => ({
    GetSetupPermissions: mocks.permissions,
    RequestPermission: mocks.request,
    OpenPermissionSettings: mocks.open,
    GetMobileConnectionStatus: vi.fn().mockResolvedValue({ enabled: true, devices: [] }),
    GenerateMobileConnectionCode: mocks.generate,
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
    mocks.generate.mockResolvedValue({ enabled: true, devices: [], connectionCode: "isoform://pair#test" });
});

it.each([false, true, "error"] as const)("continues after the notification request returns %s", async (result) => {
    if (result === "error") mocks.request.mockRejectedValueOnce(new Error("failed"));
    else mocks.request.mockResolvedValueOnce(result);
    const next = vi.fn();
    render(<ReachesScene onNext={next} reduced enter="" />);
    await waitFor(() => expect(mocks.permissions).toHaveBeenCalled());
    fireEvent.click(screen.getByTestId("setup-v2-notifications"));
    await waitFor(() => expect(next).toHaveBeenCalledTimes(1));
});

it("continues when the notification request never returns", async () => {
    mocks.request.mockImplementationOnce(() => new Promise(() => {}));
    const next = vi.fn();
    render(<ReachesScene onNext={next} reduced enter="" />);
    await waitFor(() => expect(mocks.permissions).toHaveBeenCalled());
    vi.useFakeTimers();
    fireEvent.click(screen.getByTestId("setup-v2-notifications"));
    await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
    expect(next).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId("setup-v2-notifications")).not.toBeDisabled();
    vi.useRealTimers();
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

it("shows the App Store in Chinese", async () => {
    await i18n.changeLanguage("zh-CN");
    render(<ReachesScene onNext={vi.fn()} reduced still="reaches" enter="" />);
    expect(screen.getByRole("button", { name: "打开 App Store" })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: "手机配对二维码" })).toBeInTheDocument();
});

it.each(["en", "zh-CN"])("shows an unreachable relay in %s without a QR code", async (language) => {
    await i18n.changeLanguage(language);
    mocks.generate.mockRejectedValueOnce(new Error("remote access is not ready; try again when the relay is available"));
    render(<ReachesScene onNext={vi.fn()} reduced enter="" />);
    expect(await screen.findByText(language === "en" ? "Your phone can't reach this computer yet. Turn on remote access." : "手机暂时无法连接到这台电脑。请开启远程访问。")).toBeInTheDocument();
    expect(screen.queryByRole("img", { name: language === "en" ? "Phone pairing code" : "手机配对二维码" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: language === "en" ? "Try again" : "重试" }));
    expect(await screen.findByRole("img", { name: language === "en" ? "Phone pairing code" : "手机配对二维码" })).toBeInTheDocument();
});
