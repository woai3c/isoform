import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import type { YourAgentPageData, YourAgentReservation } from "@/lib/wails-shim-app";
import { formatYourAgentClock } from "@/lib/your-agent";
import { YourAgentPage } from "./your-agent-page";

const mocks = vi.hoisted(() => ({
    getPage: vi.fn(),
    getReservation: vi.fn(),
    check: vi.fn(),
    reserve: vi.fn(),
    verify: vi.fn(),
    resend: vi.fn(),
    choose: vi.fn(),
    cancel: vi.fn(),
    copy: vi.fn(),
    open: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    GetYourAgentPage: mocks.getPage,
    GetYourAgentReservation: mocks.getReservation,
    CheckYourAgentName: mocks.check,
    ReserveYourAgent: mocks.reserve,
    VerifyYourAgentCode: mocks.verify,
    ResendYourAgentCode: mocks.resend,
    ChooseYourAgentPage: mocks.choose,
    CancelYourAgentRequest: mocks.cancel,
    NativeClipboardSetText: mocks.copy,
}));

vi.mock("@/lib/wails-shim-runtime", () => ({ BrowserOpenURL: mocks.open }));

const learned: YourAgentPageData = {
    specialties: [
        { name: "Weekly updates", seen: 22 },
        { name: "Client SOWs", seen: 14 },
        { name: "Hiring plans", seen: 6 },
    ],
    taste: [
        "Lead the weekly update with what changed, not what happened.",
        "Put the price and the date in the first paragraph of an SOW.",
        "Name one owner for every open item.",
    ],
    stillLearning: false,
    nameCandidates: ["alex.rivera", "alexrivera", "a.rivera"],
    reservation: null,
    online: true,
    computed: true,
} as YourAgentPageData;

const sentReservation: YourAgentReservation = {
    name: "alex.rivera",
    email: "alex@example.com",
    reservedAt: "2026-10-27T12:00:00Z",
};

function pageWith(overrides: Partial<YourAgentPageData>): YourAgentPageData {
    return { ...learned, ...overrides } as YourAgentPageData;
}

function nameInput() {
    return screen.getByLabelText(i18n.t("settings:yourAgent.nameLabel")) as HTMLInputElement;
}

function emailInput() {
    return screen.getByLabelText(i18n.t("settings:yourAgent.emailPlaceholder")) as HTMLInputElement;
}

function sendButton() {
    return screen.getByRole("button", { name: i18n.t("settings:yourAgent.sendButton") });
}

/** The reservation read defaults to the cached reservation, online as the page says. */
async function renderWith(
    data: YourAgentPageData,
    read: Promise<{ online: boolean; reservation: YourAgentReservation | null; ranOut?: YourAgentReservation }> = Promise.resolve({
        online: data.online,
        reservation: data.reservation ?? null,
    }),
) {
    mocks.getPage.mockResolvedValue(data);
    mocks.getReservation.mockReturnValue(read);
    render(<YourAgentPage />);
    await screen.findByTestId("your-agent-page");
}

describe("YourAgentPage", () => {
    beforeEach(() => {
        mocks.getPage.mockReset();
        mocks.getReservation.mockReset();
        mocks.check.mockReset();
        mocks.reserve.mockReset();
        mocks.verify.mockReset();
        mocks.resend.mockReset();
        mocks.choose.mockReset();
        mocks.cancel.mockReset();
        mocks.copy.mockReset().mockResolvedValue(undefined);
        mocks.open.mockReset();
    });

    it("copies the page link and opens X with the learned specialties", async () => {
        await renderWith(pageWith({ reservation: sentReservation }));
        await screen.findByTestId("your-agent-sent");
        fireEvent.click(screen.getByRole("button", { name: "Copy your link" }));
        await waitFor(() => expect(mocks.copy).toHaveBeenCalledWith("https://isoform.ai/agents/alex.rivera"));
        fireEvent.keyDown(screen.getByRole("button", { name: "Share…" }), { key: "ArrowDown" });
        const x = await screen.findByRole("menuitem", { name: "Post on X" });
        fireEvent.click(x);
        await waitFor(() => expect(mocks.open).toHaveBeenCalled());
        const url = new URL(mocks.open.mock.calls[0][0]);
        expect(url.origin + url.pathname).toBe("https://x.com/intent/tweet");
        expect(url.searchParams.get("url")).toBe("https://isoform.ai/agents/alex.rivera");
        expect(url.searchParams.get("text")).toContain("Mine will handle Weekly updates, Client SOWs and Hiring plans");
    }, 15000);

    it("copies the LinkedIn post before opening its link", async () => {
        await renderWith(pageWith({ reservation: sentReservation }));
        await screen.findByTestId("your-agent-sent");
        fireEvent.keyDown(screen.getByRole("button", { name: "Share…" }), { key: "ArrowDown" });
        fireEvent.click(await screen.findByRole("menuitem", { name: "Share on LinkedIn" }));
        await waitFor(() => expect(mocks.open).toHaveBeenCalled());
        expect(mocks.copy.mock.calls[0][0]).toContain("Mine will handle Weekly updates, Client SOWs and Hiring plans.");
        expect(mocks.copy.mock.invocationCallOrder[0]).toBeLessThan(mocks.open.mock.invocationCallOrder[0]);
        expect(new URL(mocks.open.mock.calls[0][0]).searchParams.get("url")).toBe("https://isoform.ai/agents/alex.rivera");
        expect(await screen.findByText("Your post is copied. Paste it into LinkedIn.")).toBeTruthy();
    });

    it("omits the work sentence when there are no specialties", async () => {
        await renderWith(pageWith({ reservation: sentReservation, specialties: [] }));
        await screen.findByTestId("your-agent-sent");
        fireEvent.keyDown(screen.getByRole("button", { name: "Share…" }), { key: "ArrowDown" });
        fireEvent.click(await screen.findByRole("menuitem", { name: "Post on X" }));
        const text = new URL(mocks.open.mock.calls[0][0]).searchParams.get("text");
        expect(text).not.toContain("Mine will handle");
        expect(text).toContain("and I get paid for every minute it works.");
    });

    it("shares the Chinese post with the top three specialties", async () => {
        await i18n.changeLanguage("zh-CN");
        try {
            await renderWith(pageWith({ reservation: sentReservation, specialties: [
                { name: "产品规划", seen: 4 }, { name: "招聘计划", seen: 3 }, { name: "写作", seen: 2 }, { name: "不显示", seen: 1 },
            ] }));
            await screen.findByTestId("your-agent-sent");
            fireEvent.keyDown(screen.getByRole("button", { name: "分享…" }), { key: "ArrowDown" });
            fireEvent.click(await screen.findByRole("menuitem", { name: "发到 X" }));
            const text = new URL(mocks.open.mock.calls[0][0]).searchParams.get("text");
            expect(text).toContain("我的会负责产品规划、招聘计划和写作，它每工作一分钟，我都有收入。");
            expect(text).not.toContain("不显示");
        } finally {
            await i18n.changeLanguage("en");
        }
    });

    it("opens with the names of the top specialties, no counts, and both fields empty", async () => {
        await renderWith(learned);
        expect(screen.getByText("Weekly updates")).toBeTruthy();
        expect(screen.getByText("Client SOWs")).toBeTruthy();
        expect(screen.getByText("Hiring plans")).toBeTruthy();
        expect(screen.queryByText(/Seen \d+ times/)).toBeNull();
        expect(screen.queryByText(/见过/)).toBeNull();
        expect(screen.getAllByRole("listitem").length).toBeGreaterThanOrEqual(3);
        expect(screen.getByText(i18n.t("settings:yourAgent.tasteTitle"))).toBeTruthy();
        expect(nameInput().value).toBe("");
        expect(emailInput().value).toBe("");
        expect((sendButton() as HTMLButtonElement).disabled).toBe(true);
        expect(screen.getByText(i18n.t("settings:yourAgent.comingSoon"))).toBeTruthy();
    });

    it("shows the still-learning line, hides the taste, and keeps the request form", async () => {
        await renderWith(pageWith({ specialties: [], taste: [], stillLearning: true }));
        expect(screen.getByText(i18n.t("settings:yourAgent.stillLearning"))).toBeTruthy();
        expect(screen.queryByText(i18n.t("settings:yourAgent.tasteTitle"))).toBeNull();
        expect(nameInput()).toBeTruthy();
    });

    it("keeps Send request disabled until both fields are valid", async () => {
        await renderWith(learned);
        fireEvent.change(nameInput(), { target: { value: "alex.rivera" } });
        expect((sendButton() as HTMLButtonElement).disabled).toBe(true);
        fireEvent.change(emailInput(), { target: { value: "alex" } });
        expect((sendButton() as HTMLButtonElement).disabled).toBe(true);
        expect(screen.getByText(i18n.t("settings:yourAgent.invalidEmail"))).toBeTruthy();
        fireEvent.change(emailInput(), { target: { value: "alex@example.com" } });
        expect(screen.queryByText(i18n.t("settings:yourAgent.invalidEmail"))).toBeNull();
        expect((sendButton() as HTMLButtonElement).disabled).toBe(false);
    });

    it("checks the name when the person leaves the field, not while typing", async () => {
        mocks.check.mockResolvedValue({ name: "alex.rivera", available: true, reason: "" });
        await renderWith(learned);
        fireEvent.change(nameInput(), { target: { value: "alex.rivera" } });
        expect(mocks.check).not.toHaveBeenCalled();
        fireEvent.blur(nameInput());
        await waitFor(() => expect(mocks.check).toHaveBeenCalledWith("alex.rivera"));
    });

    it("shows Taken with the suggestion, and tapping it fills the field", async () => {
        mocks.check.mockResolvedValue({ name: "alex.rivera", available: false, reason: "taken", suggestion: "alexrivera" });
        await renderWith(learned);
        fireEvent.change(nameInput(), { target: { value: "alex.rivera" } });
        fireEvent.blur(nameInput());
        const suggestion = await screen.findByRole("button", { name: "alexrivera" });
        expect(screen.getByText(/Taken\. Try/)).toBeTruthy();
        fireEvent.click(suggestion);
        expect(nameInput().value).toBe("alexrivera");
    });

    it("refuses a name kept for Isoform without a server call", async () => {
        await renderWith(learned);
        fireEvent.change(nameInput(), { target: { value: "support" } });
        fireEvent.blur(nameInput());
        expect(screen.getByText(i18n.t("settings:yourAgent.notAllowed"))).toBeTruthy();
        expect(mocks.check).not.toHaveBeenCalled();
    });

    it("sends the request and shows Request sent with Change", async () => {
        mocks.reserve.mockResolvedValue({ status: "reserved", reservation: sentReservation });
        await renderWith(learned);
        fireEvent.change(nameInput(), { target: { value: "alex.rivera" } });
        fireEvent.change(emailInput(), { target: { value: "alex@example.com" } });
        fireEvent.click(sendButton());
        await screen.findByTestId("your-agent-sent");
        expect(mocks.reserve).toHaveBeenCalledWith("alex.rivera", "alex@example.com");
        expect(screen.getByText("isoform.ai/agents/alex.rivera is yours.")).toBeTruthy();
        expect(screen.getByText(/alex@example\.com/)).toBeTruthy();
        fireEvent.click(screen.getByRole("button", { name: i18n.t("settings:yourAgent.changeButton") }));
        expect(nameInput().value).toBe("alex.rivera");
        expect(emailInput().value).toBe("alex@example.com");
    });

    it("opens with a request already sent", async () => {
        await renderWith(pageWith({ reservation: sentReservation }));
        expect(screen.getByTestId("your-agent-sent")).toBeTruthy();
        expect(screen.queryByLabelText(i18n.t("settings:yourAgent.nameLabel"))).toBeNull();
    });

    it("shows the taken suggestion when the server refuses on Send", async () => {
        mocks.reserve.mockResolvedValue({ status: "taken", suggestion: "alexrivera" });
        await renderWith(learned);
        fireEvent.change(nameInput(), { target: { value: "alex.rivera" } });
        fireEvent.change(emailInput(), { target: { value: "alex@example.com" } });
        fireEvent.click(sendButton());
        await screen.findByRole("button", { name: "alexrivera" });
    });

    it("dims Send and shows the offline line when the server is unreachable", async () => {
        await renderWith(pageWith({ online: false }));
        fireEvent.change(nameInput(), { target: { value: "alex.rivera" } });
        fireEvent.change(emailInput(), { target: { value: "alex@example.com" } });
        expect((sendButton() as HTMLButtonElement).disabled).toBe(true);
        expect(screen.getByText(i18n.t("settings:yourAgent.offline"))).toBeTruthy();
    });

    it("first paint shows the cached data while the reservation read hangs", async () => {
        // The read never settles: the cached page must already be on screen.
        await renderWith(learned, new Promise(() => {}));
        expect(screen.getByText("Weekly updates")).toBeTruthy();
        expect(screen.getByText(i18n.t("settings:yourAgent.tasteTitle"))).toBeTruthy();
        expect((sendButton() as HTMLButtonElement).disabled).toBe(true);
        expect(screen.queryByText(i18n.t("settings:yourAgent.offline"))).toBeNull();
    });

    it("updates in place when the reservation read returns a request already sent", async () => {
        let resolveRead: (value: { online: boolean; reservation: YourAgentReservation | null }) => void = () => {};
        const read = new Promise<{ online: boolean; reservation: YourAgentReservation | null }>((resolve) => {
            resolveRead = resolve;
        });
        await renderWith(learned, read);
        expect(screen.queryByTestId("your-agent-sent")).toBeNull();
        resolveRead({ online: true, reservation: sentReservation });
        await screen.findByTestId("your-agent-sent");
        expect(screen.getByText("isoform.ai/agents/alex.rivera is yours.")).toBeTruthy();
    });

    it("keeps the cached request and shows the offline line when the read fails", async () => {
        await renderWith(pageWith({ reservation: sentReservation }), Promise.reject(new Error("network down")));
        await screen.findByText(i18n.t("settings:yourAgent.offline"));
        expect(screen.getByTestId("your-agent-sent")).toBeTruthy();
    });

    it("shows the offline line when the read reports the server unreachable", async () => {
        await renderWith(pageWith({ reservation: sentReservation }), Promise.resolve({ online: false, reservation: sentReservation }));
        await screen.findByText(i18n.t("settings:yourAgent.offline"));
        expect(screen.getByTestId("your-agent-sent")).toBeTruthy();
    });

    it("shows wordless placeholder rows on the first open, never a loading line or Still learning", async () => {
        await renderWith(pageWith({ computed: false, specialties: [], taste: [], stillLearning: false }));
        const placeholder = screen.getByTestId("your-agent-specialties-placeholder");
        expect(placeholder.textContent).toBe("");
        expect(placeholder.children.length).toBe(3);
        expect(screen.getByTestId("your-agent-taste-placeholder").textContent).toBe("");
        expect(screen.queryByText(i18n.t("settings:yourAgent.stillLearning"))).toBeNull();
        expect(screen.queryByText(/loading/i)).toBeNull();
        expect(screen.getByText(i18n.t("settings:yourAgent.specialtiesTitle"))).toBeTruthy();
        expect(screen.getByText(i18n.t("settings:yourAgent.tasteTitle"))).toBeTruthy();
    });

    it("replaces the placeholders in place and never shows Still learning on the way", async () => {
        mocks.getPage.mockResolvedValueOnce(pageWith({ computed: false, specialties: [], taste: [], stillLearning: false }));
        mocks.getPage.mockResolvedValue(learned);
        mocks.getReservation.mockResolvedValue({ online: true, reservation: null });
        render(<YourAgentPage />);
        await waitFor(() => expect(screen.getByText("Weekly updates")).toBeTruthy(), { timeout: 3000 });
        expect(screen.queryByText(i18n.t("settings:yourAgent.stillLearning"))).toBeNull();
        expect(screen.queryByTestId("your-agent-specialties-placeholder")).toBeNull();
    });

    it("keeps Still learning when the Mac's copy says so", async () => {
        await renderWith(pageWith({ specialties: [], taste: [], stillLearning: true, computed: true }));
        expect(screen.getByText(i18n.t("settings:yourAgent.stillLearning"))).toBeTruthy();
    });

    it("cleans the name as the person types", async () => {
        await renderWith(learned);
        fireEvent.change(nameInput(), { target: { value: "Alex Rivera!" } });
        expect(nameInput().value).toBe("alexrivera");
        fireEvent.change(nameInput(), { target: { value: "a..b" } });
        expect(nameInput().value).toBe("a.b");
    });

    it("shows the rules line only on blur, for length or a dot at either end", async () => {
        mocks.check.mockResolvedValue({ name: "alex.rivera", available: true, reason: "" });
        await renderWith(learned);
        const rules = i18n.t("settings:yourAgent.invalidName");
        fireEvent.change(nameInput(), { target: { value: ".abc" } });
        expect(screen.queryByText(rules)).toBeNull();
        fireEvent.blur(nameInput());
        expect(screen.getByText(rules)).toBeTruthy();
        expect(mocks.check).not.toHaveBeenCalled();
        fireEvent.change(nameInput(), { target: { value: "ab" } });
        fireEvent.blur(nameInput());
        expect(screen.getByText(rules)).toBeTruthy();
        fireEvent.change(nameInput(), { target: { value: "alex.rivera" } });
        fireEvent.blur(nameInput());
        expect(screen.queryByText(rules)).toBeNull();
    });

    it("shows the new send-failed line with the Email Bo line right under it, and keeps Send enabled", async () => {
        mocks.reserve.mockResolvedValue({ status: "token" });
        await renderWith(learned);
        fireEvent.change(nameInput(), { target: { value: "alex.rivera" } });
        fireEvent.change(emailInput(), { target: { value: "alex@example.com" } });
        fireEvent.click(sendButton());
        const failed = await screen.findByText(i18n.t("settings:yourAgent.sendFailed"));
        expect(failed.nextElementSibling?.textContent).toContain("bo@isoform.ai");
        expect((sendButton() as HTMLButtonElement).disabled).toBe(false);
    });

    it("names the time in the too-many-tries line and dims Send until then", async () => {
        const retryAt = new Date(Date.now() + 60 * 60 * 1000);
        mocks.reserve.mockResolvedValue({ status: "rate", retryAt: retryAt.toISOString() });
        await renderWith(learned);
        fireEvent.change(nameInput(), { target: { value: "alex.rivera" } });
        fireEvent.change(emailInput(), { target: { value: "alex@example.com" } });
        fireEvent.click(sendButton());
        const time = formatYourAgentClock(retryAt, "en");
        await screen.findByText(`Too many tries. Try again at ${time}.`);
        expect((sendButton() as HTMLButtonElement).disabled).toBe(true);
    });

    it("screen readers hear the prefix on the name field and the email field's label", async () => {
        await renderWith(learned);
        expect(screen.getByLabelText("Page name, after isoform.ai/agents/")).toBeTruthy();
        expect(screen.getByLabelText("Your email")).toBeTruthy();
    });

    it("shows the Request sent card in three parts, with the lead body line", async () => {
        await renderWith(pageWith({ reservation: sentReservation }));
        expect(screen.getByText("isoform.ai/agents/alex.rivera is yours.")).toBeTruthy();
        expect(screen.getByText(/We'll email alex@example.com when it opens/)).toBeTruthy();
        expect(screen.getByRole("button", { name: "Change" })).toBeTruthy();
        expect(screen.getByText(i18n.t("settings:yourAgent.leadRest"))).toBeTruthy();
    });
    const pending = { ...sentReservation, status: "pending", expiresAt: "2099-01-01T00:00:00Z" };
    const choice = { ...pending, status: "choice", oldName: "old.name" };

    it("shows one code input and verifies on the sixth digit, including paste", async () => {
        mocks.verify.mockResolvedValue({ status: "reserved", reservation: { ...sentReservation, status: "verified", place: 7 } });
        await renderWith(pageWith({ reservation: pending }));
        const input = screen.getByLabelText("6-digit code") as HTMLInputElement;
        expect(input.autocomplete).toBe("one-time-code");
        fireEvent.change(input, { target: { value: "12a34" } });
        expect(input.value).toBe("1234");
        expect(mocks.verify).not.toHaveBeenCalled();
        fireEvent.change(input, { target: { value: "123456" } });
        await screen.findByText("You're #7 in line.");
        expect(mocks.verify).toHaveBeenCalledWith("123456");
    });

    it("clears and refocuses a wrong code, and displays expiry", async () => {
        mocks.verify.mockResolvedValueOnce({ status: "wrong_code" }).mockResolvedValueOnce({ status: "expired" });
        await renderWith(pageWith({ reservation: pending }));
        const input = screen.getByLabelText("6-digit code") as HTMLInputElement;
        fireEvent.change(input, { target: { value: "123456" } });
        await screen.findByText("That code didn't match. Try again.");
        expect(input.value).toBe("");
        await waitFor(() => expect(document.activeElement).toBe(input));
        fireEvent.change(input, { target: { value: "654321" } });
        await screen.findByText("That code has expired. Send it again.");
    });

    it("shows the retry time and disables code entry after rate limiting", async () => {
        const retryAt = new Date(Date.now() + 60 * 60 * 1000);
        mocks.verify.mockResolvedValue({ status: "rate", retryAt: retryAt.toISOString() });
        await renderWith(pageWith({ reservation: pending }));
        const input = screen.getByLabelText("6-digit code") as HTMLInputElement;
        fireEvent.change(input, { target: { value: "123456" } });
        await screen.findByText(`Too many tries. Try again at ${formatYourAgentClock(retryAt, "en")}.`);
        expect(input.disabled).toBe(true);
    });

    it("shows Sent for 1.6 seconds after resending", async () => {
        mocks.resend.mockResolvedValue({ status: "pending", reservation: pending });
        await renderWith(pageWith({ reservation: pending }));
        fireEvent.click(screen.getByRole("button", { name: "Send it again" }));
        await screen.findByRole("button", { name: "Sent" });
        await waitFor(() => expect(screen.getByRole("button", { name: "Send it again" })).toBeTruthy(), { timeout: 2500 });
    });

    it("reserves the same details again when resend says none, and handles taken", async () => {
        mocks.resend.mockResolvedValue({ status: "none" });
        mocks.reserve.mockResolvedValue({ status: "taken", suggestion: "alexrivera" });
        await renderWith(pageWith({ reservation: pending }));
        fireEvent.click(screen.getByRole("button", { name: "Send it again" }));
        await screen.findByRole("button", { name: "alexrivera" });
        expect(mocks.reserve).toHaveBeenCalledWith(pending.name, pending.email);
        expect(nameInput().value).toBe(pending.name);
        expect(emailInput().value).toBe(pending.email);
    });

    it("returns to the code step after none re-reserves successfully", async () => {
        mocks.resend.mockResolvedValue({ status: "none" });
        mocks.reserve.mockResolvedValue({ status: "pending", reservation: pending });
        await renderWith(pageWith({ reservation: pending }));
        fireEvent.click(screen.getByRole("button", { name: "Send it again" }));
        await screen.findByRole("button", { name: "Sent" });
        expect(mocks.reserve).toHaveBeenCalledWith(pending.name, pending.email);
        expect(screen.getByLabelText("6-digit code")).toBeTruthy();
    });

    it("cancels the hold and refills the form", async () => {
        mocks.cancel.mockResolvedValue({ status: "released" });
        await renderWith(pageWith({ reservation: pending }));
        fireEvent.click(screen.getByRole("button", { name: "Change the name or email" }));
        await screen.findByLabelText(i18n.t("settings:yourAgent.nameLabel"));
        expect(nameInput().value).toBe(pending.name);
        expect(emailInput().value).toBe(pending.email);
        expect(mocks.cancel).toHaveBeenCalledOnce();
    });

    it.each([true, false])("offers choice and sends keep=%s", async (keep) => {
        const result = { ...sentReservation, name: keep ? "old.name" : sentReservation.name, status: "verified" };
        mocks.choose.mockResolvedValue({ status: "reserved", reservation: result });
        await renderWith(pageWith({ reservation: choice }));
        expect(screen.getByText("alex@example.com already has isoform.ai/agents/old.name.")).toBeTruthy();
        fireEvent.click(screen.getByRole("button", { name: keep ? "Keep old.name" : "Switch to alex.rivera" }));
        await screen.findByText(`isoform.ai/agents/${result.name} is yours.`);
        expect(mocks.choose).toHaveBeenCalledWith(keep);
    });

    it("shows place and Copied for 1.6 seconds", async () => {
        await renderWith(pageWith({ reservation: { ...sentReservation, status: "verified", place: 7 } }));
        expect(screen.getByText("You're #7 in line.")).toBeTruthy();
        expect(screen.getByText("Move up the line")).toBeTruthy();
        fireEvent.click(screen.getByRole("button", { name: "Copy your link" }));
        await screen.findByRole("button", { name: "Copied" });
        await waitFor(() => expect(screen.getByRole("button", { name: "Copy your link" })).toBeTruthy(), { timeout: 2500 });
    });

    it("sends a code for an older request", async () => {
        mocks.resend.mockResolvedValue({ status: "pending", reservation: { ...pending, status: "unconfirmed" } });
        await renderWith(pageWith({ reservation: { ...sentReservation, status: "unconfirmed" } }));
        expect(screen.getByText("Confirm it's you")).toBeTruthy();
        fireEvent.click(screen.getByRole("button", { name: "Send code" }));
        await screen.findByLabelText("6-digit code");
        expect(mocks.resend).toHaveBeenCalledOnce();
    });

    it("refills and checks the name after a hold ran out", async () => {
        mocks.check.mockResolvedValue({ available: false, reason: "taken", suggestion: "alexrivera" });
        await renderWith(pageWith({ reservation: pending }), Promise.resolve({ online: true, reservation: null, ranOut: pending }));
        await screen.findByText("The 10 minutes ran out, so the name was let go. Send the request again.");
        await screen.findByRole("button", { name: "alexrivera" });
        expect(nameInput().value).toBe(pending.name);
        expect(emailInput().value).toBe(pending.email);
        expect(mocks.check).toHaveBeenCalledWith(pending.name);
    });

    it("refills an expired change while the verified original remains", async () => {
        mocks.check.mockResolvedValue({ available: true, reason: "" });
        const original = { ...sentReservation, name: "old.name", status: "verified" };
        await renderWith(pageWith({ reservation: pending }), Promise.resolve({ online: true, reservation: original, ranOut: pending }));
        await screen.findByText("The 10 minutes ran out, so the name was let go. Send the request again.");
        expect(nameInput().value).toBe(pending.name);
        expect(emailInput().value).toBe(pending.email);
    });

});
