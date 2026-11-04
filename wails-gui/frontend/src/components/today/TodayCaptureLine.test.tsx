import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import type { CaptureCounts } from "@/lib/wails-shim-app";
import { TodayCaptureLine, describeTodayCapture } from "./TodayCaptureLine";
import { TodayStatus } from "./TodayStatus";

function counts(screens: number, minutes: number): CaptureCounts {
    return {
        paused: false,
        notReady: false,
        todayStart: 0,
        weekStart: 0,
        sources: [
            { source: "screens", today: screens, week: screens + 100 },
            { source: "audioMinutes", today: minutes, week: minutes + 10 },
        ],
    };
}

const t = (key: string, options?: Record<string, unknown>) =>
    i18n.t(`today:${key}`, options as never) as unknown as string;
const describe_ = (c: CaptureCounts | null) => describeTodayCapture(c, t, i18n.language);

describe("describeTodayCapture", () => {
    beforeEach(async () => {
        await i18n.changeLanguage("en");
    });

    it("names screens and minutes with speech, with plurals and formatted numbers", () => {
        expect(describe_(counts(412, 38))).toBe("Today Isoform saw 412 screens and heard speech for 38 minutes.");
        expect(describe_(counts(1, 1))).toBe("Today Isoform saw 1 screen and heard speech for 1 minute.");
        expect(describe_(counts(2903, 0))).toBe("Today Isoform saw 2,903 screens.");
        expect(describe_(counts(0, 5))).toBe("Today Isoform heard speech for 5 minutes.");
        // "for 2 hours", not "during 120 minutes" (activity review v1.0.7 AR52).
        expect(describe_(counts(0, 120))).toBe("Today Isoform heard speech for 2 hours.");
        expect(describe_(counts(0, 0))).toBe("Isoform has not captured anything yet today.");
    });

    it("says Paused instead of counts, and nothing while unknown", () => {
        expect(describe_({ paused: true, notReady: false, todayStart: 0, weekStart: 0, sources: null })).toBe(
            "Paused. Isoform is not capturing anything right now.",
        );
        expect(describe_(null)).toBeNull();
        // Not "nothing captured yet" while activity.db is still opening.
        expect(describe_({ paused: false, notReady: true, todayStart: 0, weekStart: 0, sources: null })).toBeNull();
    });

    it("reads in Chinese", async () => {
        await i18n.changeLanguage("zh-CN");
        expect(describe_(counts(412, 38))).toBe("今天 Isoform 看到了 412 个屏幕画面，听到了 38 分钟的语音。");
    });
});

describe("TodayCaptureLine", () => {
    beforeEach(async () => {
        await i18n.changeLanguage("en");
    });

    it("links to the card in Settings", () => {
        const onOpen = vi.fn();
        render(<TodayCaptureLine text="Today Isoform saw 412 screens." onOpen={onOpen} />);

        const line = screen.getByRole("button", { name: "Today Isoform saw 412 screens." });
        expect(line).toHaveAttribute("title", "See what Isoform captured");
        fireEvent.click(line);
        expect(onOpen).toHaveBeenCalledTimes(1);
    });

    it("is plain text without a link, and keeps the Today status box when it is the only line", () => {
        render(<TodayStatus brief={null} capture={{ text: "Isoform has not captured anything yet today." }} />);
        expect(screen.getByTestId("today-status")).toHaveTextContent("Isoform has not captured anything yet today.");
        expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });
});
