import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import en from "@/i18n/locales/en/settings.json";
import zh from "@/i18n/locales/zh-CN/settings.json";
import componentSource from "./computer-use-app-access-settings.tsx?raw";
import {
    ComputerUseAccessCard,
    ComputerUseAppList,
    useComputerUseAppAccess,
} from "./computer-use-app-access-settings";

const mocks = vi.hoisted(() => ({
    getSnapshot: vi.fn(),
    browserOpenURL: vi.fn(),
    listeners: new Map<string, () => void>(),
}));

vi.mock("@/lib/wails-shim-app", () => ({
    GetComputerUseAppAccessSnapshot: mocks.getSnapshot,
}));

vi.mock("../../wailsjs/runtime/runtime", () => ({
    BrowserOpenURL: mocks.browserOpenURL,
    EventsOn: (name: string, callback: () => void) => {
        mocks.listeners.set(name, callback);
        return () => mocks.listeners.delete(name);
    },
}));

vi.mock("./enable-computer-use-dialog", () => ({
    EnableComputerUseDialog: ({ open }: { open: boolean }) =>
        open ? <div role="dialog">Permission setup</div> : null,
}));

function snapshot(overrides: Record<string, unknown> = {}) {
    return {
        policyVersion: "computer-use-app-access-v1",
        mode: "computer_use_only",
        oauthFallback: false,
        available: true,
        accessibility: true,
        screenRecording: true,
        screenLocked: false,
        status: "ready",
        statusReason: "Existing signed-in sessions are ready.",
        apps: [
            {
                id: "gmail",
                label: "Gmail",
                category: "communication",
                entryUrl: "https://mail.google.com/",
            },
        ],
        ...overrides,
    };
}

// The page shares one snapshot between the everyday card and the Advanced list.
function Harness() {
    const access = useComputerUseAppAccess();
    return (
        <>
            <ComputerUseAccessCard access={access} />
            <ComputerUseAppList access={access} />
        </>
    );
}

describe("Using your apps: app access", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        mocks.listeners.clear();
        await i18n.changeLanguage("en");
        mocks.getSnapshot.mockResolvedValue(snapshot());
    });

    it("explains readiness in plain words with one setup control and no policy details", async () => {
        render(<Harness />);

        expect(await screen.findByText("Ready")).toBeInTheDocument();
        expect(
            screen.getByText(/works inside the apps and websites you are already signed in to/),
        ).toBeInTheDocument();
        expect(screen.queryByText(/computer_use_only/)).not.toBeInTheDocument();
        expect(screen.queryByText(/computer-use-app-access-v1/)).not.toBeInTheDocument();
        expect(screen.queryByText(/OAuth/)).not.toBeInTheDocument();
        expect(screen.queryByText("Existing signed-in sessions are ready.")).not.toBeInTheDocument();

        expect(screen.getAllByRole("button", { name: /Review permissions|Set up/ })).toHaveLength(1);
        fireEvent.click(screen.getByRole("button", { name: "Review permissions" }));
        expect(screen.getByRole("dialog")).toHaveTextContent("Permission setup");
    });

    it("asks for setup while Accessibility is missing", async () => {
        mocks.getSnapshot.mockResolvedValue(
            snapshot({ accessibility: false, status: "permission_required" }),
        );
        render(<Harness />);

        expect(await screen.findByText("Needs setup")).toBeInTheDocument();
        expect(
            screen.getByText("Turn on Accessibility so Isoform can see and use your apps."),
        ).toBeInTheDocument();
        fireEvent.click(screen.getByRole("button", { name: "Set up" }));
        expect(screen.getByRole("dialog")).toBeInTheDocument();
    });

    it("lists the apps with translated categories, opens one and refreshes", async () => {
        render(<Harness />);

        expect(await screen.findByText("Gmail")).toBeInTheDocument();
        // ST73: Gmail reads "Email"; Slack and Teams read "Messages".
        expect(screen.getByText("Email")).toBeInTheDocument();
        expect(screen.queryByText("Messages and email")).not.toBeInTheDocument();

        fireEvent.click(screen.getByRole("button", { name: "Open Gmail" }));
        expect(mocks.browserOpenURL).toHaveBeenCalledWith("https://mail.google.com/");

        fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
        await waitFor(() => expect(mocks.getSnapshot).toHaveBeenCalledTimes(2));
    });

    it("reads the status again when a permission changes", async () => {
        render(<Harness />);
        await screen.findByText("Ready");

        mocks.getSnapshot.mockResolvedValue(snapshot({ screenLocked: true, status: "locked" }));
        act(() => mocks.listeners.get("permissions:changed")?.());

        expect(await screen.findByText("Waiting for you to unlock")).toBeInTheDocument();
    });

    it("has every status and category in both languages", () => {
        for (const id of [
            "checking",
            "ready",
            "read_only_ready",
            "permission_required",
            "locked",
            "unavailable",
            "unknown",
        ] as const) {
            expect(en.usingApps.status[id].title).toBeTypeOf("string");
            expect(zh.usingApps.status[id].description).toBeTypeOf("string");
        }
        expect(Object.keys(zh.usingApps.apps.categories).sort()).toEqual(
            Object.keys(en.usingApps.apps.categories).sort(),
        );
        // Every user-facing string goes through t().
        expect(componentSource).not.toMatch(/>\s*[A-Z][a-z]+(?: [a-z]+)+\s*</);
    });
});

describe("Using your apps: a failed read (ST21)", () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        mocks.listeners.clear();
        await i18n.changeLanguage("en");
    });

    it("says so in the card in copper, with no toast and no 'Checking', and Try again reads again", async () => {
        mocks.getSnapshot.mockRejectedValueOnce(new Error("denied")).mockResolvedValue(snapshot());
        render(<Harness />);

        const alert = await screen.findByRole("alert");
        expect(alert).toHaveTextContent(
            "Couldn't check app access. Isoform couldn't read its permissions on this computer.",
        );
        expect(alert.className).toContain("text-copper");
        expect(componentSource).not.toMatch(/toast/);

        fireEvent.click(screen.getByRole("button", { name: "Try again" }));
        expect(await screen.findByText("Ready")).toBeInTheDocument();
        expect(screen.queryByRole("alert")).toBeNull();
    });

    it("has the ST21 words in Chinese", () => {
        expect(zh.usingApps.loadFailedToast).toBe("无法检查应用访问权限。Isoform 无法读取它在这台电脑上的权限。");
        expect(en.usingApps.loadFailedToast).toMatch(/^Couldn't check app access\./);
    });
});
