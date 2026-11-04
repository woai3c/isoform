import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import type {
    MagpieInstallView,
    ModelAccountView,
    ModelAccountsStatus,
    ModelSidecarView,
} from "@/lib/wails-shim-app";
import en from "@/i18n/locales/en/settings.json";
import zh from "@/i18n/locales/zh-CN/settings.json";
import componentSource from "./model-accounts-card.tsx?raw";
import { ModelAccountsSettings } from "./model-accounts-card";

const mocks = vi.hoisted(() => ({
    getStatus: vi.fn(),
    accept: vi.fn(),
    revoke: vi.fn(),
    refresh: vi.fn(),
    setMode: vi.fn(),
    setOrder: vi.fn(),
    eventsOn: vi.fn(),
    unsubscribe: vi.fn(),
    toastSuccess: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    GetModelAccountsStatus: mocks.getStatus,
    AcceptManagedModelConsent: mocks.accept,
    RevokeManagedModelConsent: mocks.revoke,
    RefreshModelAccounts: mocks.refresh,
    SetAIProviderMode: mocks.setMode,
    SetManagedAccountOrder: mocks.setOrder,
}));

vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: mocks.eventsOn,
}));

vi.mock("sonner", () => ({
    toast: { success: mocks.toastSuccess },
}));

// Assistant health has its own suite and its own bindings.
vi.mock("./settings/assistant-health", () => ({
    AssistantHealthLines: () => <div data-testid="assistant-health-stub" />,
}));

// Who answers has its own suite and its own bindings.
vi.mock("./settings/assistant-choice", () => ({
    AssistantChoiceCard: ({ footer }: { footer?: React.ReactNode }) => <div data-testid="assistant-choice-stub">{footer}</div>,
}));
vi.mock("./settings/magpie-profile", () => ({ MagpieProfileCard: () => <div data-testid="magpie-profile-stub" /> }));

// The tools card has its own suite; here it only reports whether the gateway is in use.
vi.mock("./settings/tools-on-this-mac", () => ({
    ToolsOnThisMacCard: ({ gatewayInUse }: { gatewayInUse?: boolean }) => (
        <div data-testid="tools-card">{gatewayInUse ? "gateway in use" : "gateway not in use"}</div>
    ),
}));

const FORM_STUB = "Provider form stub";
const EMPTY_BODY =
    "Nothing to show yet. Sign in to Claude Code or Codex on this computer, or add your own key under Advanced.";
const OWN_KEY_NOTICE =
    "Isoform is using your own API key or model, set under Advanced, so signed-in assistants are not used.";
const GATEWAY_PROBLEM = "Isoform can't reach your assistants right now, so it can't think.";

const sidecarReady: ModelSidecarView = {
    available: true,
    running: true,
    healthy: true,
    degraded: false,
    port: 3425,
    version: "0.9.1",
    lastError: "",
    binaryPath: "/Applications/Isoform.app/Contents/Resources/magpie-bundle/magpie-cli",
};

const bundledInstall: MagpieInstallView = {
    kind: "bundled",
    version: "0.9.1",
    binaryPath: "",
    configDir: "",
    serverUrl: "http://127.0.0.1:3425",
    healthy: true,
    activeGroup: "",
    groups: [],
    agentModels: {},
};

function inHours(hours: number): string {
    return new Date(Date.now() + hours * 3_600_000).toISOString();
}

function inDays(days: number): string {
    return new Date(Date.now() + days * 86_400_000).toISOString();
}

function statusWith(overrides: Partial<ModelAccountsStatus> = {}): ModelAccountsStatus {
    return {
        mode: "managed",
        sidecar: sidecarReady,
        accounts: [],
        groups: [],
        consent: { required: false, accepted: false, version: 1, acceptedAt: "" },
        accountOrder: [],
        install: bundledInstall,
        ollama: false,
        byokRegistered: false,
        error: "",
        ...overrides,
    };
}

const consentRequired = statusWith({
    consent: { required: true, accepted: false, version: 1, acceptedAt: "" },
});
const consentGiven = statusWith({
    consent: { required: true, accepted: true, version: 1, acceptedAt: "2026-10-10T08:00:00Z" },
});

function claudeAccount(overrides: Partial<ModelAccountView> = {}): ModelAccountView {
    return {
        provider: "claude",
        name: "claude-max",
        kind: "subscription",
        plan: "max",
        user: "user@example.com",
        on: true,
        balance: "",
        windows: [
            { name: "5-hour", used: 30, remaining: 70, resetsAt: inHours(2) },
            { name: "Weekly", used: 80, remaining: 20, resetsAt: inDays(3) },
        ],
        readAt: "2026-10-10T08:00:00Z",
        ...overrides,
    };
}

function codexAccount(overrides: Partial<ModelAccountView> = {}): ModelAccountView {
    return {
        provider: "codex",
        name: "codex-pro",
        kind: "subscription",
        plan: "pro",
        user: "user@example.com",
        on: true,
        balance: "",
        windows: [],
        readAt: "2026-10-10T08:00:00Z",
        ...overrides,
    };
}

const isoformGroup = {
    id: "isoform",
    name: "Isoform",
    routing: "order",
    members: ["claude/claude-sonnet-5-5", "codex/gpt-6-astra"],
};

async function renderPage() {
    const view = render(<ModelAccountsSettings ownKeyForm={<p>{FORM_STUB}</p>} />);
    await waitFor(() => {
        expect(screen.queryByText("Checking your assistants...")).not.toBeInTheDocument();
    });
    return view;
}

function openAdvanced() {
    fireEvent.click(screen.getByRole("button", { name: /^Advanced/ }));
}

function cardOf(title: string): HTMLElement {
    const heading = screen.getByRole("heading", { name: title });
    const card = heading.closest(".settings-card");
    if (!(card instanceof HTMLElement)) throw new Error(`No card for ${title}`);
    return card;
}

/** The handler the page registered for a backend event. */
function subscriptionFor(eventName: string): () => void {
    const call = mocks.eventsOn.mock.calls.find(([name]) => name === eventName);
    if (!call) throw new Error(`No subscription for ${eventName}`);
    return call[1] as () => void;
}

const devTools = vi.hoisted(() => ({ on: true }));
vi.mock("@/components/settings/settings-dev-tools", async (original) => ({
    ...(await original<typeof import("@/components/settings/settings-dev-tools")>()),
    useSettingsDevTools: () => devTools.on,
}));
beforeEach(() => {
    devTools.on = true;
});

describe("ModelAccountsSettings (Settings > Assistant)", () => {
    beforeEach(async () => {
        // resetAllMocks also drops queued mockResolvedValueOnce / mockImplementationOnce values, so a
        // failing test cannot leak a pending status read into the next one.
        vi.resetAllMocks();
        window.localStorage.clear();
        await i18n.changeLanguage("en");
        mocks.getStatus.mockResolvedValue(statusWith());
        mocks.accept.mockRejectedValue(new Error("unexpected accept"));
        mocks.revoke.mockRejectedValue(new Error("unexpected revoke"));
        mocks.refresh.mockRejectedValue(new Error("unexpected refresh"));
        mocks.setMode.mockRejectedValue(new Error("unexpected mode"));
        mocks.setOrder.mockRejectedValue(new Error("unexpected order"));
        mocks.eventsOn.mockImplementation(() => mocks.unsubscribe);
    });

    it("renders when Go omits the empty lastError and binaryPath fields", async () => {
        // magpie_api.go tags both with omitempty, so a healthy gateway sends
        // neither; the status details must not read them as strings.
        const { lastError: _lastError, binaryPath: _binaryPath, ...sidecar } = sidecarReady;
        mocks.getStatus.mockResolvedValue(statusWith({ sidecar }));
        await renderPage();
        openAdvanced();

        expect(screen.getByTestId("assistant-status-details")).toBeInTheDocument();
    });

    it("opens with the everyday cards and keeps the technical parts under a closed Advanced", async () => {
        await renderPage();

        // Isoform runs the gateway: who answers is chosen in one card.
        expect(screen.getByTestId("assistant-choice-stub")).toBeInTheDocument();
        expect(screen.queryByRole("heading", { name: "Thinks with" })).not.toBeInTheDocument();
        expect(screen.getByRole("heading", { name: "Usage left" })).toBeInTheDocument();
        expect(screen.getByTestId("tools-card")).toHaveTextContent("gateway in use");
        expect(screen.queryByRole("alert")).not.toBeInTheDocument();

        const advanced = screen.getByRole("button", { name: /^Advanced/ });
        expect(advanced).toHaveTextContent("Your own API key or model, who handles what, and status details");
        expect(screen.queryByText(FORM_STUB)).not.toBeInTheDocument();
        expect(screen.queryByRole("heading", { name: "Who handles what" })).not.toBeInTheDocument();
        // The old segmented switch and its pill are gone.
        expect(screen.queryByRole("button", { name: "Managed accounts" })).not.toBeInTheDocument();
        expect(screen.queryByText("Ready")).not.toBeInTheDocument();
    });

    it("says which way Today's searches reach the web", async () => {
        mocks.getStatus.mockResolvedValue(statusWith({ researchVia: "claude_code" }));
        const { unmount } = await renderPage();
        expect(screen.getByTestId("research-route")).toHaveTextContent("web search of your Claude Code");
        unmount();

        mocks.getStatus.mockResolvedValue(statusWith({ researchVia: "opencli" }));
        await renderPage();
        expect(screen.getByTestId("research-route")).toHaveTextContent("Grok, Gemini or Doubao");
    });

    it("asks for consent in plain language and accepts version 1 when the user agrees", async () => {
        mocks.getStatus.mockResolvedValue(consentRequired);
        mocks.accept.mockResolvedValue(consentGiven);
        await renderPage();

        expect(screen.getByRole("heading", { name: "Use the subscriptions signed in on this computer?" })).toBeInTheDocument();
        expect(
            screen.getByText("Isoform can use the Claude Code and Codex subscriptions already signed in on this computer."),
        ).toBeInTheDocument();
        expect(screen.getByText(/Each provider's terms govern that use/)).toBeInTheDocument();
        expect(screen.getByText(/read the Claude Code sign-in from the Keychain/)).toBeInTheDocument();
        expect(screen.getByText("Isoform uses these sign-ins only on this computer, to send your requests.")).toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: "Use my signed-in accounts" }));
        await waitFor(() => expect(mocks.accept).toHaveBeenCalledWith(1));
        await waitFor(() =>
            expect(screen.queryByRole("button", { name: "Use my signed-in accounts" })).not.toBeInTheDocument(),
        );
    });

    it("hides the consent block for the visit when the user picks not now", async () => {
        mocks.getStatus.mockResolvedValue(consentRequired);
        await renderPage();
        fireEvent.click(screen.getByRole("button", { name: "Not now" }));
        expect(screen.queryByRole("button", { name: "Use my signed-in accounts" })).not.toBeInTheDocument();
        expect(mocks.accept).not.toHaveBeenCalled();
    });

    it("shows the accept error and keeps the consent block when the call fails", async () => {
        mocks.getStatus.mockResolvedValue(consentRequired);
        mocks.accept.mockRejectedValue(new Error("keychain denied"));
        await renderPage();
        fireEvent.click(screen.getByRole("button", { name: "Use my signed-in accounts" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("Could not save your choice. Try again.");
        expect(screen.getByRole("button", { name: "Use my signed-in accounts" })).toBeEnabled();
    });

    it("chooses who answers in one card, and keeps the usage card", async () => {
        mocks.getStatus.mockResolvedValue(
            statusWith({
                accounts: [claudeAccount(), codexAccount()],
                accountOrder: ["codex", "claude"],
                groups: [isoformGroup],
                install: { ...bundledInstall, activeGroup: "isoform" },
            }),
        );
        await renderPage();

        expect(screen.getByTestId("assistant-choice-stub")).toBeInTheDocument();
        expect(screen.getByRole("heading", { name: "Usage left" })).toBeInTheDocument();
        // The old order view and its Change picker are gone.
        expect(screen.queryByRole("heading", { name: "Thinks with" })).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Change" })).not.toBeInTheDocument();
    });
    it("names the user's own magpie group and sends order changes to magpie", async () => {
        mocks.getStatus.mockResolvedValue(
            statusWith({
                accounts: [claudeAccount()],
                accountOrder: ["claude"],
                install: {
                    ...bundledInstall,
                    kind: "user",
                    activeGroup: "isoform",
                    groups: [
                        { id: "never-stuck", name: "Never stuck", routing: "order", members: ["codex/gpt-6-astra"] },
                        { ...isoformGroup, members: ["codex/gpt-6-astra", "ollama/llama3.2"] },
                    ],
                },
            }),
        );
        await renderPage();

        expect(screen.getByTestId("magpie-profile-stub")).toBeInTheDocument();
        expect(screen.getByTestId("assistant-choice-stub")).toBeInTheDocument();
        expect(screen.queryByText(/To change the order, edit that group/)).not.toBeInTheDocument();
    });

    it("shows each assistant's model when the user's magpie has no Isoform group yet", async () => {
        mocks.getStatus.mockResolvedValue(
            statusWith({
                install: {
                    ...bundledInstall,
                    kind: "user",
                    agentModels: { claude: "claude/claude-haiku-5-5" },
                },
            }),
        );
        await renderPage();

        expect(screen.getByTestId("magpie-profile-stub")).toBeInTheDocument();
    });

    it.each([
        ["en", ["Isoform chat", "Isoform background", "Isoform light"]],
        ["zh-CN", ["Isoform 对话", "Isoform 后台", "Isoform 轻量任务"]],
    ] as const)("localizes original group names in everyday and routing cards in %s", async (locale, labels) => {
        await i18n.changeLanguage(locale);
        window.localStorage.setItem("yansu.settings.advanced.ai-provider", "1");
        const groups = ["chat", "background", "light"].map((kind) => ({
            id: `yansu-${kind}`,
            name: `yansu ${kind}`,
            routing: "order",
            members: ["codex/gpt-6-astra"],
        }));
        const initial = statusWith({
            groups,
            install: { ...bundledInstall, activeGroup: "yansu-chat" },
        });
        mocks.getStatus.mockResolvedValue(initial);
        const bundled = await renderPage();
        const tSettings = i18n.getFixedT(locale, "settings");
        await screen.findByRole("heading", { name: tSettings("modelAccounts.routingTitle") });
        const routing = cardOf(tSettings("modelAccounts.routingTitle"));
        for (const label of labels) {
            expect(within(routing).getByText(tSettings("modelAccounts.routingVia", { group: label }))).toBeInTheDocument();
        }
        bundled.unmount();

        mocks.getStatus.mockResolvedValue({
            ...initial,
            install: { ...initial.install, kind: "user", groups },
        });
        await renderPage();
        expect(await screen.findByTestId("magpie-profile-stub")).toBeInTheDocument();
        expect(within(cardOf(tSettings("modelAccounts.routingTitle"))).getAllByText(
            tSettings("modelAccounts.routingVia", { group: labels[0] }),
        )).toHaveLength(3);
        expect(within(screen.getByTestId("assistant-status-details")).getByText("yansu-chat")).toBeInTheDocument();
        expect(mocks.setOrder).not.toHaveBeenCalled();
    });

    it.each([
        ["yansu-chat", "My chosen assistant"],
        ["my-chat", "yansu chat"],
    ])("preserves the custom group %s named %s in both cards", async (id, name) => {
        window.localStorage.setItem("yansu.settings.advanced.ai-provider", "1");
        mocks.getStatus.mockResolvedValue(statusWith({
            install: {
                ...bundledInstall,
                kind: "user",
                activeGroup: id,
                groups: [{ id, name, routing: "order", members: ["codex/gpt-6-astra"] }],
            },
        }));
        await renderPage();

        expect(screen.getByTestId("magpie-profile-stub")).toBeInTheDocument();
        expect(within(cardOf("Who handles what")).getAllByText(`${name} group`)).toHaveLength(3);
        expect(within(screen.getByTestId("assistant-status-details")).getByText(id)).toBeInTheDocument();
        expect(mocks.setOrder).not.toHaveBeenCalled();
    });

    it("says the own key is in use with a reason, shows no agent choice and opens Advanced on the form", async () => {
        mocks.getStatus.mockResolvedValue(
            statusWith({ mode: "advanced", accounts: [codexAccount()], accountOrder: ["codex"] }),
        );
        await renderPage();

        expect(screen.getByText(OWN_KEY_NOTICE)).toBeInTheDocument();
        expect(
            screen.getByText("To choose an assistant here, turn off Use your own API key or model under Advanced."),
        ).toBeInTheDocument();
        expect(screen.queryByTestId("assistant-choice-stub")).not.toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "Change" })).not.toBeInTheDocument();
        expect(screen.getByTestId("tools-card")).toHaveTextContent("gateway not in use");
        // Signed-in accounts are not in use, so nothing says In use.
        expect(screen.queryByText("In use")).not.toBeInTheDocument();
        expect(screen.getByRole("switch", { name: "Use your own API key or model" })).toBeChecked();
        expect(screen.getByText(FORM_STUB)).toBeInTheDocument();
        expect(screen.queryByRole("heading", { name: "Who handles what" })).not.toBeInTheDocument();
    });

    it("turns on the own key through SetAIProviderMode, refetches and reveals the form", async () => {
        mocks.setMode.mockResolvedValue(undefined);
        mocks.getStatus
            .mockResolvedValueOnce(statusWith())
            .mockResolvedValueOnce(statusWith({ mode: "advanced" }));
        await renderPage();
        openAdvanced();

        const toggle = screen.getByRole("switch", { name: "Use your own API key or model" });
        expect(toggle).not.toBeChecked();
        expect(screen.queryByText(FORM_STUB)).not.toBeInTheDocument();
        fireEvent.click(toggle);

        await waitFor(() => expect(mocks.setMode).toHaveBeenCalledWith("advanced"));
        await waitFor(() => expect(mocks.getStatus).toHaveBeenCalledTimes(2));
        expect(await screen.findByText(FORM_STUB)).toBeInTheDocument();
        expect(screen.getByRole("switch", { name: "Use your own API key or model" })).toBeChecked();
    });

    it("turns the own key off through SetAIProviderMode and shows the error when it fails", async () => {
        mocks.getStatus.mockResolvedValue(statusWith({ mode: "external" }));
        mocks.setMode.mockRejectedValueOnce(new Error("agent failed"));
        await renderPage();

        fireEvent.click(screen.getByRole("switch", { name: "Use your own API key or model" }));
        await waitFor(() => expect(mocks.setMode).toHaveBeenCalledWith("managed"));
        expect(await screen.findByRole("alert")).toHaveTextContent("Could not change this setting. Try again.");
    });

    it("refetches on ai:config-changed so a key saved under Advanced shows as on", async () => {
        await renderPage();
        mocks.getStatus.mockResolvedValueOnce(statusWith({ mode: "advanced" }));

        await act(async () => {
            subscriptionFor("ai:config-changed")();
        });

        expect(await screen.findByText(OWN_KEY_NOTICE)).toBeInTheDocument();
        expect(mocks.getStatus).toHaveBeenCalledTimes(2);
    });

    it("shows remaining percentages and reset times for each quota window", async () => {
        mocks.getStatus.mockResolvedValue(statusWith({ accounts: [claudeAccount()] }));
        await renderPage();

        const usage = cardOf("Usage left");
        expect(within(usage).getByText("Claude Code")).toBeInTheDocument();
        expect(within(usage).getByText("Max plan")).toBeInTheDocument();
        expect(within(usage).getByText("5-hour limit")).toBeInTheDocument();
        expect(within(usage).getByText("Weekly limit")).toBeInTheDocument();
        expect(within(usage).getByText("70% left")).toBeInTheDocument();
        expect(within(usage).getByText("20% left")).toBeInTheDocument();
        expect(within(usage).getByText("resets in 2 hours")).toBeInTheDocument();
        expect(within(usage).getByText("resets in 3 days")).toBeInTheDocument();
        expect(screen.getAllByRole("progressbar").map((bar) => bar.getAttribute("aria-valuenow"))).toEqual([
            "70",
            "20",
        ]);
    });

    it.each([
        ["en", ["5-hour limit", "Weekly limit", "Daily limit"]],
        ["zh-CN", ["5 小时额度", "每周额度", "每日额度"]],
    ])("says each window as a limit, never the model's name after the dot, in %s (ST61)", async (language, labels) => {
        await i18n.changeLanguage(language);
        try {
            mocks.getStatus.mockResolvedValue(
                statusWith({
                    accounts: [
                        claudeAccount({
                            windows: [
                                { name: "5 hours", used: 30, remaining: 70, resetsAt: inHours(2) },
                                { name: "7 days · Fable", used: 80, remaining: 20, resetsAt: inDays(3) },
                                { name: "1 day", used: 10, remaining: 90, resetsAt: inHours(9) },
                            ],
                        }),
                    ],
                }),
            );
            render(<ModelAccountsSettings ownKeyForm={null} />);
            const bars = await screen.findAllByRole("progressbar");
            expect(bars).toHaveLength(3);
            for (const label of labels) expect(screen.getByText(label)).toBeInTheDocument();
            expect(document.body).not.toHaveTextContent(/Fable|7 days|1 day|5 hours/);
        } finally {
            await i18n.changeLanguage("en");
        }
    });

    it("hides the stale number and bar for a window whose reset time has passed", async () => {
        mocks.getStatus.mockResolvedValue(
            statusWith({
                accounts: [
                    claudeAccount({
                        windows: [
                            { name: "5-hour", used: 30, remaining: 70, resetsAt: inHours(-1) },
                            { name: "Weekly", used: 80, remaining: 20, resetsAt: inDays(3) },
                        ],
                    }),
                ],
            }),
        );
        await renderPage();

        expect(screen.getByText("Window reset, refreshing")).toBeInTheDocument();
        expect(screen.queryByText("70% left")).not.toBeInTheDocument();
        expect(screen.queryByText(/resets .* ago/)).not.toBeInTheDocument();
        expect(screen.getByText("20% left")).toBeInTheDocument();
        expect(screen.getAllByRole("progressbar").map((bar) => bar.getAttribute("aria-valuenow"))).toEqual(["20"]);
    });

    it("shows In use without a number for accounts with no windows, plus balance and Off", async () => {
        mocks.getStatus.mockResolvedValue(
            statusWith({
                accounts: [
                    codexAccount({ balance: "$12.40" }),
                    codexAccount({ provider: "Codex backup", name: "codex-backup", on: false }),
                ],
            }),
        );
        await renderPage();

        const usage = cardOf("Usage left");
        expect(within(usage).getByText("In use")).toBeInTheDocument();
        expect(within(usage).getByText("Balance $12.40")).toBeInTheDocument();
        expect(within(usage).getByText("Off")).toBeInTheDocument();
        expect(within(usage).getByText("Codex backup")).toBeInTheDocument();
        expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
    });

    it("explains how to make models available when nothing is signed in", async () => {
        await renderPage();
        expect(screen.getByText(EMPTY_BODY)).toBeInTheDocument();
    });

    it("keeps local models and the registered key as rows", async () => {
        mocks.getStatus.mockResolvedValue(statusWith({ ollama: true, byokRegistered: true }));
        await renderPage();
        const usage = cardOf("Usage left");
        expect(within(usage).getByText("Local models")).toBeInTheDocument();
        expect(within(usage).getByText("Ollama on this computer")).toBeInTheDocument();
        expect(within(usage).getByText("Your API key")).toBeInTheDocument();
        expect(within(usage).getByText("Added under Advanced")).toBeInTheDocument();
        expect(within(usage).getAllByText("In use")).toHaveLength(2);
    });

    it("refreshes the usage through RefreshModelAccounts", async () => {
        mocks.getStatus.mockResolvedValue(statusWith({ accounts: [claudeAccount()] }));
        mocks.refresh.mockResolvedValue(statusWith({ accounts: [codexAccount()] }));
        await renderPage();
        fireEvent.click(within(cardOf("Usage left")).getByRole("button", { name: "Refresh" }));
        await waitFor(() => expect(mocks.refresh).toHaveBeenCalledTimes(1));
        expect(await within(cardOf("Usage left")).findByText("Codex")).toBeInTheDocument();
        expect(within(cardOf("Usage left")).queryByText("Claude Code")).not.toBeInTheDocument();
    });

    it.each<[string, Partial<ModelSidecarView>]>([
        ["degraded", { healthy: false, degraded: true, lastError: "magpie exited with code 2" }],
        ["unavailable", { available: false, running: false, healthy: false, lastError: "magpie binary not found" }],
    ])("shows one plain problem line with Try again when the gateway is %s", async (_state, overrides) => {
        mocks.getStatus.mockResolvedValue(statusWith({ sidecar: { ...sidecarReady, ...overrides } }));
        mocks.refresh.mockResolvedValue(statusWith());
        await renderPage();

        expect(screen.getByRole("alert")).toHaveTextContent(GATEWAY_PROBLEM);
        // The raw reason stays under Advanced, not in the everyday view.
        expect(screen.queryByText(/magpie (exited|binary)/)).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Try again" }));
        await waitFor(() => expect(mocks.refresh).toHaveBeenCalledTimes(1));
        await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
    });

    it.each<[string, Partial<ModelAccountsStatus>]>([
        ["the gateway is starting", { sidecar: { ...sidecarReady, healthy: false, running: true } }],
        ["the own key is on", { mode: "advanced", sidecar: { ...sidecarReady, healthy: false, degraded: true } }],
    ])("shows no problem line when %s", async (_case, overrides) => {
        mocks.getStatus.mockResolvedValue(statusWith(overrides));
        await renderPage();
        expect(screen.queryByText(GATEWAY_PROBLEM)).not.toBeInTheDocument();
    });

    it("shows a status error as a plain line and keeps the raw error under Advanced", async () => {
        mocks.getStatus.mockResolvedValue(statusWith({ error: "quota poll failed: 503" }));
        mocks.refresh.mockResolvedValue(statusWith({ accounts: [codexAccount()] }));
        await renderPage();

        expect(screen.getByRole("alert")).toHaveTextContent("Some usage details could not be checked.");
        expect(screen.queryByText(EMPTY_BODY)).not.toBeInTheDocument();
        openAdvanced();
        expect(within(screen.getByTestId("assistant-status-details")).getByText("quota poll failed: 503")).toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: "Try again" }));
        await waitFor(() => expect(mocks.refresh).toHaveBeenCalledTimes(1));
        expect(await screen.findByText("In use")).toBeInTheDocument();
        expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    });

    it("shows a retry when the status cannot be loaded and keeps the key form reachable", async () => {
        mocks.getStatus
            .mockRejectedValueOnce(new Error("bridge down"))
            .mockResolvedValueOnce(statusWith({ accounts: [codexAccount()] }));
        render(<ModelAccountsSettings ownKeyForm={<p>{FORM_STUB}</p>} />);

        expect(await screen.findByRole("alert")).toHaveTextContent("Could not check your assistants. Try again.");
        openAdvanced();
        expect(screen.getByText(FORM_STUB)).toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: "Try again" }));
        expect(await screen.findByText("In use")).toBeInTheDocument();
    });

    it("reads who handles what from Isoform's group for chat and background work", async () => {
        mocks.getStatus.mockResolvedValue(
            statusWith({
                accounts: [claudeAccount(), codexAccount()],
                byokRegistered: true,
                groups: [
                    { id: "yansu-chat", name: "yansu chat", routing: "order", members: ["codex/gpt-6-astra"] },
                    { id: "yansu-background", name: "yansu background", routing: "pace", members: [] },
                    { id: "yansu-light", name: "yansu light", routing: "usage", members: ["yansu-byok/gpt-4o-mini"] },
                    isoformGroup,
                ],
                install: { ...bundledInstall, activeGroup: "isoform" },
            }),
        );
        await renderPage();
        openAdvanced();

        const routing = cardOf("Who handles what");
        expect(within(routing).getByText(/^Chat: Claude Code \(claude-sonnet-5-5\), then Codex \(gpt-6-astra\)/)).toBeInTheDocument();
        expect(
            within(routing).getByText(/^Background work: Claude Code \(claude-sonnet-5-5\), then Codex \(gpt-6-astra\)/),
        ).toBeInTheDocument();
        expect(within(routing).getByText(/^Quick tasks: your key \(gpt-4o-mini\)/)).toBeInTheDocument();
        expect(within(routing).getAllByText("Isoform group")).toHaveLength(2);
        expect(within(routing).getByText("Isoform light group")).toBeInTheDocument();
    });

    it("reads the yansu groups without Isoform's group, including legacy member names", async () => {
        mocks.getStatus.mockResolvedValue(
            statusWith({
                accounts: [
                    claudeAccount({ provider: "Claude", plan: "Max 5x" }),
                    codexAccount({ provider: "Codex", plan: "Pro" }),
                ],
                byokRegistered: true,
                groups: [
                    { id: "yansu-chat", name: "Chat", routing: "order", members: ["claude-max", "codex-pro", "byok"] },
                    { id: "yansu-background", name: "Background", routing: "pace", members: [] },
                    { id: "yansu-light", name: "Light", routing: "usage", members: ["byok"] },
                ],
            }),
        );
        await renderPage();
        openAdvanced();

        const routing = cardOf("Who handles what");
        expect(within(routing).getByText(/^Chat: Claude Max 5x, then Codex Pro, then your key/)).toBeInTheDocument();
        expect(within(routing).getByText(/^Background work: No models yet/)).toBeInTheDocument();
        expect(within(routing).getByText(/^Quick tasks: your key/)).toBeInTheDocument();
    });

    it("asks before withdrawing the agreement and withdraws on confirm", async () => {
        mocks.getStatus.mockResolvedValue(consentGiven);
        mocks.revoke.mockResolvedValue(consentRequired);
        await renderPage();
        openAdvanced();

        const card = cardOf("Signed-in subscriptions");
        expect(within(card).getByText(/You agreed that Isoform may use/)).toBeInTheDocument();
        fireEvent.click(within(card).getByRole("button", { name: "Withdraw" }));

        const dialog = await screen.findByRole("dialog");
        expect(dialog).toHaveTextContent("Withdraw your agreement?");
        expect(mocks.revoke).not.toHaveBeenCalled();
        fireEvent.click(within(dialog).getByRole("button", { name: "Withdraw" }));

        await waitFor(() => expect(mocks.revoke).toHaveBeenCalledTimes(1));
        expect(await screen.findByRole("button", { name: "Use my signed-in accounts" })).toBeInTheDocument();
    });

    it("keeps the agreement when the confirmation is cancelled", async () => {
        mocks.getStatus.mockResolvedValue(consentGiven);
        await renderPage();
        openAdvanced();

        fireEvent.click(within(cardOf("Signed-in subscriptions")).getByRole("button", { name: "Withdraw" }));
        const dialog = await screen.findByRole("dialog");
        fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));

        await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
        expect(mocks.revoke).not.toHaveBeenCalled();
    });

    it("lists the raw gateway status and copies it", async () => {
        const writeText = vi.fn().mockResolvedValue(undefined);
        Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
        mocks.getStatus.mockResolvedValue(
            statusWith({
                sidecar: { ...sidecarReady, healthy: false, degraded: true, lastError: "magpie exited with code 2" },
                install: { ...bundledInstall, activeGroup: "isoform" },
            }),
        );
        await renderPage();
        openAdvanced();

        const details = screen.getByTestId("assistant-status-details");
        expect(within(details).getByText("Isoform's built-in magpie 0.9.1")).toBeInTheDocument();
        expect(within(details).getByText("Degraded")).toBeInTheDocument();
        expect(within(details).getByText("isoform")).toBeInTheDocument();
        expect(within(details).getByText("http://127.0.0.1:3425")).toBeInTheDocument();
        expect(within(details).getByText(sidecarReady.binaryPath ?? "")).toBeInTheDocument();
        expect(within(details).getByText("magpie exited with code 2")).toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: "Copy details" }));
        await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
        expect(writeText.mock.calls[0][0]).toContain("Last error: magpie exited with code 2");
        expect(mocks.toastSuccess).toHaveBeenCalledWith("Status details copied");
    });

    it("subscribes to both backend events, refetches on them, and unsubscribes on unmount", async () => {
        const view = await renderPage();
        expect(mocks.eventsOn).toHaveBeenCalledWith("model-accounts:updated", expect.any(Function));
        expect(mocks.eventsOn).toHaveBeenCalledWith("ai:config-changed", expect.any(Function));
        const handler = subscriptionFor("model-accounts:updated");

        mocks.getStatus.mockResolvedValueOnce(statusWith({ accounts: [claudeAccount()] }));
        await act(async () => {
            handler();
        });
        expect(await screen.findByText("Max plan")).toBeInTheDocument();

        view.unmount();
        expect(mocks.unsubscribe).toHaveBeenCalledTimes(2);
    });

    it("renders the Chinese copy in the zh-CN locale", async () => {
        await act(async () => {
            await i18n.changeLanguage("zh-CN");
        });
        mocks.getStatus.mockResolvedValue(consentRequired);
        const view = await renderPage();
        try {
            expect(screen.getByTestId("assistant-choice-stub")).toBeInTheDocument();
            expect(screen.getByRole("heading", { name: "剩余用量" })).toBeInTheDocument();
            expect(screen.getByRole("button", { name: "使用我已登录的账户" })).toBeInTheDocument();
        } finally {
            view.unmount();
            await act(async () => {
                await i18n.changeLanguage("en");
            });
        }
    });

    it("keeps every modelAccounts key used by the component in both locales", () => {
        const used = new Set(
            [...componentSource.matchAll(/["'`](modelAccounts\.[A-Za-z0-9]+)["'`]/g)].map(
                (match) => match[1].slice("modelAccounts.".length),
            ),
        );
        const enKeys = Object.keys(en.modelAccounts).sort();
        const zhKeys = Object.keys(zh.modelAccounts).sort();
        expect([...used].sort()).toEqual(enKeys);
        expect(zhKeys).toEqual(enKeys);
    });

    it("never passes fallback strings to translation calls", () => {
        expect(componentSource).not.toMatch(/\bt\(\s*["'`][A-Za-z:.]+["'`]\s*,\s*["'`]/);
        expect(componentSource).not.toMatch(/defaultValue/);
    });
});

describe("plan names (ST70)", () => {
    it("says Max plan, Pro plan and Kimi Code, never the raw label", async () => {
        const { planDisplayName } = await import("./model-accounts-card");
        const plan = (p: string) => `${p} plan`;
        expect(planDisplayName("max", plan)).toBe("Max plan");
        expect(planDisplayName("pro", plan)).toBe("Pro plan");
        expect(planDisplayName("kimi-code", plan)).toBe("Kimi Code");
    });
});

// ST13: Status details (raw gateway status) are developer-only.
describe("ModelAccountsSettings without Developer tools", () => {
    it("hides Status details", async () => {
        devTools.on = false;
        await renderPage();
        expect(screen.queryByTestId("assistant-status-details")).not.toBeInTheDocument();
        expect(screen.queryByText(en.modelAccounts.detailsTitle)).not.toBeInTheDocument();
    });
});
