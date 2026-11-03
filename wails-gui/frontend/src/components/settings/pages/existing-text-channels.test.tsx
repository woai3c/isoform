import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import type { SMSChannelStatus, TextChannelStatus } from "@/lib/wails-shim-app";
import { resetPageQueries } from "@/lib/page-queries";
import { ExistingTextChannels } from "./existing-text-channels";

const bindings = vi.hoisted(() => ({
    getText: vi.fn(), getSMS: vi.fn(), setText: vi.fn(), stopSMS: vi.fn(), sendTest: vi.fn(),
    startText: vi.fn(), confirmText: vi.fn(), startSMS: vi.fn(), confirmSMS: vi.fn(),
}));
vi.mock("@/lib/wails-shim-app", () => ({
    GetTextChannelStatus: bindings.getText, GetSMSChannelStatus: bindings.getSMS,
    SetTextChannel: bindings.setText, StopSMSChannel: bindings.stopSMS, SendTestText: bindings.sendTest,
    StartTextVerification: bindings.startText, ConfirmTextVerification: bindings.confirmText,
    StartSMSVerification: bindings.startSMS, ConfirmSMSVerification: bindings.confirmSMS,
}));
const emptyText: TextChannelStatus = {
    supported: true, enabled: false, number: "", includeNames: false, consentAccepted: false,
    permissionGranted: true, sentToday: 0, lastSentAt: "", lastError: "", verified: false, verifying: false,
};
const verifiedText: TextChannelStatus = {
    ...emptyText, enabled: true, number: "+15550100000", includeNames: true, consentAccepted: true, verified: true,
};
const emptySMS: SMSChannelStatus = {
    available: true, state: "none", number: "", pendingNumber: "", codeExpiresAt: "",
    sentToday: 0, sentThisMonth: 0, dailyCap: 10, monthlyCap: 200, lastError: "",
};

async function renderPage() {
    render(<ExistingTextChannels />);
    await waitFor(() => expect(screen.queryAllByText(i18n.t("settings:textMessages.loading"))).toHaveLength(0));
}

describe("Existing text channels on Overview", () => {
    beforeEach(async () => {
        vi.resetAllMocks();
        await i18n.changeLanguage("en");
        // Both cards read through the shared query cache; each test starts cold.
        resetPageQueries();
        bindings.getText.mockResolvedValue(emptyText);
        bindings.getSMS.mockResolvedValue(emptySMS);
    });

    it.each(["en", "zh-CN"])("offers no dead setup controls in %s", async (language) => {
        await i18n.changeLanguage(language);
        await renderPage();
        expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
        expect(screen.queryByRole("switch")).not.toBeInTheDocument();
        expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });

    it.each(["en", "zh-CN"])("keeps an active relay subscription reachable for unsubscribe in %s", async (language) => {
        await i18n.changeLanguage(language);
        bindings.getSMS.mockResolvedValue({ ...emptySMS, state: "active", number: "+14155550123",
            pendingNumber: "+14155550116", codeExpiresAt: "2099-01-01T00:00:00Z" });
        bindings.stopSMS.mockResolvedValue(emptySMS);
        await renderPage();
        expect(screen.getByText(i18n.t("settings:smsTexts.activeOn", { number: "+14155550123" }))).toBeInTheDocument();
        expect(screen.queryByText(i18n.t("settings:smsTexts.changeNumber"))).not.toBeInTheDocument();
        expect(screen.queryByLabelText(i18n.t("settings:smsTexts.codeLabel"))).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: i18n.t("settings:smsTexts.stop") }));
        await waitFor(() => expect(bindings.stopSMS).toHaveBeenCalledTimes(1));
        await waitFor(() => expect(screen.queryByRole("button", { name: i18n.t("settings:smsTexts.stop") })).not.toBeInTheDocument());
        expect(bindings.startSMS).not.toHaveBeenCalled();
    });

    it("keeps relay unsubscribe visible while the connection is unavailable", async () => {
        bindings.getSMS.mockResolvedValue({ ...emptySMS, available: false, state: "active", number: "+14155550123" });
        bindings.stopSMS.mockRejectedValue(new Error("offline"));
        await renderPage();
        fireEvent.click(screen.getByRole("button", { name: "Stop texts" }));
        expect(await screen.findByRole("alert")).toHaveTextContent(i18n.t("settings:smsTexts.errors.offline"));
        expect(screen.getByRole("button", { name: "Stop texts" })).toBeEnabled();
    });

    it.each([true, false])("allows withdrawing names consent while native texts enabled=%s", async (enabled) => {
        const saved = { ...verifiedText, enabled };
        bindings.getText.mockResolvedValue(saved);
        bindings.setText.mockResolvedValue({ ...saved, includeNames: false });
        await renderPage();
        const names = screen.getByRole("switch", { name: "Include names in texts" });
        expect(names).toBeChecked();
        expect(names).toBeEnabled();
        fireEvent.click(names);
        await waitFor(() => expect(bindings.setText).toHaveBeenCalledWith({
            enabled, number: saved.number, includeNames: false, acceptConsent: false,
        }));
        await waitFor(() => expect(names).not.toBeChecked());
    });

    it("shows the real native number and lets an upgraded user turn delivery off", async () => {
        bindings.getText.mockResolvedValue(verifiedText);
        bindings.setText.mockResolvedValue({ ...verifiedText, enabled: false });
        await renderPage();
        expect(screen.getByText(i18n.t("settings:textMessages.enableOn", { number: verifiedText.number }))).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Turn off" }));
        await waitFor(() => expect(bindings.setText).toHaveBeenCalledWith({
            enabled: false, number: verifiedText.number, includeNames: true, acceptConsent: false,
        }));
        expect(await screen.findByText(i18n.t("settings:comingSoon.nativeOff", { number: verifiedText.number }))).toBeInTheDocument();
    });

    it("lets a verified native channel recover from a persistent send failure through a test", async () => {
        bindings.getText.mockResolvedValue({ ...verifiedText, lastError: "texts are paused" });
        bindings.sendTest.mockResolvedValue(verifiedText);
        await renderPage();
        expect(screen.getByText(i18n.t("settings:comingSoon.nativePaused", { number: verifiedText.number }))).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Send a test text" }));
        expect(await screen.findByText(i18n.t("settings:textMessages.testSent"))).toBeInTheDocument();
        expect(bindings.sendTest).toHaveBeenCalledTimes(1);
        expect(screen.getByText(i18n.t("settings:textMessages.enableOn", { number: verifiedText.number }))).toBeInTheDocument();
    });

    it("keeps native reverification and confirmation mounted for an existing number", async () => {
        bindings.getText.mockResolvedValue(verifiedText);
        bindings.startText.mockResolvedValue({ ...verifiedText, verified: false, verifying: true });
        bindings.confirmText.mockResolvedValue(verifiedText);
        await renderPage();
        expect(screen.queryByRole("button", { name: "Text me a code" })).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Verify this number" }));
        fireEvent.change(await screen.findByLabelText("Verification code"), { target: { value: "123456" } });
        fireEvent.click(screen.getByRole("button", { name: "Confirm number" }));
        await waitFor(() => expect(bindings.confirmText).toHaveBeenCalledWith("123456"));
        await waitFor(() => expect(screen.queryByLabelText("Verification code")).not.toBeInTheDocument());
        expect(bindings.startText).toHaveBeenCalledTimes(1);
    });

    it("offers a retry instead of hiding saved management after a read failure", async () => {
        bindings.getText.mockRejectedValueOnce(new Error("unavailable")).mockResolvedValue(verifiedText);
        await renderPage();
        fireEvent.click(screen.getByRole("button", { name: "Try again" }));
        expect(await screen.findByRole("button", { name: "Turn off" })).toBeEnabled();
    });
});
