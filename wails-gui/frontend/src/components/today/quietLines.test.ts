import { beforeEach, describe, expect, it } from "vitest";
import { computerClock } from "@/lib/brief-time";
import i18n from "@/i18n";
import type { QuietLine } from "@/lib/wails-shim-app";
import { describeQuietLines } from "./quietLines";

const at = (hour: number, minute: number) => new Date(2026, 9, 21, hour, minute).toISOString();
const line = (overrides: Partial<QuietLine>): QuietLine => ({ kind: "capture_paused", firstAt: at(8, 40), lastAt: at(8, 55), count: 0, intervals: [], ...overrides });

function describe_(lines: QuietLine[], language: string) {
    const tr = (key: string, options?: Record<string, unknown>) => String(i18n.t(key as never, { ...options, ns: "today", lng: language }));
    const label = (kind: string) => {
        const key = `proactiveNotifications.kinds.${kind}.label`;
        return i18n.exists(key, { ns: "settings", lng: language }) ? String(i18n.t(key as never, { ns: "settings", lng: language })) : null;
    };
    return describeQuietLines(lines, tr, label, language).map((entry) => entry.text);
}

const clock = (hour: number, minute: number, language = "en") => computerClock(new Date(2026, 9, 21, hour, minute), language);

beforeEach(async () => {
    await i18n.changeLanguage("en");
});

describe("describeQuietLines (RC 1 A4)", () => {
    it("names observing's paused stretches, open ones as since", () => {
        expect(describe_([line({ intervals: [{ start: at(8, 40), end: at(8, 55) }, { start: at(13, 5) }] })], "en")).toEqual([
            `Observing paused ${clock(8, 40)} to ${clock(8, 55)}, since ${clock(13, 5)}`,
        ]);
    });

    it("says when a quiet kind spoke once, or how often and when last", () => {
        expect(describe_([
            line({ kind: "meeting_prep", count: 1, intervals: [], lastAt: at(9, 50) }),
            line({ kind: "waiting_on_reply", count: 3, intervals: [], lastAt: at(14, 10) }),
        ], "en")).toEqual([
            `Meeting coming up at ${clock(9, 50)}`,
            `Waiting on your reply: 3 today, last at ${clock(14, 10)}`,
        ]);
    });

    it("leaves out a kind without a known name, never showing a code", () => {
        expect(describe_([line({ kind: "not_a_kind", count: 2 })], "en")).toEqual([]);
    });

    it("joins stretches with the language's own separator", () => {
        const stretches = [{ start: at(8, 40), end: at(8, 55) }, { start: at(13, 5) }];
        expect(describe_([line({ intervals: stretches })], "zh-CN")).toEqual([
            `观察已暂停：${clock(8, 40, "zh-CN")} 至 ${clock(8, 55, "zh-CN")}、自 ${clock(13, 5, "zh-CN")} 起`,
        ]);
    });

    it("reads in Chinese", () => {
        expect(describe_([line({ intervals: [{ start: at(8, 40), end: at(8, 55) }] })], "zh-CN")).toEqual([
            `观察已暂停：${clock(8, 40, "zh-CN")} 至 ${clock(8, 55, "zh-CN")}`,
        ]);
    });
});
