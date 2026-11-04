import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import feishuSource from "./feishu-setup.tsx?raw";
import botSource from "./bot-setups.tsx?raw";
import { DiscordStandaloneSetup, SlackStandaloneSetup, TelegramStandaloneSetup } from "./bot-setups";
import { FeishuSettings } from "./feishu-setup";
import { WeChatSettings } from "./wechat-setup";
import { SaveSlackLocalCredentials, TestSlackConnection } from "../../../../../wailsjs/go/main/App";

const mocks = vi.hoisted(() => ({
    feishuStatus: vi.fn(),
    feishuReceiver: vi.fn(),
    feishuChats: vi.fn(),
    feishuFilter: vi.fn(),
    disconnectFeishu: vi.fn(),
    telegramStatus: vi.fn(),
    disconnectTelegram: vi.fn(),
    telegramArchived: vi.fn(),
    discardTelegramArchived: vi.fn(),
    wechatStatus: vi.fn(),
    startWeChatLogin: vi.fn(),
    pollWeChatLogin: vi.fn(),
    disconnectWeChat: vi.fn(),
    slackStatus: vi.fn(),
    discordStatus: vi.fn(),
}));

vi.mock("../../../../../wailsjs/go/main/App", () => ({
    // Feishu
    DisconnectFeishu: mocks.disconnectFeishu,
    GetArchivedFeishuApp: vi.fn(() => Promise.resolve(null)),
    ReuseArchivedFeishuApp: vi.fn(),
    GetLarkAccountStatus: vi.fn(() => Promise.resolve({ configured: false })),
    LarkCliUserLoginStart: vi.fn(),
    LarkCliUserLoginPoll: vi.fn(),
    GetFeishuChats: mocks.feishuChats,
    GetFeishuReceiverStatus: mocks.feishuReceiver,
    GetFeishuScanChatFilter: mocks.feishuFilter,
    GetFeishuStatus: mocks.feishuStatus,
    RefreshFeishuPermissions: vi.fn(),
    SetFeishuScanChatFilter: vi.fn(() => Promise.resolve()),
    StartFeishuDaemon: vi.fn(),
    RestartFeishuReceiver: vi.fn(),
    StartFeishuRegistration: vi.fn(),
    PollFeishuRegistration: vi.fn(),
    // Slack, Discord, Telegram
    DisconnectSlack: vi.fn(),
    GetSlackStatus: mocks.slackStatus,
    GetDiscordStatus: mocks.discordStatus,
    UnbindDiscordAccount: vi.fn(),
    GetTelegramStatus: mocks.telegramStatus,
    DisconnectTelegram: mocks.disconnectTelegram,
    TestTelegramConnection: vi.fn(),
    SaveTelegramLocalCredentials: vi.fn(),
    TestSlackConnection: vi.fn(),
    SaveSlackLocalCredentials: vi.fn(),
    TestDiscordConnection: vi.fn(),
    SaveDiscordLocalCredentials: vi.fn(),
    GetDiscordInviteURL: vi.fn(),
    GetTelegramArchived: mocks.telegramArchived,
    ReuseTelegramLocal: vi.fn(),
    DiscardTelegramArchived: mocks.discardTelegramArchived,
    GetSlackArchived: vi.fn(() => Promise.resolve({ has: false, label: "" })),
    ReuseSlackLocal: vi.fn(),
    DiscardSlackArchived: vi.fn(),
    GetDiscordArchived: vi.fn(() => Promise.resolve({ has: false, label: "" })),
    ReuseDiscordLocal: vi.fn(),
    DiscardDiscordArchived: vi.fn(),
    // WeChat
    GetWeChatStatus: mocks.wechatStatus,
    ConnectWeChat: vi.fn(),
    DisconnectWeChat: mocks.disconnectWeChat,
    StartWeChatLogin: mocks.startWeChatLogin,
    PollWeChatLogin: mocks.pollWeChatLogin,
}));

vi.mock("../../../../../wailsjs/runtime/runtime", () => ({
    BrowserOpenURL: vi.fn(),
    EventsOn: vi.fn(() => () => {}),
}));

vi.mock("@/lib/notify", () => ({
    notifyImportant: vi.fn(),
}));

vi.mock("sonner", () => ({
    toast: { error: vi.fn(), success: vi.fn(), info: vi.fn() },
}));

describe("chat app setups", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        window.localStorage.clear();
        await i18n.changeLanguage("en");
        mocks.feishuStatus.mockResolvedValue({
            connected: true,
            app_id: "cli_123",
            bot_name: "Isoform Bot",
            daemon_online: true,
            permission_ready: false,
        });
        mocks.feishuReceiver.mockResolvedValue({
            configured: true,
            started: true,
            connected: true,
            status: "connected",
        });
        mocks.feishuChats.mockResolvedValue({
            chats: [
                { chat_id: "oc_1", name: "Team", chat_type: "group" },
                { chat_id: "oc_2", name: "Ana", chat_type: "p2p" },
            ],
        });
        mocks.feishuFilter.mockResolvedValue([]);
        mocks.disconnectFeishu.mockResolvedValue(undefined);
        mocks.telegramStatus.mockResolvedValue({
            connected: true,
            telegram_user_name: "isoform_bot",
            daemon_online: true,
        });
        mocks.disconnectTelegram.mockResolvedValue(undefined);
        mocks.telegramArchived.mockResolvedValue({ has: false, label: "" });
        mocks.discardTelegramArchived.mockResolvedValue(undefined);
    });

    it("keeps the Feishu app details, full access and chats under Advanced", async () => {
        render(<FeishuSettings brand="feishu" onStatusChange={vi.fn()} />);

        expect(await screen.findByText("Isoform Bot")).toBeInTheDocument();
        expect(screen.getByText(/turn on full access under Advanced/)).toBeInTheDocument();
        expect(screen.queryByText("cli_123")).not.toBeInTheDocument();
        expect(screen.queryByText("Chats Isoform learns from")).not.toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: /Advanced/ }));
        expect(screen.getByText("Your Feishu app")).toBeInTheDocument();
        expect(screen.getByText("cli_123")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: /Copy full permission JSON/ })).toBeInTheDocument();
        expect(screen.getByText("Chats Isoform learns from")).toBeInTheDocument();
        expect(await screen.findByText("2 of 2 selected")).toBeInTheDocument();
        expect(screen.getByText("Group")).toBeInTheDocument();
        expect(screen.getByText("DM")).toBeInTheDocument();
    });

    it("translates the Feishu chats card", async () => {
        await i18n.changeLanguage("zh-CN");
        window.localStorage.setItem("yansu.settings.advanced.chat-apps", "1");
        render(<FeishuSettings brand="feishu" onStatusChange={vi.fn()} />);

        expect(await screen.findByText("Isoform 从哪些聊天中学习")).toBeInTheDocument();
        expect(await screen.findByText("已选 2 / 2")).toBeInTheDocument();
        expect(screen.getByText("群组")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "全选" })).toBeInTheDocument();
    });

    it("leaves the Chats card out for Lark", async () => {
        window.localStorage.setItem("yansu.settings.advanced.chat-apps", "1");
        render(<FeishuSettings brand="lark" onStatusChange={vi.fn()} />);

        expect(await screen.findByText("Your Lark app")).toBeInTheDocument();
        expect(screen.queryByText("Chats Isoform learns from")).not.toBeInTheDocument();
        expect(mocks.feishuChats).not.toHaveBeenCalled();
    });

    it("asks before disconnecting Feishu", async () => {
        render(<FeishuSettings brand="feishu" onStatusChange={vi.fn()} />);
        fireEvent.click(await screen.findByRole("button", { name: "Disconnect" }));

        const dialog = await screen.findByRole("dialog");
        expect(within(dialog).getByText("Disconnect Feishu?")).toBeInTheDocument();
        expect(mocks.disconnectFeishu).not.toHaveBeenCalled();
        fireEvent.click(within(dialog).getByRole("button", { name: "Disconnect" }));
        await waitFor(() => expect(mocks.disconnectFeishu).toHaveBeenCalledWith("feishu"));
    });

    it("asks before disconnecting a bot", async () => {
        render(<TelegramStandaloneSetup onStatusChange={vi.fn()} />);
        fireEvent.click(await screen.findByRole("button", { name: "Disconnect" }));

        const dialog = await screen.findByRole("dialog");
        fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
        expect(mocks.disconnectTelegram).not.toHaveBeenCalled();

        fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));
        fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Disconnect" }));
        await waitFor(() => expect(mocks.disconnectTelegram).toHaveBeenCalled());
    });

    it("reports whether a bot is set up separately from whether it is online", async () => {
        mocks.telegramStatus.mockResolvedValue({ connected: true, telegram_user_name: "isoform_bot", daemon_online: false });
        const onStatusChange = vi.fn();
        render(<TelegramStandaloneSetup onStatusChange={onStatusChange} />);

        await waitFor(() => expect(onStatusChange).toHaveBeenCalledWith({ configured: true, online: false }));
    });

    it("shows the pairing code until the bot is paired", async () => {
        mocks.telegramStatus.mockResolvedValue({
            connected: true,
            telegram_user_name: "isoform_bot",
            daemon_online: true,
            paired: false,
            pairing_code: "K7QF-M2XP",
        });
        render(<TelegramStandaloneSetup onStatusChange={vi.fn()} />);

        expect(await screen.findByText("K7QF-M2XP")).toBeInTheDocument();
        expect(screen.getByText("Finish pairing")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Open bot and pair" })).toBeInTheDocument();
    });

    it("says only the paired account can use a paired bot", async () => {
        mocks.telegramStatus.mockResolvedValue({ connected: true, telegram_user_name: "isoform_bot", daemon_online: true, paired: true });
        render(<TelegramStandaloneSetup onStatusChange={vi.fn()} />);

        expect(await screen.findByText(/Only your Telegram account can use this bot/)).toBeInTheDocument();
        expect(screen.queryByText("Finish pairing")).not.toBeInTheDocument();
    });

    it("keeps whether the bot is receiving messages under Advanced", async () => {
        render(<TelegramStandaloneSetup onStatusChange={vi.fn()} />);

        expect(await screen.findByText("Connected as @isoform_bot")).toBeInTheDocument();
        expect(screen.queryByText("Online")).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: /Advanced/ }));
        expect(screen.getByText("Receiving messages")).toBeInTheDocument();
        expect(screen.getByText("Online")).toBeInTheDocument();
    });

    it("keeps the plain intro and token field in the card and the setup steps under Advanced", async () => {
        mocks.telegramStatus.mockResolvedValue({ connected: false, daemon_online: false });
        render(<TelegramStandaloneSetup onStatusChange={vi.fn()} />);

        expect(await screen.findByText(/setup steps under Advanced/)).toBeInTheDocument();
        expect(screen.getByText("Bot token")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Save and connect" })).toBeInTheDocument();
        expect(screen.queryByText(/send \/newbot/)).not.toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: /Advanced/ }));
        expect(screen.getByText("Setup steps")).toBeInTheDocument();
        expect(screen.getByText(/send \/newbot/)).toBeInTheDocument();
    });

    it("asks before Create new deletes a saved bot", async () => {
        mocks.telegramStatus.mockResolvedValue({ connected: false, daemon_online: false });
        mocks.telegramArchived.mockResolvedValue({ has: true, label: "old_bot" });
        render(<TelegramStandaloneSetup onStatusChange={vi.fn()} />);

        fireEvent.click(await screen.findByRole("button", { name: "Create new" }));
        const dialog = await screen.findByRole("dialog");
        expect(within(dialog).getByText("Remove your previous Telegram app?")).toBeInTheDocument();
        fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
        expect(mocks.discardTelegramArchived).not.toHaveBeenCalled();
        expect(screen.getByText("old_bot")).toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: "Create new" }));
        fireEvent.click(
            within(await screen.findByRole("dialog")).getByRole("button", { name: "Remove and create new" }),
        );
        await waitFor(() => expect(mocks.discardTelegramArchived).toHaveBeenCalled());
        expect(await screen.findByText("Bot token")).toBeInTheDocument();
    });

    it("draws the WeChat login code locally", async () => {
        mocks.wechatStatus.mockResolvedValue({ connected: false, online: false });
        mocks.startWeChatLogin.mockResolvedValue({ qr_data: "https://login.weixin.example/qr", qr_code: "abc" });
        mocks.pollWeChatLogin.mockResolvedValue({ status: "waiting" });
        const { container } = render(<WeChatSettings onStatusChange={vi.fn()} />);

        fireEvent.click(await screen.findByRole("button", { name: /Connect/ }));
        await waitFor(() => expect(container.querySelector("svg title")).not.toBeNull());
        expect(container.querySelector("img")).toBeNull();
    });

    it("has no hardcoded English left in the Feishu chats card or bot tests", () => {
        expect(feishuSource).not.toMatch(/"Failed to load chats"|"Loading chats\.\.\."|>\s*All\s*<|>\s*None\s*</);
        expect(feishuSource).not.toMatch(/timed out"\)/);
        expect(botSource).not.toMatch(/\?\? "failed"/);
    });
});

const PAIRED_NOTE = "Paired. Only the account that sent the pairing code can use this connection.";

// Each connector shows its pairing code until the owner sends it, then the
// paired note. These cases check the code and the note in both states.
describe("chat connector owner pairing", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        window.localStorage.clear();
        await i18n.changeLanguage("en");
        mocks.telegramArchived.mockResolvedValue({ has: false, label: "" });
        mocks.wechatStatus.mockResolvedValue({ connected: false, online: false });
    });

    it("shows the Slack pairing code until its owner sends it, then the paired note", async () => {
        mocks.slackStatus.mockResolvedValueOnce({
            connected: true, team_name: "Acme", slack_user_name: "isoform", daemon_online: true,
            paired: false, pairing_code: "SL4K-M2XP",
        });
        const first = render(<SlackStandaloneSetup onStatusChange={vi.fn()} />);
        expect(await screen.findByText("SL4K-M2XP")).toBeInTheDocument();
        expect(screen.getByText("Finish pairing")).toBeInTheDocument();
        first.unmount();

        mocks.slackStatus.mockResolvedValueOnce({
            connected: true, team_name: "Acme", slack_user_name: "isoform", daemon_online: true,
            paired: true, pairing_code: "",
        });
        render(<SlackStandaloneSetup onStatusChange={vi.fn()} />);
        expect(await screen.findByText(PAIRED_NOTE)).toBeInTheDocument();
        expect(screen.queryByText("Finish pairing")).not.toBeInTheDocument();
    });

    it("shows the Discord pairing code until its owner sends it, then the paired note", async () => {
        mocks.discordStatus.mockResolvedValueOnce({
            bound: true, discord_user: "isoform", daemon_online: true, guilds: [],
            paired: false, pairing_code: "DC7Q-PX2M",
        });
        const first = render(<DiscordStandaloneSetup onStatusChange={vi.fn()} />);
        expect(await screen.findByText("DC7Q-PX2M")).toBeInTheDocument();
        expect(screen.getByText("Finish pairing")).toBeInTheDocument();
        first.unmount();

        mocks.discordStatus.mockResolvedValueOnce({
            bound: true, discord_user: "isoform", daemon_online: true, guilds: [],
            paired: true, pairing_code: "",
        });
        render(<DiscordStandaloneSetup onStatusChange={vi.fn()} />);
        expect(await screen.findByText(PAIRED_NOTE)).toBeInTheDocument();
        expect(screen.queryByText("Finish pairing")).not.toBeInTheDocument();
    });

    it("shows the WeChat pairing code until its owner sends it, then the paired note", async () => {
        mocks.wechatStatus.mockResolvedValueOnce({
            connected: true, bot_id: "bot_1", online: true, paired: false, pairing_code: "WX3R-7KQN",
        });
        const first = render(<WeChatSettings onStatusChange={vi.fn()} />);
        expect(await screen.findByText("WX3R-7KQN")).toBeInTheDocument();
        expect(screen.getByText("Finish pairing")).toBeInTheDocument();
        first.unmount();

        mocks.wechatStatus.mockResolvedValueOnce({
            connected: true, bot_id: "bot_1", online: true, paired: true, pairing_code: "",
        });
        render(<WeChatSettings onStatusChange={vi.fn()} />);
        expect(await screen.findByText(PAIRED_NOTE)).toBeInTheDocument();
        expect(screen.queryByText("Finish pairing")).not.toBeInTheDocument();
    });

    it("says Couldn't disconnect WeChat in copper without the raw error, and Try again retries", async () => {
        mocks.wechatStatus.mockResolvedValue({ connected: true, bot_id: "bot_1", online: true, paired: true, pairing_code: "" });
        mocks.disconnectWeChat.mockRejectedValueOnce(new Error("socket closed")).mockResolvedValueOnce(undefined);
        render(<WeChatSettings onStatusChange={vi.fn()} />);
        fireEvent.click(await screen.findByRole("button", { name: "Disconnect" }));
        fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Disconnect" }));
        const line = await screen.findByRole("alert");
        expect(line).toHaveTextContent("Couldn't disconnect WeChat.");
        expect(line).not.toHaveTextContent("socket closed");
        expect(line).toHaveClass("text-copper");
        fireEvent.click(within(line).getByRole("button", { name: "Try again" }));
        await waitFor(() => expect(mocks.disconnectWeChat).toHaveBeenCalledTimes(2));
    });

    it("shows the Feishu pairing code until its owner sends it, then the paired note", async () => {
        mocks.feishuStatus.mockResolvedValueOnce({
            connected: true, app_id: "cli_1", bot_name: "Isoform Bot", daemon_online: true,
            permission_ready: true, paired: false, pairing_code: "FS5N-2TAB",
        });
        mocks.feishuReceiver.mockResolvedValue({ configured: true, started: true, connected: true, status: "connected" });
        mocks.feishuChats.mockResolvedValue({ chats: [] });
        mocks.feishuFilter.mockResolvedValue([]);
        const first = render(<FeishuSettings brand="feishu" onStatusChange={vi.fn()} />);
        expect(await screen.findByText("FS5N-2TAB")).toBeInTheDocument();
        expect(screen.getByText("Finish pairing")).toBeInTheDocument();
        first.unmount();

        mocks.feishuStatus.mockResolvedValueOnce({
            connected: true, app_id: "cli_1", bot_name: "Isoform Bot", daemon_online: true,
            permission_ready: true, paired: true, pairing_code: "",
        });
        render(<FeishuSettings brand="feishu" onStatusChange={vi.fn()} />);
        expect(await screen.findByText(PAIRED_NOTE)).toBeInTheDocument();
        expect(screen.queryByText("Finish pairing")).not.toBeInTheDocument();
    });
});

describe("chat app results in place (ST29)", () => {
    async function fillSlack() {
        mocks.slackStatus.mockResolvedValue({ connected: false, daemon_online: false });
        render(<SlackStandaloneSetup onStatusChange={() => {}} />);
        fireEvent.change(await screen.findByPlaceholderText("xoxb-..."), { target: { value: "xoxb-1" } });
        fireEvent.change(screen.getByPlaceholderText("xapp-..."), { target: { value: "xapp-1" } });
    }

    beforeEach(async () => {
        await i18n.changeLanguage("en");
        vi.mocked(TestSlackConnection).mockReset();
        vi.mocked(SaveSlackLocalCredentials).mockReset();
    });

    it("says under the token fields that the token works", async () => {
        vi.mocked(TestSlackConnection).mockResolvedValue({ success: true, team_name: "Northwind" } as never);
        await fillSlack();
        fireEvent.click(screen.getByRole("button", { name: "Test connection" }));
        expect(await screen.findByText("The token works for Northwind. Save and connect to finish.")).toBeInTheDocument();
        expect(botSource).not.toContain("sonner");
    });

    it("shows a failed test in copper without the raw error", async () => {
        vi.mocked(TestSlackConnection).mockResolvedValue({ success: false, error: "invalid_auth" } as never);
        await fillSlack();
        fireEvent.click(screen.getByRole("button", { name: "Test connection" }));
        const line = await screen.findByRole("alert");
        expect(line).toHaveTextContent("The token didn't work. Check it and try again.");
        expect(line).toHaveClass("text-copper");
        expect(line).not.toHaveTextContent("invalid_auth");
    });

    it("says Couldn't connect and retries the save", async () => {
        vi.mocked(SaveSlackLocalCredentials).mockRejectedValueOnce(new Error("boom")).mockResolvedValueOnce(undefined as never);
        await fillSlack();
        fireEvent.click(screen.getByRole("button", { name: "Save and connect" }));
        const line = await screen.findByRole("alert");
        expect(line).toHaveTextContent("Couldn't connect Slack.");
        fireEvent.click(within(line).getByRole("button", { name: "Try again" }));
        await waitFor(() => expect(SaveSlackLocalCredentials).toHaveBeenCalledTimes(2));
    });

    it("words the results in Chinese", async () => {
        await i18n.changeLanguage("zh-CN");
        expect(i18n.t("settings:connectors.common.couldntDisconnect", { app: "Slack" })).toBe("无法断开Slack。");
        await i18n.changeLanguage("en");
    });
});
