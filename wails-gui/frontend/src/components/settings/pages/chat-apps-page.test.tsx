import { useState } from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import type { ChatAppId } from "@/components/settings/settings-registry";
import { ChatAppsPage } from "./chat-apps-page";

const mocks = vi.hoisted(() => ({
    slack: vi.fn(),
    discord: vi.fn(),
    telegram: vi.fn(),
    wechat: vi.fn(),
    feishu: vi.fn(),
}));

vi.mock("../../../../wailsjs/go/main/App", () => ({
    GetSlackStatus: mocks.slack,
    GetDiscordStatus: mocks.discord,
    GetTelegramStatus: mocks.telegram,
    GetWeChatStatus: mocks.wechat,
    GetFeishuStatus: mocks.feishu,
}));

// The setup flows are the existing live components; here they are markers.
vi.mock("./chat-apps/bot-setups", () => ({
    SlackStandaloneSetup: () => <div data-testid="setup">slack-setup</div>,
    DiscordStandaloneSetup: () => <div data-testid="setup">discord-setup</div>,
    TelegramStandaloneSetup: () => <div data-testid="setup">telegram-setup</div>,
}));
vi.mock("./chat-apps/wechat-setup", () => ({
    WeChatSettings: () => <div data-testid="setup">wechat-setup</div>,
}));
vi.mock("./chat-apps/feishu-setup", () => ({
    FeishuSettings: ({ brand }: { brand: string }) => <div data-testid="setup">{brand}-setup</div>,
}));

function Harness({ initial }: { initial?: ChatAppId }) {
    const [app, setApp] = useState<ChatAppId | undefined>(initial);
    return <ChatAppsPage selectedApp={app} onSelectApp={setApp} />;
}

describe("ChatAppsPage", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        await i18n.changeLanguage("en");
        mocks.slack.mockResolvedValue({ connected: true, daemon_online: true });
        mocks.discord.mockResolvedValue({ bound: false, daemon_online: false, guilds: [] });
        mocks.telegram.mockResolvedValue({ connected: true, daemon_online: false });
        mocks.wechat.mockRejectedValue(new Error("not available"));
        mocks.feishu.mockImplementation((brand: string) =>
            Promise.resolve({ connected: brand === "lark", daemon_online: false, permission_ready: false }),
        );
    });

    it("lists every chat app with its status and the right action", async () => {
        render(<Harness />);

        await waitFor(() => expect(screen.getByText("Connected")).toBeInTheDocument());
        expect(screen.getByText("Where you can message Isoform")).toBeInTheDocument();
        for (const name of ["Slack", "Discord", "Telegram", "WeChat", "Feishu / Lark"]) {
            expect(screen.getByText(name)).toBeInTheDocument();
        }
        expect(screen.getByRole("button", { name: "Manage Slack" })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Set up Discord" })).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Manage Telegram" })).toBeInTheDocument();
        // Status unknown (the call failed): no chip, but still a way in.
        expect(screen.getByRole("button", { name: "Set up WeChat" })).toBeInTheDocument();
        // Feishu / Lark is set up if either brand is.
        expect(screen.getByRole("button", { name: "Manage Feishu / Lark" })).toBeInTheDocument();
        expect(screen.getAllByText("Offline")).toHaveLength(2);
        expect(screen.getByText("Not set up")).toBeInTheDocument();
    });

    it("opens a setup as a drill-in with a back link", async () => {
        render(<Harness />);
        fireEvent.click(await screen.findByRole("button", { name: "Set up Discord" }));

        expect(screen.getByTestId("setup")).toHaveTextContent("discord-setup");
        // A setup that is not connected yet opens with what a message does.
        expect(screen.getByText("Before you connect")).toBeInTheDocument();
        expect(screen.getByText(/asks you in the chat before/)).toBeInTheDocument();
        expect(screen.queryByText(/channels Isoform is invited to/)).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "All chat apps" }));
        expect(screen.queryByTestId("setup")).not.toBeInTheDocument();
        expect(await screen.findByRole("button", { name: "Set up Discord" })).toBeInTheDocument();
    });

    it("opens the brand that is set up and switches between Feishu and Lark", async () => {
        render(<Harness />);
        fireEvent.click(await screen.findByRole("button", { name: "Manage Feishu / Lark" }));

        expect(screen.getByTestId("setup")).toHaveTextContent("lark-setup");
        // Already set up: the before-you-connect note is not repeated.
        expect(screen.queryByText("Before you connect")).not.toBeInTheDocument();
        expect(screen.getByText("Which version do you use?")).toBeInTheDocument();
        expect(screen.getByRole("radio", { name: "Lark" })).toHaveAttribute("aria-checked", "true");

        fireEvent.click(screen.getByRole("radio", { name: "Feishu" }));
        expect(screen.getByTestId("setup")).toHaveTextContent("feishu-setup");
        // Let the drill-in's own status check finish.
        await act(async () => {});
    });

    it("starts on the drill-in for a deep link and checks whether that app is set up", async () => {
        render(<Harness initial="discord" />);
        expect(screen.getByTestId("setup")).toHaveTextContent("discord-setup");
        // The drill-in loads statuses itself, so a deep link knows Discord is not set up.
        expect(await screen.findByText("Before you connect")).toBeInTheDocument();
        expect(mocks.discord).toHaveBeenCalled();
    });

    it("never shows the note for an app that is set up but offline", async () => {
        render(<Harness initial="telegram" />);
        expect(screen.getByTestId("setup")).toHaveTextContent("telegram-setup");
        // Not even while its status is still loading.
        expect(screen.queryByText("Before you connect")).not.toBeInTheDocument();
        await waitFor(() => expect(mocks.telegram).toHaveBeenCalled());
        await act(async () => {});
        expect(screen.queryByText("Before you connect")).not.toBeInTheDocument();
    });

    it("adds what happens to invited channels on the Slack setup", async () => {
        mocks.slack.mockResolvedValue({ connected: false, daemon_online: false });
        render(<Harness initial="slack" />);
        expect(screen.getByTestId("setup")).toHaveTextContent("slack-setup");
        expect(await screen.findByText(/channels Isoform is invited to/)).toBeInTheDocument();
    });

    it("renders in Chinese", async () => {
        await i18n.changeLanguage("zh-CN");
        render(<Harness initial="wechat" />);
        expect(await screen.findByText("连接前请了解")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "所有聊天应用" })).toBeInTheDocument();
    });
});
