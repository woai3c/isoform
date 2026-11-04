import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import type { PlaybookView, SopEntry } from "@/lib/wails-shim-app";
import en from "@/i18n/locales/en/app.json";
import zh from "@/i18n/locales/zh-CN/app.json";
import { PlaybookSuggestionCard } from "./PlaybookSuggestionCard";
import { PlaybookDeliveryPanel } from "./PlaybookDeliveryPanel";

const mocks = vi.hoisted(() => ({ setSchedule: vi.fn() }));

vi.mock("@/lib/wails-shim-app", () => ({ SetPlaybookSchedule: mocks.setSchedule }));
vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

const entry: SopEntry = {
    id: "posthog-morning-check",
    title: "PostHog morning check",
    description: "Reads the numbers you check on PostHog and sends them to you.",
    trigger: "Most days around 9:05",
    status: "draft",
    source: "observed",
    sourceIds: [],
    version: 1,
    createdAt: 1,
    updatedAt: 1,
    scripts: [],
    sites: ["https://us.posthog.com/"],
    body: "# Goal\n",
};

const proposed: PlaybookView = {
    id: entry.id,
    proposal: {
        host: "us.posthog.com",
        siteName: "PostHog",
        time: "9:05",
        days: 4,
        weekdays: true,
        titles: ["Downloads", "Retention", "Time in app"],
        proposedAt: 1,
        answer: "",
        answeredAt: 0,
    },
    schedule: null,
    channels: [],
    sites: [],
    lastRun: null,
    nextRunAt: 0,
    suggestedTime: "08:55",
    held: "",
};

describe("PlaybookSuggestionCard", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        await i18n.changeLanguage("en");
    });

    it("says what Isoform saw in plain words and takes a one-tap yes", () => {
        const onAccept = vi.fn();
        const onDecline = vi.fn();
        render(<PlaybookSuggestionCard entry={entry} plan={proposed} busy={false} onAccept={onAccept} onDecline={onDecline} />);
        expect(screen.getByText("Isoform noticed a routine")).toBeInTheDocument();
        expect(
            screen.getByText(/You open PostHog most days around 9:05: 4 of the last 7 days\. You look at Downloads, Retention, and Time in app\./),
        ).toBeInTheDocument();
        expect(screen.getByText("Want Isoform to check it for you at 08:55 each weekday and send you the numbers?")).toBeInTheDocument();
        expect(screen.getByText("It opens pages on us.posthog.com in your browser and only reads them.")).toBeInTheDocument();
        expect(screen.queryByRole("button", { name: "See the steps" })).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Do this every weekday" }));
        fireEvent.click(screen.getByRole("button", { name: "Not now" }));
        expect(onAccept).toHaveBeenCalledTimes(1);
        expect(onDecline).toHaveBeenCalledTimes(1);
    });

    it("reads in Chinese", async () => {
        await i18n.changeLanguage("zh-CN");
        render(<PlaybookSuggestionCard entry={entry} plan={proposed} busy={false} onAccept={vi.fn()} onDecline={vi.fn()} onSee={vi.fn()} />);
        expect(screen.getByText("要让 Isoform 每个工作日 08:55 替你查看，并把数字发给你吗？")).toBeInTheDocument();
        expect(screen.getByRole("button", { name: "查看步骤" })).toBeInTheDocument();
    });
});

describe("PlaybookDeliveryPanel", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        await i18n.changeLanguage("en");
    });

    const scheduled: PlaybookView = {
        ...proposed,
        proposal: { ...proposed.proposal!, answer: "accepted" },
        schedule: { jobId: "job-1", time: "08:55", weekdays: true, enabled: true },
        channels: ["desktop", "text"],
        sites: ["https://us.posthog.com/"],
        lastRun: { runId: "r1", at: Date.now(), ok: true, result: "Downloads: 1,240 (+8%)" },
        nextRunAt: Date.now() + 3600_000,
    };

    it("shows the schedule, the channels, the pages and the last result, and saves a change", async () => {
        mocks.setSchedule.mockResolvedValue({ ...scheduled, channels: ["desktop", "text", "email"] });
        const onSaved = vi.fn();
        render(
            <PlaybookDeliveryPanel
                entry={entry}
                plan={scheduled}
                channels={{ desktop: true, push: false, text: true, email: true, textNames: true }}
                defaultChannels={["desktop", "text"]}
                onSaved={onSaved}
            />,
        );
        expect(screen.getByRole("switch", { name: "Run on a schedule" })).toBeChecked();
        expect(screen.getByDisplayValue("08:55")).toBeInTheDocument();
        expect(screen.getByText(/Downloads: 1,240 \(\+8%\)/)).toBeInTheDocument();
        expect(screen.getByText("us.posthog.com")).toBeInTheDocument();
        expect(screen.getByText("Pair a phone on the Phone page first.")).toBeInTheDocument();
        const save = screen.getByRole("button", { name: "Save" });
        expect(save).toBeDisabled();

        fireEvent.click(screen.getByRole("checkbox", { name: "Email" }));
        expect(save).toBeEnabled();
        fireEvent.click(save);
        await waitFor(() =>
            expect(mocks.setSchedule).toHaveBeenCalledWith({
                id: entry.id,
                enabled: true,
                time: "08:55",
                weekdays: true,
                channels: ["desktop", "text", "email"],
                sites: ["https://us.posthog.com/"],
            }),
        );
        await waitFor(() => expect(onSaved).toHaveBeenCalled());
    });

    it("keeps unsaved edits when the same playbook reloads, and says when texts name nothing", () => {
        const props = {
            entry,
            channels: { desktop: true, push: true, text: true, email: false, textNames: false },
            defaultChannels: ["desktop", "text"],
            onSaved: vi.fn(),
        };
        const view = render(<PlaybookDeliveryPanel {...props} plan={scheduled} />);
        expect(
            screen.getByText("Texts say only that a playbook finished until you allow names in texts on the Phone page."),
        ).toBeInTheDocument();
        fireEvent.click(screen.getByRole("checkbox", { name: "Your phone" }));
        expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
        // A background reload hands the panel a new object for the same plan.
        view.rerender(<PlaybookDeliveryPanel {...props} plan={{ ...scheduled, lastRun: { ...scheduled.lastRun!, result: "newer" } }} />);
        expect(screen.getByRole("checkbox", { name: "Your phone" })).toBeChecked();
        expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
        // Another playbook starts from its own plan.
        view.rerender(<PlaybookDeliveryPanel {...props} entry={{ ...entry, id: "other" }} plan={{ ...scheduled, id: "other" }} />);
        expect(screen.getByRole("checkbox", { name: "Your phone" })).not.toBeChecked();
    });

    it("says a schedule waits for approval and lets it be saved as it is", async () => {
        mocks.setSchedule.mockResolvedValue({ ...scheduled, held: "" });
        const held: PlaybookView = { ...scheduled, schedule: { ...scheduled.schedule!, enabled: false }, held: "unapproved" };
        render(
            <PlaybookDeliveryPanel
                entry={entry}
                plan={held}
                channels={{ desktop: true, push: false, text: true, email: true, textNames: true }}
                defaultChannels={["desktop", "text"]}
                onSaved={vi.fn()}
            />,
        );
        expect(
            screen.getByText("Steps changed before approval was added. Open and save to approve."),
        ).toBeInTheDocument();
        const save = screen.getByRole("button", { name: "Save" });
        expect(save).toBeEnabled();
        fireEvent.click(save);
        await waitFor(() => expect(mocks.setSchedule).toHaveBeenCalled());
    });

    it("W10: a failed save is copper in place, never a toast, and Try again saves", async () => {
        mocks.setSchedule.mockRejectedValueOnce(new Error("disk full")).mockResolvedValue(scheduled);
        const onSaved = vi.fn();
        render(
            <PlaybookDeliveryPanel
                entry={entry}
                plan={scheduled}
                channels={{ desktop: true, push: false, text: true, email: true, textNames: true }}
                defaultChannels={["desktop", "text"]}
                onSaved={onSaved}
            />,
        );
        fireEvent.click(screen.getByRole("checkbox", { name: "Email" }));
        fireEvent.click(screen.getByRole("button", { name: "Save" }));
        expect(await screen.findByText("Didn’t save.")).toBeInTheDocument();
        expect(document.body.textContent).not.toContain("disk full");
        fireEvent.click(screen.getByRole("button", { name: "Try again" }));
        await waitFor(() => expect(mocks.setSchedule).toHaveBeenCalledTimes(2));
        await waitFor(() => expect(onSaved).toHaveBeenCalled());
    });

    it("W21: when the schedule didn't load it says so, never 'when you ask', and Try again re-reads", () => {
        const onRetry = vi.fn();
        render(
            <PlaybookDeliveryPanel
                entry={entry}
                plan={scheduled}
                channels={{ desktop: true, push: false, text: true, email: true, textNames: true }}
                defaultChannels={["desktop", "text"]}
                onSaved={vi.fn()}
                schedulesFailed
                onRetrySchedules={onRetry}
            />,
        );
        expect(screen.getByText("Couldn’t load when this runs.")).toBeInTheDocument();
        expect(screen.queryByText(/when you ask/i)).toBeNull();
        expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
        fireEvent.click(screen.getByRole("button", { name: "Try again" }));
        expect(onRetry).toHaveBeenCalledTimes(1);
    });

    it("has every key in both languages", () => {
        const keys = (value: unknown, prefix = ""): string[] =>
            value && typeof value === "object"
                ? Object.entries(value).flatMap(([k, v]) => keys(v, prefix ? `${prefix}.${k}` : k))
                : [prefix];
        expect(keys(zh.sopsPage).sort()).toEqual(keys(en.sopsPage).sort());
    });
});
