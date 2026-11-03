import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import componentSource from "./skill-hub-settings.tsx?raw";
import { SkillHubSettings } from "./skill-hub-settings";

const mocks = vi.hoisted(() => ({
    list: vi.fn(),
    uninstall: vi.fn(),
    setEnabled: vi.fn(),
    setDomains: vi.fn(),
    toastError: vi.fn(),
    toastSuccess: vi.fn(),
}));

vi.mock("../../wailsjs/go/main/App", () => ({
    InstallClawHubSkill: vi.fn(),
    InstallSkillFromAgentDir: vi.fn(),
    InstallSkillFromGitHub: vi.fn(),
    InstallSkillFromLocal: vi.fn(),
    DiscoverAgentSkills: vi.fn(),
    ListSkillHubInstalledSkills: mocks.list,
    OpenSkillHubSkillFolder: vi.fn(),
    SearchAgentSkills: vi.fn(),
    SearchClawHubSkills: vi.fn(),
    SelectLocalSkillDir: vi.fn(),
    SetSkillHubSkillDomains: mocks.setDomains,
    SetSkillHubSkillEnabled: mocks.setEnabled,
    UninstallSkillHubSkill: mocks.uninstall,
    // mcp-hub-settings (for the shared tabs) imports these at module load.
    AddMcpServerManual: vi.fn(),
    ConnectMcpServerOAuth: vi.fn(),
    DiscoverLocalMcpServers: vi.fn(),
    ImportLocalMcpServer: vi.fn(),
    InstallMcpFromRegistry: vi.fn(),
    ListMcpServers: vi.fn(),
    OpenMcpServerFolder: vi.fn(),
    RefreshMcpRegistry: vi.fn(),
    RestartMcpServer: vi.fn(),
    SearchMcpRegistry: vi.fn(),
    SetMcpServerEnabled: vi.fn(),
    SetMcpServerTrusted: vi.fn(),
    StopMcpServer: vi.fn(),
    TailMcpServerLog: vi.fn(),
    UninstallMcpServer: vi.fn(),
}));

vi.mock("../../wailsjs/runtime/runtime", () => ({
    BrowserOpenURL: vi.fn(),
}));

vi.mock("sonner", () => ({
    toast: { error: mocks.toastError, success: mocks.toastSuccess },
}));

function skill(overrides: Record<string, unknown>) {
    return {
        id: "calendar",
        slug: "calendar",
        name: "Calendar helper",
        description: "Plans meetings.",
        source: "clawhub",
        source_url: "https://clawhub.ai/calendar",
        version: "1.2.0",
        path: "/Users/me/.yansu-agent/skills/calendar",
        enabled: true,
        domains: { chat: true, project: true, automation: false },
        installed_at: 0,
        updated_at: 0,
        security_status: "clean",
        ...overrides,
    };
}

const SKILLS = [
    skill({}),
    skill({
        id: "notes",
        slug: "notes",
        name: "Notes writer",
        description: "Writes notes.",
        source: "github",
        enabled: false,
        security_status: "suspicious",
        path: "/Users/me/.yansu-agent/skills/notes",
    }),
];

describe("SkillHubSettings", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        window.localStorage.clear();
        await i18n.changeLanguage("en");
        mocks.list.mockResolvedValue(SKILLS);
        mocks.uninstall.mockResolvedValue(undefined);
    });

    it("opens on Installed with name, description and on/off", async () => {
        render(<SkillHubSettings />);

        expect(screen.getAllByRole("tab").map((tab) => tab.textContent)).toEqual(["Installed", "Add"]);
        expect(await screen.findByText("Calendar helper")).toBeInTheDocument();
        expect(screen.getByText("Plans meetings.")).toBeInTheDocument();
        expect(screen.getByRole("switch", { name: "Use Calendar helper" })).toBeChecked();
        expect(screen.getByRole("switch", { name: "Use Notes writer" })).not.toBeChecked();
        // A flagged skill says so in words; the raw value never shows.
        expect(screen.getByText("Flagged as suspicious")).toBeInTheDocument();
        expect(screen.queryByText("suspicious")).not.toBeInTheDocument();
        // Folder paths are Advanced.
        expect(screen.queryByText("/Users/me/.yansu-agent/skills/calendar")).not.toBeInTheDocument();
    });

    it("shows where a skill is used, and disables the choice while the skill is off", async () => {
        render(<SkillHubSettings />);
        fireEvent.click(await screen.findByRole("button", { name: "Details for Calendar helper" }));

        expect(screen.getByText("Use in")).toBeInTheDocument();
        const chat = screen.getByRole("checkbox", { name: "Chat" });
        expect(chat).toBeEnabled();
        expect(screen.getByRole("checkbox", { name: "Projects" })).toBeInTheDocument();
        expect(screen.getByRole("checkbox", { name: "Scheduled" })).not.toBeChecked();

        fireEvent.click(screen.getByRole("button", { name: "Details for Notes writer" }));
        expect(screen.getByText("Turn the skill on to choose where it is used.")).toBeInTheDocument();
        const disabled = screen.getAllByRole("checkbox").filter((box) => box.hasAttribute("disabled"));
        expect(disabled).toHaveLength(3);
    });

    it("asks before uninstalling a skill", async () => {
        render(<SkillHubSettings />);
        // In the row, as on Tool servers, not inside the details.
        fireEvent.click(await screen.findByRole("button", { name: "Uninstall Calendar helper" }));

        const dialog = await screen.findByRole("dialog");
        expect(within(dialog).getByText("Uninstall Calendar helper?")).toBeInTheDocument();
        expect(mocks.uninstall).not.toHaveBeenCalled();

        fireEvent.click(within(dialog).getByRole("button", { name: "Uninstall" }));
        await waitFor(() => expect(mocks.uninstall).toHaveBeenCalledWith("calendar"));
        await waitFor(() => expect(screen.queryByText("Calendar helper")).not.toBeInTheDocument());
    });

    it("offers the library and the other AI tools on the Add tab", async () => {
        render(<SkillHubSettings />);
        await screen.findByText("Calendar helper");
        fireEvent.click(screen.getByRole("tab", { name: "Add" }));

        expect(screen.getByText("Browse the skill library")).toBeInTheDocument();
        expect(screen.getByText("Found in your other AI tools")).toBeInTheDocument();
        // GitHub and folder installs are not on the everyday tab.
        expect(screen.queryByText("Install from GitHub")).not.toBeInTheDocument();
    });

    it("keeps GitHub, folder installs and file locations under Advanced", async () => {
        render(<SkillHubSettings />);
        await screen.findByText("Calendar helper");
        fireEvent.click(screen.getByRole("button", { name: /Advanced/ }));

        expect(screen.getByText("Install from GitHub")).toBeInTheDocument();
        expect(screen.getByText(/not reviewed by anyone/)).toBeInTheDocument();
        expect(screen.getByText("Install from a folder")).toBeInTheDocument();
        expect(screen.getByText("Where skills are stored")).toBeInTheDocument();
        expect(screen.getByText("/Users/me/.yansu-agent/skills/calendar")).toBeInTheDocument();
        expect(screen.getByText("Safety check: Checked, no issues found")).toBeInTheDocument();
        expect(screen.getByText("Safety check: Flagged as suspicious")).toBeInTheDocument();
    });

    it("renders in Chinese", async () => {
        await i18n.changeLanguage("zh-CN");
        render(<SkillHubSettings />);

        expect(await screen.findByText("被标记为可疑")).toBeInTheDocument();
        expect(screen.getByRole("tab", { name: "已安装" })).toBeInTheDocument();
    });

    it("never passes inline fallbacks to t()", () => {
        expect(componentSource).not.toMatch(/defaultValue/);
        expect(componentSource).not.toMatch(/as never/);
    });
});
