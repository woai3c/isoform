import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import en from "@/i18n/locales/en/settings.json";
import zh from "@/i18n/locales/zh-CN/settings.json";
import pageSource from "./learning-memory-page.tsx?raw";
import featuresSource from "./learning-features.tsx?raw";
import { LearningMemoryPage } from "./learning-memory-page";

const mocks = vi.hoisted(() => ({
    uiState: {} as Record<string, string>,
    loadUIState: vi.fn(),
    saveUIState: vi.fn(),
    getPermissions: vi.fn(),
    requestPermission: vi.fn(),
    openPermissionSettings: vi.fn(),
    getMemoryConfig: vi.fn(),
    getMemoryStatus: vi.fn(),
}));

// The wailsjs path and the shim resolve to the same module, so one mock covers both imports.
vi.mock("@/lib/wails-shim-app", () => ({
    LoadUIState: mocks.loadUIState,
    SaveUIState: mocks.saveUIState,
    GetPlatformPermissions: mocks.getPermissions,
    RequestPermission: mocks.requestPermission,
    OpenPermissionSettings: mocks.openPermissionSettings,
    GetMemoryConfig: mocks.getMemoryConfig,
    GetMemoryStatus: mocks.getMemoryStatus,
    SaveMemoryConfig: vi.fn(),
    TestEmbedding: vi.fn(),
}));

vi.mock("../../../../wailsjs/runtime/runtime", () => ({
    EventsOn: () => () => {},
}));

// Covered by their own tests; markers keep this one about the page layout.
vi.mock("@/components/self-updates-settings", () => ({
    SelfUpdatesSettings: () => <h2>Self updates card</h2>,
}));
vi.mock("@/components/memory-context-debug-settings", () => ({
    MemoryContextDebugSettings: () => <p>Chat context preview</p>,
}));
vi.mock("@/components/ai-memory-import-dialog", () => ({
    AIMemoryImportDialog: ({ open }: { open: boolean }) =>
        open ? <div role="dialog">Import dialog</div> : null,
}));

function permissions(contactsGranted: boolean | null) {
    const items = [{ key: "microphone", label: "Microphone", granted: true, actionable: true }];
    if (contactsGranted !== null) {
        items.push({ key: "contacts", label: "Contacts", granted: contactsGranted, actionable: true });
    }
    return { platform: "darwin", supported: true, items };
}

// What permissions_windows.go reports: Contacts is listed but can never be
// granted from Isoform (Linux reports the same shape).
function windowsPermissions() {
    return {
        platform: "windows",
        supported: true,
        items: [
            { key: "microphone", label: "Microphone", granted: true, actionable: true },
            { key: "contacts", label: "Contacts", granted: false, actionable: false },
        ],
    };
}

function renderPage() {
    const handlers = {
        onOpenMemory: vi.fn(),
        onOpenPeople: vi.fn(),
        onOpenProfile: vi.fn(),
    };
    render(<LearningMemoryPage {...handlers} />);
    return handlers;
}

function openAdvanced() {
    fireEvent.click(screen.getByRole("button", { name: /Advanced/ }));
}

describe("LearningMemoryPage", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        window.localStorage.clear();
        await i18n.changeLanguage("en");
        mocks.uiState = {};
        mocks.loadUIState.mockImplementation((key: string) =>
            Promise.resolve(mocks.uiState[key] ?? ""),
        );
        mocks.saveUIState.mockResolvedValue(undefined);
        mocks.getPermissions.mockResolvedValue(permissions(true));
        mocks.requestPermission.mockResolvedValue(false);
        mocks.openPermissionSettings.mockResolvedValue(undefined);
        mocks.getMemoryConfig.mockResolvedValue({ provider: "", apiKey: "", baseUrl: "", model: "" });
        mocks.getMemoryStatus.mockResolvedValue({
            files: 3,
            chunks: 12,
            dirty: false,
            syncReason: "",
            provider: "local-onnx",
            model: "all-MiniLM-L6-v2",
            vectorReady: true,
            cachedEmbeds: 0,
        });
    });

    it("shows a failed learning read and retries without guessing", async () => {
        mocks.loadUIState.mockRejectedValueOnce(new Error("offline"));
        renderPage();
        expect(await screen.findByText("Could not read this setting.")).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Try again" }));
        await waitFor(() => expect(screen.queryByText("Could not read this setting.")).toBeNull());
    });

    it("opens with four everyday cards and keeps the rest under Advanced", async () => {
        renderPage();

        expect(screen.getByRole("heading", { name: "What Isoform learns" })).toBeInTheDocument();
        expect(screen.getByRole("heading", { name: "Self updates card" })).toBeInTheDocument();
        expect(
            screen.getByRole("heading", { name: "Import memories from your other AI tools" }),
        ).toBeInTheDocument();
        expect(screen.getByRole("heading", { name: "See what Isoform knows" })).toBeInTheDocument();

        const advanced = screen.getByRole("button", { name: /Advanced/ });
        expect(advanced).toHaveTextContent("Learn by comparison, chat context preview, search index");
        expect(advanced).toHaveAttribute("aria-expanded", "false");
        expect(screen.queryByRole("switch", { name: "Learn by comparison" })).not.toBeInTheDocument();
        expect(screen.queryByText("Chat context preview")).not.toBeInTheDocument();
        expect(screen.queryByRole("heading", { name: "Search index" })).not.toBeInTheDocument();

        // Everyday learning switches: four of them, without Learn by comparison.
        await waitFor(() => expect(mocks.loadUIState).toHaveBeenCalledWith("triage_enable_sop"));
        expect(screen.getAllByRole("switch")).toHaveLength(4);
    });

    it("saves a learning switch and puts it back when the save fails", async () => {
        renderPage();
        const aboutYou = await screen.findByRole("switch", { name: "About you" });
        await waitFor(() => expect(mocks.loadUIState).toHaveBeenCalledWith("triage_enable_memory"));

        fireEvent.click(aboutYou);
        expect(mocks.saveUIState).toHaveBeenCalledWith("triage_enable_memory", "false");
        await waitFor(() => expect(aboutYou).toHaveAttribute("aria-checked", "false"));

        mocks.uiState.triage_enable_memory = "false";
        mocks.saveUIState.mockRejectedValueOnce(new Error("disk full"));
        fireEvent.click(aboutYou);
        expect(mocks.saveUIState).toHaveBeenLastCalledWith("triage_enable_memory", "true");
        expect(aboutYou).toHaveAttribute("aria-checked", "true");
        await waitFor(() => expect(screen.getByRole("switch", { name: "About you" })).toHaveAttribute("aria-checked", "false"));
    });

    it("links close Settings through the page callbacks", () => {
        const handlers = renderPage();

        fireEvent.click(screen.getByRole("button", { name: "Open About you" }));
        fireEvent.click(screen.getByRole("button", { name: "Open Who you know" }));
        fireEvent.click(screen.getByRole("button", { name: "Open your profile" }));

        expect(handlers.onOpenMemory).toHaveBeenCalledTimes(1);
        expect(handlers.onOpenPeople).toHaveBeenCalledTimes(1);
        expect(handlers.onOpenProfile).toHaveBeenCalledTimes(1);
    });

    it("opens the memory import dialog", () => {
        renderPage();
        fireEvent.click(screen.getByRole("button", { name: "Choose what to import" }));
        expect(screen.getByRole("dialog")).toHaveTextContent("Import dialog");
    });

    it("opens Who you know for Contacts consent without a generic permission request", async () => {
        mocks.getPermissions.mockResolvedValue(permissions(false));
        const handlers = renderPage();

        fireEvent.click(await screen.findByRole("button", { name: "Choose in Who you know" }));
        expect(handlers.onOpenPeople).toHaveBeenCalledTimes(1);
        expect(mocks.requestPermission).not.toHaveBeenCalled();
        expect(mocks.openPermissionSettings).not.toHaveBeenCalled();
    });

    it("shows nothing about Contacts when it is granted or not offered", async () => {
        mocks.getPermissions.mockResolvedValue(permissions(null));
        renderPage();
        await waitFor(() => expect(mocks.getPermissions).toHaveBeenCalled());
        expect(screen.queryByText("Recognize people from your Contacts")).not.toBeInTheDocument();
    });

    it("does not ask for Contacts where the computer cannot grant it", async () => {
        mocks.getPermissions.mockResolvedValue(windowsPermissions());
        renderPage();
        await waitFor(() => expect(mocks.getPermissions).toHaveBeenCalled());
        // Let the permission read land before checking that nothing rendered.
        await act(async () => {
            await Promise.resolve();
        });
        expect(screen.queryByText("Recognize people from your Contacts")).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Allow" })).not.toBeInTheDocument();
    });

    it("disables Prediction confidence with a reason while Learn by comparison is off", async () => {
        mocks.uiState = { triage_enable_shadow: "false" };
        renderPage();
        openAdvanced();

        const comparison = await screen.findByRole("switch", { name: "Learn by comparison" });
        await waitFor(() => expect(comparison).toHaveAttribute("aria-checked", "false"));
        const confidence = await screen.findByRole("switch", { name: "Prediction confidence" });
        expect(confidence).toBeDisabled();
        expect(screen.getByText("Turn on Learn by comparison to use this.")).toBeInTheDocument();

        fireEvent.click(comparison);
        expect(mocks.saveUIState).toHaveBeenCalledWith("triage_enable_shadow", "true");
        await waitFor(() => expect(confidence).toBeEnabled());
        expect(screen.queryByText("Turn on Learn by comparison to use this.")).not.toBeInTheDocument();
    });

    it("shows the chat preview and the search index in plain words under Advanced", async () => {
        renderPage();
        openAdvanced();

        expect(screen.getByText("Chat context preview")).toBeInTheDocument();
        const preview = screen.getByRole("heading", { name: "Preview chat context" });
        expect(preview).toHaveClass("settings-card-title");
        expect(preview.closest(".settings-card")).toContainElement(screen.getByText("Chat context preview"));
        const card = screen.getByRole("heading", { name: "Search index" }).closest(".settings-card");
        expect(card).not.toBeNull();
        expect(await within(card as HTMLElement).findByText("On this computer")).toBeInTheDocument();
        expect(within(card as HTMLElement).getByText("3 files, 12 passages")).toBeInTheDocument();
        expect(within(card as HTMLElement).getByRole("button", { name: /Save and rebuild/ })).toBeInTheDocument();
    });

    it("keeps every page string in both languages and out of the source", () => {
        const leaves = (value: unknown, prefix = ""): string[] =>
            value && typeof value === "object"
                ? Object.entries(value).flatMap(([key, child]) =>
                      leaves(child, prefix ? `${prefix}.${key}` : key),
                  )
                : [prefix];
        expect(leaves(zh.learningMemory).sort()).toEqual(leaves(en.learningMemory).sort());
        expect(leaves(zh.embedding).sort()).toEqual(leaves(en.embedding).sort());
        for (const source of [pageSource, featuresSource]) {
            expect(source).not.toMatch(/\bt\(\s*["'`][^"'`]+["'`]\s*,\s*["'`]/);
            expect(source).not.toMatch(/defaultValue/);
        }
    });
});
