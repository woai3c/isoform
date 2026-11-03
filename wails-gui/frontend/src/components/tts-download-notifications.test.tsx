import { act, render } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { TtsDownloadNotifications } from "./tts-download-notifications";

const mocks = vi.hoisted(() => ({
    listeners: new Map<string, (data: unknown) => void>(),
    loading: vi.fn(),
    success: vi.fn(),
    error: vi.fn(),
    dismiss: vi.fn(),
}));

vi.mock("@/lib/wails-shim-runtime", () => ({
    EventsOn: (name: string, callback: (data: unknown) => void) => {
        mocks.listeners.set(name, callback);
        return () => mocks.listeners.delete(name);
    },
}));

vi.mock("sonner", () => ({
    toast: {
        loading: mocks.loading,
        success: mocks.success,
        error: mocks.error,
        dismiss: mocks.dismiss,
    },
}));

function emit(name: string, payload: unknown) {
    act(() => mocks.listeners.get(name)?.(payload));
}

describe("TtsDownloadNotifications", () => {
    beforeEach(async () => {
        await i18n.changeLanguage("en");
        vi.clearAllMocks();
        mocks.listeners.clear();
    });

    it("keeps foreground progress until the engine is ready when a background voice finishes", () => {
        render(<TtsDownloadNotifications />);
        emit("tts:download-progress", { lang: "en", downloaded: 0, total: 0, bytesPerSec: 0 });
        expect(mocks.loading).toHaveBeenCalledTimes(1);

        emit("tts:model-ready", { lang: "en", source: "background" });
        expect(mocks.success).not.toHaveBeenCalled();
        expect(mocks.dismiss).not.toHaveBeenCalled();

        emit("tts:model-ready", { lang: "en" });
        expect(mocks.success).toHaveBeenCalledTimes(1);
        expect(mocks.success).toHaveBeenCalledWith(expect.any(String), expect.objectContaining({ id: "tts-model-download-en" }));
    });

    it("stays silent for a background install with no explicit read aloud", () => {
        render(<TtsDownloadNotifications />);
        emit("tts:model-ready", { lang: "en", source: "background" });
        expect(mocks.loading).not.toHaveBeenCalled();
        expect(mocks.success).not.toHaveBeenCalled();
        expect(mocks.error).not.toHaveBeenCalled();
        expect(mocks.dismiss).not.toHaveBeenCalled();
    });

    it("retains foreground download failures and clears their progress toast", () => {
        render(<TtsDownloadNotifications />);
        emit("tts:download-progress", { lang: "en", downloaded: 0, total: 0, bytesPerSec: 0 });
        emit("tts:model-error", { lang: "en" });
        expect(mocks.dismiss).toHaveBeenCalledWith("tts-model-download-en");
        expect(mocks.error).toHaveBeenCalledTimes(1);
        emit("tts:model-ready", { lang: "en", source: "background" });
        expect(mocks.success).not.toHaveBeenCalled();
    });
});
