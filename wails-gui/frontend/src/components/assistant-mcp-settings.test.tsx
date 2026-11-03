import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import type { MCPReadConnection } from "@/lib/wails-shim-app";
import { AssistantMCPSettings } from "./assistant-mcp-settings";

const mocks = vi.hoisted(() => ({
    list: vi.fn(),
    connect: vi.fn(),
    revoke: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    GetMCPReadConnections: mocks.list,
    ConnectMCPReadAssistant: mocks.connect,
    RevokeMCPReadAssistant: mocks.revoke,
}));

const claude: MCPReadConnection = {
    client: "claude-code",
    name: "Claude Code",
    command: "claude mcp add --scope user isoform -- '/Applications/Isoform.app/Contents/MacOS/yansu' mcp serve --client claude-code",
    connected: false,
    authorized: false,
    available: true,
};

const codex: MCPReadConnection = {
    client: "codex",
    name: "Codex",
    command: "codex mcp add isoform -- '/Applications/Isoform.app/Contents/MacOS/yansu' mcp serve --client codex",
    connected: false,
    authorized: false,
    available: true,
};

describe("AssistantMCPSettings", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        await i18n.changeLanguage("en");
        mocks.list.mockResolvedValue([claude, codex]);
        mocks.connect.mockResolvedValue({ ...claude, connected: true, authorized: true });
        mocks.revoke.mockResolvedValue(claude);
    });

    it("shows each exact command and only connects after the user's click", async () => {
        render(<AssistantMCPSettings />);
        const claudeCard = await screen.findByRole("article", { name: "Claude Code" });
        const codexCard = screen.getByRole("article", { name: "Codex" });

        expect(within(claudeCard).getByText(claude.command)).toBeInTheDocument();
        expect(within(codexCard).getByText(codex.command)).toBeInTheDocument();
        expect(within(claudeCard).getByRole("status")).toHaveTextContent("Not connected");
        expect(mocks.connect).not.toHaveBeenCalled();
        expect(mocks.revoke).not.toHaveBeenCalled();

        fireEvent.click(within(claudeCard).getByRole("button", { name: "Connect" }));
        await waitFor(() => expect(mocks.connect).toHaveBeenCalledWith("claude-code"));
        expect(mocks.connect).toHaveBeenCalledTimes(1);
        await waitFor(() => expect(within(claudeCard).getByRole("status")).toHaveTextContent(/^Connected$/));
        expect(within(claudeCard).getByRole("button", { name: "Revoke" })).toBeEnabled();
        expect(within(codexCard).getByRole("status")).toHaveTextContent("Not connected");
    });

    it("connects Codex using its own client identity", async () => {
        mocks.connect.mockResolvedValue({ ...codex, connected: true, authorized: true });
        render(<AssistantMCPSettings />);
        const codexCard = await screen.findByRole("article", { name: "Codex" });
        fireEvent.click(within(codexCard).getByRole("button", { name: "Connect" }));

        await waitFor(() => expect(mocks.connect).toHaveBeenCalledWith("codex"));
        expect(mocks.connect).toHaveBeenCalledTimes(1);
        await waitFor(() => expect(within(codexCard).getByRole("status")).toHaveTextContent(/^Connected$/));
    });

    it("keeps Revoke available for authorized access after the CLI disappears", async () => {
        mocks.list.mockResolvedValue([{ ...claude, authorized: true, available: false, availabilityReason: "assistant_missing" }]);
        mocks.revoke.mockResolvedValue({ ...claude, available: false, availabilityReason: "assistant_missing" });
        render(<AssistantMCPSettings />);

        expect(await screen.findByRole("button", { name: "Connect" })).toBeDisabled();
        expect(screen.getByText("Install the Claude Code command-line app, then reopen this page to connect.")).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Revoke" }));

        await waitFor(() => expect(mocks.revoke).toHaveBeenCalledWith("claude-code"));
        expect(mocks.revoke).toHaveBeenCalledTimes(1);
        await waitFor(() => expect(screen.queryByRole("button", { name: "Revoke" })).not.toBeInTheDocument());
        expect(screen.getByRole("status")).toHaveTextContent("Not connected");
    });

    it("names the missing Isoform command instead of asking to install the assistant", async () => {
        mocks.list.mockResolvedValue([{ ...claude, available: false, availabilityReason: "isoform_missing" }]);
        render(<AssistantMCPSettings />);

        expect(await screen.findByRole("button", { name: "Connect" })).toBeDisabled();
        expect(screen.getByText("The Isoform command-line app is missing. Reinstall Isoform, then reopen this page to connect.")).toBeInTheDocument();
        expect(screen.queryByText(/Install the Claude Code command-line app/)).not.toBeInTheDocument();
        expect(mocks.connect).not.toHaveBeenCalled();
    });

    it("keeps a failed connection visible beside a refreshed conflict and disables Connect", async () => {
        mocks.list.mockResolvedValueOnce([claude]).mockResolvedValue([
            { ...claude, issue: "name_conflict", conflictCommand: "env" },
        ]);
        mocks.connect.mockRejectedValueOnce(new Error("raw CLI output with a secret credential"));
        render(<AssistantMCPSettings />);
        fireEvent.click(await screen.findByRole("button", { name: "Connect" }));

        expect(await screen.findByText("Claude Code already uses the name isoform for env. Rename or remove that server in Claude Code, then try again.")).toHaveAttribute("role", "alert");
        expect(await screen.findByText("Could not connect. Check that the assistant's command-line app is installed and try again.")).toHaveAttribute("role", "alert");
        expect(screen.getByRole("button", { name: "Connect" })).toBeDisabled();
        expect(mocks.list).toHaveBeenCalledTimes(2);
        expect(mocks.connect).toHaveBeenCalledTimes(1);
        expect(screen.queryByText(/secret credential/)).not.toBeInTheDocument();
    });

    it.each([
        { language: "en", connect: "Connect", revoke: "Revoke", failure: "Could not finish revoking access and removing the MCP entry. Try again." },
        { language: "zh-CN", connect: "连接", revoke: "撤销", failure: "无法完成撤销访问权限和移除 MCP 条目的操作，请重试。" },
    ])("keeps failed revocation visible beside a conflict in $language", async ({ language, connect, revoke, failure }) => {
        await i18n.changeLanguage(language);
        mocks.list.mockResolvedValue([{ ...claude, authorized: true, issue: "name_conflict", conflictCommand: "env" }]);
        mocks.revoke.mockRejectedValueOnce(new Error("raw CLI output with a secret credential"));
        render(<AssistantMCPSettings />);

        const connectButton = await screen.findByRole("button", { name: connect });
        fireEvent.click(screen.getByRole("button", { name: revoke }));

        expect(await screen.findByText(failure)).toHaveAttribute("role", "alert");
        expect(screen.getAllByRole("alert")).toHaveLength(2);
        expect(screen.getByRole("button", { name: revoke })).toBeEnabled();
        expect(connectButton).toBeDisabled();
        fireEvent.click(connectButton);
        expect(mocks.connect).not.toHaveBeenCalled();
        expect(mocks.revoke).toHaveBeenCalledTimes(1);
        expect(mocks.revoke).toHaveBeenCalledWith("claude-code");
        expect(screen.queryByText(/secret credential/)).not.toBeInTheDocument();
    });

    it.each([
        { language: "en", revoke: "Revoke", status: "Not connected", failure: "Could not finish revoking access and removing the MCP entry. Try again." },
        { language: "zh-CN", revoke: "撤销", status: "未连接", failure: "无法完成撤销访问权限和移除 MCP 条目的操作，请重试。" },
    ])("refreshes revoked access and lets cleanup retry in $language", async ({ language, revoke, status, failure }) => {
        await i18n.changeLanguage(language);
        mocks.list.mockResolvedValueOnce([{ ...claude, authorized: true, connected: true }]).mockResolvedValue([claude]);
        mocks.revoke.mockRejectedValueOnce(new Error("private CLI cleanup output"));
        render(<AssistantMCPSettings />);
        fireEvent.click(await screen.findByRole("button", { name: revoke }));

        expect(await screen.findByRole("alert")).toHaveTextContent(failure);
        expect(screen.getByRole("status")).toHaveTextContent(status);
        expect(screen.getByRole("button", { name: revoke })).toBeEnabled();
        expect(screen.queryByText(/private CLI/)).not.toBeInTheDocument();
        expect(mocks.list).toHaveBeenCalledTimes(2);

        fireEvent.click(screen.getByRole("button", { name: revoke }));
        await waitFor(() => expect(screen.queryByRole("button", { name: revoke })).not.toBeInTheDocument());
        expect(screen.queryByRole("alert")).not.toBeInTheDocument();
        expect(mocks.revoke).toHaveBeenCalledTimes(2);
        expect(mocks.connect).not.toHaveBeenCalled();
    });

    it("renders missing-command and name-conflict details in Chinese without HTML interpretation", async () => {
        await i18n.changeLanguage("zh-CN");
        mocks.list.mockResolvedValue([
            { ...claude, issue: "name_conflict", conflictCommand: "<server>" },
            { ...codex, available: false, availabilityReason: "isoform_missing" },
        ]);
        const { container } = render(<AssistantMCPSettings />);

        expect(await screen.findByRole("alert")).toHaveTextContent("Claude Code 中名为 isoform 的服务器已用于 <server>。请在 Claude Code 中重命名或移除该服务器，然后重试。");
        expect(screen.getByText("缺少 Isoform 命令行应用。请重新安装 Isoform，然后重新打开此页面进行连接。")).toBeInTheDocument();
        expect(container.querySelector("server")).toBeNull();
        expect(mocks.connect).not.toHaveBeenCalled();
    });

    it("prevents same-tick reentrant registration before the button becomes disabled", async () => {
        let finish!: (connection: MCPReadConnection) => void;
        const pending = new Promise<MCPReadConnection>((resolve) => { finish = resolve; });
        render(<AssistantMCPSettings />);
        const claudeCard = await screen.findByRole("article", { name: "Claude Code" });
        const connect = within(claudeCard).getByRole("button", { name: "Connect" });
        mocks.connect.mockImplementationOnce(() => {
            // Reenter while the first click is still being handled: React has
            // not committed the disabled state, so only the ref can stop it.
            expect(connect).toBeEnabled();
            connect.click();
            return pending;
        }).mockReturnValue(pending);
        fireEvent.click(connect);

        expect(mocks.connect).toHaveBeenCalledTimes(1);
        expect(mocks.connect).toHaveBeenCalledWith("claude-code");
        expect(screen.getByRole("button", { name: "Connecting..." })).toBeDisabled();
        expect(screen.getByRole("button", { name: "Connect" })).toBeDisabled();
        await act(async () => { finish({ ...claude, connected: true, authorized: true }); });
        expect(screen.getByRole("button", { name: "Connect" })).toBeEnabled();
    });

    it("prevents same-tick clicks from registering either assistant twice", async () => {
        let finish!: (connection: MCPReadConnection) => void;
        const pending = new Promise<MCPReadConnection>((resolve) => { finish = resolve; });
        mocks.connect.mockReturnValue(pending);
        render(<AssistantMCPSettings />);
        const claudeCard = await screen.findByRole("article", { name: "Claude Code" });
        const connect = within(claudeCard).getByRole("button", { name: "Connect" });
        // All events reach the handler before React renders disabled controls.
        // Separate fireEvent calls would flush between clicks and only test the
        // disabled button, leaving the synchronous actionPending guard untested.
        act(() => {
            connect.dispatchEvent(new MouseEvent("click", { bubbles: true }));
            connect.dispatchEvent(new MouseEvent("click", { bubbles: true }));
            within(screen.getByRole("article", { name: "Codex" }))
                .getByRole("button", { name: "Connect" })
                .dispatchEvent(new MouseEvent("click", { bubbles: true }));
        });

        expect(mocks.connect).toHaveBeenCalledTimes(1);
        expect(mocks.connect).toHaveBeenCalledWith("claude-code");
        expect(screen.getByRole("button", { name: "Connecting..." })).toBeDisabled();
        expect(screen.getByRole("button", { name: "Connect" })).toBeDisabled();
        await act(async () => { finish({ ...claude, connected: true, authorized: true }); });
        expect(screen.getByRole("button", { name: "Connect" })).toBeEnabled();
    });

    it("retries a failed status read without registering either assistant", async () => {
        mocks.list.mockRejectedValueOnce(new Error("unavailable"));
        render(<AssistantMCPSettings />);
        expect(await screen.findByRole("alert")).toHaveTextContent("Could not check connections. Try again.");
        fireEvent.click(screen.getByRole("button", { name: "Try again" }));

        expect(await screen.findByRole("article", { name: "Claude Code" })).toBeInTheDocument();
        expect(mocks.list).toHaveBeenCalledTimes(2);
        expect(mocks.connect).not.toHaveBeenCalled();
    });

    it("keeps connection state and offers retry when registration or revocation fails", async () => {
        mocks.list.mockResolvedValue([claude, { ...codex, authorized: true, connected: true }]);
        mocks.connect.mockRejectedValueOnce(new Error("private command output"));
        mocks.revoke.mockRejectedValueOnce(new Error("private token path"));
        render(<AssistantMCPSettings />);
        const claudeCard = await screen.findByRole("article", { name: "Claude Code" });
        const codexCard = screen.getByRole("article", { name: "Codex" });
        fireEvent.click(within(claudeCard).getByRole("button", { name: "Connect" }));
        expect(await within(claudeCard).findByRole("alert")).toHaveTextContent("Could not connect.");
        expect(within(claudeCard).getByRole("button", { name: "Connect" })).toBeEnabled();
        fireEvent.click(within(codexCard).getByRole("button", { name: "Revoke" }));
        expect(await within(codexCard).findByRole("alert")).toHaveTextContent("Could not finish revoking access and removing the MCP entry. Try again.");
        expect(within(codexCard).getByRole("status")).toHaveTextContent(/^Connected$/);
        expect(within(codexCard).getByRole("button", { name: "Revoke" })).toBeEnabled();
        expect(screen.queryByText(/private command output|private token path/)).not.toBeInTheDocument();
    });

    it("renders connection controls and errors in Chinese", async () => {
        await i18n.changeLanguage("zh-CN");
        mocks.connect.mockRejectedValueOnce(new Error("unavailable"));
        render(<AssistantMCPSettings />);
        const claudeCard = await screen.findByRole("article", { name: "Claude Code" });

        expect(screen.getByRole("heading", { name: "将 Isoform 连接到 Claude Code / Codex" })).toBeInTheDocument();
        expect(within(claudeCard).getByRole("status")).toHaveTextContent("未连接");
        expect(within(claudeCard).getByText("注册命令")).toBeInTheDocument();
        fireEvent.click(within(claudeCard).getByRole("button", { name: "连接" }));
        expect(await within(claudeCard).findByRole("alert")).toHaveTextContent("无法连接。请确认已安装助手的命令行应用，然后重试。");
    });
});
