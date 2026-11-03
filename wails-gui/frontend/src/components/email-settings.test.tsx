import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import type { EmailChannelStatus } from "@/lib/wails-shim-app";
import en from "@/i18n/locales/en/settings.json";
import zh from "@/i18n/locales/zh-CN/settings.json";
import { describeEmailError, EmailSettings } from "./email-settings";

const mocks = vi.hoisted(() => ({
    getStatus: vi.fn(),
    start: vi.fn(),
    confirm: vi.fn(),
    stop: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    GetEmailChannelStatus: mocks.getStatus,
    StartEmailVerification: mocks.start,
    ConfirmEmailVerification: mocks.confirm,
    StopEmailChannel: mocks.stop,
}));

const none: EmailChannelStatus = {
    available: true,
    state: "none",
    address: "",
    pendingAddress: "",
    codeExpiresAt: "",
    sentToday: 0,
    dailyCap: 20,
    lastError: "",
};

const pending = (): EmailChannelStatus => ({
    ...none,
    state: "pending_verification",
    pendingAddress: "owner@example.com",
    codeExpiresAt: new Date(Date.now() + 10 * 60 * 1000).toISOString(),
});

const active: EmailChannelStatus = { ...none, state: "active", address: "owner@example.com", sentToday: 1 };

async function renderCard(status: EmailChannelStatus) {
    mocks.getStatus.mockResolvedValue(status);
    const view = render(<EmailSettings />);
    await waitFor(() => expect(screen.queryByText("Checking email...")).not.toBeInTheDocument());
    return view;
}

describe("EmailSettings", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        await i18n.changeLanguage("en");
    });

    it("asks for an address and mails a code", async () => {
        await renderCard(none);
        expect(screen.getByRole("heading", { name: "Results by email" })).toBeInTheDocument();
        const send = screen.getByRole("button", { name: "Email me a code" });
        expect(send).toBeDisabled();
        fireEvent.change(screen.getByLabelText("Your email address"), { target: { value: "owner@" } });
        expect(screen.getByRole("alert")).toHaveTextContent("one address");

        mocks.start.mockResolvedValue(pending());
        fireEvent.change(screen.getByLabelText("Your email address"), { target: { value: " owner@example.com " } });
        fireEvent.click(send);
        await waitFor(() => expect(mocks.start).toHaveBeenCalledWith("owner@example.com"));
        expect(await screen.findByText(/sent a 6-digit code to owner@example.com/)).toBeInTheDocument();
    });

    it("confirms the code, then shows where results go and how to stop", async () => {
        await renderCard(pending());
        mocks.confirm.mockResolvedValue(active);
        fireEvent.change(screen.getByLabelText("Code"), { target: { value: "12a3456" } });
        fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
        await waitFor(() => expect(mocks.confirm).toHaveBeenCalledWith("123456"));
        expect(await screen.findByText("Results go to owner@example.com.")).toBeInTheDocument();
        expect(screen.getByText("1 sent today, up to 20 a day.")).toBeInTheDocument();

        mocks.stop.mockResolvedValue(none);
        fireEvent.click(screen.getByRole("button", { name: "Stop emails" }));
        await waitFor(() => expect(mocks.stop).toHaveBeenCalled());
        expect(await screen.findByLabelText("Your email address")).toBeInTheDocument();
    });

    it("says why it cannot be used and turns refusals into sentences", async () => {
        await renderCard({ ...none, available: false });
        expect(screen.getByText(/Turn on the connection on the Phone page/)).toBeInTheDocument();

        mocks.getStatus.mockResolvedValue({ ...none, lastError: "not_configured" });
        render(<EmailSettings />);
        expect(await screen.findAllByText("Email is not available on this server yet.")).not.toHaveLength(0);

        const t = i18n.getFixedT("en", "settings");
        expect(describeEmailError("code_wrong", t)).toBe("That code is not right.");
        expect(describeEmailError("something new", t)).toBe("Something went wrong. Try again.");
    });

    it("has every key in both languages", () => {
        const keys = (value: unknown, prefix = ""): string[] =>
            value && typeof value === "object"
                ? Object.entries(value).flatMap(([k, v]) => keys(v, prefix ? `${prefix}.${k}` : k))
                : [prefix];
        expect(keys(zh.emailResults).sort()).toEqual(keys(en.emailResults).sort());
        expect(Object.keys(zh.proactiveNotifications.kinds).sort()).toEqual(
            Object.keys(en.proactiveNotifications.kinds).sort(),
        );
    });
});
