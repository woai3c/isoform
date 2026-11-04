import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import type { AgentChoiceView, AssistantChoiceView } from "@/lib/wails-shim-app";
import { AssistantChoiceCard } from "./assistant-choice";

const mocks = vi.hoisted(() => ({
    getChoice: vi.fn(),
    setAnswering: vi.fn(),
    setModel: vi.fn(),
    setEffort: vi.fn(),
    setBackground: vi.fn(),
    signIn: vi.fn(),
    getHealth: vi.fn(),
    retryHealth: vi.fn(),
    eventsOn: vi.fn(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    GetAssistantChoice: mocks.getChoice,
    SetAnsweringAgent: mocks.setAnswering,
    SetAgentModel: mocks.setModel,
    SetAgentEffort: mocks.setEffort,
    SetBackgroundUsesChoice: mocks.setBackground,
    OpenAssistantSignIn: mocks.signIn,
    GetAssistantHealth: mocks.getHealth,
    RetryAssistantHealth: mocks.retryHealth,
}));

vi.mock("../../../wailsjs/runtime/runtime", () => ({
    EventsOn: mocks.eventsOn,
}));

const FIVE = ["low", "medium", "high", "xhigh", "max"];

function agentRow(agent: string, overrides: Partial<AgentChoiceView> = {}): AgentChoiceView {
    return { agent, canAnswer: agent !== "codex" && agent !== "deepseek", model: "", effort: "high", models: [], modelEfforts: {}, efforts: FIVE, ...overrides };
}

function choice(overrides: Partial<AssistantChoiceView> = {}): AssistantChoiceView {
    return {
        answering: "claude-code",
        editable: true,
        backgroundUsesChoice: false,
        isoformReady: true,
        agents: [
            agentRow("claude-code", {
                models: ["claude/claude-opus-5-5", "claude/claude-sonnet-5-5"],
                modelEfforts: { "claude/claude-opus-5-5": FIVE, "claude/claude-sonnet-5-5": ["low", "medium", "high"] },
            }),
            agentRow("codex", { models: ["codex/gpt-6-astra"], efforts: ["low", "medium", "high", "xhigh"] }),
            agentRow("deepseek", { effort: "", efforts: [] }),
            agentRow("isoform", { models: ["claude/claude-opus-5-5", "codex/gpt-6-astra"] }),
        ],
        ...overrides,
    };
}

const healthy = [
    { assistant: "claude-code", code: "ready", checking: false },
    { assistant: "codex", code: "tools_unverified", checking: false },
];

beforeEach(async () => {
    vi.clearAllMocks();
    mocks.eventsOn.mockReturnValue(() => undefined);
    mocks.getChoice.mockResolvedValue(choice());
    mocks.getHealth.mockResolvedValue(healthy);
    await i18n.changeLanguage("en");
});
afterEach(cleanup);

function rowOf(name: string): HTMLElement {
    return screen.getByRole("radio", { name: new RegExp(`^${name}`) }).closest("div.rounded-lg") as HTMLElement;
}

function pick(trigger: HTMLElement, option: string) {
    fireEvent.keyDown(trigger, { key: "ArrowDown" });
    fireEvent.click(screen.getByRole("option", { name: option }));
}

describe("AssistantChoiceCard", () => {
    it("shows one pick from three rows, each with its state, and says the scope", async () => {
        render(<AssistantChoiceCard />);
        expect(await screen.findByRole("heading", { name: "Who answers in Isoform" })).toBeInTheDocument();
        expect(screen.getByRole("radio", { name: /^Claude Code/ })).toBeChecked();
        await waitFor(() => expect(within(rowOf("Claude Code")).getByText("Signed in and working")).toBeInTheDocument());
        expect(screen.queryByRole("radio", { name: /^Codex/ })).not.toBeInTheDocument();
        expect(within(rowOf("Isoform")).getByText("Always available")).toBeInTheDocument();
        expect(screen.getByText("If your choice is unavailable, Isoform answers instead.")).toBeInTheDocument();
        expect(screen.getByText("Applies only to Isoform. Your own Claude Code and Codex settings stay as they are.")).toBeInTheDocument();
        // No reorder list and no prompt editing.
        expect(screen.queryByRole("button", { name: /Change|Reorder/ })).not.toBeInTheDocument();
        expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    });

    it("saves a new pick at once and says Saved", async () => {
        mocks.setAnswering.mockResolvedValue(choice({ answering: "isoform" }));
        render(<AssistantChoiceCard />);
        fireEvent.click(await screen.findByRole("radio", { name: /^Isoform/ }));
        await waitFor(() => expect(mocks.setAnswering).toHaveBeenCalledWith("isoform"));
        expect(await screen.findByText("Saved")).toBeInTheDocument();
        expect(screen.getByRole("radio", { name: /^Isoform/ })).toBeChecked();
        expect(screen.getByRole("combobox", { name: "Isoform model" })).toHaveTextContent("Automatic");
    });

    it("sets the picked agent's model from magpie's list, and back to Automatic", async () => {
        const withOpus = choice();
        withOpus.agents[0] = { ...withOpus.agents[0], model: "claude/claude-opus-5-5" };
        mocks.setModel.mockResolvedValueOnce(withOpus).mockResolvedValue(choice());
        render(<AssistantChoiceCard />);
        const model = await screen.findByRole("combobox", { name: "Claude Code model" });
        expect(model).toHaveTextContent("Automatic");
        pick(model, "claude/claude-opus-5-5");
        await waitFor(() => expect(mocks.setModel).toHaveBeenCalledWith("claude-code", "claude/claude-opus-5-5"));
        await waitFor(() => expect(screen.getByRole("combobox", { name: "Claude Code model" })).toHaveTextContent("claude/claude-opus-5-5"));
        pick(screen.getByRole("combobox", { name: "Claude Code model" }), "Automatic");
        await waitFor(() => expect(mocks.setModel).toHaveBeenCalledWith("claude-code", ""));
    });

    it("offers only the effort steps magpie lists for the chosen model, High by default", async () => {
        const base = choice();
        base.agents[0] = { ...base.agents[0], model: "claude/claude-sonnet-5-5", efforts: ["low", "medium", "high"] };
        mocks.getChoice.mockResolvedValue(base);
        mocks.setEffort.mockResolvedValue(base);
        render(<AssistantChoiceCard />);
        const effort = await screen.findByRole("radiogroup", { name: "Claude Code effort" });
        expect(within(effort).getAllByRole("radio").map((step) => step.textContent)).toEqual(["Low", "Medium", "High"]);
        expect(within(effort).getByRole("radio", { name: "High" })).toHaveAttribute("aria-checked", "true");
        expect(screen.getByText("Higher effort thinks longer and uses more of your plan.")).toBeInTheDocument();
        fireEvent.click(within(effort).getByRole("radio", { name: "Low" }));
        await waitFor(() => expect(mocks.setEffort).toHaveBeenCalledWith("claude-code", "low"));
    });

    it("shows all five steps, Extra included, for a model with them", async () => {
        render(<AssistantChoiceCard />);
        const effort = await screen.findByRole("radiogroup", { name: "Claude Code effort" });
        expect(within(effort).getAllByRole("radio").map((step) => step.textContent)).toEqual(["Low", "Medium", "High", "Extra", "Max"]);
    });

    it("hides effort for a model magpie lists without steps", async () => {
        const base = choice();
        base.agents[0] = { ...base.agents[0], model: "claude/claude-haiku-4-5", effort: "", efforts: [] };
        mocks.getChoice.mockResolvedValue(base);
        render(<AssistantChoiceCard />);
        await screen.findByRole("combobox", { name: "Claude Code model" });
        expect(screen.queryByRole("radiogroup", { name: "Claude Code effort" })).not.toBeInTheDocument();
    });

    // ST19: Who answers lists only the assistants that can answer, plus Isoform.
    it("lists only the assistants that can answer, plus Isoform", async () => {
        render(<AssistantChoiceCard />);
        await screen.findByRole("radio", { name: /^Claude Code/ });
        const radios = within(screen.getByRole("radiogroup", { name: "Who answers in Isoform" })).getAllByRole("radio");
        expect(radios.map((radio) => (radio as HTMLInputElement).value)).toEqual(["claude-code", "isoform"]);
        expect(screen.queryByText("DeepSeek Harness")).not.toBeInTheDocument();
        expect(screen.queryByText("Can't use Isoform's tools yet")).not.toBeInTheDocument();
        expect(screen.queryByText("Not installed on this computer")).not.toBeInTheDocument();
    });

    it("keeps a saved pick in the list while it can't answer, and never re-picks it", async () => {
        mocks.getChoice.mockResolvedValue(choice({ answering: "codex" }));
        render(<AssistantChoiceCard />);
        const codex = await screen.findByRole("radio", { name: /^Codex/ });
        expect(codex).toBeChecked();
        expect(within(rowOf("Codex")).getByText("Can't use Isoform's tools yet")).toBeInTheDocument();
        expect(screen.queryByRole("radio", { name: /^DeepSeek Harness/ })).not.toBeInTheDocument();
    });

    it("tells Models Isoform uses which assistant was picked", async () => {
        mocks.setAnswering.mockResolvedValue(choice({ answering: "isoform" }));
        const picked: string[] = [];
        const onPicked = (event: Event) => picked.push((event as CustomEvent<string>).detail);
        window.addEventListener("assistant-choice:answering", onPicked);
        try {
            render(<AssistantChoiceCard profileModels />);
            fireEvent.click(await screen.findByRole("radio", { name: /^Isoform/ }));
            await waitFor(() => expect(picked).toEqual(["isoform"]));
        } finally {
            window.removeEventListener("assistant-choice:answering", onPicked);
        }
        expect(screen.queryByText(/model profile/)).not.toBeInTheDocument();
    });

    it("allows choosing Isoform while its models start", async () => {
        mocks.getChoice.mockResolvedValue(choice({ isoformReady: false }));
        mocks.setAnswering.mockResolvedValue(choice({ isoformReady: false, answering: "isoform" }));
        render(<AssistantChoiceCard />);
        const radio = await screen.findByRole("radio", { name: /^Isoform/ });
        expect(radio).toBeEnabled();
        expect(within(rowOf("Isoform")).getByText("Not available until its models start")).toBeInTheDocument();
        fireEvent.click(radio);
        await waitFor(() => expect(mocks.setAnswering).toHaveBeenCalledWith("isoform"));
    });

    it("keeps background work on the lighter default unless the switch is on", async () => {
        mocks.setBackground.mockResolvedValue(choice({ backgroundUsesChoice: true }));
        render(<AssistantChoiceCard />);
        const toggle = await screen.findByRole("switch", { name: "Use my choice for background work too" });
        expect(toggle).not.toBeChecked();
        expect(screen.getByText(/background work \(the brief, learning, summaries\) uses Sonnet 5.5 at Medium/)).toBeInTheDocument();
        fireEvent.click(toggle);
        await waitFor(() => expect(mocks.setBackground).toHaveBeenCalledWith(true));
        await waitFor(() => expect(screen.getByRole("switch", { name: "Use my choice for background work too" })).toBeChecked());
    });

    it("names the lighter default of the picked agent", async () => {
        mocks.getChoice.mockResolvedValue(choice({ answering: "isoform" }));
        render(<AssistantChoiceCard />);
        expect(await screen.findByText(/uses lighter models at a steady pace/)).toBeInTheDocument();
        expect(screen.queryByText(/Sonnet 5.5/)).not.toBeInTheDocument();
    });

    it("offers Sign in on a signed-out row and Try again on a paused one", async () => {
        mocks.getHealth.mockResolvedValue([
            { assistant: "claude-code", code: "signed_out", checking: false },
            { assistant: "codex", code: "tools_unverified", checking: false },
        ]);
        mocks.signIn.mockResolvedValue(undefined);
        const { unmount } = render(<AssistantChoiceCard />);
        fireEvent.click(await within(await waitFor(() => rowOf("Claude Code"))).findByRole("button", { name: "Sign in" }));
        expect(mocks.signIn).toHaveBeenCalledWith("claude-code");
        expect(within(rowOf("Claude Code")).getByText("Signed out")).toBeInTheDocument();
        unmount();

        mocks.getHealth.mockResolvedValue([{ assistant: "claude-code", code: "check_failed", checking: false }]);
        mocks.retryHealth.mockResolvedValue([{ assistant: "claude-code", code: "ready", checking: false }]);
        render(<AssistantChoiceCard />);
        expect(await screen.findByText("Paused after a failed check. Isoform answers until it passes.")).toBeInTheDocument();
        fireEvent.click(within(rowOf("Claude Code")).getByRole("button", { name: "Try again" }));
        await waitFor(() => expect(mocks.retryHealth).toHaveBeenCalledWith("claude-code"));
    });

    it.each(["own_magpie", "own_key", "gateway_unavailable", ""])("keeps agent choices editable with %s", async (reason) => {
        mocks.getChoice.mockResolvedValue(choice({ editable: false, reason }));
        mocks.setModel.mockResolvedValue(choice({ reason }));
        mocks.setEffort.mockResolvedValue(choice({ reason }));
        mocks.setBackground.mockResolvedValue(choice({ reason, backgroundUsesChoice: true }));
        render(<AssistantChoiceCard />);
        const model = await screen.findByRole("combobox", { name: "Claude Code model" });
        expect(model).toBeEnabled();
        pick(model, "claude/claude-opus-5-5");
        await waitFor(() => expect(mocks.setModel).toHaveBeenCalledWith("claude-code", "claude/claude-opus-5-5"));
        await waitFor(() => expect(screen.getByRole("combobox", { name: "Codex model" })).toBeEnabled());
        pick(screen.getByRole("combobox", { name: "Codex model" }), "codex/gpt-6-astra");
        await waitFor(() => expect(mocks.setModel).toHaveBeenCalledWith("codex", "codex/gpt-6-astra"));
        await waitFor(() => expect(within(screen.getByRole("radiogroup", { name: "Codex effort" })).getByRole("radio", { name: "Low" })).toBeEnabled());
        fireEvent.click(within(screen.getByRole("radiogroup", { name: "Codex effort" })).getByRole("radio", { name: "Low" }));
        await waitFor(() => expect(mocks.setEffort).toHaveBeenCalledWith("codex", "low"));
        await waitFor(() => expect(screen.getByRole("switch", { name: "Use my choice for background work too" })).toBeEnabled());
        fireEvent.click(screen.getByRole("switch", { name: "Use my choice for background work too" }));
        await waitFor(() => expect(mocks.setBackground).toHaveBeenCalledWith(true));
    });

    it.each([["own_magpie", false], ["own_key", true], ["", false]])("shows Isoform model source for %s", async (reason, disabled) => {
        mocks.getChoice.mockResolvedValue(choice({ answering: "isoform", reason }));
        render(<AssistantChoiceCard />);
        const model = await screen.findByRole("combobox", { name: "Isoform model" });
        expect(model).toHaveProperty("disabled", disabled);
        if (reason === "own_magpie") expect(screen.queryByText("Your magpie controls the order of fallback models.")).not.toBeInTheDocument();
        if (reason === "own_key") expect(screen.getByText("Your API key model is set under Advanced.")).toBeInTheDocument();
    });

    it("says so calmly when a choice cannot be saved, and reads the saved state again", async () => {
        mocks.setEffort.mockRejectedValue(new Error("magpie claude: exit status 1"));
        render(<AssistantChoiceCard />);
        const effort = await screen.findByRole("radiogroup", { name: "Claude Code effort" });
        fireEvent.click(within(effort).getByRole("radio", { name: "Max" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("Isoform could not save that choice. Try again.");
        expect(mocks.getChoice).toHaveBeenCalledTimes(2);
        expect(screen.queryByText(/exit status/)).not.toBeInTheDocument();
        expect(screen.queryByText("Saved")).not.toBeInTheDocument();
    });

    it("follows changes made elsewhere", async () => {
        render(<AssistantChoiceCard />);
        await screen.findByRole("radio", { name: /^Claude Code/ });
        const listeners = mocks.eventsOn.mock.calls.filter(([name]) => name === "ai:config-changed").map(([, fn]) => fn as () => void);
        expect(listeners.length).toBeGreaterThan(0);
        mocks.getChoice.mockResolvedValue(choice({ answering: "isoform" }));
        await act(async () => listeners.forEach((fn) => fn()));
        await waitFor(() => expect(screen.getByRole("radio", { name: /^Isoform/ })).toBeChecked());
    });

    it("refreshes the catalog when the gateway updates", async () => {
        render(<AssistantChoiceCard />);
        await screen.findByRole("combobox", { name: "Claude Code model" });
        const listener = mocks.eventsOn.mock.calls.find(([name]) => name === "model-accounts:updated")?.[1] as () => void;
        expect(listener).toBeDefined();
        mocks.getChoice.mockResolvedValue(choice({ answering: "isoform" }));
        await act(async () => listener());
        expect(await screen.findByRole("combobox", { name: "Isoform model" })).toBeEnabled();
    });

    it("speaks Chinese", async () => {
        await i18n.changeLanguage("zh-CN");
        render(<AssistantChoiceCard />);
        expect(await screen.findByRole("heading", { name: "在 Isoform 中由谁回答" })).toBeInTheDocument();
        expect(screen.getByText("只作用于 Isoform。你自己的 Claude Code 和 Codex 设置保持不变。")).toBeInTheDocument();
        const effort = screen.getByRole("radiogroup", { name: "Claude Code 的思考强度" });
        expect(within(effort).getByRole("radio", { name: "高" })).toHaveAttribute("aria-checked", "true");
        expect(within(effort).getByRole("radio", { name: "超高" })).toBeInTheDocument();
    });
});
