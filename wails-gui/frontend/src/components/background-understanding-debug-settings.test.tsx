import {
    fireEvent,
    render,
    screen,
    waitFor,
    within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { BackgroundUnderstandingDebugSettings } from "./background-understanding-debug-settings";

const mocks = vi.hoisted(() => ({
    getSnapshot: vi.fn(),
    getDetail: vi.fn(),
    eventsOn: vi.fn(() => vi.fn()),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    GetBackgroundUnderstandingSnapshot: mocks.getSnapshot,
    GetBackgroundUnderstandingRunDetail: mocks.getDetail,
}));

vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: mocks.eventsOn,
}));

describe("BackgroundUnderstandingDebugSettings", () => {
    beforeEach(() => {
        vi.clearAllMocks();
        mocks.eventsOn.mockReturnValue(vi.fn());
        mocks.getSnapshot.mockResolvedValue({
            enabled: true,
            status: "waiting_for_source",
            statusReason:
                "One daily learning run is reserved for Gmail. Unlock this computer before Isoform can use it.",
            schedulingPolicyVersion:
                "background-understanding-scheduling-v2-source-fair",
            dailyRunLimit: 4,
            todayRunCount: 3,
            todayAttemptCount: 4,
            reservedRunCount: 1,
            reservedSource: "gmail",
            minIntervalMinutes: 360,
            sources: [
                {
                    source: "local_activity",
                    label: "Retained activity",
                    available: true,
                    connected: true,
                    allowed: true,
                },
                {
                    source: "gmail",
                    label: "Gmail",
                    available: true,
                    connected: true,
                    allowed: true,
                    grantId: "grant-1",
                    scopeLabel: "Current wider correspondence",
                },
            ],
            runs: [
                {
                    id: "run-1",
                    missionKind: "gmail",
                    source: "gmail",
                    state: "completed",
                    triggerKind: "manual",
                    triggerReason: "User requested a bounded mission.",
                    startedAt: "2026-07-30T12:00:00Z",
                    updatedAt: "2026-07-30T12:01:00Z",
                    summary: "Reviewed bounded Gmail metadata.",
                    sourceItemCount: 2,
                    materializationCount: 1,
                },
            ],
        });
        mocks.getDetail.mockResolvedValue({
            run: {
                id: "run-1",
                missionKind: "gmail",
                source: "gmail",
                triggerKind: "manual",
                triggerReason: "User requested a bounded mission.",
                grantId: "grant-1",
                policyVersion: "background-understanding-v2-computer-use",
                sourceScopeJson:
                    '{"accessMode":"computer_use_only","allowedOperations":["gmail.computer_use.read"],"oauthFallback":false,"scopeLabel":"Work and personal correspondence"}',
                currentState: "completed",
                startedAt: 1,
                updatedAt: 2,
                detailJson: '{"summary":"done"}',
            },
            events: [
                {
                    id: "event-1",
                    sequence: 1,
                    state: "selected",
                    detailJson: '{"scopeHash":"abc"}',
                    detailHash: "abcdef0123456789",
                    occurredAt: 1,
                },
                {
                    id: "event-2",
                    sequence: 2,
                    state: "completed",
					detailJson:
						'{"transport":"computer_use","oauthFallback":false,"browser":"Safari","axStatus":"populated","snapshotHash":"snapshot-abc","actions":[{"tool":"list_apps","outcome":"completed"},{"tool":"get_window_state","outcome":"completed"}]}',
                    detailHash: "fedcba9876543210",
                    occurredAt: 2,
                },
            ],
            sourceItems: [
                {
                    id: "source-1",
                    source: "gmail",
                    sourceItemKind: "message_metadata",
                    externalIdHash: "externalhash123456789",
                    contentHash: "contenthash123456789",
                    attributionKind: "other",
                    metadataJson: '{"hasSnippet":true}',
                },
            ],
            materializations: [
                {
                    id: "artifact-1",
                    artifactKind: "knowledge",
                    artifactId: "gmail-knowledge-1",
                    action: "created",
                    summary: "Launch coordination",
                },
            ],
        });
    });

    it("leads with product language while preserving exact technical details", async () => {
        const user = userEvent.setup();
        render(<BackgroundUnderstandingDebugSettings />);

        expect(
            await screen.findByText(
                "Reviewed 2 source records and added 1 learning item.",
            ),
        ).toBeInTheDocument();
        expect(screen.getByText("Waiting for an allowed source")).toBeInTheDocument();
        expect(screen.getByText("3 of 4 checks today")).toBeInTheDocument();
        expect(screen.getByText("Checks attempted")).toBeInTheDocument();
        expect(screen.getByText("Checks set aside")).toBeInTheDocument();
        expect(screen.getByText("4")).toBeInTheDocument();
        expect(screen.getByText("1 for Gmail")).toBeInTheDocument();
        expect(
            screen.getByText(
                "Isoform is waiting for an allowed source to become available.",
            ),
        ).toBeInTheDocument();

        const rawStatusReason = screen.getByText(
            /Unlock this computer before Isoform can use it/,
        );
        const schedulingPolicy = screen.getByText(
            "background-understanding-scheduling-v2-source-fair",
        );
        expect(rawStatusReason).not.toBeVisible();
        expect(schedulingPolicy).not.toBeVisible();

        const statusDetails = rawStatusReason.closest("details");
        expect(statusDetails).not.toBeNull();
        await user.click(
            within(statusDetails!).getByText("Technical details"),
        );
        expect(rawStatusReason).toBeVisible();
        expect(schedulingPolicy).toBeVisible();

        const sourceRegion = screen.getByRole("region", {
            name: "Allowed learning sources",
        });
        const gmailSource = within(sourceRegion)
            .getByText("Gmail")
            .closest("article");
        expect(gmailSource).not.toBeNull();
        expect(
            within(gmailSource!).getByText("Learning allowed · Ready"),
        ).toBeInTheDocument();
        const grantID = within(gmailSource!).getByText("grant grant-1");
        expect(grantID).not.toBeVisible();
        await user.click(
            within(gmailSource!).getByText("Technical details"),
        );
        expect(grantID).toBeVisible();

        await user.click(screen.getByRole("button", { name: /gmail/i }));

        await waitFor(() =>
            expect(mocks.getDetail).toHaveBeenCalledWith("run-1"),
        );
        expect(
            screen.getByRole("region", {
                name: "Background learning details",
            }),
        ).toHaveClass("block");
        expect(
            await screen.findByText("What Isoform could access"),
        ).toBeInTheDocument();
        expect(
            screen.getByText(
                "The allowed Gmail view covered work and personal correspondence.",
            ),
        ).toBeInTheDocument();
        expect(
            screen.getByText("You asked Isoform to learn from this source now."),
        ).toBeInTheDocument();
        expect(screen.getByText("How Isoform accessed the source")).toBeInTheDocument();
        expect(screen.getByText("Browser control")).toBeInTheDocument();
        expect(screen.getByText("Not used")).toBeInTheDocument();
        expect(screen.getByText("Safari")).toBeInTheDocument();
        expect(screen.getByText("What happened")).toBeInTheDocument();
        expect(screen.getByText(/1\. Source selected/)).toBeInTheDocument();
        expect(screen.getByText(/2\. Learning completed/)).toBeInTheDocument();
        expect(screen.getByText("Information reviewed")).toBeInTheDocument();
        expect(screen.getByText("Email details")).toBeInTheDocument();
        expect(screen.getByText("Created reusable know-how")).toBeInTheDocument();
        expect(screen.getByText("Launch coordination")).toBeInTheDocument();

        const rawScope = screen.getByText(/gmail.computer_use.read/);
        expect(rawScope).not.toBeVisible();
        const scopeDetails = rawScope.closest("details");
        expect(scopeDetails).not.toBeNull();
        await user.click(within(scopeDetails!).getByText("Technical details"));
        expect(rawScope).toBeVisible();

        const rawTransport = screen.getByText("transport computer_use");
        expect(rawTransport).not.toBeVisible();
        const transportDetails = rawTransport.closest("details");
        expect(transportDetails).not.toBeNull();
        await user.click(
            within(transportDetails!).getByText("Technical details"),
        );
        expect(rawTransport).toBeVisible();

        const rawSourceID = screen.getByText(/externalhash123456789/);
        expect(rawSourceID).not.toBeVisible();
        const sourceDetails = rawSourceID.closest("details");
        expect(sourceDetails).not.toBeNull();
        await user.click(
            within(sourceDetails!).getByText("Technical details"),
        );
        expect(rawSourceID).toBeVisible();

        const rawArtifactID = screen.getByText(/gmail-knowledge-1/);
        expect(rawArtifactID).not.toBeVisible();
        const artifactDetails = rawArtifactID.closest("details");
        expect(artifactDetails).not.toBeNull();
        await user.click(
            within(artifactDetails!).getByText("Technical details"),
        );
        expect(rawArtifactID).toBeVisible();

        const rawTriggerReason = screen.getByText(
            "User requested a bounded mission.",
        );
        const rawRunSummary = screen.getByText(
            "Reviewed bounded Gmail metadata.",
        );
        expect(rawTriggerReason).not.toBeVisible();
        expect(rawRunSummary).not.toBeVisible();
        const runDetails = rawTriggerReason.closest("details");
        expect(runDetails).not.toBeNull();
        await user.click(within(runDetails!).getByText("Technical details"));
        expect(rawTriggerReason).toBeVisible();
        expect(rawRunSummary).toBeVisible();

        const detail = screen.getByRole("region", {
            name: "Background learning details",
        });
        fireEvent.keyDown(detail, { key: "Escape" });
        expect(
            screen.getByRole("region", {
                name: "Background learning history",
            }),
        ).toHaveClass("block");
    });

    it("does not expose raw failure strings in the learning history", async () => {
        mocks.getSnapshot.mockResolvedValue({
            enabled: true,
            status: "ready",
            statusReason: "eligible=true",
            schedulingPolicyVersion: "background-understanding-v2",
            dailyRunLimit: 4,
            todayRunCount: 1,
            todayAttemptCount: 1,
            reservedRunCount: 0,
            minIntervalMinutes: 360,
            sources: [],
            runs: [
                {
                    id: "run-failed",
                    missionKind: "gmail",
                    source: "gmail",
                    state: "failed",
                    triggerKind: "scheduler",
                    triggerReason: "scheduler selected grant-1",
                    startedAt: "2026-07-30T12:00:00Z",
                    updatedAt: "2026-07-30T12:01:00Z",
                    summary: "context canceled",
                    sourceItemCount: 0,
                    materializationCount: 0,
                },
            ],
        });

        render(<BackgroundUnderstandingDebugSettings />);

        expect(
            await screen.findByText(
                "Isoform could not finish this learning check.",
            ),
        ).toBeInTheDocument();
        expect(screen.getByText("Could not finish")).toBeInTheDocument();

        const rawFailure = await screen.findByText("context canceled");
        expect(rawFailure).not.toBeVisible();
        const failureDetails = rawFailure.closest("details");
        expect(failureDetails).not.toBeNull();
        fireEvent.click(
            within(failureDetails!).getByText("Technical details"),
        );
        expect(rawFailure).toBeVisible();
    });
});
