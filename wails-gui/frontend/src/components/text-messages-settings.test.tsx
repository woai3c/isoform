import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import type { TextChannelStatus } from "@/lib/wails-shim-app";
import { pageQueryCache } from "@/lib/query-cache";
import { resetPageQueries } from "@/lib/page-queries";
import en from "@/i18n/locales/en/settings.json";
import zh from "@/i18n/locales/zh-CN/settings.json";
import componentSource from "./text-messages-settings.tsx?raw";
import {
    describeTextError,
    TextMessagesDetails,
    TextMessagesSettings,
    useTextMessages,
} from "./text-messages-settings";

const mocks = vi.hoisted(() => ({
    getStatus: vi.fn(),
    setTextChannel: vi.fn(),
    sendTest: vi.fn(),
    startVerification: vi.fn(),
    confirmVerification: vi.fn(),
    requestPermission: vi.fn(),
    openPermissionSettings: vi.fn(),
}));

// The wailsjs path and the shim resolve to the same module, so one mock covers both imports.
vi.mock("@/lib/wails-shim-app", () => ({
    GetTextChannelStatus: mocks.getStatus,
    SetTextChannel: mocks.setTextChannel,
    SendTestText: mocks.sendTest,
    StartTextVerification: mocks.startVerification,
    ConfirmTextVerification: mocks.confirmVerification,
    RequestPermission: mocks.requestPermission,
    OpenPermissionSettings: mocks.openPermissionSettings,
}));

// The iPhone page shows the everyday card and, under Advanced, the details,
// both driven by one useTextMessages() controller.
function TextMessagesHarness() {
    const controller = useTextMessages();
    return (
        <>
            <TextMessagesSettings controller={controller} />
            <TextMessagesDetails controller={controller} />
        </>
    );
}

const NUMBER_LABEL = "Your phone number";
const ENABLE_LABEL = "Turn on text messages";
const NAMES_LABEL = "Include names in texts";

const unsupportedStatus: TextChannelStatus = {
    verified: false,
    verifying: false,
    supported: false,
    enabled: false,
    number: "",
    includeNames: false,
    consentAccepted: false,
    permissionGranted: false,
    sentToday: 0,
    lastSentAt: "",
    lastError: "",
};

const readyStatus: TextChannelStatus = {
    ...unsupportedStatus,
    supported: true,
    permissionGranted: true,
};

const consentedStatus: TextChannelStatus = {
    ...readyStatus,
    number: "+15550100000",
    consentAccepted: true,
    verified: true,
};

async function renderReady(status: TextChannelStatus = readyStatus) {
    mocks.getStatus.mockResolvedValue(status);
    const view = render(<TextMessagesHarness />);
    await waitFor(() => {
        expect(screen.queryByText("Checking text messages...")).not.toBeInTheDocument();
    });
    return view;
}

describe("TextMessagesSettings component", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        await i18n.changeLanguage("en");
        // The hook reads through the shared query cache; each test starts cold.
        resetPageQueries();
        mocks.setTextChannel.mockRejectedValue(new Error("unexpected save"));
        mocks.sendTest.mockRejectedValue(new Error("unexpected test"));
        mocks.requestPermission.mockResolvedValue(true);
        mocks.openPermissionSettings.mockResolvedValue(undefined);
    });

    it("shows the title and one sentence on a computer without Messages, with no inputs", async () => {
        await renderReady(unsupportedStatus);
        expect(screen.getByRole("heading", { name: "Texts through Messages" })).toBeInTheDocument();
        expect(screen.getByText("Texts are only sent from a computer with Messages.")).toBeInTheDocument();
        expect(screen.queryByLabelText(NUMBER_LABEL)).not.toBeInTheDocument();
        expect(screen.queryByRole("switch")).not.toBeInTheDocument();
        expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });

    it("hints at an invalid number and shows the consent block only for a valid one", async () => {
        await renderReady();
        fireEvent.change(screen.getByLabelText(NUMBER_LABEL), { target: { value: "+1 555" } });
        expect(screen.getByRole("alert")).toHaveTextContent(
            "Start with + and the country code, then 8 to 15 digits.",
        );
        expect(screen.queryByRole("button", { name: "Allow texts" })).not.toBeInTheDocument();

        fireEvent.change(screen.getByLabelText(NUMBER_LABEL), { target: { value: "+1 555 010 0000" } });
        expect(screen.queryByRole("alert")).not.toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Allow texts" })).toBeInTheDocument();
    });

    it("keeps the enable switch off until the number and consent are saved, then enables", async () => {
        await renderReady();
        const enable = screen.getByRole("switch", { name: ENABLE_LABEL });
        expect(enable).toBeDisabled();

        fireEvent.change(screen.getByLabelText(NUMBER_LABEL), { target: { value: "+1 555 010 0000" } });
        expect(screen.getByRole("switch", { name: ENABLE_LABEL })).toBeDisabled();

        mocks.setTextChannel.mockResolvedValueOnce(consentedStatus);
        fireEvent.click(screen.getByRole("button", { name: "Allow texts" }));
        await waitFor(() => {
            expect(screen.getByRole("switch", { name: ENABLE_LABEL })).toBeEnabled();
        });
        expect(mocks.setTextChannel).toHaveBeenLastCalledWith({
            enabled: false,
            number: "+15550100000",
            includeNames: false,
            acceptConsent: true,
        });
        expect(screen.queryByRole("button", { name: "Allow texts" })).not.toBeInTheDocument();

        mocks.setTextChannel.mockResolvedValueOnce({ ...consentedStatus, enabled: true });
        fireEvent.click(screen.getByRole("switch", { name: ENABLE_LABEL }));
        await waitFor(() => {
            expect(screen.getByRole("switch", { name: ENABLE_LABEL })).toBeChecked();
        });
        expect(mocks.setTextChannel).toHaveBeenLastCalledWith({
            enabled: true,
            number: "+15550100000",
            includeNames: false,
            acceptConsent: false,
        });
    });

    it("hides the consent block after Not now and keeps the switch off", async () => {
        await renderReady();
        fireEvent.change(screen.getByLabelText(NUMBER_LABEL), { target: { value: "+1 555 010 0000" } });
        fireEvent.click(screen.getByRole("button", { name: "Not now" }));
        expect(screen.queryByRole("button", { name: "Allow texts" })).not.toBeInTheDocument();
        expect(screen.getByRole("switch", { name: ENABLE_LABEL })).toBeDisabled();
        expect(screen.getByText("Allow texts, then verify your number before turning this on.")).toBeInTheDocument();
        expect(mocks.setTextChannel).not.toHaveBeenCalled();
    });

    it("re-asks for consent when the saved number is changed", async () => {
        await renderReady(consentedStatus);
        fireEvent.change(screen.getByLabelText(NUMBER_LABEL), { target: { value: "+1 555 010 0001" } });
        expect(screen.getByRole("button", { name: "Allow texts" })).toBeInTheDocument();
        expect(screen.getByRole("switch", { name: ENABLE_LABEL })).toBeDisabled();
    });

    it("sends a test text and reports the result with the day count", async () => {
        await renderReady({ ...consentedStatus, enabled: true });
        mocks.sendTest.mockResolvedValueOnce({
            ...consentedStatus,
            enabled: true,
            sentToday: 1,
            lastSentAt: "2026-10-10T09:00:00Z",
        });
        fireEvent.click(screen.getByRole("button", { name: "Send a test text" }));
        expect(await screen.findByText("Test text sent. It should reach Messages shortly.")).toBeInTheDocument();
        expect(screen.getByText("Sent today 1 of 3.")).toBeInTheDocument();
    });

    it("shows a failed test and the stored last error in plain words instead of backend text", async () => {
        await renderReady({ ...consentedStatus, enabled: true });
        // Wails rejects a bound call with the Go error's text.
        mocks.sendTest.mockRejectedValueOnce("the daily text limit is reached");
        mocks.getStatus.mockResolvedValueOnce({
            ...consentedStatus,
            enabled: true,
            sentToday: 3,
            lastError: "Messages could not send the text (error -1728)",
        });
        fireEvent.click(screen.getByRole("button", { name: "Send a test text" }));
        await screen.findByText("Isoform already sent the 3 texts it may send today. It can text again tomorrow.");
        expect(screen.getByText(
            "Isoform already sent the 3 texts it may send today. It can text again tomorrow.",
        )).toBeInTheDocument();
        expect(screen.queryByText(/error -1728/)).not.toBeInTheDocument();
        expect(screen.getAllByRole("alert")).toHaveLength(1);
        expect(screen.getByText("Sent today 3 of 3.")).toBeInTheDocument();
        expect(screen.queryByText(/daily text limit is reached/)).not.toBeInTheDocument();
    });

    it("asks for the test only once the switch is on and the number is saved", async () => {
        await renderReady(consentedStatus);
        expect(screen.getByRole("button", { name: "Send a test text" })).toBeDisabled();
        expect(screen.getByText("Turn on text messages before you send a test.")).toBeInTheDocument();
    });

    it("grants Messages access through the permission request and opens System Settings when it stays off", async () => {
        const missing = { ...consentedStatus, permissionGranted: false };
        await renderReady(missing);
        mocks.getStatus.mockResolvedValueOnce(missing);
        mocks.requestPermission.mockResolvedValueOnce(false);
        fireEvent.click(screen.getByRole("button", { name: "Allow" }));

        await waitFor(() => {
            expect(mocks.openPermissionSettings).toHaveBeenCalledWith("automation_messages");
        });
        expect(mocks.requestPermission).toHaveBeenCalledWith("automation_messages");
        expect(screen.getByRole("alert")).toHaveTextContent(
            "Messages access is still off. Allow Isoform in System Settings, then come back here.",
        );

        mocks.getStatus.mockResolvedValueOnce({ ...missing, permissionGranted: true });
        fireEvent(window, new Event("focus"));
        await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
        expect(mocks.requestPermission).toHaveBeenCalledTimes(1);
    });

    it("shows the Messages access row only while it is missing for texts that are set up", async () => {
        const view = await renderReady(consentedStatus);
        expect(screen.queryByText("Messages access")).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Allow" })).not.toBeInTheDocument();
        view.unmount();

        // A fresh card with a different saved state, not the cached one.
        pageQueryCache.invalidate(["GetTextChannelStatus"]);
        await renderReady({ ...readyStatus, permissionGranted: false });
        expect(screen.queryByRole("button", { name: "Allow" })).not.toBeInTheDocument();
    });

    it("keeps the include-names switch off, with the reason, until text messages are on", async () => {
        await renderReady(consentedStatus);
        expect(screen.getByRole("switch", { name: NAMES_LABEL })).toBeDisabled();
        expect(screen.getByText("Turn on text messages first.")).toBeInTheDocument();
    });

    it("keeps the include-names switch separate from consent", async () => {
        const on = { ...consentedStatus, enabled: true };
        await renderReady(on);
        mocks.setTextChannel.mockResolvedValueOnce({ ...on, includeNames: true });
        fireEvent.click(screen.getByRole("switch", { name: NAMES_LABEL }));
        await waitFor(() => {
            expect(screen.getByRole("switch", { name: NAMES_LABEL })).toBeChecked();
        });
        expect(mocks.setTextChannel).toHaveBeenLastCalledWith({
            enabled: true,
            number: "+15550100000",
            includeNames: true,
            acceptConsent: false,
        });
    });

    it("verifies the saved number before enabling, without asking for Automation during reads or saves", async () => {
        const unverified = { ...consentedStatus, verified: false };
        await renderReady(unverified);
        expect(screen.getByRole("status")).toHaveTextContent("Off. Isoform sends no texts.");
        expect(screen.getByRole("switch", { name: ENABLE_LABEL })).toBeDisabled();
        expect(screen.getByText(en.textMessages.consentBody)).toBeInTheDocument();
        expect(mocks.requestPermission).not.toHaveBeenCalled();

        mocks.startVerification.mockResolvedValueOnce({ ...unverified, verifying: true });
        fireEvent.click(screen.getByRole("button", { name: "Text me a code" }));
        await waitFor(() => expect(screen.getByRole("status")).toHaveTextContent("Verifying +15550100000"));
        fireEvent.change(await screen.findByLabelText("Verification code"), { target: { value: "123456" } });
        mocks.confirmVerification.mockResolvedValueOnce(consentedStatus);
        fireEvent.click(screen.getByRole("button", { name: "Confirm number" }));
        await waitFor(() => expect(screen.getByRole("switch", { name: ENABLE_LABEL })).toBeEnabled());
        expect(mocks.confirmVerification).toHaveBeenCalledWith("123456");
        expect(mocks.requestPermission).not.toHaveBeenCalled();
    });

    it("requests one Automation permission only after the explicit Allow click", async () => {
        const missing = { ...consentedStatus, verified: false, permissionGranted: false };
        await renderReady(missing);
        expect(mocks.requestPermission).not.toHaveBeenCalled();
        expect(screen.queryByRole("button", { name: "Text me a code" })).not.toBeInTheDocument();
        let answer: (granted: boolean) => void = () => {};
        mocks.requestPermission.mockImplementationOnce(() => new Promise<boolean>((resolve) => { answer = resolve; }));
        mocks.getStatus.mockResolvedValueOnce(missing);
        const allow = screen.getByRole("button", { name: "Allow" });
        fireEvent.click(allow);
        fireEvent.click(allow);
        expect(mocks.requestPermission).toHaveBeenCalledTimes(1);
        await act(async () => answer(false));
        expect(await screen.findByRole("alert")).toHaveTextContent(en.textMessages.grantNotGranted);
        window.dispatchEvent(new Event("focus"));
        await waitFor(() => expect(mocks.getStatus).toHaveBeenCalledTimes(3));
        expect(mocks.requestPermission).toHaveBeenCalledTimes(1);
    });

    it("leaves verifying after a spent code and never automatically resends", async () => {
        const unverified = { ...consentedStatus, verified: false };
        await renderReady({ ...unverified, verifying: true });
        fireEvent.change(screen.getByLabelText("Verification code"), { target: { value: "123456" } });
        mocks.confirmVerification.mockRejectedValueOnce("the text verification code expired or was tried too often");
        mocks.getStatus.mockResolvedValueOnce({ ...unverified, verifying: false });
        fireEvent.click(screen.getByRole("button", { name: "Confirm number" }));
        expect(await screen.findByRole("alert")).toHaveTextContent(en.textMessages.errorCodeSpent);
        expect(screen.getByRole("status")).toHaveTextContent(en.textMessages.enableOff);
        expect(screen.queryByLabelText("Verification code")).not.toBeInTheDocument();
        expect(mocks.startVerification).not.toHaveBeenCalled();
    });

    it.each(["en", "zh-CN"])("shows one calm line for each failure in %s", async (lang) => {
        await i18n.changeLanguage(lang);
        const locale = lang === "en" ? en : zh;
        for (const raw of ["Messages is not signed in on this computer", "automation for Messages is not allowed", "verify your phone number before turning on text messages"]) {
            // Each card reads its own lastError, not the previous card's cache.
            pageQueryCache.invalidate(["GetTextChannelStatus"]);
            const view = await renderReady({ ...consentedStatus, enabled: true, lastError: raw });
            expect(screen.getAllByRole("alert")).toHaveLength(1);
            expect(screen.getByRole("alert")).toHaveTextContent(describeTextError(raw, i18n.getFixedT(lang, "settings")));
            expect(mocks.sendTest).not.toHaveBeenCalled();
            expect(mocks.startVerification).not.toHaveBeenCalled();
            expect(screen.getByText(locale.textMessages.consentBody)).toBeInTheDocument();
            view.unmount();
        }
    });

    it("translates every text-channel error the desktop reports", () => {
        const t = i18n.getFixedT("en", "settings");
        const generic = t("textMessages.errorUnknown");
        // errTextChannel* in wails-gui/text_channel.go and messagesFailure in text_channel_darwin.go.
        const backend = [
            "text message settings are unavailable",
            "text messages are off",
            "enter a phone number before turning on text messages",
            "accept the text message terms before turning on text messages",
            "text messages are not available on this computer",
            "automation for Messages is not allowed; turn it on in System Settings > Privacy & Security > Automation",
            "the daily text limit is reached",
            "a text was sent recently",
            "wait a minute before sending another test text",
            "the phone number must be in international form, such as +14155550123",
            "the saved text message settings are invalid",
            "the saved text message state is invalid",
            "Messages did not answer within 10 seconds",
            "Messages could not send the text",
            "Messages could not send the text (error -1728); the SMS service also failed: Messages could not send the text (error -1700)",
        ];
        for (const raw of backend) {
            const shown = describeTextError(raw, t);
            expect(shown, raw).not.toBe(generic);
            expect(shown, raw).not.toBe(raw);
        }
        expect(describeTextError("something new", t)).toBe(generic);
    });

    it("renders the Chinese copy in the zh-CN locale", async () => {
        await act(async () => {
            await i18n.changeLanguage("zh-CN");
        });
        const view = await renderReady();
        try {
            expect(screen.getByRole("heading", { name: "通过「信息」发短信" })).toBeInTheDocument();
            expect(screen.getByLabelText("你的手机号码")).toBeInTheDocument();
        } finally {
            view.unmount();
            await act(async () => {
                await i18n.changeLanguage("en");
            });
        }
    });

    it("keeps every textMessages key used by the component in both locales", () => {
        const used = new Set(
            [...componentSource.matchAll(/["'`](textMessages\.[A-Za-z0-9]+)["'`]/g)].map(
                (match) => match[1].slice("textMessages.".length),
            ),
        );
        const enKeys = Object.keys(en.textMessages).sort();
        const zhKeys = Object.keys(zh.textMessages).sort();
        expect([...used].sort()).toEqual(enKeys);
        expect(zhKeys).toEqual(enKeys);
    });

    it("never passes fallback strings to translation calls", () => {
        expect(componentSource).not.toMatch(/\bt\(\s*["'`]textMessages\.[A-Za-z0-9]+["'`]\s*,\s*["'`]/);
    });
});
