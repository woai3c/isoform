import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import type { SMSChannelStatus } from "@/lib/wails-shim-app";
import { pageQueryCache } from "@/lib/query-cache";
import { resetPageQueries } from "@/lib/page-queries";
import en from "@/i18n/locales/en/settings.json";
import zh from "@/i18n/locales/zh-CN/settings.json";
import componentSource from "./sms-text-settings.tsx?raw";
import { describeSMSError, SmsTextSettings } from "./sms-text-settings";

const mocks = vi.hoisted(() => ({
    getStatus: vi.fn(),
    start: vi.fn(),
    confirm: vi.fn(),
    stop: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    GetSMSChannelStatus: mocks.getStatus,
    StartSMSVerification: mocks.start,
    ConfirmSMSVerification: mocks.confirm,
    StopSMSChannel: mocks.stop,
}));

const none: SMSChannelStatus = {
    available: true,
    state: "none",
    number: "",
    pendingNumber: "",
    codeExpiresAt: "",
    sentToday: 0,
    sentThisMonth: 0,
    dailyCap: 10,
    monthlyCap: 200,
    lastError: "",
};

const inTenMinutes = () => new Date(Date.now() + 10 * 60 * 1000).toISOString();

const pending = (): SMSChannelStatus => ({
    ...none,
    state: "pending_verification",
    pendingNumber: "+14155550123",
    codeExpiresAt: inTenMinutes(),
});

const active: SMSChannelStatus = { ...none, state: "active", number: "+14155550123", sentToday: 2, sentThisMonth: 7 };

async function renderCard(status: SMSChannelStatus) {
    mocks.getStatus.mockResolvedValue(status);
    const view = render(<SmsTextSettings />);
    await waitFor(() => expect(screen.queryByText("Checking text messages...")).not.toBeInTheDocument());
    return view;
}

describe("SmsTextSettings", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        await i18n.changeLanguage("en");
        // The card reads through the shared query cache; each test starts cold.
        resetPageQueries();
    });

    it("asks for a number, says the plan is coming soon, and texts a code", async () => {
        await renderCard(none);
        expect(screen.getByRole("heading", { name: "Get Isoform by text message" })).toBeInTheDocument();
        expect(screen.getByText(/\$2 a month plan/)).toHaveTextContent("coming soon");
        expect(screen.getByText(/unencrypted/)).toBeInTheDocument();
        const send = screen.getByRole("button", { name: "Text me a code" });
        expect(send).toBeDisabled();

        fireEvent.change(screen.getByLabelText("Mobile number"), { target: { value: "4155550123" } });
        expect(screen.getByRole("alert")).toHaveTextContent("international form");
        expect(send).toBeDisabled();

        mocks.start.mockResolvedValue(pending());
        fireEvent.change(screen.getByLabelText("Mobile number"), { target: { value: "+1 (415) 555-0123" } });
        fireEvent.click(send);
        await waitFor(() => expect(mocks.start).toHaveBeenCalledWith("+14155550123"));
        expect(await screen.findByText(/texted a 6-digit code to \+14155550123/)).toBeInTheDocument();
    });

    it("confirms the code and shows what is texted and how to stop", async () => {
        await renderCard(pending());
        const confirm = screen.getByRole("button", { name: "Confirm" });
        expect(confirm).toBeDisabled();
        fireEvent.change(screen.getByLabelText("Code"), { target: { value: "12a34" } });
        expect(screen.getByLabelText("Code")).toHaveValue("1234");
        expect(confirm).toBeDisabled();

        mocks.confirm.mockResolvedValue(active);
        fireEvent.change(screen.getByLabelText("Code"), { target: { value: "123456" } });
        fireEvent.click(confirm);
        await waitFor(() => expect(mocks.confirm).toHaveBeenCalledWith("123456"));
        expect(await screen.findByText(/Isoform texts \+14155550123/)).toBeInTheDocument();
        expect(screen.getByText("2 of 10 texts today, 7 this month.")).toBeInTheDocument();

        mocks.stop.mockResolvedValue(none);
        fireEvent.click(screen.getByRole("button", { name: "Stop texts" }));
        await waitFor(() => expect(mocks.stop).toHaveBeenCalled());
        expect(await screen.findByRole("button", { name: "Text me a code" })).toBeInTheDocument();
    });

    it("shows a refusal in plain words and reads the status back", async () => {
        await renderCard(pending());
        mocks.confirm.mockRejectedValue(new Error("code_wrong"));
        mocks.getStatus.mockResolvedValue(pending());
        fireEvent.change(screen.getByLabelText("Code"), { target: { value: "000000" } });
        fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("That code is not right.");
        await waitFor(() => expect(mocks.getStatus).toHaveBeenCalledTimes(2));
    });

    it("explains a number that replied STOP", async () => {
        await renderCard({ ...active, state: "stopped" });
        expect(screen.getByText(/replied STOP/)).toHaveTextContent("Text START");
        expect(screen.getByRole("button", { name: "Remove number" })).toBeEnabled();
    });

    it("keeps showing the active number while a code for a new one is out", async () => {
        await renderCard({ ...pending(), state: "active", number: "+14155550123", pendingNumber: "+14155550199" });
        expect(screen.getByText(/still texts \+14155550123/)).toBeInTheDocument();
        expect(screen.getByText(/texted a 6-digit code to \+14155550199/)).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
        expect(screen.getByText(/Isoform texts \+14155550123/)).toBeInTheDocument();
    });

    it("keeps the active number while a new one is entered", async () => {
        await renderCard(active);
        fireEvent.click(screen.getByRole("button", { name: "Change number" }));
        expect(screen.getByLabelText("Mobile number")).toHaveValue("");
        fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
        expect(screen.getByText(/Isoform texts \+14155550123/)).toBeInTheDocument();
    });

    it("says what is missing when texts cannot work yet", async () => {
        const view = await renderCard({ ...none, available: false });
        expect(screen.getByText(/Turn on the connection above/)).toBeInTheDocument();
        expect(screen.queryByLabelText("Mobile number")).not.toBeInTheDocument();
        view.unmount();
        // A fresh card with a different saved state, not the cached one.
        pageQueryCache.invalidate(["GetSMSChannelStatus"]);
        await renderCard({ ...none, lastError: "not_configured" });
        expect(screen.getByText("Text messages are not available on this server yet.")).toBeInTheDocument();
    });

    it("maps every error code and nothing else", () => {
        const t = i18n.getFixedT("en", "settings");
        expect(describeSMSError("opted_out", t)).toMatch(/replied STOP/);
        expect(describeSMSError("quota", t)).toBe("This number has had all its texts for now.");
        expect(describeSMSError("not_entitled", t)).toBe("Text messages are not turned on for this computer yet. Try again in a moment.");
        expect(describeSMSError("toString", t)).toBe("Something went wrong with text messages.");
        expect(describeSMSError("mystery", t)).toBe("Something went wrong with text messages.");
    });

    it("has every key in both locales and no fallback strings", () => {
        const used = new Set(Array.from(componentSource.matchAll(/"(smsTexts\.[A-Za-z.]+)"/g), (match) => match[1]));
        const lookup = (root: unknown, path: string) =>
            path.split(".").reduce<unknown>((node, key) => (node as Record<string, unknown> | undefined)?.[key], root);
        for (const key of used) {
            expect(typeof lookup(en, key), key).toBe("string");
            expect(typeof lookup(zh, key), key).toBe("string");
        }
        const keys = (node: Record<string, unknown>, prefix = ""): string[] =>
            Object.entries(node).flatMap(([key, value]) =>
                typeof value === "string" ? [prefix + key] : keys(value as Record<string, unknown>, `${prefix}${key}.`),
            );
        expect(keys(zh.smsTexts).sort()).toEqual(keys(en.smsTexts).sort());
        expect(componentSource).not.toMatch(/\bt\(\s*["'`]smsTexts\.[A-Za-z0-9.]+["'`]\s*,\s*["'`]/);
    });
});
