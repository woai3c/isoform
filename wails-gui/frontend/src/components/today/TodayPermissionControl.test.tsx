import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createInstance } from "i18next";
import { I18nextProvider } from "react-i18next";
import { beforeEach, expect, it, vi } from "vitest";
import en from "@/i18n/locales/en/today.json";
import zh from "@/i18n/locales/zh-CN/today.json";
import { TodayPermissionControl } from "./TodayPermissionControl";

const mocks = vi.hoisted(() => ({ states: vi.fn(), request: vi.fn(), settings: vi.fn(), record: vi.fn() }));
vi.mock("@/lib/wails-shim-app", () => ({ GetTodayPermissionStates: mocks.states, RequestTodayPermission: mocks.request, OpenPermissionSettings: mocks.settings, RecordTodayAction: mocks.record }));
vi.mock("../../../wailsjs/runtime/runtime", () => ({ EventsOn: vi.fn(() => () => {}) }));
beforeEach(() => { vi.clearAllMocks(); mocks.states.mockResolvedValue({ screenRecording: "not_determined", calendars: "not_determined" }); mocks.request.mockResolvedValue(true); mocks.settings.mockResolvedValue(undefined); mocks.record.mockResolvedValue(undefined); });
async function show(permission: "screen_recording" | "calendars", language = "en") {
    const i18n = createInstance();
    await i18n.init({ lng: language, resources: { en: { today: en }, "zh-CN": { today: zh } }, defaultNS: "today" });
    render(<I18nextProvider i18n={i18n}><TodayPermissionControl permission={permission} action="T06" explain /></I18nextProvider>);
}
it("requests the selected permission only after Turn on", async () => {
    await show("calendars");
    const button = screen.getByRole("button", { name: "Turn on" });
    await waitFor(() => expect(button).toBeEnabled());
    expect(mocks.request).not.toHaveBeenCalled();
    fireEvent.click(button);
    await waitFor(() => expect(mocks.request).toHaveBeenCalledWith("calendars"));
    await waitFor(() => expect(mocks.record).toHaveBeenCalledWith("today", "T06", "do", "done", expect.any(Number)));
});
it("offers System Settings after a denial in Chinese", async () => {
    mocks.states.mockResolvedValue({ screenRecording: "denied", calendars: "granted" });
    await show("screen_recording", "zh-CN");
    const button = await screen.findByRole("button", { name: "打开系统设置" });
    expect(screen.getByText("macOS 拒绝了屏幕录制。在系统设置里开启后再回来。")).toBeInTheDocument();
    fireEvent.click(button);
    await waitFor(() => expect(mocks.settings).toHaveBeenCalledWith("screen_recording"));
    expect(mocks.request).not.toHaveBeenCalled();
});
it("shows an inline retry when the request fails", async () => {
    mocks.request.mockRejectedValueOnce(new Error("failed"));
    await show("screen_recording");
    const button = screen.getByRole("button", { name: "Turn on" });
    await waitFor(() => expect(button).toBeEnabled());
    fireEvent.click(button);
    expect(await screen.findByRole("alert")).toHaveTextContent("Couldn't update permission. Try again");
    fireEvent.click(button);
    await waitFor(() => expect(mocks.request).toHaveBeenCalledTimes(2));
});
