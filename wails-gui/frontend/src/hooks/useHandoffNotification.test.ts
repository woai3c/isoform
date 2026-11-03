import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@/i18n";

type Handler = (event: unknown) => void;
const handlers: Record<string, Handler> = {};

vi.mock("../../wailsjs/runtime/runtime", () => ({
    EventsOn: vi.fn((name: string, handler: Handler) => {
        handlers[name] = handler;
        return () => {
            delete handlers[name];
        };
    }),
}));
vi.mock("@/lib/notify", () => ({ notifyToast: vi.fn() }));
vi.mock("../lib/tts-player", () => ({ maybeSpeakNotification: vi.fn(() => Promise.resolve()) }));

import { notifyToast } from "@/lib/notify";
import { maybeSpeakNotification } from "../lib/tts-player";
import { useHandoffNotification } from "./useHandoffNotification";

describe("useHandoffNotification", () => {
    beforeEach(() => {
        vi.clearAllMocks();
        for (const name of Object.keys(handlers)) delete handlers[name];
    });

    it("does not raise a New task in Tasks toast", () => {
        const { unmount } = renderHook(() => useHandoffNotification());
        act(() => handlers["handoff:created"]({ id: "task-one", title: "Fix tests" }));
        expect(notifyToast).not.toHaveBeenCalled();
        expect(maybeSpeakNotification).toHaveBeenCalledWith("Fix tests");
        unmount();
        expect(handlers["handoff:created"]).toBeUndefined();
    });
});
