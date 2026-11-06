import {
    act,
    fireEvent,
    render,
    screen,
    waitFor,
    within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AboutInfo } from "@/lib/wails-shim-app";

const mocks = vi.hoisted(() => ({
    getAbout: vi.fn(),
    getVersion: vi.fn(),
    check: vi.fn(),
    download: vi.fn(),
    restart: vi.fn(),
    background: vi.fn(),
    saveLogs: vi.fn(),
    openFolder: vi.fn(),
    openURL: vi.fn(),
    handlers: {} as Record<string, (payload: unknown) => void>,
}));
vi.mock("@/lib/wails-shim-app", () => ({
    GetAboutInfo: mocks.getAbout,
    GetVersion: mocks.getVersion,
    CheckForUpdates: mocks.check,
    DownloadUpdate: mocks.download,
    RestartToUpdate: mocks.restart,
    SetBackgroundUpdates: mocks.background,
    SaveSupportLogs: mocks.saveLogs,
    OpenDataFolder: mocks.openFolder,
    OpenExternalURL: mocks.openURL,
    GetPerfSnapshot: vi.fn(),
}));
vi.mock("../../../../wailsjs/runtime/runtime", () => ({
    EventsOn: vi.fn((name: string, handler: (payload: unknown) => void) => {
        mocks.handlers[name] = handler;
        return () => {
            delete mocks.handlers[name];
        };
    }),
}));
import i18n from "@/i18n";
import { HelpAboutPage } from "./help-about-page";

let info: AboutInfo;
async function status(update: AboutInfo["update"]) {
    info = { ...info, update };
    await act(async () => {
        mocks.handlers["update:status"]?.(update);
    });
}

describe("Settings About", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        localStorage.clear();
        await i18n.changeLanguage("en");
        info = {
            version: "0.1.301",
            build: "staging",
            buildTime: "2026-10-22T17:42:00Z",
            computer: "Mac mini",
            os: "macOS 26.1",
            phones: ["My phone"],
            dataBytes: 1.4e9,
            backgroundUpdates: true,
            lastChecked: Date.now(),
            later: false,
            releaseNotesVersion: "0.1.301",
            releaseNotes: "",
            releaseNotesUrl: "https://example.com/releases",
            update: { status: "up-to-date" },
        };
        mocks.getAbout.mockImplementation(async () => info);
        mocks.getVersion.mockResolvedValue("0.1.301");
        mocks.check.mockResolvedValue({ available: false });
        mocks.restart.mockResolvedValue(undefined);
        mocks.download.mockResolvedValue(undefined);
        mocks.background.mockImplementation(async (enabled) => {
            info = { ...info, backgroundUpdates: enabled };
        });
    });
    it.each(["en", "zh-CN"])("shows a readable build date and an ink Discord action without badge helpers in %s", async (language) => {
        await i18n.changeLanguage(language);
        info.buildTime = new Date(2026, 9, 22, 22, 22).toISOString();
        render(<HelpAboutPage onReplayOnboarding={vi.fn()} />);
        const build = await screen.findByText(i18n.t("settings:aboutPolish.build"));
        const row = build.closest("[data-about-row]") ?? build.parentElement!;
        expect(row.textContent).toBe(language === "en" ? "BuildStaging · Oct 22 at 10:22 PM" : "构建预发布版 · 10月22日 22:22");
        fireEvent.click(screen.getByRole("button", { name: /Advanced|高级/ }));
        const discord = screen.getByRole("button", { name: i18n.t("settings:support.discordButton") });
        expect(discord).toHaveClass("text-ink", "border-border", "bg-background");
        expect(discord.querySelector("svg")).toHaveAttribute("fill", "currentColor");
    });
    it("shows the legal wording and links together in Chinese", async () => {
        await i18n.changeLanguage("zh-CN");
        render(<HelpAboutPage onReplayOnboarding={vi.fn()} />);
        expect(await screen.findByText("Isoform 根据 Functional Source License 开源。")).toBeInTheDocument();
        const license = screen.getByRole("button", { name: i18n.t("settings:aboutPolish.license") });
        const privacy = screen.getByRole("button", { name: i18n.t("settings:aboutPolish.privacy") });
        expect(license.parentElement).toBe(privacy.parentElement);
        expect(license.parentElement).toHaveClass("whitespace-nowrap");
    });
    it("shows board rows, build details, paired phone, Get help with its email row outside Advanced (ST26), and recovery controls", async () => {
        const setup = vi.fn();
        render(<HelpAboutPage onReplayOnboarding={setup} />);
        await screen.findByText("Isoform for your computer");
        for (const text of [
            "What is new",
            "Build",
            "Computer",
            "Data",
            "Support",
            "Legal",
        ])
            expect(screen.getByText(text)).toBeInTheDocument();
        expect(
            screen.getByText(/Mac mini · macOS 26.1 · paired with My phone/),
        ).toBeInTheDocument();
        expect(screen.getByText("Isoform is open source under the Functional Source License.")).toBeInTheDocument();
        const license = screen.getByRole("button", { name: "License" });
        const privacy = screen.getByRole("button", { name: "Privacy" });
        expect(license.parentElement).toBe(privacy.parentElement);
        expect(license.parentElement).toHaveClass("whitespace-nowrap");
        expect(screen.getByText(/1.3 GB/)).toBeInTheDocument();
        expect(screen.getByText("Email support")).toBeInTheDocument();
        expect(
            screen.getByText("Write to bo@isoform.ai. Your mail app opens a draft with your version."),
        ).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "Send feedback" })).toBeInTheDocument();
        expect(
            screen.getByRole("button", { name: "Copy report" }),
        ).toBeInTheDocument();
        expect(
            screen.getByRole("button", { name: "Join Discord" }),
        ).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: /Advanced/ }));
        fireEvent.click(
            screen.getByRole("button", { name: "Run setup again" }),
        );
        expect(setup).toHaveBeenCalledOnce();

        expect(
            screen.getByRole("heading", {
                name: "What the support report includes",
            }),
        ).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Open folder" }));
        await waitFor(() => expect(mocks.openFolder).toHaveBeenCalledOnce());
        fireEvent.click(
            screen.getByRole("button", { name: "Save logs for support" }),
        );
        await waitFor(() => expect(mocks.saveLogs).toHaveBeenCalledOnce());
        fireEvent.click(screen.getByRole("button", { name: "Release notes" }));
        await waitFor(() =>
            expect(mocks.openURL).toHaveBeenCalledWith(info.releaseNotesUrl),
        );
    });
    it("uses the theme accent for the About mark", async () => {
        render(<HelpAboutPage onReplayOnboarding={vi.fn()} />);
        const title = await screen.findByRole("heading", {
            name: "Isoform for your computer",
        });
        const mark = title.closest("section")?.querySelector(
            'svg[viewBox="0 0 19 19"]',
        );
        expect(mark).toHaveClass("text-accent");
        expect(mark).not.toHaveClass("text-ink");
    });
    it.each(["update-available", "downloading", "update-downloaded"])(
        "shows the selected release summary and its own notes link while %s, even with a newer upstream release",
        async (state) => {
            const notesURL =
                "https://github.com/example/isoform-releases/releases/tag/v0.1.302";
            Object.assign(info, {
                releaseNotesVersion: "0.1.302",
                releaseNotes:
                    "Today with **topics**, the pinned main chat panel, one Search with commands.",
                releaseNotesUrl: notesURL,
                update: { status: state, version: "0.1.302", percent: 42 },
            });
            // The feed has moved on; reading About must use the selected metadata.
            mocks.check.mockResolvedValue({
                available: true,
                latest: "0.1.303",
                release_notes: "Different upstream notes.",
            });
            render(<HelpAboutPage onReplayOnboarding={vi.fn()} />);
            const row = (await screen.findByText("What is new")).parentElement!;
            expect(within(row).getByText("topics")).toHaveProperty(
                "tagName",
                "STRONG",
            );
            expect(within(row).getByText("topics").closest(".prose")).not.toBeNull();
            expect(row).toHaveTextContent(
                "v0.1.302: Today with topics, the pinned main chat panel, one Search with commands.",
            );
            expect(screen.queryByText("Different upstream notes.")).not.toBeInTheDocument();
            fireEvent.click(within(row).getByRole("button", { name: "Release notes" }));
            await waitFor(() => expect(mocks.openURL).toHaveBeenCalledWith(notesURL));
            expect(mocks.check).not.toHaveBeenCalled();
        },
    );
    it("checks, downloads without restarting, and only restarts on the explicit action", async () => {
        render(<HelpAboutPage onReplayOnboarding={vi.fn()} />);
        expect(
            await screen.findByText(/Up to date. Checked today at/),
        ).toBeInTheDocument();
        fireEvent.click(
            screen.getByRole("button", { name: "Check for updates" }),
        );
        await waitFor(() => expect(mocks.check).toHaveBeenCalledOnce());
        await status({ status: "checking" });
        expect(screen.getByText("Checking for updates…")).toBeInTheDocument();
        expect(
            screen.getByRole("button", { name: "Checking…" }),
        ).toBeDisabled();
        await status({ status: "update-available", version: "0.1.302" });
        fireEvent.click(
            screen.getByRole("button", { name: "Download update" }),
        );
        await waitFor(() => expect(mocks.download).toHaveBeenCalledOnce());
        expect(mocks.restart).not.toHaveBeenCalled();
        await status({
            status: "downloading",
            version: "0.1.302",
            percent: 42,
        });
        expect(screen.getByText(/42%/)).toBeInTheDocument();
        expect(screen.getByRole("progressbar")).toHaveAttribute(
            "aria-valuenow",
            "42",
        );
        await status({ status: "update-downloaded", version: "0.1.302" });
        expect(
            screen.getByText("v0.1.301 · v0.1.302 is downloaded"),
        ).toBeInTheDocument();
        expect(
            screen.getByText(/Observing keeps running while it does/),
        ).toBeInTheDocument();
        fireEvent.click(
            screen.getByRole("button", { name: "Restart to update" }),
        );
        expect(
            screen.getByRole("button", { name: "Restarting…" }),
        ).toBeDisabled();
        await waitFor(() => expect(mocks.restart).toHaveBeenCalledOnce());
        expect(
            screen.getByRole("button", { name: "Restarting…" }),
        ).toBeDisabled();
    });
    it("persists the background switch and keeps failures visible and retryable", async () => {
        render(<HelpAboutPage onReplayOnboarding={vi.fn()} />);
        const toggle = await screen.findByRole("switch");
        expect(toggle).toBeChecked();
        fireEvent.click(toggle);
        await waitFor(() =>
            expect(mocks.background).toHaveBeenCalledWith(false),
        );
        await waitFor(() => expect(toggle).not.toBeChecked());
        mocks.check.mockRejectedValue(new Error("network"));
        fireEvent.click(
            screen.getByRole("button", { name: "Check for updates" }),
        );
        expect(await screen.findByRole("alert")).toHaveTextContent(
            "This action could not finish",
        );
        expect(
            screen.getByRole("button", { name: "Check for updates" }),
        ).toBeEnabled();
    });
    it("explains the release-only rule without claiming staging is up to date", async () => {
        info.update = { status: "disabled" };
        render(<HelpAboutPage onReplayOnboarding={vi.fn()} />);
        expect(
            await screen.findByText(
                "Update checks are available in release builds.",
            ),
        ).toBeInTheDocument();
        expect(
            screen.getByRole("button", { name: "Check for updates" }),
        ).toBeDisabled();
        expect(mocks.check).not.toHaveBeenCalled();
    });
    it("refreshes the saved background policy without overwriting newer download progress", async () => {
        info.backgroundUpdates = false;
        info.update = { status: "update-available", version: "0.1.302" };
        render(<HelpAboutPage onReplayOnboarding={vi.fn()} />);
        const toggle = await screen.findByRole("switch");
        let finishMetadata: (value: AboutInfo) => void = () => {};
        mocks.getAbout.mockImplementationOnce(
            () =>
                new Promise<AboutInfo>((resolve) => {
                    finishMetadata = resolve;
                }),
        );
        fireEvent.click(toggle);
        await waitFor(() => expect(mocks.getAbout).toHaveBeenCalledTimes(2));
        await status({
            status: "downloading",
            version: "0.1.302",
            percent: 42,
        });
        await act(async () => {
            finishMetadata({
                ...info,
                backgroundUpdates: true,
                update: { status: "update-available", version: "0.1.302" },
            });
        });
        expect(toggle).toBeChecked();
        expect(screen.getByRole("progressbar")).toHaveAttribute(
            "aria-valuenow",
            "42",
        );
    });
    it("names a staging build by its short commit and a release build never by one (ST85)", async () => {
        info = { ...info, build: "staging", commit: "d1282d52", buildTime: new Date(2026, 9, 22, 22, 22).toISOString() };
        render(<HelpAboutPage onReplayOnboarding={vi.fn()} />);
        const build = await screen.findByText("Build");
        const row = build.closest("[data-about-row]") ?? build.parentElement!;
        expect(row.textContent).toBe("BuildStaging · d1282d52 · Oct 22 at 10:22 PM");
    });
    it("names a release build by the version people know, Isoform 1.0.32 (ST85)", async () => {
        info = { ...info, version: "1.0.32", build: "release", releaseNotesVersion: "1.0.32", commit: "d1282d52" };
        render(<HelpAboutPage onReplayOnboarding={vi.fn()} />);
        expect(await screen.findByText("Isoform 1.0.32")).toBeInTheDocument();
        expect(screen.queryByText(/d1282d52/)).toBeNull();
        expect(screen.queryByText(/v0\.1\.301/)).toBeNull();
    });
    it("keeps the version readable when richer About metadata is unavailable", async () => {
        mocks.getAbout.mockRejectedValue(
            new Error("native metadata unavailable"),
        );
        render(<HelpAboutPage onReplayOnboarding={vi.fn()} />);
        expect(await screen.findByText("v0.1.301")).toBeInTheDocument();
        expect(
            screen.getByRole("button", { name: "Retry" }),
        ).toBeInTheDocument();
    });
    it("offers a retry when About metadata fails", async () => {
        mocks.getAbout.mockRejectedValue(new Error("offline"));
        render(<HelpAboutPage onReplayOnboarding={vi.fn()} />);
        fireEvent.click(await screen.findByRole("button", { name: "Retry" }));
        await waitFor(() => expect(mocks.getAbout).toHaveBeenCalledTimes(2));
    });
    it("renders the same ready state and controls in Chinese", async () => {
        await i18n.changeLanguage("zh-CN");
        info.releaseNotesVersion = "0.1.302";
        info.releaseNotes = "Today with **topics**.";
        info.update = { status: "update-downloaded", version: "0.1.302" };
        render(<HelpAboutPage onReplayOnboarding={vi.fn()} />);
        expect(
            await screen.findByText("v0.1.301 · v0.1.302 已下载"),
        ).toBeInTheDocument();
        expect(screen.getByText("新功能").parentElement).toHaveTextContent(
            "v0.1.302： Today with topics.",
        );
        expect(
            screen.getByRole("button", { name: "重启以更新" }),
        ).toBeInTheDocument();
        expect(
            screen.getByRole("switch", {
                name: "在后台下载更新，并在下次重启时安装",
            }),
        ).toBeChecked();
        expect(
            screen.getByRole("button", { name: "保存支持日志" }),
        ).toBeInTheDocument();
    });
});
