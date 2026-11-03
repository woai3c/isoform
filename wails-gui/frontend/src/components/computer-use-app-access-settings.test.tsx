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
        expect(screen.getByText("Messages and email")).toBeInTheDocument();

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
