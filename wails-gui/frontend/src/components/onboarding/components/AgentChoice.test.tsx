import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@/i18n";
import type { ModelAccountsStatus } from "@/lib/wails-shim-app";
import { AgentChoice, assistantsReady } from "./AgentChoice";

const mocks = vi.hoisted(() => ({
    getStatus: vi.fn(),
    setOrder: vi.fn(),
    acceptConsent: vi.fn(),
    refresh: vi.fn(),
    eventsOn: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    GetModelAccountsStatus: mocks.getStatus,
    SetManagedAccountOrder: mocks.setOrder,
    AcceptManagedModelConsent: mocks.acceptConsent,
    RefreshModelAccounts: mocks.refresh,
}));

vi.mock("../../../../wailsjs/runtime/runtime", () => ({
    EventsOn: mocks.eventsOn,
}));

function status(overrides: Partial<ModelAccountsStatus> = {}): ModelAccountsStatus {
    return {
        mode: "managed",
        sidecar: {
            available: true,
            running: true,
            healthy: true,
            degraded: false,
            port: 3425,
            version: "1.0.0",
            lastError: "",
            binaryPath: "",
        },
        accounts: [
            {
                provider: "claude",
                name: "Claude",
                kind: "subscription",
                plan: "max",
                user: "bo@example.com",
                on: true,
                balance: "",
                windows: [],
                readAt: "",
            },
            {
                provider: "codex",
                name: "Codex",
                kind: "subscription",
                plan: "pro",
                user: "bo@example.com",
                on: true,
                balance: "",
                windows: [],
                readAt: "",
            },
            {
                provider: "gemini",
                name: "Gemini",
                kind: "subscription",
                plan: "",
                user: "Google",
                on: true,
                balance: "",
                windows: [],
                readAt: "",
            },
        ],
        groups: [],
        consent: { required: true, accepted: false, version: 1, acceptedAt: "" },
        accountOrder: ["claude", "codex"],
        install: {
            kind: "bundled",
            version: "0.1.1172",
            binaryPath: "",
            configDir: "",
            serverUrl: "http://127.0.0.1:59289",
            healthy: true,
            activeGroup: "",
            groups: [],
            agentModels: {},
        },
        ollama: false,
        byokRegistered: false,
        error: "",
        ...overrides,
    } as ModelAccountsStatus;
}

const readyStatus = status({
    consent: { required: false, accepted: true, version: 1, acceptedAt: "2026-10-12T18:00:00Z" },
    groups: [
        { id: "yansu-chat", name: "yansu chat", routing: "order", members: ["codex/gpt-5.6-sol", "claude/claude-sonnet-5-5"] },
    ],
    accountOrder: ["codex", "claude"],
});

// The cards render before an effect picks the primary, and React may commit
// them a task earlier than that effect, so wait for the pick to land.
async function expectPrimary(id: string) {
    await waitFor(() => expect(screen.getByTestId(`agent-card-${id}`)).toHaveAttribute("aria-checked", "true"));
}

describe("AgentChoice", () => {
    beforeEach(() => {
        vi.clearAllMocks();
        mocks.eventsOn.mockReturnValue(() => undefined);
    });

    it("lists only the assistants the groups can use and applies the order plus consent", async () => {
        mocks.getStatus.mockResolvedValue(status());
        mocks.setOrder.mockResolvedValue(status({ accountOrder: ["codex", "claude"] }));
        mocks.acceptConsent.mockResolvedValue(readyStatus);
        const onReady = vi.fn();

        render(<AgentChoice onReady={onReady} />);

        expect(await screen.findByTestId("agent-card-claude")).toBeInTheDocument();
        expect(screen.getByTestId("agent-card-codex")).toBeInTheDocument();
        expect(screen.queryByText("Gemini")).not.toBeInTheDocument();
        // The saved order picks the primary.
        await expectPrimary("claude");

        fireEvent.click(screen.getByTestId("agent-card-codex"));
        expect(screen.getByTestId("agent-card-codex")).toHaveAttribute("aria-checked", "true");
        fireEvent.click(screen.getByTestId("agent-apply"));

        await waitFor(() => expect(mocks.setOrder).toHaveBeenCalledWith(["codex", "claude"]));
        await waitFor(() => expect(mocks.acceptConsent).toHaveBeenCalledWith(1));
        expect(await screen.findByTestId("agent-choice-ready")).toHaveTextContent("Codex");
        // onReady runs from an effect after the ready banner commits.
        await waitFor(() => expect(onReady).toHaveBeenCalledWith(["codex", "claude"]));
    });

    it("drops the backup when the box is unchecked", async () => {
        mocks.getStatus.mockResolvedValue(status());
        mocks.setOrder.mockResolvedValue(readyStatus);

        render(<AgentChoice />);

        // The backup box appears once the primary is picked.
        fireEvent.click(await screen.findByRole("checkbox", { name: /Use Codex as backup/ }));
        fireEvent.click(screen.getByTestId("agent-apply"));

        await waitFor(() => expect(mocks.setOrder).toHaveBeenCalledWith(["claude"]));
        // Consent was already accepted in the returned status, so no second call.
        expect(mocks.acceptConsent).not.toHaveBeenCalled();
    });

    it("opens the picker right away with startChanging and reports the applied status", async () => {
        mocks.getStatus.mockResolvedValue(readyStatus);
        const applied = { ...readyStatus, accountOrder: ["claude", "codex"] };
        mocks.setOrder.mockResolvedValue(applied);
        const onApplied = vi.fn();

        render(<AgentChoice startChanging onApplied={onApplied} />);

        await expectPrimary("codex");
        expect(screen.queryByTestId("agent-choice-ready")).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Back" })).not.toBeInTheDocument();

        fireEvent.click(screen.getByTestId("agent-card-claude"));
        fireEvent.click(screen.getByTestId("agent-apply"));

        await waitFor(() => expect(mocks.setOrder).toHaveBeenCalledWith(["claude", "codex"]));
        await waitFor(() => expect(onApplied).toHaveBeenCalledWith(applied));
        expect(mocks.acceptConsent).not.toHaveBeenCalled();
    });

    it("keeps the ready banner and the Back link without startChanging", async () => {
        mocks.getStatus.mockResolvedValue(readyStatus);

        render(<AgentChoice />);

        expect(await screen.findByTestId("agent-choice-ready")).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Change" }));
        expect(await screen.findByRole("button", { name: "Back" })).toBeInTheDocument();
    });

    it("explains how to sign in when no assistant is available", async () => {
        mocks.getStatus.mockResolvedValue(status({ accounts: [] }));
        mocks.refresh.mockResolvedValue(status());

        render(<AgentChoice />);

        expect(await screen.findByTestId("agent-choice-empty")).toBeInTheDocument();
        expect(screen.getByText("No signed-in assistant yet")).toBeInTheDocument();
        fireEvent.click(screen.getByTestId("agent-check-again"));
        await waitFor(() => expect(mocks.refresh).toHaveBeenCalledTimes(1));
        expect(await screen.findByTestId("agent-card-claude")).toBeInTheDocument();
    });

    it("reports ready once the chat group has members, consent or not", () => {
        expect(assistantsReady(null)).toBe(false);
        expect(assistantsReady(status())).toBe(false);
        expect(
            assistantsReady(
                status({
                    groups: [{ id: "yansu-chat", name: "yansu chat", routing: "order", members: [] }],
                }),
            ),
        ).toBe(false);
        expect(
            assistantsReady(
                status({
                    groups: [{ id: "yansu-chat", name: "yansu chat", routing: "order", members: ["claude/claude-sonnet-5-5"] }],
                }),
            ),
        ).toBe(true);
        expect(assistantsReady(readyStatus)).toBe(true);
    });

    it.each([
        ["isoform", "Isoform", "Isoform"],
        ["yansu-chat", "yansu chat", "Isoform chat"],
        ["yansu-chat", "My daily work", "My daily work"],
    ])("defers to the user's own magpie with group %s named %s", async (id, name, label) => {
        const userStatus = status({
            install: {
                kind: "user",
                version: "0.1.1194",
                binaryPath: "/Users/example/.local/bin/magpie",
                configDir: "/Users/example/.config/magpie",
                serverUrl: "http://127.0.0.1:3425",
                healthy: true,
                activeGroup: id,
                groups: [
                    { id: "never-stuck", name: "Never stuck", routing: "order", members: ["claude/claude-sonnet-5-5", "codex/gpt-6-astra"] },
                    { id, name, routing: "order", members: ["claude/claude-sonnet-5-5", "codex/gpt-6-astra"] },
                ],
                agentModels: { claude: "claude/claude-haiku-5-5", codex: "codex/gpt-6-astra" },
            },
        });
        mocks.getStatus.mockResolvedValue(userStatus);
        const onReady = vi.fn();

        render(<AgentChoice onReady={onReady} />);

        expect(await screen.findByTestId("agent-choice-user-magpie")).toBeInTheDocument();
        expect(screen.getByText(`Group: ${label}`)).toBeInTheDocument();
        expect(screen.queryByTestId("agent-card-claude")).not.toBeInTheDocument();
        await waitFor(() => expect(onReady).toHaveBeenCalledTimes(1));
        expect(mocks.setOrder).not.toHaveBeenCalled();
        expect(assistantsReady(userStatus)).toBe(true);
    });
});
