import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import componentSource from "./mcp-hub-settings.tsx?raw";
import { McpHubSettings } from "./mcp-hub-settings";

const mocks = vi.hoisted(() => ({
    list: vi.fn(),
    setEnabled: vi.fn(),
    uninstall: vi.fn(),
    discover: vi.fn(),
    searchRegistry: vi.fn(),
    tailLog: vi.fn(),
    toastError: vi.fn(),
    toastSuccess: vi.fn(),
}));

vi.mock("../../wailsjs/go/main/App", () => ({
    AddMcpServerManual: vi.fn(),
    ConnectMcpServerOAuth: vi.fn(),
    DiscoverLocalMcpServers: mocks.discover,
    ImportLocalMcpServer: vi.fn(),
    InstallMcpFromRegistry: vi.fn(),
    ListMcpServers: mocks.list,
    OpenMcpServerFolder: vi.fn(),
    RefreshMcpRegistry: vi.fn(),
    RestartMcpServer: vi.fn(),
    SearchMcpRegistry: mocks.searchRegistry,
    SetMcpServerEnabled: mocks.setEnabled,
    SetMcpServerTrusted: vi.fn(),
    StopMcpServer: vi.fn(),
    TailMcpServerLog: mocks.tailLog,
    UninstallMcpServer: mocks.uninstall,
}));

vi.mock("../../wailsjs/runtime/runtime", () => ({
    BrowserOpenURL: vi.fn(),
}));

vi.mock("sonner", () => ({
    toast: { error: mocks.toastError, success: mocks.toastSuccess },
}));

function server(overrides: Record<string, unknown>) {
    return {
        id: "files",
        name: "Files",
        description: "",
        source: "manual",
        transport: "stdio",
        command: "npx",
        args: ["-y", "files"],
        enabled: true,
        auto_start: true,
        installed_at: 0,
        updated_at: 0,
        health: "healthy",
        tools: [{ name: "read" }, { name: "write" }],
        trusted: false,
        ...overrides,
    };
}

const SERVERS = [
    server({}),
    server({
        id: "notes",
        name: "Notes",
        transport: "http",
        url: "https://notes.example/mcp",
        auth_mode: "oauth",
        oauth_status: "authorization_required",
        enabled: false,
        health: "unknown",
        tools: [],
    }),
];

describe("McpHubSettings", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        window.localStorage.clear();
        await i18n.changeLanguage("en");
        mocks.list.mockResolvedValue(SERVERS);
        mocks.uninstall.mockResolvedValue(undefined);
        mocks.discover.mockResolvedValue({ sources: [], servers: [] });
        mocks.searchRegistry.mockResolvedValue([]);
        mocks.tailLog.mockRejectedValue(new Error("not running"));
    });

    it("opens on Installed with plain status words and two tabs", async () => {
        render(<McpHubSettings />);

        expect(screen.getAllByRole("tab").map((tab) => tab.textContent)).toEqual(["Installed", "Add"]);
        expect(screen.getByRole("tab", { name: "Installed" })).toHaveAttribute("aria-selected", "true");

        expect(await screen.findByText("Files")).toBeInTheDocument();
        expect(screen.getByText("Running")).toBeInTheDocument();
        expect(screen.getByText("2 tools")).toBeInTheDocument();
        expect(screen.getByText("Not running")).toBeInTheDocument();
        // The raw backend value never reaches the screen.
        expect(screen.getByText("Sign-in needed")).toBeInTheDocument();
        expect(screen.queryByText(/authorization_required/)).not.toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Authorize" })).toBeInTheDocument();
        expect(screen.getByRole("switch", { name: "Use Files" })).toBeChecked();
    });

    it("asks before uninstalling a tool server", async () => {
        render(<McpHubSettings />);
        fireEvent.click(await screen.findByRole("button", { name: "Uninstall Files" }));

        const dialog = await screen.findByRole("dialog");
        expect(within(dialog).getByText("Uninstall Files?")).toBeInTheDocument();
        expect(mocks.uninstall).not.toHaveBeenCalled();

        fireEvent.click(within(dialog).getByRole("button", { name: "Uninstall" }));
        await waitFor(() => expect(mocks.uninstall).toHaveBeenCalledWith("files"));
        await waitFor(() => expect(screen.queryByText("Files")).not.toBeInTheDocument());
    });

    it("keeps the server when the confirmation is cancelled", async () => {
        render(<McpHubSettings />);
        fireEvent.click(await screen.findByRole("button", { name: "Uninstall Files" }));
        const dialog = await screen.findByRole("dialog");
        fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));

        expect(mocks.uninstall).not.toHaveBeenCalled();
        expect(screen.getByText("Files")).toBeInTheDocument();
    });

    it("reports a failed switch in words, not an inserted English verb", async () => {
        mocks.setEnabled.mockRejectedValue(new Error("boom"));
        render(<McpHubSettings />);
        fireEvent.click(await screen.findByRole("switch", { name: "Use Files" }));

        await waitFor(() =>
            expect(mocks.toastError).toHaveBeenCalledWith("Could not turn off Files: boom"),
        );
    });

    it("lists import and the server list on the Add tab", async () => {
        render(<McpHubSettings />);
        fireEvent.click(screen.getByRole("tab", { name: "Add" }));

        expect(await screen.findByText("Import from other apps on this computer")).toBeInTheDocument();
        expect(screen.getByText("Browse the server list")).toBeInTheDocument();
        expect(mocks.discover).toHaveBeenCalledTimes(1);
        expect(mocks.searchRegistry).toHaveBeenCalledWith("");
    });

    it("keeps manual setup and server details under Advanced", async () => {
        render(<McpHubSettings />);
        await screen.findByText("Files");

        expect(screen.queryByText("Add a server manually")).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: /Advanced/ }));

        expect(screen.getByText("Add a server manually")).toBeInTheDocument();
        // Command and arguments come first; the rest is folded.
        expect(screen.getByLabelText("Command")).toBeInTheDocument();
        expect(screen.queryByLabelText("Working directory")).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: /More options/ }));
        expect(screen.getByLabelText("Working directory")).toBeInTheDocument();

        // Sign-in settings only exist for a remote server.
        expect(screen.queryByRole("button", { name: /Sign-in settings/ })).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Remote (HTTP)" }));
        expect(screen.getByRole("button", { name: /Sign-in settings/ })).toBeInTheDocument();
        expect(screen.queryByText(/P1/)).not.toBeInTheDocument();
    });

    it("shows a server's details with a translated log message and the restart reason", async () => {
        render(<McpHubSettings />);
        await screen.findByText("Files");
        fireEvent.click(screen.getByRole("button", { name: /Advanced/ }));

        fireEvent.click(screen.getByRole("button", { name: "Details for Notes" }));
        expect(await screen.findByText("No logs to show: not running")).toBeInTheDocument();
        expect(screen.getByText("Sign-in (OAuth): Sign-in needed")).toBeInTheDocument();
        expect(screen.getByText("Turn this server on to restart it.")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Restart" })).toBeDisabled();
    });

    it("renders in Chinese", async () => {
        await i18n.changeLanguage("zh-CN");
        render(<McpHubSettings />);

        expect(await screen.findByText("运行中")).toBeInTheDocument();
        expect(screen.getByText("需要登录")).toBeInTheDocument();
        expect(screen.getByRole("tab", { name: "添加" })).toBeInTheDocument();
    });

    it("never passes inline fallbacks to t()", () => {
        expect(componentSource).not.toMatch(/defaultValue/);
        expect(componentSource).not.toMatch(/dangerouslySetInnerHTML/);
    });
});
