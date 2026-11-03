import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@/i18n";
import { OnboardingScreen, resumeScreen } from "./onboarding-screen";
import type { OnboardingState } from "@/types";

const mocks = vi.hoisted(() => ({
    load: vi.fn(),
    save: vi.fn(),
    close: vi.fn(),
    windowMode: vi.fn(),
    reveal: vi.fn(),
    sees: vi.fn(),
}));
vi.mock("@/lib/wails-shim-app", () => ({
    LoadUIState: mocks.load,
    SaveUIState: mocks.save,
    CloseMainChatSetupSteps: mocks.close,
    GetWhatIsoformSees: mocks.sees,
}));
vi.mock("./onboarding/v2/native", () => ({
    setSetupWindowMode: mocks.windowMode,
    revealMainWindow: mocks.reveal,
}));

function scene(name: string) {
    return ({ onNext }: { onNext: () => void }) => (
        <button type="button" onClick={onNext}>
            {`${name} next`}
        </button>
    );
}
vi.mock("./onboarding/v2/scenes/NoticesScene", () => ({ NoticesScene: scene("notices") }));
vi.mock("./onboarding/v2/scenes/YourAIScene", () => ({ YourAIScene: scene("ai") }));
vi.mock("./onboarding/v2/scenes/SeesScene", () => ({
    SeesScene: scene("sees"),
    SEES_ROWS: [
        { id: "screen", keys: ["screen_recording"] },
        { id: "day", keys: ["calendars", "reminders"] },
    ],
}));
vi.mock("./onboarding/v2/scenes/RunsScene", () => ({ RunsScene: scene("runs") }));
vi.mock("./onboarding/v2/scenes/SendScene", () => ({ SendScene: scene("send") }));
vi.mock("./onboarding/v2/scenes/ReachesScene", () => ({ ReachesScene: scene("reaches") }));
vi.mock("./onboarding/v2/scenes/FinishScene", () => ({
    FinishScene: ({ onDone, onClosing }: { onDone: () => void; onClosing: () => void }) => (
        <button
            type="button"
            onClick={() => {
                onClosing();
                onDone();
            }}
        >
            finish done
        </button>
    ),
}));

const state = {
    version: 2,
    completed: false,
    neverShowAgain: false,
    permissionsComplete: false,
} as OnboardingState;

function renderScreen(props: Partial<Parameters<typeof OnboardingScreen>[0]> = {}) {
    const onFinish = vi.fn();
    const onSkip = vi.fn();
    const onStateChange = vi.fn();
    render(
        <OnboardingScreen
            state={state}
            onFinish={onFinish}
            onSkip={onSkip}
            onStateChange={onStateChange}
            {...props}
        />,
    );
    return { onFinish, onSkip, onStateChange };
}

const next = (name: string) => fireEvent.click(screen.getByRole("button", { name: `${name} next` }));

beforeEach(() => {
    vi.clearAllMocks();
    mocks.load.mockResolvedValue("");
    mocks.save.mockResolvedValue(undefined);
    mocks.close.mockResolvedValue(undefined);
    mocks.windowMode.mockResolvedValue(null);
    mocks.reveal.mockResolvedValue(null);
    mocks.sees.mockResolvedValue({ sources: [], assistant: "" });
});

describe("resumeScreen", () => {
    it("keeps v2 screens and maps the steps saved before v2", () => {
        expect(resumeScreen("send")).toBe("send");
        expect(resumeScreen("welcome")).toBe("notices");
        expect(resumeScreen("agent")).toBe("notices");
        expect(resumeScreen("permissions")).toBe("sees");
        expect(resumeScreen("models")).toBe("sees");
        expect(resumeScreen("first-task")).toBe("reaches");
        expect(resumeScreen("nonsense")).toBeNull();
    });
});

describe("OnboardingScreen v2", () => {
    it("walks the six screens in order with one progress dash each", async () => {
        renderScreen();
        expect(await screen.findByRole("button", { name: "notices next" })).toBeInTheDocument();
        expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "1");
        expect(screen.queryByTestId("setup-v2-back")).toBeNull();
        next("notices");
        next("ai");
        next("sees");
        expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "4");
        expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuemax", "6");
        next("runs");
        next("send");
        expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "6");
        next("reaches");
        expect(screen.getByRole("button", { name: "finish done" })).toBeInTheDocument();
        expect(screen.queryByRole("progressbar")).toBeNull();
    });

    it("goes back one screen", async () => {
        renderScreen();
        next("notices");
        next("ai");
        fireEvent.click(screen.getByTestId("setup-v2-back"));
        expect(screen.getByRole("button", { name: "ai next" })).toBeInTheDocument();
    });

    it("resumes on the saved screen after the Screen Recording reopen", async () => {
        mocks.load.mockResolvedValue("permissions");
        renderScreen();
        expect(await screen.findByRole("button", { name: "sees next" })).toBeInTheDocument();
        await waitFor(() => expect(mocks.save).toHaveBeenCalledWith("yansu.onboarding.step", "sees"));
    });

    it("never saves a screen before the saved one is read", async () => {
        let resolve: (value: string) => void = () => {};
        mocks.load.mockReturnValue(new Promise<string>((r) => (resolve = r)));
        renderScreen();
        expect(mocks.save).not.toHaveBeenCalled();
        await act(async () => resolve(""));
        await waitFor(() => expect(mocks.save).toHaveBeenCalledWith("yansu.onboarding.step", "notices"));
    });

    it("closes the chat's asks for what setup asked, then opens Chat", async () => {
        const { onFinish, onStateChange } = renderScreen();
        await screen.findByRole("button", { name: "notices next" });
        next("notices");
        next("ai");
        next("sees");
        await waitFor(() =>
            expect(mocks.close).toHaveBeenCalledWith(["screen_recording", "calendars", "reminders"]),
        );
        expect(onStateChange).toHaveBeenCalledWith(expect.objectContaining({ permissionsComplete: true }));
        next("runs");
        next("send");
        next("reaches");
        await waitFor(() => expect(mocks.close).toHaveBeenCalledWith(["notifications", "first_task"]));
        expect(mocks.save).toHaveBeenCalledWith("yansu.onboarding.step", "");
        fireEvent.click(screen.getByRole("button", { name: "finish done" }));
        await waitFor(() => expect(onFinish).toHaveBeenCalledTimes(1));
        expect(mocks.windowMode).toHaveBeenLastCalledWith(false);
        await waitFor(() => expect(mocks.reveal).toHaveBeenCalled());
    });

    it("asks for the fixed setup window", () => {
        renderScreen();
        expect(mocks.windowMode).toHaveBeenCalledWith(true);
    });

    it("leaves setup on Escape, but not when an input method closes", () => {
        const { onSkip } = renderScreen();
        fireEvent.keyDown(window, { key: "Escape", keyCode: 229 });
        expect(onSkip).not.toHaveBeenCalled();
        fireEvent.keyDown(window, { key: "Escape" });
        expect(onSkip).toHaveBeenCalledTimes(1);
    });

    it("closes the sees sheet on Escape and outside click without leaving setup", async () => {
        const { onSkip } = renderScreen();
        const pill = screen.getByTestId("setup-v2-local");
        expect(pill).toHaveTextContent("On this computer");
        fireEvent.click(pill);
        await screen.findByRole("dialog", { name: "What Isoform sees" });
        fireEvent.keyDown(window, { key: "Escape" });
        expect(screen.queryByRole("dialog")).toBeNull();
        expect(onSkip).not.toHaveBeenCalled();
        fireEvent.click(pill);
        fireEvent.pointerDown(document.body);
        await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
        expect(onSkip).not.toHaveBeenCalled();
        next("notices");
        expect(screen.getByTestId("setup-v2-local")).toBeInTheDocument();
    });

    it("renders a gallery still with no reads or writes", () => {
        renderScreen({ still: "send" });
        expect(screen.getByRole("button", { name: "send next" })).toBeInTheDocument();
        expect(mocks.load).not.toHaveBeenCalled();
        expect(mocks.windowMode).not.toHaveBeenCalled();
        next("send");
        expect(screen.getByRole("button", { name: "send next" })).toBeInTheDocument();
    });
});
