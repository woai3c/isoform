import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import en from "@/i18n/locales/en/chat.json";
import zh from "@/i18n/locales/zh-CN/chat.json";
import onboardingEn from "@/i18n/locales/en/onboarding.json";
import { MAIN_CHAT_SETUP_TOOL } from "@/lib/stream-helpers";
import type { StreamItem } from "@/types";
import componentSource from "./main-chat-setup-card.tsx?raw";
import {
    MainChatSetupCard,
    SETUP_PERMISSION_KEYS,
    SetupRecordLine,
    type MainChatSetupCardProps,
} from "./main-chat-setup-card";
import { StreamCard } from "./stream-card";

vi.mock("../../wailsjs/go/main/App", () => ({ OpenAutomationDeliverable: vi.fn() }));
vi.mock("../../wailsjs/runtime/runtime", () => ({ EventsOn: vi.fn(() => () => {}), EventsEmit: vi.fn() }));

function flatKeys(obj: Record<string, unknown>, prefix = ""): string[] {
    return Object.entries(obj).flatMap(([k, v]) =>
        typeof v === "object" && v !== null ? flatKeys(v as Record<string, unknown>, `${prefix}${k}.`) : [`${prefix}${k}`],
    );
}

function props(overrides: Partial<MainChatSetupCardProps> = {}): MainChatSetupCardProps {
    return {
        step: { key: "microphone", kind: "permission", status: "pending", asked: true },
        position: 3,
        total: 8,
        busy: false,
        waiting: false,
        onGrant: vi.fn(),
        onNotNow: vi.fn(),
        onOpenSettings: vi.fn(),
        ...overrides,
    };
}

function record(step: string, answer: string): StreamItem {
    return { id: `r-${step}`, kind: "system", content: "x", toolName: MAIN_CHAT_SETUP_TOOL, toolInput: { step, answer } };
}

describe("MainChatSetupCard", () => {
    beforeEach(async () => {
        await i18n.changeLanguage("en");
    });

    it("asks for one permission with one sentence, Turn on and Not now", () => {
        const p = props();
        render(<MainChatSetupCard {...p} />);
        expect(screen.getByText(en.setup.permissions.microphone.title)).toBeInTheDocument();
        expect(screen.getByText(en.setup.permissions.microphone.body)).toBeInTheDocument();
        expect(screen.getByText(/3 of 8/)).toBeInTheDocument();
        // The button names the permission the way onboarding and macOS do.
        fireEvent.click(screen.getByText("Turn on Microphone"));
        expect(p.onGrant).toHaveBeenCalledTimes(1);
        fireEvent.click(screen.getByText(en.setup.notNow));
        expect(p.onNotNow).toHaveBeenCalledTimes(1);
    });

    it("points to System Settings while waiting for a permission turned on there", () => {
        const p = props({ step: { key: "screen_recording", kind: "permission", status: "pending", asked: true }, waiting: true });
        render(<MainChatSetupCard {...p} />);
        expect(
            screen.getByText("Turn on Screen Recording in System Settings, then come back here. I'll notice on my own."),
        ).toBeInTheDocument();
        expect(screen.queryByText("Turn on Screen Recording")).toBeNull();
        fireEvent.click(screen.getByText(en.setup.openSettings));
        expect(p.onOpenSettings).toHaveBeenCalledTimes(1);
        // Turning it on in Settings sometimes needs one more tap to register.
        fireEvent.click(screen.getByText(en.setup.tryAgain));
        expect(p.onGrant).toHaveBeenCalledTimes(1);
    });

    it("shows the assistant picker inside the assistant step, or a Settings link without one", () => {
        const step = { key: "assistant", kind: "assistant" as const, status: "pending" as const, asked: true };
        const { unmount } = render(<MainChatSetupCard {...props({ step, assistantPicker: <div>picker</div> })} />);
        expect(screen.getByText(en.setup.assistant.title)).toBeInTheDocument();
        expect(screen.getByText("picker")).toBeInTheDocument();
        expect(screen.queryByText(en.setup.assistant.openSettings)).toBeNull();
        unmount();
        const p = props({ step });
        render(<MainChatSetupCard {...p} />);
        expect(screen.getByText(en.setup.assistant.body)).toBeInTheDocument();
        fireEvent.click(screen.getByText(en.setup.assistant.openSettings));
        expect(p.onOpenSettings).toHaveBeenCalledTimes(1);
    });

    it("renders no card for a legacy first-task step", () => {
        const step = { key: "first_task", kind: "first_task" as const, status: "pending" as const, asked: true };
        const { container } = render(<MainChatSetupCard {...props({ step })} />);

        expect(container).toBeEmptyDOMElement();
        expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });

    it("asks what to call the user with a prefilled field, Save and Not now", () => {
        const step = { key: "name", kind: "name" as const, status: "pending" as const, asked: true, suggestion: "Ada Lovelace" };
        const onSaveName = vi.fn();
        const p = props({ step, position: 1, total: 4, onSaveName });
        const { unmount } = render(<MainChatSetupCard {...p} />);
        expect(screen.getByText(en.setup.name.title)).toBeInTheDocument();
        expect(screen.getByText(/1 of 4/)).toBeInTheDocument();
        const input = screen.getByLabelText(en.setup.name.title) as HTMLInputElement;
        expect(input.value).toBe("Ada Lovelace");
        // The user shortens it; the saved name is trimmed.
        fireEvent.change(input, { target: { value: "  Ada " } });
        fireEvent.click(screen.getByText(en.setup.name.save));
        expect(onSaveName).toHaveBeenCalledWith("Ada", false);
        fireEvent.click(screen.getByText(en.setup.notNow));
        expect(p.onNotNow).toHaveBeenCalledTimes(1);
        // Enter in the field saves too.
        fireEvent.submit(input);
        expect(onSaveName).toHaveBeenCalledTimes(2);
        unmount();
        // Nothing to save until there is a name.
        render(<MainChatSetupCard {...props({ step: { ...step, suggestion: undefined }, onSaveName })} />);
        const save = screen.getByText(en.setup.name.save).closest("button") as HTMLButtonElement;
        expect(save.disabled).toBe(true);
        expect((screen.getByLabelText(en.setup.name.title) as HTMLInputElement).placeholder).toBe(en.setup.name.placeholder);
    });

    it("says when the name was not saved and starts over on a new suggestion", async () => {
        const step = { key: "name", kind: "name" as const, status: "pending" as const, asked: true, suggestion: "Ada Lovelace" };
        const onSaveName = vi.fn(async () => ({ kind: "failed" as const }));
        const { rerender } = render(<MainChatSetupCard {...props({ step, onSaveName })} />);
        expect(screen.queryByText(en.setup.name.saveError)).toBeNull();
        fireEvent.click(screen.getByText(en.setup.name.save));
        expect(await screen.findByText(en.setup.name.saveError)).toBeInTheDocument();
        // A name stored meanwhile replaces what the field held.
        rerender(<MainChatSetupCard {...props({ step: { ...step, suggestion: "Ada L." }, onSaveName })} />);
        expect((screen.getByLabelText(en.setup.name.title) as HTMLInputElement).value).toBe("Ada L.");
        expect(screen.queryByText(en.setup.name.saveError)).toBeNull();
    });

    it("asks which name to keep when another one was stored meanwhile", async () => {
        const step = { key: "name", kind: "name" as const, status: "pending" as const, asked: true, suggestion: "Ada Lovelace" };
        const onSaveName = vi.fn(async (_name: string, replace?: boolean) =>
            replace ? ({ kind: "saved" as const }) : ({ kind: "stored" as const, name: "Ada L." }),
        );
        render(<MainChatSetupCard {...props({ step, onSaveName })} />);
        fireEvent.click(screen.getByText(en.setup.name.save));
        expect(await screen.findByText("I already have you down as Ada L.. Which should I use?")).toBeInTheDocument();
        expect(screen.queryByText(en.setup.name.save)).toBeNull();
        fireEvent.click(screen.getByText("Keep Ada L."));
        expect(onSaveName).toHaveBeenLastCalledWith("Ada L.", false);
        fireEvent.click(await screen.findByText("Call me Ada Lovelace"));
        expect(onSaveName).toHaveBeenLastCalledWith("Ada Lovelace", true);
        expect(screen.queryByText(en.setup.name.saveError)).toBeNull();
    });

    it("turns answered asks into quiet record lines, also through StreamCard", () => {
        render(
            <>
                <SetupRecordLine item={record("microphone", "granted")} />
                <SetupRecordLine item={record("contacts", "not_now")} />
                <SetupRecordLine item={record("assistant", "granted")} />
                <SetupRecordLine item={record("first_task", "done")} />
            </>,
        );
        expect(screen.getByText("Microphone is on")).toBeInTheDocument();
        expect(screen.getByText("Contacts: not now")).toBeInTheDocument();
        expect(screen.getByText(en.setup.record.assistantReady)).toBeInTheDocument();
        expect(screen.getByText(en.setup.record.done)).toBeInTheDocument();
        // The name line says the name the user gave.
        const named = record("name", "granted");
        named.toolInput = { step: "name", answer: "granted", name: "Ada" };
        render(
            <>
                <SetupRecordLine item={named} />
                <SetupRecordLine item={record("name", "not_now")} />
            </>,
        );
        expect(screen.getByText("I'll call you Ada")).toBeInTheDocument();
        expect(screen.getByText(en.setup.record.nameNotNow)).toBeInTheDocument();
        render(<StreamCard item={record("calendars", "granted")} />);
        expect(document.querySelector("[data-setup-record='calendars']")).not.toBeNull();
    });

    it("has words for every permission in both locales, unlike the onboarding window, and no accent bars", () => {
        for (const key of SETUP_PERMISSION_KEYS) {
            expect(en.setup.permissions[key].title, key).toBeTruthy();
            expect(en.setup.names[key], key).toBeTruthy();
        }
        expect(flatKeys(zh.setup).sort()).toEqual(flatKeys(en.setup).sort());
        // No run of seven words is shared with the onboarding window's copy.
        const words = (text: string) => text.toLowerCase().replace(/[^a-z' ]/g, " ").split(/\s+/).filter(Boolean);
        const onboardingWords = words(JSON.stringify(onboardingEn)).join(" ");
        for (const key of SETUP_PERMISSION_KEYS) {
            const w = words(en.setup.permissions[key].body);
            for (let i = 0; i + 7 <= w.length; i++) {
                const run = w.slice(i, i + 7).join(" ");
                expect(onboardingWords.includes(run), `${key}: "${run}"`).toBe(false);
            }
        }
        expect(en.setup.permissions).not.toHaveProperty("reminders");
        expect(componentSource).not.toMatch(/border-l-|before:w-|after:w-|w-0\.5|w-\[2px\]/);
    });
});
