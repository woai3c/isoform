import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { stopBriefPlayback } from "@/lib/brief-audio";
import { BriefAudioPlayer, formatAudioDuration } from "./BriefAudioPlayer";

const mocks = vi.hoisted(() => ({ getAudio: vi.fn() }));

vi.mock("@/lib/wails-shim-app", () => ({
    GetMobileBriefAudio: mocks.getAudio,
}));

const audio = { mime: "audio/mp4", bytes: 1200, durationSeconds: 95 };

describe("BriefAudioPlayer", () => {
    let play: ReturnType<typeof vi.fn>;
    let pause: ReturnType<typeof vi.fn>;

    beforeEach(async () => {
        act(() => stopBriefPlayback());
        await i18n.changeLanguage("en");
        vi.clearAllMocks();
        play = vi.fn().mockResolvedValue(undefined);
        pause = vi.fn();
        vi.spyOn(window.HTMLMediaElement.prototype, "play").mockImplementation(play);
        vi.spyOn(window.HTMLMediaElement.prototype, "pause").mockImplementation(pause);
        vi.spyOn(window.HTMLMediaElement.prototype, "load").mockImplementation(() => {});
        mocks.getAudio.mockResolvedValue({ briefId: "2026-10-14-morning", mime: "audio/mp4", base64: "AAAA", durationSeconds: 95 });
    });

    afterEach(() => {
        act(() => stopBriefPlayback());
        vi.restoreAllMocks();
    });

    it("shows the play label with the duration, loads the file on the first press and plays it", async () => {
        render(<BriefAudioPlayer briefId="2026-10-14-morning" audio={audio} />);
        const button = screen.getByTestId("brief-audio-toggle");
        expect(button).toHaveTextContent("Hear your brief");
        expect(button).toHaveTextContent("1:35");
        expect(button).toHaveAttribute("aria-pressed", "false");

        fireEvent.click(button);
        await waitFor(() => expect(button).toHaveAttribute("aria-pressed", "true"));
        expect(mocks.getAudio).toHaveBeenCalledWith("2026-10-14-morning");
        expect(play).toHaveBeenCalledTimes(1);
        expect(button).toHaveTextContent("Pause");

        fireEvent.click(button);
        expect(pause).toHaveBeenCalledTimes(1);
        await waitFor(() => expect(button).toHaveAttribute("aria-pressed", "false"));

        // The second play reuses the loaded file.
        fireEvent.click(button);
        await waitFor(() => expect(play).toHaveBeenCalledTimes(2));
        expect(mocks.getAudio).toHaveBeenCalledTimes(1);
    });

    it("keeps the play control and duration in place while audio loads", () => {
        mocks.getAudio.mockReturnValue(new Promise(() => {}));
        render(<BriefAudioPlayer briefId="brief" audio={audio} />);
        const button = screen.getByTestId("brief-audio-toggle");
        fireEvent.click(button);
        expect(button).toHaveAttribute("aria-busy", "true");
        expect(button).toHaveTextContent("Hear your brief");
        expect(button).toHaveTextContent("1:35");
        expect(screen.queryByText("Loading the spoken brief...")).not.toBeInTheDocument();
    });

    it.each(['paper', 'onedark'])('assigns the primary fill to the play circle only in %s', (theme) => {
        document.documentElement.dataset.theme = theme;
        const { container } = render(<BriefAudioPlayer briefId="brief" audio={audio} />);
        const button = screen.getByTestId('brief-audio-toggle');
        expect(button).not.toHaveClass('patina-verdigris');
        expect(button.firstElementChild).toHaveClass('patina-verdigris', 'bg-solid-btn-bg');
        expect(container.querySelectorAll('.patina-verdigris')).toHaveLength(1);
    });

    it("names the brief's own time beside the length", () => {
        render(<BriefAudioPlayer briefId="2026-10-14-morning" audio={audio} caption="Morning brief · 7:42am" />);
        expect(screen.getByTestId("brief-audio-toggle")).toHaveAccessibleName("Hear your brief");
        expect(screen.getByTestId("brief-audio-toggle")).toHaveTextContent("Morning brief · 7:42am · 1:35");
    });

    it("says when the spoken brief cannot be loaded", async () => {
        mocks.getAudio.mockRejectedValue(new Error("no audio"));
        render(<BriefAudioPlayer briefId="2026-10-14-morning" audio={audio} />);
        await act(async () => {
            fireEvent.click(screen.getByTestId("brief-audio-toggle"));
        });
        await waitFor(() => expect(screen.getByRole("alert")).toHaveTextContent("Could not play the spoken brief."));
        expect(play).not.toHaveBeenCalled();
    });

    it("reflects playback shared by two controls for the same brief", async () => {
        render(<><BriefAudioPlayer briefId="latest" audio={audio} /><BriefAudioPlayer briefId="latest" audio={audio} /></>);
        const buttons = screen.getAllByTestId("brief-audio-toggle");
        fireEvent.click(buttons[0]);
        await waitFor(() => buttons.forEach(button => expect(button).toHaveAttribute("aria-pressed", "true")));
        fireEvent.click(buttons[1]);
        await waitFor(() => buttons.forEach(button => expect(button).toHaveAttribute("aria-pressed", "false")));
        expect(mocks.getAudio).toHaveBeenCalledTimes(1);
    });

    it("formats durations as minutes and seconds", () => {
        expect(formatAudioDuration(95)).toEqual({ minutes: "1", seconds: "35" });
        expect(formatAudioDuration(7.4)).toEqual({ minutes: "0", seconds: "07" });
        expect(formatAudioDuration(0)).toBeNull();
        expect(formatAudioDuration(Number.NaN)).toBeNull();
    });
});
