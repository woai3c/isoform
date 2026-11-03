import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import type {
    ModelChoices,
    ModelDownload,
    ModelDownloadPlan,
    ModelJob,
    SetupPermissions,
} from "@/lib/setup-backend";
import { RunsScene } from "./RunsScene";
import { formatModelBytes } from "../models";

const mocks = vi.hoisted(() => ({
    choices: vi.fn(),
    perms: vi.fn(),
    jobs: vi.fn(),
    network: vi.fn(),
    plan: vi.fn(),
    start: vi.fn(),
    defer: vi.fn(),
    ask: vi.fn(),
    events: new Map<string, (payload: unknown) => void>(),
}));
vi.mock("@/lib/wails-shim-app", () => ({
    GetModelChoices: mocks.choices,
    GetSetupPermissions: mocks.perms,
    GetModelDownloads: mocks.jobs,
    GetModelNetworkState: mocks.network,
    PlanSetupModelDownload: mocks.plan,
    StartModelDownloads: mocks.start,
    DeferModelDownloads: mocks.defer,
    RequestPermission: mocks.ask,
}));
vi.mock("../../../../../wailsjs/runtime/runtime", () => ({
    EventsOn: (key: string, fn: (payload: unknown) => void) => {
        mocks.events.set(key, fn);
        return () => mocks.events.delete(key);
    },
}));

// A catalog with the shipped recommended set's sizes; the planner below
// totals what the request asks for, as the desktop's does.
const SIZES: Record<string, number> = {
    sensevoice: 239_549_735,
    "speaker-pack": 30_465_498,
    "qwen3-asr-0.6b": 878_702_423,
    none: 0,
    "kokoro-en": 319_625_534,
    "melotts-zh-en": 167_006_755,
    "gliner-pii": 673_414_035,
    minilm: 90_636_722,
};
const JOBS: Array<[ModelJob, string[]]> = [
    ["live", ["sensevoice"]],
    ["speakerPack", ["speaker-pack"]],
    ["afterCall", ["qwen3-asr-0.6b", "none"]],
    ["voice", ["kokoro-en", "melotts-zh-en"]],
    ["privacy", ["gliner-pii"]],
    ["memory", ["minilm"]],
];
const NAMES: Record<string, string> = {
    sensevoice: "SenseVoice",
    "qwen3-asr-0.6b": "Qwen3-ASR 0.6B",
    "kokoro-en": "Kokoro",
    "melotts-zh-en": "MeloTTS",
};
function catalog(nonCommercial: string[] = []): ModelChoices {
    return {
        jobs: JOBS.map(([job, ids]) => ({
            job,
            chosenId: ids[0],
            locked: false,
            alternatives: ids.map((id) => ({
                id,
                job,
                displayName: NAMES[id] ?? id,
                line: "",
                evidence: "",
                sizeBytes: SIZES[id],
                files: [],
                sources: [],
                licence: { name: "CC BY-NC 4.0", url: "", nonCommercial: nonCommercial.includes(id) },
                recommended: id === ids[0],
                languages: id === "melotts-zh-en" ? ["zh", "en"] : ["en"],
                downloaded: false,
            })),
        })),
        totalBytesToDownload: 0,
        freeBytes: 412e9,
        modelsFolder: "/models",
    };
}
let free = 412e9;
let licences: string[] = [];
function planner(choices: Partial<Record<ModelJob, string>>, skip: ModelJob[]): ModelDownloadPlan {
    const items = Object.entries(choices)
        .filter(([job, id]) => id && id !== "none" && !skip.includes(job as ModelJob))
        .map(([, id]) => ({
            id: id!,
            bytes: SIZES[id!],
            fileCount: 1,
            source: { kind: "mirror" as const, url: "https://m.example/" },
            host: "m.example",
            licence: { name: "", url: "", nonCommercial: licences.includes(id!) },
            nonCommercial: licences.includes(id!),
        }));
    const totalBytes = items.reduce((sum, item) => sum + item.bytes, 0);
    return {
        planId: `plan-${mocks.plan.mock.calls.length}`,
        choices,
        requiredBytes: totalBytes,
        items,
        totalBytes,
        freeBytes: free,
        enoughSpace: free >= totalBytes,
        modelsFolder: "/models",
    };
}
const callsOn: SetupPermissions = {
    answers: [
        { key: "microphone", state: "on" },
        { key: "system_audio", state: "on" },
    ],
    running: false,
    resumeStep: "sees",
    next: 8,
};
const callsOff: SetupPermissions = { ...callsOn, answers: [{ key: "microphone", state: "denied" }] };

beforeEach(async () => {
    await i18n.changeLanguage("en");
    vi.resetAllMocks();
    mocks.events.clear();
    free = 412e9;
    licences = [];
    mocks.choices.mockImplementation(async () => catalog(licences));
    mocks.perms.mockResolvedValue(callsOn);
    mocks.jobs.mockResolvedValue([]);
    mocks.network.mockResolvedValue({ known: true, offline: false, expensive: false, constrained: false });
    mocks.plan.mockImplementation(async (choices, skip) => planner(choices, skip));
    mocks.start.mockResolvedValue(undefined);
    mocks.defer.mockResolvedValue(undefined);
});

const TOTAL_ALL = 2_232_393_947;
const lastPlan = () => mocks.plan.mock.calls.at(-1) as [Partial<Record<ModelJob, string>>, ModelJob[]];
const row = (id: string) => screen.getByTestId(`setup-v2-model-${id}`);
const button = () => screen.getByTestId("setup-v2-continue");

function renderScene(props: Partial<Parameters<typeof RunsScene>[0]> = {}) {
    const onNext = vi.fn();
    render(<RunsScene onNext={onNext} reduced={false} enter="" {...props} />);
    return { onNext };
}

describe("Runs on this computer", () => {
    it("shows each job with its model and size from the plan, and the live total", async () => {
        renderScene();
        await waitFor(() => expect(button()).toHaveTextContent("Download 2.2 GB"));
        expect(screen.getByRole("heading")).toHaveTextContent("Runs on this computer");
        expect(row("calls")).toHaveTextContent("Hear your calls");
        expect(row("calls")).toHaveTextContent("SenseVoice and speaker models · 270 MB");
        expect(row("afterCall")).toHaveTextContent("Qwen3-ASR · 879 MB");
        expect(row("voice")).toHaveTextContent("Kokoro · 320 MB");
        expect(row("always")).toHaveTextContent("764 MB");
        expect(row("always")).toHaveTextContent("Always on");
        expect(within(row("always")).queryByRole("switch")).toBeNull();
        expect(screen.getByTestId("setup-v2-models-total")).toHaveTextContent("2.2 GB · 412 GB free on this disk");
        expect(formatModelBytes(TOTAL_ALL)).toBe("2.2 GB");
    });

    it("downloads nothing before Download or Later", async () => {
        renderScene();
        await waitFor(() => expect(button()).toBeEnabled());
        fireEvent.click(within(row("voice")).getByRole("switch"));
        await waitFor(() => expect(button()).toHaveTextContent("Download 1.9 GB"));
        expect(mocks.start).not.toHaveBeenCalled();
        expect(mocks.defer).not.toHaveBeenCalled();
    });

    it("follows a switch: the total and the button change, and the job is left out", async () => {
        renderScene();
        await waitFor(() => expect(button()).toHaveTextContent("Download 2.2 GB"));
        const toggle = within(row("afterCall")).getByRole("switch");
        fireEvent.click(toggle);
        expect(toggle).toHaveAttribute("aria-checked", "false");
        await waitFor(() => expect(button()).toHaveTextContent("Download 1.4 GB"));
        expect(screen.getByTestId("setup-v2-models-total")).toHaveTextContent("1.4 GB · 412 GB free on this disk");
        expect(lastPlan()[0].afterCall).toBe("none");
        fireEvent.click(within(row("calls")).getByRole("switch"));
        await waitFor(() => expect(lastPlan()[1]).toEqual(["live", "speakerPack"]));
        fireEvent.click(within(row("voice")).getByRole("switch"));
        await waitFor(() => expect(lastPlan()[1]).toEqual(["live", "speakerPack", "voice"]));
    });

    it("starts the reviewed plan on Download, shows each row's real progress and continues at once", async () => {
        const { onNext } = renderScene();
        await waitFor(() => expect(button()).toBeEnabled());
        fireEvent.click(button());
        await waitFor(() => expect(mocks.start).toHaveBeenCalledTimes(1));
        expect(mocks.start.mock.calls[0][0].totalBytes).toBe(TOTAL_ALL);
        await waitFor(() => expect(button()).toHaveTextContent("Continue"));
        expect(screen.getByTestId("setup-v2-models-total")).toHaveTextContent(
            "Downloading in the background. You can keep going.",
        );
        expect(screen.queryByTestId("setup-v2-models-later")).toBeNull();
        expect(within(row("voice")).queryByRole("switch")).toBeNull();
        const jobs: ModelDownload[] = [
            { id: "sensevoice", state: "downloading", pauseReason: "", bytesDone: 239_549_735, bytesTotal: 239_549_735, error: "" },
            { id: "speaker-pack", state: "queued", pauseReason: "", bytesDone: 0, bytesTotal: 30_465_498, error: "" },
            { id: "kokoro-en", state: "downloading", pauseReason: "", bytesDone: 159_812_767, bytesTotal: 319_625_534, error: "" },
        ];
        act(() => mocks.events.get("models:downloads")?.(jobs));
        expect(row("calls")).toHaveTextContent("89%");
        expect(row("voice")).toHaveTextContent("50%");
        expect(row("afterCall")).toHaveTextContent("0%");
        expect(row("voice").querySelector(".bar")).toHaveStyle({ transform: "scaleX(0.5)" });
        fireEvent.click(button());
        expect(onNext).toHaveBeenCalledTimes(1);
    });

    it("confirms the same plan for later on Later and moves on", async () => {
        const { onNext } = renderScene();
        await waitFor(() => expect(button()).toBeEnabled());
        fireEvent.click(screen.getByTestId("setup-v2-models-later"));
        await waitFor(() => expect(onNext).toHaveBeenCalledTimes(1));
        expect(mocks.defer).toHaveBeenCalledTimes(1);
        expect(mocks.defer.mock.calls[0][0].totalBytes).toBe(TOTAL_ALL);
        expect(mocks.start).not.toHaveBeenCalled();
        expect(screen.getByText(/waits until your computer is plugged in and idle/)).toBeInTheDocument();
    });

    it("starts the call rows off when Your calls is off, and Turn on asks then switches the row on", async () => {
        mocks.perms.mockResolvedValue(callsOff);
        mocks.ask.mockResolvedValue(true);
        renderScene();
        await waitFor(() => expect(button()).toHaveTextContent("Download 1.1 GB"));
        expect(row("calls")).toHaveAttribute("data-on", "false");
        expect(row("afterCall")).toHaveAttribute("data-on", "false");
        expect(row("calls")).toHaveTextContent("Needs Your calls · Turn on");
        expect(within(row("calls")).getByRole("switch")).toBeDisabled();
        expect(lastPlan()[1]).toEqual(["live", "speakerPack"]);
        fireEvent.click(screen.getByTestId("setup-v2-model-calls-turn-on"));
        await waitFor(() => expect(row("calls")).toHaveAttribute("data-on", "true"));
        expect(mocks.ask.mock.calls.map((c) => c[0])).toEqual(["microphone", "system_audio"]);
        expect(row("afterCall")).toHaveAttribute("data-on", "false");
        expect(row("afterCall")).not.toHaveTextContent("Needs Your calls");
        await waitFor(() => expect(button()).toHaveTextContent("Download 1.4 GB"));
    });

    it("keeps the row off when a calls prompt is refused", async () => {
        mocks.perms.mockResolvedValue(callsOff);
        mocks.ask.mockResolvedValueOnce(true).mockResolvedValueOnce(false);
        renderScene();
        await waitFor(() => expect(button()).toBeEnabled());
        fireEvent.click(screen.getByTestId("setup-v2-model-afterCall-turn-on"));
        await waitFor(() => expect(mocks.ask).toHaveBeenCalledTimes(2));
        expect(row("afterCall")).toHaveAttribute("data-on", "false");
    });

    it("shows a limiting licence in copper on its row before Download", async () => {
        licences = ["qwen3-asr-0.6b"];
        renderScene();
        await waitFor(() => expect(button()).toBeEnabled());
        const note = within(row("afterCall")).getByText(/Non-commercial use only/);
        expect(note).toHaveClass("lic");
        expect(row("calls")).not.toHaveTextContent("Non-commercial");
    });

    it("replaces the total with the shortfall and disables Download when it does not fit; Later still works", async () => {
        free = 1.5e9;
        const { onNext } = renderScene();
        await waitFor(() =>
            expect(screen.getByTestId("setup-v2-models-total")).toHaveTextContent(
                "Not enough space. Free 732 MB more before downloading.",
            ),
        );
        expect(button()).toBeDisabled();
        fireEvent.click(within(row("afterCall")).getByRole("switch"));
        await waitFor(() => expect(button()).toBeEnabled());
        expect(button()).toHaveTextContent("Download 1.4 GB");
        fireEvent.click(within(row("afterCall")).getByRole("switch"));
        await waitFor(() => expect(button()).toBeDisabled());
        fireEvent.click(screen.getByTestId("setup-v2-models-later"));
        await waitFor(() => expect(onNext).toHaveBeenCalled());
        expect(mocks.defer).toHaveBeenCalledTimes(1);
    });

    it.each([
        [{ known: true, offline: true, expensive: false, constrained: false }, "You are offline."],
        [{ known: true, offline: false, expensive: true, constrained: false }, "This connection is metered."],
        [{ known: true, offline: false, expensive: true, constrained: true }, "Low Data Mode is on."],
    ])("after Download on %o, shows the existing reason and Continue still works", async (network, reason) => {
        mocks.network.mockResolvedValue(network);
        const { onNext } = renderScene();
        await waitFor(() => expect(button()).toBeEnabled());
        fireEvent.click(button());
        await waitFor(() => expect(screen.getByTestId("setup-v2-models-total")).toHaveTextContent(reason));
        expect(screen.getByTestId("setup-v2-models-total")).not.toHaveClass("ok");
        fireEvent.click(button());
        expect(onNext).toHaveBeenCalledTimes(1);
    });

    it("draws the bars without moving under Reduce Motion", async () => {
        const { container } = render(<RunsScene onNext={vi.fn()} reduced enter="" />);
        await waitFor(() => expect(button()).toBeEnabled());
        expect(container.querySelector(".mods")).toHaveClass("still");
    });

    it("reads its words in Chinese, with MeloTTS for the voice", async () => {
        await i18n.changeLanguage("zh-CN");
        renderScene();
        await waitFor(() => expect(button()).toHaveTextContent("下载 2.1 GB"));
        expect(screen.getByRole("heading")).toHaveTextContent("在这台电脑上运行");
        expect(row("voice")).toHaveTextContent("MeloTTS · 167 MB（中文和英文）");
        expect(row("calls")).toHaveTextContent("SenseVoice 和说话人模型 · 270 MB");
        expect(screen.getByTestId("setup-v2-models-total")).toHaveTextContent("共 2.1 GB · 磁盘剩余 412 GB");
        expect(lastPlan()[0].voice).toBe("melotts-zh-en");
    });

    it("comes back to the progress when the downloads it started are still running", async () => {
        mocks.jobs.mockResolvedValue([
            { id: "kokoro-en", state: "downloading", pauseReason: "", bytesDone: 0, bytesTotal: 319_625_534, error: "" },
        ]);
        renderScene();
        await waitFor(() => expect(button()).toHaveTextContent("Continue"));
        expect(row("voice")).toHaveTextContent("0%");
        expect(row("calls")).toHaveAttribute("data-on", "false");
        expect(mocks.plan).not.toHaveBeenCalled();
    });
});
